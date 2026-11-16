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
		{"the word typed in full leads the words it starts", "banana",
			[]string{"bananas", "banana boat", "banana"}, 5, []string{"banana", "banana boat", "bananas"}},
		{"one row for a word, whatever its capitals", "ban",
			[]string{"BANANA", "banana", "Banana", "BANANA BOAT"}, 5, []string{"banana", "BANANA BOAT"}},
		{"the spelling typed is the one kept", "Banana",
			[]string{"banana", "BANANA", "Banana"}, 5, []string{"Banana"}},
		{"also when it sorts past the cut", "March",
			[]string{"mar", "march", "March"}, 2, []string{"mar", "March"}},
		{"an accent is another word", "cafe",
			[]string{"café", "cafe"}, 5, []string{"cafe", "café"}},
		{"cut to n", "a", []string{"ac", "ab", "aa", "ad"}, 3, []string{"aa", "ab", "ac"}},
		{"blank keys are dropped, and spaces around one", "a", []string{" ", "", " ab "}, 5, []string{"ab"}},
	} {
		if got := mergeWords(tc.q, tc.words, tc.n); got == nil || !slices.Equal(got, tc.want) {
			t.Errorf("%s: %v, want %v", tc.name, got, tc.want)
		}
	}
}
