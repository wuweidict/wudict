// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Package search fans one query out over many dictionaries concurrently
// (draego's "all" mode, minus its sequential per-request DB opens —
// FTS-audit #6: dictionaries stay open, queries run in parallel).
package search

import (
	"context"
	"sync"

	"github.com/glowinthedark/gonow-dict/internal/dict"
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

const defaultWorkers = 8

// All queries every dictionary with term, at most perDict results each.
func All(ctx context.Context, dicts []dict.Dictionary, mode Mode, term string, perDict int) []Hit {
	hits := make([]Hit, len(dicts))
	sem := make(chan struct{}, defaultWorkers)
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
			hits[i] = query(d, mode, term, perDict)
		}(i, d)
	}
	wg.Wait()
	return hits
}

// Stream queries every dictionary concurrently (bounded) and invokes emit
// once per dictionary as its query completes. emit calls are serialized
// (safe to write to a shared response) but arrive in completion order, not
// input order — i is the dictionary's index in dicts so the caller can
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
// cost. On open failure the worker emits a Hit with Err set and a zero Meta —
// the caller supplies the dictionary id out of band (by slot index).
type Opener func() (dict.Dictionary, error)

// StreamOpen is Stream with the per-dictionary open deferred into each worker.
// This lets an HTTP handler emit its slot layout immediately (from cheap ids)
// instead of serializing every cold open before the first byte: TTFB becomes
// one open, not the sum of all of them. emit calls are serialized (safe for a
// shared response) but arrive in completion order; i is the input index.
func StreamOpen(ctx context.Context, openers []Opener, mode Mode, term string, perDict int, emit func(i int, h Hit)) {
	sem := make(chan struct{}, defaultWorkers)
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
			d, err := open()
			if err != nil {
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
