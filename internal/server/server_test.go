// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"archive/zip"
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/legbehindneck/wudict/internal/dict"
	_ "github.com/legbehindneck/wudict/internal/format/dsl" // register .dsl
	"github.com/legbehindneck/wudict/internal/store"
)

// stubReader ingests a single entry; used to fabricate a native .text.db.
type stubReader struct {
	meta dict.Meta
	done bool
}

func (s *stubReader) Meta() dict.Meta { return s.meta }
func (s *stubReader) Close() error    { return nil }
func (s *stubReader) Next() (dict.Entry, error) {
	if s.done {
		return dict.Entry{}, io.EOF
	}
	s.done = true
	return dict.Entry{Headwords: []string{"hello"}, Body: "<p>world</p>", Kind: dict.BodyHTML}, nil
}

// TestDictProvenance: /api/dicts reports the foreign source and its media
// companion, and flags packable media (the DSL fixture ships a .files.zip).
func TestDictProvenance(t *testing.T) {
	s := newTestServer(t)
	dicts := getDicts(t, s, "/api/dicts")
	if len(dicts) != 1 {
		t.Fatalf("want 1 dict, got %d", len(dicts))
	}
	d := dicts[0]
	if !strings.HasSuffix(d.Source, "test.dsl") {
		t.Errorf("source=%q, want the .dsl path", d.Source)
	}
	if len(d.MediaSrc) != 1 || !strings.HasSuffix(d.MediaSrc[0], "test.dsl.files.zip") {
		t.Errorf("mediaSrc=%v, want [test.dsl.files.zip]", d.MediaSrc)
	}
	if !d.HasMedia {
		t.Error("a DSL with a .files.zip should report packable media")
	}
}

// TestLibraryIsOptIn: the library is never a discovery root by default — that
// is what kept the setup page hidden — and USE_CACHED turns it on. A prepared
// dictionary whose source is gone then opens with the wudict: prefix stripped.
func TestLibraryIsOptIn(t *testing.T) {
	isolatedDBDir(t)
	src := "/gone/x.mdx"
	dir, err := store.ClaimDir(src)
	if err != nil {
		t.Fatal(err)
	}
	r := &stubReader{meta: dict.Meta{Name: "Naturalized", Format: "mdx", Path: src}}
	dbPath := store.TextDBPath(dir)
	if err := store.Ingest(r, dbPath, nil); err != nil {
		t.Fatal(err)
	}
	if filepath.Base(dir) != "x" {
		t.Errorf("library folder = %q, want %q (mirrors the source file name)", filepath.Base(dir), "x")
	}

	// default: opted out — an empty dictionary folder means an empty registry,
	// so the first-run setup page shows.
	off, err := NewRegistry([]string{t.TempDir()}, false)
	if err != nil {
		t.Fatal(err)
	}
	if off.Count() != 0 {
		t.Fatalf("library must not be a discovery root by default: count=%d", off.Count())
	}

	reg, err := NewRegistry([]string{t.TempDir()}, true) // opted in
	if err != nil {
		t.Fatal(err)
	}
	if reg.Count() != 1 {
		t.Fatalf("prepared dict not listed with USE_CACHED: count=%d", reg.Count())
	}
	d, err := reg.all()[0].open()
	if err != nil {
		t.Fatal(err)
	}
	m := d.Meta()
	if m.Name != "Naturalized" {
		t.Errorf("name=%q, want Naturalized", m.Name)
	}
	if m.Format != "mdx" {
		t.Errorf("format=%q, want mdx (wudict: prefix stripped)", m.Format)
	}
	if m.Path != dbPath {
		t.Errorf("path=%q, want the .text.db path %q", m.Path, dbPath)
	}
	if !d.Caps().FTS || !d.Caps().Prefix {
		t.Errorf("a prepared dictionary must still search: %+v", d.Caps())
	}
}

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

	isolatedDBDir(t)
	reg, err := NewRegistry([]string{dir}, false)
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

// getDicts drives the NDJSON /api/dicts (or /api/rescan) stream to completion
// and returns its rows. The server emits them in completion order, so they are
// sorted by id here to keep assertions deterministic.
func getDicts(t *testing.T, s *Server, path string) []dictInfo {
	t.Helper()
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
	if rec.Code != 200 {
		t.Fatalf("GET %s: status %d: %s", path, rec.Code, rec.Body.String())
	}
	var out []dictInfo
	var begin, end bool
	sc := bufio.NewScanner(strings.NewReader(rec.Body.String()))
	sc.Buffer(make([]byte, 0, 64*1024), 4<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var m dictMsg
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("GET %s: bad NDJSON line (%v): %s", path, err, line)
		}
		switch m.T {
		case "begin":
			begin = true
		case "dict":
			if m.Dict == nil {
				t.Fatalf("GET %s: dict line without a row: %s", path, line)
			}
			out = append(out, *m.Dict)
		case "end":
			end = true
		}
	}
	// the client unblocks on "begin" and stops waiting on "end"; a stream
	// missing either one leaves it stuck, so both are part of the contract
	if !begin || !end {
		t.Fatalf("GET %s: incomplete stream (begin=%v end=%v): %s", path, begin, end, rec.Body.String())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// searchStream parses the NDJSON /api/search response into per-slot hits
// (only "hit" lines with results), ordered by slot index.
// sse drives an SSE endpoint to completion (the ingest/feature-toggle flow)
// and fails on an error event.
func sse(t *testing.T, s *Server, path string) {
	t.Helper()
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
	if rec.Code != 200 {
		t.Fatalf("%s: status %d", path, rec.Code)
	}
	if strings.Contains(rec.Body.String(), "event: error") {
		t.Fatalf("%s: %s", path, rec.Body.String())
	}
}

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

// TestDictsStreamShape: the client blocks its search form until it knows how
// many dictionaries are coming and unblocks on the first row (D30), so the
// order and content of the NDJSON frames is a contract, not an implementation
// detail: "begin" carries the registry count and must precede every row.
func TestDictsStreamShape(t *testing.T) {
	s := newTestServer(t)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("GET", "/api/dicts", nil))
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/x-ndjson") {
		t.Fatalf("content-type = %q, want application/x-ndjson", ct)
	}
	var kinds []string
	var total, rows int
	for _, line := range strings.Split(strings.TrimSpace(rec.Body.String()), "\n") {
		var m dictMsg
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("bad NDJSON line (%v): %s", err, line)
		}
		kinds = append(kinds, m.T)
		switch m.T {
		case "begin":
			total = m.Total
		case "dict":
			rows++
		}
	}
	if len(kinds) < 2 || kinds[0] != "begin" || kinds[len(kinds)-1] != "end" {
		t.Fatalf("frame order = %v, want begin … end", kinds)
	}
	if want := s.reg.Count(); total != want {
		t.Errorf("begin.total = %d, want the registry count %d", total, want)
	}
	if rows != total {
		t.Errorf("got %d dict rows, want %d (one per registry entry)", rows, total)
	}
}

func TestDictsAndSearch(t *testing.T) {
	s := newTestServer(t)

	dicts := getDicts(t, s, "/api/dicts")
	if len(dicts) != 1 || dicts[0].Name != "Server Test Dict" {
		t.Fatalf("dicts: %+v", dicts)
	}
	// a DSL prepares itself (it has no native index) — the cheap headword
	// index only, plus a shareable DBPath (D7)
	if !dicts[0].Caps.Prefix || dicts[0].DBPath == "" {
		t.Errorf("caps/dbpath: %+v", dicts[0])
	}
	if dicts[0].Caps.FTS || dicts[0].Caps.Contains {
		t.Errorf("preparing a DSL must not build the opt-in indexes: %+v", dicts[0].Caps)
	}
	id := dicts[0].ID

	// contains is opt-in: skipped until asked for, then it works. (A trigram
	// index costs about as much as the whole rest of the index.)
	hits := searchStream(t, s, "/api/search?q=razon&mode=contains&dict="+id)
	if len(hits) != 1 || !hits[0].Skipped {
		t.Fatalf("contains should be unavailable until enabled: %+v", hits)
	}
	sse(t, s, "/api/ingest?dict="+id+"&contains=1")
	hits = searchStream(t, s, "/api/search?q=razon&mode=contains&dict="+id)
	if len(hits) != 1 || len(hits[0].Results) != 1 || hits[0].Results[0].Headword != "corazón" {
		t.Fatalf("contains hits after enabling: %+v", hits)
	}
	sse(t, s, "/api/ingest?dict="+id+"&fts=1")
	hits = searchStream(t, s, "/api/search?q=vivienda&mode=fts&dict=all")
	if len(hits) != 1 || len(hits[0].Results) != 1 || hits[0].Results[0].Headword != "casa" {
		t.Fatalf("fts hits: %+v", hits)
	}

	if rec := getJSON(t, s, "/api/search?mode=contains", nil); rec.Code != 400 {
		t.Errorf("missing q: %d", rec.Code)
	}
	if rec := getJSON(t, s, "/api/search?q=x&mode=bogus", nil); rec.Code != 400 {
		t.Errorf("bad mode: %d", rec.Code)
	}
}

// --- fake direct-only format, for the auto-index test ------------------

type fakeDict struct {
	words []string
	path  string
}

func (d *fakeDict) Meta() dict.Meta {
	return dict.Meta{Name: "Fake Dict", Format: "fake", Path: d.path, EntryCount: len(d.words)}
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
	path  string
	i     int
}

func (r *fakeReader) Meta() dict.Meta { return (&fakeDict{words: r.words, path: r.path}).Meta() }
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
	dict.RegisterFormat(".fake", func(p string) (dict.Dictionary, error) { return &fakeDict{words: words, path: p}, nil })
	dict.RegisterReader(".fake", func(p string) (dict.Reader, error) { return &fakeReader{words: words, path: p}, nil })
}

// TestAutoIndexOnFirstSearch: a direct-only dictionary (no contains) must gain
// an index in the background the first time it is searched, so a later
// contains query — including accent-folded — succeeds without any explicit
// "enable" step.
func TestAutoIndexOnFirstSearch(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "x.fake"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	isolatedDBDir(t)
	reg, err := NewRegistry([]string{dir}, false)
	if err != nil {
		t.Fatal(err)
	}
	s := New(reg)
	s.AutoIndex = true

	dicts := getDicts(t, s, "/api/dicts")
	id := dicts[0].ID
	if dicts[0].Caps.Contains {
		t.Fatalf("fake dict should start without contains: %+v", dicts[0])
	}

	// first search (prefix) triggers the background index build
	searchStream(t, s, "/api/search?q=beta&mode=prefix&dict="+id)

	// poll until the accent-folded prefix query resolves via the new index:
	// the direct backend matches raw strings only, so "corazon" finding
	// "córazon" proves the index was built behind the search
	var hits []streamMsg
	for i := 0; i < 100; i++ {
		hits = searchStream(t, s, "/api/search?q=corazon&mode=prefix&dict="+id)
		if len(hits) == 1 && !hits[0].Skipped && len(hits[0].Results) == 1 {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	if len(hits) != 1 || hits[0].Skipped || len(hits[0].Results) != 1 || hits[0].Results[0].Headword != "córazon" {
		t.Fatalf("auto-index did not become available: %+v", hits)
	}
	// and it built the cheap index only — contains stays off until asked for
	dicts2 := getDicts(t, s, "/api/dicts")
	if dicts2[0].Caps.Contains {
		t.Error("auto-index must not build a trigram index")
	}
}

// TestFullIngestNoResourcesFlagsEmpty: a full ingest of a dictionary with no
// packable resources must flag the entry so the panel stops offering "pack
// media" (fixes the flash-and-revert loop), and must not error.
func TestFullIngestNoResourcesFlagsEmpty(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "x.fake"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	isolatedDBDir(t)
	reg, err := NewRegistry([]string{dir}, false)
	if err != nil {
		t.Fatal(err)
	}
	e := reg.all()[0]
	if err := e.setFeatures(features{FullText: true, Media: true}, nil); err != nil {
		t.Fatalf("full ingest errored: %v", err)
	}
	if !e.noPackableMedia() {
		t.Error("a resource-less full ingest should flag the entry as having no packable media")
	}
	dicts := getDicts(t, New(reg), "/api/dicts")
	if dicts[0].HasMedia {
		t.Error("HasMedia must be false after a resource-less full ingest")
	}
}

func TestResourceAndIndex(t *testing.T) {
	s := newTestServer(t)
	dicts := getDicts(t, s, "/api/dicts")
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
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "wudict") {
		t.Errorf("index: %d", rec.Code)
	}
}

func TestIngestSSEAndMedia(t *testing.T) {
	s := newTestServer(t)
	dicts := getDicts(t, s, "/api/dicts")
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
	matches, _ := filepath.Glob(filepath.Join(os.Getenv("WUDICT_DB_DIR"), "*", store.MediaDBName))
	if len(matches) != 1 {
		t.Fatalf("media.db: %v", matches)
	}
	// uuid pairing: media uuid matches text.db uuid
	textDB := store.TextDBPath(filepath.Dir(matches[0]))
	if _, err := os.Stat(textDB); err != nil {
		t.Fatalf("paired text.db missing: %v", err)
	}
}

// TestSetupFlow: empty dict dir → "/" serves the setup page; /api/setup
// validates, rejects empty folders, then switches live and persists.
func TestSetupFlow(t *testing.T) {
	emptyDir := t.TempDir()
	isolatedDBDir(t)
	reg, err := NewRegistry([]string{filepath.Join(emptyDir, "does-not-exist")}, false)
	if err != nil {
		t.Fatal(err)
	}
	s := New(reg)
	s.ConfigPath = filepath.Join(t.TempDir(), "wudict.toml")

	// missing folder → setup page, not the app
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Point WuWeiDict at your dictionaries") {
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
	if dirs := reg.Dirs(); len(dirs) != 1 || dirs[0] != dictDir || reg.Count() != 1 {
		t.Errorf("registry not switched: %q %d", reg.Dirs(), reg.Count())
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

// TestMediaDBIsNeverADictionary: the reported bug, end to end. A full ingest
// leaves a media.db beside the text.db in the library folder; with the library
// opted in, the dictionary must be listed exactly once — never a second,
// phantom "wudict:…" row for the sidecar.
func TestMediaDBIsNeverADictionary(t *testing.T) {
	isolatedDBDir(t)
	src := "/gone/AHD5-2017.slob"
	dir, err := store.ClaimDir(src)
	if err != nil {
		t.Fatal(err)
	}
	r := &stubReader{meta: dict.Meta{Name: "AHD5", Format: "slob", Path: src}}
	if err := store.Ingest(r, store.TextDBPath(dir), nil); err != nil {
		t.Fatal(err)
	}
	uuid, err := store.ReadMetaValue(store.TextDBPath(dir), "dict_uuid")
	if err != nil {
		t.Fatal(err)
	}
	// a real packed media.db: same user_version + meta table as a text.db,
	// which is exactly why bare ".db" registration used to open it.
	if err := store.IngestMedia(&resDict{}, []string{"a.mp3"}, store.MediaDBPath(dir), uuid, nil); err != nil {
		t.Fatal(err)
	}

	reg, err := NewRegistry([]string{t.TempDir()}, true)
	if err != nil {
		t.Fatal(err)
	}
	s := New(reg)
	dicts := getDicts(t, s, "/api/dicts")
	// (other tests share this process and the WUDICT_DB_DIR env var, so assert
	// on this dictionary specifically rather than on the total row count)
	var ahd []dictInfo
	for _, d := range dicts {
		if strings.HasSuffix(strings.ToLower(d.Path), store.MediaDBName) {
			t.Errorf("a media.db was listed as a dictionary: %+v", d)
		}
		if strings.HasPrefix(d.Format, "wudict") {
			t.Errorf("internal wudict format leaked into the list: %+v", d)
		}
		if d.Name == "AHD5" {
			ahd = append(ahd, d)
		}
	}
	if len(ahd) != 1 {
		t.Fatalf("want exactly 1 row for AHD5, got %d: %+v", len(ahd), ahd)
	}
	if ahd[0].Format != "slob" {
		t.Errorf("format = %q, want slob", ahd[0].Format)
	}
	if ahd[0].MediaDB == "" {
		t.Error("the packed media.db should be reported as provenance, not as a dictionary")
	}
}

// resDict is a minimal dictionary that can serve one resource, for packing.
type resDict struct{}

func (resDict) Meta() dict.Meta                           { return dict.Meta{Name: "res", Format: "fake"} }
func (resDict) Caps() dict.Caps                           { return dict.Caps{} }
func (resDict) Close() error                              { return nil }
func (resDict) Exact(string, int) ([]dict.Result, error)  { return nil, nil }
func (resDict) Prefix(string, int) ([]dict.Result, error) { return nil, nil }
func (resDict) Keywords(int, int) []string                { return nil }
func (resDict) Resource(name string) (io.ReadCloser, string, error) {
	return io.NopCloser(strings.NewReader("SOUND")), "audio/mpeg", nil
}

// TestSetupConsentFlow: with an empty dictionary folder and a non-empty
// library, "/" must still serve the setup page (the library alone must not
// suppress it), and "Use these dictionaries" enrolls them live and remembers
// the choice in the config file.
func TestSetupConsentFlow(t *testing.T) {
	isolatedDBDir(t)
	dir, err := store.ClaimDir("/gone/Prepared.mdx")
	if err != nil {
		t.Fatal(err)
	}
	r := &stubReader{meta: dict.Meta{Name: "Prepared", Format: "mdx", Path: "/gone/Prepared.mdx"}}
	if err := store.Ingest(r, store.TextDBPath(dir), nil); err != nil {
		t.Fatal(err)
	}

	reg, err := NewRegistry([]string{t.TempDir()}, false)
	if err != nil {
		t.Fatal(err)
	}
	s := New(reg)
	cfgPath := filepath.Join(t.TempDir(), "wudict.toml")
	s.ConfigPath = cfgPath

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if !strings.Contains(rec.Body.String(), "Point WuWeiDict at your dictionaries") {
		t.Fatal("prepared dictionaries must not suppress the setup page")
	}

	// the page offers them, without enrolling anything
	var lib map[string]any
	getJSON(t, s, "/api/library", &lib)
	if lib["count"].(float64) != 1 || lib["useCached"].(bool) {
		t.Fatalf("library listing wrong: %v", lib)
	}
	if reg.Count() != 0 {
		t.Fatal("listing the library must not enroll it")
	}

	// "Use these dictionaries"
	var out map[string]any
	getJSON(t, s, "/api/setup?useCached=1&save=1", &out)
	if out["saved"] != true {
		t.Fatalf("consent not saved: %v", out)
	}
	if reg.Count() != 1 {
		t.Fatalf("prepared dictionaries not in use after consent: %d", reg.Count())
	}
	saved, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(saved), `USE_CACHED = "1"`) {
		t.Errorf("USE_CACHED not persisted: %s", saved)
	}
	// and the app page is served now that dictionaries are in use
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if strings.Contains(rec.Body.String(), "Point WuWeiDict at your dictionaries") {
		t.Error("setup page still shown after dictionaries were enrolled")
	}
}

// isolatedDBDir points WUDICT_DB_DIR at a temp dir that is deliberately NOT
// t.TempDir(): background work started by the test (Registry.Warm, and with it
// DSL/BGL auto-preparation) can still be writing when the test ends, and
// t.TempDir's cleanup FAILS the test if the directory grows while it is being
// removed. Removal here is best-effort for exactly the same reason.
func isolatedDBDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "wudict-db")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("WUDICT_DB_DIR", dir)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// TestMain isolates the whole package from the user's real library. Tests set
// WUDICT_DB_DIR per-test, but background work started by a test (Registry.Warm,
// auto-index, DSL auto-preparation) can outlive it and read the variable after
// t.Setenv has restored it — which would prepare dictionaries into the real
// ~/.wudict/db. Setting it for the process makes that fallback a temp dir.
func TestMain(m *testing.M) {
	tmp, err := os.MkdirTemp("", "wudict-server-tests")
	if err != nil {
		panic(err)
	}
	os.Setenv("WUDICT_DB_DIR", tmp)
	code := m.Run()
	os.RemoveAll(tmp)
	os.Exit(code)
}

// TestSetupMultipleFolders: the setup page can save several folders; they are
// scanned as one library, persisted as a TOML array, and a folder listed twice
// (or nested inside another) contributes its dictionaries exactly once.
func TestSetupMultipleFolders(t *testing.T) {
	isolatedDBDir(t)
	a, b := t.TempDir(), t.TempDir()
	nested := filepath.Join(a, "sub")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{filepath.Join(a, "one.dsl"), filepath.Join(b, "two.dsl"), filepath.Join(nested, "three.dsl")} {
		if err := os.WriteFile(f, []byte(sampleDSL), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	reg, err := NewRegistry([]string{filepath.Join(t.TempDir(), "none")}, false)
	if err != nil {
		t.Fatal(err)
	}
	s := New(reg)
	s.ConfigPath = filepath.Join(t.TempDir(), "wudict.toml")

	// a + b + (a/sub, already covered by a) + a repeated
	q := "path=" + a + "&path=" + b + "&path=" + nested + "&path=" + a + "&save=1"
	var v map[string]any
	getJSON(t, s, "/api/setup?"+q, &v)
	if v["saved"] != true {
		t.Fatalf("save failed: %v", v)
	}
	if got := v["found"].(float64); got != 3 {
		t.Errorf("found = %v, want 3 (each dictionary counted once)", got)
	}
	if reg.Count() != 3 {
		t.Fatalf("registry has %d dictionaries, want 3", reg.Count())
	}
	// the duplicate and the nested folder are dropped; order is preserved
	dirs := reg.Dirs()
	if len(dirs) != 3 || dirs[0] != a || dirs[1] != b {
		t.Errorf("dirs = %q", dirs)
	}
	roots := reg.Roots()
	if len(roots) != 3 || roots[0].Count != 2 || roots[1].Count != 1 || roots[2].Count != 0 {
		t.Errorf("per-root counts = %+v, want 2/1/0 (earlier root wins a tie)", roots)
	}
	// the nested folder holds a dictionary but contributed none — the UI must
	// be able to say "already listed", not "empty"
	if roots[2].Total != 1 {
		t.Errorf("nested root Total = %d, want 1", roots[2].Total)
	}
	// persisted as an array
	data, _ := os.ReadFile(s.ConfigPath)
	if !strings.Contains(string(data), `DICT_DIR = ["`+a+`", "`+b+`"`) {
		t.Errorf("array not persisted: %q", data)
	}
	// and the library folder is refused as a dictionary folder
	getJSON(t, s, "/api/setup?path="+store.DefaultDBDir()+"&save=1", &v)
	if v["error"] == nil || !strings.Contains(v["error"].(string), "library folder") {
		t.Errorf("db dir should be refused: %v", v)
	}
}

// A missing folder must not take the others down with it.
func TestMissingRootIsNotFatal(t *testing.T) {
	isolatedDBDir(t)
	good := t.TempDir()
	if err := os.WriteFile(filepath.Join(good, "one.dsl"), []byte(sampleDSL), 0o644); err != nil {
		t.Fatal(err)
	}
	gone := filepath.Join(t.TempDir(), "unmounted-drive")
	reg, err := NewRegistry([]string{gone, good}, false)
	if err != nil {
		t.Fatalf("a missing folder must not fail the scan: %v", err)
	}
	if reg.Count() != 1 {
		t.Fatalf("count = %d, want 1", reg.Count())
	}
	roots := reg.Roots()
	if len(roots) != 2 || roots[0].Exists || roots[1].Count != 1 {
		t.Errorf("roots = %+v, want the missing one flagged and the good one counted", roots)
	}
}

// newDictWithResources builds a one-dictionary server whose .files.zip holds
// the named resources verbatim, so a test can put damaged bytes in one.
func newDictWithResources(t *testing.T, files map[string][]byte) *Server {
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
	for name, data := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	zw.Close()
	zf.Close()

	isolatedDBDir(t)
	reg, err := NewRegistry([]string{dir}, false)
	if err != nil {
		t.Fatal(err)
	}
	return New(reg)
}

// A dictionary can ship a damaged bundled resource — one real case in a
// 105-dictionary corpus is a Cambridge slob whose jquery.js carries 12,186 NUL
// bytes, which makes it unparseable and silently kills the dictionary's own
// tab script. Two guarantees are being tested: wudict serves the bytes
// VERBATIM (diagnosing damage is not licence to alter it), and it says so once.
func TestDamagedTextResourceIsReportedButServedVerbatim(t *testing.T) {
	damaged := []byte("var a=1;\x00\x00\x00var b=2;")
	s := newDictWithResources(t, map[string][]byte{
		"broken.js": damaged,
		"clean.js":  []byte("var ok=1;"),
		"blob.bin":  {0, 1, 2, 0},
	})
	id := getDicts(t, s, "/api/dicts")[0].ID

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("GET", "/res/"+id+"/broken.js", nil))
	if rec.Code != 200 {
		t.Fatalf("broken.js: %d", rec.Code)
	}
	if got := rec.Body.Bytes(); !bytes.Equal(got, damaged) {
		t.Errorf("damaged bytes were altered: %q, want %q", got, damaged)
	}
	if _, ok := s.nulSeen.Load(id + "\x00" + "broken.js"); !ok {
		t.Error("a NUL in a .js resource must be reported")
	}

	// A second request must not re-report: the fact is about the file, and
	// every article referencing it would otherwise repeat the warning.
	before := 0
	s.nulSeen.Range(func(any, any) bool { before++; return true })
	s.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/res/"+id+"/broken.js", nil))
	after := 0
	s.nulSeen.Range(func(any, any) bool { after++; return true })
	if before != after {
		t.Errorf("re-reported the same damaged resource: %d -> %d", before, after)
	}

	s.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/res/"+id+"/clean.js", nil))
	if _, ok := s.nulSeen.Load(id + "\x00" + "clean.js"); ok {
		t.Error("a clean .js must not be reported")
	}
	// A NUL is ordinary in a binary: only source-text formats are evidence.
	s.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/res/"+id+"/blob.bin", nil))
	if _, ok := s.nulSeen.Load(id + "\x00" + "blob.bin"); ok {
		t.Error("a NUL in a binary resource is not damage")
	}
}

// The override lets a user repair a dictionary that ships a broken resource,
// without rewriting a multi-gigabyte container. It is general: any resource of
// any dictionary, no knowledge of which file or why.
func TestResourceOverrideFromLibraryFolder(t *testing.T) {
	s := newDictWithResources(t, map[string][]byte{"beat.mp3": []byte("BUNDLED")})
	id := getDicts(t, s, "/api/dicts")[0].ID
	e, err := s.reg.get(id)
	if err != nil {
		t.Fatal(err)
	}

	// no library folder yet: the bundled blob is all there is
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("GET", "/res/"+id+"/beat.mp3", nil))
	if rec.Body.String() != "BUNDLED" {
		t.Fatalf("before preparing: %q", rec.Body.String())
	}

	if err := e.setFeatures(features{}, nil); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	textDB, ok := preparedTextDB(e.Path)
	if !ok {
		t.Fatal("no library folder after preparing")
	}
	resDir := filepath.Join(filepath.Dir(textDB), "res")
	if err := os.MkdirAll(resDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(resDir, "beat.mp3"), []byte("OVERRIDE"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(resDir, "extra.js"), []byte("var added=1;"), 0o644); err != nil {
		t.Fatal(err)
	}

	// shadows a resource the dictionary does have …
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("GET", "/res/"+id+"/beat.mp3", nil))
	if rec.Code != 200 || rec.Body.String() != "OVERRIDE" {
		t.Errorf("override not served: %d %q", rec.Code, rec.Body.String())
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
		// a day-long cache would hide the user's next edit of the file
		t.Errorf("override Cache-Control = %q, want no-cache", cc)
	}
	// … and supplies one it does not: the override is consulted BEFORE the
	// dictionary, so it fills a gap as readily as it shadows a bad file.
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("GET", "/res/"+id+"/extra.js", nil))
	if rec.Code != 200 || rec.Body.String() != "var added=1;" {
		t.Errorf("new override resource: %d %q", rec.Code, rec.Body.String())
	}

	// Nested names, because that is what articles actually reference: one slob
	// in the corpus asks for js/entry.js and css/bootstrap.min.css. A flat-only
	// override would be useless for exactly the dictionaries that need it.
	if err := os.MkdirAll(filepath.Join(resDir, "js"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(resDir, "js", "entry.js"), []byte("var nested=1;"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("GET", "/res/"+id+"/js/entry.js", nil))
	if rec.Code != 200 || rec.Body.String() != "var nested=1;" {
		t.Errorf("nested override: %d %q", rec.Code, rec.Body.String())
	}

	// A resource with no override still comes from the dictionary.
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("GET", "/res/"+id+"/nope.png", nil))
	if rec.Code != 404 {
		t.Errorf("missing resource: %d", rec.Code)
	}
}

// The name comes from a URL, so it must not be able to reach outside the
// override directory — by "..", by an absolute path, or by both.
func TestResourceOverrideRejectsEscapes(t *testing.T) {
	s := newDictWithResources(t, map[string][]byte{"beat.mp3": []byte("BUNDLED")})
	id := getDicts(t, s, "/api/dicts")[0].ID
	e, err := s.reg.get(id)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.setFeatures(features{}, nil); err != nil {
		t.Fatal(err)
	}
	textDB, _ := preparedTextDB(e.Path)
	lib := filepath.Dir(textDB)
	// a real, readable file one level above the override dir — the exact
	// thing a traversal would be reaching for
	secret := filepath.Join(lib, "info.txt")
	if err := os.WriteFile(secret, []byte("SECRET"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(lib, "res"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Positive control: without this the whole loop below could pass simply
	// because nothing is ever served, and the test would prove nothing.
	if err := os.WriteFile(filepath.Join(lib, "res", "ok.txt"), []byte("INSIDE"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	if !s.serveOverride(rec, httptest.NewRequest("GET", "/res/"+id+"/ok.txt", nil), e, "ok.txt") ||
		rec.Body.String() != "INSIDE" {
		t.Fatalf("control: a real override must be served, got %q", rec.Body.String())
	}

	// The property is "never serves a file outside <lib>/res", not "always
	// returns false": path.Clean folds "../x" to "x", which lands INSIDE the
	// directory and is a perfectly fine thing to serve. Assert the content.
	for _, name := range []string{
		"../info.txt",
		"../../info.txt",
		"a/../../info.txt",
		"./../info.txt",
		"/etc/passwd",
		"../res/../info.txt",
	} {
		rec := httptest.NewRecorder()
		s.serveOverride(rec, httptest.NewRequest("GET", "/res/"+id+"/x", nil), e, name)
		if strings.Contains(rec.Body.String(), "SECRET") {
			t.Errorf("%q reached outside the override directory: %q", name, rec.Body.String())
		}
	}
}
