// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The marking rule lives in three places that can drift independently - the
// `hl` parameter, the full-text-only gate, and the rewriter hook that actually
// emits the tag - so it is asserted end to end, on the bytes a client receives.
//
// The fixture is its own dictionary, not the shared one: proving that a
// non-full-text mode does NOT mark requires a body that contains the query
// word, which "casa"/"vivienda" cannot give.
const hlDSL = "#NAME \"HL Test Dict\"\n\n" +
	"vivienda\n\tuna vivienda es una casa\n\n" +
	"casa\n\tvivienda\n"

func newHLServer(t *testing.T) (*Server, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "hl.dsl"), []byte(hlDSL), 0o644); err != nil {
		t.Fatal(err)
	}
	isolatedDBDir(t)
	reg, err := NewRegistry([]string{dir}, false)
	if err != nil {
		t.Fatal(err)
	}
	s := New(reg)
	dicts := getDicts(t, s, "/api/dicts")
	if len(dicts) != 1 {
		t.Fatalf("dicts: %+v", dicts)
	}
	id := dicts[0].ID
	sse(t, s, "/api/ingest?dict="+id+"&fts=1")
	return s, id
}

func bodies(hits []streamMsg) string {
	var b strings.Builder
	for _, h := range hits {
		for _, r := range h.Results {
			b.WriteString(r.Body)
		}
	}
	return b.String()
}

func TestSearchHighlight(t *testing.T) {
	s, id := newHLServer(t)

	got := bodies(searchStream(t, s, "/api/search?q=vivi&mode=fts&hl=1&dict="+id))
	if !strings.Contains(got, `<mark class="wu-hl">vivienda</mark>`) {
		t.Errorf("fts&hl=1 did not mark the whole token: %s", got)
	}

	for _, q := range []string{
		"/api/search?q=vivi&mode=fts&dict=" + id,             // opt-in: absent means off
		"/api/search?q=vivi&mode=fts&hl=0&dict=" + id,        // explicitly off
		"/api/search?q=vivienda&mode=prefix&hl=1&dict=" + id, // full-text only
	} {
		got := bodies(searchStream(t, s, q))
		if !strings.Contains(got, "vivienda") {
			t.Fatalf("%s returned no body carrying the query word - the case would pass vacuously: %q", q, got)
		}
		if strings.Contains(got, "wu-hl") {
			t.Errorf("%s marked anyway: %s", q, got)
		}
	}
}

// The phrase is the unit. A multi-word full-text query marks ONE span across
// the words it matched, and only a descent to a weaker rung marks them apart -
// which is also the only case the response admits to, via `rung`.
func TestSearchPhraseMarking(t *testing.T) {
	s, id := newHLServer(t)

	for _, tc := range []struct {
		name, q, rung string
		marks         []string
	}{
		{"adjacent words are one span", "vivienda es una", "phrase",
			[]string{`<mark class="wu-hl">vivienda es una</mark>`}},
		{"reversed words fall to NEAR and mark apart", "casa una", "near",
			[]string{`<mark class="wu-hl">casa</mark>`, `<mark class="wu-hl">una</mark>`}},
		{"a single word does not relax", "vivi", "words",
			[]string{`<mark class="wu-hl">vivienda</mark>`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hits := searchStream(t, s, "/api/search?q="+url.QueryEscape(tc.q)+"&mode=fts&hl=1&dict="+id)
			var rung string
			for _, h := range hits {
				if len(h.Results) > 0 {
					rung = h.Rung
				}
			}
			if rung != tc.rung {
				t.Errorf("q=%q answered on rung %q, want %q", tc.q, rung, tc.rung)
			}
			got := bodies(hits)
			for _, m := range tc.marks {
				if !strings.Contains(got, m) {
					t.Errorf("q=%q: missing %s in %s", tc.q, m, got)
				}
			}
		})
	}
}
