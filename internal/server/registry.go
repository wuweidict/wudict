// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Package server is the persistent HTTP server (D3): dictionary
// registry, JSON API, resource streaming, and the "Enable fuzzy &
// full-text search" ingest flow with SSE progress (SPEC §6).
package server

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/glowinthedark/gonow-dict/internal/dict"
	"github.com/glowinthedark/gonow-dict/internal/logx"
	"github.com/glowinthedark/gonow-dict/internal/store"
)

// upgraded serves queries from an ingested text.db while resolving
// resources media.db → original source (D2 resolution order). The direct
// source backend (`src`) is opened lazily — only when a resource actually
// has to fall back to it — so opening an ingested dictionary costs just a
// cheap SQLite open instead of decompressing every key block and building
// fold-maps for a backend we would only use for resource fallback.
type upgraded struct {
	*store.Store
	srcPath string

	srcOnce sync.Once
	src     dict.Dictionary
	srcErr  error
}

// source lazily opens the direct backend for resource fallback.
func (u *upgraded) source() (dict.Dictionary, error) {
	u.srcOnce.Do(func() {
		if u.src == nil && u.srcErr == nil {
			u.src, u.srcErr = dict.Open(u.srcPath)
		}
	})
	return u.src, u.srcErr
}

func (u *upgraded) Meta() dict.Meta {
	// derived entirely from the text.db meta (name/description/entry_count
	// were captured at ingest) so no direct open is needed for the list.
	m := u.Store.Meta()
	m.Format = strings.TrimPrefix(m.Format, "gonow:")
	m.Path = u.srcPath
	return m
}

func (u *upgraded) Caps() dict.Caps { return u.Store.Caps() }

func (u *upgraded) Resource(name string) (io.ReadCloser, string, error) {
	// the embedded Store serves from its auto-attached sibling media.db
	// (D2/D9); only fall back to the original source when that misses.
	if rc, mime, err := u.Store.Resource(name); err == nil {
		return rc, mime, nil
	}
	src, err := u.source()
	if err != nil {
		return nil, "", err
	}
	return src.Resource(name)
}

func (u *upgraded) Close() error {
	if u.src != nil {
		u.src.Close()
	}
	return u.Store.Close() // Store.Close also closes its attached media.db
}

// entry is one discovered dictionary, opened lazily.
type entry struct {
	ID   string
	Path string

	once sync.Once
	dMu  sync.RWMutex
	d    dict.Dictionary
	err  error

	mediaEmpty bool // a full ingest found no packable resources (dMu-guarded)

	ingestMu sync.Mutex // one ingest at a time per dictionary
	autoOnce sync.Once  // first-search auto-index, attempted once
}

// noPackableMedia reports whether a prior full ingest found nothing to pack,
// so the panel can stop offering "pack media".
func (e *entry) noPackableMedia() bool {
	e.dMu.RLock()
	defer e.dMu.RUnlock()
	return e.mediaEmpty
}

// maybeAutoIndex builds a fuzzy (headwords-level) index for this dictionary
// in the background the first time it is searched, unless it already has
// one. Attempted at most once per process; failures (e.g. read-only cache,
// no ingest reader for the format) are swallowed — auto-indexing is a
// silent convenience, never a hard requirement.
func (e *entry) maybeAutoIndex() {
	e.autoOnce.Do(func() {
		go func() {
			d, err := e.open()
			if err != nil || d.Caps().Contains {
				return // unopenable, or already contains-capable (ingested/DSL/gonow)
			}
			if err := e.ingest(false, store.LevelHeadwords, nil); err != nil {
				logx.V("auto-index %s: %v", e.Path, err)
			} else {
				logx.V("auto-index %s: fuzzy index ready", e.Path)
			}
		}()
	})
}

// open opens the source backend and, when a cached text.db (and
// media.db) exists for it, wraps it into the upgraded view.
func (e *entry) open() (dict.Dictionary, error) {
	e.once.Do(func() {
		start := time.Now()
		d, err := openUpgradedOrDirect(e.Path)
		e.dMu.Lock()
		e.d, e.err = d, err
		e.dMu.Unlock()
		if err != nil {
			logx.V("open %s: FAILED: %v", e.Path, err)
		} else {
			m := d.Meta()
			logx.V("open %s [%s] %d entries contains=%v (%s)",
				m.Name, m.Format, m.EntryCount, d.Caps().Contains, time.Since(start).Round(time.Millisecond))
		}
	})
	e.dMu.RLock()
	defer e.dMu.RUnlock()
	return e.d, e.err
}

// native is a standalone naturalized dictionary: a .text.db whose foreign
// source is gone (the db dir is the native dictionary root). It presents like
// `upgraded` — the internal gonow: format prefix stripped, Path set to the db
// file — but has no source to fall back to, so resources come only from its
// attached media.db when present.
type native struct {
	*store.Store
	path string
}

func (n *native) Meta() dict.Meta {
	m := n.Store.Meta()
	m.Format = strings.TrimPrefix(m.Format, "gonow:")
	m.Path = n.path
	return m
}

// openUpgradedOrDirect resolves the best backend for a source file. When a
// cached text.db exists it is opened alone (cheap) with the direct source
// kept lazy; the source name is obtained via a header-only Probe so the
// heavy direct backend is never built just to locate the cache.
func openUpgradedOrDirect(path string) (dict.Dictionary, error) {
	// A prepared dictionary (library folder text.db, or a loose .text.db) is
	// opened directly: self-describing (name/format/uuid in its meta) and
	// auto-attaching its media.db. When the source it was built from is still
	// on disk it also serves as the resource fallback (D2 order).
	if store.IsTextDB(path) {
		s, err := store.Open(path)
		if err != nil {
			return nil, err
		}
		if src := s.SourcePath(); src != "" && fileExists(src) {
			return &upgraded{Store: s, srcPath: src}, nil
		}
		return &native{Store: s, path: path}, nil
	}
	// a prepared folder for this source short-circuits the heavy direct
	// backend entirely — no probe needed, since the folder is located from
	// the source path, not from the dictionary name.
	if textDB, ok := store.PreparedFor(path); ok {
		if s, err := store.Open(textDB); err == nil {
			return &upgraded{Store: s, srcPath: path}, nil
		}
	}
	// not prepared (or the source changed since): open the direct backend.
	src, err := dict.Open(path)
	if err != nil {
		return nil, err
	}
	return src, nil
}

// Registry tracks all dictionaries: the foreign-format sources found under the
// dictionary folder and — only when the user has opted in (USE_CACHED) — the
// prepared dictionaries in the library (the db dir).
//
// The library is NOT a discovery root by default. It is the app's private
// working area, and treating it as a dictionary folder is what let a media.db
// masquerade as a dictionary and kept the first-run setup page hidden behind a
// non-empty registry. Opting in is a deliberate, remembered choice made on the
// setup page ("Use these dictionaries").
type Registry struct {
	dictDir string // dictionary folder: .mdx/.slob/.ifo/.dsl/.bgl sources

	mu        sync.RWMutex
	useCached bool // include prepared dictionaries from the library (USE_CACHED)
	entries   []*entry
	byID      map[string]*entry
	fromLib   int // how many entries came from the library, not the dict folder
}

func NewRegistry(dictDir string, useCached bool) (*Registry, error) {
	r := &Registry{dictDir: dictDir, useCached: useCached, byID: map[string]*entry{}}
	if err := r.Rescan(); err != nil {
		return r, err
	}
	r.Warm()
	return r, nil
}

// UseCached reports whether prepared dictionaries are included.
func (r *Registry) UseCached() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.useCached
}

// SetUseCached opts the library in or out and rescans (setup flow — no
// restart needed).
func (r *Registry) SetUseCached(on bool) error {
	r.mu.Lock()
	r.useCached = on
	r.mu.Unlock()
	if err := r.Rescan(); err != nil {
		return err
	}
	r.Warm()
	return nil
}

// Dir returns the current dictionary directory.
func (r *Registry) Dir() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.dictDir
}

// Count returns the number of discovered dictionaries.
func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.entries)
}

// SetDir re-points the registry at a new dictionary directory and
// rescans (used by the first-run setup flow — no restart needed).
func (r *Registry) SetDir(dir string) error {
	r.mu.Lock()
	r.dictDir = dir
	r.mu.Unlock()
	if err := r.Rescan(); err != nil {
		return err
	}
	r.Warm()
	return nil
}

// Rescan re-discovers dictionaries, keeping already-open entries.
func (r *Registry) Rescan() error {
	r.mu.RLock()
	dir := r.dictDir
	useCached := r.useCached
	r.mu.RUnlock()
	paths, err := dict.Discover(dir)
	if err != nil {
		return err
	}
	fromLib := 0
	if useCached {
		lib := libraryPaths(paths)
		fromLib = len(lib)
		paths = append(paths, lib...)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	seen := map[string]bool{}
	var entries []*entry
	for _, p := range paths {
		id := pathID(p)
		if seen[id] {
			continue
		}
		seen[id] = true
		if old, ok := r.byID[id]; ok {
			entries = append(entries, old)
			continue
		}
		e := &entry{ID: id, Path: p}
		r.byID[id] = e
		entries = append(entries, e)
	}
	sort.Slice(entries, func(i, j int) bool {
		return strings.ToLower(entries[i].Path) < strings.ToLower(entries[j].Path)
	})
	r.entries = entries
	r.fromLib = fromLib
	return nil
}

// Counts reports how many dictionaries came from the dictionary folder and
// how many from the library, so the startup summary can describe each folder
// by what it actually contributed instead of showing one blended total.
func (r *Registry) Counts() (fromFolder, fromLibrary int) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.entries) - r.fromLib, r.fromLib
}

// libraryPaths returns the text.db of every prepared dictionary that is not
// already represented by a discovered source file. The dedup is by source
// path, not by "is the source still on disk": a dictionary whose source lives
// outside the current dictionary folder is unrepresented and therefore listed,
// which is the whole point of opting the library in.
func libraryPaths(discovered []string) []string {
	lib, err := store.Library()
	if err != nil {
		logx.V("library scan: %v", err)
		return nil
	}
	seen := make(map[string]bool, len(discovered))
	for _, p := range discovered {
		if abs, err := filepath.Abs(p); err == nil {
			seen[filepath.Clean(abs)] = true
		}
	}
	var out []string
	for _, e := range lib {
		if e.Source != "" {
			if abs, err := filepath.Abs(e.Source); err == nil && seen[filepath.Clean(abs)] {
				continue // its source is in the dictionary folder: same dictionary
			}
		}
		out = append(out, e.TextDB)
	}
	return out
}

// pathID derives a stable slash-free dictionary id from its path.
func pathID(path string) string {
	sum := sha256.Sum256([]byte(path))
	return hex.EncodeToString(sum[:])[:12]
}

// Warm opens every discovered dictionary in the background (bounded
// concurrency) so the first search does not pay the open cost on the
// request path. Opens are memoized (sync.Once); ingested dictionaries
// open cheaply, only non-ingested direct backends do real work here.
func (r *Registry) Warm() {
	entries := r.all()
	go func() {
		sem := make(chan struct{}, 4)
		var wg sync.WaitGroup
		for _, e := range entries {
			wg.Add(1)
			sem <- struct{}{}
			go func(e *entry) {
				defer wg.Done()
				defer func() { <-sem }()
				_, _ = e.open()
			}(e)
		}
		wg.Wait()
	}()
}

func (r *Registry) all() []*entry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]*entry(nil), r.entries...)
}

func (r *Registry) get(id string) (*entry, error) {
	r.mu.RLock()
	e, ok := r.byID[id]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("unknown dictionary id %q", id)
	}
	return e, nil
}

// ingest builds the text.db (and media.db when full) for one entry and
// swaps its open view to the upgraded backend. A headwords-only db is
// deleted and rebuilt when full-text level is requested later.
func (e *entry) ingest(full bool, level store.Level, progress store.Progress) error {
	e.ingestMu.Lock()
	defer e.ingestMu.Unlock()

	cur, err := e.open()
	if err != nil {
		return err
	}
	name := cur.Meta().Name
	if store.IsTextDB(e.Path) {
		// already a prepared dictionary: nothing to (re)build from, since its
		// source is what an ingest would read.
		return fmt.Errorf("%s is already a prepared dictionary", name)
	}
	// claim (or re-use) this source's library folder: <db dir>/<source name>/
	dir, err := store.ClaimDir(e.Path)
	if err != nil {
		return err
	}
	textDB := store.TextDBPath(dir)

	// decide whether the text.db needs (re)building. Never delete the existing
	// one first: IngestLevel writes a temp and renames over it atomically, so
	// an interrupted upgrade leaves the old index intact instead of destroying
	// it (a stopped "enable all" must not corrupt a dictionary).
	needIngest := false
	switch {
	case !fileExists(textDB):
		needIngest = true // missing
	case store.SourceChanged(textDB, e.Path):
		logx.V("ingest %s: source changed since it was prepared — re-indexing", name)
		needIngest = true
	case level == store.LevelText:
		if cl, _ := store.ReadMetaValue(textDB, "ingest_level"); cl == string(store.LevelHeadwords) {
			logx.V("ingest %s: upgrading headwords-only db to full text", name)
			needIngest = true
		}
	}
	if needIngest {
		rd, err := dict.OpenReader(e.Path)
		if err != nil {
			return err
		}
		rep, ierr := store.IngestLevelReport(rd, textDB, level, progress)
		rd.Close()
		if ierr != nil {
			return fmt.Errorf("preparing %q: %w", name, ierr)
		}
		logx.V("%s%d entries indexed (%s)", logx.Dict(name), rep.Entries, level)
		if rep.UnresolvedLinks > 0 {
			logx.V("%s%d redirects pointed at headwords not present in the source (skipped)",
				logx.Dict(name), rep.UnresolvedLinks)
		}
	}

	if full {
		mediaDB := store.MediaDBPath(dir)
		if _, err := os.Stat(mediaDB); err != nil {
			src := cur
			if u, ok := cur.(*upgraded); ok {
				s, err := u.source() // lazily open the direct backend for resources
				if err != nil {
					return err
				}
				src = s
			}
			lister, _ := src.(dict.ResourceLister)
			var names []string
			if lister != nil {
				names = lister.Resources()
			}
			if len(names) > 0 {
				uuid, err := store.ReadMetaValue(textDB, "dict_uuid")
				if err != nil {
					return err
				}
				if err := store.IngestMedia(src, names, mediaDB, uuid, progress); err != nil {
					return err
				}
			} else {
				// nothing to pack (text-only dict, or format has no resources):
				// remember it so the panel stops offering "pack media".
				e.dMu.Lock()
				e.mediaEmpty = true
				e.dMu.Unlock()
			}
		}
	}

	// swap in the upgraded view (old handle stays open for in-flight
	// requests; it is garbage collected once idle)
	fresh, err := openUpgradedOrDirect(e.Path)
	if err != nil {
		return err
	}
	e.dMu.Lock()
	e.d, e.err = fresh, nil
	e.dMu.Unlock()
	return nil
}
