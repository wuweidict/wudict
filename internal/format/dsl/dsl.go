// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package dsl

import (
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"time"

	"github.com/wuweidict/wudict/internal/dict"
	"github.com/wuweidict/wudict/internal/logx"
	"github.com/wuweidict/wudict/internal/resource"
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
	// O8: a prepared DSL reaches its media through the path alone. No Fetcher -
	// DSL keeps nothing inside the .dsl itself, so there is no location to
	// record and the Sources below are the complete answer.
	resource.Register("dsl", resource.Provider{Sources: MediaSources})
}

// Dict is the DSL "direct" backend. DSL has no native index, so Open
// transparently prepares a library folder (<db dir>/<source name>/text.db) on
// first use (SPEC §1); a changed source is detected from the recorded
// size/mtime/hash and re-indexed in place. Resources stay lazy in
// `<name>.files.zip`, the matching `.files` folder, or loose beside the .dsl.
type Dict struct {
	*store.Store
	srcPath string

	resOnce sync.Once
	res     []resource.Source
	resMu   sync.Mutex // guards res against a concurrent Close
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

func (d *Dict) Close() error {
	// Do, not a flag: a Resource call racing Close must find the sources
	// already built and closed, never build a fresh set nothing will close.
	d.resOnce.Do(func() {})
	d.resMu.Lock()
	for _, s := range d.res {
		s.Close()
	}
	d.res = nil
	d.resMu.Unlock()
	return d.Store.Close()
}

// Resource serves from the dictionary's resource containers, in the order
// LingvoDSL itself documents: the `.files.zip` archive first, then the
// `.files` folder, then loose beside the source.
func (d *Dict) Resource(name string) (io.ReadCloser, string, error) {
	for _, src := range d.sources() {
		if rc, err := src.Open(name); err == nil {
			return rc, resource.MIME(name), nil
		}
	}
	return nil, "", dict.ErrNotFound
}

// Resources lists what the containers hold, for media packing. The folder the
// dictionary merely sits in contributes nothing: it holds other dictionaries
// and their assets, and packing them would copy a neighbour's media into this
// dictionary's library folder.
func (d *Dict) Resources() []string {
	var out []string
	seen := map[string]bool{}
	for _, src := range d.sources() {
		for _, n := range src.List() {
			if k := resource.Key(n); k != "" && !seen[k] {
				seen[k] = true
				out = append(out, n)
			}
		}
	}
	sort.Strings(out)
	return out
}

func (d *Dict) sources() []resource.Source {
	d.resOnce.Do(d.loadSources)
	d.resMu.Lock()
	defer d.resMu.Unlock()
	return d.res
}

func (d *Dict) loadSources() {
	res := MediaSources(d.srcPath)
	d.resMu.Lock()
	d.res = res
	d.resMu.Unlock()
}

// MediaSources builds the resource containers of a DSL from its PATH alone -
// no parsing, no headwords, nothing opened but the archives themselves.
//
// A prepared DSL used to reach its images by opening the whole .dsl again
// (registry.go's resource fallback), which for a large one means parsing
// hundreds of megabytes of text to serve a thumbnail. Everything below is
// derived from the file name, so the prepared folder - which records the source
// path - can do it directly. Registered as the format's O8 provider; the method
// above is the same call, so the two can never drift apart.
func MediaSources(srcPath string) []resource.Source {
	// A ".dsl.dz" names its resources after either the compressed file or the
	// ".dsl" inside it; both spellings are in the wild.
	bases := []string{srcPath}
	if strings.EqualFold(filepath.Ext(srcPath), ".dz") {
		bases = append(bases, strings.TrimSuffix(srcPath, filepath.Ext(srcPath)))
	}
	var res []resource.Source
	for _, b := range bases {
		if z, err := resource.OpenZip(b + ".files.zip"); err == nil {
			res = append(res, z)
		}
	}
	for _, b := range bases {
		if dir := b + ".files"; resource.IsDir(dir) {
			res = append(res, resource.NewDir(dir))
		}
	}
	// Last: a file lying loose beside the .dsl. Exact paths only - this
	// folder is not the dictionary's own, so it is never walked or listed.
	return append(res, resource.NewDirExact(filepath.Dir(srcPath)))
}
