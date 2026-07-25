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
	Fuzzy
	FullText
)

func (m Mode) String() string {
	return [...]string{"exact", "prefix", "fuzzy", "fts"}[m]
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
	sem := make(chan struct{}, defaultWorkers)
	var wg sync.WaitGroup
	var mu sync.Mutex
	send := func(i int, h Hit) {
		mu.Lock()
		emit(i, h)
		mu.Unlock()
	}
	for i, d := range dicts {
		wg.Add(1)
		go func(i int, d dict.Dictionary) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				send(i, Hit{Meta: d.Meta(), Err: ctx.Err()})
				return
			}
			send(i, query(d, mode, term, perDict))
		}(i, d)
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
	case Fuzzy:
		f, ok := d.(dict.FuzzySearcher)
		if !ok || !caps.Fuzzy {
			h.Skipped = true
			return h
		}
		h.Results, h.Err = f.Fuzzy(term, perDict)
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
