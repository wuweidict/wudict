// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/glowinthedark/gonow-dict/internal/dict"
	"github.com/glowinthedark/gonow-dict/internal/store"
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
	s.ConfigPath = filepath.Join(t.TempDir(), "config.toml")

	allowed := []string{
		dicts,                         // a dictionary folder
		filepath.Join(dicts, "x.dsl"), // something inside it
		os.Getenv("GONOW_DB_DIR"),     // the library
		filepath.Join(os.Getenv("GONOW_DB_DIR"), "Espasa", "text.db"), // inside the library
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
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Point gonow at your dictionaries") {
		t.Fatalf("/setup not served: %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Serving 1 dictionary from 2 folders") {
		t.Errorf("intro should summarise, not list every path: %q", firstLines(rec.Body.String()))
	}
	// "/" still serves the app, not setup, while dictionaries are in use
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if strings.Contains(rec.Body.String(), "Point gonow at your dictionaries") {
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
	s.ConfigPath = filepath.Join(t.TempDir(), "config.toml")
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
	s.ConfigPath = filepath.Join(t.TempDir(), "config.toml")

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
// it, "the port is busy" cannot tell gonow-dict from anything else.
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
	if got := rec.Header().Get("Server"); got != "gonow-dict/v9.9" {
		t.Errorf("Server header = %q, want gonow-dict/v9.9", got)
	}
	// and it is present even on a 404, so any endpoint identifies the app
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("GET", "/nope", nil))
	if got := rec.Header().Get("Server"); got != "gonow-dict/v9.9" {
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
	var dicts []dictInfo
	getJSON(t, s, "/api/dicts", &dicts)
	id := dicts[0].ID
	caps := func() dict.Caps {
		var d []dictInfo
		getJSON(t, s, "/api/dicts", &d)
		return d[0].Caps
	}
	// default: finds and full-text (DSL prepares itself), no trigram
	if c := caps(); c.Contains {
		t.Fatalf("contains must be off by default: %+v", c)
	}
	sse(t, s, "/api/ingest?dict="+id+"&contains=1")
	if c := caps(); !c.Contains || !c.FTS {
		t.Fatalf("after contains=1: %+v", c)
	}
	// naming one feature leaves the other alone
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
