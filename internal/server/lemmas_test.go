// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wuweidict/wudict/internal/morph"
)

// The catalogue these tests install from. Plain .tsv rather than .tsv.gz: the
// point under test is the HTTP plumbing, and gzip framing would only add a way
// for the fixture itself to be wrong.
const plData = "być\tjest\tjestem\n"

// catalogSite serves a manifest and its one asset. gate, when non-nil, is
// received from before the asset body is written, which is how a test holds a
// download open long enough to observe it running. hits counts asset requests -
// the single-flight assertion is "one download", not "one job".
type catalogSite struct {
	url  string
	hits *int32
}

func newCatalogSite(t *testing.T, digest string, gate <-chan struct{}) catalogSite {
	t.Helper()
	sum := sha256.Sum256([]byte(plData))
	if digest == "" {
		digest = hex.EncodeToString(sum[:])
	}
	manifest := fmt.Sprintf(`{"version":1,"generated":"2026-09-01","languages":[
	  {"code":"pl","name":"Polish","file":"pl.tsv","size":%d,"lemmas":3,
	   "sha256":%q,"heap_mb":7,"source":"michmech/lemmatization-lists","license":"ODbL-1.0"}]}`,
		len(plData), digest)

	var hits int32
	mux := http.NewServeMux()
	mux.HandleFunc("/manifest.json", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(manifest))
	})
	mux.HandleFunc("/pl.tsv", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		if gate != nil {
			select {
			case <-gate:
			case <-r.Context().Done():
				return
			}
		}
		_, _ = w.Write([]byte(plData))
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return catalogSite{url: ts.URL + "/manifest.json", hits: &hits}
}

// lemmaServer is a registry over an empty folder - no dictionary is involved in
// installing lemma data - plus a lemma folder and a catalogue.
func lemmaServer(t *testing.T, url string, cacheSize int) (*Server, string) {
	t.Helper()
	isolatedDBDir(t)
	reg, err := NewRegistry([]string{t.TempDir()}, false)
	if err != nil {
		t.Fatal(err)
	}
	s := New(reg)
	// Installs outlive their request by design; they must not outlive the
	// temp folders they are writing into.
	t.Cleanup(func() { s.lemmas.wg.Wait() })
	dir := t.TempDir()
	s.LemmaDir, s.LemmaURL = dir, url
	s.Morph = morph.New(cacheSize, dir)
	return s, dir
}

func do(t *testing.T, s *Server, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, newRequest(method, path, nil))
	return rec
}

func getLemmas(t *testing.T, s *Server) lemmaInfo {
	t.Helper()
	rec := do(t, s, "GET", "/api/lemmas")
	if rec.Code != 200 {
		t.Fatalf("GET /api/lemmas: status %d: %s", rec.Code, rec.Body.String())
	}
	var info lemmaInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &info); err != nil {
		t.Fatalf("GET /api/lemmas: %v: %s", err, rec.Body.String())
	}
	return info
}

func rowOf(t *testing.T, info lemmaInfo, code string) lemmaRow {
	t.Helper()
	for _, r := range info.Languages {
		if r.Code == code {
			return r
		}
	}
	t.Fatalf("no %s row in %+v", code, info.Languages)
	return lemmaRow{}
}

// waitRow polls the endpoint the way the page does, until want reports true.
func waitRow(t *testing.T, s *Server, code string, want func(lemmaRow) bool) lemmaRow {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		r := rowOf(t, getLemmas(t, s), code)
		if want(r) {
			return r
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting on %s: %+v", code, r)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// The whole point of doing this from a page rather than a shell: the language
// is searchable when the download ends, with no restart.
func TestLemmaInstallMakesTheLanguageUsableAtOnce(t *testing.T) {
	site := newCatalogSite(t, "", nil)
	s, dir := lemmaServer(t, site.url, 2)

	if s.Morph.Supports("pl") {
		t.Fatal("setup: pl was already supported")
	}
	if r := rowOf(t, getLemmas(t, s), "pl"); r.Installed || !r.Catalogued || r.HeapMB != 7 {
		t.Fatalf("before install: %+v", r)
	}

	rec := do(t, s, "POST", "/api/lemmas?code=polish") // a NAME, as the CLI accepts
	if rec.Code != 202 {
		t.Fatalf("POST: status %d: %s", rec.Code, rec.Body.String())
	}
	r := waitRow(t, s, "pl", func(r lemmaRow) bool { return r.Installed })
	if r.State != "" || r.Mismatch {
		t.Fatalf("after install: %+v", r)
	}
	b, err := os.ReadFile(filepath.Join(dir, "pl.tsv"))
	if err != nil || string(b) != plData {
		t.Fatalf("installed file = %q, %v", b, err)
	}
	if !s.Morph.Supports("pl") {
		t.Fatal("installed, but the lemmatizer was not told: Rescan did not run")
	}
}

// A wrong digest must leave the folder exactly as it was - no file, and no
// half-written temporary that a later scan could pick up - and must say why.
func TestLemmaChecksumFailureLeavesNothing(t *testing.T) {
	site := newCatalogSite(t, strings.Repeat("a", 64), nil)
	s, dir := lemmaServer(t, site.url, 2)

	if rec := do(t, s, "POST", "/api/lemmas?code=pl"); rec.Code != 202 {
		t.Fatalf("POST: status %d: %s", rec.Code, rec.Body.String())
	}
	r := waitRow(t, s, "pl", func(r lemmaRow) bool { return r.State == "error" })
	if !strings.Contains(r.Error, "checksum") {
		t.Fatalf("error = %q, want a checksum mismatch", r.Error)
	}
	if r.Installed {
		t.Fatalf("a failed download must not count as installed: %+v", r)
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		t.Fatalf("the folder must be empty, found %q", e.Name())
	}
	if s.Morph.Supports("pl") {
		t.Fatal("a failed download must not make the language supported")
	}
}

// Two taps on one checkbox, or two open tabs: one download.
func TestLemmaInstallIsSingleFlight(t *testing.T) {
	gate := make(chan struct{})
	site := newCatalogSite(t, "", gate)
	s, _ := lemmaServer(t, site.url, 2)

	for i := 0; i < 3; i++ {
		if rec := do(t, s, "POST", "/api/lemmas?code=pl"); rec.Code != 202 {
			t.Fatalf("POST %d: status %d: %s", i, rec.Code, rec.Body.String())
		}
	}
	waitRow(t, s, "pl", func(r lemmaRow) bool { return r.State == "downloading" })
	close(gate)
	waitRow(t, s, "pl", func(r lemmaRow) bool { return r.Installed })

	if n := atomic.LoadInt32(site.hits); n != 1 {
		t.Fatalf("%d downloads, want 1", n)
	}
}

// Removal deletes every file supplying the language, not only the one in use.
func TestLemmaRemove(t *testing.T) {
	site := newCatalogSite(t, "", nil)
	s, dir := lemmaServer(t, site.url, 2)

	if rec := do(t, s, "POST", "/api/lemmas?code=pl"); rec.Code != 202 {
		t.Fatalf("POST: status %d", rec.Code)
	}
	waitRow(t, s, "pl", func(r lemmaRow) bool { return r.Installed })
	// A second file for the same language, of the kind Installed reports as
	// shadowed. Removing pl must take this one too.
	if err := os.WriteFile(filepath.Join(dir, "polish.tsv"), []byte(plData), 0o644); err != nil {
		t.Fatal(err)
	}

	rec := do(t, s, "DELETE", "/api/lemmas?code=pl")
	if rec.Code != 200 {
		t.Fatalf("DELETE: status %d: %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Removed []string `json:"removed"`
		Builtin bool     `json:"builtin"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Removed) != 2 || got.Builtin {
		t.Fatalf("removed = %+v (builtin %v), want both files", got.Removed, got.Builtin)
	}
	if s.Morph.Supports("pl") {
		t.Fatal("removed, but still supported: Rescan did not run")
	}
	if r := rowOf(t, getLemmas(t, s), "pl"); r.Installed {
		t.Fatalf("after removal: %+v", r)
	}
}

// Nothing reaches the network before the argument is known to name a language
// this catalogue carries.
func TestLemmaInstallRejectsBadCodes(t *testing.T) {
	site := newCatalogSite(t, "", nil)
	s, _ := lemmaServer(t, site.url, 2)

	for _, q := range []string{"", "zzz", "../../etc/passwd", "pl%20ru"} {
		if rec := do(t, s, "POST", "/api/lemmas?code="+q); rec.Code != 400 {
			t.Fatalf("POST code=%q: status %d, want 400", q, rec.Code)
		}
	}
	// A real language the catalogue does not carry is equally a 400: the caller
	// asked for something that does not exist, not for something that failed.
	if rec := do(t, s, "POST", "/api/lemmas?code=ru"); rec.Code != 400 {
		t.Fatalf("POST code=ru: status %d, want 400", rec.Code)
	}
	if n := atomic.LoadInt32(site.hits); n != 0 {
		t.Fatalf("%d asset requests, want none", n)
	}
}

// Offline must not blank the page: what is installed is the half of the answer
// that needs no network.
func TestLemmaCatalogueUnreachable(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", 404)
	}))
	url := ts.URL + "/manifest.json"
	ts.Close()

	s, dir := lemmaServer(t, url, 2)
	if err := os.WriteFile(filepath.Join(dir, "ru.tsv"), []byte("идти\tидёт\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	info := getLemmas(t, s)
	if info.Error == "" {
		t.Fatal("an unreachable catalogue must be reported")
	}
	if r := rowOf(t, info, "ru"); !r.Installed || r.Catalogued {
		t.Fatalf("ru = %+v, want installed and not catalogued", r)
	}
	// English is compiled in, so it is present with or without a catalogue.
	if r := rowOf(t, info, "en"); !r.Installed || !r.Builtin {
		t.Fatalf("en = %+v, want installed and built in", r)
	}
	// And installing is refused with the reason, rather than with a 400 that
	// would read as "there is no such language".
	if rec := do(t, s, "POST", "/api/lemmas?code=pl"); rec.Code != 502 {
		t.Fatalf("POST: status %d, want 502", rec.Code)
	}
}

// MORPH_CACHE=0 makes every download on this page inert. Saying so is the
// difference between a page that works and one that lies.
func TestLemmaReportsLemmatizationOff(t *testing.T) {
	site := newCatalogSite(t, "", nil)
	s, dir := lemmaServer(t, site.url, 0)

	info := getLemmas(t, s)
	if info.Enabled || info.CacheSize != 0 {
		t.Fatalf("enabled = %v, cacheSize = %d, want off", info.Enabled, info.CacheSize)
	}
	if info.Dir != dir || info.URL != site.url {
		t.Fatalf("dir = %q, url = %q", info.Dir, info.URL)
	}
}

// The file the lemmatizer ignores is invisible in a GUI unless the GUI says so.
func TestLemmaReportsShadowedFiles(t *testing.T) {
	site := newCatalogSite(t, "", nil)
	s, dir := lemmaServer(t, site.url, 2)
	for _, name := range []string{"ru.tsv", "ru.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("идти\tидёт\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	info := getLemmas(t, s)
	if len(info.Shadowed) != 1 || !strings.Contains(info.Shadowed[0], "ru.txt") {
		t.Fatalf("shadowed = %+v, want the ignored ru.txt", info.Shadowed)
	}
}

// A file that is installed but is not the published bytes is marked, never
// corrected: a hand-written file is a legitimate thing to have.
func TestLemmaMismatchIsReported(t *testing.T) {
	site := newCatalogSite(t, "", nil)
	s, dir := lemmaServer(t, site.url, 2)
	if err := os.WriteFile(filepath.Join(dir, "pl.tsv"), []byte("mine\tmine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := rowOf(t, getLemmas(t, s), "pl")
	if !r.Installed || !r.Mismatch {
		t.Fatalf("pl = %+v, want installed with a mismatch", r)
	}
	// Hashing is memoized on path|mtime|size; rewriting the file must still be
	// noticed rather than answered from the cache.
	if err := os.WriteFile(filepath.Join(dir, "pl.tsv"), []byte(plData), 0o644); err != nil {
		t.Fatal(err)
	}
	if r := rowOf(t, getLemmas(t, s), "pl"); r.Mismatch {
		t.Fatalf("pl = %+v, want the rewritten file to match", r)
	}
}

// D69: the CORS surface is exactly search, dicts and resources. This endpoint
// names a folder on the user's disk and writes to it.
func TestLemmasIsNotCORSEnabled(t *testing.T) {
	site := newCatalogSite(t, "", nil)
	s, _ := lemmaServer(t, site.url, 2)
	for _, m := range []string{"GET", "POST", "DELETE"} {
		rec := httptest.NewRecorder()
		req := newRequest(m, "/api/lemmas?code=pl", nil)
		req.Header.Set("Origin", chromeOrigin)
		s.ServeHTTP(rec, req)
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Fatalf("%s: Access-Control-Allow-Origin = %q", m, got)
		}
	}
}
