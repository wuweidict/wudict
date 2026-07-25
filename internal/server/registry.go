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
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/glowinthedark/gonow-dict/internal/dict"
	"github.com/glowinthedark/gonow-dict/internal/logx"
	"github.com/glowinthedark/gonow-dict/internal/store"
)

// upgraded serves queries from an ingested text.db while resolving
// resources media.db → original source (D2 resolution order).
type upgraded struct {
	*store.Store
	src   dict.Dictionary
	media *store.Media
}

func (u *upgraded) Meta() dict.Meta {
	m := u.src.Meta()
	m.EntryCount = u.Store.Meta().EntryCount
	return m
}

func (u *upgraded) Caps() dict.Caps { return u.Store.Caps() }

func (u *upgraded) Resource(name string) (io.ReadCloser, string, error) {
	if u.media != nil {
		if rc, mime, err := u.media.Resource(name); err == nil {
			return rc, mime, nil
		}
	}
	return u.src.Resource(name)
}

func (u *upgraded) Close() error {
	if u.media != nil {
		u.media.Close()
	}
	u.src.Close()
	return u.Store.Close()
}

// entry is one discovered dictionary, opened lazily.
type entry struct {
	ID   string
	Path string

	once sync.Once
	dMu  sync.RWMutex
	d    dict.Dictionary
	err  error

	ingestMu sync.Mutex // one ingest at a time per dictionary
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
			logx.V("open %s [%s] %d entries fuzzy=%v (%s)",
				m.Name, m.Format, m.EntryCount, d.Caps().Fuzzy, time.Since(start).Round(time.Millisecond))
		}
	})
	e.dMu.RLock()
	defer e.dMu.RUnlock()
	return e.d, e.err
}

func openUpgradedOrDirect(path string) (dict.Dictionary, error) {
	src, err := dict.Open(path)
	if err != nil {
		return nil, err
	}
	if src.Caps().FTS { // e.g. DSL auto-ingest: already store-backed
		return src, nil
	}
	base := store.CacheBase(path, src.Meta().Name)
	s, err := store.Open(base + ".text.db")
	if err != nil {
		return src, nil // no cache yet: plain direct backend
	}
	u := &upgraded{Store: s, src: src}
	if m, err := store.OpenMedia(base + ".media.db"); err == nil {
		u.media = m
	}
	return u, nil
}

// Registry tracks all dictionaries under the dict dir.
type Registry struct {
	dictDir string

	mu      sync.RWMutex
	entries []*entry
	byID    map[string]*entry
}

func NewRegistry(dictDir string) (*Registry, error) {
	r := &Registry{dictDir: dictDir, byID: map[string]*entry{}}
	return r, r.Rescan()
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
	return r.Rescan()
}

// Rescan re-discovers dictionaries, keeping already-open entries.
func (r *Registry) Rescan() error {
	r.mu.RLock()
	dir := r.dictDir
	r.mu.RUnlock()
	paths, err := dict.Discover(dir)
	if err != nil {
		return err
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
	return nil
}

// pathID derives a stable slash-free dictionary id from its path.
func pathID(path string) string {
	sum := sha256.Sum256([]byte(path))
	return hex.EncodeToString(sum[:])[:12]
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
	base := store.CacheBase(e.Path, name)
	textDB := base + ".text.db"

	if _, err := os.Stat(textDB); err == nil && level == store.LevelText {
		if cl, _ := store.ReadMetaValue(textDB, "ingest_level"); cl == string(store.LevelHeadwords) {
			logx.V("ingest %s: upgrading headwords-only db to full text", name)
			_ = os.Remove(textDB)
		}
	}
	if _, err := os.Stat(textDB); err != nil {
		rd, err := dict.OpenReader(e.Path)
		if err != nil {
			return err
		}
		err = store.IngestLevel(rd, textDB, level, progress)
		rd.Close()
		if err != nil {
			return err
		}
	}

	if full {
		mediaDB := base + ".media.db"
		if _, err := os.Stat(mediaDB); err != nil {
			src := cur
			if u, ok := cur.(*upgraded); ok {
				src = u.src
			}
			lister, ok := src.(dict.ResourceLister)
			if !ok {
				return fmt.Errorf("%s: format cannot enumerate resources", name)
			}
			names := lister.Resources()
			if len(names) > 0 {
				uuid, err := store.ReadMetaValue(textDB, "dict_uuid")
				if err != nil {
					return err
				}
				if err := store.IngestMedia(src, names, mediaDB, uuid, progress); err != nil {
					return err
				}
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
