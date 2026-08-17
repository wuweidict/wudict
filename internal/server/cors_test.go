// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const (
	chromeOrigin = "chrome-extension://abcdefghijklmnopabcdefghijklmnop"
	fxOrigin     = "moz-extension://5b1f2e3c-0a4d-4c7e-9f21-2a6c8d1e7b40"
)

// The predicate is the whole security boundary: everything it says yes to may
// read the dictionary API from someone else's page.
func TestExtensionOrigin(t *testing.T) {
	tests := []struct {
		origin string
		want   bool
	}{
		{chromeOrigin, true},
		{fxOrigin, true},
		{"chrome-extension://a", true}, // short ids are still extension ids

		{"", false},
		{"null", false},                  // sandboxed iframe / file: page
		{"https://evil.example", false},  // the case that matters
		{"http://127.0.0.1:6888", false}, // our own page goes same-origin
		{"file://", false},
		{"chrome-extension:", false},          // no host
		{"chrome-extension://", false},        // empty host
		{"chrome-extension://a/b", false},     // an origin has no path
		{"chrome-extension://a?x=1", false},   // …nor a query
		{"chrome-extension://a#f", false},     // …nor a fragment
		{"chrome-extension://a:1", false},     // …nor a port
		{"chrome-extension://u@a", false},     // …nor userinfo
		{"chrome-extension://ev.il", false},   // not a hostname
		{"chrome-extension://a b", false},     // no whitespace smuggling
		{" chrome-extension://abc", false},    // header values are not trimmed for us
		{"Chrome-Extension://abc", false},     // schemes are lowercase on the wire
		{"moz-extension://a_b", false},        // outside the id alphabet
		{"safari-web-extension://abc", false}, // no Safari build to trust yet
		{"chrome-extension://" + strings.Repeat("a", 129), false},
	}
	for _, tc := range tests {
		if got := extensionOrigin(tc.origin); got != tc.want {
			t.Errorf("extensionOrigin(%q) = %v, want %v", tc.origin, got, tc.want)
		}
	}
}

func TestAllowOriginPinsToBrowserExtensions(t *testing.T) {
	s := &Server{BrowserExtensions: []string{chromeOrigin}}
	if !s.allowOrigin(chromeOrigin) {
		t.Errorf("the pinned origin was refused")
	}
	if s.allowOrigin(fxOrigin) {
		t.Errorf("an unlisted extension passed a pin")
	}
	if s.allowOrigin("https://evil.example") {
		t.Errorf("a website passed a pin")
	}
	// Chrome prints ids in lowercase but a user may paste them either way.
	if !(&Server{BrowserExtensions: []string{strings.ToUpper(chromeOrigin)}}).allowOrigin(chromeOrigin) {
		t.Errorf("extension ids are compared case-insensitively")
	}
	if !(&Server{}).allowOrigin(fxOrigin) {
		t.Errorf("with no pin, any extension may read")
	}
}

// The wire behaviour, end to end through the mux, which is where a route that
// forgot its wrapper would show up.
func TestCORSRoutes(t *testing.T) {
	s := newTestServer(t)
	tests := []struct {
		name   string
		method string
		path   string
		origin string
		echo   bool
	}{
		{"dicts answers an extension", "GET", "/api/dicts", chromeOrigin, true},
		{"search answers an extension", "GET", "/api/search?q=casa&mode=exact", fxOrigin, true},
		{"resources answer an extension", "GET", "/res/nope/x.png", chromeOrigin, true},

		{"a website gets nothing", "GET", "/api/search?q=casa&mode=exact", "https://evil.example", false},
		{"no Origin, no header", "GET", "/api/dicts", "", false},

		// Everything that can read the user or write the installation stays
		// same-origin, whoever asks.
		{"prefs are not readable cross-origin", "GET", "/api/prefs", chromeOrigin, false},
		{"config is not readable cross-origin", "GET", "/api/config", chromeOrigin, false},
		{"library is not readable cross-origin", "GET", "/api/library", chromeOrigin, false},
		{"library cannot be emptied cross-origin", "DELETE", "/api/library?name=x", chromeOrigin, false},
		{"ingest cannot be driven cross-origin", "GET", "/api/ingest?id=x", chromeOrigin, false},
		{"rescan cannot be driven cross-origin", "GET", "/api/rescan", chromeOrigin, false},
		{"reveal cannot be driven cross-origin", "GET", "/api/reveal?path=/tmp", chromeOrigin, false},
		{"the page itself is not readable cross-origin", "GET", "/", chromeOrigin, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			rec := httptest.NewRecorder()
			s.ServeHTTP(rec, req)
			got := rec.Header().Get("Access-Control-Allow-Origin")
			switch {
			case tc.echo && got != tc.origin:
				t.Errorf("%s %s: Allow-Origin = %q, want %q", tc.method, tc.path, got, tc.origin)
			case !tc.echo && got != "":
				t.Errorf("%s %s: Allow-Origin = %q, want none", tc.method, tc.path, got)
			}
			if got == "*" {
				t.Errorf("%s %s: answered a wildcard", tc.method, tc.path)
			}
			if rec.Header().Get("Access-Control-Allow-Credentials") != "" {
				t.Errorf("%s %s: offered credentials", tc.method, tc.path)
			}
		})
	}
}

// A shared cache that does not know the response varies by Origin will hand the
// allowed one to a denied origin.
func TestCORSAlwaysVaries(t *testing.T) {
	s := newTestServer(t)
	for _, origin := range []string{chromeOrigin, "https://evil.example", ""} {
		for _, path := range []string{"/api/dicts", "/api/search?q=casa&mode=exact", "/res/nope/x.png"} {
			req := httptest.NewRequest("GET", path, nil)
			if origin != "" {
				req.Header.Set("Origin", origin)
			}
			rec := httptest.NewRecorder()
			s.ServeHTTP(rec, req)
			if !strings.Contains(rec.Header().Get("Vary"), "Origin") {
				t.Errorf("GET %s (Origin %q): Vary = %q, want it to include Origin",
					path, origin, rec.Header().Get("Vary"))
			}
		}
	}
}

func TestCORSPreflight(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest("OPTIONS", "/api/search", nil)
	req.Header.Set("Origin", chromeOrigin)
	req.Header.Set("Access-Control-Request-Method", "GET")
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Errorf("preflight status = %d, want %d", rec.Code, http.StatusNoContent)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != chromeOrigin {
		t.Errorf("preflight Allow-Origin = %q, want %q", got, chromeOrigin)
	}
	if got := rec.Header().Get("Access-Control-Allow-Methods"); !strings.Contains(got, "GET") {
		t.Errorf("preflight Allow-Methods = %q, want it to include GET", got)
	}
	if got := rec.Header().Get("Access-Control-Max-Age"); got == "" {
		t.Errorf("preflight has no Max-Age: every request would preflight again")
	}

	// A denied origin still gets a 204, and still no grant.
	req = httptest.NewRequest("OPTIONS", "/api/search", nil)
	req.Header.Set("Origin", "https://evil.example")
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Errorf("denied preflight status = %d, want %d", rec.Code, http.StatusNoContent)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("denied preflight Allow-Origin = %q, want none", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Methods"); got != "" {
		t.Errorf("denied preflight advertised methods: %q", got)
	}
}

// The pin has to hold on the wire, not only in the predicate.
func TestCORSPinnedServer(t *testing.T) {
	s := newTestServer(t)
	s.BrowserExtensions = []string{fxOrigin}
	for origin, want := range map[string]string{fxOrigin: fxOrigin, chromeOrigin: ""} {
		req := httptest.NewRequest("GET", "/api/dicts", nil)
		req.Header.Set("Origin", origin)
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, req)
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != want {
			t.Errorf("Origin %q: Allow-Origin = %q, want %q", origin, got, want)
		}
	}
}
