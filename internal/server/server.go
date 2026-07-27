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
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/glowinthedark/gonow-dict/internal/config"
	"github.com/glowinthedark/gonow-dict/internal/dict"
	"github.com/glowinthedark/gonow-dict/internal/logx"
	"github.com/glowinthedark/gonow-dict/internal/search"
	"github.com/glowinthedark/gonow-dict/internal/speex"
	"github.com/glowinthedark/gonow-dict/internal/store"
)

//go:embed web/index.html
var indexHTML []byte

//go:embed web/mark.min.js
var markJS []byte // vendored mark.js v8.11.1 (draego pulled it from a CDN)

//go:embed web/setup.html
var setupHTML string

//go:embed web/frame.js
var frameJS []byte // bridge script for sandboxed article iframes

//go:embed web/favicon.svg
var faviconSVG []byte // "Lookup" mark: magnifier over headword lines

// Server exposes the registry over HTTP.
type Server struct {
	reg *Registry
	mux *http.ServeMux

	// ConfigPath, when set, is where the first-run setup flow persists
	// DICT_DIR. In-memory state is updated regardless, so setup works
	// even when the folder came from a CLI flag or the file is read-only.
	ConfigPath string

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

	// AutoIndex, when true (config auto_index != "off"), builds a fuzzy
	// headword index for a dictionary the first time it is searched —
	// silently, in the background — so fuzzy search becomes available on
	// the next query without the user ever asking. Full-text stays opt-in.
	AutoIndex bool
}

func New(reg *Registry) *Server {
	s := &Server{reg: reg, mux: http.NewServeMux()}
	s.mux.HandleFunc("GET /", s.handleIndex)
	s.mux.HandleFunc("GET /api/dicts", s.handleDicts)
	s.mux.HandleFunc("GET /api/search", s.handleSearch)
	s.mux.HandleFunc("GET /api/rescan", s.handleRescan)
	s.mux.HandleFunc("GET /api/ingest", s.handleIngest)
	s.mux.HandleFunc("GET /api/setup", s.handleSetup)
	s.mux.HandleFunc("GET /api/library", s.handleLibrary)
	s.mux.HandleFunc("GET /res/", s.handleResource)
	s.mux.HandleFunc("GET /assets/mark.min.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=604800")
		_, _ = w.Write(markJS)
	})
	s.mux.HandleFunc("GET /assets/frame.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=604800")
		_, _ = w.Write(frameJS)
	})
	// SVG favicon, plus a /favicon.ico route so browsers that fetch the
	// well-known path by default get the same mark instead of a 404.
	serveFavicon := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/svg+xml")
		w.Header().Set("Cache-Control", "public, max-age=604800")
		_, _ = w.Write(faviconSVG)
	}
	s.mux.HandleFunc("GET /assets/favicon.svg", serveFavicon)
	s.mux.HandleFunc("GET /favicon.ico", serveFavicon)
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	defer func() {
		if rec := recover(); rec != nil {
			logx.V("PANIC %s %s: %v", r.Method, r.URL.RequestURI(), rec)
			fmt.Fprintf(os.Stderr, "panic serving %s: %v\n", r.URL.RequestURI(), rec)
			httpErr(w, 500, "internal error: %v", rec)
		}
	}()
	s.mux.ServeHTTP(w, r)
	logx.V("%s %s (%s)", r.Method, r.URL.RequestURI(), time.Since(start).Round(time.Microsecond))
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
	// first-run: no dictionaries yet → serve the setup page instead
	if s.reg.Count() == 0 {
		dir := s.reg.Dir()
		reason := "contains no dictionaries yet"
		if _, err := os.Stat(dir); err != nil {
			reason = "does not exist"
		}
		page := strings.ReplaceAll(setupHTML, "{{DIR}}", htmlEscape(dir))
		page = strings.ReplaceAll(page, "{{REASON}}", reason)
		_, _ = io.WriteString(w, page)
		return
	}
	_, _ = w.Write(indexHTML)
}

func htmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
	return r.Replace(s)
}

// handleLibrary lists the previously imported dictionaries kept in the db dir
// — the ones the setup page offers to use, and the basis of the USE_CACHED
// choice. Reading it never enrolls them: listing is not consent.
func (s *Server) handleLibrary(w http.ResponseWriter, r *http.Request) {
	entries, err := store.Library()
	if err != nil {
		httpErr(w, 500, "reading library: %v", err)
		return
	}
	if entries == nil {
		entries = []store.LibEntry{}
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
// dictionaries (persisting USE_CACHED) — on its own, or together with a
// folder. Clicking a Use button IS the "don't ask again": the setup page only
// appears while the registry is empty.
func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	raw := strings.TrimSpace(q.Get("path"))
	save := q.Get("save") != ""
	useCached := q.Get("useCached") == "1"

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
			out["found"] = s.reg.Count()
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
	if r.URL.Query().Get("save") != "" {
		if len(paths) == 0 {
			out["error"] = "no dictionaries found in this folder"
		} else if err := s.reg.SetDir(dir); err != nil {
			out["error"] = err.Error()
		} else {
			out["saved"] = true
			if s.ConfigPath != "" {
				if err := config.SaveKey(s.ConfigPath, "DICT_DIR", dir); err != nil {
					out["warning"] = "folder switched, but saving config failed: " + err.Error()
				}
			}
			if useCached {
				out["useCached"] = true
				if err := s.setUseCached(true); err != nil {
					out["warning"] = "folder saved, but keeping the prepared dictionaries failed: " + err.Error()
				}
			}
		}
	}
	writeJSON(w, out)
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

	// provenance (panel display): where the dictionary came from and what
	// derived files exist. All optional and filesystem-cheap.
	Source   string   `json:"source,omitempty"`   // foreign source file, if still on disk
	MediaSrc []string `json:"mediaSrc,omitempty"` // companion media sources (.mdd, .files.zip, res/)
	TextDB   string   `json:"textDB,omitempty"`   // prepared text.db, if present
	MediaDB  string   `json:"mediaDB,omitempty"`  // packed media.db, if present
	Folder   string   `json:"folder,omitempty"`   // library folder holding them (the transferable unit)
	HasMedia bool     `json:"hasMedia,omitempty"` // packable binary resources exist (drives "pack media")
}

func (s *Server) handleDicts(w http.ResponseWriter, r *http.Request) {
	entries := s.reg.all()
	out := make([]dictInfo, len(entries))
	// resolve metadata in parallel. The cheap path (header-only probe +
	// text.db meta read) avoids building the heavy in-memory index, so the
	// list loads fast even for dozens of dictionaries; only non-probeable
	// formats fall back to a full open.
	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup
	for i, e := range entries {
		wg.Add(1)
		go func(i int, e *entry) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			out[i] = s.dictInfoFor(e)
		}(i, e)
	}
	wg.Wait()
	writeJSON(w, out)
}

// dictInfoFor resolves one dictionary's list row cheaply when possible:
// a header-only Probe locates a content-matched text.db and reads its
// meta (name/entry_count/ingest_level) without opening the direct
// backend; a probeable format with no cache reports direct caps
// (exact+prefix); everything else falls back to a full open for real caps.
func (s *Server) dictInfoFor(e *entry) dictInfo {
	info := s.baseDictInfo(e)
	addProvenance(&info, e.Path)
	if e.noPackableMedia() {
		info.HasMedia = false // a prior pack found nothing — stop offering it
	}
	return info
}

func (s *Server) baseDictInfo(e *entry) dictInfo {
	// a prepared dictionary answers the whole row from its own meta — no
	// probe, no direct open, and it works for every format (the library
	// folder is located from the source PATH, so the name is not needed to
	// find it).
	if textDB, ok := preparedTextDB(e.Path); ok {
		if meta, err := store.ReadMeta(textDB); err == nil {
			ec, _ := strconv.Atoi(meta["entry_count"])
			return dictInfo{
				ID: e.ID, Path: e.Path, Name: meta["name"], Format: meta["format"], Entries: ec,
				Caps:   dict.Caps{Exact: true, Prefix: true, Contains: meta["has_trigram"] == "1", FTS: meta["ingest_level"] != string(store.LevelHeadwords)},
				DBPath: textDB,
			}
		}
	}
	// only probe formats with a real cheap prober — otherwise dict.Probe
	// falls back to a full dict.Open outside the entry's memoization (and
	// can trigger DSL auto-ingest), racing the background warm.
	if dict.HasProber(e.Path) {
		if m, err := dict.Probe(e.Path); err == nil {
			return dictInfo{ // probeable, not prepared → direct backend
				ID: e.ID, Path: e.Path, Name: m.Name, Format: m.Format, Entries: m.EntryCount,
				Caps: dict.Caps{Exact: true, Prefix: true},
			}
		}
	}
	// fall back to a full open (non-probeable formats, or probe errors).
	info := dictInfo{ID: e.ID, Path: e.Path}
	d, err := e.open()
	if err != nil {
		info.Error = err.Error()
		return info
	}
	m := d.Meta()
	info.Name, info.Format, info.Entries, info.Caps = m.Name, m.Format, m.EntryCount, d.Caps()
	if textDB, ok := preparedTextDB(e.Path); ok {
		info.DBPath = textDB
	}
	return info
}

// dbPathOf is the prepared database path for an entry, or "" when it has none.
func dbPathOf(e *entry) string {
	if p, ok := preparedTextDB(e.Path); ok {
		return p
	}
	return ""
}

// preparedTextDB locates the prepared database for a registry entry: the entry
// itself when it IS one, else this source's library folder (when prepared and
// still matching the source).
func preparedTextDB(entryPath string) (string, bool) {
	if store.IsTextDB(entryPath) {
		return entryPath, true
	}
	return store.PreparedFor(entryPath)
}

// addProvenance fills the panel's "where did this come from" fields cheaply
// (stat only): the foreign source and its media companions, the cached
// text.db/media.db, and whether any packable media exists. entryPath is the
// registry entry's path — a foreign source, or a .text.db for a standalone
// native dictionary (which has no source).
func addProvenance(info *dictInfo, entryPath string) {
	native := store.IsTextDB(entryPath)
	if !native && fileExists(entryPath) {
		info.Source = entryPath
		info.MediaSrc = companionMedia(entryPath)
	}

	// locate the cached text.db: the entry itself when native, else the
	// DBPath from baseDictInfo, else the content-addressed cache name.
	textDB := info.DBPath
	if textDB == "" {
		if p, ok := preparedTextDB(entryPath); ok {
			textDB = p
		}
	}
	if fileExists(textDB) {
		info.TextDB = textDB
		if mediaDB := store.MediaSibling(textDB); fileExists(mediaDB) {
			info.MediaDB = mediaDB
		}
		if strings.EqualFold(filepath.Base(textDB), store.TextDBName) {
			// the folder, not the .db file, is what a user copies or shares
			info.Folder = filepath.Dir(textDB)
		}
	}
	info.DBPath = info.TextDB // keep the legacy field consistent (never a bogus name)

	// packable media: an already-packed media.db, external companions, or a
	// SLOB (which embeds resources — not cheaply enumerable, so assume it may).
	info.HasMedia = info.MediaDB != "" || len(info.MediaSrc) > 0 ||
		(info.Source != "" && strings.HasSuffix(strings.ToLower(info.Source), ".slob"))
}

// companionMedia lists the media SOURCE files that sit beside a foreign source
// and would be packed into a media.db: MDX .mdd siblings, a DSL .files.zip, a
// StarDict res/ dir or res.zip. Cheap (stat only). Empty for SLOB (media is
// embedded in the .slob) and for formats with none.
func companionMedia(src string) []string {
	ext := strings.ToLower(filepath.Ext(src))
	noExt := strings.TrimSuffix(src, filepath.Ext(src))
	dir := filepath.Dir(src)
	var out []string
	switch ext {
	case ".mdx":
		for _, f := range []string{noExt + ".mdd", noExt + ".1.mdd"} {
			if fileExists(f) {
				out = append(out, f)
			}
		}
		for n := 2; ; n++ {
			f := fmt.Sprintf("%s.%d.mdd", noExt, n)
			if !fileExists(f) {
				break
			}
			out = append(out, f)
		}
	case ".dsl", ".dz":
		for _, f := range []string{src + ".files.zip", noExt + ".files.zip"} {
			if fileExists(f) {
				out = append(out, f)
				break
			}
		}
	case ".ifo":
		if d := filepath.Join(dir, "res"); dirExists(d) {
			out = append(out, d)
		}
		if z := filepath.Join(dir, "res.zip"); fileExists(z) {
			out = append(out, z)
		}
	}
	return out
}

func fileExists(p string) bool {
	if p == "" {
		return false
	}
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

func dirExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

func (s *Server) handleRescan(w http.ResponseWriter, r *http.Request) {
	if err := s.reg.Rescan(); err != nil {
		httpErr(w, 500, "rescan: %v", err)
		return
	}
	s.reg.Warm()
	s.handleDicts(w, r)
}

func parseMode(s string) (search.Mode, error) {
	switch s {
	case "", "prefix":
		return search.Prefix, nil
	case "exact":
		return search.Exact, nil
	case "contains":
		return search.Contains, nil
	case "fuzzy": // legacy alias: the old "fuzzy" behaviour is now part of prefix
		return search.Prefix, nil
	case "fts":
		return search.FullText, nil
	}
	return 0, fmt.Errorf("unknown mode %q", s)
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
	mode, err := parseMode(r.URL.Query().Get("mode"))
	if err != nil {
		httpErr(w, 400, "%v", err)
		return
	}
	n := 20
	if v := r.URL.Query().Get("n"); v != "" {
		if p, err := strconv.Atoi(v); err == nil && p > 0 {
			n = p
		}
	}

	dictParam := r.URL.Query().Get("dict")
	var entries []*entry
	if dictParam == "" || dictParam == "all" {
		entries = s.reg.all()
	} else {
		for _, id := range strings.Split(dictParam, ",") {
			if id = strings.TrimSpace(id); id == "" {
				continue
			}
			if e, err := s.reg.get(id); err == nil {
				entries = append(entries, e)
			}
		}
		if len(entries) == 0 { // none of the requested ids resolved
			httpErr(w, 404, "unknown dictionary id %q", dictParam)
			return
		}
	}

	fl, ok := w.(http.Flusher)
	if !ok {
		httpErr(w, 500, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no") // disable proxy buffering (Caddy/nginx)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	writeLine := func(m streamMsg) {
		_ = enc.Encode(m) // Encode appends '\n' → one NDJSON record
		fl.Flush()
	}

	// Emit the slot layout FIRST, from cheap entry ids only — no opens on the
	// request path. The client paints the empty accordion immediately; each
	// dictionary's real name arrives with its "hit" as it completes. Opening
	// (cold MDX ~180ms, cold SLOB ~1s) is deferred into the workers below, so
	// time-to-first-byte is one open at most, never the sum of all of them.
	begin := make([]streamSlot, len(entries))
	openers := make([]search.Opener, len(entries))
	for i, e := range entries {
		e := e
		begin[i] = streamSlot{Dict: e.ID, Name: e.ID}
		openers[i] = func() (dict.Dictionary, error) {
			d, err := e.open()
			if err == nil && s.AutoIndex {
				e.maybeAutoIndex() // first search of a direct dict → build fuzzy in background
			}
			return d, err
		}
	}
	writeLine(streamMsg{T: "begin", Slots: begin})

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	search.StreamOpen(ctx, openers, mode, q, n, func(i int, h search.Hit) {
		id := entries[i].ID
		name := h.Meta.Name
		if name == "" {
			// open failed or cancelled: no Meta. Show the filename, not the
			// opaque id hash, so a broken dictionary is identifiable.
			name = filepath.Base(entries[i].Path)
		}
		// resolve dictionary-internal refs (sound://, relative, absolute)
		// to /res/{dict}/… here, where it is unit-tested.
		for j := range h.Results {
			h.Results[j].Body = RewriteEntryHTML(h.Results[j].Body, id)
		}
		m := streamMsg{T: "hit", I: i, Dict: id, Name: name, Results: h.Results, Skipped: h.Skipped}
		if h.Err != nil {
			m.Error = h.Err.Error()
		}
		writeLine(m)
	})
	writeLine(streamMsg{T: "end"})
}

// webMIME is the authoritative Content-Type for web-critical extensions.
// It overrides whatever the backend reports because Go's
// mime.TypeByExtension returns text/plain for .css/.js on some platforms
// (OS mime DB / Windows registry), and ingested media.db rows carry
// whatever the ingest host happened to report — either makes browsers
// refuse stylesheets/scripts under strict MIME checking.
//
// Values follow MDN's Common MIME types and IANA registrations: the modern
// registered font types (font/woff, font/ttf, … — RFC 8081), text/javascript
// (RFC 9239, not the obsolete application/javascript), and
// image/vnd.microsoft.icon — not the legacy application/x-font-* variants.
// Exception: .spx is mapped to audio/wav because gonow-dict transcodes Speex
// to WAV on the way out (see handleResource), so that is what the client
// actually receives.
var webMIME = map[string]string{
	// images
	".bmp": "image/bmp", ".gif": "image/gif", ".ico": "image/vnd.microsoft.icon",
	".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".png": "image/png",
	".svg": "image/svg+xml", ".tif": "image/tiff", ".tiff": "image/tiff",
	".webp": "image/webp",
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
	// audio. .spx is served as WAV: the server transcodes Speex→WAV via
	// speexdec before it ever reaches the client, so the byte stream at a
	// .spx URL is audio/wav, NOT audio/ogg. .webm can be audio or video —
	// dictionaries ship audio, so default to that.
	".mp3": "audio/mpeg", ".ogg": "audio/ogg", ".opus": "audio/ogg",
	".oga": "audio/ogg", ".spx": "audio/wav", ".wav": "audio/wav",
	".m4a": "audio/mp4", ".m4b": "audio/mp4", ".aac": "audio/aac",
	".webm": "audio/webm", ".weba": "audio/webm",
	// video
	".mp4": "video/mp4",
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
	d, err := e.open()
	if err != nil {
		httpErr(w, 500, "%v", err)
		return
	}
	rc, mime, err := d.Resource(name)
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
			_, _ = w.Write(wav)
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
	// — it is unplayable raw Speex, so don't let a day-long cache entry mask
	// the fix once speexdec is installed. Everything else caches normally.
	if isSpx {
		w.Header().Set("Cache-Control", "no-store")
	} else {
		w.Header().Set("Cache-Control", "public, max-age=86400")
	}
	_, _ = io.Copy(w, rc)
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
	// winner's WAV in the re-check below — no duplicate decode, no two writers
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
// decoder is used by default and trusted — there is no per-file fallback to
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
	tmp, err := os.MkdirTemp("", "gonow-spx")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	spxPath := filepath.Join(tmp, "in.spx")
	wavPath := filepath.Join(tmp, "out.wav")
	if err := os.WriteFile(spxPath, raw, 0o644); err != nil {
		return nil, err
	}
	if outb, err := exec.Command(s.Speexdec, spxPath, wavPath).CombinedOutput(); err != nil {
		return nil, fmt.Errorf("%s: %v (%s)", s.Speexdec, err, strings.TrimSpace(string(outb)))
	}
	data, err := os.ReadFile(wavPath)
	if err != nil || len(data) == 0 {
		return nil, fmt.Errorf("speexdec produced no output")
	}
	return data, nil
}

// handleIngest runs "Enable fuzzy & full-text search" (D5) for one
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
	full := r.URL.Query().Get("full") != ""
	level := store.LevelText
	if r.URL.Query().Get("level") == "headwords" {
		level = store.LevelHeadwords
	}

	fl, ok := w.(http.Flusher)
	if !ok {
		httpErr(w, 500, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")

	emit := func(event string, v any) {
		var buf strings.Builder
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		_ = enc.Encode(v) // Encode appends \n — harmless before the blank line
		fmt.Fprintf(w, "event: %s\ndata: %s\n", event, buf.String())
		fl.Flush()
	}

	last := time.Now()
	err = e.ingest(full, level, func(done, total int) {
		if time.Since(last) > 200*time.Millisecond {
			last = time.Now()
			emit("progress", map[string]int{"done": done, "total": total})
		}
	})
	if err != nil {
		emit("error", map[string]string{"error": err.Error()})
		return
	}
	d, err := e.open()
	if err != nil {
		emit("error", map[string]string{"error": err.Error()})
		return
	}
	m := d.Meta()
	emit("done", dictInfo{
		ID: e.ID, Name: m.Name, Format: m.Format, Path: e.Path,
		Entries: m.EntryCount, Caps: d.Caps(),
		DBPath: dbPathOf(e),
	})
}
