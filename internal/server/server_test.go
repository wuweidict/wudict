// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"archive/zip"
	"bufio"
	"encoding/json"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/glowinthedark/gonow-dict/internal/dict"
	_ "github.com/glowinthedark/gonow-dict/internal/format/dsl" // register .dsl
)

const sampleDSL = "#NAME \"Server Test Dict\"\n\n" +
	"corazón\n\t[b]1.[/b] órgano muscular [s]beat.mp3[/s]\n\n" +
	"casa\n\tvivienda\n"

// newTestServer builds a dict dir with one DSL dictionary (+files.zip)
// and an isolated DB cache.
func newTestServer(t *testing.T) *Server {
	t.Helper()
	dir := t.TempDir()
	dslPath := filepath.Join(dir, "test.dsl")
	if err := os.WriteFile(dslPath, []byte(sampleDSL), 0o644); err != nil {
		t.Fatal(err)
	}
	zf, err := os.Create(dslPath + ".files.zip")
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(zf)
	w, _ := zw.Create("beat.mp3")
	w.Write([]byte("MP3DATA"))
	zw.Close()
	zf.Close()

	t.Setenv("GONOW_DB_DIR", t.TempDir())
	reg, err := NewRegistry(dir)
	if err != nil {
		t.Fatal(err)
	}
	return New(reg)
}

func getJSON(t *testing.T, s *Server, path string, into any) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("GET", path, nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if into != nil {
		if err := json.Unmarshal(rec.Body.Bytes(), into); err != nil {
			t.Fatalf("GET %s: bad JSON (%v): %s", path, err, rec.Body.String())
		}
	}
	return rec
}

// searchStream parses the NDJSON /api/search response into per-slot hits
// (only "hit" lines with results), ordered by slot index.
func searchStream(t *testing.T, s *Server, path string) []streamMsg {
	t.Helper()
	req := httptest.NewRequest("GET", path, nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	var out []streamMsg
	sc := bufio.NewScanner(strings.NewReader(rec.Body.String()))
	sc.Buffer(make([]byte, 0, 64*1024), 4<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var m streamMsg
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("GET %s: bad NDJSON line (%v): %s", path, err, line)
		}
		if m.T == "hit" {
			out = append(out, m)
		}
	}
	return out
}

func TestDictsAndSearch(t *testing.T) {
	s := newTestServer(t)

	var dicts []dictInfo
	getJSON(t, s, "/api/dicts", &dicts)
	if len(dicts) != 1 || dicts[0].Name != "Server Test Dict" {
		t.Fatalf("dicts: %+v", dicts)
	}
	// DSL auto-ingests: full caps + a shareable DBPath (D7)
	if !dicts[0].Caps.FTS || dicts[0].DBPath == "" {
		t.Errorf("caps/dbpath: %+v", dicts[0])
	}
	id := dicts[0].ID

	hits := searchStream(t, s, "/api/search?q=corazon&mode=fuzzy&dict="+id)
	if len(hits) != 1 || len(hits[0].Results) != 1 || hits[0].Results[0].Headword != "corazón" {
		t.Fatalf("fuzzy hits: %+v", hits)
	}
	hits = searchStream(t, s, "/api/search?q=vivienda&mode=fts&dict=all")
	if len(hits) != 1 || len(hits[0].Results) != 1 || hits[0].Results[0].Headword != "casa" {
		t.Fatalf("fts hits: %+v", hits)
	}

	if rec := getJSON(t, s, "/api/search?mode=fuzzy", nil); rec.Code != 400 {
		t.Errorf("missing q: %d", rec.Code)
	}
	if rec := getJSON(t, s, "/api/search?q=x&mode=bogus", nil); rec.Code != 400 {
		t.Errorf("bad mode: %d", rec.Code)
	}
}

// --- fake direct-only format, for the auto-index test ------------------

type fakeDict struct{ words []string }

func (d *fakeDict) Meta() dict.Meta {
	return dict.Meta{Name: "Fake Dict", Format: "fake", Path: "x.fake", EntryCount: len(d.words)}
}
func (d *fakeDict) Caps() dict.Caps { return dict.Caps{Exact: true, Prefix: true} }
func (d *fakeDict) match(pred func(string) bool) []dict.Result {
	var out []dict.Result
	for _, w := range d.words {
		if pred(w) {
			out = append(out, dict.Result{Headword: w, Body: "<p>" + w + "</p>"})
		}
	}
	return out
}
func (d *fakeDict) Exact(w string, n int) ([]dict.Result, error) {
	return d.match(func(x string) bool { return x == w }), nil
}
func (d *fakeDict) Prefix(w string, n int) ([]dict.Result, error) {
	return d.match(func(x string) bool { return strings.HasPrefix(x, w) }), nil
}
func (d *fakeDict) Keywords(offset, n int) []string { return d.words }
func (d *fakeDict) Resource(string) (io.ReadCloser, string, error) {
	return nil, "", dict.ErrNotFound
}
func (d *fakeDict) Close() error { return nil }

type fakeReader struct {
	words []string
	i     int
}

func (r *fakeReader) Meta() dict.Meta { return (&fakeDict{words: r.words}).Meta() }
func (r *fakeReader) Close() error    { return nil }
func (r *fakeReader) Next() (dict.Entry, error) {
	if r.i >= len(r.words) {
		return dict.Entry{}, io.EOF
	}
	w := r.words[r.i]
	r.i++
	return dict.Entry{Headwords: []string{w}, Body: "<p>" + w + "</p>", Kind: dict.BodyHTML}, nil
}

func init() {
	words := []string{"córazon", "beta", "gamma"}
	dict.RegisterFormat(".fake", func(string) (dict.Dictionary, error) { return &fakeDict{words: words}, nil })
	dict.RegisterReader(".fake", func(string) (dict.Reader, error) { return &fakeReader{words: words}, nil })
}

// TestAutoIndexOnFirstSearch: a direct-only dictionary (no fuzzy) must gain
// a fuzzy index in the background the first time it is searched, so a
// later fuzzy query — including accent-folded — succeeds without any
// explicit "enable" step.
func TestAutoIndexOnFirstSearch(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "x.fake"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GONOW_DB_DIR", t.TempDir())
	reg, err := NewRegistry(dir)
	if err != nil {
		t.Fatal(err)
	}
	s := New(reg)
	s.AutoIndex = true

	var dicts []dictInfo
	getJSON(t, s, "/api/dicts", &dicts)
	id := dicts[0].ID
	if dicts[0].Caps.Fuzzy {
		t.Fatalf("fake dict should start without fuzzy: %+v", dicts[0])
	}

	// first search (prefix) triggers the background fuzzy build
	searchStream(t, s, "/api/search?q=beta&mode=prefix&dict="+id)

	// poll until the accent-folded fuzzy query resolves via the new index
	var hits []streamMsg
	for i := 0; i < 100; i++ {
		hits = searchStream(t, s, "/api/search?q=corazon&mode=fuzzy&dict="+id)
		if len(hits) == 1 && !hits[0].Skipped && len(hits[0].Results) == 1 {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	if len(hits) != 1 || hits[0].Skipped || len(hits[0].Results) != 1 || hits[0].Results[0].Headword != "córazon" {
		t.Fatalf("auto-index fuzzy did not become available: %+v", hits)
	}
}

func TestResourceAndIndex(t *testing.T) {
	s := newTestServer(t)
	var dicts []dictInfo
	getJSON(t, s, "/api/dicts", &dicts)
	id := dicts[0].ID

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("GET", "/res/"+id+"/beat.mp3", nil))
	if rec.Code != 200 || rec.Body.String() != "MP3DATA" {
		t.Fatalf("res: %d %q", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "audio/mpeg" {
		t.Errorf("mime: %q", ct)
	}
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("GET", "/res/"+id+"/nope.png", nil))
	if rec.Code != 404 {
		t.Errorf("missing res: %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "gonow-dict") {
		t.Errorf("index: %d", rec.Code)
	}
}

func TestIngestSSEAndMedia(t *testing.T) {
	s := newTestServer(t)
	var dicts []dictInfo
	getJSON(t, s, "/api/dicts", &dicts)
	id := dicts[0].ID

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("GET", "/api/ingest?dict="+id+"&full=1", nil))
	if ct := rec.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("not SSE: %q body=%s", ct, rec.Body.String())
	}
	var sawDone bool
	sc := bufio.NewScanner(rec.Body)
	for sc.Scan() {
		if sc.Text() == "event: done" {
			sawDone = true
		}
		if sc.Text() == "event: error" {
			sc.Scan()
			t.Fatalf("ingest error: %s", sc.Text())
		}
	}
	if !sawDone {
		t.Fatal("no done event")
	}

	// media.db must exist and hold the resource
	matches, _ := filepath.Glob(filepath.Join(os.Getenv("GONOW_DB_DIR"), "*.media.db"))
	if len(matches) != 1 {
		t.Fatalf("media.db: %v", matches)
	}
	// uuid pairing: media uuid matches text.db uuid
	textDB := strings.TrimSuffix(matches[0], ".media.db") + ".text.db"
	if _, err := os.Stat(textDB); err != nil {
		t.Fatalf("paired text.db missing: %v", err)
	}
}

// TestSetupFlow: empty dict dir → "/" serves the setup page; /api/setup
// validates, rejects empty folders, then switches live and persists.
func TestSetupFlow(t *testing.T) {
	emptyDir := t.TempDir()
	t.Setenv("GONOW_DB_DIR", t.TempDir())
	reg, err := NewRegistry(filepath.Join(emptyDir, "does-not-exist"))
	if err != nil {
		t.Fatal(err)
	}
	s := New(reg)
	s.ConfigPath = filepath.Join(t.TempDir(), "config.toml")

	// missing folder → setup page, not the app
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "choose your dictionary folder") {
		t.Fatalf("expected setup page, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "does not exist") {
		t.Errorf("missing-folder reason not shown")
	}

	// validate a folder without dictionaries
	var v map[string]any
	getJSON(t, s, "/api/setup?path="+emptyDir, &v)
	if v["error"] != nil || v["found"].(float64) != 0 {
		t.Fatalf("empty validate: %v", v)
	}
	// saving an empty folder must be refused
	getJSON(t, s, "/api/setup?path="+emptyDir+"&save=1", &v)
	if v["error"] == nil || v["saved"] != nil {
		t.Fatalf("empty save not refused: %v", v)
	}

	// a real dictionary folder
	dictDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dictDir, "mini.dsl"), []byte(sampleDSL), 0o644); err != nil {
		t.Fatal(err)
	}
	getJSON(t, s, "/api/setup?path="+dictDir+"&save=1", &v)
	if v["saved"] != true || v["found"].(float64) != 1 {
		t.Fatalf("save failed: %v", v)
	}
	// registry switched live
	if reg.Dir() != dictDir || reg.Count() != 1 {
		t.Errorf("registry not switched: %q %d", reg.Dir(), reg.Count())
	}
	// persisted to config
	data, err := os.ReadFile(s.ConfigPath)
	if err != nil || !strings.Contains(string(data), "DICT_DIR = "+`"`+dictDir+`"`) {
		t.Errorf("config not persisted: %v %q", err, data)
	}
	// "/" now serves the app
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if !strings.Contains(rec.Body.String(), "design tokens") {
		t.Errorf("app page not served after setup")
	}
	// nonexistent path errors cleanly
	getJSON(t, s, "/api/setup?path=/no/such/dir/xyz", &v)
	if v["error"] != "folder not found" {
		t.Errorf("bad path: %v", v)
	}
}

func TestUnknownDict(t *testing.T) {
	s := newTestServer(t)
	if rec := getJSON(t, s, "/api/search?q=x&dict=deadbeef", nil); rec.Code != 404 {
		t.Errorf("unknown dict: %d", rec.Code)
	}
	if rec := getJSON(t, s, "/api/ingest?dict=deadbeef", nil); rec.Code != 404 {
		t.Errorf("unknown ingest: %d", rec.Code)
	}
}
