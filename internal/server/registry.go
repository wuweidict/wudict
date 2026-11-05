// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Package server is the persistent HTTP server (D3): dictionary
// registry, JSON API, resource streaming, and the per-dictionary feature
// flow (full-text / media) with SSE progress (SPEC §6, D24).
package server

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wuweidict/wudict/internal/dict"
	"github.com/wuweidict/wudict/internal/fsx"
	"github.com/wuweidict/wudict/internal/htmlref"
	"github.com/wuweidict/wudict/internal/logx"
	"github.com/wuweidict/wudict/internal/resource"
	"github.com/wuweidict/wudict/internal/store"
)

// errReindexing answers an open of a dictionary whose prepared database is
// being replaced: on Windows an open during the rebuild would hold the old
// text.db and fail the rename the rebuild ends in (registry_windows.go). The
// slot reports the error until the rebuild's reopen() hands the new database
// out; every other dictionary in the fan-out is unaffected.
var errReindexing = errors.New("dictionary is being re-indexed")

// upgraded serves queries from an ingested text.db while resolving
// resources media.db → original source (D2 resolution order). The direct
// source backend (`src`) is opened lazily - only when a resource actually
// has to fall back to it - so opening an ingested dictionary costs just a
// cheap SQLite open instead of decompressing every key block and building
// fold-maps for a backend we would only use for resource fallback.
type upgraded struct {
	*store.Store
	srcPath string

	// The source handle is opened only when a resource misses media.db - but
	// it is a full direct backend, so it holds the same ~350 bytes per headword
	// as any preview (docs.local/PERF.md §3.1). It is therefore evictable on the same
	// terms: no sync.Once, a recorded weight and last-use, and a release path.
	srcMu  sync.Mutex
	src    dict.Dictionary
	srcErr error
	srcW   atomic.Int64
	srcUse atomic.Int64
	// retired is a released source handle sitting out closeGrace; Close
	// closes it at once, so closing the view lets go of the source files.
	retired retiring

	// The middle rung of resource resolution (O8): containers found from the
	// source PATH, and locations recorded in media.link.db. Both provide a
	// resource without opening the dictionary, which is the entire point -
	// `src` above costs hundreds of megabytes to serve eight kilobytes.
	medMu    sync.Mutex
	medOnce  bool // path-derived containers resolved (they are stable)
	medSrc   []resource.Source
	medLink  *store.Links     // nil when there is no usable locator
	medFet   resource.Fetcher // opened with the locator, over its containers
	medTried bool             // locator open attempted and failed; cleared by a rebuild
	medRetry time.Time        // a stale locator could not be deleted: not asked again before this
	medBuild sync.Once        // one enumeration per process, however many misses
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
// working - text comes from SQLite - and the handle reopens if another
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
	u.retired.retire(d)
	logx.V("released resource handle for %s (~%d MB)", filepath.Base(u.srcPath), w>>20)
	return w
}

// decoderHolder is a prepared database whose article decoder can be released
// (store.Store, D191): about the size of its zstd dictionary each, made again
// by the next article.
type decoderHolder interface {
	DecoderHeld() (bytes, lastUse int64)
	ReleaseDecoder() int64
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
	m.Format = strings.TrimPrefix(m.Format, "wudict:")
	m.Path = u.srcPath
	return m
}

// Resource resolves in three rungs, cheapest first (D2, extended by O8):
//
//  1. the packed media.db the Store attaches - portable, and always right when
//     it is there;
//  2. the source's own containers and recorded locations - a stat or a block
//     read, no dictionary opened;
//  3. the full direct backend, which is correct for every format and every
//     name and costs what a preview costs.
//
// Rung 3 remains the floor, so a format with no provider, a locator that has
// not been built yet, and a resource nobody recorded all keep working. Reaching
// it also schedules the enumeration that stops the NEXT article from doing so.
func (u *upgraded) Resource(name string) (io.ReadCloser, string, error) {
	if rc, mime, err := u.Store.Resource(name); err == nil {
		return rc, mime, nil
	}
	if rc, mime, ok := u.linked(name); ok {
		return rc, mime, nil
	}
	src, err := u.source()
	if err != nil {
		return nil, "", err
	}
	u.srcUse.Store(time.Now().UnixNano())
	rc, mime, rerr := src.Resource(name)
	if rerr == nil {
		u.recordLinks(src)
	}
	return rc, mime, rerr
}

// linked answers from the path-derived containers, then from the locator.
// A miss is not an error: it means "ask the next rung", and every failure in
// here - no provider, no cache, an unreadable container - is exactly that.
func (u *upgraded) linked(name string) (io.ReadCloser, string, bool) {
	prov, ok := resource.Get(u.Meta().Format)
	if !ok || u.srcPath == "" {
		return nil, "", false
	}
	srcs, links, fet := u.media(prov)
	// Packed before loose, which is the order the direct backend uses: MDict
	// serves a file lying beside the .mdx only when no .mdd holds that name, so
	// checking the folder first would answer differently for a dictionary that
	// ships both spellings of one asset.
	if links != nil && fet != nil {
		if l, ok := links.Lookup(name); ok {
			data, err := fet.Fetch(l.Part, l.Off, l.Size)
			if err != nil {
				logx.V("locator fetch failed for %s in %s: %v", name, filepath.Base(u.srcPath), err)
			} else {
				mime := l.MIME
				if mime == "" {
					mime = resource.MIME(name)
				}
				return resource.Bytes(data), mime, true
			}
		}
	}
	for _, s := range srcs {
		if rc, err := s.Open(name); err == nil {
			return rc, resource.MIME(name), true
		}
	}
	return nil, "", false
}

// media resolves this dictionary's containers once and its locator on first
// use, reopening the latter after a rebuild has produced one.
func (u *upgraded) media(prov resource.Provider) ([]resource.Source, *store.Links, resource.Fetcher) {
	u.medMu.Lock()
	defer u.medMu.Unlock()
	if !u.medOnce {
		u.medOnce = true
		if prov.Sources != nil {
			u.medSrc = prov.Sources(u.srcPath)
		}
	}
	if u.medLink == nil && !u.medTried && prov.Open != nil && !time.Now().Before(u.medRetry) {
		u.medTried = true
		if path := store.LinkSibling(u.Store.Meta().Path); path != "" && fsx.FileExists(path) {
			links, err := store.OpenLinks(path, u.Store.UUID())
			if err != nil {
				// The cache no longer describes the files it points into (or
				// never did). Serving from it would hand back whatever now sits
				// at those offsets, so it is deleted rather than distrusted.
				logx.V("discarding %s: %v", filepath.Base(path), err)
				if rerr := os.Remove(path); rerr != nil {
					// Still open in a backend sitting out its closeGrace, and
					// Windows refuses to delete an open file. Left as it was,
					// the stale file would also stop recordLinks from writing
					// a new one for the life of the process: asked again once
					// that grace has run out.
					u.medTried, u.medRetry = false, time.Now().Add(closeGrace)
				}
			} else if fet, ferr := prov.Open(links.Parts()); ferr != nil {
				links.Close()
			} else {
				u.medLink, u.medFet = links, fet
			}
		}
	}
	return u.medSrc, u.medLink, u.medFet
}

// recordLinks enumerates the source's media locations in the background, once,
// after a fallback has proved that this dictionary actually serves resources
// the cheap rungs miss. It is best-effort throughout: the cache is an
// optimization, and every path here already works without it.
func (u *upgraded) recordLinks(src dict.Dictionary) {
	lk, ok := src.(resource.Linker)
	if !ok {
		return
	}
	path := store.LinkSibling(u.Store.Meta().Path)
	if path == "" || fsx.FileExists(path) {
		return
	}
	u.medBuild.Do(func() {
		go func() {
			parts, links, err := lk.MediaLinks()
			if err != nil || len(parts) == 0 || len(links) == 0 {
				return
			}
			if err := store.WriteLinks(path, parts, links, u.Store.UUID()); err != nil {
				logx.V("could not record media locations for %s: %v", filepath.Base(u.srcPath), err)
				return
			}
			logx.V("recorded %d media locations for %s", len(links), filepath.Base(u.srcPath))
			u.medMu.Lock()
			u.medTried = false // a locator exists now: open it on the next miss
			u.medMu.Unlock()
		}()
	})
}

func (u *upgraded) Close() error {
	u.srcMu.Lock()
	src := u.src
	u.src = nil
	u.srcMu.Unlock()
	if src != nil {
		src.Close()
	}
	u.retired.closeAll()
	u.medMu.Lock()
	srcs, links, fet := u.medSrc, u.medLink, u.medFet
	u.medSrc, u.medLink, u.medFet = nil, nil, nil
	u.medMu.Unlock()
	for _, s := range srcs {
		s.Close()
	}
	if fet != nil {
		fet.Close()
	}
	if links != nil {
		links.Close()
	}
	return u.Store.Close() // Store.Close also closes its attached media.db
}

// Preparing a dictionary costs a saturated core and a few hundred bytes of RAM
// per headword (docs.local/PERF.md §3). Nothing bounded that: `maybeAutoIndex` fired
// one goroutine per dictionary, so a single "all dictionaries" search over a
// 100-dictionary library started 100 concurrent ingests - measured at 500 MB
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

// acquire blocks until a slot of sem is free and returns what frees it. The
// release is bound to the channel the slot was taken from, never re-read from
// the variable: SetIndexWorkers replaces indexLimit, and a slot handed back to
// a lane it did not come from takes another holder's slot, whose own release
// then waits forever.
func acquire(sem chan struct{}) (release func()) {
	sem <- struct{}{}
	return func() { <-sem }
}

// entry is one discovered dictionary, opened lazily.
type entry struct {
	ID   string
	Path string
	// builtin: a dictionary the app ships (Builtin), not one the user added.
	builtin bool

	// reg is the registry that owns this entry, so an open can tell the
	// janitor there is something to watch again. Nil in tests that build an
	// entry directly; every call site guards.
	reg *Registry

	openMu sync.Mutex // serialises opening; NOT sync.Once - an evicted
	// backend must be openable again
	dMu sync.RWMutex
	d   dict.Dictionary
	err error

	// backing is the prepared database this entry's derived state was resolved
	// against - the text.db that open() found, or "" for a dictionary that had
	// none and was opened directly. Everything below (the memoized backend and
	// error, autoTried, demanded, demandFail, abbrevTried) is a conclusion
	// drawn from that resolution, and none of it survives the file it was drawn
	// from: a library folder deleted from outside the app must not leave the
	// entry serving a SQLite handle whose file is gone. Rescan re-derives this
	// and resets what disagrees (revalidate). dMu-guarded, like d and err.
	backing string

	// srcSig is the source file's size and mtime as they were when the backend
	// was opened (sourceSig), dMu-guarded like backing. Rescan compares it
	// with the file now: a source replaced in place - a newer edition copied
	// over the old one - leaves backing unchanged, so without this the entry
	// would keep serving the previous edition's headwords (preview), or keep
	// routing to an index built from it (prepared), until the process restarts.
	srcSig string

	// retired holds the backends this entry superseded or evicted while they
	// sit out closeGrace; closeNow and releasePrepared close them at once.
	retired retiring

	lastUse atomic.Int64 // unix nanos, for LRU eviction
	weight  atomic.Int64 // estimated bytes held by a preview backend (0 if cheap)

	// lastWeight is weight, remembered across eviction. weight must go to zero
	// when the backend is dropped - it is what the sweep totals - but the cost
	// of opening this dictionary again does not stop being known just because we
	// closed it, and the fan-out cap needs that number BEFORE it pays it. First
	// open of a dictionary is therefore uncapped (nothing is known about it yet)
	// and every later one is priced.
	lastWeight atomic.Int64

	mediaEmpty bool // a full ingest found no packable resources (dMu-guarded)

	// This dictionary's own CSS, reduced to class → display, for the `clean`
	// and `text` article formats (see articlestyle.go). Derived on first use
	// and forgotten whenever the backend it was read from is closed or
	// replaced. styleDone distinguishes "not derived yet" from "derived, and
	// this dictionary has no stylesheet" - the second must not retry.
	styleMu   sync.Mutex
	styleDone bool
	style     htmlref.Styles

	ingestMu  sync.Mutex  // one ingest at a time per dictionary
	autoTried atomic.Bool // first-search auto-index, attempted once
	demanded  atomic.Bool // a user-demanded index is queued or running (demandIndex)
	// demandFail is when the last demanded ingest failed, in Unix nanoseconds.
	// It is what keeps a retryable failure from becoming a per-keystroke storm:
	// a search naming ONE dictionary is a demand, and the selector produces one
	// of those per character typed.
	demandFail atomic.Int64
	// abbrevTried marks this dictionary as already considered by the
	// abbreviation upgrade sweep, so a rescan does not re-queue it.
	abbrevTried atomic.Bool

	// gen moves whenever what this entry's /api/dicts row was derived from
	// changes under the app's own hand: a prepare, rebuild or pack
	// (entry.reconcile), and a backend or prepared folder a rescan finds
	// changed (revalidate) - removing prepared data and every Rescan caller
	// land there. Keyed into the row cache (dictrows.go), it catches what a
	// coarse filesystem clock, or a row derived from a still-open backend,
	// would hide from the stat keys. It starts at 0 in every process, so a
	// validator kept across a restart can outlive a change only the stats
	// missed; that is accepted (304s survive restarts), and Rescan clears it.
	gen atomic.Uint64

	// rebuilding bars opens for the length of an ingest that will rename over
	// the prepared database (Windows only; set with the backend handback in
	// registry_windows.go). Set false again by the defers in setFeatures,
	// ensureBaseIndex and reabsorbAbbrev - whichever stage armed it.
	rebuilding atomic.Bool
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
// once per process; a failure (e.g. a read-only library, no ingest reader for
// the format) is reported and not retried - auto-indexing is a convenience,
// never a hard requirement. What the user asks for by name IS retried; see
// demandIndex.
//
// Automatic while it is cheap; on demand once it is not. This fires from a
// SUCCESSFUL open only, which is what bounds it: a dictionary the fan-out cap
// declined is not opened and is therefore not indexed here, deliberately. That
// refusal is the ceiling past which preparation stops being automatic - on a
// phone the alternative is queueing an hour of sustained ingest for
// dictionaries the user has not asked for, which is the storm the cap exists to
// prevent. What lifts it is the user opening that dictionary's section: the
// resulting single-dictionary search is uncapped (handleSearch), so it opens,
// so it lands here, so it is prepared and never deferred again. Meanwhile every
// dictionary that DID fit is prepared in the background, and each one that
// finishes drops to previewWeight 0 and frees budget for the next - the library
// converges on its own, at a rate the cap chooses.
//
// Never while the process is not active: indexing is the single most expensive
// thing this program does (a saturated core and hundreds of bytes per headword),
// and starting it because a search landed just as the screen went off is exactly
// how an app gets flagged as a battery hog. The attempt is un-marked when it
// declines, so this stays a deferral rather than a silent cancellation - the
// next search once the user is back does it.
func (e *entry) maybeAutoIndex() {
	if CurrentPower() != PowerActive {
		return
	}
	// Never for a backend that carries its own on-disk index. This is a
	// property of the backend, not a name check, so a format that later gains
	// it inherits the behaviour without another special case. For ZIM it is
	// also the difference between an idle library and a runaway one:
	// preparing it EXPANDS the data (the source packs whole clusters with
	// zstd, a text.db compresses one article at a time, D24, D191; measured
	// with DEFLATE, a 123 MB wiktionary became a 431 MB text.db), and an 8 GB
	// wikipedia would become something no one asked for.
	e.dMu.RLock()
	si, selfIdx := e.d.(selfIndexed)
	e.dMu.RUnlock()
	if selfIdx && si.SelfIndexed() {
		return
	}
	if !e.autoTried.CompareAndSwap(false, true) {
		return
	}
	// Automatic only while it is cheap (D99). Above the gate the convenience
	// stops being one: a 2.9 M-headword dictionary measured 17 s of saturated
	// CPU and 553 MB peak on a laptop, and it is not the program's place to
	// spend minutes of a phone's battery and a gigabyte of its disk on a
	// dictionary nobody has asked for yet. Checked here rather than in
	// previewWeight's budget because that budget prices the OPEN, which has
	// already happened by the time this runs, and prices nothing about the
	// ingest that follows it.
	//
	// The gate is the background lane's alone. demandIndex - the user chose
	// this dictionary - stays ungated (D92): size is a reason to not do this
	// unasked, never a reason to refuse what was asked for.
	if n := entryCountOf(e); n > autoIndexMaxEntries {
		// A format that prepares itself at open - DSL ingests inside dsl.Open -
		// is already past this gate by the time anyone can read the line, and
		// telling its owner to "select the dictionary to prepare it" describes
		// work that has happened. The gate still returns: ensureBaseIndex would
		// no-op anyway, and a size ceiling has nothing to say about a library
		// that already exists.
		if _, prepared := validPrepared(e.Path); !prepared {
			logx.V("auto-index %s: %d entries is over the %d automatic ceiling; "+
				"select the dictionary to prepare it", e.Path, n, autoIndexMaxEntries)
		}
		return
	}
	go func() {
		defer acquire(indexLimit)() // at most INDEX_WORKERS of these run at once
		// the queue is FIFO and an ingest takes minutes, so the state that
		// permitted this may be long gone by the time the slot is ours
		if CurrentPower() != PowerActive {
			e.autoTried.Store(false)
			return
		}
		// ensureBaseIndex is a no-op when this dictionary is already
		// prepared, at whatever level its owner chose
		if err := e.ensureBaseIndex(nil); err != nil {
			// Once per process, and said out loud: an auto-index is not
			// retried, so this line is the only account of why a dictionary
			// stayed unprepared.
			logx.Warn("could not prepare %s: %v", filepath.Base(e.Path), err)
		} else {
			logx.V("auto-index %s: index ready", e.Path)
		}
	}()
}

// demandIndex prepares this dictionary's headword index because the user asked
// for THIS dictionary: they chose it in the selector, followed a link into it,
// or opened a section the fan-out cap had deferred. Same work as
// maybeAutoIndex, two differences - the queue, and what a failure means.
//
// maybeAutoIndex waits on indexLimit, which is one worker and, on a library of
// a hundred dictionaries, hours long. That is right for a convenience nobody
// asked for and wrong here: the deferred dictionary is already in that queue,
// somewhere, and leaving it there would mean "I opened this one" buys nothing
// until this evening - the user would meet the same deferral on the same
// dictionary tomorrow. So a demand jumps to frontLimit, the lane setFeatures
// uses for work a person is waiting on. At most one such ingest runs at a time,
// alongside at most one background one; the ceiling on concurrent ingests goes
// from one to two, and only ever because someone asked.
//
// A FAILED demand is retried, unlike a failed auto-index. The once-per-process
// flag exists to stop a search from re-queueing an ingest on every keystroke,
// not to make one unwritable folder or one full disk permanent for the life of
// the process while the deferred section goes on offering a tap that can no
// longer do anything. The flag is therefore released on failure, and
// demandRetryAfter does the flag's real job: typing cannot re-queue anything,
// and a person who reads the message and taps again gets a real second attempt.
//
// No power check, deliberately, and this is the one place that omits one. Every
// other expensive thing here starts on the program's initiative and must not
// start while the screen is off; this one starts because a finger touched the
// screen. A demand declined for a stale power state - a thermal warning, a
// battery-saver flag the shell has not yet cleared - is a dictionary that
// searches but never prepares, with nothing said about why. The work is
// bounded (one dictionary, one front slot) and it is the work the user is
// waiting on.
const demandRetryAfter = 30 * time.Second

func (e *entry) demandIndex() {
	// Nothing to prepare: return before anything is spent. Not an
	// optimisation of a few milliseconds - the goroutine, the front slot and
	// the power hold are all observable. HoldActiveProcs announces "work the
	// user is waiting on" to the host (power.go), and on Android that hoists a
	// foreground service for an ingest that ends 36 ms later.
	if _, ok := validPrepared(e.Path); ok {
		e.demanded.Store(true) // ready, so the UI stops offering to prepare it
		return
	}
	if f := e.demandFail.Load(); f != 0 && time.Since(time.Unix(0, f)) < demandRetryAfter {
		return
	}
	if !e.demanded.CompareAndSwap(false, true) {
		return
	}
	go func() {
		// Marked before any wait: a background preparation of this very
		// dictionary that holds its lock sees the mark and scales up (D182).
		defer dict.MarkUrgent(e.Path)()
		defer acquire(frontLimit)()
		// Someone is waiting on this one, so it keeps every core it was
		// started with even if the screen goes off mid-ingest (power.go
		// HoldActiveProcs). The background lane deliberately gets no such
		// exemption.
		defer HoldActiveProcs()()
		if err := e.ensureBaseIndex(nil); err != nil {
			// Warn, not V: this is the whole reason the dictionary keeps
			// asking to be tapped, and at V the user never sees it.
			logx.Warn("could not prepare %s: %v", filepath.Base(e.Path), err)
			e.demandFail.Store(time.Now().UnixNano())
			e.demanded.Store(false) // the next demand, after the cooldown, retries
			return
		}
		logx.V("demand-index %s: index ready", e.Path)
	}()
}

// indexing reports that a demanded ingest is queued or running for this
// dictionary, which is what turns the deferred section's "tap to search" into
// "preparing…". It stays true after a successful one - harmless, because a
// prepared dictionary weighs nothing and is never deferred again.
func (e *entry) indexing() bool { return e.demanded.Load() }

// open opens the source backend and, when a cached text.db (and
// media.db) exists for it, wraps it into the upgraded view.
func (e *entry) open() (dict.Dictionary, error) {
	e.lastUse.Store(time.Now().UnixNano())
	if e.rebuilding.Load() {
		return nil, errReindexing
	}
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
	// Read before the open, so the recorded resolution is the one this open
	// actually acted on rather than whatever disk looked like once it finished.
	backing, sig := backingDB(e.Path), sourceSig(e.Path)
	// A failure kept from an earlier start answers for a file with no library
	// folder (openfail.go).
	var fails *failures
	if e.reg != nil && backing == "" {
		fails = e.reg.fails
	}
	if err = fails.lookup(e.Path, sig); err != nil {
		e.dMu.Lock()
		e.err, e.backing, e.srcSig = err, backing, sig
		e.dMu.Unlock()
		logx.V("open %s: FAILED, as at the last start: %v", e.Path, err)
		return nil, err
	}
	// dsl, bgl and wudict markdown prepare themselves inside Open, and a
	// re-prepare ends in a rename over text.db - which a backend superseded
	// moments ago (a rescan that saw the source change) may still hold through
	// its closeGrace. Fatal only on Windows; see registry_windows.go.
	releaseSuperseded(e)
	d, err = openUpgradedOrDirect(e.Path)
	e.dMu.Lock()
	e.d, e.err, e.backing, e.srcSig = d, err, backing, sig
	e.dMu.Unlock()
	if err != nil {
		fails.record(e.Path, sig, err)
		logx.V("open %s: FAILED: %v", e.Path, err)
		return d, err
	}
	m := d.Meta()
	w := previewWeight(d, m)
	e.weight.Store(w)
	if w > 0 {
		e.lastWeight.Store(w)
	}
	if e.reg != nil {
		// something is open that was not open before: the janitor has a reason
		// to exist again. It sleeps with no timer whenever nothing does.
		e.reg.nudge()
	}
	logx.V("open %s [%s] %d entries fts=%v (%s)",
		m.Name, m.Format, m.EntryCount, d.Caps().FTS, time.Since(start).Round(time.Millisecond))
	return d, err
}

// fanout is one search's materialisation budget: how many bytes of *newly
// opened* preview backends a single query may bring into memory.
//
// The preview budget cannot do this job. It is enforced
// by the janitor, between bursts, and it deliberately refuses to evict anything
// used in the last minEvictIdle - which is every dictionary a `dict=all` search
// just touched. Measured, that means a 64 MB budget coexisting with 6.3 GB held
// (docs.local/PERF.md §8.2) on a desktop, and 1.71 GB on a 3.8 GB tablet (§8.6): the
// budget bounds the *steady state* and nothing bounds the burst. This does.
//
// The unit is estimated bytes, not dictionaries, for the same reason the budget
// is: dictionaries in one library differ by two orders of magnitude in headword
// count, so "open at most N" is 50 MB or 3 GB depending on which N.
//
// What it costs is result completeness, and that is the honest name for it: a
// dictionary the cap refuses is reported to the client as DEFERRED - its
// section is still there, in preference order, and opening it searches it,
// because a search naming one dictionary is not capped. That is the whole
// remedy, and it is a tap: the same open prepares the dictionary in the
// background, and a prepared dictionary answers from SQLite, weighs nothing
// here and is never capped again. It is off by default on the
// desktop, where RAM is the machine's own business, and on by default on
// Android, where the alternative outcome is not a slower search but a killed
// process. Preview mode is the transient state before preparation (D15/D20), so
// on a settled library the cap never fires at all.
type fanout struct{ left atomic.Int64 }

// fanout opens a budget for one search, or nil when uncapped. Nil is a valid
// receiver everywhere below: "no cap" costs no allocation and no branching at
// the call sites.
func (r *Registry) fanout() *fanout {
	r.mu.RLock()
	cap_ := r.searchBudget
	r.mu.RUnlock()
	if cap_ <= 0 {
		return nil
	}
	f := &fanout{}
	f.left.Store(cap_)
	return f
}

// admit reserves est bytes, reporting whether the caller may open. A dictionary
// whose known cost exceeds what is left is refused WITHOUT spending the
// remainder, so one 961 MB monster early in the user's preference order costs
// the rest of the list nothing - the fan-out packs what fits instead of
// stopping at the first thing that does not.
func (f *fanout) admit(est int64) bool {
	if f == nil {
		return true
	}
	for {
		left := f.left.Load()
		if left <= 0 || est > left {
			return false
		}
		if f.left.CompareAndSwap(left, left-est) {
			return true
		}
	}
}

// settle corrects the reservation once the real weight is known. A first open
// reserves nothing (est 0) and is charged in full here, which can drive the
// budget negative - that is correct, and it is what refuses everything after it.
func (f *fanout) settle(est, actual int64) {
	if f == nil {
		return
	}
	if d := actual - est; d != 0 {
		f.left.Add(-d)
	}
}

// tooHeavy is what a refused dictionary reports: deferred, not failed. It
// carries the estimate so the caller can say what was declined and at what
// price. This string is a LOG line and a test fixture; it is never rendered.
// handleSearch matches this type and streams `deferred` instead, because the
// user-facing statement is "not searched yet - open this to search it", and a
// megabyte figure over a budget nobody set is not something a reader can act on.
type tooHeavy struct{ bytes int64 }

func (t tooHeavy) Error() string {
	if t.bytes > 0 {
		return fmt.Sprintf("deferred: ~%d MB to open, over this search's materialisation budget", t.bytes>>20)
	}
	return "deferred: this search reached its materialisation budget"
}

// openWithin is open, subject to a fan-out's materialisation budget. A backend
// that is already resident is free and never refused: the cap exists to stop
// memory being *created*, and refusing to read what is already in RAM would
// cost results for no saving whatsoever.
func (e *entry) openWithin(f *fanout) (dict.Dictionary, error) {
	if f == nil {
		return e.open()
	}
	e.dMu.RLock()
	d, err := e.d, e.err
	e.dMu.RUnlock()
	if d != nil || err != nil {
		return e.open() // memoized; open() only refreshes lastUse
	}
	est := e.lastWeight.Load()
	if !f.admit(est) {
		// A prepared dictionary is never capped: it answers from SQLite, holds
		// no headword index, and costs this budget nothing. Worth one stat on
		// the refusal path to be certain, because declining one would drop
		// results for no memory saved at all - and the refusal path is by
		// definition the rare one.
		if _, prepared := validPrepared(e.Path); !prepared {
			return nil, tooHeavy{bytes: est}
		}
		est = 0 // admitted without a reservation: charge whatever it turns out to cost
	}
	d, err = e.open()
	if err != nil {
		f.settle(est, 0) // nothing was materialised; give the reservation back
		return d, err
	}
	f.settle(est, e.weight.Load())
	return d, err
}

// previewWeight estimates the resident cost of an open backend. A PREPARED
// dictionary answers from SQLite and costs a few MB whatever its size, so it
// weighs nothing here. A direct ("preview", D15) backend builds an in-memory
// headword index on first use - measured at 300–500 bytes per headword across
// MDX and SLOB (docs.local/PERF.md §3.1) - and that is what eviction reclaims.
func previewWeight(d dict.Dictionary, m dict.Meta) int64 {
	if _, ok := d.(storeBacked); ok {
		return 0 // answers from SQLite: the index lives on disk, not in RAM
	}
	if sw, ok := d.(selfWeighing); ok {
		return sw.PreviewBytes() // it knows; the estimate below does not
	}
	if m.EntryCount <= 0 {
		return 0
	}
	return int64(m.EntryCount) * previewBytesPerEntry
}

// selfWeighing is a backend that measures its own resident cost instead of
// being estimated from a headword count. The estimate assumes an in-memory
// headword index; ZIM searches its own file with a binary search and keeps no
// map, so the estimate would over-charge it by two orders of magnitude and hold
// it under permanent eviction pressure for memory it never allocated.
type selfWeighing interface{ PreviewBytes() int64 }

// selfIndexed is a backend whose own file already answers exact and prefix
// lookup at no resident cost. Preparation stays fully available for these -
// the per-dictionary switches, `wudict ingest` - but it stops being automatic,
// because the one thing maybeAutoIndex buys for other formats (getting a
// headword index off the heap and onto disk) is already true here. See
// maybeAutoIndex.
type selfIndexed interface{ SelfIndexed() bool }

// storeBacked matches anything answering from a prepared database - the
// `upgraded`/`native` wrappers, and the formats that embed a *store.Store
// directly because they have no native index (DSL, BGL). Type-switching on the
// wrappers alone missed those, and a self-prepared dictionary would have been
// counted as if it held a headword map in RAM.
type storeBacked interface{ SourcePath() string }

// previewBytesPerEntry is the per-headword cost of a direct backend's in-memory
// index (docs.local/PERF.md §3.1: 290–570 B across formats; 350 is the middle of that
// range). It is an estimate applied to a headword count, never a measurement of
// this dictionary: re-measured against MDX in 2026-08 (PERF §8.3) the real cost
// was 83 B/entry at open and 201 B/entry once a search had built the folded
// index, so this over-charges that format by ~1.7×. Kept deliberately - on
// Android over-charging sheds early, and under-charging is what gets a process
// killed.
const previewBytesPerEntry = 350

// autoIndexMaxEntries is the headword count above which preparation stops
// happening on the program's own initiative (D99). Measured against the local
// 152-dictionary corpus: a prepared headwords-only text.db costs ~1 KB per
// entry in aggregate, so this ceiling is about a gigabyte of disk and, at
// previewBytesPerEntry, a third of a gigabyte resident during the scan. Only
// two dictionaries in that corpus are above it. It is a ceiling on what is
// automatic, not on what is possible.
const autoIndexMaxEntries = 1_000_000

// entryCountOf reads the headword count off an already-open backend. It never
// opens one: maybeAutoIndex runs from a successful open, so the backend is
// there, and a nil one (a test entry, an evicted backend) reports 0 - which
// reads as "under the ceiling" and preserves the previous behaviour for
// anything that cannot be measured.
func entryCountOf(e *entry) int {
	e.dMu.RLock()
	d := e.d
	e.dMu.RUnlock()
	if d == nil {
		return 0
	}
	return d.Meta().EntryCount
}

// evict drops this entry's open backend so its memory can be reclaimed. It
// refuses while the dictionary is being prepared, and closes after a grace so
// requests already reading from it finish. The next open reopens the file.
func (e *entry) evict() int64 { n, _ := e.drop(false); return n }

// drop is evict, plus the option to close a backend that weighs nothing.
// Weightless means "prepared": it answers from SQLite and holds no headword
// map, so the budget has no reason to touch it - but its file descriptors and
// page cache are still real, and under PowerRestricted they are worth giving
// back. Returns the bytes the eviction accounting knows about, which for a
// prepared dictionary is honestly zero. The second result says whether a
// backend was actually let go, which the byte count cannot: zero bytes is both
// "closed a prepared dictionary" and "declined, an ingest holds it". Only
// revalidate needs the difference - it must not record a new resolution for a
// backend it failed to drop - but the distinction belongs here rather than in a
// second, subtly different close path.
func (e *entry) drop(force bool) (int64, bool) {
	if !e.ingestMu.TryLock() {
		return 0, false // being prepared right now: leave it alone
	}
	defer e.ingestMu.Unlock()
	e.dMu.Lock()
	d := e.d
	w := e.weight.Load()
	if d == nil || (w == 0 && !force) {
		e.dMu.Unlock()
		return 0, false
	}
	e.d, e.err = nil, nil
	e.weight.Store(0)
	e.dMu.Unlock()
	// Outside dMu, so this lock is only ever taken before dMu and never after
	// it - the derivation in entry.styles holds styleMu while it opens.
	e.forgetStyles()
	e.retired.retire(d)
	if w > 0 {
		logx.V("evicted preview backend %s (~%d MB)", filepath.Base(e.Path), w>>20)
	} else {
		logx.V("closed %s", filepath.Base(e.Path))
	}
	return w, true
}

// scheduleReclaim hands freed pages back to the OS shortly after a batch of
// closes. Coalesced deliberately: FreeOSMemory is a stop-the-world collection
// plus a scavenge, and shedding a hundred dictionaries at once would otherwise
// mean a hundred of them back to back - a CPU spike indistinguishable, from the
// platform's point of view, from the runaway work this whole mechanism exists
// to avoid.
var reclaimArmed atomic.Bool

const reclaimDelay = 2 * time.Second

func scheduleReclaim() {
	if reclaimArmed.Swap(true) {
		return // one is already pending; it will cover this close too
	}
	time.AfterFunc(reclaimDelay, func() {
		reclaimArmed.Store(false)
		debug.FreeOSMemory()
	})
}

// native is a standalone naturalized dictionary: a .text.db whose foreign
// source is gone (the db dir is the native dictionary root). It presents like
// `upgraded` - the internal wudict: format prefix stripped, Path set to the db
// file - but has no source to fall back to, so resources come only from its
// attached media.db when present.
type native struct {
	*store.Store
	path string
}

func (n *native) Meta() dict.Meta {
	m := n.Store.Meta()
	m.Format = strings.TrimPrefix(m.Format, "wudict:")
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
		if src := s.SourcePath(); src != "" && fsx.FileExists(src) {
			return &upgraded{Store: s, srcPath: src}, nil
		}
		return &native{Store: s, path: path}, nil
	}
	// a prepared folder for this source short-circuits the heavy direct
	// backend entirely - no probe needed, since the folder is located from
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
// dictionary folder and - only when the user has opted in (USE_CACHED) - the
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
	builtin   []Builtin

	// previewBudget caps the memory unprepared dictionaries may hold open
	// (PREVIEW_MEMORY; 0 = unlimited). Prepared ones answer from disk and are
	// never evicted - there would be nothing to reclaim.
	previewBudget int64

	// searchBudget caps how much preview memory ONE search may materialise
	// (SEARCH_MEMORY; 0 = uncapped). See the fanout type for why the preview
	// budget cannot do this.
	searchBudget int64

	// prefs is the user's enabled set and order (state.json). Never nil: an
	// in-memory Prefs answers "nothing is disabled", which is the right
	// default everywhere the caller supplied no file.
	prefs *Prefs

	// wake arms the janitor. Buffered by one: a signal means "there may be
	// work now", and two of them mean nothing more than one.
	wake chan struct{}

	// fails are the open failures kept across restarts (openfail.go); nil
	// keeps none beyond each entry's own err.
	fails *failures
}

// nudge tells the janitor something changed. Never blocks - it is called from
// request paths, and a janitor that is already awake needs no telling.
func (r *Registry) nudge() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

// Option configures a Registry at construction. Prefs must be in place BEFORE
// the first Warm, which is why this is an option and not a setter - a warm-up
// that pre-opened everything and only then learned what to skip would have
// already paid the memory it was told to save.
type Option func(*Registry)

// Builtin is a dictionary the app ships: the wudict howto (internal/howto).
// It is listed after every folder is scanned, while its file exists, under a
// fixed ID so links to it work on every machine, and it stands down when a
// dictionary folder holds a file of the same name - the user's own copy, which
// is then the one listed. Removing it deletes its file, so it is no longer
// listed; the caller records the removal so it is not written back.
type Builtin struct {
	ID   string
	Path string
}

// WithBuiltin lists the app's own dictionaries alongside the user's.
func WithBuiltin(b ...Builtin) Option {
	return func(r *Registry) { r.builtin = append(r.builtin, b...) }
}

// WithPrefs attaches the persisted enabled/disabled state.
func WithPrefs(p *Prefs) Option {
	return func(r *Registry) {
		if p != nil {
			r.prefs = p
		}
	}
}

// WithFailures keeps the open failures that would only fail again in the file
// at path, so the next start does not pay for them (openfail.go).
func WithFailures(path string) Option {
	return func(r *Registry) { r.fails = loadFailures(path) }
}

// ForgetFailures drops the open failures kept across restarts: the user's
// Rescan is the request to try every dictionary again, whatever it cost.
func (r *Registry) ForgetFailures() { r.fails.forget() }

// Root is one dictionary folder and what it contributed. A folder that is
// missing (an unmounted drive, a deleted path) is reported, never fatal: the
// other folders must keep working.
type Root struct {
	Path   string `json:"path"`
	Count  int    `json:"count"` // dictionaries this folder was the first to offer
	Total  int    `json:"total"` // everything it holds (Total > Count ⇒ overlap)
	Exists bool   `json:"exists"`
}

func NewRegistry(dictDirs []string, useCached bool, opts ...Option) (*Registry, error) {
	r := &Registry{
		dictDirs:  dict.DedupeDirs(dictDirs),
		useCached: useCached,
		byID:      map[string]*entry{},
		prefs:     LoadPrefs(""),
		wake:      make(chan struct{}, 1),
	}
	for _, o := range opts {
		o(r)
	}
	if err := r.Rescan(); err != nil {
		return r, err
	}
	r.Warm()
	r.upgradeAbbrev()
	r.startJanitor()
	return r, nil
}

// UseCached reports whether prepared dictionaries are included.
func (r *Registry) UseCached() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.useCached
}

// SetUseCached opts the library in or out and rescans (setup flow - no
// restart needed).
func (r *Registry) SetUseCached(on bool) error {
	r.mu.Lock()
	r.useCached = on
	r.mu.Unlock()
	if err := r.Rescan(); err != nil {
		return err
	}
	r.Warm()
	r.upgradeAbbrev()
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

// UserCount is Count without the dictionaries the app ships (Builtin): what
// the user has, which is what "your library is empty" is about.
func (r *Registry) UserCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	n := 0
	for _, e := range r.entries {
		if !e.builtin {
			n++
		}
	}
	return n
}

// SetDirs re-points the registry at new dictionary folders and rescans
// (used by the first-run setup flow - no restart needed).
func (r *Registry) SetDirs(dirs []string) error {
	r.mu.Lock()
	// one folder listed twice (a repeat, a trailing slash, a symlink) must not
	// become two rows, two walks and two lines in wudict.toml
	r.dictDirs = dict.DedupeDirs(dirs)
	r.mu.Unlock()
	if err := r.Rescan(); err != nil {
		return err
	}
	r.Warm()
	r.upgradeAbbrev()
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
	// A dictionary file that was moved, not deleted, takes its prepared folder
	// with it (D156) - before anything below lists that folder on its own or
	// prepares the file again from scratch.
	for _, rl := range store.Relink(paths) {
		logx.V("rescan: %s moved to %s; its prepared data follows it", rl.From, rl.To)
	}
	roots := make([]Root, len(dirs))
	for i, d := range dirs {
		roots[i] = Root{Path: d, Count: perRoot[i].New, Total: perRoot[i].Total, Exists: fsx.DirExists(d)}
	}
	r.mu.RLock()
	builtin := append([]Builtin(nil), r.builtin...)
	r.mu.RUnlock()
	ids := map[string]string{}     // path → fixed id: a builtin, or the user's copy of one
	shippedBy := map[string]bool{} // the builtins listed
	var shipped []string           // every builtin's path, listed or standing down
	for _, b := range builtin {
		shipped = append(shipped, b.Path)
		if !fsx.FileExists(b.Path) {
			continue // removed by the user (howto.MarkRemoved), or never written
		}
		if copyPath, ok := fileNamed(paths, filepath.Base(b.Path)); ok {
			// The user's copy stands in, under the builtin's id, so every
			// link written to the guide still reaches it. It is theirs:
			// not flagged builtin, removable like any file of theirs.
			ids[copyPath] = b.ID
			continue
		}
		ids[b.Path] = b.ID
		shippedBy[b.Path] = true
		paths = append(paths, b.Path)
	}
	fromLib := 0
	if useCached {
		// A builtin's library folder is never listed on its own: it belongs
		// to the builtin, or to nothing while the user's copy stands in.
		lib := libraryPaths(append(paths, shipped...))
		fromLib = len(lib)
		paths = append(paths, lib...)
	}
	r.mu.Lock()
	seen := map[string]bool{}
	// Rebuilt, not appended to: an id that no longer discovers to anything -
	// a dictionary deleted (D63) or a drive unmounted - must stop resolving,
	// or `get` would hand out an entry that is not in the list and a removed
	// dictionary would stay addressable by anyone still holding its id.
	byID := make(map[string]*entry, len(paths))
	var entries, kept, replaced []*entry
	for _, p := range paths {
		id, fixed := ids[p]
		if !fixed {
			id = pathID(p)
		}
		builtin := shippedBy[p]
		if seen[id] {
			continue
		}
		seen[id] = true
		e, ok := r.byID[id] // keep the open backend across a rescan
		if ok && e.Path != p {
			// A fixed id that now names another file: the built-in guide's,
			// taken over by the user's copy of it, or given back. The old
			// entry is closed like one the scan no longer finds.
			replaced = append(replaced, e)
			ok = false
		}
		if !ok {
			e = &entry{ID: id, Path: p, builtin: builtin, reg: r}
		} else {
			kept = append(kept, e)
		}
		byID[id] = e
		entries = append(entries, e)
	}
	// Entries the scan no longer finds. Dropping them from the map stops them
	// resolving but does not close them, and a dictionary deleted or unmounted
	// from outside the app would otherwise keep its backend open - descriptors
	// and, in preview mode, a headword map of hundreds of bytes per entry - with
	// nothing left in the registry able to reach it: the janitor sweeps
	// r.entries, which an entry that has just left it is no longer in.
	gone := replaced
	for id, e := range r.byID {
		if !seen[id] {
			gone = append(gone, e)
		}
	}
	r.byID = byID
	sort.Slice(entries, func(i, j int) bool {
		return strings.ToLower(entries[i].Path) < strings.ToLower(entries[j].Path)
	})
	r.entries = entries
	r.fromLib = fromLib
	r.roots = roots
	r.mu.Unlock()

	// Outside the registry lock: this stats the library and can close backends,
	// and holding the write lock across that would stall every search for the
	// duration of a filesystem walk. The lists are private to this call, and
	// each entry's own state is guarded by its own locks.
	for _, e := range kept {
		e.revalidate()
	}
	for _, e := range gone {
		if _, dropped := e.drop(true); dropped {
			logx.V("rescan: %s is gone; closed it", filepath.Base(e.Path))
		}
		// Nothing can reach a gone entry again, so its grace protects no one -
		// while on Windows the handle it would keep for closeGrace blocks
		// deleting the library folder it held (an orphan removed right after
		// the rescan that revealed it). See registry_windows.go.
		releaseSuperseded(e)
	}
	return nil
}

// Counts reports how many dictionaries came from the dictionary folder and
// how many from the library, so the startup summary can describe each folder
// by what it actually contributed instead of showing one blended total.
func (r *Registry) Counts() (fromFolder, fromLibrary int) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	builtin := 0
	for _, e := range r.entries {
		if e.builtin {
			builtin++
		}
	}
	return len(r.entries) - r.fromLib - builtin, r.fromLib
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
			// A DSL abbreviation glossary prepared by an older build. Its
			// content now belongs to its parent, so the folder is retired from
			// the list rather than shown; the folder itself is left on disk for
			// the user to remove through the panel like any other.
			if dict.IsAbbrevCompanion(e.Source) {
				continue
			}
		}
		out = append(out, e.TextDB)
	}
	return out
}

// builtinID reports whether id names a dictionary the app ships, as listed now.
func (r *Registry) builtinID(id string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.byID[id]
	return ok && e.builtin
}

// AddBuiltin lists one more of the app's own dictionaries (a restored guide);
// the next Rescan picks it up.
func (r *Registry) AddBuiltin(b Builtin) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, have := range r.builtin {
		if have.ID == b.ID {
			return
		}
	}
	r.builtin = append(r.builtin, b)
}

// IsBuiltin reports whether path is the source of a dictionary the app ships.
func (r *Registry) IsBuiltin(path string) bool {
	if path == "" {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, b := range r.builtin {
		if filepath.Clean(b.Path) == filepath.Clean(path) {
			return true
		}
	}
	return false
}

// fileNamed returns the first of paths that is a file named name, ignoring
// case as the file systems most dictionaries live on do.
func fileNamed(paths []string, name string) (string, bool) {
	for _, p := range paths {
		if strings.EqualFold(filepath.Base(p), name) {
			return p, true
		}
	}
	return "", false
}

// pathID derives a stable slash-free dictionary id from its path.
func pathID(path string) string {
	sum := sha256.Sum256([]byte(path))
	return hex.EncodeToString(sum[:])[:12]
}

// Warm pre-opens dictionaries in the background so the first search does not
// pay the open cost. It opens only the ones that are PREPARED - a SQLite handle
// costing a few MB - and deliberately leaves unprepared ones alone: opening
// those builds an in-memory headword index (measured 300–500 B per headword),
// and doing it for a whole library costs several GB of resident memory for
// dictionaries nobody has searched yet (docs.local/PERF.md M2). An unprepared
// dictionary is opened when something actually needs it: a search, or the
// background indexer that is about to replace it with a prepared one.
//
// Disabled dictionaries are skipped. Turning one off is the user asking us to
// stop spending on it, and a few MB of SQLite handle each is exactly the kind
// of spending they meant; opening them anyway would make the switch a
// decoration. One that is turned back on opens on its next search.
//
// Not on a phone, and not while the app is away (warmEnabled, CurrentPower):
// pre-opening is a bet that the user is about to search, paid in file
// descriptors, SQLite page caches and - worst on Android - a burst of I/O
// during the exact seconds the platform is measuring the app's launch cost.
// The bet is good on a desktop that just started a long-lived server; it is a
// bad one on a device that may be resuming an activity for a screen rotation.
// The first search opens what it needs, once.
func (r *Registry) Warm() {
	if !warmEnabled || CurrentPower() != PowerActive {
		return
	}
	entries := r.all()
	prefs := r.prefs
	go func() {
		sem := make(chan struct{}, 4)
		var wg sync.WaitGroup
		for _, e := range entries {
			if _, prepared := validPrepared(e.Path); !prepared {
				continue
			}
			if prefs.Off(e.ID, e.Path) {
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
// 3 GB depending on which 8 (docs.local/PERF.md §1).
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
	r.nudge() // a budget that just got smaller may already be exceeded
}

// SetSearchBudget sets how much preview memory a single search may materialise
// (config SEARCH_MEMORY; 0 = uncapped). Unlike the preview budget this needs no
// nudge: it applies to the next search, and changes nothing already open.
func (r *Registry) SetSearchBudget(bytes int64) {
	r.mu.Lock()
	r.searchBudget = bytes
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

// decoders lists the article decoders of prepared dictionaries. They are not
// preview memory and are not held to PREVIEW_MEMORY; sweep releases them only
// under memory pressure, when every byte that can be made again is shed.
func (r *Registry) decoders() []reclaimable {
	var out []reclaimable
	for _, e := range r.all() {
		e.dMu.RLock()
		d := e.d
		e.dMu.RUnlock()
		if h, ok := d.(decoderHolder); ok {
			if w, used := h.DecoderHeld(); w > 0 {
				out = append(out, reclaimable{used, w, h.ReleaseDecoder, "article decoder"})
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
//
// Under memory pressure (see memoryPressure) the rules change: the target
// becomes zero rather than the budget, and the idle grace is waived. That is
// the ONLY correct response to approaching a soft heap limit - the alternative,
// which is what a limit does on its own, is to collect continuously against a
// live set that no amount of collecting will shrink.
func (r *Registry) sweep() int64 {
	r.mu.RLock()
	budget := r.previewBudget
	r.mu.RUnlock()
	pressed := memoryPressure()
	if budget <= 0 && !pressed {
		return 0
	}
	target := budget
	if pressed {
		target = 0
	}
	var total int64
	var idle []reclaimable
	cutoff := time.Now().Add(-minEvictIdle).UnixNano()
	for _, c := range r.reclaimables() {
		total += c.bytes
		if pressed || c.used < cutoff {
			idle = append(idle, c)
		}
	}
	if pressed {
		for _, c := range r.decoders() {
			total += c.bytes
			idle = append(idle, c)
		}
	}
	if total <= target || len(idle) == 0 {
		return 0
	}
	sort.Slice(idle, func(i, j int) bool { return idle[i].used < idle[j].used }) // oldest first
	var freed int64
	for _, c := range idle {
		if total-freed <= target {
			break
		}
		freed += c.free()
	}
	if freed > 0 {
		logx.V("preview budget: reclaimed %d MB (was %d MB, budget %d MB, pressure=%v)",
			freed>>20, total>>20, budget>>20, pressed)
	}
	return freed
}

// needsSweep reports whether there is anything for the janitor to do at all.
//
// This is the whole point of the event-driven janitor: a periodic timer in a
// process that outlives its window is a wakeup the kernel must schedule, a core
// it must bring out of idle, and - on a phone, every twenty seconds, forever -
// a measurable battery cost for a function that in the overwhelmingly common
// case finds nothing to free. A sleeping goroutine costs nothing at all.
func (r *Registry) needsSweep() bool {
	r.mu.RLock()
	budget := r.previewBudget
	r.mu.RUnlock()
	var total int64
	n := 0
	for _, c := range r.reclaimables() {
		total += c.bytes
		n++
	}
	if n == 0 {
		// Nothing reclaimable. Ordinarily that is nothing to do - but under a
		// ceiling this workload cannot fit beneath, it is the one state that
		// must still be acted on, because the correction left is to the ceiling
		// rather than to the memory (adjustLimit). A ceiling already raised is
		// the same case seen from the other side: it must be handed back.
		return memoryPressure() || limitRelaxed()
	}
	if budget > 0 && total > budget {
		return true
	}
	return memoryPressure() || limitRelaxed()
}

// startJanitor keeps preview memory under the budget in the background. It
// runs only while there is something to reclaim; otherwise it blocks on wake,
// which nudge signals whenever a dictionary is opened, the budget changes, or
// the power state does.
func (r *Registry) startJanitor() {
	go func() {
		for {
			if !r.needsSweep() {
				<-r.wake
				continue
			}
			// a fresh timer per pass rather than a Ticker: the pass only
			// happens while there is work, so nothing is armed when idle
			select {
			case <-time.After(janitorPeriod):
			case <-r.wake:
			}
			r.sweep()
			adjustLimit() // and then judge the ceiling the sweep was working under
		}
	}()
}

// preparedDB names the prepared database for this entry: the resolution its
// open was checked against when there is one, else backingDB's stat-and-
// receipt answer. Deliberately NOT validPrepared, whose source-changed check
// is a SQLite open per call - and /res/ lands here for every resource on a
// page. Where the two disagree (the source was edited after indexing) the
// answer is still the folder the user would have put an override into:
// staleness is open-time business, not path business.
func (e *entry) preparedDB() (string, bool) {
	if store.IsTextDB(e.Path) {
		return e.Path, true
	}
	e.dMu.RLock()
	backing := e.backing
	e.dMu.RUnlock()
	if backing != "" {
		return backing, true
	}
	if p := backingDB(e.Path); p != "" {
		return p, true
	}
	return "", false
}

// backingDB names the prepared database that exists on disk for a dictionary
// path right now, or "" when there is none. It is the identity a cached open is
// checked against, so it answers from stat() and the folder's info.txt claim
// only.
//
// Deliberately not validPrepared: that one also asks whether the SOURCE has
// changed since it was indexed, which reads the meta table out of every
// candidate text.db - a SQLite open per dictionary, on a path a rescan walks
// once per entry. The two disagree on exactly one state (source edited, index
// still present), and there the cached open is stale but not broken: it serves
// the previous edition's articles until something re-ingests it. What this
// catches is the state that IS broken - the database the handle reads is gone.
func backingDB(path string) string {
	if store.IsTextDB(path) {
		if fsx.FileExists(path) {
			return path
		}
		return ""
	}
	if dir, ok := store.LookupDir(path); ok {
		return store.TextDBPath(dir) // LookupDir already required the text.db
	}
	return ""
}

// revalidate re-derives this entry against the filesystem and resets whatever
// no longer matches it. Called for every entry that survives a rescan, which is
// what makes "Rescan folders" mean reconcile rather than re-list.
//
// Two things are reset, for two different reasons.
//
// A memoized OPEN ERROR is always cleared. entry.open caches failures on
// purpose - a fan-out over a hundred dictionaries must not retry a broken file
// once per keystroke - and a rescan is the one thing that clears that cache:
// it is the user asking for exactly that retry, paid for once, here, however
// thoroughly they have fixed the file in between. A failure kept across
// restarts (openfail.go) answers the retry instead while its file and the
// build are unchanged; the user's own Rescan forgets those first
// (ForgetFailures), every other rescan keeps them.
//
// A backend resolved against a DIFFERENT database than the one now on disk is
// dropped, along with the conclusions drawn from it: autoTried and demanded
// both mean "this dictionary has been prepared, or has refused to be", and a
// library folder that no longer exists refutes both. Without this, deleting a
// prepared folder from the file manager would leave the entry holding a handle
// to it - answering searches until SQLite needs a new connection, then failing
// every one with "unable to open database file" - and neither preparation lane
// would rebuild it, because both would be marked as done. It is also what keeps
// the in-app removal path honest: Remove closes the backend and says the
// dictionary "will be indexed again the next time it is searched", which holds
// only because autoTried does not outlive the data it describes.
//
// The same holds for the SOURCE: a file replaced in place keeps its path and
// its library folder, so backing alone cannot see it. Its size and mtime are
// compared with the ones the backend was opened against (srcSig); a
// difference drops the handle, and the next open resolves afresh -
// store.PreparedFor no longer vouches for an index built from the old
// edition, so it is served from the new source until it is prepared again.
func (e *entry) revalidate() {
	now, sig := backingDB(e.Path), sourceSig(e.Path)
	e.dMu.Lock()
	e.err = nil
	changed := now != e.backing || (e.d != nil && e.srcSig != sig)
	open := e.d != nil
	if !changed || !open {
		// Nothing to let go of: record the resolution and be done. Doing it
		// under the same lock keeps "backing describes e.d" true for readers.
		e.backing, e.srcSig = now, sig
	}
	e.dMu.Unlock()
	if !changed {
		return
	}
	// what this entry's /api/dicts row was derived from - a backend, or a
	// prepared folder - is not what is there now (dictrows.go)
	e.gen.Add(1)
	if open {
		if _, dropped := e.drop(true); !dropped {
			// An ingest holds this entry. It ends in reopen(), which resolves
			// and records afresh, so leaving backing untouched here is not a
			// leak of the stale value - it is the next rescan's business if
			// that ingest fails instead.
			return
		}
		e.dMu.Lock()
		e.backing, e.srcSig = now, sig
		e.dMu.Unlock()
	}
	// The dictionary this entry describes is not the one its flags describe.
	e.autoTried.Store(false)
	e.demanded.Store(false)
	e.demandFail.Store(0)
	e.abbrevTried.Store(false)
	logx.V("rescan: %s changed underneath us (prepared=%v); reopening on next use",
		filepath.Base(e.Path), now != "")
}

// sourceSig is a cheap identity for a dictionary's source file: size and
// modification time, or "" when it cannot be stat'ed. For a prepared
// dictionary whose source is gone (a loose text.db) it describes the text.db
// itself, which is what that entry's backend reads.
func sourceSig(path string) string {
	st, err := os.Stat(path)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%d:%d", st.Size(), st.ModTime().UnixNano())
}

// SourcePaths lists the path of every dictionary the user has, in every
// configured folder - not the ones the app ships. What an import checks
// "already installed" against beyond its own folder (intake.Manager.Library).
func (r *Registry) SourcePaths() []string {
	var out []string
	for _, e := range r.all() {
		if !e.builtin {
			out = append(out, e.Path)
		}
	}
	return out
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

// features is the state a dictionary's prepared data can be in. Finding a
// headword is not among them: it needs no switch, costs ~2 MB, and every
// backend can do it. These two are the ones that cost real disk, so each is
// something the user turns on and off.
type features struct {
	FullText bool
	Media    bool
}

// reconcile brings this entry's prepared data to t (store.Reconcile) under the
// entry's own rules: one ingest at a time per dictionary; the served databases
// handed back, and opens barred, while they are replaced on Windows
// (releasePrepared); and the result served the moment it exists. The lane and
// the power hold are the caller's, because they depend on who is waiting.
//
// Nothing here opens the dictionary to rebuild its text: the ingest reader
// parses the file itself, and holding the direct backend at the same time
// doubles the working set of the largest thing in the process
// (docs.local/PERF.md M3). Only a media pack needs a backend (mediaBackend).
func (e *entry) reconcile(name string, t store.Target, progress store.Progress) (store.Outcome, error) {
	e.ingestMu.Lock()
	defer e.ingestMu.Unlock()
	defer e.rebuilding.Store(false) // releasePrepared may have armed it
	out, err := store.Reconcile(e.Path, t, store.Hooks{
		Release:       func(textDB string) { releasePrepared(e, textDB) },
		Backend:       e.mediaBackend,
		Progress:      progress,
		MediaProgress: progress,
	})
	if out.Rebuilt {
		if len(out.Why) > 0 {
			logx.V("%sprepared data was outdated (%v) - re-indexed", logx.Dict(name), out.Why)
		}
		logx.V("%s%d entries indexed (fullText=%v)",
			logx.Dict(name), out.Report.Entries, out.Plan.FullText)
		if out.Report.UnresolvedLinks > 0 {
			logx.V("%s%d redirects pointed at headwords not present in the source (skipped)",
				logx.Dict(name), out.Report.UnresolvedLinks)
		}
	}
	if out.Beside > 0 {
		logx.V("%s%d referenced files are not in the dictionary's own container - packed from beside it",
			logx.Dict(name), out.Beside)
	}
	switch {
	case out.MediaEmpty:
		e.setMediaEmpty(true) // the panel stops offering "pack media"
	case out.MediaRemoved:
		logx.V("%spacked media removed", logx.Dict(name))
		e.setMediaEmpty(false)
	}
	// Serve what is on disk now. Also after a failure that came after a
	// change - a media step failing behind a successful text rebuild - or the
	// served handle would go on reading the replaced text.db.
	e.dMu.RLock()
	stale := e.d != nil && e.backing != out.TextDB
	e.dMu.RUnlock()
	var rerr error
	if out.Changed() || (err == nil && stale) {
		rerr = e.reopen()
	}
	// What this entry's /api/dicts row is derived from moved - or may have,
	// on a failure - and is served afresh: after the reopen, so the row
	// describes the result (dictrows.go). A reconcile that found everything
	// as wanted, as the startup abbreviation sweep usually does, leaves the
	// kept row and the client's validator standing.
	if out.Changed() || stale || err != nil {
		e.gen.Add(1)
	}
	if err != nil {
		return out, fmt.Errorf("preparing %q: %w", name, err)
	}
	return out, rerr
}

func (e *entry) setMediaEmpty(v bool) {
	e.dMu.Lock()
	e.mediaEmpty = v
	e.dMu.Unlock()
}

// mediaBackend is what a media pack reads resources from: the backend this
// entry serves when there is one - for a prepared dictionary, its lazily opened
// handle on the source - else a handle of the pack's own, closed after it. A
// backend that releasePrepared took away for the rebuild is no longer in e.d,
// so a closed one is never handed out.
func (e *entry) mediaBackend() (dict.Dictionary, func(), error) {
	e.dMu.RLock()
	cur := e.d
	e.dMu.RUnlock()
	switch d := cur.(type) {
	case nil:
	case *upgraded:
		src, err := d.source()
		return src, func() {}, err
	default:
		return d, func() {}, nil
	}
	d, err := dict.Open(e.Path)
	if err != nil {
		return nil, nil, err
	}
	return d, func() { d.Close() }, nil
}

// setFeatures brings a dictionary's prepared data to the requested state:
// building or rebuilding the index when the wanted indexes differ from the
// built ones or the data is outdated, packing media, or deleting media that is
// no longer wanted. Rebuilds are the same atomic temp+rename as any ingest, so
// an interrupted change leaves the previous data intact.
//
// Stripping is only ever offered while the SOURCE exists - that is what makes
// it reversible, and it is why none of this needs a confirmation prompt. A
// dictionary whose source is gone carries the only copy of its own text, so
// its features are locked rather than dangerous.
func (e *entry) setFeatures(want features, progress store.Progress) error {
	defer dict.MarkUrgent(e.Path)() // before any wait, so a running preparation scales up (D182)
	defer acquire(frontLimit)()     // the user is waiting: never queue behind background work
	// The user ticked a box and is watching a progress bar: the longest
	// user-visible operation the app has keeps every core it started with,
	// and the Android shell keeps its foreground service up (power.go).
	// Refcounted, so nesting with a concurrent demand is safe.
	defer HoldActiveProcs()()
	name := e.probeName()
	if store.IsTextDB(e.Path) {
		return fmt.Errorf("%q is a prepared dictionary - its original files are gone, so its data cannot be rebuilt", name)
	}
	media := store.MediaOff
	if want.Media {
		media = store.MediaOn
	}
	// A dictionary the user is already changing is brought current on the
	// way; one they are not is left alone until they ask (D151).
	_, err := e.reconcile(name, store.Target{
		FullText: &want.FullText, Media: media, Rebuild: store.IfOutdated,
	}, progress)
	return err
}

// reopen swaps in a view of the freshly written data and lets go of the old
// one. The superseded handle is usually a DIRECT backend holding a headword map
// worth hundreds of bytes per entry; left to the garbage collector, that memory
// would stay resident for the life of the process (docs.local/PERF.md M2). It is
// closed after a grace period so requests already reading from it finish first.
func (e *entry) reopen() error {
	sig := sourceSig(e.Path)
	fresh, err := openUpgradedOrDirect(e.Path)
	if err != nil {
		return err
	}
	e.dMu.Lock()
	old := e.d
	e.d, e.err, e.backing, e.srcSig = fresh, nil, backingDB(e.Path), sig
	// the weight must follow the view, not the one it replaced: a prepared
	// dictionary holds no headword map, so it weighs nothing and must never
	// become an eviction candidate
	e.weight.Store(previewWeight(fresh, fresh.Meta()))
	e.dMu.Unlock()
	// The new view resolves resources differently (media.db first, D2), so the
	// stylesheet is read again rather than trusted from the backend it replaced.
	e.forgetStyles()
	if old != nil && old != fresh {
		// preparing is the memory high-water mark; the close hands the pages
		// back rather than sitting on them until the next natural GC -
		// coalesced with any other close landing at the same moment, since
		// INDEX_WORKERS>1 makes that the normal case
		e.retired.retire(old)
	}
	return nil
}

// closeGrace is how long a superseded backend stays open for in-flight reads.
// Ten seconds is far beyond any article fetch; the memory it holds (hundreds of
// bytes per headword) is worth reclaiming promptly.
const closeGrace = 10 * time.Second

// retiring is the set of superseded backends sitting out closeGrace. The grace
// is for in-flight readers; a caller that needs every handle on the files gone
// now - removal and a rebuild's rename, which Windows refuses over an open
// file - closes the lot early with closeAll. Each backend closes exactly once,
// whichever comes first. The zero value is ready to use.
type retiring struct {
	mu      sync.Mutex
	pending map[*retiree]struct{}
}

type retiree struct {
	once sync.Once
	c    io.Closer
}

// close blocks a concurrent caller until the one close completes, so closeAll
// never returns while the timer is still mid-close.
func (r *retiree) close() {
	r.once.Do(func() {
		r.c.Close()
		scheduleReclaim()
	})
}

// retire closes c after closeGrace, or at the next closeAll.
func (rs *retiring) retire(c io.Closer) {
	r := &retiree{c: c}
	rs.mu.Lock()
	if rs.pending == nil {
		rs.pending = make(map[*retiree]struct{})
	}
	rs.pending[r] = struct{}{}
	rs.mu.Unlock()
	time.AfterFunc(closeGrace, func() {
		r.close() // before the delete: closeAll must find it or find it closed
		rs.mu.Lock()
		delete(rs.pending, r)
		rs.mu.Unlock()
	})
}

// closeAll closes every backend still in its grace, now.
func (rs *retiring) closeAll() {
	rs.mu.Lock()
	pending := rs.pending
	rs.pending = nil
	rs.mu.Unlock()
	for r := range pending {
		r.close()
	}
}

// upgradeAbbrev re-indexes dictionaries prepared before their abbreviation
// glossary could be absorbed. DSL bakes those expansions into the article HTML
// at ingest, so there is no way to add them to data already on disk; the only
// fix is to build it again.
//
// This is the one sweep in the program that starts work nobody asked for on a
// whole library, so where it runs matters more than what it does. It takes the
// BACKGROUND lane (indexLimit, one at a time, FIFO), never the front one, and
// never runs from Warm or open - a rebuild triggered by pre-opening would turn
// a startup into an hours-long, invisible re-index of everything. Here it is a
// drip: one dictionary at a time, deferred entirely while the process is not
// active, and each one attempted once per process.
//
// Only dictionaries whose companion is present on disk are swept. The reverse
// case - a companion deleted after it was absorbed - would cost a meta read per
// dictionary in the library to detect, and is caught by setFeatures the next
// time anything about that dictionary is changed.
func (r *Registry) upgradeAbbrev() {
	var todo []*entry
	for _, e := range r.all() {
		if store.IsTextDB(e.Path) {
			continue
		}
		if _, ok := dict.AbbrevCompanion(e.Path); !ok {
			continue
		}
		if e.abbrevTried.Load() {
			continue
		}
		todo = append(todo, e)
	}
	if len(todo) == 0 {
		return
	}
	go func() {
		for _, e := range todo {
			release := acquire(indexLimit)
			// the queue is FIFO and an ingest takes minutes, so the state that
			// permitted this may be long gone by the time the slot is ours
			if CurrentPower() != PowerActive {
				release()
				return
			}
			// Claimed HERE, not at collection. Marking the whole list up front
			// and then abandoning it on a power transition would leave every
			// entry behind this one flagged as tried and never tried - and the
			// sweep runs only from NewRegistry/SetUseCached/SetDirs, so
			// "attempted once per process" would become "attempted never" for
			// the tail, which on a phone, where the screen goes off mid-ingest
			// as a matter of course, is the normal case. Claiming at the start
			// of the work also keeps a transient failure - a full disk - from
			// being made permanent by an entry that never got its turn.
			if !e.abbrevTried.CompareAndSwap(false, true) {
				release()
				continue
			}
			err := e.reabsorbAbbrev()
			release()
			if err != nil {
				logx.Warn("could not re-index %s for abbreviations: %v", filepath.Base(e.Path), err)
			}
		}
	}()
}

// reabsorbAbbrev rebuilds this dictionary's prepared data so its articles carry
// the abbreviation glossary again. DSL bakes the expansions into the article
// HTML at ingest, so data already on disk cannot gain them; the only fix is to
// build it again, with the plan it already has - full text survives.
func (e *entry) reabsorbAbbrev() error {
	if store.IsTextDB(e.Path) {
		return nil
	}
	dir, ok := store.LookupDir(e.Path)
	if !ok {
		return nil // not prepared yet: its first ingest absorbs the companion
	}
	companion, _ := dict.AbbrevCompanion(e.Path)
	if !store.AbbrevChanged(store.TextDBPath(dir), companion) {
		return nil
	}
	_, err := e.reconcile(e.probeName(), store.Target{Rebuild: abbrevChanged}, nil)
	return err
}

func abbrevChanged(why []store.Reason) bool { return slices.Contains(why, store.ReasonAbbrev) }

// ensureBaseIndex builds the cheap find-only index when a dictionary has none
// (D13's silent auto-index). It never strips: a dictionary that is "not
// prepared" only because its source changed, or because its text.db is one
// this build cannot open, is rebuilt with the plan it already had - the user's
// full text is not quietly demoted by a rebuild they did not ask for. Media is
// left to the user: an orphaned media.db degrades to serving from the source,
// and the dictionary is reported outdated until they rebuild it.
func (e *entry) ensureBaseIndex(progress store.Progress) error {
	if _, ok := validPrepared(e.Path); ok {
		return nil // prepared already, at whatever level its owner chose
	}
	_, err := e.reconcile(e.probeName(), store.Target{}, progress)
	return err
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
