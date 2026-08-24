// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Package search fans one query out over many dictionaries concurrently
// (draego's "all" mode, minus its sequential per-request DB opens -
// FTS-audit #6: dictionaries stay open, queries run in parallel).
package search

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/wuweidict/wudict/internal/dict"
)

// Mode selects the query type; dictionaries lacking the capability are
// skipped (Hit.Skipped).
type Mode int

const (
	Exact Mode = iota
	Prefix
	Contains
	FullText
)

func (m Mode) String() string {
	return [...]string{"exact", "prefix", "contains", "fts"}[m]
}

// Hit is the result of one dictionary. Order of hits mirrors the input
// dictionary order regardless of completion order.
type Hit struct {
	Meta    dict.Meta
	Results []dict.Result
	Err     error
	Skipped bool // dictionary does not support the requested mode
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

// All queries every dictionary with term, at most perDict results each.
func All(ctx context.Context, dicts []dict.Dictionary, mode Mode, term string, perDict int) []Hit {
	hits := make([]Hit, len(dicts))
	sem := make(chan struct{}, Workers())
	var wg sync.WaitGroup
	for i, d := range dicts {
		wg.Add(1)
		go func(i int, d dict.Dictionary) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				hits[i] = Hit{Meta: d.Meta(), Err: ctx.Err()}
				return
			}
			if err := ctx.Err(); err != nil {
				hits[i] = Hit{Meta: d.Meta(), Err: err}
				return
			}
			hits[i] = query(d, mode, term, perDict)
		}(i, d)
	}
	wg.Wait()
	return hits
}

// Stream queries every dictionary concurrently (bounded) and invokes emit
// once per dictionary as its query completes. emit calls are serialized
// (safe to write to a shared response) but arrive in completion order, not
// input order - i is the dictionary's index in dicts so the caller can
// place each result in its preference-ordered slot. Blocks until done.
func Stream(ctx context.Context, dicts []dict.Dictionary, mode Mode, term string, perDict int, emit func(i int, h Hit)) {
	openers := make([]Opener, len(dicts))
	for i, d := range dicts {
		d := d
		openers[i] = func() (dict.Dictionary, error) { return d, nil }
	}
	StreamOpen(ctx, openers, mode, term, perDict, emit)
}

// Opener lazily opens one dictionary. StreamOpen calls it inside the worker
// goroutine so the caller can flush its "begin" line before paying any open
// cost. On open failure the worker emits a Hit with Err set and a zero Meta -
// the caller supplies the dictionary id out of band (by slot index).
type Opener func() (dict.Dictionary, error)

// StreamOpen is Stream with the per-dictionary open deferred into each worker.
// This lets an HTTP handler emit its slot layout immediately (from cheap ids)
// instead of serializing every cold open before the first byte: TTFB becomes
// one open, not the sum of all of them. emit calls are serialized (safe for a
// shared response) but arrive in completion order; i is the input index.
func StreamOpen(ctx context.Context, openers []Opener, mode Mode, term string, perDict int, emit func(i int, h Hit)) {
	sem := make(chan struct{}, Workers())
	var wg sync.WaitGroup
	var mu sync.Mutex
	send := func(i int, h Hit) {
		mu.Lock()
		emit(i, h)
		mu.Unlock()
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
			// index - measured on a phone (docs/PERF.md §8.7), three abandoned
			// fan-outs over 24 preview dictionaries took 90s each and drove RSS
			// to 1.0 GB, all of it for output nobody would read. Stopping here
			// bounds the damage of a cancelled search to the opens already in
			// flight.
			if err := ctx.Err(); err != nil {
				send(i, Hit{Err: err})
				return
			}
			d, err := open()
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
			send(i, query(d, mode, term, perDict))
		}(i, open)
	}
	wg.Wait()
}

func query(d dict.Dictionary, mode Mode, term string, perDict int) Hit {
	h := Hit{Meta: d.Meta()}
	caps := d.Caps()
	switch mode {
	case Exact:
		if !caps.Exact {
			h.Skipped = true
			return h
		}
		h.Results, h.Err = d.Exact(term, perDict)
	case Prefix:
		if !caps.Prefix {
			h.Skipped = true
			return h
		}
		h.Results, h.Err = d.Prefix(term, perDict)
	case Contains:
		f, ok := d.(dict.ContainsSearcher)
		if !ok || !caps.Contains {
			h.Skipped = true
			return h
		}
		h.Results, h.Err = f.Contains(term, perDict)
	case FullText:
		f, ok := d.(dict.FullTextSearcher)
		if !ok || !caps.FTS {
			h.Skipped = true
			return h
		}
		h.Results, h.Err = f.FullText(term, perDict)
	}
	return h
}
