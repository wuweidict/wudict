// Copyright (C) 2026 glowinthedark
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"cmp"
	"context"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"

	"github.com/wuweidict/wudict/internal/dict"
	"github.com/wuweidict/wudict/internal/search"
	"github.com/wuweidict/wudict/internal/store"
	"github.com/wuweidict/wudict/internal/wildcard"
)

const (
	// suggestMax is the length of the list, and what a dictionary without a
	// headword index is asked for: it reads an article for every key.
	suggestMax = 20
	// suggestKeys is what a prepared dictionary is asked for (dict.Keyer, the
	// index only). Its keys arrive in dictionary order and the list ranks
	// words by length (mergeWords), so its first suggestMax keys can be a run
	// of phrases that hides a shorter word filed after them: `genehmigen
	// Bank` before `genehmigt` (issue #45). The words a dictionary files
	// after its first suggestKeys keys are still out of reach.
	suggestKeys = 200
	// suggestKeysShort is what it is asked for when the text has at most 2
	// characters (suggestKeysFor). Those 20 rows are words of 2 to 4 letters,
	// and the 200 keys cost about twice the read and the merge: ~26,000 keys
	// across 160 dictionaries (D199 Am. 5).
	suggestKeysShort = 50
	// suggestBudget is how long the list waits for the dictionaries. One that
	// has not answered by then is left out, and is open by the next keystroke.
	suggestBudget = 2 * time.Second
)

type suggestResponse struct {
	Words []string `json:"words"`
	// The corrections of a prefix no headword matches exactly (D206): the
	// word's lemmas, and at a dead end - no headword starts with it - the
	// headwords spelled like it.
	Lemmas  []string `json:"lemmas,omitempty"`
	Similar []string `json:"similar,omitempty"`
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
//
// A prefix that no headword matches exactly also gets its corrections
// (D206), on signals that do not depend on how fast anyone types: a lemma
// only from data already in memory, which finds whole word forms
// (listLemmas), and the headwords spelled like it only at a dead end - no
// headword starts with the text, and no further typing can make one.
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
		mu      sync.Mutex
		words   []string
		metas   = make([]dict.Meta, len(entries))
		done    = make(chan struct{})
		openers = s.searchOpeners(entries)
	)
	go func() {
		defer close(done)
		search.StreamKeys(ctx, openers, mode, q, suggestMax, suggestKeysFor(q), func(i int, h search.Hit) {
			mu.Lock()
			defer mu.Unlock()
			metas[i] = h.Meta
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
	resp := suggestResponse{Words: mergeWords(q, words, suggestMax)}
	metas = slices.Clone(metas) // the fan-out may still be writing behind a deadline
	mu.Unlock()
	if mode == search.Prefix && oneWord(q) && (len(resp.Words) == 0 || foldWord(resp.Words[0]) != foldWord(q)) {
		resp.Lemmas = s.listLemmas(ctx, entries, metas, openers, q)
		if len(resp.Words) == 0 { // a dead end
			for _, sw := range s.similarWords(ctx, openers, q) {
				if !containsFold(resp.Lemmas, foldWord(sw)) {
					resp.Similar = append(resp.Similar, sw)
				}
			}
		}
	}
	writeJSON(w, resp)
}

// suggestKeysFor is what a prepared dictionary is asked for, for the text q:
// suggestKeysShort when what the keys must start with - the text before a
// wildcard character - is at most 2 characters long, suggestKeys otherwise.
func suggestKeysFor(q string) int {
	head := q
	if i := strings.IndexAny(q, "*?"); i >= 0 {
		head = q[:i]
	}
	if utf8.RuneCountInString(head) <= 2 {
		return suggestKeysShort
	}
	return suggestKeys
}

// The classes of mergeWords, best first. "As typed" ignores capitals only;
// "folded" ignores accents too (D181: they rank, they never exclude).
const (
	rowAsTyped      = iota // the word typed, as typed
	rowSameWord            // the word typed, with other capitals or accents
	rowWord                // a word it starts as typed
	rowWordFolded          // a word it starts folded
	rowPhrase              // a key of several words it starts as typed
	rowPhraseFolded        // a key of several words it starts folded
)

// suggestRow is one word of the list and what ranks it; keys is how many of
// the dictionaries' keys it merges.
type suggestRow struct {
	w                 string
	class, size, keys int
}

// oneRowEach makes one row of each word whatever its capitals - a dictionary
// that files its headwords in capitals adds no second row. The spelling typed
// is the one kept when a dictionary has it; otherwise the first in
// dictionary order (store.Collate), the lowercase one.
func oneRowEach(q string, words []string) []suggestRow {
	at := make(map[string]int, len(words))
	rows := make([]suggestRow, 0, len(words))
	for _, w := range words {
		if w = strings.TrimSpace(w); w == "" {
			continue
		}
		k := strings.ToLower(w)
		i, ok := at[k]
		if !ok {
			at[k] = len(rows)
			rows = append(rows, suggestRow{w: w, keys: 1})
			continue
		}
		rows[i].keys++
		if kept := rows[i].w; kept != q && (w == q || store.Collate(w, kept) < 0) {
			rows[i].w = w
		}
	}
	return rows
}

// topRows is the first n words of rows by rank, then dictionary order. Only
// the rows that rank with the first n are put in dictionary order, which is
// the costly comparison: a two-letter prefix brings tens of thousands of
// keys. It sorts rows in place.
func topRows(rows []suggestRow, n int, rank func(a, b suggestRow) int) []string {
	slices.SortFunc(rows, rank)
	// Every row past end ranks below the nth.
	end := min(n, len(rows))
	for end > 0 && end < len(rows) && rank(rows[end-1], rows[end]) == 0 {
		end++
	}
	slices.SortFunc(rows[:end], func(a, b suggestRow) int { return cmp.Or(rank(a, b), store.Collate(a.w, b.w)) })
	out := make([]string, 0, min(n, len(rows)))
	for _, r := range rows[:min(n, len(rows))] {
		out = append(out, r.w)
	}
	return out
}

// mergeWords makes one list of at most n words out of the dictionaries' keys,
// one row each (oneRowEach).
//
// The list ranks by class, then shortest first, then in dictionary order:
// the word typed, then the single words it starts, then the phrases built on
// them, which a reader looks up by their rarest word rather than their first
// (issue #45; GoldenDict ranks prefix matches by length too). Within words
// and within phrases, the ones that start as typed lead the ones reached by
// folding: `inform` lists informal before înforma. Length counts the
// characters Collate's first level reads: an accent written as a combining
// mark adds nothing.
func mergeWords(q string, words []string, n int) []string {
	rows := oneRowEach(q, words)
	fq, lq := foldWord(q), lowerNFC(q)
	for i := range rows {
		r := &rows[i]
		f := foldWord(r.w)
		r.size = utf8.RuneCountInString(f)
		switch {
		case r.w == q:
			r.class = rowAsTyped
		case f == fq:
			r.class = rowSameWord
		default:
			if strings.ContainsFunc(r.w, unicode.IsSpace) {
				r.class = rowPhrase
			} else {
				r.class = rowWord
			}
			if !strings.HasPrefix(lowerNFC(r.w), lq) {
				r.class++ // the folded class follows its as-typed one
			}
		}
	}
	return topRows(rows, n, func(a, b suggestRow) int {
		return cmp.Or(cmp.Compare(a.class, b.class), cmp.Compare(a.size, b.size))
	})
}

// lowerNFC is a key lowercased and composed: how mergeWords compares the
// start of a word as typed, whichever normalization form a dictionary or a
// keyboard wrote it in.
func lowerNFC(s string) string { return norm.NFC.String(strings.ToLower(s)) }

// foldWord is a word as Collate's first level reads it: every character
// through wildcard.Fold, the ones it drops left out.
func foldWord(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if f := wildcard.Fold(r); f >= 0 {
			b.WriteRune(f)
		}
	}
	return b.String()
}
