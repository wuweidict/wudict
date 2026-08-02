// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"database/sql"
	"fmt"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/legbehindneck/wudict/internal/dict"
	"github.com/legbehindneck/wudict/internal/store"
)

// Reveal hands a path to the OS file manager, so it must never accept a path
// the app is not already displaying, and never run at all for a browser on
// another machine (it would open a window on someone else's desktop).
func TestRevealAuthorization(t *testing.T) {
	isolatedDBDir(t)
	dicts := t.TempDir()
	if err := os.WriteFile(filepath.Join(dicts, "x.dsl"), []byte(sampleDSL), 0o644); err != nil {
		t.Fatal(err)
	}
	reg, err := NewRegistry([]string{dicts}, false)
	if err != nil {
		t.Fatal(err)
	}
	s := New(reg)
	s.ConfigPath = filepath.Join(t.TempDir(), "wudict.toml")

	allowed := []string{
		dicts,                         // a dictionary folder
		filepath.Join(dicts, "x.dsl"), // something inside it
		os.Getenv("WUDICT_DB_DIR"),    // the library
		filepath.Join(os.Getenv("WUDICT_DB_DIR"), "Espasa", "text.db"), // inside the library
		s.ConfigPath, // the config file itself
	}
	for _, p := range allowed {
		if !s.revealAllowed(p) {
			t.Errorf("should be revealable: %s", p)
		}
	}
	denied := []string{"/etc/passwd", "/", filepath.Dir(dicts), dicts + "-sibling", ""}
	for _, p := range denied {
		if s.revealAllowed(p) {
			t.Errorf("must NOT be revealable: %s", p)
		}
	}

	// a request from another machine is refused outright
	req := httptest.NewRequest("GET", "/api/reveal?path="+dicts, nil)
	req.RemoteAddr = "192.168.1.50:5555"
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != 403 {
		t.Errorf("remote reveal: got %d, want 403", rec.Code)
	}
	// and an unknown path from localhost is refused too (no shell-out)
	req = httptest.NewRequest("GET", "/api/reveal?path=/etc/passwd", nil)
	req.RemoteAddr = "127.0.0.1:5555"
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != 403 {
		t.Errorf("unknown path: got %d, want 403", rec.Code)
	}
}

// within must not treat a sibling with a shared prefix as "inside".
func TestWithin(t *testing.T) {
	cases := []struct {
		root, path string
		want       bool
	}{
		{"/a/b", "/a/b", true},
		{"/a/b", "/a/b/c", true},
		{"/a/b", "/a/b/c/d.txt", true},
		{"/a/b", "/a/bc", false},
		{"/a/b", "/a", false},
		{"/a/b", "/x", false},
	}
	for _, c := range cases {
		if got := within(c.root, c.path); got != c.want {
			t.Errorf("within(%q, %q) = %v, want %v", c.root, c.path, got, c.want)
		}
	}
}

// Each desktop gets its own established wording.
func TestRevealLabel(t *testing.T) {
	got := revealLabel()
	want := map[string]string{
		"darwin":  "Reveal in Finder",
		"windows": "Show in File Explorer",
	}[runtime.GOOS]
	if want == "" {
		want = "Open Containing Folder"
	}
	if got != want {
		t.Errorf("revealLabel() on %s = %q, want %q", runtime.GOOS, got, want)
	}
}

// /api/config is what the panel's Folders section reads, and /setup must stay
// reachable after first run so folders can be seen and edited from the app.
func TestConfigEndpointAndSetupPage(t *testing.T) {
	isolatedDBDir(t)
	dicts := t.TempDir()
	if err := os.WriteFile(filepath.Join(dicts, "x.dsl"), []byte(sampleDSL), 0o644); err != nil {
		t.Fatal(err)
	}
	reg, err := NewRegistry([]string{dicts, filepath.Join(t.TempDir(), "gone")}, false)
	if err != nil {
		t.Fatal(err)
	}
	s := New(reg)
	s.ConfigPath = "/tmp/cfg.toml"
	s.DictDirOrigin = "flag"
	s.DictDirEditable = false

	var info map[string]any
	getJSON(t, s, "/api/config", &info)
	roots := info["roots"].([]any)
	if len(roots) != 2 {
		t.Fatalf("roots = %v", roots)
	}
	if info["configPath"] != "/tmp/cfg.toml" || info["dictDirOrigin"] != "flag" {
		t.Errorf("config info = %v", info)
	}
	if info["dictDirEditable"] != false {
		t.Error("a flag-set DICT_DIR must be reported as not editable in the file")
	}
	if info["revealLabel"] == "" {
		t.Error("missing reveal label")
	}
	// httptest requests come from a TEST-NET address, so this one is remote:
	// reveal must not be offered
	if info["canReveal"] != false {
		t.Error("a non-loopback request must not be offered reveal")
	}
	// ...while the same call from localhost is
	req := httptest.NewRequest("GET", "/api/config", nil)
	req.RemoteAddr = "127.0.0.1:4444"
	rec0 := httptest.NewRecorder()
	s.ServeHTTP(rec0, req)
	if !strings.Contains(rec0.Body.String(), `"canReveal":true`) {
		t.Errorf("loopback request should be offered reveal: %s", rec0.Body.String())
	}

	// the setup page is served on demand, not only while the registry is empty
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("GET", "/setup", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Point WuWeiDict at your dictionaries") {
		t.Fatalf("/setup not served: %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Serving 1 dictionary from 2 folders") {
		t.Errorf("intro should summarise, not list every path: %q", firstLines(rec.Body.String()))
	}
	// "/" still serves the app, not setup, while dictionaries are in use
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if strings.Contains(rec.Body.String(), "Point WuWeiDict at your dictionaries") {
		t.Error("/ should serve the app once dictionaries are in use")
	}
}

func firstLines(s string) string {
	if i := strings.Index(s, "<h2"); i > 0 && i < len(s) {
		return s[:i]
	}
	if len(s) > 400 {
		return s[:400]
	}
	return s
}

// The registry must collapse a folder given twice, however it is spelled —
// straight from config/flags (NewRegistry) and from the setup page (SetDirs).
func TestRegistryDedupesFolders(t *testing.T) {
	isolatedDBDir(t)
	dicts := t.TempDir()
	if err := os.WriteFile(filepath.Join(dicts, "x.dsl"), []byte(sampleDSL), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "shortcut")
	if err := os.Symlink(dicts, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	reg, err := NewRegistry([]string{dicts, dicts, dicts + string(filepath.Separator), link}, false)
	if err != nil {
		t.Fatal(err)
	}
	if roots := reg.Roots(); len(roots) != 1 {
		t.Fatalf("four spellings of one folder gave %d rows: %+v", len(roots), roots)
	}
	if reg.Count() != 1 {
		t.Errorf("dictionaries served = %d, want 1", reg.Count())
	}

	// and through the setup save path, which also persists the list
	s := New(reg)
	s.ConfigPath = filepath.Join(t.TempDir(), "wudict.toml")
	var v map[string]any
	getJSON(t, s, "/api/setup?path="+dicts+"&path="+link+"&path="+dicts+"&save=1", &v)
	if v["saved"] != true {
		t.Fatalf("save failed: %v", v)
	}
	if dirs := v["dirs"].([]any); len(dirs) != 1 {
		t.Errorf("saved dirs = %v, want one", dirs)
	}
	data, _ := os.ReadFile(s.ConfigPath)
	if strings.Count(string(data), dicts) != 1 {
		t.Errorf("duplicate folder written to config: %q", data)
	}
}

// The "keep my previously imported dictionaries" checkbox must work in BOTH
// directions. It used to send its state only when checked, so the server only
// ever turned the library ON: clearing the box could not undo an earlier yes,
// and the page always rendered it unchecked regardless of the real setting.
func TestUseCachedIsTwoWay(t *testing.T) {
	isolatedDBDir(t)
	dicts := t.TempDir()
	if err := os.WriteFile(filepath.Join(dicts, "x.dsl"), []byte(sampleDSL), 0o644); err != nil {
		t.Fatal(err)
	}
	reg, err := NewRegistry([]string{dicts}, false)
	if err != nil {
		t.Fatal(err)
	}
	s := New(reg)
	s.ConfigPath = filepath.Join(t.TempDir(), "wudict.toml")

	var v map[string]any
	// checked → on, and persisted
	getJSON(t, s, "/api/setup?path="+dicts+"&useCached=1&save=1", &v)
	if v["saved"] != true || !reg.UseCached() {
		t.Fatalf("useCached=1 did not enable: %v", v)
	}
	data, _ := os.ReadFile(s.ConfigPath)
	if !strings.Contains(string(data), `USE_CACHED = "1"`) {
		t.Errorf("not persisted: %q", data)
	}
	// unchecked → off again (this is what silently did nothing before)
	getJSON(t, s, "/api/setup?path="+dicts+"&useCached=0&save=1", &v)
	if v["saved"] != true || reg.UseCached() {
		t.Fatalf("useCached=0 did not disable: %v", v)
	}
	data, _ = os.ReadFile(s.ConfigPath)
	if !strings.Contains(string(data), `USE_CACHED = "0"`) {
		t.Errorf("off state not persisted: %q", data)
	}
	// omitted → the setting is left exactly as it was
	if err := s.setUseCached(true); err != nil {
		t.Fatal(err)
	}
	getJSON(t, s, "/api/setup?path="+dicts+"&save=1", &v)
	if !reg.UseCached() {
		t.Error("omitting useCached must not change the setting")
	}
}

// Every response carries the Server header a second launch looks for; without
// it, "the port is busy" cannot tell wudict from anything else.
func TestServerIdentityHeader(t *testing.T) {
	isolatedDBDir(t)
	reg, err := NewRegistry([]string{t.TempDir()}, false)
	if err != nil {
		t.Fatal(err)
	}
	s := New(reg)
	s.Version = "v9.9"
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("GET", "/api/config", nil))
	if got := rec.Header().Get("Server"); got != "wudict/v9.9" {
		t.Errorf("Server header = %q, want wudict/v9.9", got)
	}
	// and it is present even on a 404, so any endpoint identifies the app
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("GET", "/nope", nil))
	if got := rec.Header().Get("Server"); got != "wudict/v9.9" {
		t.Errorf("404 Server header = %q", got)
	}
}

// The panel's switches: each feature goes on and off independently, naming
// one must not disturb the others, and a dictionary whose source is gone
// cannot be changed at all (its prepared data is the only copy — which is why
// none of this needs a confirmation prompt).
func TestFeatureTogglesBothWays(t *testing.T) {
	isolatedDBDir(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "x.dsl"), []byte(sampleDSL), 0o644); err != nil {
		t.Fatal(err)
	}
	reg, err := NewRegistry([]string{dir}, false)
	if err != nil {
		t.Fatal(err)
	}
	s := New(reg)
	dicts := getDicts(t, s, "/api/dicts")
	id := dicts[0].ID
	caps := func() dict.Caps {
		return getDicts(t, s, "/api/dicts")[0].Caps
	}
	// default: a DSL prepares itself so it can be searched at all, but only
	// the cheap headword index — both heavy indexes start off
	if c := caps(); c.Contains || c.FTS {
		t.Fatalf("neither heavy index may be built by default: %+v", c)
	}
	sse(t, s, "/api/ingest?dict="+id+"&contains=1")
	if c := caps(); !c.Contains || c.FTS {
		t.Fatalf("contains=1 must add contains and nothing else: %+v", c)
	}
	// naming one feature leaves the other alone
	sse(t, s, "/api/ingest?dict="+id+"&fts=1")
	if c := caps(); !c.Contains || !c.FTS {
		t.Fatalf("adding full-text must keep contains: %+v", c)
	}
	sse(t, s, "/api/ingest?dict="+id+"&fts=0")
	if c := caps(); !c.Contains || c.FTS {
		t.Fatalf("stripping full-text must keep contains: %+v", c)
	}
	sse(t, s, "/api/ingest?dict="+id+"&contains=0")
	if c := caps(); c.Contains || c.FTS {
		t.Fatalf("after contains=0: %+v", c)
	}
	// and back on again — stripping is reversible while the source is there
	sse(t, s, "/api/ingest?dict="+id+"&fts=1&contains=1")
	if c := caps(); !c.Contains || !c.FTS {
		t.Fatalf("re-enabling both: %+v", c)
	}

	// a prepared dictionary with no source refuses, rather than destroying the
	// only copy of its own text
	src := "/gone/orphan.mdx"
	odir, err := store.ClaimDir(src)
	if err != nil {
		t.Fatal(err)
	}
	r := &stubReader{meta: dict.Meta{Name: "Orphan", Format: "mdx", Path: src}}
	if err := store.Ingest(r, store.TextDBPath(odir), nil); err != nil {
		t.Fatal(err)
	}
	reg2, err := NewRegistry([]string{t.TempDir()}, true)
	if err != nil {
		t.Fatal(err)
	}
	e := reg2.all()[0]
	if err := e.setFeatures(features{}, nil); err == nil {
		t.Error("a source-less dictionary must refuse to have its data stripped")
	}
}

// slowFormat is a dictionary whose ingest takes measurable time, so a test can
// observe how many are being prepared at once.
type slowReader struct{ n int }

var (
	liveIngests atomic.Int32
	maxIngests  atomic.Int32
)

func (r *slowReader) Meta() dict.Meta { return dict.Meta{Name: "Slow", Format: "slow"} }
func (r *slowReader) Close() error {
	liveIngests.Add(-1)
	return nil
}
func (r *slowReader) Next() (dict.Entry, error) {
	if r.n == 0 {
		if v := liveIngests.Add(1); v > maxIngests.Load() {
			maxIngests.Store(v)
		}
	}
	if r.n >= 40 {
		return dict.Entry{}, io.EOF
	}
	r.n++
	time.Sleep(3 * time.Millisecond) // long enough for overlap to be visible
	return dict.Entry{Headwords: []string{fmt.Sprintf("w%d", r.n)}, Body: "<p>x</p>", Kind: dict.BodyHTML}, nil
}

func init() {
	dict.RegisterFormat(".slow", func(p string) (dict.Dictionary, error) { return &fakeDict{words: []string{"w1"}, path: p}, nil })
	dict.RegisterReader(".slow", func(p string) (dict.Reader, error) { return &slowReader{}, nil })
}

// Background indexing must respect INDEX_WORKERS. Before this bound existed, a
// single "all dictionaries" search started one ingest per dictionary — measured
// at 500 MB and 424 % CPU for four real dictionaries, extrapolating to 18 GB
// for a 100-dictionary library (docs/PERF.md M1).
func TestIndexingConcurrencyIsBounded(t *testing.T) {
	isolatedDBDir(t)
	dir := t.TempDir()
	const dicts = 12
	for i := 0; i < dicts; i++ {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("d%02d.slow", i)), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, workers := range []int{1, 3} {
		liveIngests.Store(0)
		maxIngests.Store(0)
		SetIndexWorkers(workers)
		isolatedDBDir(t) // a fresh library so every dictionary needs preparing
		reg, err := NewRegistry([]string{dir}, false)
		if err != nil {
			t.Fatal(err)
		}
		s := New(reg)
		s.AutoIndex = true
		searchStream(t, s, "/api/search?q=w&mode=prefix&dict=all")
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			var prepared int
			for _, e := range reg.all() {
				if _, ok := preparedFor(e.Path); ok {
					prepared++
				}
			}
			if prepared == dicts {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		got := int(maxIngests.Load())
		if got == 0 {
			t.Fatalf("workers=%d: no indexing observed", workers)
		}
		// the foreground slot may add one on top of the background limit
		if got > workers+1 {
			t.Errorf("INDEX_WORKERS=%d: %d dictionaries were indexed at once", workers, got)
		}
		t.Logf("INDEX_WORKERS=%d → peak concurrent ingests %d", workers, got)
	}
	SetIndexWorkers(1)
}

// Preview eviction: unprepared dictionaries hold an in-memory headword index
// (~350 B per headword), so the registry caps how much of that may stay open
// and closes the least recently used. Prepared dictionaries answer from disk
// and must never be evicted — there is nothing to reclaim and reopening costs.
func TestPreviewEviction(t *testing.T) {
	isolatedDBDir(t)
	dir := t.TempDir()
	for i := 0; i < 4; i++ {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("p%d.fake", i)), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	reg, err := NewRegistry([]string{dir}, false)
	if err != nil {
		t.Fatal(err)
	}
	// open all four as preview backends
	for _, e := range reg.all() {
		if _, err := e.open(); err != nil {
			t.Fatal(err)
		}
	}
	if got := reg.previewBytes(); got == 0 {
		t.Fatal("preview backends should be weighed")
	}
	// nothing is evicted while the backends are in use, whatever the budget
	reg.SetPreviewBudget(1)
	if freed := reg.sweep(); freed != 0 {
		t.Errorf("recently-used backends must not be evicted (freed %d)", freed)
	}
	// age them past the idle threshold, then sweep
	old := time.Now().Add(-2 * minEvictIdle).UnixNano()
	for _, e := range reg.all() {
		e.lastUse.Store(old)
	}
	if freed := reg.sweep(); freed == 0 {
		t.Fatal("idle backends over budget should be evicted")
	}
	if got := reg.previewBytes(); got > 1 {
		t.Errorf("still %d bytes of preview open after the sweep", got)
	}
	// an evicted dictionary reopens on next use and still answers
	e := reg.all()[0]
	d, err := e.open()
	if err != nil || d == nil {
		t.Fatalf("evicted dictionary must reopen: %v", err)
	}
	if res, err := d.Prefix("bet", 5); err != nil || len(res) == 0 {
		t.Errorf("reopened dictionary should still search: %v %v", res, err)
	}

	// a PREPARED dictionary weighs nothing and is never a candidate
	dsl := t.TempDir()
	if err := os.WriteFile(filepath.Join(dsl, "x.dsl"), []byte(sampleDSL), 0o644); err != nil {
		t.Fatal(err)
	}
	reg2, err := NewRegistry([]string{dsl}, false)
	if err != nil {
		t.Fatal(err)
	}
	e2 := reg2.all()[0]
	if _, err := e2.open(); err != nil {
		t.Fatal(err)
	}
	if w := e2.weight.Load(); w != 0 {
		t.Errorf("a prepared dictionary must weigh 0, got %d", w)
	}
	reg2.SetPreviewBudget(1)
	for _, e := range reg2.all() {
		e.lastUse.Store(old)
	}
	if freed := reg2.sweep(); freed != 0 {
		t.Errorf("prepared dictionaries must never be evicted (freed %d)", freed)
	}
}

// A PREPARED dictionary answers text from SQLite, but opens its source again
// when a resource misses media.db — a full direct backend holding the same
// ~350 B per headword. That handle must be budgeted and released like any
// other, and releasing it must not break the dictionary: text keeps working,
// and the next resource request reopens it.
func TestResourceHandleIsEvictable(t *testing.T) {
	isolatedDBDir(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "r.fake"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	reg, err := NewRegistry([]string{dir}, false)
	if err != nil {
		t.Fatal(err)
	}
	e := reg.all()[0]
	// prepare it, so it is served through `upgraded` with a lazy source handle
	if err := e.setFeatures(features{}, nil); err != nil {
		t.Fatal(err)
	}
	d, err := e.open()
	if err != nil {
		t.Fatal(err)
	}
	u, ok := d.(*upgraded)
	if !ok {
		t.Fatalf("a prepared dictionary should be served through upgraded, got %T", d)
	}
	if u.srcWeight() != 0 || reg.previewBytes() != 0 {
		t.Fatal("nothing should be held before a resource is requested")
	}

	// a resource request misses the (absent) media.db and opens the source
	_, _, _ = d.Resource("audio/x.mp3")
	if u.srcWeight() == 0 {
		t.Fatal("the resource handle should be weighed once opened")
	}
	if reg.previewBytes() == 0 {
		t.Error("the registry must count the resource handle against the budget")
	}

	// in use → never released, whatever the budget
	reg.SetPreviewBudget(1)
	if freed := reg.sweep(); freed != 0 {
		t.Errorf("a just-used resource handle must not be released (freed %d)", freed)
	}
	// idle and over budget → released
	u.srcUse.Store(time.Now().Add(-2 * minEvictIdle).UnixNano())
	if freed := reg.sweep(); freed == 0 {
		t.Fatal("an idle resource handle over budget should be released")
	}
	if u.srcWeight() != 0 {
		t.Error("weight should be zero once released")
	}

	// the dictionary still works — text comes from SQLite …
	if res, err := d.Prefix("bet", 5); err != nil || len(res) == 0 {
		t.Errorf("text lookup must survive releasing the resource handle: %v %v", res, err)
	}
	// … and the handle reopens on the next resource request
	_, _, _ = d.Resource("audio/x.mp3")
	if u.srcWeight() == 0 {
		t.Error("the reopened handle should be weighed again")
	}
}

// O1: a trigram index built by an older dict.FoldVersion is reported through
// /api/dicts, keeps working, and is repaired by re-requesting the same
// feature — the panel's "click to rebuild".
func TestStaleFoldIsReportedAndRebuildable(t *testing.T) {
	isolatedDBDir(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "x.dsl"), []byte(sampleDSL), 0o644); err != nil {
		t.Fatal(err)
	}
	reg, err := NewRegistry([]string{dir}, false)
	if err != nil {
		t.Fatal(err)
	}
	s := New(reg)
	id := getDicts(t, s, "/api/dicts")[0].ID
	sse(t, s, "/api/ingest?dict="+id+"&contains=1")

	row := getDicts(t, s, "/api/dicts")[0]
	if !row.Caps.Contains || row.ContainsStale {
		t.Fatalf("a freshly built index must be usable and not stale: %+v", row)
	}

	// rewrite the recorded folding version, as a future dict.Fold change would
	textDB := row.DBPath
	if textDB == "" {
		t.Fatal("no prepared database to age")
	}
	db, err := sql.Open(sqliteDriver(), "file:"+textDB)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("UPDATE meta SET value = '0' WHERE key = 'fold_version'"); err != nil {
		t.Fatal(err)
	}
	db.Close()

	row = getDicts(t, s, "/api/dicts")[0]
	if !row.ContainsStale {
		t.Fatal("an index folded by another version was not reported as stale")
	}
	if !row.Caps.Contains {
		t.Error("contains was withdrawn: a stale index is marked, not disabled")
	}

	// the panel re-requests the feature it already has; that is the repair
	sse(t, s, "/api/ingest?dict="+id+"&contains=1")
	row = getDicts(t, s, "/api/dicts")[0]
	if row.ContainsStale {
		t.Error("re-requesting contains did not rebuild the stale index")
	}
	if !row.Caps.Contains {
		t.Errorf("the rebuild lost the index: %+v", row.Caps)
	}
}

// sqliteDriver names whichever SQLite driver this build registered — mattn
// under cgo, modernc under purego (D29). The store package picks it at compile
// time and keeps the name private, so a test that needs to write to a
// text.db asks the sql package what got registered.
func sqliteDriver() string {
	for _, d := range sql.Drivers() {
		if d == "sqlite3" || d == "sqlite" {
			return d
		}
	}
	return "sqlite"
}
