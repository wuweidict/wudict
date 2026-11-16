// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Package search fans one query out over many dictionaries concurrently
// (draego's "all" mode, minus its sequential per-request DB opens -
// FTS-audit #6: dictionaries stay open, queries run in parallel).
package search

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/wuweidict/wudict/internal/dict"
	"github.com/wuweidict/wudict/internal/ftsq"
	"github.com/wuweidict/wudict/internal/hilite"
)

// Mode selects the query type; dictionaries lacking the capability are
// skipped (Hit.Skipped).
type Mode int

const (
	Exact Mode = iota
	Prefix
	FullText
	Wildcard
)

func (m Mode) String() string {
	return [...]string{"exact", "prefix", "fts", "wildcard"}[m]
}

// ParseMode reads a mode by its API name. "" is prefix, the default; "fuzzy"
// is the retired name of what prefix now does (D16) and is still accepted.
func ParseMode(s string) (Mode, error) {
	switch s {
	case "", "prefix", "fuzzy":
		return Prefix, nil
	case "exact":
		return Exact, nil
	case "fts":
		return FullText, nil
	case "wildcard":
		return Wildcard, nil
	}
	return 0, fmt.Errorf("unknown mode %q", s)
}

// Query searches one dictionary the way the fan-out searches each of its
// members - the same plan, the same ladder, the same capability rules - so a
// command-line lookup and the app cannot answer the same question differently.
func Query(ctx context.Context, d dict.Dictionary, mode Mode, term string, limit int) Hit {
	return query(ctx, d, mode, term, planFor(mode, term), limit, false)
}

// Hit is the result of one dictionary. Order of hits mirrors the input
// dictionary order regardless of completion order.
type Hit struct {
	Meta    dict.Meta
	Results []dict.Result
	Err     error
	Skipped bool // dictionary does not support the requested mode

	// Term is what was actually searched for. It is normally the caller's
	// term, and differs only on the lemma wave, where the caller needs to know
	// which word these results answer in order to mark them.
	Term string

	// Rung names which reading of a full-text query produced these results -
	// "phrase", "near" or "words" (internal/ftsq). Empty for every other mode
	// and for a backend that has only one reading. The relaxation is PER
	// DICTIONARY: one may answer the phrase while the next needs the words, so
	// this belongs on the hit and not on the request.
	Rung string

	// Marks, when set, are the spans to mark in Results instead of the
	// rung's own: an archive's index matches a word by its stem, and reports
	// the forms it found (ftsq.Answer).
	Marks []hilite.Phrase

	// Partial says a reading stricter than Rung was given up on with
	// candidates unread (ftsq.Answer).
	Partial bool

	// More says Rung was itself answered with candidates unread
	// (ftsq.Answer).
	More bool
}

// workers bounds how many dictionaries are queried at once. Eight is right for
// a desktop: the queries are I/O-bound, so oversubscribing the cores hides seek
// latency and costs nothing that matters there.
//
// It is wrong for a phone, where eight concurrent SQLite readers means eight
// page caches, eight threads the scheduler must place, and a burst of parallel
// I/O that reads to the platform exactly like a misbehaving app - for a query
// whose answer a human then spends seconds reading. The Android startup path
// sets this to the same number as GOMAXPROCS (D64).
const defaultWorkers = 8

var workers atomic.Int32 // 0 = unset, see Workers

// SetWorkers sizes the fan-out. Values below one are ignored rather than
// clamped silently to a fixed floor, because "no parallelism" is not a
// configuration this code can honour: one worker is the floor.
func SetWorkers(n int) {
	if n < 1 {
		return
	}
	workers.Store(int32(n))
}

// Workers reports the current fan-out width, so the other place that touches
// every dictionary at once (the dictionary list) can use the same number.
func Workers() int {
	if n := int(workers.Load()); n > 0 {
		return n
	}
	return defaultWorkers
}

// Opener lazily opens one dictionary. StreamOpen calls it inside the worker
// goroutine so the caller can flush its "begin" line before paying any open
// cost. On open failure the worker emits a Hit with Err set and a zero Meta -
// the caller supplies the dictionary id out of band (by slot index).
type Opener func() (dict.Dictionary, error)

// StreamOpen queries every dictionary concurrently (bounded by Workers) and
// calls emit once per dictionary as its query completes. The open is deferred
// into each worker, so an HTTP handler can emit its slot layout immediately
// (from cheap ids) instead of serializing every cold open before the first
// byte: TTFB becomes one open, not the sum of all of them. emit calls are
// serialized (safe for a shared response) but arrive in completion order; i
// is the input index, so the caller places each result in its slot.
func StreamOpen(ctx context.Context, openers []Opener, mode Mode, term string, perDict int, emit func(i int, h Hit)) {
	stream(ctx, openers, mode, term, perDict, false, emit)
}

// StreamKeys is StreamOpen for the list of matching words (D199): the same
// fan-out, and every Result is a Headword with no Body. A prepared database
// reads its headword index only (dict.Keyer); any other backend runs its
// ordinary query and the articles are dropped. For Exact, Prefix and
// Wildcard: a full-text match is not a headword.
func StreamKeys(ctx context.Context, openers []Opener, mode Mode, term string, perDict int, emit func(i int, h Hit)) {
	stream(ctx, openers, mode, term, perDict, true, emit)
}

func stream(ctx context.Context, openers []Opener, mode Mode, term string, perDict int, keys bool, emit func(i int, h Hit)) {
	plan := planFor(mode, term)
	sem := make(chan struct{}, Workers())
	var wg sync.WaitGroup
	var mu sync.Mutex
	// emit does the caller's rendering - reference rewriting, highlighting,
	// encoding - and it does it HERE, on the worker's own goroutine. That is
	// the one place a panic is not the HTTP handler's to recover: net/http
	// guards the goroutine it created, this is not that goroutine, and Go's
	// rule that a panic can only be recovered where it runs means the process
	// dies and every other request with it. The same rule query() applies to
	// the parsers one layer down therefore has to hold one layer up: one bad
	// article costs one row.
	//
	// The replacement hit carries no results, so the rendering that failed is
	// not attempted again on the way out; the inner recover is there because a
	// caller whose emit panicked once may panic reporting it, and a panic in a
	// deferred report would defeat the whole guard.
	send := func(i int, h Hit) {
		mu.Lock()
		defer mu.Unlock()
		defer func() {
			r := recover()
			if r == nil {
				return
			}
			defer func() { _ = recover() }()
			emit(i, Hit{Meta: h.Meta, Term: h.Term, Err: dict.PanicError(h.Meta.Path, r)})
		}()
		emit(i, h)
	}
	for i, open := range openers {
		wg.Add(1)
		go func(i int, open Opener) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				send(i, Hit{Err: ctx.Err()})
				return
			}
			// Re-check after the wait. Acquiring the semaphore says only that a
			// slot came free, not that the answer is still wanted: the queue is
			// long (every dictionary at once) and the slots are few, so by the
			// time a worker is admitted the request that asked for it may be
			// minutes dead. The web UI aborts the in-flight fetch on every
			// keystroke (index.html, searchAC), which makes "the caller has
			// gone" the common case rather than the exceptional one.
			//
			// What it costs to miss this is not a wasted CPU slice. open() on a
			// direct backend materialises that dictionary's whole in-memory
			// index - measured on a phone (docs.local/PERF.md §8.7), three abandoned
			// fan-outs over 24 preview dictionaries took 90s each and drove RSS
			// to 1.0 GB, all of it for output nobody would read. Stopping here
			// bounds the damage of a cancelled search to the opens already in
			// flight.
			if err := ctx.Err(); err != nil {
				send(i, Hit{Err: err})
				return
			}
			d, err := safeOpen(open)
			if err != nil {
				send(i, Hit{Err: err})
				return
			}
			if err := ctx.Err(); err != nil {
				// Cancelled during the open. The dictionary is now materialised
				// either way; skip only the query, and let the janitor shed it.
				send(i, Hit{Err: err})
				return
			}
			send(i, runQuery(ctx, d, mode, term, plan, perDict, keys))
		}(i, open)
	}
	wg.Wait()
}

// runQuery runs one dictionary's query and waits for it, unless the context
// dies first - then the query is ABANDONED, not waited on.
//
// The context reaches a prepared database's query (dict.Searcher), which SQLite
// then interrupts; it cannot reach a direct backend, which is a plain parser.
// Waiting for a wedged one would hold the whole fan-out - wg.Wait() waits for
// every dictionary, so one crawling search would stall every other result
// behind it. Abandoning bounds the wedge to one goroutine holding one backend,
// which the janitor eventually closes underneath it; the buffered send means
// the abandoned goroutine also ends without blocking whenever its query
// finally does.
func runQuery(ctx context.Context, d dict.Dictionary, mode Mode, term string, plan []ftsq.Rung, perDict int, keys bool) Hit {
	done := make(chan Hit, 1)
	go func() { done <- query(ctx, d, mode, term, plan, perDict, keys) }()
	select {
	case h := <-done:
		return h
	case <-ctx.Done():
		// A finished query outranks the cancellation that landed in the
		// same scheduler tick: the work is done and the answer exists, so
		// a slot reporting "canceled" over a result it is holding would
		// be a lie about the work.
		select {
		case h := <-done:
			return h
		default:
			return Hit{Meta: d.Meta(), Err: ctx.Err()}
		}
	}
}

// safeOpen runs a caller-supplied opener under the same panic conversion as
// the query. dict.Open already recovers internally, but an Opener is a closure
// the server builds (registry lookup, store open, format dispatch) and it runs
// on the worker goroutine, where an escaping panic is fatal to the process.
func safeOpen(open Opener) (d dict.Dictionary, err error) {
	defer func() {
		if r := recover(); r != nil {
			d, err = nil, dict.PanicError("dictionary", r)
		}
	}()
	return open()
}

// planFor parses a full-text query ONCE per fan-out. Parsing it per dictionary
// would be cheap and still wrong: a hundred dictionaries must answer the same
// question, and a query is not a per-dictionary object.
func planFor(mode Mode, term string) []ftsq.Rung {
	if mode != FullText {
		return nil
	}
	return ftsq.Parse(term).Rungs()
}

// query runs one dictionary's lookup, converting a parser panic into that
// dictionary's error.
//
// The recover is not defensive programming against bugs in this package: it is
// the same policy internal/dict already applies at Open, applied at the other
// end of the same parsers. A corrupt record block reaches a slice expression
// whose bounds came out of the file, and the fan-out runs it in a goroutine
// the HTTP handler did not create - so Go's rule that a panic can only be
// recovered on its own goroutine means the handler's recover cannot see it and
// the process dies. One bad file must cost one row, not the server.
//
// With keys the answer is headwords only (StreamKeys).
func query(ctx context.Context, d dict.Dictionary, mode Mode, term string, plan []ftsq.Rung, perDict int, keys bool) (h Hit) {
	defer func() {
		if r := recover(); r != nil {
			h.Err = dict.PanicError(h.Meta.Path, r)
			h.Results = nil
		}
	}()
	h = Hit{Meta: d.Meta(), Term: term}
	caps := d.Caps()
	// A prepared database answers every mode through its context-aware
	// surface; a direct backend has only Exact and Prefix, and no way to stop.
	s, indexed := d.(dict.Searcher)
	k, keyed := d.(dict.Keyer)
	keyed = keyed && keys
	switch mode {
	case Exact:
		if !caps.Exact {
			h.Skipped = true
			return h
		}
		if indexed {
			h.Results, h.Err = s.ExactContext(ctx, term, perDict)
		} else {
			h.Results, h.Err = d.Exact(term, perDict)
		}
	case Prefix:
		if !caps.Prefix {
			h.Skipped = true
			return h
		}
		switch {
		case keyed:
			h.Results, h.Err = headwords(k.PrefixKeys(ctx, term, perDict))
		case indexed:
			h.Results, h.Err = s.PrefixContext(ctx, term, perDict)
		default:
			h.Results, h.Err = d.Prefix(term, perDict)
		}
	case FullText:
		if !indexed || !caps.FTS {
			// No full-text index of ours: an archive that carries its own
			// answers the ladder itself (D194).
			p, ok := d.(ftsq.Planner)
			if !ok || len(plan) == 0 {
				h.Skipped = true
				return h
			}
			a, err := p.FullTextPlan(ctx, plan, perDict)
			if errors.Is(err, dict.ErrUnsupported) {
				h.Skipped = true
				return h
			}
			h.Results, h.Rung, h.Marks, h.Partial, h.More, h.Err = a.Results, a.Rung, a.Marks, a.Partial, a.More, err
			return h
		}
		if len(plan) > 0 && runPlan(ctx, s, plan, perDict, &h) {
			return h
		}
		// No plan (the query has no words), or every rung failed to execute -
		// a defect in the lowering, not an answer. The store's own single
		// reading serves the reader better than an error.
		h.Results, h.Err = s.FullTextContext(ctx, term, perDict)
		if h.Err == nil {
			h.Rung = "words"
		}
	case Wildcard:
		if !indexed || !caps.Wildcard {
			h.Skipped = true
			return h
		}
		if keyed {
			h.Results, h.Err = headwords(k.WildcardKeys(ctx, term, perDict))
		} else {
			h.Results, h.Err = s.WildcardContext(ctx, term, perDict)
		}
	}
	if keys {
		for i := range h.Results {
			h.Results[i].Body = ""
		}
	}
	return h
}

// headwords is a list of keys as results without articles.
func headwords(ws []string, err error) ([]dict.Result, error) {
	if err != nil || len(ws) == 0 {
		return nil, err
	}
	out := make([]dict.Result, len(ws))
	for i, w := range ws {
		out[i].Headword = w
	}
	return out, nil
}

// runPlan walks the relaxation ladder and stops at the FIRST rung that returns
// anything, reporting whether any rung executed at all.
//
// Descent is on EMPTINESS, never on "few results". A phrase that matched three
// articles has answered the question; adding the bag-of-words hits underneath
// it would bury the three that were actually asked for, which is the failure
// mode this whole design exists to remove. An error is not emptiness either -
// it means this reading was never tried, so the ladder keeps going and the
// caller is told only if nothing ran.
func runPlan(ctx context.Context, s dict.Searcher, plan []ftsq.Rung, perDict int, h *Hit) bool {
	ran := false
	for _, r := range plan {
		res, err := s.FullTextMatch(ctx, r.Match, perDict)
		if err != nil {
			h.Err = err
			continue
		}
		ran = true
		if len(res) > 0 {
			h.Results, h.Rung, h.Err = res, r.Name, nil
			return true
		}
	}
	if ran {
		h.Err = nil // the ladder ran to the end and the answer is "nothing"
	}
	return ran
}
