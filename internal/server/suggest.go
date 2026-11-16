// Copyright (C) 2026 glowinthedark
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/wuweidict/wudict/internal/search"
	"github.com/wuweidict/wudict/internal/store"
	"github.com/wuweidict/wudict/internal/wildcard"
)

const (
	// suggestMax is the length of the list, and what each dictionary is asked
	// for: a dictionary's first suggestMax keys hold every one of its keys
	// that can be among the first suggestMax of them all.
	suggestMax = 20
	// suggestBudget is how long the list waits for the dictionaries. One that
	// has not answered by then is left out, and is open by the next keystroke.
	suggestBudget = 2 * time.Second
)

type suggestResponse struct {
	Words []string `json:"words"`
}

// handleSuggest lists the headwords a search would match, as one list for
// all the dictionaries asked: what the page shows under the search box while
// a word is typed (D199). It is the search fan-out with the articles left out
// (search.StreamKeys), so a dictionary is opened, capped and indexed exactly
// as a search of it would.
//
// `mode` is prefix or wildcard. Exact has no list of its own - a word still
// being typed matches nothing exactly - and a full-text match is not a
// headword.
func (s *Server) handleSuggest(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		httpErr(w, 400, "missing q parameter")
		return
	}
	mode, err := search.ParseMode(r.URL.Query().Get("mode"))
	if err != nil {
		httpErr(w, 400, "%v", err)
		return
	}
	switch mode {
	case search.Prefix:
	case search.Wildcard:
		if _, err := wildcard.Parse(q); err != nil {
			httpErr(w, 400, "%v", err)
			return
		}
	default:
		httpErr(w, 400, "mode %q has no list of words", mode)
		return
	}
	entries, ok := s.searchEntries(w, r.URL.Query().Get("dict"))
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), suggestBudget)
	defer cancel()
	// The fan-out returns when its last open does, and an open cannot be
	// interrupted: the answer leaves at the deadline with what has arrived,
	// and the fan-out ends by itself behind it.
	var (
		mu    sync.Mutex
		words []string
		done  = make(chan struct{})
	)
	go func() {
		defer close(done)
		search.StreamKeys(ctx, s.searchOpeners(entries), mode, q, suggestMax, func(_ int, h search.Hit) {
			mu.Lock()
			defer mu.Unlock()
			for _, r := range h.Results {
				words = append(words, r.Headword)
			}
		})
	}()
	select {
	case <-done:
	case <-ctx.Done():
	}
	mu.Lock()
	out := mergeWords(q, words, suggestMax)
	mu.Unlock()
	writeJSON(w, suggestResponse{Words: out})
}

// mergeWords makes one list of at most n words out of the dictionaries' keys:
// in dictionary order (store.Collate), which puts the word typed in full
// before the words it starts, and each word once whatever its capitals - a
// dictionary that files its headwords in capitals adds no second row. The
// spelling typed is the one kept when a dictionary has it; otherwise the first
// in that order, the lowercase one. It sorts words in place.
func mergeWords(q string, words []string, n int) []string {
	slices.SortFunc(words, store.Collate)
	out := make([]string, 0, min(n, len(words)))
	at := map[string]int{}
	for _, w := range words {
		if w = strings.TrimSpace(w); w == "" {
			continue
		}
		k := strings.ToLower(w)
		if i, ok := at[k]; ok {
			if w == q {
				out[i] = w
			}
			continue
		}
		// Past n the scan goes on: the spelling typed may be yet to come.
		if len(out) < n {
			at[k] = len(out)
			out = append(out, w)
		}
	}
	return out
}
