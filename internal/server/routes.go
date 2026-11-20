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
//     statements, that rule would be enforced only by remembering to write
//     withCORS on three lines and not on a fourth.
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
	// The scripts are embedded in the same binary as index.html and versioned
	// with it, but the browser caches them SEPARATELY - index.html fresh from
	// "/", frame.js up to a week stale - so without the hash any change to the
	// protocol between the two (D41's message names, say) would break silently,
	// and only for dictionaries rendered in an iframe.
	serveAsset := func(mime string, body []byte) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", mime)
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
		{"POST", "/api/howto", s.handleHowtoCopy, "/api/howto", false},
		{"GET", "/api/ingest", s.handleIngest, "/api/ingest", false},
		{"GET", "/api/setup", s.handleSetup, "/api/setup", false},
		{"GET", "/api/library", s.handleLibrary, "/api/library", false},
		{"DELETE", "/api/library", s.handleRemoveLibrary, "/api/library", false},
		// prepared dictionaries whose source is gone (orphans.go, D156): list
		// them, and delete or keep them. Never CORS - the list names files on
		// the user's disk, and the answer deletes from the library.
		{"GET", "/api/orphans", s.handleOrphans, "/api/orphans", false},
		{"POST", "/api/orphans", s.handleResolveOrphans, "/api/orphans", false},
		{"GET", "/api/config", s.handleConfig, "/api/config", false},
		{"GET", "/api/prefs", s.handlePrefs, "/api/prefs", false},
		{"PUT", "/api/prefs", s.handleSavePrefs, "/api/prefs", false},
		// the reader's history and favorites (words.go). Never CORS: what a
		// person looked up is theirs, and an extension reads dictionaries only.
		{"GET", "/api/words", s.handleWords, "/api/words", false},
		{"POST", "/api/words", s.handleSaveWord, "/api/words", false},
		{"DELETE", "/api/words", s.handleClearWords, "/api/words", false},
		{"GET", "/api/reveal", s.handleReveal, "/api/reveal", false},
		// the user's own global stylesheets (style.go). Never CORS: the GET
		// reports a path on the user's disk and the PUT writes to it.
		{"GET", "/api/style", s.handleStyle, "/api/style", false},
		{"PUT", "/api/style", s.handleSaveStyle, "/api/style", false},
		// "I picked this one": prepare it now, before a query exists
		// (demand.go). Never CORS - it starts work and writes to the library.
		{"POST", "/api/demand", s.handleDemand, "/api/demand", false},
		// what a dictionary says about itself (about.go). Never CORS: an
		// annotation names a sidecar file on the user's disk, and the list
		// endpoint an extension does get deliberately carries no description.
		{"GET", "/api/about", s.handleAbout, "/api/about", false},
		// one page of the headword list (browse.go). Never CORS: the D69
		// grant is lookup, and this dumps a dictionary a page at a time.
		{"GET", "/api/browse", s.handleBrowse, "/api/browse", false},
		// the headwords a search would match, as one list (suggest.go, D199).
		// Never CORS, as browse: the grant is lookup, and this lists what a
		// dictionary holds.
		{"GET", "/api/suggest", s.handleSuggest, "/api/suggest", false},
		// one headword drawn at random (random.go, D202). Never CORS, as
		// browse: the grant is lookup.
		{"GET", "/api/random", s.handleRandom, "/api/random", false},
		// what the platform is doing to us (D64) - the Android shell's channel
		// for onStop / onTrimMemory / thermal / battery-saver, which the
		// exec'd server has no other way of learning.
		{"POST", "/api/power", s.handlePower, "/api/power", false},
		// The contract, served by the thing it describes.
		{"GET", "/api/openapi.yaml", s.handleOpenAPI, "/api/openapi.yaml", false},
		// lemma data: list what is installed and installable, install one,
		// remove one (D91). Never CORS - the list names a folder on the
		// user's disk, and the other two write to it.
		{"GET", "/api/lemmas", s.handleLemmas, "/api/lemmas", false},
		{"POST", "/api/lemmas", s.handleLemmaInstall, "/api/lemmas", false},
		{"DELETE", "/api/lemmas", s.handleLemmaRemove, "/api/lemmas", false},
		// importing an archive of dictionaries (intake.go): start or confirm
		// a job, poll it, cancel it. Never CORS - all three write to the
		// library, and the status names a folder on the user's disk.
		{"POST", "/api/intake", s.handleIntake, "/api/intake", false},
		{"GET", "/api/intake", s.handleIntakeStatus, "/api/intake", false},
		{"DELETE", "/api/intake", s.handleIntakeCancel, "/api/intake", false},
		// rebuilding the dictionaries an older build prepared (reindex.go):
		// start, poll, stop. Never CORS - it rewrites the library.
		{"POST", "/api/reindex", s.handleReindex, "/api/reindex", false},
		{"GET", "/api/reindex", s.handleReindexStatus, "/api/reindex", false},
		{"DELETE", "/api/reindex", s.handleReindexCancel, "/api/reindex", false},
		// the picker's groups (groups.go, D162): read, replace, reset to the
		// default. Never CORS - the file is the user's, and two of the three
		// write it.
		{"GET", "/api/groups", s.handleGroups, "/api/groups", false},
		{"PUT", "/api/groups", s.handleSaveGroups, "/api/groups", false},
		{"DELETE", "/api/groups", s.handleResetGroups, "/api/groups", false},
		// the user's own file store (userfiles.go): what their custom CSS,
		// or a dictionary they wrote themselves, can reference by URL. Never
		// CORS - the list names a folder on the user's disk, and the other
		// two write to it.
		{"GET", "/api/files", s.handleFiles, "/api/files", false},
		{"POST", "/api/files", s.handleFileUpload, "/api/files", false},
		{"DELETE", "/api/files", s.handleFileDelete, "/api/files", false},

		// ---- the app itself: pages and assets, not part of the API document.
		{"GET", "/", s.handleIndex, "", false},
		// the setup page stays reachable after first run: it is where folders
		// are edited, not just where they are first chosen
		{"GET", "/setup", s.handleSetupPage, "", false},
		// the lemma installer, reached from setup and from the app's
		// configuration disclosure
		{"GET", "/lemmas", s.handleLemmasPage, "", false},
		// the groups.ini editor, reached from the panel's configuration
		// disclosure next to the folders
		{"GET", "/groups", s.handleGroupsPage, "", false},
		// reading the dictionary instead of querying it (browse.go), reached
		// from the panel's action row and from each dictionary's own card
		{"GET", "/browse", s.handleBrowsePage, "", false},
		// The user's global stylesheets, served the way a res/ override is:
		// no-cache, because this is a file they are actively editing. Not
		// under /assets/ - nothing here is embedded in the binary, and the
		// immutable week-long cache those get would hide every edit.
		{"GET", "/style/", s.handleUserCSS, "", false},
		// The files that stylesheet references. Same folder, same no-cache
		// reasoning, and not under /assets/ for the same reason: /assets/ is
		// the binary's own embedded, immutably cached content.
		{"GET", "/files/", s.handleUserFile, "", false},
		{"GET", "/assets/frame.js", serveAsset("application/javascript; charset=utf-8", frameJS), "", false},
		{"GET", "/assets/pick.js", serveAsset("application/javascript; charset=utf-8", pickJS), "", false},
		{"GET", "/assets/speak.js", serveAsset("application/javascript; charset=utf-8", speakJS), "", false},
		{"GET", "/assets/setup.css", serveAsset("text/css; charset=utf-8", setupCSS), "", false},
		{"GET", "/assets/favicon.svg", serveFavicon, "", false},
		{"GET", "/favicon.ico", serveFavicon, "", false},
	}
}
