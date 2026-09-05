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

// authServer is a real server - registry, state file and all - with a key
// set. Real, because half of what is under test is that an ALLOWED request
// still reaches its handler and works; a stub registry would turn that half
// into a panic.
func authServer(t *testing.T) *Server {
	t.Helper()
	s, _ := newPrefsServer(t)
	s.AuthToken = "s3cret-token-value"
	return s
}

func serve(s *Server, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	return rec
}

// TestAuthFreeMatchesRoutes pins the allowlist to the table in both
// directions. A route added without thought is gated (the default); a name
// left in authFree after its route was renamed would silently protect
// nothing, so that direction is an error too.
func TestAuthFreeMatchesRoutes(t *testing.T) {
	have := map[string]bool{}
	for _, rt := range New(nil).routes() {
		have[rt.Method+" "+rt.Pattern] = true
	}
	for op := range authFree {
		if !have[op] {
			t.Errorf("authFree lists %s, which is not a route", op)
		}
	}
	// The three read-only endpoints an extension reaches must stay open: a
	// key requirement there breaks every installed extension, which is a
	// published contract (D69), not an implementation detail.
	for op := range corsAllowed {
		if !authFree[op] {
			t.Errorf("%s answers cross-origin but requires the key: extensions cannot send one", op)
		}
	}
	// And the data endpoints must NOT be. Spot-checked by name rather than by
	// "everything else", so that this test fails when one of these is moved
	// into authFree, not merely when the count changes.
	for _, op := range []string{
		"GET /api/config", "GET /api/prefs", "PUT /api/prefs", "GET /api/library",
		"DELETE /api/library", "GET /api/rescan", "GET /api/ingest", "GET /api/setup",
		"GET /api/reveal", "POST /api/demand", "GET /api/about", "POST /api/power",
		"GET /api/lemmas", "POST /api/lemmas", "DELETE /api/lemmas",
	} {
		if authFree[op] {
			t.Errorf("%s is reachable without the key", op)
		}
		if !have[op] {
			t.Errorf("%s is not a route: this test is out of date", op)
		}
	}
}

func TestGatedRouteRefusesWithoutKey(t *testing.T) {
	s := authServer(t)
	rec := serve(s, newRequest("GET", "/api/config", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"auth":"required"`) {
		t.Errorf("no machine-readable marker: %s", rec.Body.String())
	}
	// A person who typed the address gets a sentence, not JSON.
	req := newRequest("GET", "/api/config", nil)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	rec = serve(s, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type %q, want text/html", ct)
	}
}

func TestGatedRouteAcceptsEveryCarrier(t *testing.T) {
	s := authServer(t)
	cases := []struct {
		name string
		fix  func(*http.Request)
	}{
		{"cookie", func(r *http.Request) {
			r.AddCookie(&http.Cookie{Name: cookieName, Value: s.AuthToken})
		}},
		{"bearer", func(r *http.Request) {
			r.Header.Set("Authorization", "Bearer "+s.AuthToken)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := newRequest("GET", "/api/config", nil)
			tc.fix(req)
			if rec := serve(s, req); rec.Code != 200 {
				t.Fatalf("status %d, want 200: %s", rec.Code, rec.Body.String())
			}
		})
	}
	// ?k= on a non-navigation is served directly, with the cookie set for
	// everything that follows.
	rec := serve(s, newRequest("GET", "/api/config?k="+s.AuthToken, nil))
	if rec.Code != 200 {
		t.Fatalf("?k=: status %d, want 200", rec.Code)
	}
	if c := rec.Header().Get("Set-Cookie"); !strings.Contains(c, cookieName+"=") {
		t.Errorf("?k= set no cookie: %q", c)
	}
}

func TestWrongKeyIsRefused(t *testing.T) {
	s := authServer(t)
	for _, bad := range []string{"", "s3cret-token-valuX", "S3CRET-TOKEN-VALUE"} {
		req := newRequest("GET", "/api/config", nil)
		if bad != "" {
			req.Header.Set("Authorization", "Bearer "+bad)
		}
		if rec := serve(s, req); rec.Code != http.StatusUnauthorized {
			t.Errorf("key %q: status %d, want 401", bad, rec.Code)
		}
	}
}

// TestKeyLinkRedirects is the property that keeps the key out of the address
// bar, the history and every Referer the page would otherwise send.
func TestKeyLinkRedirects(t *testing.T) {
	s := authServer(t)
	req := newRequest("GET", "/?q=word&k="+s.AuthToken, nil)
	req.Header.Set("Accept", "text/html")
	rec := serve(s, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("status %d, want 302", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if strings.Contains(loc, "k=") {
		t.Errorf("redirect still carries the key: %s", loc)
	}
	if !strings.Contains(loc, "q=word") {
		t.Errorf("redirect dropped the query: %s", loc)
	}
	cookie := rec.Header().Get("Set-Cookie")
	for _, want := range []string{cookieName + "=" + s.AuthToken, "HttpOnly", "SameSite=Strict", "Path=/"} {
		if !strings.Contains(cookie, want) {
			t.Errorf("Set-Cookie %q lacks %q", cookie, want)
		}
	}
	if rec.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Errorf("no Referrer-Policy on the redirect that carries the key")
	}
}

// A wrong ?k= must not be laundered into a redirect: it is refused like any
// other absent key, and sets no cookie.
func TestBadKeyLinkSetsNoCookie(t *testing.T) {
	s := authServer(t)
	req := newRequest("GET", "/api/config?k=wrong", nil)
	req.Header.Set("Accept", "text/html")
	rec := serve(s, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401", rec.Code)
	}
	if c := rec.Header().Get("Set-Cookie"); c != "" {
		t.Errorf("a wrong key set a cookie: %q", c)
	}
}

func TestOpenRoutesStayOpen(t *testing.T) {
	s := authServer(t)
	for _, p := range []string{"/", "/setup", "/lemmas", "/favicon.ico", "/assets/frame.js", "/api/openapi.yaml"} {
		if rec := serve(s, newRequest("GET", p, nil)); rec.Code == http.StatusUnauthorized {
			t.Errorf("%s requires the key: the page cannot explain itself", p)
		}
	}
}

// With no key configured the server behaves exactly as it did before: this is
// the loopback default, and every other test in the package relies on it.
func TestNoTokenNoGate(t *testing.T) {
	// A real (empty) registry, not nil: /api/config reads the roots, and a nil
	// registry panics there - which ServeHTTP would recover into a 500 and
	// this test would read as "not 401", i.e. as a pass.
	reg, err := NewRegistry([]string{t.TempDir()}, false)
	if err != nil {
		t.Fatal(err)
	}
	s := New(reg)
	if rec := serve(s, newRequest("GET", "/api/config", nil)); rec.Code == http.StatusUnauthorized {
		t.Fatal("a server with no key refused a request")
	}
}
