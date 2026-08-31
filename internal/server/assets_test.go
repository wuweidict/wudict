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
// binary - and the browser used to cache them independently, index.html fresh
// from "/" and frame.js for a week at a fixed URL. Any change to the message
// between them then broke silently, and only inside iframe-rendered
// dictionaries. This pins the fix: the page names each script by a hash of its
// own bytes, so the two can never be a version apart.
func TestScriptsAreContentAddressed(t *testing.T) {
	s := newTestServer(t)
	s.Version = "1.2.3"
	page := string(s.page())

	if want := "/assets/frame.js?v=" + assetTag(frameJS); !strings.Contains(page, want) {
		t.Errorf("index.html does not request frame.js by content hash (%s)", want)
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
		t.Errorf(`GET / Cache-Control = %q, want "no-cache" - a stale page would ask for stale scripts`, got)
	}
	for _, p := range []string{"/assets/frame.js?v=abc123"} {
		rec := get(p)
		if rec.Code != 200 || rec.Body.Len() == 0 {
			t.Errorf("GET %s: status %d, %d bytes", p, rec.Code, rec.Body.Len())
		}
		if got := rec.Header().Get("Cache-Control"); !strings.Contains(got, "immutable") {
			t.Errorf("GET %s Cache-Control = %q, want immutable", p, got)
		}
	}
}

// "no-cache" means revalidate, not "do not store" - but revalidation needs a
// validator, and without one the browser could only re-download the whole
// 100 KB page on every load. This pins the validator AND the freshness
// guarantee D45 depends on: the page still revalidates every time, it just
// stops re-sending itself when nothing changed.
func TestIndexRevalidatesWithoutResending(t *testing.T) {
	s := newTestServer(t)
	s.Version = "1.2.3"

	get := func(inm string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", "/", nil)
		if inm != "" {
			req.Header.Set("If-None-Match", inm)
		}
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, req)
		return rec
	}

	first := get("")
	etag := first.Header().Get("ETag")
	if first.Code != 200 || first.Body.Len() == 0 {
		t.Fatalf("GET / = %d, %d bytes", first.Code, first.Body.Len())
	}
	if !strings.HasPrefix(etag, `"`) || !strings.HasSuffix(etag, `"`) || len(etag) < 3 {
		t.Fatalf("ETag = %q, want a quoted entity-tag", etag)
	}

	// The whole point: the same page again costs no body.
	again := get(etag)
	if again.Code != 304 {
		t.Errorf("If-None-Match with the current ETag = %d, want 304", again.Code)
	}
	if again.Body.Len() != 0 {
		t.Errorf("304 carried %d bytes of body", again.Body.Len())
	}
	if got := again.Header().Get("Cache-Control"); got != "no-cache" {
		t.Errorf("304 Cache-Control = %q - the next load must still revalidate", got)
	}

	// A GET compares entity-tags WEAKLY, so a cache may echo ours back as
	// W/"…". A hand-rolled equality check would fail to match a validator we
	// issued ourselves; net/http's comparison must not.
	if rec := get(`W/` + etag); rec.Code != 304 {
		t.Errorf("weak If-None-Match = %d, want 304", rec.Code)
	}
	// …and a list, which is what a browser holding several versions sends.
	if rec := get(`"deadbeef", ` + etag); rec.Code != 304 {
		t.Errorf("If-None-Match list containing the current ETag = %d, want 304", rec.Code)
	}
	// A validator we did not issue must send the page.
	stale := get(`"deadbeef"`)
	if stale.Code != 200 || stale.Body.Len() == 0 {
		t.Errorf("stale If-None-Match = %d, %d bytes - want the page", stale.Code, stale.Body.Len())
	}

	// The stamp is substituted into the page, so it is part of what the browser
	// holds: a rebuild that changes only the version must invalidate.
	other := newTestServer(t)
	other.Version = "9.9.9"
	if other.pageETag() == etag {
		t.Error("ETag ignores the version stamped into the page")
	}
	// No Last-Modified: these bytes are embedded and have no meaningful date,
	// and a second validator we cannot stand behind is worse than none.
	if got := first.Header().Get("Last-Modified"); got != "" {
		t.Errorf("Last-Modified = %q, want none", got)
	}
}
