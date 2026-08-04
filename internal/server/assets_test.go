// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// index.html and frame.js are two halves of one protocol, shipped in one
// binary — and the browser used to cache them independently, index.html fresh
// from "/" and frame.js for a week at a fixed URL. Any change to the message
// between them then broke silently, and only inside iframe-rendered
// dictionaries. This pins the fix: the page names each script by a hash of its
// own bytes, so the two can never be a version apart.
func TestScriptsAreContentAddressed(t *testing.T) {
	s := newTestServer(t)
	s.Version = "1.2.3"
	page := string(s.page())

	for name, want := range map[string]string{
		"frame.js":    "/assets/frame.js?v=" + assetTag(frameJS),
		"mark.min.js": "/assets/mark.min.js?v=" + assetTag(markJS),
	} {
		if !strings.Contains(page, want) {
			t.Errorf("index.html does not request %s by content hash (%s)", name, want)
		}
	}
	if i := strings.Index(page, "{{"); i >= 0 {
		t.Errorf("unsubstituted placeholder in the served page: %q", page[i:min(i+24, len(page))])
	}
	// the hash must actually depend on the content
	if assetTag(frameJS) == assetTag(append(append([]byte{}, frameJS...), '\n')) {
		t.Error("assetTag ignores a change in content")
	}
}

// The page is the only thing that names those hashes, so it must revalidate;
// the scripts it names may be cached hard, because their URL now changes with
// their bytes.
func TestAssetCacheHeaders(t *testing.T) {
	s := newTestServer(t)
	get := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		return rec
	}
	if got := get("/").Header().Get("Cache-Control"); got != "no-cache" {
		t.Errorf(`GET / Cache-Control = %q, want "no-cache" — a stale page would ask for stale scripts`, got)
	}
	for _, p := range []string{"/assets/frame.js?v=abc123", "/assets/mark.min.js?v=abc123"} {
		rec := get(p)
		if rec.Code != 200 || rec.Body.Len() == 0 {
			t.Errorf("GET %s: status %d, %d bytes", p, rec.Code, rec.Body.Len())
		}
		if got := rec.Header().Get("Cache-Control"); !strings.Contains(got, "immutable") {
			t.Errorf("GET %s Cache-Control = %q, want immutable", p, got)
		}
	}
}
