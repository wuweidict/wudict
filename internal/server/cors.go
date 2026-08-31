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
// browser and cannot be forged by page script - `fetch` refuses to let a page
// set it, and XHR strips it - and no web page can ever be served from
// chrome-extension:// or moz-extension://. So "the request came from an
// installed extension" is a fact the header can carry, unlike the value of any
// other header. What it cannot carry is WHICH extension on Firefox, where
// moz-extension://<uuid> is regenerated per installation; hence the grant is by
// scheme, with BROWSER_EXTENSIONS to pin exact origins when that is wanted.
//
// The grant covers the read-only client API only - /api/dicts, /api/search,
// /res/ - never prefs, config, library, ingest, rescan, reveal or power. An
// extension can therefore read dictionaries, which is the whole point, and can
// do nothing to the installation.
//
// WEB_ORIGINS extends the same three-endpoint grant to named http(s) page
// origins, for the case an extension cannot serve: a page of your own that
// wants to look words up. Its default is the opposite of BROWSER_EXTENSIONS's
// and deliberately so. An extension is something the user chose to install;
// a web page is whatever they happened to open, and `Origin` says which site
// asked but nothing about whether the user meant it to. So the extension list
// defaults to "any" and narrows, while the web list defaults to "none" and
// widens, one origin at a time, by an edit the user had to make.

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

// webOrigin splits an http(s) page origin into the two parts that identify it,
// and reports whether the string was an origin at all. A browser serialises one
// as scheme://host[:port] and nothing else, so a path, a query, a fragment,
// userinfo or whitespace means the value came from a config file with a typo in
// it - or from an attacker hoping a substring comparison would settle for
// "https://evil.example/#https://trusted.example". Neither may match.
//
// The default port is dropped because the browser drops it: a page on
// https://x.example:443 sends Origin: https://x.example, and a user who spells
// the port out in WEB_ORIGINS must still be understood.
func webOrigin(origin string) (scheme, hostport string, ok bool) {
	i := strings.Index(origin, "://")
	if i < 0 {
		return "", "", false
	}
	// `null`, `file://…` and every extension scheme fall out here. `null` is
	// what a sandboxed iframe and a file: page send, and it is not a name -
	// every one of them sends the same string, so it can never be allowlisted.
	switch {
	case strings.EqualFold(origin[:i], "http"):
		scheme = "http"
	case strings.EqualFold(origin[:i], "https"):
		scheme = "https"
	default:
		return "", "", false
	}
	hostport = origin[i+len("://"):]
	if hostport == "" {
		return "", "", false
	}
	// An allowlist by whole string only works if the string cannot carry
	// anything but a host and a port, so the alphabet is the check: none of
	// '/' '\' '?' '#' '@' or a space is in it. '[' and ']' are, for IPv6.
	for j := 0; j < len(hostport); j++ {
		c := hostport[j]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '-', c == '.', c == '_', c == ':', c == '[', c == ']':
		default:
			return "", "", false
		}
	}
	// The last ':' is a port separator only when it is outside an IPv6 literal.
	if j := strings.LastIndex(hostport, ":"); j > strings.LastIndex(hostport, "]") {
		host, port := hostport[:j], hostport[j+1:]
		if (scheme == "http" && port == "80") || (scheme == "https" && port == "443") {
			hostport = host
		}
	}
	return scheme, hostport, hostport != ""
}

// allowWebOrigin answers the WEB_ORIGINS grant. Empty list = no web page, which
// is why the length test comes first: the parse below must never be reachable
// as a way to widen a default that is meant to be closed.
func (s *Server) allowWebOrigin(origin string) bool {
	if len(s.WebOrigins) == 0 {
		return false
	}
	scheme, hostport, ok := webOrigin(origin)
	if !ok {
		return false
	}
	for _, allowed := range s.WebOrigins {
		// "*" is every site the user visits, opted into by hand. It still
		// echoes the concrete origin below rather than a literal "*", so a
		// future Allow-Credentials cannot silently become legal.
		if allowed == "*" {
			return true
		}
		as, ah, ok := webOrigin(allowed)
		if ok && as == scheme && strings.EqualFold(ah, hostport) {
			return true
		}
	}
	return false
}

// allowOrigin decides whether this request's Origin gets the header. With
// BROWSER_EXTENSIONS unset, any extension may read dictionaries - the same
// reach any extension could take for itself by declaring the host permission,
// so the setting is a tightening, not the security boundary. A web page has no
// such fallback: it is refused unless WEB_ORIGINS names it.
func (s *Server) allowOrigin(origin string) bool {
	if !extensionOrigin(origin) {
		return s.allowWebOrigin(origin)
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
// unconditionally - the response differs by Origin whether or not this one was
// allowed, and a cache that does not know that will serve the allowed response
// to a denied origin.
func (s *Server) setCORS(w http.ResponseWriter, r *http.Request) bool {
	w.Header().Add("Vary", "Origin")
	origin := r.Header.Get("Origin")
	if origin == "" || !s.allowOrigin(origin) {
		return false
	}
	// Echoed, never "*": with a concrete origin the browser enforces the
	// allowlist for us on every response. Never Allow-Credentials either -
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
// but ServeMux would answer a future one with 405 - a failure that says nothing
// about what is wrong.
func (s *Server) handlePreflight(w http.ResponseWriter, r *http.Request) {
	if s.setCORS(w, r) {
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "accept, content-type")
		w.Header().Set("Access-Control-Max-Age", "600")
		// Private Network Access: 127.0.0.1 is a private address, so Chrome
		// preflights a request to it from any page that is not itself local -
		// including the plain GET that would otherwise be CORS-simple - and
		// fails the request unless the answer opts in. Without this header
		// WEB_ORIGINS would appear to work in Firefox and do nothing in
		// Chrome. Sent only when asked for, and only to an origin already
		// allowed above.
		if r.Header.Get("Access-Control-Request-Private-Network") == "true" {
			w.Header().Set("Access-Control-Allow-Private-Network", "true")
		}
	}
	w.WriteHeader(http.StatusNoContent)
}
