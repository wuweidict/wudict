// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"net/http"
	"strings"
)

// CORS for browser extensions (D69).
//
// The browser extension used to declare a host permission for
// http://127.0.0.1:6888/*, which bought it two things at once: a scary install
// prompt ("read your data on 127.0.0.1"), and a free pass through CORS. Dropping
// the permission removes the prompt, so the pass has to be issued here instead.
//
// Why an extension origin may be trusted with it at all: `Origin` is set by the
// browser and cannot be forged by page script — `fetch` refuses to let a page
// set it, and XHR strips it — and no web page can ever be served from
// chrome-extension:// or moz-extension://. So "the request came from an
// installed extension" is a fact the header can carry, unlike the value of any
// other header. What it cannot carry is WHICH extension on Firefox, where
// moz-extension://<uuid> is regenerated per installation; hence the grant is by
// scheme, with BROWSER_EXTENSIONS to pin exact origins when that is wanted.
//
// The grant covers the read-only client API only — /api/dicts, /api/search,
// /res/ — never prefs, config, library, ingest, rescan, reveal or power. An
// extension can therefore read dictionaries, which is the whole point, and can
// do nothing to the installation.

// extensionOrigin reports whether origin is a well-formed browser-extension
// origin: exactly a scheme and a host, no port, path, query, fragment or
// userinfo. `null`, `file://` and every http(s) origin are refused, so a web
// page gains nothing here that Local Network Access does not already give it.
func extensionOrigin(origin string) bool {
	var host string
	switch {
	case strings.HasPrefix(origin, "chrome-extension://"):
		host = origin[len("chrome-extension://"):]
	case strings.HasPrefix(origin, "moz-extension://"):
		host = origin[len("moz-extension://"):]
	default:
		return false
	}
	// Chrome ids are 32 letters, Firefox's are UUIDs. Restricting to that
	// alphabet is what rejects "chrome-extension://a/b", "…://a:1",
	// "…://u@a" and "…://a?x" without parsing any of them: none of ':' '/'
	// '?' '#' '@' '.' is in it.
	if host == "" || len(host) > 128 {
		return false
	}
	for i := 0; i < len(host); i++ {
		c := host[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-':
		default:
			return false
		}
	}
	return true
}

// allowOrigin decides whether this request's Origin gets the header. With
// BROWSER_EXTENSIONS unset, any extension may read dictionaries — the same
// reach any extension could take for itself by declaring the host permission,
// so the setting is a tightening, not the security boundary.
func (s *Server) allowOrigin(origin string) bool {
	if !extensionOrigin(origin) {
		return false
	}
	if len(s.BrowserExtensions) == 0 {
		return true
	}
	for _, allowed := range s.BrowserExtensions {
		if strings.EqualFold(allowed, origin) {
			return true
		}
	}
	return false
}

// setCORS applies the headers for one request. Vary: Origin goes on
// unconditionally — the response differs by Origin whether or not this one was
// allowed, and a cache that does not know that will serve the allowed response
// to a denied origin.
func (s *Server) setCORS(w http.ResponseWriter, r *http.Request) bool {
	w.Header().Add("Vary", "Origin")
	origin := r.Header.Get("Origin")
	if origin == "" || !s.allowOrigin(origin) {
		return false
	}
	// Echoed, never "*": with a concrete origin the browser enforces the
	// allowlist for us on every response. Never Allow-Credentials either —
	// there are no cookies to send, and turning it on would make a future
	// one reachable cross-origin.
	w.Header().Set("Access-Control-Allow-Origin", origin)
	return true
}

// withCORS wraps a read-only handler in the extension grant.
func (s *Server) withCORS(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.setCORS(w, r)
		h(w, r)
	}
}

// handlePreflight answers OPTIONS on the granted routes. The extension's own
// requests are CORS-simple (GET plus a safelisted `accept`) and never preflight,
// but ServeMux would answer a future one with 405 — a failure that says nothing
// about what is wrong.
func (s *Server) handlePreflight(w http.ResponseWriter, r *http.Request) {
	if s.setCORS(w, r) {
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "accept, content-type")
		w.Header().Set("Access-Control-Max-Age", "600")
	}
	w.WriteHeader(http.StatusNoContent)
}
