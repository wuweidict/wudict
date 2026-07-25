// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
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

// Server exposes the registry over HTTP.
type Server struct {
	reg *Registry
	mux *http.ServeMux

	// ConfigPath, when set, is where the first-run setup flow persists
	// DICT_DIR. In-memory state is updated regardless, so setup works
	// even when the folder came from a CLI flag or the file is read-only.
	ConfigPath string

	// Speexdec is the path to the speexdec binary; when set, .spx
	// resources are transcoded to WAV on demand (cached) since no
	// browser can play Speex natively.
	Speexdec string
}

func New(reg *Registry) *Server {
	s := &Server{reg: reg, mux: http.NewServeMux()}
	s.mux.HandleFunc("GET /", s.handleIndex)
	s.mux.HandleFunc("GET /api/dicts", s.handleDicts)
	s.mux.HandleFunc("GET /api/search", s.handleSearch)
	s.mux.HandleFunc("GET /api/rescan", s.handleRescan)
	s.mux.HandleFunc("GET /api/ingest", s.handleIngest)
	s.mux.HandleFunc("GET /api/setup", s.handleSetup)
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

// handleSetup validates a dictionary folder and, with save=1, switches
// the registry to it live and persists DICT_DIR to the config file.
func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	raw := strings.TrimSpace(r.URL.Query().Get("path"))
	if raw == "" {
		httpErr(w, 400, "missing path parameter")
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
}

func (s *Server) handleDicts(w http.ResponseWriter, r *http.Request) {
	entries := s.reg.all()
	out := make([]dictInfo, len(entries))
	// open in parallel: first load after setup would otherwise block for
	// seconds while dozens of dictionaries build their indexes serially
	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup
	for i, e := range entries {
		wg.Add(1)
		go func(i int, e *entry) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			info := dictInfo{ID: e.ID, Path: e.Path}
			d, err := e.open()
			if err != nil {
				info.Error = err.Error()
				out[i] = info
				return
			}
			m := d.Meta()
			info.Name, info.Format, info.Entries, info.Caps = m.Name, m.Format, m.EntryCount, d.Caps()
			if info.Caps.FTS {
				info.DBPath = store.CacheBase(e.Path, m.Name) + ".text.db"
			}
			out[i] = info
		}(i, e)
	}
	wg.Wait()
	writeJSON(w, out)
}

func (s *Server) handleRescan(w http.ResponseWriter, r *http.Request) {
	if err := s.reg.Rescan(); err != nil {
		httpErr(w, 500, "rescan: %v", err)
		return
	}
	s.handleDicts(w, r)
}

func parseMode(s string) (search.Mode, error) {
	switch s {
	case "", "prefix":
		return search.Prefix, nil
	case "exact":
		return search.Exact, nil
	case "fuzzy":
		return search.Fuzzy, nil
	case "fts":
		return search.FullText, nil
	}
	return 0, fmt.Errorf("unknown mode %q", s)
}

// searchHit is one dictionary's results in /api/search.
type searchHit struct {
	Dict    string        `json:"dict"`
	Name    string        `json:"name"`
	Results []dict.Result `json:"results"`
	Skipped bool          `json:"skipped,omitempty"`
	Error   string        `json:"error,omitempty"`
}

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
		e, err := s.reg.get(dictParam)
		if err != nil {
			httpErr(w, 404, "%v", err)
			return
		}
		entries = []*entry{e}
	}

	var dicts []dict.Dictionary
	var hits []searchHit
	idxOf := map[dict.Dictionary]int{}
	for _, e := range entries {
		d, err := e.open()
		h := searchHit{Dict: e.ID}
		if err != nil {
			h.Error = err.Error()
			hits = append(hits, h)
			continue
		}
		h.Name = d.Meta().Name
		idxOf[d] = len(hits)
		hits = append(hits, h)
		dicts = append(dicts, d)
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	for i, sh := range search.All(ctx, dicts, mode, q, n) {
		_ = i
		j := idxOf[dicts[i]]
		hits[j].Results = sh.Results
		hits[j].Skipped = sh.Skipped
		if sh.Err != nil {
			hits[j].Error = sh.Err.Error()
		}
		// Resolve dictionary-internal refs (sound://, relative, absolute)
		// to /res/{dict}/… here, where it is unit-tested — the client
		// renders article HTML as-is.
		for k := range hits[j].Results {
			hits[j].Results[k].Body = RewriteEntryHTML(hits[j].Results[k].Body, hits[j].Dict)
		}
	}
	writeJSON(w, hits)
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
	// browsers cannot play Speex: transcode .spx to WAV via speexdec
	if strings.HasSuffix(strings.ToLower(name), ".spx") && s.Speexdec != "" {
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
	if mime != "" {
		w.Header().Set("Content-Type", mime)
	}
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_, _ = io.Copy(w, rc)
}

// spxToWav transcodes one Speex resource to WAV via the external
// speexdec binary, caching the result under <dbdir>/spxcache/.
func (s *Server) spxToWav(dictID, name string, rc io.Reader) ([]byte, error) {
	sum := sha256.Sum256([]byte(dictID + "\x00" + name))
	cacheDir := filepath.Join(store.DefaultDBDir(), "spxcache")
	wavPath := filepath.Join(cacheDir, hex.EncodeToString(sum[:])[:24]+".wav")
	if data, err := os.ReadFile(wavPath); err == nil && len(data) > 0 {
		return data, nil
	}
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return nil, err
	}
	spxPath := wavPath + ".spx"
	f, err := os.Create(spxPath)
	if err != nil {
		return nil, err
	}
	_, cpErr := io.Copy(f, rc)
	f.Close()
	defer os.Remove(spxPath)
	if cpErr != nil {
		return nil, cpErr
	}
	cmd := exec.Command(s.Speexdec, spxPath, wavPath)
	if outb, err := cmd.CombinedOutput(); err != nil {
		_ = os.Remove(wavPath) // never leave a 0-byte cache entry
		return nil, fmt.Errorf("%s: %v (%s)", s.Speexdec, err, strings.TrimSpace(string(outb)))
	}
	data, err := os.ReadFile(wavPath)
	if err != nil || len(data) == 0 {
		_ = os.Remove(wavPath)
		return nil, fmt.Errorf("speexdec produced no output")
	}
	logx.V("spx transcoded %s/%s -> %s (%d bytes)", dictID, name, wavPath, len(data))
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
		DBPath: store.CacheBase(e.Path, m.Name) + ".text.db",
	})
}
