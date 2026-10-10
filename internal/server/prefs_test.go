package server

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
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
	Exists  bool       `json:"exists"`
	Version int        `json:"version"`
	Dicts   []DictPref `json:"dicts"`
	UI      *UIPrefs   `json:"ui"`
}

func putPrefs(t *testing.T, s *Server, body string) prefsResp {
	t.Helper()
	req := newRequest("PUT", "/api/prefs", strings.NewReader(body))
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
	i := slices.IndexFunc(got.Dicts, func(d DictPref) bool { return d.ID == "deadbeef0000" })
	if i < 0 || got.Dicts[i].Path != "/Volumes/Stick/Big.mdx" || !got.Dicts[i].Off {
		t.Errorf("retained record was altered: %+v", got.Dicts)
	}
}

// A pinned record the page could not send keeps its rank among the pinned,
// and its pin: a pinned dictionary on a drive that is away is back at its
// rank when the drive is (D166). An unpinned one has no rank, and goes last.
func TestPrefsMergeKeepsUnseenInPlace(t *testing.T) {
	r := &Registry{}
	for _, id := range []string{"a", "b", "c"} {
		r.entries = append(r.entries, &entry{ID: id, Path: "/d/" + id + ".dsl"})
	}
	r.byID = map[string]*entry{}
	for _, e := range r.entries {
		r.byID[e.ID] = e
	}
	ids := func(ds []DictPref) []string {
		var out []string
		for _, d := range ds {
			out = append(out, d.ID)
		}
		return out
	}
	for _, tc := range []struct {
		name   string
		stored []DictPref
		sent   []DictPref
		want   []string
	}{{
		name:   "between two sent records",
		stored: []DictPref{{ID: "a", Pin: true}, {ID: "x", Pin: true}, {ID: "b", Pin: true}, {ID: "c"}},
		sent:   []DictPref{{ID: "a", Pin: true}, {ID: "b", Pin: true}, {ID: "c"}},
		want:   []string{"a", "x", "b", "c"},
	}, {
		name:   "first of all",
		stored: []DictPref{{ID: "x", Pin: true}, {ID: "a", Pin: true}, {ID: "b"}},
		sent:   []DictPref{{ID: "a", Pin: true}, {ID: "b"}},
		want:   []string{"x", "a", "b"},
	}, {
		name:   "follows its neighbour when the neighbour moves",
		stored: []DictPref{{ID: "a", Pin: true}, {ID: "x", Pin: true}, {ID: "y"}, {ID: "b"}},
		sent:   []DictPref{{ID: "b", Pin: true}, {ID: "a", Pin: true}},
		want:   []string{"b", "a", "x", "y"},
	}, {
		name:   "a record sent under its new id anchors the next",
		stored: []DictPref{{ID: "old", Path: "/d/a.dsl", Pin: true}, {ID: "x", Pin: true}, {ID: "b"}},
		sent:   []DictPref{{ID: "b"}, {ID: "a", Pin: true}},
		want:   []string{"b", "a", "x"},
	}, {
		name:   "a neighbour the page unpinned anchors nothing",
		stored: []DictPref{{ID: "a", Pin: true}, {ID: "x", Pin: true}, {ID: "b", Pin: true}, {ID: "c"}},
		sent:   []DictPref{{ID: "b", Pin: true}, {ID: "c"}, {ID: "a"}},
		want:   []string{"x", "b", "c", "a"},
	}, {
		name:   "an unpinned record goes last",
		stored: []DictPref{{ID: "y"}, {ID: "a", Pin: true}, {ID: "b"}},
		sent:   []DictPref{{ID: "a", Pin: true}, {ID: "b"}},
		want:   []string{"a", "b", "y"},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			p := LoadPrefs("")
			if err := p.Replace(tc.stored); err != nil {
				t.Fatal(err)
			}
			got := p.merge(r, tc.sent)
			if !slices.Equal(ids(got), tc.want) {
				t.Fatalf("order %v, want %v", ids(got), tc.want)
			}
			if x := slices.IndexFunc(got, func(d DictPref) bool { return d.ID == "x" }); x >= 0 && !got[x].Pin {
				t.Error("the retained record lost its pin")
			}
		})
	}

	// A page working from a list it cached may send a dictionary the
	// registry no longer lists: its stored path is kept, not blanked.
	p := LoadPrefs("")
	if err := p.Replace([]DictPref{{ID: "gone", Path: "/away/gone.dsl", Pin: true}}); err != nil {
		t.Fatal(err)
	}
	if got := p.merge(r, []DictPref{{ID: "gone", Pin: true}}); len(got) != 1 || got[0].Path != "/away/gone.dsl" {
		t.Errorf("the stored path was lost: %+v", got)
	}
}

// The version tells the page whether the list is still in the order-only
// format of version 1 (D166). Only a list the page sent moves it: a repair
// the server makes itself, or a save of UI settings alone, leaves an old list
// marked old, or the page would never convert it.
func TestPrefsVersionMovesWithThePagesList(t *testing.T) {
	s, state := newPrefsServer(t)
	live := s.reg.all()[0]
	old := `{"version":1,"dicts":[{"id":"0000stale000","path":"` +
		filepath.ToSlash(filepath.Join("/elsewhere", filepath.Base(live.Path))) + `","name":"Alpha"}]}`
	if err := os.WriteFile(state, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	s.reg.prefs = LoadPrefs(state)

	var got prefsResp
	getJSON(t, s, "/api/prefs", &got) // heals: the record is re-attached and written
	if len(got.Dicts) != 1 || got.Dicts[0].ID != live.ID {
		t.Fatalf("not healed: %+v", got.Dicts)
	}
	if got.Version != 1 {
		t.Fatalf("version %d after a repair, want 1", got.Version)
	}
	putPrefs(t, s, `{"ui":{"fontSize":20}}`)
	getJSON(t, s, "/api/prefs", &got)
	if got.Version != 1 {
		t.Fatalf("version %d after a UI-only save, want 1", got.Version)
	}
	putPrefs(t, s, `{"dicts":[{"id":"`+live.ID+`","pin":true}]}`)
	getJSON(t, s, "/api/prefs", &got)
	if got.Version != prefsVersion || !got.Dicts[0].Pin {
		t.Fatalf("version %d pin %v after the page's list, want %d and true", got.Version, got.Dicts[0].Pin, prefsVersion)
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

// The two settings share one file and one write, but not one request: the
// client that reorders dictionaries and the client that resizes text are the
// same page sending different bodies, and neither may erase the other.
func TestPrefsUI(t *testing.T) {
	s, state := newPrefsServer(t)
	all := s.reg.all()
	if len(all) != 2 {
		t.Fatalf("want 2 dictionaries, got %d", len(all))
	}
	a, b := all[0].ID, all[1].ID
	order := `{"dicts":[{"id":"` + b + `"},{"id":"` + a + `"}]}`

	for _, tc := range []struct {
		name string
		body string
		want int // 0 = no ui record at all
	}{
		{"set", `{"ui":{"fontSize":24}}`, 24},
		{"dicts alone leave it", order, 24},
		{"an empty patch leaves it", `{"ui":{}}`, 24},
		{"null leaves it", `{"ui":null}`, 24},
		{"over the ceiling clamps", `{"ui":{"fontSize":900}}`, FontSizeMax},
		{"under the floor clamps", `{"ui":{"fontSize":1}}`, FontSizeMin},
		{"zero is unset", `{"ui":{"fontSize":0}}`, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := putPrefs(t, s, tc.body); got.UI.size() != tc.want {
				t.Fatalf("PUT echoed %d, want %d", got.UI.size(), tc.want)
			}
			var back prefsResp
			getJSON(t, s, "/api/prefs", &back)
			if back.UI.size() != tc.want {
				t.Fatalf("GET returned %d, want %d", back.UI.size(), tc.want)
			}
			// and it is on disk, not merely in memory
			if got := LoadPrefs(state).UI().size(); got != tc.want {
				t.Fatalf("reloaded %d, want %d", got, tc.want)
			}
		})
	}
	// the reorder above must have survived every ui-only write since
	var back prefsResp
	getJSON(t, s, "/api/prefs", &back)
	if len(back.Dicts) != 2 || back.Dicts[0].ID != b {
		t.Fatalf("ui writes disturbed the dictionary order: %+v", back.Dicts)
	}
}

// Every UI boolean has the standing default as its ZERO value - two of them by
// negating the feature (hlOff, fastFirst), the third by naming the non-default
// view (full) - so what is checked here is that "absent means the default"
// survives a round trip through the file for each of them, that setting one
// never silently clears another or the font size beside them, and that only a
// key sent as false clears one.
func TestPrefsUIFlags(t *testing.T) {
	s, state := newPrefsServer(t)

	for _, tc := range []struct {
		name            string
		body            string
		hlOff, wantFast bool
		wantFull        bool
	}{
		{"absent is the default", `{"ui":{"fontSize":24}}`, false, false, false},
		{"fastest on", `{"ui":{"fontSize":24,"fastFirst":true}}`, false, true, false},
		{"and highlighting off beside it", `{"ui":{"fontSize":24,"hlOff":true,"fastFirst":true}}`, true, true, false},
		{"back to my order, highlighting still off", `{"ui":{"fastFirst":false}}`, true, false, false},
		{"the full view, nothing else moved", `{"ui":{"full":true}}`, true, false, true},
		{"all three at once", `{"ui":{"hlOff":true,"fastFirst":true,"full":true}}`, true, true, true},
		{"an empty patch keeps them all", `{"ui":{}}`, true, true, true},
		{"false clears them", `{"ui":{"hlOff":false,"fastFirst":false,"full":false}}`, false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := putPrefs(t, s, tc.body)
			if hl, ff := got.UI.flags(); hl != tc.hlOff || ff != tc.wantFast || got.UI.full() != tc.wantFull {
				t.Fatalf("PUT echoed hlOff=%v fastFirst=%v full=%v, want %v/%v/%v",
					hl, ff, got.UI.full(), tc.hlOff, tc.wantFast, tc.wantFull)
			}
			var back prefsResp
			getJSON(t, s, "/api/prefs", &back)
			if hl, ff := back.UI.flags(); hl != tc.hlOff || ff != tc.wantFast || back.UI.full() != tc.wantFull {
				t.Fatalf("GET returned hlOff=%v fastFirst=%v full=%v, want %v/%v/%v",
					hl, ff, back.UI.full(), tc.hlOff, tc.wantFast, tc.wantFull)
			}
			// and it is on disk, not merely in memory
			ui := LoadPrefs(state).UI()
			if hl, ff := ui.flags(); hl != tc.hlOff || ff != tc.wantFast || ui.full() != tc.wantFull {
				t.Fatalf("reloaded hlOff=%v fastFirst=%v full=%v, want %v/%v/%v",
					hl, ff, ui.full(), tc.hlOff, tc.wantFast, tc.wantFull)
			}
		})
	}

	// A dictionary-only write must not reach into the ui record: the page that
	// reorders the list and the page that picks a strategy send different
	// bodies, and neither may erase the other.
	putPrefs(t, s, `{"ui":{"fastFirst":true}}`)
	putPrefs(t, s, `{"dicts":[{"id":"`+s.reg.all()[0].ID+`"}]}`)
	if _, ff := LoadPrefs(state).UI().flags(); !ff {
		t.Fatal("a dicts-only write cleared fastFirst")
	}
}

// speakOff (D148) joins the same record under the same rule: absent is "on",
// and it neither clears nor is cleared by the flags beside it.
func TestPrefsSpeakOff(t *testing.T) {
	s, state := newPrefsServer(t)
	for _, tc := range []struct {
		name, body string
		want, hl   bool
	}{
		{"absent is on", `{"ui":{"fontSize":24}}`, false, false},
		{"off", `{"ui":{"speakOff":true}}`, true, false},
		{"off beside highlighting off", `{"ui":{"speakOff":true,"hlOff":true}}`, true, true},
		{"back on, highlighting still off", `{"ui":{"speakOff":false}}`, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			putPrefs(t, s, tc.body)
			ui := LoadPrefs(state).UI()
			if hl, _ := ui.flags(); ui.spoken() != tc.want || hl != tc.hl {
				t.Fatalf("reloaded speakOff=%v hlOff=%v, want %v/%v", ui.spoken(), hl, tc.want, tc.hl)
			}
		})
	}
}

// groupsOff (D149) is a set of opaque facet ids: bounded, de-duplicated and
// sorted on the way in, left alone by a write that does not mention it, and
// cleared only by one that sends it empty.
func TestPrefsGroupsOff(t *testing.T) {
	s, state := newPrefsServer(t)
	long := strings.Repeat("x", 65)
	for _, tc := range []struct {
		name, body string
		want       []string
	}{
		{"absent shows everything", `{"ui":{"fontSize":24}}`, nil},
		{"hidden, cleaned", `{"ui":{"groupsOff":["publisher"," my groups ","publisher","","` + long + `"]}}`, []string{"my groups", "publisher"}},
		{"a dicts-only write keeps it", `{"dicts":[]}`, []string{"my groups", "publisher"}},
		{"empty shows everything again", `{"ui":{"groupsOff":[]}}`, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			putPrefs(t, s, tc.body)
			var got []string
			if ui := LoadPrefs(state).UI(); ui != nil {
				got = ui.GroupsOff
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("groupsOff = %q, want %q", got, tc.want)
			}
		})
	}
}

// full (the brief/full view of wu-sec) is spelled positively, so absent is
// Lingvo's brief view; it survives the file and the flags beside it.
func TestPrefsFull(t *testing.T) {
	s, state := newPrefsServer(t)
	for _, tc := range []struct {
		name, body string
		want, hl   bool
	}{
		{"absent is brief", `{"ui":{"fontSize":24}}`, false, false},
		{"full", `{"ui":{"full":true}}`, true, false},
		{"full beside highlighting off", `{"ui":{"full":true,"hlOff":true}}`, true, true},
		{"brief again, highlighting still off", `{"ui":{"full":false}}`, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := putPrefs(t, s, tc.body)
			if got.UI.full() != tc.want {
				t.Fatalf("PUT echoed full=%v, want %v", got.UI.full(), tc.want)
			}
			ui := LoadPrefs(state).UI()
			if hl, _ := ui.flags(); ui.full() != tc.want || hl != tc.hl {
				t.Fatalf("reloaded full=%v hlOff=%v, want %v/%v", ui.full(), hl, tc.want, tc.hl)
			}
		})
	}
}

// mode is the reader's last picked search mode. Prefix is the default and is
// stored as absent; a mode the page does not have is dropped, not kept.
func TestPrefsMode(t *testing.T) {
	s, state := newPrefsServer(t)
	for _, tc := range []struct{ name, body, want string }{
		{"absent is prefix", `{"ui":{"fontSize":24}}`, ""},
		{"full-text", `{"ui":{"mode":"fts"}}`, "fts"},
		{"a patch without it keeps it", `{"ui":{"full":true}}`, "fts"},
		{"exact", `{"ui":{"mode":"exact"}}`, "exact"},
		{"contains", `{"ui":{"mode":"contains"}}`, "contains"},
		{"prefix is stored as absent", `{"ui":{"mode":"prefix"}}`, ""},
		{"a retired mode is dropped", `{"ui":{"mode":"fuzzy"}}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			putPrefs(t, s, tc.body)
			ui := LoadPrefs(state).UI()
			if ui == nil || ui.Mode != tc.want {
				t.Fatalf("mode = %+v, want %q", ui, tc.want)
			}
		})
	}
	if raw, _ := os.ReadFile(state); strings.Contains(string(raw), `"mode"`) {
		t.Fatalf("prefix was written to the file:\n%s", raw)
	}
}

// openN and openAll set how many sections open by themselves. One is the
// default and is stored as absent; there is no ceiling.
func TestPrefsOpenCount(t *testing.T) {
	s, state := newPrefsServer(t)
	for _, tc := range []struct {
		name, body string
		n          int
		all, fast  bool
	}{
		{"absent is one", `{"ui":{"fontSize":24}}`, 0, false, false},
		{"three", `{"ui":{"openN":3}}`, 3, false, false},
		{"no ceiling", `{"ui":{"openN":40}}`, 40, false, false},
		{"one is stored as absent", `{"ui":{"openN":1}}`, 0, false, false},
		{"below one is one", `{"ui":{"openN":-2}}`, 0, false, false},
		{"all, the count kept beside it", `{"ui":{"openN":3,"openAll":true}}`, 3, true, false},
		{"the menu's Fastest clears both", `{"ui":{"fastFirst":true,"openN":0,"openAll":false}}`, 0, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			putPrefs(t, s, tc.body)
			ui := LoadPrefs(state).UI()
			if ui == nil || ui.OpenN != tc.n || ui.OpenAll != tc.all || ui.FastFirst != tc.fast {
				t.Fatalf("got %+v, want openN=%d openAll=%v fastFirst=%v", ui, tc.n, tc.all, tc.fast)
			}
		})
	}
}

// A UI patch changes the keys it names and nothing else. The page that sends
// it may have loaded long before another page changed a setting it does not
// mention (two tabs; the Android app and its lookup popup), and that newer
// choice must survive.
func TestPrefsUIPatch(t *testing.T) {
	s, state := newPrefsServer(t)
	putPrefs(t, s, `{"ui":{"fontSize":20,"full":true,"groupsOff":["lang","pair"]}}`)
	putPrefs(t, s, `{"ui":{"fontSize":22}}`) // the other page: text size only
	ui := LoadPrefs(state).UI()
	if ui == nil || ui.FontSize != 22 || !ui.Full || !slices.Equal(ui.GroupsOff, []string{"lang", "pair"}) {
		t.Fatalf("a one-key patch disturbed the others: %+v", ui)
	}

	// json decodes an array into the slice it finds; the patch must not
	// write into the record the running app still holds
	held := s.reg.prefs.UI().GroupsOff
	putPrefs(t, s, `{"ui":{"groupsOff":["zz"]}}`)
	if !slices.Equal(held, []string{"lang", "pair"}) {
		t.Fatalf("the patch wrote into the held record: %q", held)
	}
	if got := s.reg.prefs.UI().GroupsOff; !slices.Equal(got, []string{"zz"}) {
		t.Fatalf("groupsOff = %q, want [zz]", got)
	}
}

// A request without dicts keeps the stored order and enabled set: the page
// that changed only a UI setting may hold a stale copy of the list.
func TestPrefsAbsentDictsKeepList(t *testing.T) {
	s, state := newPrefsServer(t)
	ids := s.reg.all()
	a, b := ids[0].ID, ids[1].ID
	putPrefs(t, s, `{"dicts":[{"id":"`+b+`"},{"id":"`+a+`","off":true}]}`)
	for _, body := range []string{`{"ui":{"fontSize":20}}`, `{"dicts":null,"ui":{"full":true}}`} {
		got := putPrefs(t, s, body)
		if len(got.Dicts) != 2 || got.Dicts[0].ID != b || !got.Dicts[1].Off {
			t.Fatalf("%s: echoed list %+v, want b then a (off)", body, got.Dicts)
		}
		stored, _ := LoadPrefs(state).Snapshot()
		if len(stored) != 2 || stored[0].ID != b || !stored[1].Off {
			t.Fatalf("%s: stored list %+v, want b then a (off)", body, stored)
		}
	}
	if ui := LoadPrefs(state).UI(); ui == nil || ui.FontSize != 20 || !ui.Full {
		t.Fatalf("the UI patches were lost: %+v", ui)
	}
}

// A wrongly typed UI field is the caller's mistake, and nothing is written.
func TestPrefsUIBadField(t *testing.T) {
	s, state := newPrefsServer(t)
	putPrefs(t, s, `{"ui":{"fontSize":20}}`)
	req := newRequest("PUT", "/api/prefs", strings.NewReader(`{"ui":{"fontSize":"big"}}`))
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != 400 {
		t.Fatalf("status %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if ui := LoadPrefs(state).UI(); ui == nil || ui.FontSize != 20 {
		t.Fatalf("a refused patch changed the file: %+v", ui)
	}
}

func (u *UIPrefs) full() bool {
	if u == nil {
		return false
	}
	return u.Full
}

func (u *UIPrefs) spoken() bool {
	if u == nil {
		return false
	}
	return u.SpeakOff
}

func (u *UIPrefs) size() int {
	if u == nil {
		return 0
	}
	return u.FontSize
}

func (u *UIPrefs) flags() (hlOff, fastFirst bool) {
	if u == nil {
		return false, false
	}
	return u.HLOff, u.FastFirst
}

// The file-name rung of heal's identity ladder, from both sides. It is the
// weakest rung and the only one that can guess wrong, so it fires only when
// the name names exactly one thing in the registry AND exactly one thing in
// the stored records. On the stored side, a library folder the user removes
// leaves a record behind (kept on purpose - an
// unplugged drive looks the same), every one of those records has a path
// ending "text.db", and with one prepared dictionary left the dead record
// would otherwise adopt the live one, taking its off switch and its place in
// the order with it.
func TestPrefsFileNameRungNeedsBothSidesUnique(t *testing.T) {
	const live0, live1 = "live00000000", "live11111111"
	lib := func(name string) string { return filepath.Join("/lib", name, "text.db") }

	tests := []struct {
		name   string
		paths  []string   // dictionaries the registry has right now
		stored []DictPref // what state.json remembers
		want   []string   // id each record must hold afterwards
	}{{
		name:   "unique on both sides re-attaches",
		paths:  []string{lib("one")},
		stored: []DictPref{{ID: "stale0000000", Path: lib("gone"), Off: true}},
		want:   []string{live0},
	}, {
		name:   "ambiguous in the registry is not guessed",
		paths:  []string{lib("one"), lib("two")},
		stored: []DictPref{{ID: "stale0000000", Path: lib("gone"), Off: true}},
		want:   []string{"stale0000000"},
	}, {
		name:   "ambiguous in the stored records is not guessed",
		paths:  []string{lib("one")},
		stored: []DictPref{{ID: "stale0000000", Path: lib("gone")}, {ID: "stale1111111", Path: lib("alsogone")}},
		want:   []string{"stale0000000", "stale1111111"},
	}, {
		name:  "an exact path still wins over the collision",
		paths: []string{lib("one")},
		stored: []DictPref{
			{ID: "stale0000000", Path: lib("gone")},
			{ID: "stale1111111", Path: lib("one"), Off: true},
		},
		want: []string{"stale0000000", live0},
	}, {
		name:   "distinct file names are unaffected",
		paths:  []string{"/dicts/alpha.dsl", "/dicts/beta.dsl"},
		stored: []DictPref{{ID: "stale0000000", Path: "/elsewhere/beta.dsl", Off: true}},
		want:   []string{live1},
	}}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ids := []string{live0, live1}
			r := &Registry{}
			for i, p := range tc.paths {
				r.entries = append(r.entries, &entry{ID: ids[i], Path: p})
			}
			p := LoadPrefs("") // in-memory: this rung reads nothing from disk
			if err := p.Replace(tc.stored); err != nil {
				t.Fatal(err)
			}
			got := p.heal(r)
			if len(got) != len(tc.want) {
				t.Fatalf("records lost or invented: %+v", got)
			}
			for i, want := range tc.want {
				if got[i].ID != want {
					t.Errorf("record %d: id %q, want %q (%+v)", i, got[i].ID, want, got[i])
				}
			}
			// Re-attaching must carry the setting across, never reset it.
			for i, d := range got {
				if d.Off != tc.stored[i].Off {
					t.Errorf("record %d: off %v, want %v", i, d.Off, tc.stored[i].Off)
				}
			}
		})
	}
}

// A JSON body over the limit is 413, a malformed one 400, and both answer in
// the API's JSON error shape (decodeJSON).
func TestDecodeJSONStatus(t *testing.T) {
	s, _ := newPrefsServer(t)
	for _, tc := range []struct {
		name string
		body string
		code int
	}{
		{"malformed", "{", 400},
		{"over the limit", `{"dicts":[` + strings.Repeat(`{"id":"x"},`, 120_000) + `{"id":"x"}]}`, 413},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			s.ServeHTTP(rec, newRequest("PUT", "/api/prefs", strings.NewReader(tc.body)))
			if rec.Code != tc.code || !strings.Contains(rec.Body.String(), `"error"`) {
				t.Errorf("code %d body %.80s, want %d with a JSON error", rec.Code, rec.Body.String(), tc.code)
			}
		})
	}
}

// A hand edit to state.json made while the app runs is adopted, and the next
// save builds on it instead of writing it away: a save that does not mention
// the text size keeps the size the user just typed into the file.
func TestPrefsHandEditSurvivesASave(t *testing.T) {
	s, state := newPrefsServer(t)
	putPrefs(t, s, `{"dicts":[],"ui":{"fontSize":14}}`)
	if err := os.WriteFile(state, []byte(`{"version":1,"ui":{"fontSize":21},"dicts":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	putPrefs(t, s, `{"dicts":[]}`) // the list only
	if ui := LoadPrefs(state).UI(); ui == nil || ui.FontSize != 21 {
		t.Errorf("the hand-edited size was written away: %+v", ui)
	}
	// and it is what the app now reports, not only what the file holds
	s.reg.prefs.file.mu.Lock()
	s.reg.prefs.file.checked = time.Time{}
	s.reg.prefs.file.mu.Unlock()
	if ui := s.reg.prefs.UI(); ui == nil || ui.FontSize != 21 {
		t.Errorf("the running app does not see the hand edit: %+v", ui)
	}
}
