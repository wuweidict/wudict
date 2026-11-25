// Copyright (C) 2026 glowinthedark
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"slices"
	"testing"
)

// The list's corrections (D206), on a dictionary of English, the language
// whose lemma data is built in.
func TestSuggestCorrections(t *testing.T) {
	s := morphServer(t, map[string]string{"en-en-x.dsl": dslFile("English", "house", "receive", "go", "wentletrap")})
	getDicts(t, s, "/api/dicts")
	suggest := func(q string) suggestResponse {
		t.Helper()
		var got suggestResponse
		if rec := getJSON(t, s, "/api/suggest?mode=prefix&q="+q, &got); rec.Code != 200 {
			t.Fatalf("%s: status %d", q, rec.Code)
		}
		return got
	}

	// The list loads no lemma data, a dead end included: nothing comes from
	// it while no search has loaded it. The spelling still does.
	if got := suggest("went"); len(got.Lemmas) != 0 || len(got.Similar) != 0 {
		t.Errorf("went, lemma data not loaded: %+v", got)
	}
	if got := suggest("houses"); len(got.Lemmas) != 0 || !slices.Equal(got.Similar, []string{"house"}) {
		t.Errorf("houses, lemma data not loaded: %+v", got)
	}
	// Once a search has loaded it, the same word gets its lemma: the data
	// finds whole word forms only.
	stream(t, s, "/api/search?q=went&mode=exact&dict=all")
	if got := suggest("went"); !slices.Equal(got.Lemmas, []string{"go"}) || len(got.Similar) != 0 {
		t.Errorf("went, lemma data loaded: %+v", got)
	}

	for _, tc := range []struct {
		q               string
		lemmas, similar []string
	}{
		{"recieve", nil, []string{"receive"}}, // a dead end: no headword starts with it
		{"houses", []string{"house"}, nil},    // the lemma, not again as a spelling
		{"hous", nil, nil},                    // starts a headword: no spelling offered
		{"house", nil, nil},                   // matches: nothing to correct
		{"qqqqq", nil, nil},
	} {
		got := suggest(tc.q)
		if !slices.Equal(got.Lemmas, tc.lemmas) || !slices.Equal(got.Similar, tc.similar) {
			t.Errorf("%s: lemmas %q, similar %q; want %q, %q", tc.q, got.Lemmas, got.Similar, tc.lemmas, tc.similar)
		}
	}
	// A wildcard list offers none.
	var got suggestResponse
	getJSON(t, s, "/api/suggest?mode=wildcard&q=recieve*", &got)
	if len(got.Lemmas)+len(got.Similar) != 0 {
		t.Errorf("wildcard: %+v", got)
	}
}

// A search that finds nothing, lemmas included, ends with the headwords
// spelled like the query (D206); one that finds something does not.
func TestSearchSimilar(t *testing.T) {
	s := morphServer(t, map[string]string{"en-en-x.dsl": dslFile("English", "house", "receive", "go")})
	getDicts(t, s, "/api/dicts")
	similar := func(path string) []string {
		t.Helper()
		var out []string
		for _, m := range stream(t, s, path) {
			if m.T == "similar" {
				out = append(out, m.Words...)
			}
		}
		return out
	}
	for path, want := range map[string][]string{
		"/api/search?q=recieve&mode=exact&dict=all":    {"receive"},
		"/api/search?q=recieve&mode=prefix&dict=all":   {"receive"},
		"/api/search?q=recieve&mode=wildcard&dict=all": {"receive"}, // no wildcard character: a word
		"/api/search?q=receive&mode=exact&dict=all":    nil,
		"/api/search?q=houses&mode=exact&dict=all":     nil, // the lemma wave found house
		"/api/search?q=recieve&mode=fts&dict=all":      nil, // a full-text query is not a headword
		"/api/search?q=re*ve&mode=wildcard&dict=all":   nil,
		"/api/search?q=recieve+it&mode=exact&dict=all": nil, // not one word
	} {
		if got := similar(path); !slices.Equal(got, want) {
			t.Errorf("%s: similar %q, want %q", path, got, want)
		}
	}
}
