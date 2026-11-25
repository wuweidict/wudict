// Copyright (C) 2026 glowinthedark
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"cmp"
	"context"
	"strings"
	"unicode/utf8"

	"github.com/wuweidict/wudict/internal/dict"
	"github.com/wuweidict/wudict/internal/search"
	"github.com/wuweidict/wudict/internal/store"
)

// Corrections (D206): what the list under the search box and an empty search
// offer for a word no headword matches - its lemma, and the headwords spelled
// like it.
const (
	// similarMax is how many headwords spelled like the word are offered.
	similarMax = 5
	// similarPerDict is what each dictionary is asked for: more than
	// similarMax, since the dictionaries disagree on which come first.
	similarPerDict = 8
	// similarWide is the length, in folded characters, from which a second
	// edit is allowed - only when no headword is one edit away. Below it two
	// edits reach a large share of the short headwords.
	similarWide = 5
)

// similarWords is the headwords spelled like q in the dictionaries openers
// open: those one edit away (store.SimilarKeys); with none in any of them
// and q at least similarWide characters long, two. One row per word whatever
// its capitals (oneRowEach), at most similarMax, ranked by:
//
//   - edits (store.Distances);
//   - how many dictionaries hold the word: one that many hold is more likely
//     meant than one a single glossary does - the one popularity signal a
//     library carries;
//   - the length nearest q's;
//   - dictionary order.
//
// Each dictionary keeps its answer (store.SimilarKeys), so the search that
// follows a list asks nothing twice.
func (s *Server) similarWords(ctx context.Context, openers []search.Opener, q string) []string {
	f := foldWord(q)
	size := utf8.RuneCountInString(f)
	if size < store.SimilarMin {
		return nil
	}
	for dist := 1; dist <= 2 && ctx.Err() == nil; dist++ {
		if dist == 2 && size < similarWide {
			break
		}
		var keys []string
		search.StreamSimilar(ctx, openers, q, dist, similarPerDict, func(_ int, h search.Hit) {
			for _, r := range h.Results { // emit is serialised: no lock
				keys = append(keys, r.Headword)
			}
		})
		rows := oneRowEach(q, keys)
		words := make([]string, len(rows))
		for i, r := range rows {
			words[i] = r.w
		}
		for i, d := range store.Distances(q, dist, words) {
			n := utf8.RuneCountInString(foldWord(rows[i].w))
			rows[i].class, rows[i].size = d, max(n-size, size-n)
		}
		if out := topRows(rows, similarMax, func(a, b suggestRow) int {
			return cmp.Or(cmp.Compare(a.class, b.class), cmp.Compare(b.keys, a.keys), cmp.Compare(a.size, b.size))
		}); len(out) > 0 {
			return out
		}
	}
	return nil
}

// listLemmas is the lemmas of q that are headwords of the dictionaries in
// the list, in their spelling (D206). The lemma data is the search's own
// (lemmaLangs, the language of each dictionary, morph); each lemma is checked
// against the headword index of the dictionaries of its language, as the
// lemma wave's are.
//
// Only lemma data already in memory answers (morph.Loaded, LemmaLoaded): the
// list follows the keys, and a pack loaded per key - 7 to 65 MB, one at a
// time under Android's MORPH_CACHE - would evict and reload on every one. The
// search loads it on Enter, as before. Such data finds a lemma only for a
// whole word form, so the row is right even while typing goes on.
func (s *Server) listLemmas(ctx context.Context, entries []*entry, metas []dict.Meta, openers []search.Opener, q string) []string {
	if !oneWord(q) {
		return nil
	}
	var (
		out    []string
		byLang map[string][]int
	)
	for _, code := range s.Morph.Loaded() {
		lemma, ok := s.Morph.LemmaLoaded(code, q)
		if !ok {
			continue
		}
		fl := foldWord(lemma)
		if containsFold(out, fl) {
			continue
		}
		if byLang == nil { // the dictionaries' languages, once a lemma asks
			_, byLang = s.lemmaLangs(entries, metas, func(int) bool { return true })
		}
		slots := byLang[code]
		if len(slots) == 0 {
			continue
		}
		sub := make([]search.Opener, len(slots))
		for k, i := range slots {
			sub[k] = openers[i]
		}
		// A prefix query lists the word itself first when it is a headword
		// (D181), so one key per dictionary settles it, and the first
		// dictionary that has it settles them all: the rest are cancelled.
		spelled := ""
		one, stop := context.WithCancel(ctx)
		search.StreamKeys(one, sub, search.Prefix, lemma, 1, 1, func(_ int, h search.Hit) {
			for _, r := range h.Results {
				if spelled == "" && foldWord(r.Headword) == fl {
					spelled = strings.TrimSpace(r.Headword)
					stop()
				}
			}
		})
		stop()
		if spelled != "" {
			out = append(out, spelled)
		}
	}
	return out
}

// containsFold reports whether one of ws folds to f.
func containsFold(ws []string, f string) bool {
	for _, w := range ws {
		if foldWord(w) == f {
			return true
		}
	}
	return false
}
