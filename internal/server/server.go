package server

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/glowinthedark/gonow-dict/internal/dict"
	"github.com/glowinthedark/gonow-dict/internal/search"
	"github.com/glowinthedark/gonow-dict/internal/store"
)

//go:embed web/index.html
var indexHTML []byte

// Server exposes the registry over HTTP.
type Server struct {
	reg *Registry
	mux *http.ServeMux
}

func New(reg *Registry) *Server {
	s := &Server{reg: reg, mux: http.NewServeMux()}
	s.mux.HandleFunc("GET /", s.handleIndex)
	s.mux.HandleFunc("GET /api/dicts", s.handleDicts)
	s.mux.HandleFunc("GET /api/search", s.handleSearch)
	s.mux.HandleFunc("GET /api/rescan", s.handleRescan)
	s.mux.HandleFunc("GET /api/ingest", s.handleIngest)
	s.mux.HandleFunc("GET /res/", s.handleResource)
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.mux.ServeHTTP(w, r) }

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}

func httpErr(w http.ResponseWriter, code int, format string, args ...any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf(format, args...)})
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(indexHTML)
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
	var out []dictInfo
	for _, e := range s.reg.all() {
		info := dictInfo{ID: e.ID, Path: e.Path}
		d, err := e.open()
		if err != nil {
			info.Error = err.Error()
			out = append(out, info)
			continue
		}
		m := d.Meta()
		info.Name, info.Format, info.Entries, info.Caps = m.Name, m.Format, m.EntryCount, d.Caps()
		if info.Caps.FTS {
			info.DBPath = store.CacheBase(e.Path, m.Name) + ".text.db"
		}
		out = append(out, info)
	}
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
	defer rc.Close()
	if mime != "" {
		w.Header().Set("Content-Type", mime)
	}
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_, _ = io.Copy(w, rc)
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

	fl, ok := w.(http.Flusher)
	if !ok {
		httpErr(w, 500, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")

	emit := func(event string, v any) {
		data, _ := json.Marshal(v)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
		fl.Flush()
	}

	last := time.Now()
	err = e.ingest(full, func(done, total int) {
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
