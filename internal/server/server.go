// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/wuweidict/wudict/internal/artmark"
	"github.com/wuweidict/wudict/internal/config"
	"github.com/wuweidict/wudict/internal/dict"
	"github.com/wuweidict/wudict/internal/facet"
	"github.com/wuweidict/wudict/internal/fsx"
	"github.com/wuweidict/wudict/internal/ftsq"
	"github.com/wuweidict/wudict/internal/hilite"
	"github.com/wuweidict/wudict/internal/htmlref"
	"github.com/wuweidict/wudict/internal/intake"
	"github.com/wuweidict/wudict/internal/lang"
	"github.com/wuweidict/wudict/internal/logx"
	"github.com/wuweidict/wudict/internal/morph"
	"github.com/wuweidict/wudict/internal/search"
	"github.com/wuweidict/wudict/internal/speex"
	"github.com/wuweidict/wudict/internal/store"
	"github.com/wuweidict/wudict/internal/wildcard"
)

//go:embed web/index.html
var indexHTMLSrc []byte
var indexHTML = webAsset(indexHTMLSrc, htmlAsset)

//go:embed web/setup.html
var setupHTMLSrc []byte
var setupHTML = string(webAsset(setupHTMLSrc, htmlAsset))

//go:embed web/groups.html
var groupsHTMLSrc []byte
var groupsHTML = webAsset(groupsHTMLSrc, htmlAsset) // the groups.ini editor (D162)

//go:embed web/lemmas.html
var lemmasHTMLSrc []byte
var lemmasHTML = webAsset(lemmasHTMLSrc, htmlAsset) // the lemma-data installer (D91)

//go:embed web/browse.html
var browseHTMLSrc []byte
var browseHTML = webAsset(browseHTMLSrc, htmlAsset) // the page-by-page headword view (browse.go)

//go:embed web/setup.css
var setupCSSSrc []byte
var setupCSS = webAsset(setupCSSSrc, cssAsset) // palette and controls shared by setup.html and lemmas.html

//go:embed web/frame.js
var frameJSSrc []byte
var frameJS = webAsset(frameJSSrc, jsAsset) // bridge script for sandboxed article iframes

//go:embed web/pick.js
var pickJSSrc []byte
var pickJS = webAsset(pickJSSrc, jsAsset) // word-at-point for a double tap, loaded by both article surfaces

//go:embed web/speak.js
var speakJSSrc []byte
var speakJS = webAsset(speakJSSrc, jsAsset) // read-aloud of selected article text, fetched on the first selection

//go:embed web/favicon.svg
var faviconSVGSrc []byte

var faviconSVG = webAsset(faviconSVGSrc, htmlAsset) // "Lookup" mark: magnifier over headword lines

// Server exposes the registry over HTTP.
type Server struct {
	reg *Registry
	mux *http.ServeMux

	// dictRows keeps /api/dicts rows between requests (dictrows.go).
	dictRows rowCache

	// ConfigPath, when set, is where the first-run setup flow persists
	// DICT_DIR. In-memory state is updated regardless, so setup works
	// even when the folder came from a CLI flag or the file is read-only.
	ConfigPath string

	// User is the folder beside the wudict.toml in effect, holding what the
	// user owns besides it (userdir.go). Empty: none of it can be saved.
	User UserDir

	// Version identifies this build in the Server response header. A second
	// launch uses that header to recognise an already-running wudict on
	// the port - without it, "the port is busy" says nothing about WHO holds
	// it, and sending the user's browser to an unknown local service would be
	// worse than an error message.
	Version string

	// indexOnce caches the substitutions index.html needs that never change
	// after startup ({{VERSION}} in the About box, the {{FRAMEJS}}, {{PICKJS}} and {{SPEAKJS}} hashes,
	// {{ARTCSS}}, {{PREFSV}}).
	// Version is assigned after the Server is built, so this cannot be done at
	// embed time; doing it per request would re-copy the whole page on every
	// load. {{USERCSS}} is deliberately left standing here - see pageFor.
	indexOnce sync.Once
	indexBase []byte

	// The finished page, keyed by the user stylesheet it names. app.css is a
	// file the user edits at any moment, and both the cached page AND its
	// validator are derived from the substituted bytes, so neither may be
	// computed once: a sync.Once over the whole substitution would ship the
	// hash of a stylesheet that no longer exists, forever. One entry, because
	// there is one current stylesheet; ?style=off keys on "" and shares the
	// no-stylesheet page byte for byte.
	styleMu    sync.Mutex
	styledTag  string
	styledPage []byte
	styledETag string

	groups ownedFile[*facet.Rules]         // groups.ini (groups.go)
	styles map[string]*ownedFile[styleCSS] // app.css, article.css (style.go)
	words  wordLists                       // history.txt, favourites.txt (words.go)

	// DictDirOrigin / DictDirEditable describe where the dictionary folders
	// came from (config layering), so the UI can warn that a flag or an
	// environment variable will override anything saved to the file.
	DictDirOrigin   string
	DictDirEditable bool

	// ConfigProblems are the config file's lines that set nothing
	// (config.Config.Problems), shown on the setup page.
	ConfigProblems []string

	// Effective is every tunable key's resolved value and origin, published by
	// /api/config for shells that override them per device (D101).
	Effective map[string]config.Setting

	// Speexdec is the path to the external speexdec binary. It is used when
	// UseExternalSpeex is set, or as a fallback when the in-process decoder is
	// not compiled in (CGO_ENABLED=0) or fails on a given file.
	Speexdec string

	// UseExternalSpeex forces the external speexdec binary even when the
	// in-process (cgo) libspeex decoder is available (config SPEEX_BACKEND).
	UseExternalSpeex bool

	// spxLocks single-flights .spx→WAV transcodes per cache key so two
	// concurrent plays of the same word don't spawn two speexdec processes
	// racing the same output file. Keyed by wav cache path → *sync.Mutex.
	spxLocks sync.Map

	// nulSeen keys "<dictID>\x00<resource>" for damaged blobs already
	// reported, so the warning states the fact once instead of on every
	// article that references the file.
	nulSeen sync.Map

	// AutoIndex, when true (config AUTO_INDEX=on, the default), prepares a
	// dictionary's headword index the first time it is searched - silently,
	// in the background - so accent-insensitive search works on the next
	// query without the user ever asking. The full-text index and media stay
	// opt-in per dictionary.
	AutoIndex bool

	// BrowserExtensions optionally pins which extension origins may read the
	// client API cross-origin (config BROWSER_EXTENSIONS). Empty = any
	// chrome-extension:// or moz-extension:// origin, which is what Firefox
	// forces anyway: its moz-extension UUID is regenerated per install, so
	// there is no stable id to list. See cors.go (D69).
	BrowserExtensions []string

	// AllowRemoteDelete lets a browser on another machine delete a dictionary
	// (config ALLOW_REMOTE_DELETE). Loopback may regardless; this is only
	// about the remote caller. New() sets it false, matching the config
	// default, so a Server built directly in a test behaves like a real one.
	AllowRemoteDelete bool

	// Morph lemmatizes a word when a search finds nothing anywhere, so an
	// inflected form still reaches its entry (O3, config MORPH_CACHE). Nil -
	// which is what a Server built directly in a test has - is disabled, and
	// every call site goes through Cache.Enabled.
	Morph *morph.Cache

	// random remembers what the random word learns about each dictionary -
	// whether it files its headwords in capitals, and their language - so a
	// request does not learn it again (random.go). The zero value is ready.
	random randomMemo

	// WebOrigins lists http(s) page origins allowed to read the same three
	// endpoints cross-origin (config WEB_ORIGINS). Empty - the default - means
	// none: a web page gets nothing here. A single "*" allows any origin. See
	// cors.go (D69).
	WebOrigins []string

	// TrustedHosts lists DNS names, beyond "localhost", that may appear in a
	// request's Host header (config TRUSTED_HOSTS, plus SERVER_IP when that is
	// a name - D104 Am. 1, added by the caller). Empty - the default -
	// means none: every legitimate client addresses this server by IP, so the
	// only thing a name buys is DNS rebinding. See host.go. A single "*"
	// disables the check, for a reverse proxy whose name we cannot know.
	TrustedHosts []string

	// AuthToken, when set, is the capability token every route outside
	// authFree must present (auth.go). Empty means the server is open, which
	// is the right default for a loopback bind and the wrong one for any
	// other; the CLI resolves which case this is (config.AuthRequired).
	AuthToken string

	// LemmaDir is where /api/lemmas installs lemma files (config LEMMA_DIR) -
	// the same folder Morph indexes, which is what makes an install visible to
	// the next search without a restart.
	LemmaDir string

	// LemmaURL is the catalogue those installs come from (config LEMMA_URL).
	// It is server state and never a request parameter: a client-chosen URL
	// would turn this endpoint into a fetcher for whatever address the machine
	// running wudict can reach.
	LemmaURL string

	// ImportKeep (config IMPORT_KEEP) decides what becomes of the archive an
	// import came from: "ask" leaves the question to the page, "keep" and
	// "delete" answer it. A FAILED import always keeps it, whatever this
	// says - see intake.Manager.Confirm.
	ImportKeep string

	// ImportURLHosts (config IMPORT_URL_HOSTS) optionally restricts which
	// sites this server will fetch an archive from. Empty - the default - is
	// any site: the link came from the person using the app, who found the
	// dictionary somewhere no list was ever going to enumerate (D139). It
	// earns its keep on a wudict other people can reach, where the caller
	// and the owner are no longer the same person.
	//
	// ImportInsecure (IMPORT_INSECURE) is a different question and is NOT
	// opened by the above: it lifts the two refusals that protect the machine
	// this runs on - plain http, and an address on the local network. Both
	// are server state for the same reason LemmaURL is: policy a request
	// could widen is not policy.
	ImportURLHosts []string
	ImportInsecure bool

	// intake owns the single import job (intake.go). The zero value is
	// usable; intakeOnce wires its one callback the first time a request
	// reaches it, so a Server built directly in a test needs no constructor.
	intake     intake.Manager
	intakeOnce sync.Once

	// jobs is the long-running work (jobs.go): the panel's Rebuild, lemma
	// downloads, index changes. The zero value is usable.
	jobs jobTable

	// lemmas holds the installer's caches - the catalogue and the file
	// digests; its downloads are jobs (jobs). Built on first use so a Server
	// made directly in a test needs no constructor.
	lemmas lemmaState
}

func New(reg *Registry) *Server {
	s := &Server{reg: reg, mux: http.NewServeMux(), AllowRemoteDelete: false}
	s.initGroups()
	s.initStyles()
	s.initWords()
	// The surface is a table (routes.go), so it can be asserted about: the
	// CORS boundary and the OpenAPI document are both checked against it.
	for _, rt := range s.routes() {
		h := rt.Handler
		if rt.CORS {
			h = s.withCORS(h)
		}
		if !authFree[rt.Method+" "+rt.Pattern] {
			h = s.withAuth(h)
		}
		s.mux.HandleFunc(rt.Method+" "+rt.Pattern, h)
	}
	return s
}

// ServerHeader is the value wudict answers with (plus its version), and
// the token a second launch looks for.
const ServerHeader = "wudict"

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	id := ServerHeader
	if s.Version != "" {
		id += "/" + s.Version
	}
	w.Header().Set("Server", id)
	defer func() {
		if rec := recover(); rec != nil {
			// Path, never RequestURI: the query string carries ?k=<token>,
			// and a log file is exactly where it must not end up (auth.go
			// scrubs it from the address bar for the same reason).
			logx.V("PANIC %s %s: %v", r.Method, r.URL.Path, rec)
			logx.Warn("panic serving %s: %v", r.URL.Path, rec)
			httpErr(w, 500, "internal error: %v", rec)
		}
	}()
	// Before routing, not per handler: the point of the check is that no
	// route can be reached with a forged authority, and a per-route opt-in
	// is a list somebody eventually forgets to extend (host.go).
	if !s.requireHost(w, r) {
		logx.V("%s %s: refused Host %q", r.Method, r.URL.Path, r.Host)
		return
	}
	// Same reasoning, same place: the initiator matters for every route, so
	// the check cannot be a per-handler opt-in (host.go).
	if !s.requireSameSite(w, r) {
		logx.V("%s %s: refused cross-site request", r.Method, r.URL.Path)
		return
	}
	// Nothing this server serves has any business naming its own URLs to a
	// third party, and one of those URLs may carry ?k=. Set for every
	// response, including the ones an article's outbound requests inherit.
	w.Header().Set("Referrer-Policy", "no-referrer")
	// Also before routing: a tokenised link must work on whatever route the
	// user was sent to, and the redirect that scrubs the token from the
	// address bar has to happen before a handler writes a body (auth.go).
	if s.grantCookie(w, r) {
		return
	}
	s.mux.ServeHTTP(w, r)
	logx.V("%s %s (%s)", r.Method, r.URL.Path, time.Since(start).Round(time.Microsecond))
}

// writeJSON emits v as plain UTF-8. SetEscapeHTML(false) matters: the default
// encoder turns every <, > and & into six-byte \u00XX escapes, which roughly
// doubles the size of HTML-heavy search payloads for no benefit (we serve
// application/json, never inline it into HTML).
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

// decodeJSON reads a JSON request body of at most limit bytes into v. On
// failure it has already answered - 413 for a body over the limit, 400 for
// anything else - and the caller only returns.
func decodeJSON(w http.ResponseWriter, r *http.Request, v any, limit int64) bool {
	err := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit)).Decode(v)
	switch {
	case err == nil:
		return true
	case errors.As(err, new(*http.MaxBytesError)):
		httpErr(w, http.StatusRequestEntityTooLarge, "the request is larger than %d bytes", limit)
	default:
		httpErr(w, http.StatusBadRequest, "bad request: %v", err)
	}
	return false
}

func httpErr(w http.ResponseWriter, code int, format string, args ...any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(map[string]string{"error": fmt.Sprintf(format, args...)})
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// The page is the only thing that names the asset hashes, so it must never
	// be served from cache without asking: a stale index.html would ask for a
	// stale script by its old hash and put the two out of step again (D45).
	w.Header().Set("Cache-Control", "no-cache")
	// first-run: no dictionaries yet → serve the setup page instead. No
	// validator here: this body is a function of registry state that nothing
	// versions, so an ETag would be a promise we cannot keep. The wudict
	// howto counts: with it listed the app has something to show, and the
	// page opens on it instead, with the way to add dictionaries on top.
	if s.reg.Count() == 0 {
		_, _ = io.WriteString(w, setupPage(s.reg.Dirs(), 0))
		return
	}
	// "no-cache" means REVALIDATE, not "never store" - and revalidation needs
	// something to revalidate against. Without a validator the browser had no
	// way to ask "still the same?", so every load, every reload, re-sent the
	// whole 100 KB page. With one, an unchanged page costs a 304 and no body,
	// and the freshness guarantee D45 depends on is unchanged: the browser
	// still asks the server on every single load.
	//
	// ServeContent rather than a hand-rolled `If-None-Match == etag`: the
	// header is a LIST, it may be `*`, and GET compares weakly, so entries may
	// arrive as `W/"…"`. net/http implements all of that; an equality test
	// would quietly fail to match a validator we ourselves issued.
	//
	// A zero modtime deliberately emits no Last-Modified: these bytes are
	// embedded in the binary and have no meaningful date, and offering a
	// second validator we cannot stand behind is worse than offering one.
	// ?style=off is the way back in after app.css has hidden its own editor
	// (style.go). It resolves to the same bytes as "no stylesheet at all", so
	// it costs no second cache entry.
	tag := ""
	if !styleOff(r) {
		tag = s.appStyleTag()
	}
	page, etag := s.pageFor(tag)
	w.Header().Set("ETag", etag)
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(page))
}

// basePage is index.html with everything that is fixed for this process
// already substituted, and {{USERCSS}} still standing.
func (s *Server) basePage() []byte {
	s.indexOnce.Do(func() {
		v := s.Version
		if v == "" {
			v = "dev"
		}
		page := strings.ReplaceAll(string(indexHTML), "{{VERSION}}", v)
		page = strings.ReplaceAll(page, "{{FRAMEJS}}", assetTag(frameJS))
		page = strings.ReplaceAll(page, "{{PICKJS}}", assetTag(pickJS))
		page = strings.ReplaceAll(page, "{{SPEAKJS}}", assetTag(speakJS))
		// the state.json format the page writes its lists in (handleSavePrefs)
		page = strings.ReplaceAll(page, "{{PREFSV}}", strconv.Itoa(prefsVersion))
		// The role stylesheet for articles wudict writes itself
		// (internal/artmark). It is a floor under BOTH article surfaces, so
		// it is substituted once here and index.html hands it to the shadow
		// root and to the iframe from the same constant.
		page = strings.ReplaceAll(page, "{{ARTCSS}}", artmark.DefaultCSS)
		s.indexBase = []byte(page)
	})
	return s.indexBase
}

// pageFor finishes the page for one user stylesheet, identified by its content
// hash ("" for none). The link is render-blocking and last in <head>, which is
// two decisions in one element: last so the user's rules win ties against the
// app's own <style>, and a <link> rather than an inlined <style> so a sepia
// reader never sees a white flash on load - and so no CSS ever has to be
// escaped into an HTML document.
//
// The ETag is computed from the finished bytes - the version stamp, the asset
// hashes AND the stylesheet hash are all part of what the browser is holding,
// so a change to any of them must invalidate.
func (s *Server) pageFor(tag string) ([]byte, string) {
	s.styleMu.Lock()
	defer s.styleMu.Unlock()
	if s.styledPage != nil && s.styledTag == tag {
		return s.styledPage, s.styledETag
	}
	link := ""
	if tag != "" {
		link = `<link rel="stylesheet" href="/style/` + appCSSName + `?v=` + tag + `">`
	}
	page := []byte(strings.ReplaceAll(string(s.basePage()), "{{USERCSS}}", link))
	s.styledTag, s.styledPage, s.styledETag = tag, page, `"`+assetTag(page)+`"`
	return s.styledPage, s.styledETag
}

// handleSetupPage serves the folder editor on demand (the same page first run
// shows). Reachable at any time so "which folders am I scanning?" is always
// answerable from the app itself, never only from a terminal.
func (s *Server) handleSetupPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = io.WriteString(w, setupPage(s.reg.Dirs(), s.reg.UserCount()))
}

// setupPage renders the folder chooser/editor. The intro names a single
// folder, but never repeats a list the editable rows below already show -
// four long paths in a sentence pushed the actual controls off the screen.
func setupPage(dirs []string, serving int) string {
	var intro string
	switch {
	case serving > 0:
		intro = fmt.Sprintf("Serving %s from %s.",
			logx.Plural(serving, "dictionary", "dictionaries"), logx.Plural(len(dirs), "folder", "folders"))
	case len(dirs) == 1:
		state := "contains no dictionaries yet"
		if _, err := os.Stat(dirs[0]); err != nil {
			state = "does not exist"
		}
		intro = fmt.Sprintf("Your dictionary folder <code>%s</code> %s.", htmlref.EscapeAttr(dirs[0]), state)
	case len(dirs) > 1:
		intro = fmt.Sprintf("None of your %d dictionary folders hold dictionaries yet.", len(dirs))
	default:
		intro = "No dictionary folder is configured yet."
	}
	return strings.ReplaceAll(strings.ReplaceAll(setupHTML, "{{INTRO}}", intro), "{{CSS}}", cssTag)
}

// cssTag content-addresses the shared stylesheet, so its week-long cache is
// safe for exactly the reason frame.js's is (D45): the URL changes when the
// bytes do.
var cssTag = assetTag(setupCSS)

// handleLemmasPage serves the lemma-data installer (D91). No state is baked
// in: the page asks /api/lemmas for everything it draws, which is also what it
// polls while a download runs.
func (s *Server) handleLemmasPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(lemmasPage)
}

var lemmasPage = bytes.ReplaceAll(lemmasHTML, []byte("{{CSS}}"), []byte(cssTag))

// handleLibrary lists the previously imported dictionaries kept in the db dir
// - the ones the setup page offers to use, and the basis of the USE_CACHED
// choice. Reading it never enrolls them: listing is not consent.
func (s *Server) handleLibrary(w http.ResponseWriter, r *http.Request) {
	all, err := store.Library()
	if err != nil {
		httpErr(w, 500, "reading library: %v", err)
		return
	}
	// The wudict howto's own library folder is the app's, not something the
	// user imported: listing it under "previously imported" would say so.
	entries := []store.LibEntry{}
	for _, e := range all {
		if !s.reg.IsBuiltin(e.Source) {
			entries = append(entries, e)
		}
	}
	writeJSON(w, map[string]any{
		"dir":       store.DefaultDBDir(),
		"count":     len(entries),
		"useCached": s.reg.UseCached(),
		"entries":   entries,
	})
}

// setUseCached opts the library in or out, live, and remembers the choice so
// the setup page never asks again.
func (s *Server) setUseCached(on bool) error {
	if err := s.reg.SetUseCached(on); err != nil {
		return err
	}
	if s.ConfigPath == "" {
		return nil
	}
	v := "0"
	if on {
		v = "1"
	}
	return config.SaveKey(s.ConfigPath, "USE_CACHED", v)
}

// handleSetup drives the first-run choices. With a path it validates a
// dictionary folder and, with save=1, switches the registry to it live and
// persists DICT_DIR. With useCached=1 it enrolls the previously imported
// dictionaries (persisting USE_CACHED) - on its own, or together with a
// folder. Clicking a Use button IS the "don't ask again": the setup page only
// appears while the registry is empty.
func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	raw := strings.TrimSpace(q.Get("path"))
	save, _ := queryFlag(q, "save")
	// tri-state: absent leaves the setting alone, on turns the library on, off
	// turns it off - clearing the checkbox must be able to undo an earlier yes
	useCached, useCachedSent := queryFlag(q, "useCached")

	if raw == "" {
		if !save || !useCached {
			httpErr(w, 400, "missing path parameter")
			return
		}
		out := map[string]any{"useCached": true}
		if err := s.setUseCached(true); err != nil {
			out["error"] = err.Error()
		} else {
			out["saved"] = true
			out["found"] = s.reg.UserCount()
		}
		writeJSON(w, out)
		return
	}
	dir := config.ExpandHome(raw)
	abs, err := filepath.Abs(dir)
	if err == nil {
		dir = abs
	}
	st, err := os.Stat(dir)
	if err != nil {
		writeJSON(w, map[string]any{"path": dir, "error": "folder not found"})
		return
	}
	if !st.IsDir() {
		writeJSON(w, map[string]any{"path": dir, "error": "not a folder"})
		return
	}
	paths, err := dict.Discover(dir)
	if err != nil {
		writeJSON(w, map[string]any{"path": dir, "error": err.Error()})
		return
	}
	out := map[string]any{"path": dir, "found": len(paths)}
	// several folders at once: report the DEDUPED total, since folders may
	// overlap and summing per-folder counts would promise more dictionaries
	// than the registry will actually serve
	if multi := q["path"]; len(multi) > 1 {
		if dirs, total, verr := s.resolveDirs(multi); verr == nil {
			out["dirs"], out["found"] = dirs, total
		}
	}
	if save {
		// saving may carry several folders (the setup page's list); validation
		// above always concerns the first, which is what live typing checks.
		dirs, total, verr := s.resolveDirs(q["path"])
		switch {
		case verr != nil:
			out["error"] = verr.Error()
		case total == 0:
			out["error"] = "no dictionaries found in this folder"
		default:
			out["dirs"] = dirs
			out["found"] = total
			if err := s.reg.SetDirs(dirs); err != nil {
				out["error"] = err.Error()
				break
			}
			out["saved"] = true
			if s.ConfigPath != "" {
				if err := config.SaveKeyRaw(s.ConfigPath, "DICT_DIR", config.FormatList(dirs)); err != nil {
					out["warning"] = "folders switched, but saving config failed: " + err.Error()
				}
			}
			if useCachedSent {
				out["useCached"] = useCached
				if err := s.setUseCached(useCached); err != nil {
					out["warning"] = "folders saved, but the previously imported dictionaries setting failed: " + err.Error()
				}
			}
		}
	}
	writeJSON(w, out)
}

// resolveDirs cleans up the folder list a save carries: ~-expanded, absolute,
// blanks and exact duplicates dropped, order preserved. It reports the total
// number of dictionaries across them (deduplicated the same way the registry
// will), and refuses a folder that is the library itself.
func (s *Server) resolveDirs(raw []string) ([]string, int, error) {
	var dirs []string
	for _, p := range raw {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		p = config.ExpandHome(p)
		if abs, err := filepath.Abs(p); err == nil {
			p = abs
		}
		if dict.SameDir(p, store.DefaultDBDir()) {
			return nil, 0, fmt.Errorf("%s is wudict's own library folder - choose the folder holding your dictionary files", p)
		}
		dirs = append(dirs, p)
	}
	// the same folder spelled two ways (a symlink, a case variant) must not be
	// saved twice - string comparison alone would not catch either
	dirs = dict.DedupeDirs(dirs)
	if len(dirs) == 0 {
		return nil, 0, fmt.Errorf("no folder given")
	}
	found, _, _ := dict.DiscoverAll(dirs)
	return dirs, len(found), nil
}

// currentFeatures reads what a dictionary has prepared right now, so a request
// that names one feature leaves the others alone.
func (s *Server) currentFeatures(e *entry) features {
	p, ok := preparedInfo(e.Path)
	if !ok || p.Meta == nil {
		return features{}
	}
	// an unpaired media.db serves nothing, so it does not count as packed
	return features{FullText: p.Plan.FullText, Media: p.MediaPaired()}
}

// dictInfo is the /api/dicts row.
type dictInfo struct {
	ID      string    `json:"id"`
	Name    string    `json:"name"`
	Format  string    `json:"format"`
	Path    string    `json:"path"`
	Entries int       `json:"entries"`
	Caps    dict.Caps `json:"caps"`
	DBPath  string    `json:"dbPath,omitempty"` // exposed per D7: users share these files
	Error   string    `json:"error,omitempty"`

	// Outdated: the prepared data no longer matches what this build would
	// prepare from the source (store.Stale) - an older build wrote it, the
	// source changed, or it cannot be read. Reported, never acted on: the
	// panel sums these into one Rebuild the user starts (reindex.go). Never
	// set for a dictionary whose source is gone, which nothing can rebuild.
	Outdated bool `json:"outdated,omitempty"`

	// OwnText: full text is answered from an index the dictionary's own file
	// carries (D194), so Caps.FTS is set with no full-text index of ours and
	// there is none to build or remove.
	OwnText bool `json:"ownText,omitempty"`

	// TooLarge: Error is that the dictionary is too large to open on this
	// system (dict.ErrTooLarge) - its text unpacks past its reader's bound, or
	// past the free space to hold it. The page warns about it once, where an
	// error in the panel would wait to be found.
	TooLarge bool `json:"tooLarge,omitempty"`

	// Groups: the derived memberships the dictionary picker offers as single
	// choices ("English", "Encyclopedias") instead of a hundred names. Derived
	// from this row's own name, path and declared language and never stored -
	// see internal/facet, which also says why absence never becomes a value.
	Groups []facet.Group `json:"groups,omitempty"`

	// Job is the change to this dictionary's indexes running now (an ingest
	// job, jobs.go), so a page loaded mid-way shows it and follows it with
	// /api/ingest; absent when none runs.
	Job *jobProgress `json:"job,omitempty"`

	// ArticleLang: the ISO 639-1 language the articles are written in, the
	// default voice for reading a selection aloud (facet.ArticleLang). Derived
	// alongside Groups from the same evidence; "" when nothing says.
	ArticleLang string `json:"articleLang,omitempty"`
	// LemmaLang is the language the lemma retry searches this dictionary with
	// (lemmaLangOf), kept for the Lemmatization page's counts; not sent.
	LemmaLang string `json:"-"`

	// provenance (panel display): where the dictionary came from and what
	// derived files exist. All optional and filesystem-cheap.
	Source   string   `json:"source,omitempty"`    // foreign source file, if still on disk
	MediaSrc []string `json:"mediaSrc,omitempty"`  // companion media sources (.mdd, .files.zip, res/)
	TextDB   string   `json:"textDB,omitempty"`    // prepared text.db, if present
	MediaDB  string   `json:"mediaDB,omitempty"`   // packed media.db, if present
	Folder   string   `json:"folder,omitempty"`    // library folder holding them (the transferable unit)
	DBSize   int64    `json:"dbSize,omitempty"`    // bytes of text.db - what the indexes actually cost
	MediaSz  int64    `json:"mediaSize,omitempty"` // bytes of media.db
	FTSSize  int64    `json:"ftsSize,omitempty"`   // bytes of fulltext.db; absent when the index is inside text.db
	HasMedia bool     `json:"hasMedia,omitempty"`  // packable binary resources exist (drives "pack media")

	// Builtin: shipped with the app (the wudict howto), not added by the user.
	// It cannot be removed, and a library of builtins alone is an empty one.
	Builtin bool `json:"builtin,omitempty"`
}

// dictMsg is one NDJSON line of /api/dicts:
//
//	{"t":"begin","total":N}   how many rows follow - known from the registry alone
//	{"t":"dict","dict":{…}}   one resolved row, in completion order
//	{"t":"busy","busy":[…]}   the rows pending busyAfter or longer, every busyTick
//	                          while there are any; once more, empty, when the last lands
//	{"t":"end","etag":"…"}    every row sent; etag when the answer may be kept
type dictMsg struct {
	T     string    `json:"t"`
	Total int       `json:"total,omitempty"`
	Dict  *dictInfo `json:"dict,omitempty"`
	Busy  []busyRow `json:"busy,omitempty"`
	// GroupsProblems counts what groups.ini skipped (groups.go), on "begin":
	// a hand edit with a broken regex is otherwise invisible until someone
	// opens the editor, so the page marks the way to it.
	GroupsProblems int `json:"groupsProblems,omitempty"`
	// ETag, on "end", validates this whole answer: sent back as If-None-Match
	// it gets a 304 while nothing in the list has changed. Absent when the
	// answer must not be kept - a row carries an error or a running job, or
	// what a row was derived from changed while it was derived.
	ETag string `json:"etag,omitempty"`
}

// busyRow is a row that has kept the list waiting busyAfter or longer, and
// what its open is doing (dict.Activity: Phase "" when the open says
// nothing). Name is the file's (dict.Name): the row's own name is not known
// until the open that is taking the time has finished.
type busyRow struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Phase string `json:"phase,omitempty"`
	Done  int64  `json:"done,omitempty"`
	Total int64  `json:"total,omitempty"`
}

// busyAfter is how long a row may take before the stream names it and what it
// is doing (D169): a cold open takes 0.2-1 s, a dictionary indexing itself on
// first open minutes. busyTick is how often the names are sent again with
// their progress. Variables for the tests.
var (
	busyAfter = 10 * time.Second
	busyTick  = time.Second
)

// busyRows are the rows of pending (entry → when its derivation began) that
// have taken busyAfter or longer by now, longest first.
func busyRows(pending map[*entry]time.Time, now time.Time) []busyRow {
	type slow struct {
		e     *entry
		since time.Time
	}
	var ss []slow
	for e, t := range pending {
		if now.Sub(t) >= busyAfter {
			ss = append(ss, slow{e, t})
		}
	}
	sort.Slice(ss, func(i, j int) bool { return ss[i].since.Before(ss[j].since) })
	rows := make([]busyRow, 0, len(ss))
	for _, s := range ss {
		r := busyRow{ID: s.e.ID, Name: dict.Name(s.e.Path)}
		if a, ok := dict.ActivityOf(s.e.Path); ok {
			r.Phase, r.Done, r.Total = a.Phase, a.Done, a.Total
		}
		rows = append(rows, r)
	}
	return rows
}

// handleDicts streams the dictionary list as newline-delimited JSON, for the
// same reason handleSearch does (D12): the fan-out below resolves metadata in
// parallel, and delivering it as one array would make time-to-first-row the
// *sum* of every dictionary's resolution instead of the slowest single one.
// With ~100 dictionaries that gap is the entire startup wait, during which the
// client would know nothing at all - not even how many dictionaries are coming.
//
// So `total` goes out first, from cheap entry ids with no opens at all: the
// client can say "0 of 105" immediately and unblock search the instant the
// first row lands, rather than guessing from an empty list (D30).
//
// Every row is keyed by stat calls first (rowKey). The keys make the list's
// validator, so a client that still holds the previous answer is told so with
// a 304 before any row is derived; and a row whose key is unchanged comes from
// the row cache. Only a changed row pays the header probe, meta read or open.
//
// The validator goes out on `end`, never as an ETag header: whether the answer
// may be kept is known only once every row is in (an error or a running job
// says no), and a header would let the browser's own cache revalidate and
// replay a body that carried one.
func (s *Server) handleDicts(w http.ResponseWriter, r *http.Request) {
	entries := s.reg.all()
	groups := s.groupsNow()
	problems := facet.CountProblems(groups.problems)
	global := s.rowsGlobal(groups)
	keys := rowKeys(entries, global)
	ids := make(map[string]bool, len(entries))
	for _, e := range entries {
		ids[e.ID] = true
	}
	s.dictRows.keep(ids)
	tag := s.listTag(entries, keys, problems)
	if inm := r.Header.Get("If-None-Match"); inm != "" && matchesTag(inm, tag) {
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusNotModified)
		return
	}

	st, ok := newStream(w, "application/x-ndjson; charset=utf-8")
	if !ok {
		return
	}
	// the workers below write concurrently; stream serialises them
	writeLine := func(m dictMsg) { st.line(m) }

	writeLine(dictMsg{T: "begin", Total: len(entries), GroupsProblems: problems})

	// the rows being derived now, and since when: what the busy frames name
	var pendMu sync.Mutex
	pending := map[*entry]time.Time{}

	// same width as a search fan-out, and for the same reason: this is the
	// other place the whole library is touched at once (search.Workers)
	sem := make(chan struct{}, search.Workers())
	var wg sync.WaitGroup
	var keepable atomic.Bool
	keepable.Store(true)
	for i, e := range entries {
		wg.Add(1)
		go func(e *entry, key string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			pendMu.Lock()
			pending[e] = time.Now()
			pendMu.Unlock()
			info, held := s.dictInfoFor(e, global, key)
			pendMu.Lock()
			delete(pending, e)
			pendMu.Unlock()
			if !held || info.Error != "" || info.Job != nil {
				keepable.Store(false)
			}
			writeLine(dictMsg{T: "dict", Dict: &info})
		}(e, keys[i])
	}
	// The busy frames, until every row is in. A frame snapshotted just before
	// a row landed can follow that row on the stream; the page drops a name
	// whose row it already has.
	stop, stopped := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(stopped)
		tick := time.NewTicker(busyTick)
		defer tick.Stop()
		named := false
		for {
			select {
			case <-stop:
				// the last named row landed between two ticks: the empty
				// frame the stream promises still goes out, before "end"
				if named {
					writeLine(dictMsg{T: "busy"})
				}
				return
			case now := <-tick.C:
				pendMu.Lock()
				rows := busyRows(pending, now)
				pendMu.Unlock()
				if len(rows) > 0 || named {
					writeLine(dictMsg{T: "busy", Busy: rows})
				}
				named = len(rows) > 0
			}
		}
	}()
	wg.Wait()
	close(stop)
	<-stopped
	end := dictMsg{T: "end"}
	if keepable.Load() {
		end.ETag = tag
	}
	writeLine(end)
}

// dictInfoFor resolves one dictionary's list row: from the row cache while
// key still describes the files, else cheaply when possible - a header-only
// Probe locates a content-matched text.db and reads its meta
// (name/entry_count/ingest_level) without opening the direct backend; a
// probeable format with no cache reports direct caps (exact+prefix);
// everything else falls back to a full open for real caps.
//
// A derived row is kept only under a key that held across its derivation, and
// held reports whether it did. Deriving can change what it reads - a full
// open prepares a DSL, BGL or wudict markdown file on the spot - and a row
// describing the prepared state, filed under the key from before it, would be
// served again the day that state is removed and the old key comes back. The
// list validator is built from the same keys, so an answer holding such a row
// is not offered for keeping either (handleDicts).
func (s *Server) dictInfoFor(e *entry, global, key string) (info dictInfo, held bool) {
	info, held = s.dictRows.get(e.ID, key)
	if !held {
		info = s.diskDictInfo(e)
		held = rowKey(e, global) == key
		if held && info.Error == "" {
			s.dictRows.put(e.ID, key, info)
		}
	}
	s.rowState(e, &info)
	return info, held
}

// diskDictInfo is the part of a row derived from files, which the row cache
// keeps.
func (s *Server) diskDictInfo(e *entry) dictInfo {
	info := s.baseDictInfo(e)
	// a library folder that exists but is not prepared-for-this-source (an
	// unreadable or other-schema text.db, or a source edited since) is
	// outdated too - the prepared branch above never sees it
	if info.DBPath == "" && rebuildable(e.Path) {
		if _, ok := store.LookupDir(e.Path); ok {
			info.Outdated = true
		}
	}
	addProvenance(&info, e.Path)
	return info
}

func (s *Server) baseDictInfo(e *entry) dictInfo {
	// a prepared dictionary answers the whole row from its own meta - no
	// probe, no direct open, and it works for every format (the library
	// folder is located from the source PATH, so the name is not needed to
	// find it).
	if p, ok := preparedInfo(e.Path); ok && p.Meta != nil {
		meta := p.Meta
		ec, _ := strconv.Atoi(meta["entry_count"])
		// Full text without an index of ours is the source file's own (D194).
		own := !p.Plan.FullText && dict.OwnText(e.Path)
		info := dictInfo{
			ID: e.ID, Path: e.Path, Name: meta["name"], Format: meta["format"], Entries: ec,
			Caps:     dict.Caps{Exact: true, Prefix: true, FTS: p.Plan.FullText || own, Wildcard: true},
			OwnText:  own,
			DBPath:   p.TextDB,
			Outdated: rebuildable(e.Path) && len(p.Stale(e.Path)) > 0,
		}
		s.langFacts(&info, meta["name"], meta["index_lang"], meta["contents_lang"])
		return info
	}
	// only probe formats with a real cheap prober - otherwise dict.Probe
	// falls back to a full dict.Open outside the entry's memoization (and
	// can trigger DSL auto-ingest), racing the background warm.
	if dict.HasProber(e.Path) {
		if m, err := dict.Probe(e.Path); err == nil {
			info := dictInfo{ // probeable, not prepared → direct backend
				ID: e.ID, Path: e.Path, Name: m.Name, Format: m.Format, Entries: m.EntryCount,
				Caps: dict.Caps{Exact: true, Prefix: true},
			}
			s.langFacts(&info, m.Name, m.IndexLang, m.ContentsLang)
			return info
		}
	}
	// fall back to a full open (non-probeable formats, or probe errors).
	info := dictInfo{ID: e.ID, Path: e.Path}
	if dict.SelfPrepares(e.Path) {
		// The list waits on this open, which may be the dictionary's
		// preparation - the loading line a person watches, and how a freshly
		// imported one is first prepared (D182).
		defer dict.MarkUrgent(e.Path)()
	}
	d, err := e.open()
	if err != nil {
		info.Error = err.Error()
		info.TooLarge = errors.Is(err, dict.ErrTooLarge)
		return info
	}
	m := d.Meta()
	info.Name, info.Format, info.Entries, info.Caps = m.Name, m.Format, m.EntryCount, d.Caps()
	_, prepared := d.(dict.Searcher)
	info.OwnText = info.Caps.FTS && !prepared
	s.langFacts(&info, m.Name, m.IndexLang, m.ContentsLang)
	if textDB, ok := validPrepared(e.Path); ok {
		info.DBPath = textDB
	}
	return info
}

// langFacts derives one row's picker groups and the language its articles are
// read aloud in. It is called from every branch of baseDictInfo with whatever
// that branch already read - no branch opens or reads anything extra for it,
// which is the condition for grouping being in this path at all: /api/dicts
// fans out across the whole library (see docs.local/PERF.md M1), and a per-row
// cost here is paid a hundred times at startup.
func (s *Server) langFacts(info *dictInfo, name, declared, contents string) {
	in := facet.Input{
		Name:     name,
		Path:     langPath(info.Path),
		Roots:    s.reg.Dirs(),
		Declared: declared,
		Contents: contents,
		Rules:    s.groupsNow().val,
		File:     filepath.Base(langPath(info.Path)),
	}
	info.Groups = facet.Derive(in)
	info.ArticleLang = facet.ArticleLang(in)
	info.LemmaLang = s.lemmaLangOf(declared, info.Path, name)
}

// lemmaLangOf is the language whose lemma data a dictionary is searched with
// when a word finds nothing: the three naming sources in authority order -
// declared, then file and folder, then the title, which is the one label the
// user did not choose and so speaks only when the others were silent. The
// picker's groups read the same ladder through the same function. "" is
// English (lemmaWave): the one language assumed without evidence.
func (s *Server) lemmaLangOf(declared, path, name string) string {
	return lang.Resolve(declared, langPath(path), s.reg.Dirs(), name)
}

// rebuildable reports that an entry has a source to prepare from: not a
// standalone text.db, and the file still on disk.
func rebuildable(path string) bool {
	return !store.IsTextDB(path) && fsx.FileExists(path)
}

// validPrepared names the prepared database an entry answers from, checked:
// the entry itself when it IS one, else its library folder when this build
// can open it and it was built from the source as it is now. It reads the
// folder's meta table; a path walked per resource or per rescan wants the
// stat-only backingDB instead.
func validPrepared(path string) (string, bool) {
	if store.IsTextDB(path) {
		return path, true
	}
	return store.PreparedFor(path)
}

// preparedInfo is validPrepared with the meta table kept, for a caller that
// goes on to ask the database something: the one read answers both.
func preparedInfo(path string) (store.Prepared, bool) {
	if store.IsTextDB(path) {
		return store.Inspect(path), true
	}
	return store.FindPrepared(path)
}

// addProvenance fills the panel's "where did this come from" fields cheaply
// (stat only): the foreign source and its media companions, the cached
// text.db/media.db, and whether any packable media exists. entryPath is the
// registry entry's path - a foreign source, or a .text.db for a standalone
// native dictionary (which has no source).
func addProvenance(info *dictInfo, entryPath string) {
	native := store.IsTextDB(entryPath)
	if !native && fsx.FileExists(entryPath) {
		info.Source = entryPath
		info.MediaSrc = dict.CompanionMedia(entryPath)
	}

	// locate the cached text.db: the entry itself when native, else the
	// DBPath from baseDictInfo, else the content-addressed cache name.
	textDB := info.DBPath
	if textDB == "" {
		if p, ok := validPrepared(entryPath); ok {
			textDB = p
		}
	}
	if fsx.FileExists(textDB) {
		info.TextDB = textDB
		if fi, err := os.Stat(textDB); err == nil {
			info.DBSize = fi.Size()
		}
		if info.Caps.FTS && !info.OwnText {
			if fi, err := os.Stat(store.FullTextSibling(textDB)); err == nil {
				info.FTSSize = fi.Size()
			}
		}
		if mediaDB := store.MediaSibling(textDB); fsx.FileExists(mediaDB) {
			info.MediaDB = mediaDB
			if fi, err := os.Stat(mediaDB); err == nil {
				info.MediaSz = fi.Size()
			}
		}
		if strings.EqualFold(filepath.Base(textDB), store.TextDBName) {
			// the folder, not the .db file, is what a user copies or shares
			info.Folder = filepath.Dir(textDB)
		}
	}
	info.DBPath = info.TextDB // keep the legacy field consistent (never a bogus name)

	// packable media: an already-packed media.db, external companions, or a
	// SLOB (which embeds resources - not cheaply enumerable, so assume it may).
	info.HasMedia = info.MediaDB != "" || len(info.MediaSrc) > 0 ||
		(info.Source != "" && strings.HasSuffix(strings.ToLower(info.Source), ".slob"))
}

func (s *Server) handleRescan(w http.ResponseWriter, r *http.Request) {
	s.reg.ForgetFailures()
	if err := s.reg.Rescan(); err != nil {
		httpErr(w, 500, "rescan: %v", err)
		return
	}
	s.reg.Warm()
	// The button is the user asking for everything to be looked at again,
	// including what neither the stat keys nor gen can see (a permission
	// change moves neither size nor mtime).
	s.dictRows.flush()
	// and is answered in full: a 304 here would vouch for the copy a client
	// holds without the rows the flush re-derives ever reaching it
	r.Header.Del("If-None-Match")
	s.handleDicts(w, r)
}

// streamSlot names one dictionary in the result layout (begin message).
type streamSlot struct {
	Dict string `json:"dict"`
	Name string `json:"name"`
}

// streamMsg is one NDJSON line of /api/search:
//
//	{"t":"begin","slots":[{dict,name}…]}   ordered slot layout
//	{"t":"hit","i":N,dict,name,results…}   one dictionary's results
//	{"t":"morph","from":…,"to":…,"lang":…} the hits that follow are for a
//	                                       DIFFERENT word: nothing matched
//	                                       what was typed, so its dictionary
//	                                       form was searched instead (O3)
//	{"t":"end"}                            all dictionaries done
type streamMsg struct {
	T       string        `json:"t"`
	Slots   []streamSlot  `json:"slots,omitempty"`
	I       int           `json:"i"`
	Dict    string        `json:"dict,omitempty"`
	Name    string        `json:"name,omitempty"`
	Results []dict.Result `json:"results,omitempty"`
	Skipped bool          `json:"skipped,omitempty"`
	Error   string        `json:"error,omitempty"`

	// Rung says which reading of a full-text query this dictionary answered:
	// "phrase" (the words adjacent, in order), "near" (within ten tokens, any
	// order) or "words" (anywhere in the article). Absent in every other mode.
	//
	// It is reported because the relaxation is silent otherwise, and a search
	// tool that quietly answers a weaker question than the one asked is lying
	// about its result. The UI says so only when the answer is NOT the phrase -
	// the precise reading needs no announcement (D102).
	Rung string `json:"rung,omitempty"`

	// Partial: a reading stricter than Rung was given up on with candidates
	// unread - an archive's own index has no positions, so a phrase is
	// looked for in the text of its best-ranked candidates only (D194). The
	// UI then says the phrase was not found among those, not that it is
	// absent.
	Partial bool `json:"partial,omitempty"`

	// More: the reading that answered was itself cut short the same way, so
	// there may be results beyond these.
	More bool `json:"more,omitempty"`

	// Deferred: this dictionary was not searched because the fan-out cap
	// declined to materialise it (see fanout). It is NOT an error and must not
	// be reported as one: the dictionary is fine, the query was wide, and
	// asking for this one dictionary by itself answers it. Bytes is what the
	// open was estimated to cost, for logs and tests - the UI does not show a
	// number, because a megabyte figure is not a decision the reader can make.
	Deferred bool  `json:"deferred,omitempty"`
	Bytes    int64 `json:"bytes,omitempty"`

	// Indexing: this deferred dictionary is already being prepared, because
	// something asked for it by name. Only ever set beside Deferred, and the
	// difference matters to the reader: "tap to search" is an offer, and
	// repeating it at a dictionary that is midway through a ten-minute ingest
	// reads as nothing having happened the first time.
	Indexing bool `json:"indexing,omitempty"`

	// The "morph" line: From is what the user typed, To the dictionary form
	// actually searched, Lang the language that produced it. Every "hit" after
	// it belongs to To - which is why it is a line of its own and not a flag on
	// the hits: the client has to be able to SAY so.
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`
	Lang string `json:"lang,omitempty"`
}

// demandSearchBudget is how long a search of ONE named dictionary may take
// before it is cut off. Generous rather than unlimited: the request still holds
// a goroutine and an open backend, so it needs an end, but the end is there to
// catch a wedged backend, not to police a slow one.
const demandSearchBudget = 5 * time.Minute

// queryFlag reads a boolean query parameter, and is where every one of them is
// read, so a spelling means the same thing on every route. sent reports that
// the parameter was given at all, for a caller that keeps a state the request
// did not mention. "", "0", "false", "no" and "off" (any case) are off - so a
// client templating the value can say off without omitting it, and a valueless
// "?hl" is off too; say "hl=1" - and anything else is on.
func queryFlag(q url.Values, name string) (on, sent bool) {
	if !q.Has(name) {
		return false, false
	}
	switch strings.ToLower(strings.TrimSpace(q.Get(name))) {
	case "", "0", "false", "no", "off":
		return false, true
	}
	return true, true
}

// maxFindRunes bounds the `mark` parameter: a find field holds a word or a
// short phrase, and every token of it is a pattern each text node is tested
// against.
const maxFindRunes = 200

// marker returns the per-hit highlighter for one request.
//
// The marks are compiled from the query's OWN lowering, not from the raw
// search box: internal/ftsq produced both the FTS5 expression that matched and
// the phrases handed here, so what is painted is exactly what was matched. That
// is the whole cure for `Физика в конспектах` lighting up every в in the
// article - the phrase rung marks one three-word span, and only a descent to
// the word rung marks в at all.
//
// Compiled once per (term, rung) and cached, because a fan-out asks the same
// question of a hundred dictionaries and they do not all answer on the same
// rung. The cache is unsynchronised on purpose: search.StreamOpen serialises
// its emit callbacks, and the lemma wave runs after the first wave finishes, so
// there is never a second goroutine here. It is bounded by the query plus at
// most one lemma per language.
//
// on=false returns a function that always yields nil, which every caller may
// pass straight to Mark: an API client that did not ask for marks gets the
// article's own bytes, unchanged.
func marker(on bool) func(term, rung string, own []hilite.Phrase) *hilite.Terms {
	if !on {
		return func(string, string, []hilite.Phrase) *hilite.Terms { return nil }
	}
	cache := map[string]*hilite.Terms{}
	return func(term, rung string, own []hilite.Phrase) *hilite.Terms {
		if rung == "" {
			return nil
		}
		if own != nil {
			// One dictionary's own answer (search.Hit.Marks): not shared.
			return hilite.NewPhrases(own)
		}
		key := rung + "\x00" + term
		if t, ok := cache[key]; ok {
			return t
		}
		var t *hilite.Terms
		for _, r := range ftsq.Parse(term).Rungs() {
			if r.Name == rung {
				t = hilite.NewPhrases(r.Marks)
				break
			}
		}
		cache[key] = t
		return t
	}
}

// handleSearch streams results as newline-delimited JSON so the client can
// render each dictionary's accordion the instant it completes, in the
// caller's preference order (SPEC §6, progressive rendering). The `dict`
// param is "all", one id, or a comma-separated ordered id list (enabled
// subset from the panel); dictionaries are queried concurrently and each
// result line carries its slot index `i` so the client fills the correct
// preference-ordered position regardless of completion order.
func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		httpErr(w, 400, "missing q parameter")
		return
	}
	mode, err := search.ParseMode(r.URL.Query().Get("mode"))
	if err != nil {
		httpErr(w, 400, "%v", err)
		return
	}
	// A pattern that cannot be read is one 400 here, before the stream: every
	// dictionary would otherwise report the same error in its own slot.
	if mode == search.Wildcard {
		if _, err := wildcard.Parse(q); err != nil {
			httpErr(w, 400, "%v", err)
			return
		}
	}
	format, err := parseFormat(r.URL.Query().Get("format"))
	if err != nil {
		httpErr(w, 400, "%v", err)
		return
	}
	origin := originOf(r)
	// Full-text match marking, compiled ONCE for the whole fan-out rather than
	// per result: the terms are the query, and the query does not change while
	// twenty dictionaries answer it.
	//
	// Opt-in, and only here. An API client that did not ask for marks gets the
	// article's own bytes, unchanged - which is what makes "on by default in
	// the UI" (the SPA sends hl=1 from its remembered preference) and "off by
	// default for everyone else" the same rule rather than two. And only in
	// full-text mode: in exact, prefix or wildcard the match IS the headword
	// the reader clicked, and marking it inside the article would mark the
	// word they are already looking at, on every line it appears (D102).
	hl, _ := queryFlag(r.URL.Query(), "hl")
	marks := marker(mode == search.FullText && hl)
	// Find in the articles (D198): `mark` names the words to mark in place of
	// the query's own, in every mode. It is the same answer with other marks -
	// what is searched for is still `q` - so the reader's place in a section
	// can be repainted with the words they are looking for inside it.
	if find := strings.TrimSpace(r.URL.Query().Get("mark")); find != "" {
		if utf8.RuneCountInString(find) > maxFindRunes {
			httpErr(w, 400, "mark is longer than %d characters", maxFindRunes)
			return
		}
		t := hilite.CompilePhrase(find)
		marks = func(string, string, []hilite.Phrase) *hilite.Terms { return t }
	}
	// Capped like the store's own maxLimit: the store clamps at its boundary,
	// but a direct backend treats the limit as a stop sign only, and would
	// materialize every match - article bodies included - before stopping.
	n := 20
	if v := r.URL.Query().Get("n"); v != "" {
		if p, err := strconv.Atoi(v); err == nil && p > 0 {
			if p > 500 {
				p = 500
			}
			n = p
		}
	}

	entries, ok := s.searchEntries(w, r.URL.Query().Get("dict"))
	if !ok {
		return
	}

	st, ok := newStream(w, "application/x-ndjson; charset=utf-8")
	if !ok {
		return
	}
	writeLine := func(m streamMsg) { st.line(m) }

	// Emit the slot layout FIRST, from cheap entry ids only - no opens on the
	// request path. The client paints the empty accordion immediately; each
	// dictionary's real name arrives with its "hit" as it completes. Opening
	// (cold MDX ~180ms, cold SLOB ~1s) is deferred into the workers below, so
	// time-to-first-byte is one open at most, never the sum of all of them.
	begin := make([]streamSlot, len(entries))
	for i, e := range entries {
		begin[i] = streamSlot{Dict: e.ID, Name: e.ID}
	}
	demand := len(entries) == 1
	openers := s.searchOpeners(entries)
	writeLine(streamMsg{T: "begin", Slots: begin})

	// renderHit turns one dictionary's answer into its NDJSON line. Extracted
	// because the morphology wave below emits the same shape for the same
	// slots - a second copy of the rewrite/format pipeline would be a second
	// place for it to drift.
	renderHit := func(i int, h search.Hit) streamMsg {
		id := entries[i].ID
		name := h.Meta.Name
		if name == "" {
			// open failed or cancelled: no Meta. Show the filename, not the
			// opaque id hash, so a broken dictionary is identifiable.
			name = filepath.Base(entries[i].Path)
		}
		// resolve dictionary-internal refs (sound://, relative, absolute)
		// to /res/{dict}/… here, where it is unit-tested.
		// Rewrite first, reduce second: RewriteEntryHTML is what turns a
		// dictionary's internal ref into /res/{dict}/…, and `clean` then
		// absolutises exactly those. Reducing first would throw away the
		// references before they had been named.
		hl := marks(h.Term, h.Rung, h.Marks)
		for j := range h.Results {
			h.Results[j].Body = RewriteEntryHTMLMarked(h.Results[j].Body, id, hl)
		}
		// `clean` and `text` need this dictionary's own CSS to know which of
		// its classes are blocks and which are hidden (D68); derived once per
		// dictionary, from the article that first asks. `raw` never asks, so
		// the desktop path costs nothing.
		var st htmlref.Styles
		if format != formatRaw && len(h.Results) > 0 {
			st = s.articleStyles(id, h.Results[0].Body)
		}
		for j := range h.Results {
			h.Results[j].Body = applyFormat(h.Results[j].Body, format, origin, st)
		}
		m := streamMsg{T: "hit", I: i, Dict: id, Name: name, Results: h.Results, Skipped: h.Skipped, Rung: h.Rung, Partial: h.Partial, More: h.More}
		var heavy tooHeavy
		switch {
		case errors.As(h.Err, &heavy):
			// Deferred, not failed. The client renders a section the user can
			// open, and opening it re-queries this dictionary alone - which is
			// uncapped above, and which auto-indexes it so the deferral does
			// not recur.
			m.Deferred, m.Bytes = true, heavy.bytes
			m.Indexing = entries[i].indexing()
		case errors.Is(h.Err, context.DeadlineExceeded):
			// Never Go's own words. "context deadline exceeded" is what an
			// unindexed dictionary looked like from the outside while a preview
			// search crawled through its source format, and it told the user
			// nothing they could act on - which is the whole content of the
			// message they need.
			if entries[i].indexing() {
				m.Error = "timed out — still preparing this dictionary"
			} else {
				m.Error = "timed out — index this dictionary to search it"
			}
		case errors.Is(h.Err, context.Canceled):
			m.Error = "search cancelled"
		case h.Err != nil:
			m.Error = h.Err.Error()
		}
		return m
	}

	// The ceiling exists to stop ONE slow dictionary from holding a wide
	// fan-out open, and 30 s is right for that. It is the wrong shape for a
	// search of a single named dictionary: there is nothing to fan out, the
	// user chose this one, and an unprepared dictionary searched through its
	// own format on a phone can legitimately take longer than that to answer.
	// Cutting it off there produced a timeout instead of a result.
	budget := 30 * time.Second
	if demand {
		budget = demandSearchBudget
	}
	ctx, cancel := context.WithTimeout(r.Context(), budget)
	defer cancel()
	// Metas are kept because each carries the dictionary's declared language,
	// and the morphology wave needs it. Nothing is opened to obtain them: the
	// search callback is invoked for EVERY dictionary, hit or miss.
	metas := make([]dict.Meta, len(entries))
	// missed marks the dictionaries that actually searched and came back
	// EMPTY - the only ones the second wave has anything to ask. One that hit
	// has answered the question; one that was deferred (too heavy), errored,
	// or does not support this mode has said nothing about the word, and
	// asking it the same question again with a lemma gets the same refusal.
	// If nothing missed there is no second wave at all, and no lemma pack is
	// loaded to serve a search that never happened.
	//
	// Per dictionary, not per search (D89): a gate on the WHOLE collection
	// coming back empty would make lemmatization depend on what unrelated
	// dictionaries are installed beside the one being read. One Babylon
	// glossary carrying "estuviera" as a hand-listed alias of "estar" would
	// suppress the lemma probe for every proper Spanish dictionary in the same
	// search - each of which indexes lemmas only, and each of which would
	// answer. The cost of the wider gate is that a language's pack can load on
	// a search that already partly succeeded; it is still bounded by
	// MORPH_CACHE, and the retry still goes only to dictionaries that returned
	// nothing.
	missed := make([]bool, len(entries))
	empty := 0
	search.StreamOpen(ctx, openers, mode, q, n, func(i int, h search.Hit) {
		metas[i] = h.Meta
		// Callbacks are serialised by StreamOpen, so this needs no lock.
		if h.Err == nil && !h.Skipped && len(h.Results) == 0 {
			missed[i], empty = true, empty+1
		}
		writeLine(renderHit(i, h))
	})
	if empty > 0 {
		s.lemmaWave(ctx, writeLine, renderHit, entries, metas, missed, openers, mode, q, n)
	}
	writeLine(streamMsg{T: "end"})
}

// searchEntries resolves a `dict` parameter - "all" or nothing, one id, or a
// comma-separated list in the caller's order - to the dictionaries it names.
// Unknown ids in a list are skipped; when none resolves it has answered 404
// and reports false.
func (s *Server) searchEntries(w http.ResponseWriter, dictParam string) ([]*entry, bool) {
	if dictParam == "" || dictParam == "all" {
		return s.reg.all(), true
	}
	var entries []*entry
	for _, id := range strings.Split(dictParam, ",") {
		if id = strings.TrimSpace(id); id == "" {
			continue
		}
		if e, err := s.reg.get(id); err == nil {
			entries = append(entries, e)
		}
	}
	if len(entries) == 0 {
		httpErr(w, 404, "unknown dictionary id %q", dictParam)
		return nil, false
	}
	return entries, true
}

// searchOpeners makes the fan-out's opener for each dictionary of one query:
// the search itself and the list of matching words (suggest.go) open, cap and
// index a dictionary by the same rules.
func (s *Server) searchOpeners(entries []*entry) []search.Opener {
	openers := make([]search.Opener, len(entries))
	// One budget for this query: what the fan-out may newly materialise before
	// it starts declining dictionaries instead of opening them (see fanout).
	// Nil when uncapped, which is the desktop default.
	//
	// A query naming ONE dictionary has no fan-out to cap, and is never capped.
	// The budget bounds the multiplication - N dictionaries materialised at
	// once by a single `all` query - and a search of one is bounded by that one
	// dictionary. It is also, always, a direct demand: the user picked it in
	// the selector, asked for `more…`, or opened a deferred section. Declining
	// it would leave the dictionary unreachable through EVERY path the app has,
	// which is worse than the memory by a wide margin - and the steady state is
	// still held by PREVIEW_MEMORY, the janitor and the relax valve, none of
	// which this bypasses.
	fan := s.reg.fanout()
	demand := len(entries) == 1
	if demand {
		fan = nil
	}
	for i, e := range entries {
		e := e
		openers[i] = func() (dict.Dictionary, error) {
			if demand && dict.SelfPrepares(e.Path) {
				// A search of this one dictionary waits on its open, which
				// for this format may be its preparation (D182).
				defer dict.MarkUrgent(e.Path)()
			}
			d, err := e.openWithin(fan)
			if err == nil && s.AutoIndex {
				// A search of one dictionary is a demand for that dictionary,
				// so its index jumps the background queue (demandIndex); a
				// dictionary that merely happened to be in a wide search takes
				// its turn (maybeAutoIndex). Both prepare the same thing.
				if demand {
					e.demandIndex()
				} else {
					e.maybeAutoIndex()
				}
			}
			return d, err
		}
	}
	return openers
}

// lemmaWave is the second and last pass: for each dictionary that searched and
// found NOTHING, it looks the word's dictionary form up instead - "knew" in a
// dictionary that only holds "know", "fuiste" in one that only holds "ser".
//
// Running only on dictionaries that came back empty is what keeps this simple
// and safe. Within a dictionary there is nothing to rank against, so no derived
// hit can ever displace an exact one and no ordering has to be threaded through
// the stream - a dictionary sends either a first-wave hit or a lemma hit, never
// both. A MISDETECTED language costs one failed index probe, on a dictionary
// that had already failed, because a candidate is validated by the real
// headword index rather than shown on trust.
//
// Candidates are offered only to dictionaries of the matching language, which
// is free - detection already produced that grouping - and is what stops
// Spanish "sale" -> "salar" from being asked of an English dictionary.
func (s *Server) lemmaWave(
	ctx context.Context,
	writeLine func(streamMsg),
	renderHit func(int, search.Hit) streamMsg,
	entries []*entry,
	metas []dict.Meta,
	missed []bool,
	openers []search.Opener,
	mode search.Mode,
	q string,
	n int,
) {
	// One word only. A phrase has no single lemma, and lemmatizing a token of
	// it would answer a question nobody asked.
	if !s.Morph.Enabled() || strings.ContainsAny(q, " \t\r\n") {
		return
	}
	// A wildcard pattern names no word to lemmatize. A wildcard query without
	// `*` or `?` is a word, and is lemmatized as in every other mode.
	if mode == search.Wildcard && wildcard.HasWildcards(q) {
		return
	}
	byLang := map[string][]int{}
	for i := range entries {
		if !missed[i] {
			continue
		}
		// What differs from the picker is only what is done with "",
		// immediately below.
		code := s.lemmaLangOf(metas[i].IndexLang, entries[i].Path, metas[i].Name)
		if code == "" {
			// The one language assumed without evidence. It is the smallest
			// pack by a wide margin (7 MB against 65 for Russian), an
			// unlabelled dictionary is more often English than anything else,
			// and being wrong costs one probe on a search that already
			// returned nothing. Every OTHER language must be stated.
			code = "en"
		}
		if !s.Morph.Supports(code) {
			continue
		}
		byLang[code] = append(byLang[code], i)
	}
	codes := make([]string, 0, len(byLang))
	for c := range byLang {
		codes = append(codes, c)
	}
	sort.Strings(codes) // map order is random; a response should not be

	for _, code := range codes {
		lemma, ok := s.Morph.Lemma(code, q)
		if !ok {
			continue // already a lemma, unknown word, or no pack
		}
		slots := byLang[code]
		sub := make([]search.Opener, len(slots))
		for k, i := range slots {
			sub[k] = openers[i]
		}
		// Buffered, not streamed: "showing results for X" over an empty list
		// is worse than saying nothing at all, and whether the list is empty is
		// not known until the wave finishes. Bounded by n per dictionary, and
		// asked only of dictionaries that have produced nothing.
		var out []streamMsg
		search.StreamOpen(ctx, sub, mode, lemma, n, func(k int, h search.Hit) {
			if len(h.Results) == 0 {
				return // a second miss is not news; the first was already sent
			}
			out = append(out, renderHit(slots[k], h))
		})
		if len(out) == 0 {
			continue
		}
		writeLine(streamMsg{T: "morph", From: q, To: lemma, Lang: code})
		for _, m := range out {
			writeLine(m)
		}
	}
}

// langPath is the path language detection should read for a registry entry.
// A prepared dictionary is addressed by its text.db, whose name says nothing;
// the folder holding it carries the dictionary's own name, which is what the
// naming conventions are written on.
func langPath(p string) string {
	if strings.EqualFold(filepath.Base(p), store.TextDBName) {
		return filepath.Dir(p)
	}
	return p
}

// webMIME is the authoritative Content-Type for web-critical extensions.
// It overrides whatever the backend reports because Go's
// mime.TypeByExtension returns text/plain for .css/.js on some platforms
// (OS mime DB / Windows registry), and ingested media.db rows carry
// whatever the ingest host happened to report - either makes browsers
// refuse stylesheets/scripts under strict MIME checking.
//
// Values follow MDN's Common MIME types and IANA registrations: the modern
// registered font types (font/woff, font/ttf, … - RFC 8081), text/javascript
// (RFC 9239, not the obsolete application/javascript), and
// image/vnd.microsoft.icon - not the legacy application/x-font-* variants.
// .spx is the one entry that describes the CONTAINER rather than what a
// browser usually gets: handleResource transcodes Speex to WAV and sets
// audio/wav itself, so this table is consulted for a .spx only on the paths
// that ship the original bytes - a failed transcode, no decoder, or a
// user-supplied override file - where the payload really is Ogg.
var webMIME = map[string]string{
	// images
	".bmp": "image/bmp", ".gif": "image/gif", ".ico": "image/vnd.microsoft.icon",
	".jpg":   "image/jpeg",
	".jpeg":  "image/jpeg",
	".jfif":  "image/jpeg",
	".pjpeg": "image/jpeg",
	".jpe":   "image/jpeg",
	".png":   "image/png",
	".svg":   "image/svg+xml", ".tif": "image/tiff", ".tiff": "image/tiff",
	".webp": "image/webp",
	".avif": "image/avif",
	// text / markup / scripts
	".css": "text/css", ".ini": "text/plain",
	".js": "text/javascript", ".mjs": "text/javascript",
	".json": "application/json", ".html": "text/html", ".htm": "text/html",
	".xhtml": "application/xhtml+xml", ".wasm": "application/wasm",
	// fonts (RFC 8081 registered types)
	".woff": "font/woff", ".woff2": "font/woff2", ".ttf": "font/ttf",
	".otf": "font/otf", ".eot": "application/vnd.ms-fontobject",
	// documents
	".pdf": "application/pdf",
	// audio. .spx is Ogg-Speex here (see above). .webm can be audio or
	// video - dictionaries ship audio, so default to that.
	".mp3": "audio/mpeg", ".ogg": "audio/ogg", ".opus": "audio/ogg",
	".oga": "audio/ogg", ".spx": "audio/ogg", ".wav": "audio/wav",

	".m4a": "audio/mp4", ".m4b": "audio/mp4", ".aac": "audio/aac",
	".webm": "audio/webm", ".weba": "audio/webm",
	// video
	".mp4":  "video/mp4",
	".mpg":  "video/mpeg",
	".mpeg": "video/mpeg",
	".mov":  "video/quicktime",
	".3gp":  "video/3gpp",
	".ogv":  "video/ogg",
	".m4v":  "video/x-m4v",
	// Formats no browser decodes, sent so the reader's own player can. Lingvo
	// DSL names .avi as ITS video format and .asf as a sound one, GoldenDict
	// adds .wmv and .flv (docs/DSL.md); the Microsoft image formats come from
	// the same table. Registered types where one exists, the conventional
	// x- name where it does not.
	".avi": "video/x-msvideo", ".wmv": "video/x-ms-wmv",
	".asf": "video/x-ms-asf", ".flv": "video/x-flv",
	".mkv": "video/x-matroska",
	".pcx": "image/x-pcx", ".dcx": "image/x-dcx",
	".wmf": "image/wmf", ".emf": "image/emf",
	".flac": "audio/flac",
	".txt":  "text/plain",
	".xml":  "application/xml",
	".csv":  "text/csv",
	".vtt":  "text/vtt",
	".apng": "image/apng",
}

// resolveMIME prefers the web-critical override, else the backend's value.
func resolveMIME(name, backend string) string {
	if i := strings.LastIndexByte(name, '.'); i >= 0 {
		if m, ok := webMIME[strings.ToLower(name[i:])]; ok {
			return m
		}
	}
	return backend
}

// handleResource serves /res/{dictID}/{name...}.
func (s *Server) handleResource(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/res/")
	id, name, ok := strings.Cut(rest, "/")
	if !ok || name == "" {
		httpErr(w, 400, "usage: /res/{dict}/{name}")
		return
	}
	e, err := s.reg.get(id)
	if err != nil {
		httpErr(w, 404, "%v", err)
		return
	}
	if s.serveOverride(w, r, e, name) {
		return
	}
	d, err := e.open()
	if err != nil {
		httpErr(w, 500, "%v", err)
		return
	}
	rc, mime, err := d.Resource(name)
	// serving a resource is the one path that can open a handle the entry's own
	// open() never accounts for: a prepared dictionary lazily opening its source
	// because media.db missed. Tell the janitor, or that handle would stay until
	// something else happened to wake it.
	s.reg.nudge()
	if err != nil {
		if err == dict.ErrNotFound {
			http.NotFound(w, r)
			return
		}
		httpErr(w, 500, "%v", err)
		return
	}
	defer func() { rc.Close() }() // closure: rc may be swapped below
	isSpx := strings.HasSuffix(strings.ToLower(name), ".spx")
	// browsers cannot play Speex: transcode .spx to WAV (in-process libspeex by
	// default, else the external speexdec). The bytes we send are WAV, so the
	// Content-Type is audio/wav (never the audio/ogg of the raw container).
	if isSpx && s.canTranscodeSpx() {
		if wav, err := s.spxToWav(id, name, rc); err == nil {
			w.Header().Set("Content-Type", "audio/wav")
			w.Header().Set("Cache-Control", "public, max-age=86400")
			// ServeContent for the Range answer every other media path gets
			// (see below); WebKit probes audio with bytes=0-1 too.
			http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(wav))
			return
		} else {
			logx.V("spx transcode %s/%s failed: %v (serving raw)", id, name, err)
			// rc is consumed; re-open for the raw fallback
			rc2, _, err2 := d.Resource(name)
			if err2 != nil {
				httpErr(w, 500, "%v", err2)
				return
			}
			rc.Close()
			rc = rc2
		}
	}
	if m := resolveMIME(name, mime); m != "" {
		w.Header().Set("Content-Type", m)
	}
	// A .spx that reached here could NOT be transcoded (no speexdec / failure)
	// - it is unplayable raw Speex, so don't let a day-long cache entry mask
	// the fix once speexdec is installed. Everything else caches normally.
	if isSpx {
		w.Header().Set("Cache-Control", "no-store")
	} else {
		w.Header().Set("Cache-Control", "public, max-age=86400")
	}
	// A NUL byte cannot occur in JavaScript, CSS, HTML or JSON, so finding one
	// is proof the stored blob is damaged - not a rendering problem, not a
	// wudict problem, and not something the user can see any other way. One
	// real case in a 105-dictionary corpus: a Cambridge slob whose bundled
	// jquery.js has 12,186 NULs in nine block-aligned holes, which makes it
	// unparseable, so the dictionary's own tab script never runs and its
	// entries silently show one tab of three. Say so once, and name the
	// override that fixes it.
	// A seekable resource is served through ServeContent, which answers Range
	// requests. That is not an optimisation: a browser will not START a video
	// it cannot seek - Safari in particular abandons playback outright when the
	// first ranged probe comes back 200 - and a reader dragging the scrubber of
	// a 13 MB clip would otherwise re-download it from byte zero.
	// ServeContent keeps the Content-Type set above and adds Accept-Ranges,
	// Content-Length and 206 handling. Zero modtime = no Last-Modified and no
	// If-Modified-Since arithmetic; the ETag-less, immutable-ish
	// Cache-Control above is what governs caching, exactly as on the copy path.
	//
	// Text resources keep the copy path: they are small, some containers hand
	// them over non-seekable anyway, and the NUL check below only applies to
	// them.
	if rs, ok := rc.(io.ReadSeeker); ok && !isTextResource(name) {
		http.ServeContent(w, r, name, time.Time{}, rs)
		return
	}
	watch := &nulWatcher{r: rc, text: isTextResource(name)}
	_, _ = io.Copy(w, watch)
	if watch.seen {
		if _, dup := s.nulSeen.LoadOrStore(id+"\x00"+name, true); !dup {
			logx.Warn("%s%s is damaged: it contains NUL bytes and cannot parse. "+
				"The dictionary is stored that way - wudict is serving it verbatim. "+
				"Drop a good copy at <library folder>/res/%s to override it.",
				logx.Dict(e.Path), name, name)
		}
	}
}

// isTextResource reports whether a NUL byte in this resource would be proof of
// damage. Deliberately a small allow-list of source-text formats: a NUL is
// perfectly ordinary in a font, an image or an audio blob.
func isTextResource(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".js", ".mjs", ".css", ".html", ".htm", ".xhtml", ".json", ".xml", ".svg", ".txt":
		return true
	}
	return false
}

// nulWatcher passes bytes through and remembers whether any was NUL. It reads
// the stream that is already being copied, so the check costs one IndexByte
// per buffer and never buffers the resource itself.
type nulWatcher struct {
	r    io.Reader
	text bool
	seen bool
}

func (n *nulWatcher) Read(p []byte) (int, error) {
	c, err := n.r.Read(p)
	if n.text && !n.seen && c > 0 && bytes.IndexByte(p[:c], 0) >= 0 {
		n.seen = true
	}
	return c, err
}

// serveOverride serves <library folder>/res/<name> when the user has put a
// file there, and reports whether it did.
//
// The need is real and not hypothetical: a dictionary can ship a damaged
// bundled resource (see the NUL check above), and there is otherwise no way to
// fix it short of rewriting a multi-gigabyte container. This is deliberately
// NOT special-cased to any file or format - any resource of any dictionary can
// be shadowed, which is why it needs no knowledge of jQuery, Cambridge, or
// what a working replacement would look like.
//
// The library folder is the right home: it is wudict's own space (never the
// user's read-only dictionary folder), the panel already displays its path
// with a "reveal" button, and it is the unit D20 made transferable - so an
// override travels with the dictionary it repairs.
func (s *Server) serveOverride(w http.ResponseWriter, r *http.Request, e *entry, name string) bool {
	textDB, ok := e.preparedDB()
	if !ok {
		return false // no library folder yet: nothing can have been put in one
	}
	dir := filepath.Join(filepath.Dir(textDB), "res")
	// path.Clean on a rooted copy resolves "..", so /res/{id}/../../../etc/passwd
	// folds to a name inside the directory rather than above it; `within` is a
	// second, lexical check for anything that still lands outside. Both are
	// verified by removing them: four traversal vectors leak without them.
	// Neither resolves symlinks, and neither needs to - the only way a link
	// gets into this directory is the user putting it there, in wudict's own
	// storage, reachable only from loopback.
	rel := strings.TrimPrefix(path.Clean("/"+name), "/")
	if rel == "" {
		return false
	}
	full := filepath.Join(dir, filepath.FromSlash(rel))
	if !within(dir, full) {
		return false
	}
	f, err := os.Open(full)
	if err != nil {
		return false // the ordinary case: no override for this resource
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || fi.IsDir() {
		return false
	}
	if m := resolveMIME(name, ""); m != "" {
		w.Header().Set("Content-Type", m)
	}
	// no-cache, not max-age: an override is something the user is actively
	// editing, and a day-long cache would hide their next attempt. ServeContent
	// supplies Last-Modified, so revalidation stays cheap.
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeContent(w, r, name, fi.ModTime(), f)
	return true
}

// canTranscodeSpx reports whether any Speex→WAV backend is available.
func (s *Server) canTranscodeSpx() bool {
	if s.UseExternalSpeex {
		return s.Speexdec != ""
	}
	return speex.Available || s.Speexdec != ""
}

// spxToWav transcodes one Speex resource to WAV, caching the result under
// <dbdir>/spxcache/. The decode itself is done by decodeSpx (in-process or
// external); this wrapper handles the on-disk cache + per-key single-flight.
func (s *Server) spxToWav(dictID, name string, rc io.Reader) ([]byte, error) {
	sum := sha256.Sum256([]byte(dictID + "\x00" + name))
	cacheDir := filepath.Join(store.DefaultDBDir(), "spxcache")
	wavPath := filepath.Join(cacheDir, hex.EncodeToString(sum[:])[:24]+".wav")
	if data, err := os.ReadFile(wavPath); err == nil && len(data) > 0 {
		return data, nil
	}
	// single-flight per cache key: the loser waits on the mutex, then finds the
	// winner's WAV in the re-check below - no duplicate decode, no two writers
	// racing wavPath. The guard is on the shared on-disk cache, so it is
	// backend-agnostic.
	muRaw, _ := s.spxLocks.LoadOrStore(wavPath, &sync.Mutex{})
	mu := muRaw.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()
	if data, err := os.ReadFile(wavPath); err == nil && len(data) > 0 {
		return data, nil
	}

	wav, err := s.decodeSpx(rc)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(cacheDir, 0o755); err == nil {
		if err := os.WriteFile(wavPath, wav, 0o644); err != nil {
			logx.V("spx cache write %s: %v", wavPath, err)
		}
	}
	logx.V("spx decoded %s/%s -> %d bytes", dictID, name, len(wav))
	return wav, nil
}

// decodeSpx converts one Ogg-Speex stream to WAV. The in-process libspeex
// decoder is used by default and trusted - there is no per-file fallback to
// speexdec (a file the built-in decoder can't handle, speexdec almost certainly
// can't either). The external speexdec is used only when explicitly forced
// (SPEEX_BACKEND=external) or when the in-process decoder is not compiled in
// (CGO_ENABLED=0).
func (s *Server) decodeSpx(rc io.Reader) ([]byte, error) {
	raw, err := io.ReadAll(rc)
	if err != nil {
		return nil, err
	}
	if !s.UseExternalSpeex && speex.Available {
		return speex.DecodeToWAV(bytes.NewReader(raw))
	}
	return s.externalSpxToWav(raw)
}

// externalSpxToWav runs the external speexdec binary over the given .spx bytes.
func (s *Server) externalSpxToWav(raw []byte) ([]byte, error) {
	if s.Speexdec == "" {
		return nil, fmt.Errorf("no speex decoder available")
	}
	tmp, err := os.MkdirTemp("", "wudict-spx")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	spxPath := filepath.Join(tmp, "in.spx")
	wavPath := filepath.Join(tmp, "out.wav")
	if err := os.WriteFile(spxPath, raw, 0o644); err != nil {
		return nil, err
	}
	dec := exec.Command(s.Speexdec, spxPath, wavPath)
	hideWindow(dec)
	if outb, err := dec.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("%s: %v (%s)", s.Speexdec, err, strings.TrimSpace(string(outb)))
	}
	data, err := os.ReadFile(wavPath)
	if err != nil || len(data) == 0 {
		return nil, fmt.Errorf("speexdec produced no output")
	}
	return data, nil
}

// handleIngest brings one dictionary to the feature state the panel asked for
// (D24; the flow D5 called "Enable fuzzy & full-text search") for one
// dictionary, streaming progress as SSE events:
//
//	event: progress  data: {"done":N,"total":M}
//	event: done      data: {dictInfo}
//	event: error     data: {"error":"..."}
func (s *Server) handleIngest(w http.ResponseWriter, r *http.Request) {
	e, err := s.reg.get(r.URL.Query().Get("dict"))
	if err != nil {
		httpErr(w, 404, "%v", err)
		return
	}
	// The panel sends the state it wants, not a verb: fts and media each on
	// or off. A parameter left out keeps whatever that feature is now, so a
	// caller toggling one thing cannot accidentally strip another - and a
	// request naming none prepares an unprepared dictionary at the cheapest
	// level, which is the panel's index chip.
	q := r.URL.Query()
	want := s.currentFeatures(e)
	for _, f := range []struct {
		name string
		to   *bool
	}{{"fts", &want.FullText}, {"media", &want.Media}} {
		if on, sent := queryFlag(q, f.name); sent {
			*f.to = on
		}
	}

	st, ok := newStream(w, "text/event-stream")
	if !ok {
		return
	}
	// The change runs as the dictionary's job (jobs.go), not inside this
	// request: a page that closes or reloads mid-way loses nothing, and the
	// next one finds the job in /api/dicts and follows it here again. While
	// one runs, a request for the same dictionary follows that one - what it
	// asked for is not queued behind it, and the "done" it gets carries what
	// the dictionary now has.
	key := ingestKey(e.ID)
	s.jobs.start(key, 0, jobStatus{}, func(j *job) {
		last := time.Time{}
		progress := func(done, total int) {
			if time.Since(last) < 200*time.Millisecond {
				return
			}
			last = time.Now()
			j.update(func(js *jobStatus) { js.Done, js.Total = int64(done), int64(total) })
		}
		if err := e.setFeatures(want, progress); err != nil {
			j.update(func(js *jobStatus) { js.Err = err.Error() })
			return
		}
		d, err := e.open()
		if err != nil {
			j.update(func(js *jobStatus) { js.Err = err.Error() })
			return
		}
		m := d.Meta()
		dbPath, _ := validPrepared(e.Path)
		info := dictInfo{
			ID: e.ID, Name: m.Name, Format: m.Format, Path: e.Path,
			Entries: m.EntryCount, Caps: d.Caps(),
			DBPath: dbPath,
		}
		j.update(func(js *jobStatus) { js.Result = info })
	})
	s.followIngest(r.Context(), st, key)
}

// jobProgress is a running job as a list row carries it.
type jobProgress struct {
	Done  int64 `json:"done"`
	Total int64 `json:"total"`
}

// ingestKey is one dictionary's index change, as a job.
func ingestKey(id string) string { return "ingest:" + id }

// followIngest streams the job under key until it ends or the client leaves:
// "progress" on each change, then "done" with the dictionary as it now is, or
// "error" with why.
func (s *Server) followIngest(ctx context.Context, st *respStream, key string) {
	for {
		js, next, ok := s.jobs.watch(key)
		switch {
		case !ok:
			st.event("error", map[string]string{"error": "no such job"})
			return
		case !js.Running && js.Err != "":
			st.event("error", map[string]string{"error": js.Err})
			return
		case !js.Running:
			st.event("done", js.Result)
			return
		case js.Done > 0 || js.Total > 0:
			st.event("progress", map[string]int64{"done": js.Done, "total": js.Total})
		}
		select {
		case <-next:
		case <-ctx.Done():
			return // the client left; the job goes on
		}
	}
}

// assetTag is a short content hash, used as the ?v= of an embedded script so
// its URL changes exactly when its bytes do. Content addressing rather than
// the build version: a developer rebuild keeps Version at "dev", which would
// leave a changed script behind a week-long cache under an unchanged URL.
func assetTag(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])[:8]
}
