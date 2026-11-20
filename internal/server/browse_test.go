// Copyright (C) 2026 glowinthedark
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"net/url"
	"testing"
)

// A jump names where it landed and how (D203); a page turn names nothing.
func TestBrowseJump(t *testing.T) {
	s := newTestServer(t)
	id := idOf(t, s, "test.dsl")
	preparedDSL(t, s)
	for _, tc := range []struct{ query, word, match string }{
		{"at=CASA", "casa", "exact"},
		{"at=corazon", "corazón", "exact"}, // accents aside
		{"at=coraz", "corazón", "prefix"},
		{"at=cb", "corazón", "none"}, // would stand before corazón
		{"at=" + url.QueryEscape("  "), "", ""},
		{"p=1", "", ""},
	} {
		var got browseResp
		rec := getJSON(t, s, "/api/browse?dict="+id+"&"+tc.query, &got)
		if rec.Code != 200 || got.Word != tc.word || got.Match != tc.match {
			t.Errorf("%s: status %d, word %q match %q; want %q %q", tc.query, rec.Code, got.Word, got.Match, tc.word, tc.match)
		}
	}
}
