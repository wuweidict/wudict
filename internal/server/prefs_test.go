package server

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newPrefsServer builds a server over two dictionaries with a real state file,
// returning the server and the file's path.
func newPrefsServer(t *testing.T) (*Server, string) {
	t.Helper()
	dir := t.TempDir()
	for _, n := range []string{"alpha.dsl", "beta.dsl"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte(sampleDSL), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	isolatedDBDir(t)
	state := filepath.Join(t.TempDir(), StateFile)
	reg, err := NewRegistry([]string{dir}, false, WithPrefs(LoadPrefs(state)))
	if err != nil {
		t.Fatal(err)
	}
	return New(reg), state
}

type prefsResp struct {
	Exists bool       `json:"exists"`
	Dicts  []DictPref `json:"dicts"`
}

func putPrefs(t *testing.T, s *Server, body string) prefsResp {
	t.Helper()
	req := httptest.NewRequest("PUT", "/api/prefs", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("PUT /api/prefs: status %d: %s", rec.Code, rec.Body.String())
	}
	var out prefsResp
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("PUT /api/prefs: bad JSON (%v): %s", err, rec.Body.String())
	}
	return out
}

func TestPrefsRoundTrip(t *testing.T) {
	s, state := newPrefsServer(t)
	ids := s.reg.all()
	if len(ids) != 2 {
		t.Fatalf("want 2 dictionaries, got %d", len(ids))
	}
	a, b := ids[0].ID, ids[1].ID

	var first prefsResp
	getJSON(t, s, "/api/prefs", &first)
	if first.Exists {
		t.Error("exists=true before anything was saved: the client would skip adopting localStorage")
	}
	if len(first.Dicts) != 0 {
		t.Errorf("want no records on a fresh install, got %+v", first.Dicts)
	}

	// b first, a disabled
	got := putPrefs(t, s, `{"dicts":[{"id":"`+b+`","name":"Beta","off":false},{"id":"`+a+`","name":"Alpha","off":true}]}`)
	if len(got.Dicts) != 2 || got.Dicts[0].ID != b || got.Dicts[1].ID != a {
		t.Fatalf("order not preserved: %+v", got.Dicts)
	}
	if !got.Dicts[1].Off {
		t.Error("disabled flag lost")
	}
	if got.Dicts[0].Path != ids[1].Path {
		t.Errorf("path=%q, want the registry's %q", got.Dicts[0].Path, ids[1].Path)
	}

	// the file is what a restart reads
	data, err := os.ReadFile(state)
	if err != nil {
		t.Fatalf("state file not written: %v", err)
	}
	var f prefsFile
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("state file is not valid JSON: %v", err)
	}
	if f.Version != prefsVersion {
		t.Errorf("version=%d, want %d", f.Version, prefsVersion)
	}
	if len(f.Dicts) != 2 || f.Dicts[0].ID != b {
		t.Errorf("file disagrees with the response: %+v", f.Dicts)
	}

	reloaded := LoadPrefs(state)
	if !reloaded.Off(a, ids[0].Path) {
		t.Error("a disabled dictionary came back enabled after a restart")
	}
	if reloaded.Off(b, ids[1].Path) {
		t.Error("an enabled dictionary came back disabled after a restart")
	}
	if _, exists := reloaded.Snapshot(); !exists {
		t.Error("exists=false after a restart: the client would re-adopt stale localStorage")
	}
}

func TestPrefsKeepsUnseenDictionaries(t *testing.T) {
	s, state := newPrefsServer(t)
	a := s.reg.all()[0].ID

	// a dictionary on a drive that is not mounted right now
	seed := prefsFile{Version: prefsVersion, Dicts: []DictPref{
		{ID: "deadbeef0000", Path: "/Volumes/Stick/Big.mdx", Name: "Big", Off: true},
	}}
	data, _ := json.Marshal(seed)
	if err := os.WriteFile(state, data, 0o644); err != nil {
		t.Fatal(err)
	}
	s.reg.prefs = LoadPrefs(state)

	got := putPrefs(t, s, `{"dicts":[{"id":"`+a+`","name":"Alpha"}]}`)
	if len(got.Dicts) != 2 {
		t.Fatalf("the unmounted dictionary was forgotten: %+v", got.Dicts)
	}
	last := got.Dicts[len(got.Dicts)-1]
	if last.Path != "/Volumes/Stick/Big.mdx" || !last.Off {
		t.Errorf("retained record was altered: %+v", last)
	}
}

func TestPrefsHealsMovedDictionaries(t *testing.T) {
	s, state := newPrefsServer(t)
	entries := s.reg.all()
	live := entries[0]

	// The same file, remembered under the path it had before the folder was
	// renamed: its id no longer matches anything.
	seed := prefsFile{Version: prefsVersion, Dicts: []DictPref{
		{ID: "0000stale000", Path: filepath.Join("/elsewhere", filepath.Base(live.Path)), Name: "Alpha", Off: true},
	}}
	data, _ := json.Marshal(seed)
	if err := os.WriteFile(state, data, 0o644); err != nil {
		t.Fatal(err)
	}
	s.reg.prefs = LoadPrefs(state)

	var got prefsResp
	getJSON(t, s, "/api/prefs", &got)
	if len(got.Dicts) != 1 {
		t.Fatalf("want 1 record, got %+v", got.Dicts)
	}
	if got.Dicts[0].ID != live.ID || got.Dicts[0].Path != live.Path {
		t.Fatalf("record not re-attached to the moved dictionary: %+v", got.Dicts[0])
	}
	if !got.Dicts[0].Off {
		t.Error("the setting was lost while re-attaching it")
	}
	// the repair is durable, so the next save cannot resurrect the stale id
	if !LoadPrefs(state).Off(live.ID, live.Path) {
		t.Error("healed state was not written back to disk")
	}
}

func TestPrefsAmbiguousNamesAreNotGuessed(t *testing.T) {
	s, _ := newPrefsServer(t)
	// Two library entries would both be called "text.db": a file-name match is
	// only allowed when it is unique on both sides.
	dir := t.TempDir()
	for _, n := range []string{"one", "two"} {
		sub := filepath.Join(dir, n)
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	s.reg.mu.Lock()
	s.reg.entries = []*entry{
		{ID: "aaaaaaaaaaaa", Path: filepath.Join(dir, "one", "text.db")},
		{ID: "bbbbbbbbbbbb", Path: filepath.Join(dir, "two", "text.db")},
	}
	s.reg.byID = map[string]*entry{}
	for _, e := range s.reg.entries {
		s.reg.byID[e.ID] = e
	}
	s.reg.mu.Unlock()

	p := LoadPrefs("")
	if err := p.Replace([]DictPref{{ID: "ccccccccccccc", Path: "/gone/three/text.db", Off: true}}); err != nil {
		t.Fatal(err)
	}
	got := p.heal(s.reg)
	if got[0].ID != "ccccccccccccc" {
		t.Errorf("an ambiguous file name was matched anyway: %+v", got[0])
	}
}

func TestPrefsMalformedFileIsIgnored(t *testing.T) {
	state := filepath.Join(t.TempDir(), StateFile)
	if err := os.WriteFile(state, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := LoadPrefs(state)
	if _, exists := p.Snapshot(); exists {
		t.Error("a malformed file must not be reported as usable state")
	}
	if p.Off("whatever", "/some/path") {
		t.Error("a malformed file must not disable anything")
	}
}

func TestPrefsInMemoryWithoutAPath(t *testing.T) {
	p := LoadPrefs("")
	if err := p.Replace([]DictPref{{ID: "x", Path: "/a/b.mdx", Off: true}}); err != nil {
		t.Fatalf("saving without a file must succeed silently: %v", err)
	}
	if !p.Off("x", "/a/b.mdx") {
		t.Error("in-memory state did not stick")
	}
}
