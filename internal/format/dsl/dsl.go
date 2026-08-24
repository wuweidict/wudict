// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package dsl

import (
	"archive/zip"
	"fmt"
	"io"
	"mime"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"time"

	"github.com/wuweidict/wudict/internal/dict"
	"github.com/wuweidict/wudict/internal/logx"
	"github.com/wuweidict/wudict/internal/store"
)

func init() {
	openFn := func(path string) (dict.Dictionary, error) { return Open(path) }
	readFn := func(path string) (dict.Reader, error) { return NewReader(path) }
	dict.RegisterFormat(".dsl", openFn)
	dict.RegisterReader(".dsl", readFn)
	// Compressed DSL. Registered as the full ".dsl.dz" suffix (not bare ".dz")
	// so a StarDict ".dict.dz" companion is never matched here; Open/NewReader
	// handle the gunzip. matchKey prefers this longest suffix.
	dict.RegisterFormat(".dsl.dz", openFn)
	dict.RegisterReader(".dsl.dz", readFn)
}

// Dict is the DSL "direct" backend. DSL has no native index, so Open
// transparently prepares a library folder (<db dir>/<source name>/text.db) on
// first use (SPEC §1); a changed source is detected from the recorded
// size/mtime/hash and re-indexed in place. Resources stay lazy in
// `<name>.files.zip` (or loose beside the .dsl).
type Dict struct {
	*store.Store
	srcPath string

	zipOnce  sync.Once
	zipFiles map[string]*zip.File
	zipMu    sync.Mutex
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
		const format = "dsl"
		logx.Status("%spreparing search index (%s, first open)…", logx.Dict(name), format)
		// Headwords only, like every other format's automatic index (D24):
		// dsl has no native index, so it must store its article text to be
		// readable at all - but indexing that text for full-text search is
		// the user's choice, not a toll for opening the file.
		rep, ierr := store.IngestPlan(r, dbPath, store.Plan{}, func(done, total int) {
			logx.Progress("  %d entries", done)
		})
		r.Close()
		if ierr != nil {
			logx.ClearLine()
			return nil, fmt.Errorf("preparing %q: %w", name, ierr)
		}
		store.ReportPrepared(name, rep, time.Since(start))
	} else {
		r.Close()
	}

	s, err := store.Open(dbPath)
	if err != nil {
		return nil, err
	}
	return &Dict{Store: s, srcPath: path}, nil
}

func (d *Dict) Meta() dict.Meta {
	m := d.Store.Meta()
	m.Format = "dsl"
	m.Path = d.srcPath
	return m
}

func (d *Dict) Close() error { return d.Store.Close() }

// Resource serves from `<dsl-path>.files.zip` (also `<base>.files.zip`
// for .dsl.dz), else a loose file beside the source.
func (d *Dict) Resource(name string) (io.ReadCloser, string, error) {
	norm := strings.TrimLeft(path.Clean(name), "/")
	if norm == "" || norm == "." || strings.HasPrefix(norm, "..") {
		return nil, "", dict.ErrNotFound
	}
	d.zipOnce.Do(d.loadZip)
	if d.zipFiles != nil {
		if zf, ok := d.zipFiles[strings.ToLower(norm)]; ok {
			d.zipMu.Lock()
			rc, err := zf.Open()
			d.zipMu.Unlock()
			if err != nil {
				return nil, "", err
			}
			return rc, mime.TypeByExtension(path.Ext(norm)), nil
		}
	}
	if f, err := os.Open(filepath.Join(filepath.Dir(d.srcPath), filepath.FromSlash(norm))); err == nil {
		return f, mime.TypeByExtension(path.Ext(norm)), nil
	}
	return nil, "", dict.ErrNotFound
}

// Resources lists the .files.zip entries.
func (d *Dict) Resources() []string {
	d.zipOnce.Do(d.loadZip)
	out := make([]string, 0, len(d.zipFiles))
	for name := range d.zipFiles {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func (d *Dict) loadZip() {
	candidates := []string{d.srcPath + ".files.zip"}
	if strings.HasSuffix(strings.ToLower(d.srcPath), ".dz") {
		candidates = append(candidates, strings.TrimSuffix(d.srcPath, filepath.Ext(d.srcPath))+".files.zip")
	}
	for _, p := range candidates {
		zr, err := zip.OpenReader(p)
		if err != nil {
			continue
		}
		d.zipFiles = make(map[string]*zip.File, len(zr.File))
		for _, f := range zr.File {
			d.zipFiles[strings.ToLower(f.Name)] = f
		}
		return
	}
}
