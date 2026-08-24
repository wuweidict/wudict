// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package bgl

import (
	"bytes"
	"fmt"
	"io"
	"mime"
	"path"
	"sort"
	"strings"
	"sync"

	"time"

	"github.com/wuweidict/wudict/internal/dict"
	"github.com/wuweidict/wudict/internal/logx"
	"github.com/wuweidict/wudict/internal/store"
)

func init() {
	dict.RegisterFormat(".bgl", func(path string) (dict.Dictionary, error) { return Open(path) })
	dict.RegisterReader(".bgl", func(path string) (dict.Reader, error) { return NewReader(path) })
}

// Dict is the BGL "direct" backend. BGL has no native index, so Open ingests
// into a cached text.db on first use (SPEC §1); the cache name embeds a
// source-content hash, so a changed source re-ingests automatically. Embedded
// resources are scanned from the source lazily on first request.
type Dict struct {
	*store.Store
	srcPath string

	resOnce sync.Once
	res     map[string][]byte
	resList []string
	resErr  error
}

func Open(path string) (*Dict, error) {
	r, err := NewReader(path)
	if err != nil {
		return nil, err
	}
	name := r.Meta().Name

	dbPath, prepared := store.PreparedFor(path)
	if !prepared {
		dbPath, err = store.PrepareTarget(path)
		if err != nil {
			r.Close()
			return nil, err
		}
		start := time.Now()
		const format = "bgl"
		logx.Status("%spreparing search index (%s, first open)…", logx.Dict(name), format)
		// Headwords only, like every other format's automatic index (D24):
		// bgl has no native index, so it must store its article text to be
		// readable at all - but indexing that text for full-text search is
		// the user's choice, not a toll for opening the file.
		rep, ierr := store.IngestPlan(r, dbPath, store.Plan{}, func(done, _ int) {
			logx.Progress("  %d entries", done)
		})
		if ierr != nil {
			logx.ClearLine()
			r.Close()
			return nil, fmt.Errorf("preparing %q: %w", name, ierr)
		}
		store.ReportPrepared(name, rep, time.Since(start))
	}
	r.Close()

	s, err := store.Open(dbPath)
	if err != nil {
		return nil, err
	}
	return &Dict{Store: s, srcPath: path}, nil
}

func (d *Dict) Meta() dict.Meta {
	m := d.Store.Meta()
	m.Format = "bgl"
	m.Path = d.srcPath
	return m
}

func (d *Dict) Close() error { return d.Store.Close() }

// loadRes scans the source BGL for its embedded resource blocks. Kept lazy so
// a dictionary that never serves an image never pays the decompression.
func (d *Dict) loadRes() {
	d.res, d.resList, d.resErr = scanResources(d.srcPath)
}

// Resource streams one embedded resource (image/HTML) by name,
// case-insensitively.
func (d *Dict) Resource(name string) (io.ReadCloser, string, error) {
	norm := strings.ToLower(strings.TrimLeft(path.Clean(name), "/"))
	if norm == "" || norm == "." || strings.HasPrefix(norm, "..") {
		return nil, "", dict.ErrNotFound
	}
	d.resOnce.Do(d.loadRes)
	if d.resErr != nil {
		return nil, "", d.resErr
	}
	if b, ok := d.res[norm]; ok {
		return io.NopCloser(bytes.NewReader(b)), mime.TypeByExtension(path.Ext(norm)), nil
	}
	return nil, "", dict.ErrNotFound
}

// Resources lists the embedded resource names (for full-ingest media packing).
func (d *Dict) Resources() []string {
	d.resOnce.Do(d.loadRes)
	out := append([]string(nil), d.resList...)
	sort.Strings(out)
	return out
}
