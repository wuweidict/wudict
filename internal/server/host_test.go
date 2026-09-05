// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// The rule is a shape rule (host.go): addresses always, "localhost" always,
// every other name only if TRUSTED_HOSTS says so.
func TestHostAllowed(t *testing.T) {
	tests := []struct {
		host    string
		trusted []string
		want    bool
	}{
		{"127.0.0.1:6888", nil, true},
		{"127.0.0.1", nil, true},
		{"192.168.1.20:6888", nil, true}, // a LAN bind is still an address
		{"[::1]:6888", nil, true},
		{"[::1]", nil, true},
		{"::1", nil, true}, // malformed, but still not a rebindable name
		{"[fe80::1%25en0]:6888", nil, false},
		{"localhost:6888", nil, true},
		{"LocalHost", nil, true},
		{"localhost.:6888", nil, true}, // the root dot is the same name
		{"", nil, false},
		{"evil.example:6888", nil, false},
		{"wudict.lan:6888", nil, false},
		{"wudict.lan:6888", []string{"wudict.lan"}, true},
		{"WUDICT.LAN", []string{"wudict.lan"}, true},
		{"wudict.lan", []string{"wudict.lan:6888"}, true}, // port ignored both sides
		{"evil.example", []string{"wudict.lan"}, false},
		{"evil.example", []string{"*"}, true}, // the typed opt-out
	}
	for _, tt := range tests {
		s := &Server{TrustedHosts: tt.trusted}
		r := httptest.NewRequest("GET", "/api/dicts", nil)
		r.Host = tt.host
		if got := s.hostAllowed(r); got != tt.want {
			t.Errorf("Host %q with TRUSTED_HOSTS %v: allowed = %v, want %v", tt.host, tt.trusted, got, tt.want)
		}
	}
}

// A forged authority must not reach any route - which is only true if the
// check sits in front of the mux rather than inside handlers.
func TestRebindingRefusedBeforeRouting(t *testing.T) {
	s := newTestServer(t)
	for _, path := range []string{"/", "/api/dicts", "/api/search?q=casa&mode=exact", "/api/config"} {
		r := httptest.NewRequest("GET", path, nil)
		r.Host = "evil.example" // what a rebound page sends
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != http.StatusMisdirectedRequest {
			t.Errorf("GET %s with a rebound Host: got %d, want 421", path, w.Code)
		}
	}
}

// fe80::1%25en0 above is deliberate: a zone-qualified literal arrives
// percent-encoded and ParseIP refuses it, so it lands in the name branch and
// is rejected. That is the safe direction, and no client sends one.
func TestHostNameStrips(t *testing.T) {
	tests := []struct{ in, want string }{
		{"127.0.0.1:6888", "127.0.0.1"},
		{"[::1]:6888", "::1"},
		{"[::1]", "::1"},
		{"::1", "::1"},
		{"host.:80", "host"},
		{" localhost ", "localhost"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := hostName(tt.in); got != tt.want {
			t.Errorf("hostName(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// A safe method that changes state is reachable by a plain <img src> from any
// origin: the cookie rides along, and CORS never blocks the REQUEST. Fetch
// metadata is what separates that from a legitimate call, and it is absent
// from every non-browser client, so the check has to stay fail-open there.
func TestCrossSiteRequests(t *testing.T) {
	s := New(nil) // routing only; no handler is reached on a refusal
	tests := []struct {
		name    string
		method  string
		target  string
		site    string
		mode    string
		dest    string
		refused bool
	}{
		{name: "cross-site mutation by GET", method: "GET", target: "/api/rescan",
			site: "cross-site", mode: "no-cors", dest: "image", refused: true},
		{name: "cross-site DELETE", method: "DELETE", target: "/api/library",
			site: "cross-site", mode: "cors", dest: "empty", refused: true},
		{name: "cross-site navigation to the API is still a mutation",
			method: "GET", target: "/api/setup?save=1",
			site: "cross-site", mode: "navigate", dest: "document", refused: true},
		{name: "the extension grant is exempt", method: "GET", target: "/api/search?q=a",
			site: "cross-site", mode: "cors", dest: "empty"},
		{name: "so is its preflight", method: "OPTIONS", target: "/api/dicts",
			site: "cross-site", mode: "cors", dest: "empty"},
		{name: "and /res/, which is a prefix route", method: "GET", target: "/res/d/img.png",
			site: "cross-site", mode: "no-cors", dest: "image"},
		{name: "a shared link still opens the page", method: "GET", target: "/",
			site: "cross-site", mode: "navigate", dest: "document"},
		{name: "same-origin is untouched", method: "GET", target: "/api/rescan",
			site: "same-origin", mode: "cors", dest: "empty"},
		{name: "typed in the address bar", method: "GET", target: "/api/rescan",
			site: "none", mode: "navigate", dest: "document"},
		{name: "a client that sends no fetch metadata", method: "GET", target: "/api/rescan"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRequest(tt.method, tt.target, nil)
			if tt.site != "" {
				r.Header.Set("Sec-Fetch-Site", tt.site)
				r.Header.Set("Sec-Fetch-Mode", tt.mode)
				r.Header.Set("Sec-Fetch-Dest", tt.dest)
			}
			w := httptest.NewRecorder()
			got := s.requireSameSite(w, r)
			if got == tt.refused {
				t.Fatalf("requireSameSite = %v (status %d), want refused = %v",
					got, w.Code, tt.refused)
			}
			if tt.refused && w.Code != http.StatusForbidden {
				t.Errorf("status = %d, want 403", w.Code)
			}
		})
	}
}
