package server

import (
	"archive/zip"
	"bufio"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

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

	var hits []searchHit
	getJSON(t, s, "/api/search?q=corazon&mode=fuzzy&dict="+id, &hits)
	if len(hits) != 1 || len(hits[0].Results) != 1 || hits[0].Results[0].Headword != "corazón" {
		t.Fatalf("fuzzy hits: %+v", hits)
	}
	getJSON(t, s, "/api/search?q=vivienda&mode=fts&dict=all", &hits)
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

func TestUnknownDict(t *testing.T) {
	s := newTestServer(t)
	if rec := getJSON(t, s, "/api/search?q=x&dict=deadbeef", nil); rec.Code != 404 {
		t.Errorf("unknown dict: %d", rec.Code)
	}
	if rec := getJSON(t, s, "/api/ingest?dict=deadbeef", nil); rec.Code != 404 {
		t.Errorf("unknown ingest: %d", rec.Code)
	}
}
