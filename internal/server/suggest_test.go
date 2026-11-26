// Copyright (C) 2026 glowinthedark
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"net/url"
	"slices"
	"strings"
	"testing"
)

func TestSuggest(t *testing.T) {
	s := newTestServer(t)
	id := getDicts(t, s, "/api/dicts")[0].ID
	for _, tc := range []struct {
		query string
		want  []string
	}{
		{"q=c", []string{"casa", "corazón"}},
		{"q=coraz", []string{"corazón"}},
		{"q=CASA", []string{"casa"}}, // case ranks; it excludes nothing
		{"q=zz", []string{}},
		{"q=c&dict=" + id, []string{"casa", "corazón"}},
		{"q=c&dict=nope," + id, []string{"casa", "corazón"}},
		{"mode=wildcard&q=" + url.QueryEscape("c*a"), []string{"casa"}},
		{"mode=wildcard&q=" + url.QueryEscape("coraz?n"), []string{"corazón"}},
	} {
		var got suggestResponse
		rec := getJSON(t, s, "/api/suggest?"+tc.query, &got)
		if rec.Code != 200 || got.Words == nil || !slices.Equal(got.Words, tc.want) {
			t.Errorf("%s: status %d, words %v, want %v", tc.query, rec.Code, got.Words, tc.want)
		}
	}
	for query, want := range map[string]int{
		"":               400,
		"q=+":            400,
		"q=c&mode=fts":   400,
		"q=c&mode=exact": 400,
		"q=c&mode=nope":  400,
		"q=" + strings.Repeat("a", 65) + "&mode=wildcard": 400,
		"q=c&dict=nope": 404,
	} {
		if rec := getJSON(t, s, "/api/suggest?"+query, nil); rec.Code != want {
			t.Errorf("%q: status %d, want %d", query, rec.Code, want)
		}
	}
}

func TestMergeWords(t *testing.T) {
	for _, tc := range []struct {
		name  string
		q     string
		words []string
		n     int
		want  []string
	}{
		{"nothing", "a", nil, 5, []string{}},
		{"the word typed, then the keys it starts, shortest first", "banana",
			[]string{"bananas", "banana boat", "banana"}, 5, []string{"banana", "bananas", "banana boat"}},
		{"one row for a word, whatever its capitals", "ban",
			[]string{"BANANA", "banana", "Banana", "BANANA BOAT"}, 5, []string{"banana", "BANANA BOAT"}},
		{"the spelling typed is the one kept", "Banana",
			[]string{"banana", "BANANA", "Banana"}, 5, []string{"Banana"}},
		{"and leads the list", "March",
			[]string{"mar", "march", "March"}, 2, []string{"March", "mar"}},
		{"otherwise the lowercase spelling, wherever it arrives", "mar",
			[]string{"MARCH", "March", "march"}, 5, []string{"march"}},
		{"an accent is another word, after the one typed", "cafe",
			[]string{"café", "cafe", "cafes"}, 5, []string{"cafe", "café", "cafes"}},
		{"the word typed with an accent leads its other spellings", "côte",
			[]string{"cote", "coté", "côte", "côté", "coteau"}, 5, []string{"côte", "cote", "coté", "côté", "coteau"}},
		{"shortest first, words and phrases alike: a run of phrases hides no word (issue #45)", "genehmig",
			[]string{"genehmigen Ausgabe", "genehmigen Bank", "genehmigen", "genehmige", "genehmigt", "genehmigte", "genehmigungsfähig"}, 7,
			[]string{"genehmige", "genehmigt", "genehmigen", "genehmigte", "genehmigen Bank", "genehmigungsfähig", "genehmigen Ausgabe"}},
		{"a short phrase before a long word (D199 Am. 6)", "set",
			[]string{"settlement", "set up", "setting"}, 5, []string{"set up", "setting", "settlement"}},
		{"the keys that start as typed lead the ones reached by folding (D181)", "inform",
			[]string{"informà", "înforma", "informal", "informe", "inform about", "înform asupra"}, 6,
			[]string{"informà", "informe", "informal", "inform about", "înforma", "înform asupra"}},
		{"as typed whichever the normalization form", "café",
			[]string{"cafeina", "café au lait", "cafés"}, 5, []string{"cafés", "café au lait", "cafeina"}},
		{"phrases shortest first too", "take",
			[]string{"take a bow", "take on", "take a bath"}, 5, []string{"take on", "take a bow", "take a bath"}},
		{"dictionary order between words of one length", "sa",
			[]string{"säge", "Sage", "sage", "saga"}, 5, []string{"saga", "sage", "säge"}},
		{"a combining accent adds no length", "caf",
			[]string{"cafes", "café"}, 5, []string{"café", "cafes"}},
		{"cut to n", "a", []string{"ac", "ab", "aa", "ad"}, 3, []string{"aa", "ab", "ac"}},
		{"blank keys are dropped, and spaces around one", "a", []string{" ", "", " ab "}, 5, []string{"ab"}},
	} {
		if got := mergeWords(tc.q, tc.words, tc.n); got == nil || !slices.Equal(got, tc.want) {
			t.Errorf("%s: %v, want %v", tc.name, got, tc.want)
		}
	}
}

// Two characters the keys start with ask for fewer keys (D199 Am. 5); a
// wildcard pattern counts what comes before its first wildcard.
func TestSuggestKeysFor(t *testing.T) {
	for q, want := range map[string]int{
		"t": suggestKeysShort, "ta": suggestKeysShort, "tä": suggestKeysShort,
		"tak": suggestKeys, "take a": suggestKeys,
		"ta*": suggestKeysShort, "*abc": suggestKeysShort, "ab?cd": suggestKeysShort,
		"tak*": suggestKeys, "tak?x": suggestKeys,
	} {
		if got := suggestKeysFor(q); got != want {
			t.Errorf("suggestKeysFor(%q) = %d, want %d", q, got, want)
		}
	}
}
