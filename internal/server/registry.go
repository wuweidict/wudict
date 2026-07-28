// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Package server is the persistent HTTP server (D3): dictionary
// registry, JSON API, resource streaming, and the per-dictionary feature
// flow (contains / full-text / media) with SSE progress (SPEC §6, D24).
package server

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
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

	// The source handle is opened only when a resource misses media.db — but
	// it is a full direct backend, so it holds the same ~350 bytes per headword
	// as any preview (docs/PERF.md §3.1). It is therefore evictable on the same
	// terms: no sync.Once, a recorded weight and last-use, and a release path.
	srcMu  sync.Mutex
	src    dict.Dictionary
	srcErr error
	srcW   atomic.Int64
	srcUse atomic.Int64
}

// source lazily opens the direct backend for resource fallback.
func (u *upgraded) source() (dict.Dictionary, error) {
	u.srcUse.Store(time.Now().UnixNano())
	u.srcMu.Lock()
	defer u.srcMu.Unlock()
	if u.src != nil || u.srcErr != nil {
		return u.src, u.srcErr
	}
	u.src, u.srcErr = dict.Open(u.srcPath)
	if u.src != nil {
		u.srcW.Store(previewWeight(u.src, u.src.Meta()))
	}
	return u.src, u.srcErr
}

// srcWeight, srcLastUse and releaseSource let the registry's janitor treat this
// handle exactly like any other preview backend.
func (u *upgraded) srcWeight() int64  { return u.srcW.Load() }
func (u *upgraded) srcLastUse() int64 { return u.srcUse.Load() }

// releaseSource closes the resource-fallback handle. The dictionary keeps
// working — text comes from SQLite — and the handle reopens if another
// resource misses.
func (u *upgraded) releaseSource() int64 {
	u.srcMu.Lock()
	d, w := u.src, u.srcW.Load()
	if d == nil || w == 0 {
		u.srcMu.Unlock()
		return 0
	}
	u.src, u.srcErr = nil, nil
	u.srcW.Store(0)
	u.srcMu.Unlock()
	time.AfterFunc(closeGrace, func() {
		d.Close()
		debug.FreeOSMemory()
	})
	logx.V("released resource handle for %s (~%d MB)", filepath.Base(u.srcPath), w>>20)
	return w
}

// evictableSource is a view that holds a releasable direct backend alongside
// its prepared database.
type evictableSource interface {
	srcWeight() int64
	srcLastUse() int64
	releaseSource() int64
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
	u.srcUse.Store(time.Now().UnixNano())
	return src.Resource(name)
}

func (u *upgraded) Close() error {
	u.srcMu.Lock()
	src := u.src
	u.src = nil
	u.srcMu.Unlock()
	if src != nil {
		src.Close()
	}
	return u.Store.Close() // Store.Close also closes its attached media.db
}

// Preparing a dictionary costs a saturated core and a few hundred bytes of RAM
// per headword (docs/PERF.md §3). Nothing bounded that: `maybeAutoIndex` fired
// one goroutine per dictionary, so a single "all dictionaries" search over a
// 100-dictionary library started 100 concurrent ingests — measured at 500 MB
// and 424 % CPU for FOUR dictionaries, extrapolating to the reported 18 GB and
// 1000 % CPU for the full corpus.
//
// Background indexing now flows through indexLimit (INDEX_WORKERS, default 1).
// Work the user asked for explicitly gets its own single slot so a click never
// waits behind a long background job; worst case is INDEX_WORKERS + 1.
var (
	indexLimit = make(chan struct{}, 1)
	frontLimit = make(chan struct{}, 1)
)

// SetIndexWorkers sizes the background indexing limiter (config INDEX_WORKERS).
func SetIndexWorkers(n int) {
	if n < 1 {
		n = 1
	}
	indexLimit = make(chan struct{}, n)
}

// acquire blocks until a slot is free, honouring cancellation.
func acquire(sem chan struct{}) { sem <- struct{}{} }
func release(sem chan struct{}) { <-sem }

// entry is one discovered dictionary, opened lazily.
type entry struct {
	ID   string
	Path string

	openMu sync.Mutex // serialises opening; NOT sync.Once — an evicted
	// backend must be openable again
	dMu sync.RWMutex
	d   dict.Dictionary
	err error

	lastUse atomic.Int64 // unix nanos, for LRU eviction
	weight  atomic.Int64 // estimated bytes held by a preview backend (0 if cheap)

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

// maybeAutoIndex prepares this dictionary's headword index in the background
// the first time it is searched, unless it already has one. Attempted at most
// once per process; failures (e.g. a read-only library, no ingest reader for
// the format) are swallowed — auto-indexing is a silent convenience, never a
// hard requirement.
func (e *entry) maybeAutoIndex() {
	e.autoOnce.Do(func() {
		go func() {
			acquire(indexLimit) // at most INDEX_WORKERS of these run at once
			defer release(indexLimit)
			// ensureBaseIndex is a no-op when this dictionary is already
			// prepared, at whatever level its owner chose
			if err := e.ensureBaseIndex(nil); err != nil {
				logx.V("auto-index %s: %v", e.Path, err)
			} else {
				logx.V("auto-index %s: index ready", e.Path)
			}
		}()
	})
}

// open opens the source backend and, when a cached text.db (and
// media.db) exists for it, wraps it into the upgraded view.
func (e *entry) open() (dict.Dictionary, error) {
	e.lastUse.Store(time.Now().UnixNano())
	e.dMu.RLock()
	d, err := e.d, e.err
	e.dMu.RUnlock()
	if d != nil || err != nil {
		return d, err
	}
	// Not sync.Once: a preview backend can be evicted to reclaim its headword
	// map, and must then be openable again. Double-checked under openMu so a
	// burst of concurrent searches still opens the file only once.
	e.openMu.Lock()
	defer e.openMu.Unlock()
	e.dMu.RLock()
	d, err = e.d, e.err
	e.dMu.RUnlock()
	if d != nil || err != nil {
		return d, err
	}
	start := time.Now()
	d, err = openUpgradedOrDirect(e.Path)
	e.dMu.Lock()
	e.d, e.err = d, err
	e.dMu.Unlock()
	if err != nil {
		logx.V("open %s: FAILED: %v", e.Path, err)
		return d, err
	}
	m := d.Meta()
	e.weight.Store(previewWeight(d, m))
	logx.V("open %s [%s] %d entries contains=%v (%s)",
		m.Name, m.Format, m.EntryCount, d.Caps().Contains, time.Since(start).Round(time.Millisecond))
	return d, err
}

// previewWeight estimates the resident cost of an open backend. A PREPARED
// dictionary answers from SQLite and costs a few MB whatever its size, so it
// weighs nothing here. A direct ("preview", D15) backend builds an in-memory
// headword index on first use — measured at 300–500 bytes per headword across
// MDX and SLOB (docs/PERF.md §3.1) — and that is what eviction reclaims.
func previewWeight(d dict.Dictionary, m dict.Meta) int64 {
	if _, ok := d.(storeBacked); ok {
		return 0 // answers from SQLite: the index lives on disk, not in RAM
	}
	if m.EntryCount <= 0 {
		return 0
	}
	return int64(m.EntryCount) * previewBytesPerEntry
}

// storeBacked matches anything answering from a prepared database — the
// `upgraded`/`native` wrappers, and the formats that embed a *store.Store
// directly because they have no native index (DSL, BGL). Type-switching on the
// wrappers alone missed those, and a self-prepared dictionary would have been
// counted as if it held a headword map in RAM.
type storeBacked interface{ SourcePath() string }

// previewBytesPerEntry is the measured per-headword cost of a direct backend's
// in-memory index (docs/PERF.md §3.1: 290–570 B across formats; 350 is the
// middle of the measured range and errs neither way).
const previewBytesPerEntry = 350

// evict drops this entry's open backend so its memory can be reclaimed. It
// refuses while the dictionary is being prepared, and closes after a grace so
// requests already reading from it finish. The next open reopens the file.
func (e *entry) evict() int64 {
	if !e.ingestMu.TryLock() {
		return 0 // being prepared right now: leave it alone
	}
	defer e.ingestMu.Unlock()
	e.dMu.Lock()
	d := e.d
	w := e.weight.Load()
	if d == nil || w == 0 {
		e.dMu.Unlock()
		return 0
	}
	e.d, e.err = nil, nil
	e.weight.Store(0)
	e.dMu.Unlock()
	time.AfterFunc(closeGrace, func() {
		d.Close()
		debug.FreeOSMemory()
	})
	logx.V("evicted preview backend %s (~%d MB)", filepath.Base(e.Path), w>>20)
	return w
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
	mu        sync.RWMutex
	dictDirs  []string // dictionary folders: .mdx/.slob/.ifo/.dsl/.bgl sources
	useCached bool     // include prepared dictionaries from the library (USE_CACHED)
	entries   []*entry
	byID      map[string]*entry
	fromLib   int    // how many entries came from the library, not a dict folder
	roots     []Root // per-folder status, for the startup summary and setup page

	// previewBudget caps the memory unprepared dictionaries may hold open
	// (PREVIEW_MEMORY; 0 = unlimited). Prepared ones answer from disk and are
	// never evicted — there would be nothing to reclaim.
	previewBudget int64
}

// Root is one dictionary folder and what it contributed. A folder that is
// missing (an unmounted drive, a deleted path) is reported, never fatal: the
// other folders must keep working.
type Root struct {
	Path   string `json:"path"`
	Count  int    `json:"count"` // dictionaries this folder was the first to offer
	Total  int    `json:"total"` // everything it holds (Total > Count ⇒ overlap)
	Exists bool   `json:"exists"`
}

func NewRegistry(dictDirs []string, useCached bool) (*Registry, error) {
	r := &Registry{dictDirs: dict.DedupeDirs(dictDirs), useCached: useCached, byID: map[string]*entry{}}
	if err := r.Rescan(); err != nil {
		return r, err
	}
	r.Warm()
	r.startJanitor()
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

// Dirs returns the current dictionary folders.
func (r *Registry) Dirs() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]string(nil), r.dictDirs...)
}

// Roots reports each dictionary folder with its status and contribution.
func (r *Registry) Roots() []Root {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]Root(nil), r.roots...)
}

// Count returns the number of discovered dictionaries.
func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.entries)
}

// SetDirs re-points the registry at new dictionary folders and rescans
// (used by the first-run setup flow — no restart needed).
func (r *Registry) SetDirs(dirs []string) error {
	r.mu.Lock()
	// one folder listed twice (a repeat, a trailing slash, a symlink) must not
	// become two rows, two walks and two lines in config.toml
	r.dictDirs = dict.DedupeDirs(dirs)
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
	dirs := append([]string(nil), r.dictDirs...)
	useCached := r.useCached
	r.mu.RUnlock()
	paths, perRoot, err := dict.DiscoverAll(dirs)
	if err != nil {
		logx.V("scanning dictionary folders: %v", err)
	}
	roots := make([]Root, len(dirs))
	for i, d := range dirs {
		roots[i] = Root{Path: d, Count: perRoot[i].New, Total: perRoot[i].Total, Exists: dirExists(d)}
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
	r.roots = roots
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

// Warm pre-opens dictionaries in the background so the first search does not
// pay the open cost. It opens only the ones that are PREPARED — a SQLite handle
// costing a few MB — and deliberately leaves unprepared ones alone: opening
// those builds an in-memory headword index (measured 300–500 B per headword),
// and doing it for a whole library was several GB of resident memory for
// dictionaries nobody had searched yet (docs/PERF.md M2). An unprepared
// dictionary is opened when something actually needs it: a search, or the
// background indexer that is about to replace it with a prepared one.
func (r *Registry) Warm() {
	entries := r.all()
	go func() {
		sem := make(chan struct{}, 4)
		var wg sync.WaitGroup
		for _, e := range entries {
			if _, prepared := preparedFor(e.Path); !prepared {
				continue
			}
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

// Preview backends are bounded by memory, not by count: dictionaries differ by
// two orders of magnitude in headwords, so "keep 8 open" would mean 50 MB or
// 3 GB depending on which 8 (docs/PERF.md §1).
//
// Eviction runs on a janitor, never on the request path, and never touches a
// backend used in the last minEvictIdle. That is deliberate: a search that
// fans out across a hundred dictionaries would otherwise evict the very
// backends it is about to use again, and each reopen costs 0.2–1.1 s. So the
// budget is enforced between bursts of activity, not during one.
const (
	minEvictIdle  = 45 * time.Second
	janitorPeriod = 20 * time.Second
)

// SetPreviewBudget sets how much memory unprepared ("preview") dictionaries may
// hold open (config PREVIEW_MEMORY; 0 = unlimited).
func (r *Registry) SetPreviewBudget(bytes int64) {
	r.mu.Lock()
	r.previewBudget = bytes
	r.mu.Unlock()
}

// reclaimable is one thing the janitor can close: an unprepared dictionary's
// whole backend, or a prepared one's resource-fallback handle. Both hold an
// in-memory headword index; both reopen on demand; they differ only in what
// stops working meanwhile (everything, versus unpacked media).
type reclaimable struct {
	used  int64
	bytes int64
	free  func() int64
	what  string
}

// reclaimables lists every releasable handle with its weight and last use.
func (r *Registry) reclaimables() []reclaimable {
	var out []reclaimable
	for _, e := range r.all() {
		if w := e.weight.Load(); w > 0 {
			out = append(out, reclaimable{e.lastUse.Load(), w, e.evict, "backend"})
		}
		e.dMu.RLock()
		d := e.d
		e.dMu.RUnlock()
		if es, ok := d.(evictableSource); ok {
			if w := es.srcWeight(); w > 0 {
				out = append(out, reclaimable{es.srcLastUse(), w, es.releaseSource, "resource handle"})
			}
		}
	}
	return out
}

// previewBytes totals what open direct backends currently hold, including the
// resource-fallback handles of prepared dictionaries.
func (r *Registry) previewBytes() int64 {
	var n int64
	for _, c := range r.reclaimables() {
		n += c.bytes
	}
	return n
}

// sweep evicts least-recently-used preview backends until the total is back
// under budget. Returns how many bytes it reclaimed.
func (r *Registry) sweep() int64 {
	r.mu.RLock()
	budget := r.previewBudget
	r.mu.RUnlock()
	if budget <= 0 {
		return 0
	}
	var total int64
	var idle []reclaimable
	cutoff := time.Now().Add(-minEvictIdle).UnixNano()
	for _, c := range r.reclaimables() {
		total += c.bytes
		if c.used < cutoff {
			idle = append(idle, c)
		}
	}
	if total <= budget || len(idle) == 0 {
		return 0
	}
	sort.Slice(idle, func(i, j int) bool { return idle[i].used < idle[j].used }) // oldest first
	var freed int64
	for _, c := range idle {
		if total-freed <= budget {
			break
		}
		freed += c.free()
	}
	if freed > 0 {
		logx.V("preview budget: reclaimed %d MB (was %d MB, budget %d MB)",
			freed>>20, total>>20, budget>>20)
	}
	return freed
}

// startJanitor keeps preview memory under the budget in the background.
func (r *Registry) startJanitor() {
	go func() {
		for range time.Tick(janitorPeriod) {
			r.sweep()
		}
	}()
}

// preparedFor reports whether a registry entry already has prepared data.
func preparedFor(path string) (string, bool) {
	if store.IsTextDB(path) {
		return path, true
	}
	return store.PreparedFor(path)
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
// features is the state a dictionary's prepared data can be in. Finding a
// headword is not among them: it needs no switch, costs ~2 MB, and every
// backend can do it. These three are the ones that cost real disk, so each is
// something the user turns on and off.
type features struct {
	Contains bool
	FullText bool
	Media    bool
}

// setFeatures brings a dictionary's prepared data to the requested state:
// rebuilding the index when the wanted indexes differ from the built ones,
// packing media, or deleting media that is no longer wanted. Rebuilds are the
// same atomic temp+rename as any ingest, so an interrupted change leaves the
// previous data intact.
//
// Stripping is only ever offered while the SOURCE exists — that is what makes
// it reversible, and it is why none of this needs a confirmation prompt. A
// dictionary whose source is gone carries the only copy of its own text, so
// its features are locked rather than dangerous.
func (e *entry) setFeatures(want features, progress store.Progress) error {
	acquire(frontLimit) // the user is waiting: never queue behind background work
	defer release(frontLimit)
	e.ingestMu.Lock()
	defer e.ingestMu.Unlock()

	cur, err := e.open()
	if err != nil {
		return err
	}
	name := cur.Meta().Name
	if store.IsTextDB(e.Path) {
		return fmt.Errorf("%q is a prepared dictionary — its original files are gone, so its data cannot be rebuilt", name)
	}
	dir, err := store.ClaimDir(e.Path)
	if err != nil {
		return err
	}
	textDB := store.TextDBPath(dir)
	mediaDB := store.MediaDBPath(dir)

	have := features{}
	if fileExists(textDB) {
		if m, err := store.ReadMeta(textDB); err == nil {
			have.FullText = m["ingest_level"] != string(store.LevelHeadwords)
			have.Contains = m["has_trigram"] == "1"
		}
	}
	have.Media = fileExists(mediaDB)

	plan := store.Plan{FullText: want.FullText, Contains: want.Contains}
	switch {
	case !fileExists(textDB):
		err = e.rebuild(name, textDB, plan, progress)
	case store.SourceChanged(textDB, e.Path):
		logx.V("%ssource changed since it was prepared — re-indexing", logx.Dict(name))
		err = e.rebuild(name, textDB, plan, progress)
	case have.FullText != want.FullText || have.Contains != want.Contains:
		err = e.rebuild(name, textDB, plan, progress)
	}
	if err != nil {
		return err
	}

	switch {
	case want.Media && !have.Media:
		if err := e.packMedia(cur, textDB, mediaDB, progress); err != nil {
			return err
		}
	case !want.Media && have.Media:
		// the media can be packed again from the source it came from
		if err := os.Remove(mediaDB); err != nil {
			return fmt.Errorf("removing packed media for %q: %w", name, err)
		}
		logx.V("%spacked media removed", logx.Dict(name))
		e.dMu.Lock()
		e.mediaEmpty = false
		e.dMu.Unlock()
	}
	_ = store.WriteInfo(dir)
	return e.reopen()
}

// rebuild writes a fresh index for the requested plan.
func (e *entry) rebuild(name, textDB string, plan store.Plan, progress store.Progress) error {
	rd, err := dict.OpenReader(e.Path)
	if err != nil {
		return err
	}
	rep, ierr := store.IngestPlan(rd, textDB, plan, progress)
	rd.Close()
	if ierr != nil {
		return fmt.Errorf("preparing %q: %w", name, ierr)
	}
	logx.V("%s%d entries indexed (fullText=%v contains=%v)", logx.Dict(name), rep.Entries, plan.FullText, plan.Contains)
	if rep.UnresolvedLinks > 0 {
		logx.V("%s%d redirects pointed at headwords not present in the source (skipped)",
			logx.Dict(name), rep.UnresolvedLinks)
	}
	return nil
}

// reopen swaps in a view of the freshly written data and lets go of the old
// one. The superseded handle is usually a DIRECT backend holding a headword map
// worth hundreds of bytes per entry; leaving it for the garbage collector kept
// that memory resident for the life of the process (docs/PERF.md M2). It is
// closed after a grace period so requests already reading from it finish first.
func (e *entry) reopen() error {
	fresh, err := openUpgradedOrDirect(e.Path)
	if err != nil {
		return err
	}
	e.dMu.Lock()
	old := e.d
	e.d, e.err = fresh, nil
	// the weight must follow the view, not the one it replaced: a prepared
	// dictionary holds no headword map, so it weighs nothing and must never
	// become an eviction candidate
	e.weight.Store(previewWeight(fresh, fresh.Meta()))
	e.dMu.Unlock()
	if old != nil && old != fresh {
		time.AfterFunc(closeGrace, func() {
			old.Close()
			// preparing is the memory high-water mark; hand the pages back
			// rather than sitting on them until the next natural GC
			debug.FreeOSMemory()
		})
	}
	return nil
}

// closeGrace is how long a superseded backend stays open for in-flight reads.
// Ten seconds is far beyond any article fetch; the memory it holds (hundreds of
// bytes per headword) is worth reclaiming promptly.
const closeGrace = 10 * time.Second

// packMedia writes the media.db for a dictionary, from whichever backend can
// enumerate its resources.
func (e *entry) packMedia(cur dict.Dictionary, textDB, mediaDB string, progress store.Progress) error {
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
	if len(names) == 0 {
		// nothing to pack (text-only dictionary, or a format with no
		// resources): remember it so the panel stops offering media.
		e.dMu.Lock()
		e.mediaEmpty = true
		e.dMu.Unlock()
		return nil
	}
	uuid, err := store.ReadMetaValue(textDB, "dict_uuid")
	if err != nil {
		return err
	}
	return store.IngestMedia(src, names, mediaDB, uuid, progress)
}

// ensureBaseIndex builds the cheap find-only index when a dictionary has none
// (D13's silent auto-index). It never strips or rebuilds: a dictionary the
// user has already enriched must not be quietly demoted.
func (e *entry) ensureBaseIndex(progress store.Progress) error {
	e.ingestMu.Lock()
	defer e.ingestMu.Unlock()
	if store.IsTextDB(e.Path) {
		return nil
	}
	if textDB, ok := store.PreparedFor(e.Path); ok && fileExists(textDB) {
		return nil // already prepared, at whatever level the user chose
	}
	dir, err := store.ClaimDir(e.Path)
	if err != nil {
		return err
	}
	// Deliberately NOT e.open(): the ingest reader parses the file itself, and
	// holding the direct backend at the same time doubles the working set of
	// the largest thing in the process (docs/PERF.md M3). The name comes from
	// a header-only probe, or from the reader once it is open.
	if err := e.rebuild(e.probeName(), store.TextDBPath(dir), store.Plan{}, progress); err != nil {
		return err
	}
	_ = store.WriteInfo(dir)
	return e.reopen()
}

// probeName is a display name for log lines, read from the file header when
// the format has a prober and falling back to the file name.
func (e *entry) probeName() string {
	if dict.HasProber(e.Path) {
		if m, err := dict.Probe(e.Path); err == nil && m.Name != "" {
			return m.Name
		}
	}
	e.dMu.RLock()
	d := e.d
	e.dMu.RUnlock()
	if d != nil {
		return d.Meta().Name
	}
	return filepath.Base(e.Path)
}
