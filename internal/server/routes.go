// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import "net/http"

// route is one entry of the HTTP surface, as data rather than as twenty
// imperative registration calls. Two properties are worth asserting about that
// surface, and neither can be asserted about a list of statements:
//
//   - CORS is a security boundary (D69). Exactly three read-only routes may
//     answer a browser extension; every other route is same-origin. As
//     statements, that rule was enforced by remembering to write withCORS on
//     three lines and not on a fourth.
//   - Spec names the path this route is published as in web/openapi.yaml.
//     openapi_test.go walks this table against that file in both directions,
//     so an endpoint cannot be added, renamed or dropped without the document
//     following it.
type route struct {
	Method  string
	Pattern string // net/http ServeMux pattern, without the method
	Handler http.HandlerFunc

	// Spec is the OpenAPI path item this route appears under, which is not
	// always the mux pattern: /res/ is a prefix match and is published as
	// /res/{dict}/{path}. Empty means "not part of the API document" - the
	// HTML pages and the static assets, which are the app, not its contract.
	Spec string

	// CORS wraps the handler in the extension grant (cors.go). Read-only
	// routes only, and never one that touches settings or the library.
	CORS bool
}

// corsAllowed is the D69 allowlist, stated once. openapi_test.go asserts that
// the CORS flags in routes() are exactly this set, so widening the boundary
// takes an edit here and an edit there.
var corsAllowed = map[string]bool{
	"GET /api/dicts":  true,
	"GET /api/search": true,
	"GET /res/":       true,
}

func (s *Server) routes() []route {
	// Content-addressed: index.html asks for these with ?v=<hash of the file>,
	// so the URL changes whenever the file does and a week-long cache is safe.
	// It was NOT safe before. Both scripts are embedded in the same binary as
	// index.html and are versioned with it, but the browser cached them
	// SEPARATELY - index.html fresh from "/", frame.js up to a week stale -
	// so any change to the protocol between the two broke silently and only
	// for dictionaries rendered in an iframe. D41 renamed frame.js's lookup
	// message from "lookup" to "ref"/"pick"; a cached frame.js kept posting
	// the old name to an index.html that no longer listened for it, and every
	// entry:// link in a script-bearing dictionary stopped responding.
	serveScript := func(body []byte) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
			w.Header().Set("Cache-Control", "public, max-age=604800, immutable")
			_, _ = w.Write(body)
		}
	}
	// SVG favicon, plus a /favicon.ico route so browsers that fetch the
	// well-known path by default get the same mark instead of a 404.
	serveFavicon := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/svg+xml")
		w.Header().Set("Cache-Control", "public, max-age=604800")
		_, _ = w.Write(faviconSVG)
	}

	return []route{
		// ---- the client API: the three routes a browser extension reaches,
		// and the only ones that answer cross-origin (D69). An extension can
		// read dictionaries; it can neither read the user's preferences nor
		// touch the library.
		{"GET", "/api/dicts", s.handleDicts, "/api/dicts", true},
		{"GET", "/api/search", s.handleSearch, "/api/search", true},
		{"GET", "/res/", s.handleResource, "/res/{dict}/{path}", true},
		{"OPTIONS", "/api/dicts", s.handlePreflight, "/api/dicts", false},
		{"OPTIONS", "/api/search", s.handlePreflight, "/api/search", false},
		{"OPTIONS", "/res/", s.handlePreflight, "/res/{dict}/{path}", false},

		// ---- same-origin: what the web page and the platform shell use.
		{"GET", "/api/rescan", s.handleRescan, "/api/rescan", false},
		{"GET", "/api/ingest", s.handleIngest, "/api/ingest", false},
		{"GET", "/api/setup", s.handleSetup, "/api/setup", false},
		{"GET", "/api/library", s.handleLibrary, "/api/library", false},
		{"DELETE", "/api/library", s.handleRemoveLibrary, "/api/library", false},
		{"GET", "/api/config", s.handleConfig, "/api/config", false},
		{"GET", "/api/prefs", s.handlePrefs, "/api/prefs", false},
		{"PUT", "/api/prefs", s.handleSavePrefs, "/api/prefs", false},
		{"GET", "/api/reveal", s.handleReveal, "/api/reveal", false},
		// what the platform is doing to us (D64) - the Android shell's channel
		// for onStop / onTrimMemory / thermal / battery-saver, which the
		// exec'd server has no other way of learning.
		{"POST", "/api/power", s.handlePower, "/api/power", false},
		// The contract, served by the thing it describes.
		{"GET", "/api/openapi.yaml", s.handleOpenAPI, "/api/openapi.yaml", false},

		// ---- the app itself: pages and assets, not part of the API document.
		{"GET", "/", s.handleIndex, "", false},
		// the setup page stays reachable after first run: it is where folders
		// are edited, not just where they are first chosen
		{"GET", "/setup", s.handleSetupPage, "", false},
		{"GET", "/assets/mark.min.js", serveScript(markJS), "", false},
		{"GET", "/assets/frame.js", serveScript(frameJS), "", false},
		{"GET", "/assets/favicon.svg", serveFavicon, "", false},
		{"GET", "/favicon.ico", serveFavicon, "", false},
	}
}
