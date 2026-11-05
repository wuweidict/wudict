// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/wuweidict/wudict/internal/dict"
	"github.com/wuweidict/wudict/internal/fsx"
)

// prepare ingests a minimal real dictionary into a library folder for srcPath.
func prepare(t *testing.T, srcPath, name string) string {
	t.Helper()
	dir, err := ClaimDir(srcPath)
	if err != nil {
		t.Fatal(err)
	}
	r := &fakeReader{
		meta:    dict.Meta{Name: name, Format: "mdx", Path: srcPath},
		entries: []dict.Entry{h("a", "<p>x</p>")},
	}
	if err := ingestFull(r, TextDBPath(dir)); err != nil {
		t.Fatal(err)
	}
	return dir
}

// writeSrc creates a stand-in source file.
func writeSrc(t *testing.T, path, content string) string {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestFolderName(t *testing.T) {
	cases := map[string]string{
		"/d/AHD5-2017.slob":        "AHD5-2017",
		"/d/Oxford Advanced.mdx":   "Oxford Advanced",
		"/d/big.dsl.dz":            "big",
		"/d/we:ird/na*me.ifo":      "na-me",
		"/d/trailing. ":            "trailing",
		"/d/.mdx":                  "dictionary",
		"/d/Glossary.wudict.md":    "Glossary",
		"/d/notes.WUDICT.md":       "notes",
		"/d/plain.md":              "plain",
		"/d/.wudict.md":            "dictionary",
		"/d/Glossary.wudict.md.gz": "Glossary",
		"/d/Glossary.wudict.md.dz": "Glossary",
		"/d/notes.md.gz":           "notes",
	}
	for in, want := range cases {
		if got := FolderName(in); got != want {
			t.Errorf("FolderName(%q) = %q, want %q", in, got, want)
		}
	}
	if got := FolderName("/d/" + strings.Repeat("x", 200) + ".mdx"); len(got) > 80 {
		t.Errorf("FolderName did not cap length: %d", len(got))
	}
	// Cyrillic is two bytes a letter, so byte 80 can fall inside one
	long := "/d/x" + strings.Repeat("ж", 60) + ".dsl"
	if got := FolderName(long); len(got) > 80 || !utf8.ValidString(got) {
		t.Errorf("FolderName(%q) = %q (%d bytes, valid UTF-8 %v)", long, got, len(got), utf8.ValidString(got))
	}
}

// A folder on disk under the byte-cut spelling of a long name (byte 80 inside
// a character) is still the source's: found, re-claimed, never doubled.
func TestByteCutFolderStillFound(t *testing.T) {
	db := t.TempDir()
	t.Setenv("WUDICT_DB_DIR", db)
	src := writeSrc(t, filepath.Join(t.TempDir(), "x"+strings.Repeat("ж", 60)+".mdx"), "m")
	old := byteCutName(src)
	if old == "" || old == FolderName(src) {
		t.Fatalf("byteCutName(%q) = %q: the test name must split a character", src, old)
	}
	dir := filepath.Join(db, old)
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Skipf("this filesystem refuses a name that is not UTF-8: %v", err)
	}
	if err := writeClaim(dir, src); err != nil {
		t.Fatal(err)
	}
	r := &fakeReader{meta: dict.Meta{Name: "Long", Format: "mdx", Path: src},
		entries: []dict.Entry{h("a", "<p>x</p>")}}
	if err := ingestFull(r, TextDBPath(dir)); err != nil {
		t.Fatal(err)
	}
	if got, ok := LookupDir(src); !ok || got != dir {
		t.Errorf("LookupDir = %q,%v; want %q", got, ok, dir)
	}
	if got, err := ClaimDir(src); err != nil || got != dir {
		t.Errorf("ClaimDir = %q,%v; want %q", got, err, dir)
	}
	if des, _ := os.ReadDir(db); len(des) != 1 {
		t.Errorf("library holds %d folders, want the one", len(des))
	}
	if byteCutName(filepath.Join(filepath.Dir(src), "short.mdx")) != "" {
		t.Error("byteCutName of a short name must be empty")
	}
}

// Same file name, different formats - the case that exists in real libraries
// (AHD5-2017.slob and AHD5-2017.mdx). Each must own its own folder, and each
// must keep resolving to its own folder afterwards.
func TestClaimDirCollision(t *testing.T) {
	db := t.TempDir()
	t.Setenv("WUDICT_DB_DIR", db)
	srcDir := t.TempDir()
	slob := writeSrc(t, filepath.Join(srcDir, "AHD5-2017.slob"), "s")
	mdx := writeSrc(t, filepath.Join(srcDir, "AHD5-2017.mdx"), "m")

	d1 := prepare(t, slob, "AHD5")
	d2 := prepare(t, mdx, "AHD5")
	if filepath.Base(d1) != "AHD5-2017" {
		t.Errorf("first claim = %q, want AHD5-2017", filepath.Base(d1))
	}
	if filepath.Base(d2) != "AHD5-2017 (mdx)" {
		t.Errorf("second claim = %q, want \"AHD5-2017 (mdx)\"", filepath.Base(d2))
	}
	// re-claims and lookups are stable and never cross over
	if again, _ := ClaimDir(slob); again != d1 {
		t.Errorf("re-claim moved folder: %s -> %s", d1, again)
	}
	if got, ok := LookupDir(mdx); !ok || got != d2 {
		t.Errorf("LookupDir(mdx) = %q,%v; want %q", got, ok, d2)
	}
	if got, ok := LookupDir(filepath.Join(srcDir, "never-prepared.mdx")); ok {
		t.Errorf("LookupDir returned %q for an unprepared source", got)
	}
	// looking up must not create anything
	des, _ := os.ReadDir(db)
	if len(des) != 2 {
		t.Errorf("library has %d folders, want 2 (lookups must not create)", len(des))
	}
}

func TestSourceChanged(t *testing.T) {
	t.Setenv("WUDICT_DB_DIR", t.TempDir())
	srcDir := t.TempDir()
	src := writeSrc(t, filepath.Join(srcDir, "d.mdx"), "first content")
	dir := prepare(t, src, "D")
	textDB := TextDBPath(dir)

	if SourceChanged(textDB, src) {
		t.Error("unchanged source reported as changed")
	}
	// a touch that does not alter content must not force a re-index
	future := time.Now().Add(2 * time.Second)
	_ = os.Chtimes(src, future, future)
	if SourceChanged(textDB, src) {
		t.Error("touched (same content) source reported as changed")
	}
	writeSrc(t, src, "second, different content")
	_ = os.Chtimes(src, future.Add(time.Second), future.Add(time.Second))
	if !SourceChanged(textDB, src) {
		t.Error("edited source not detected")
	}
	// a source that is simply gone is NOT "changed": the prepared folder
	// stands on its own (data-loss guard).
	if err := os.Remove(src); err != nil {
		t.Fatal(err)
	}
	if SourceChanged(textDB, src) {
		t.Error("missing source must not count as changed")
	}
}

func TestLibraryListingAndReceipt(t *testing.T) {
	db := t.TempDir()
	t.Setenv("WUDICT_DB_DIR", db)
	srcDir := t.TempDir()
	live := writeSrc(t, filepath.Join(srcDir, "live.mdx"), "x")
	prepare(t, live, "Live")
	prepare(t, filepath.Join(srcDir, "gone.mdx"), "Gone") // never created on disk

	lib, err := Library()
	if err != nil {
		t.Fatal(err)
	}
	if len(lib) != 2 {
		t.Fatalf("Library() = %d entries, want 2", len(lib))
	}
	byName := map[string]LibEntry{}
	for _, e := range lib {
		byName[e.Name] = e
	}
	if !byName["Live"].SourceExists {
		t.Error("Live: SourceExists should be true")
	}
	if byName["Gone"].SourceExists {
		t.Error("Gone: SourceExists should be false")
	}
	if byName["Live"].Entries != 1 || !byName["Live"].FullText {
		t.Errorf("Live entry flags wrong: %+v", byName["Live"])
	}

	// the receipt is written at ingest and describes the same source
	info, err := readInfo(InfoPath(byName["Live"].Dir))
	if err != nil {
		t.Fatal(err)
	}
	if info["source"] != live {
		t.Errorf("info.txt source = %q, want %q", info["source"], live)
	}
	if info["name"] != "Live" {
		t.Errorf("info.txt name = %q, want Live", info["name"])
	}
	// and it is the fast index behind LookupDir even without reading the db
	if got, ok := LookupDir(live); !ok || got != byName["Live"].Dir {
		t.Errorf("LookupDir(live) = %q,%v", got, ok)
	}
}

// A format reader may report a relative, stale or empty source path in its
// meta (several real ones do). The folder must still know what it was prepared
// from - the claim written at allocation time is the ownership record - or the
// prepared dictionary is orphaned from its source and silently rebuilt forever.
func TestOwnershipSurvivesBogusReaderPath(t *testing.T) {
	t.Setenv("WUDICT_DB_DIR", t.TempDir())
	srcDir := t.TempDir()
	src := writeSrc(t, filepath.Join(srcDir, "real.mdx"), "content")

	dir, err := ClaimDir(src)
	if err != nil {
		t.Fatal(err)
	}
	// reader reports a bare relative name instead of the real path
	r := &fakeReader{
		meta:    dict.Meta{Name: "Real", Format: "mdx", Path: "real.mdx"},
		entries: []dict.Entry{h("a", "<p>x</p>")},
	}
	if err := ingestFull(r, TextDBPath(dir)); err != nil {
		t.Fatal(err)
	}
	got, ok := PreparedFor(src)
	if !ok || got != TextDBPath(dir) {
		t.Fatalf("PreparedFor = %q,%v; want %q,true", got, ok, TextDBPath(dir))
	}
	info, err := readInfo(InfoPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if info["source"] != src {
		t.Errorf("receipt source = %q, want the claimed path %q", info["source"], src)
	}
}

func TestFindLeftoversSemantics(t *testing.T) {
	db := t.TempDir()
	t.Setenv("WUDICT_DB_DIR", db)
	srcDir := t.TempDir()

	// 1. prepared dictionary whose source vanished: MUST NOT be an orphan
	keep := prepare(t, filepath.Join(srcDir, "gone.mdx"), "Gone")
	// 2. prepared dictionary whose source changed: also NOT an orphan
	//    (re-indexing overwrites it in place; nothing is superseded)
	changing := writeSrc(t, filepath.Join(srcDir, "s.mdx"), "content")
	kept2 := prepare(t, changing, "S")
	writeSrc(t, changing, "different content entirely")
	// 3. folder with no text.db (interrupted claim / stray media.db)
	stray := filepath.Join(db, "stray")
	if err := os.MkdirAll(stray, 0o755); err != nil {
		t.Fatal(err)
	}
	writeSrc(t, filepath.Join(stray, MediaDBName), "m")
	// 4. leftovers from the old flat layout and an interrupted ingest
	writeSrc(t, filepath.Join(db, "old-aaaa.text.db"), "x")
	writeSrc(t, filepath.Join(db, "old-aaaa.media.db"), "x")
	writeSrc(t, filepath.Join(db, "text.db.ingest.abc123"), "x")
	// 5. an interrupted ingest INSIDE a healthy folder: since D20 this is the
	//    only place tempDBName can put one, so it is the case that matters.
	//    The folder itself stays; only the temp is removable.
	stale := writeSrc(t, filepath.Join(kept2, "text.db.ingest.deadbeef"), "partial")
	aged := time.Now().Add(-2 * ingestGrace)
	if err := os.Chtimes(stale, aged, aged); err != nil {
		t.Fatal(err)
	}
	// 6. an ingest that is running RIGHT NOW must survive: deleting it would
	//    destroy the work of a prepare happening in another window.
	writeSrc(t, filepath.Join(kept2, "text.db.ingest.beefdead"), "in progress")

	orphs, err := FindLeftovers()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, o := range orphs {
		got[filepath.Base(o.Path)] = o.Reason
	}
	if _, bad := got[filepath.Base(keep)]; bad {
		t.Fatal("DATA LOSS: prepared dictionary with a missing source must NOT be an orphan")
	}
	if _, bad := got[filepath.Base(kept2)]; bad {
		t.Fatal("DATA LOSS: prepared dictionary with a changed source must NOT be an orphan")
	}
	for _, want := range []string{"stray", "old-aaaa.text.db", "old-aaaa.media.db",
		"text.db.ingest.abc123", "text.db.ingest.deadbeef"} {
		if _, ok := got[want]; !ok {
			t.Errorf("%s should be an orphan (got %v)", want, got)
		}
	}
	if _, bad := got["text.db.ingest.beefdead"]; bad {
		t.Fatal("DATA LOSS: an ingest temp still being written must NOT be an orphan")
	}
}

// fakeReader replays a fixed entry list.
type fakeReader struct {
	meta    dict.Meta
	entries []dict.Entry
	pos     int
}

func (f *fakeReader) Meta() dict.Meta { return f.meta }
func (f *fakeReader) Close() error    { return nil }
func (f *fakeReader) Next() (dict.Entry, error) {
	if f.pos >= len(f.entries) {
		return dict.Entry{}, io.EOF
	}
	e := f.entries[f.pos]
	f.pos++
	return e, nil
}

func h(w, body string) dict.Entry {
	return dict.Entry{Headwords: []string{w}, Body: body, Kind: dict.BodyHTML}
}

// testHeader repeats a name on purpose: the header is a list, not a map.
var testHeader = []dict.Field{{Name: "Author", Value: "A. N. Other"}, {Name: "Note", Value: "x = y"}, {Name: "Note", Value: "<p>two</p>\nlines"}}

// The source header survives preparation, order and repeats included, so a
// folder whose source is gone still says who wrote it.
func TestMetaHeaderRoundTrip(t *testing.T) {
	if got := testStore(t).Meta().Header; !reflect.DeepEqual(got, testHeader) {
		t.Errorf("Header = %q, want %q", got, testHeader)
	}
}

func testStore(t *testing.T) *Store {
	t.Helper()
	r := &fakeReader{
		meta: dict.Meta{Name: "Test Diccionario", Format: "mdx", Path: "/nonexistent/test.mdx",
			Header: testHeader},
		entries: []dict.Entry{
			h("corazón", `<div class="x"><b>corazón</b> órgano <script>evil()</script>muscular</div>`),
			h("corazonada", `<p>presentimiento súbito</p>`),
			h("pregunta", `<p>petición de <a href="bword://información">información</a></p>`),
			h("Straße", `<p>calle en alemán</p>`),
			{Headwords: []string{"preguntar", "cuestionarse"}, Body: "<p>hacer preguntas</p>", Kind: dict.BodyHTML},
			{Headwords: []string{"interrogar"}, LinkTo: "preguntar"},
			{Headwords: []string{"huérfano"}, LinkTo: "no-such-target"},
			h("100% sure", `<p>plain text with 50%_ and_underscores</p>`),
		},
	}
	dbPath := filepath.Join(t.TempDir(), "test.text.db")
	// full text is opt-in; this fixture tests it, so it asks for it
	if _, err := IngestPlan(r, dbPath, Plan{FullText: true}, nil); err != nil {
		t.Fatal(err)
	}
	s, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// A path is not a URI: "#" and "%" are ordinary in a folder name ("C#",
// "100%"), and SQLite must open the file they name, not a truncated or decoded
// spelling of it.
func TestIngestPathWithURICharacters(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "C# 100%41 x")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	dbPath := TextDBPath(dir)
	r := &fakeReader{meta: dict.Meta{Name: "Sharp", Format: "mdx", Path: "/nonexistent/sharp.mdx"},
		entries: []dict.Entry{h("alpha", "<p>a</p>")}}
	if _, err := IngestPlan(r, dbPath, Plan{}, nil); err != nil {
		t.Fatal(err)
	}
	s, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.Meta().Name != "Sharp" {
		t.Errorf("name = %q", s.Meta().Name)
	}
	// nothing was written anywhere but the folder named
	ents, err := os.ReadDir(filepath.Dir(dir))
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 1 {
		var names []string
		for _, e := range ents {
			names = append(names, e.Name())
		}
		t.Errorf("files beside the folder: %q", names)
	}
}

func TestIngestAndMeta(t *testing.T) {
	s := testStore(t)
	m := s.Meta()
	if m.Name != "Test Diccionario" || m.Format != "wudict:mdx" {
		t.Errorf("bad meta: %+v", m)
	}
	if m.EntryCount != 6 { // 8 raw - 2 pure links
		t.Errorf("entry_count = %d, want 6", m.EntryCount)
	}
	if !s.Caps().Wildcard || !s.Caps().FTS {
		t.Error("store must advertise wildcard+fts")
	}
	if m := s.SourcePath(); m == "" {
		t.Error("source path not recorded")
	}
}

func TestExactAndAliases(t *testing.T) {
	s := testStore(t)
	res, err := s.Exact("corazón", 10)
	if err != nil || len(res) != 1 {
		t.Fatalf("Exact corazón: %v %v", res, err)
	}
	// alias via extra headword
	res, err = s.Exact("cuestionarse", 10)
	if err != nil || len(res) != 1 || res[0].Headword != "preguntar" {
		t.Fatalf("alias headword: %v %v", res, err)
	}
	// alias via @@@LINK redirect
	res, err = s.Exact("interrogar", 10)
	if err != nil || len(res) != 1 || res[0].Headword != "preguntar" {
		t.Fatalf("link alias: %v %v", res, err)
	}
	// NOCASE fallback
	res, err = s.Exact("STRASSE", 10)
	if err != nil {
		t.Fatal(err)
	}
	_ = res // ß vs SS is beyond NOCASE; just must not error
	res, err = s.Exact("straße", 10)
	if err != nil || len(res) != 1 {
		t.Fatalf("nocase: %v %v", res, err)
	}
}

func TestPrefix(t *testing.T) {
	s := testStore(t)
	res, err := s.Prefix("coraz", 10)
	if err != nil || len(res) != 2 {
		t.Fatalf("Prefix coraz: %d results, err=%v", len(res), err)
	}
	// LIKE wildcards in input must be literal (FTS-audit #5)
	res, err = s.Prefix("100%", 10)
	if err != nil || len(res) != 1 || res[0].Headword != "100% sure" {
		t.Fatalf("literal %%: %v %v", res, err)
	}
	if res, _ := s.Prefix("%", 10); len(res) != 0 {
		t.Errorf("bare %% must match nothing, got %d", len(res))
	}
}

// A headword typed in full must not hide the keys it is a strict prefix of:
// returning the exact article alone would answer a full, correctly-typed
// headword with one result and the same word mistyped with a double space
// (which misses the exact pass) with all of them.
func TestPrefixDoesNotStopAtExact(t *testing.T) {
	s := testStore(t)
	res, err := s.Prefix("pregunta", 10)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range res {
		got = append(got, r.Headword)
	}
	if len(got) != 2 || got[0] != "pregunta" || got[1] != "preguntar" {
		t.Fatalf("Prefix(pregunta) = %v, want [pregunta preguntar]", got)
	}
	// The exact match sorts first because a proper prefix precedes everything
	// it prefixes, so the limit cuts the siblings and never the word itself.
	if res, err := s.Prefix("pregunta", 1); err != nil || len(res) != 1 || res[0].Headword != "pregunta" {
		t.Fatalf("Prefix(pregunta, 1) = %v, %v", res, err)
	}
	// An alias that starts with the word reaches its entry the same way.
	if res, err := s.Prefix("cuestiona", 10); err != nil || len(res) != 1 || res[0].Headword != "preguntar" {
		t.Fatalf("Prefix(cuestiona) = %v, %v", res, err)
	}
}

// An accent typed that no headword has ranks; it excludes nothing: the
// folded reading still finds corazón and corazonada (D181).
func TestPrefixFoldedFallback(t *testing.T) {
	s := testStore(t)
	// "córaz" (extra accent) starts no headword as typed, but folds to the
	// same key as corazón/corazonada.
	res, err := s.Prefix("córaz", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 2 {
		t.Fatalf("folded prefix córaz: %d results, want 2", len(res))
	}
	// a genuinely absent prefix still returns nothing.
	if r, _ := s.Prefix("zzzq", 10); len(r) != 0 {
		t.Errorf("absent prefix must match nothing, got %d", len(r))
	}
}

// Accents are ignored by prefix search always, not only when nothing
// matches as typed, and the word typed in full comes first (D181).
func TestPrefixAccentInsensitive(t *testing.T) {
	s := testStore(t)
	res, err := s.Prefix("corazon", 10) // no accent
	if err != nil {
		t.Fatal(err)
	}
	if got := headwords(res); !slices.Equal(got, []string{"corazón", "corazonada"}) {
		t.Fatalf("Prefix(corazon) = %q, want the word itself first", got)
	}
	if res, err := s.Exact("corazon", 10); err != nil || len(res) != 1 || res[0].Headword != "corazón" {
		t.Fatalf("Exact(corazon) = %v, %v", res, err)
	}
}

// A substring anywhere in a headword is the wildcard `*x*` (D170).
func TestSubstringByWildcard(t *testing.T) {
	s := testStore(t)
	res, err := s.WildcardContext(bg, "*razon*", 10)
	if err != nil {
		t.Fatalf("*razon*: %v", err)
	}
	got := map[string]bool{}
	for _, r := range res {
		got[r.Headword] = true
	}
	if !got["corazón"] || !got["corazonada"] {
		t.Errorf("*razon* should match corazón/corazonada, got %v", res)
	}
	// accent-insensitive both ways, and no minimum length
	if r, _ := s.WildcardContext(bg, "*orazó*", 10); len(r) == 0 {
		t.Error("*orazó* should match corazón")
	}
	if r, _ := s.WildcardContext(bg, "*re*", 10); len(r) == 0 {
		t.Error("*re* should match pregunta")
	}
}

func TestFullText(t *testing.T) {
	s := testStore(t)
	res, err := s.FullTextContext(bg, "muscular", 10)
	if err != nil || len(res) != 1 || res[0].Headword != "corazón" {
		t.Fatalf("FullText muscular: %v %v", res, err)
	}
	// tag/script content must NOT be indexed (FTS-audit #1)
	if res, _ := s.FullTextContext(bg, "evil", 10); len(res) != 0 {
		t.Errorf("script content leaked into FTS: %v", res)
	}
	if res, _ := s.FullTextContext(bg, "div", 10); len(res) != 0 {
		t.Errorf("tag names leaked into FTS: %v", res)
	}
	// multi-token = implicit AND
	res, err = s.FullTextContext(bg, "órgano muscular", 10)
	if err != nil || len(res) != 1 {
		t.Fatalf("multi-token: %v %v", res, err)
	}
}

// FTS-audit #2: hostile MATCH input must not cause SQL/FTS syntax errors.
func TestHostileFtsInput(t *testing.T) {
	s := testStore(t)
	hostile := []string{
		`-corazón`, `cora*zon`, `^inicio`, `a:b`, `(paren`, `NEAR`, `OR`, `NOT x`,
		`"quoted"`, `""`, `co"ra`, "back\\slash", `emoji💡word`, `-`, `*`, `   `,
	}
	for _, q := range hostile {
		if _, err := s.PrefixContext(bg, q, 5); err != nil {
			t.Errorf("Prefix(%q) errored: %v", q, err)
		}
		if _, err := s.ExactContext(bg, q, 5); err != nil {
			t.Errorf("Exact(%q) errored: %v", q, err)
		}
		if _, err := s.WildcardContext(bg, q, 5); err != nil {
			t.Errorf("Wildcard(%q) errored: %v", q, err)
		}
		if _, err := s.FullTextContext(bg, q, 5); err != nil {
			t.Errorf("FullText(%q) errored: %v", q, err)
		}
	}
}

func TestClampAndKeywords(t *testing.T) {
	s := testStore(t)
	if _, err := s.Prefix("c", -5); err != nil {
		t.Errorf("negative limit: %v", err)
	}
	if _, err := s.FullTextContext(bg, "de", 1<<30); err != nil {
		t.Errorf("huge limit: %v", err)
	}
	keys := s.Keywords(0, 3)
	if len(keys) != 3 || keys[0] != "corazón" {
		t.Errorf("Keywords: %v", keys)
	}
}

func TestOpenRejectsForeignDB(t *testing.T) {
	if _, err := Open(filepath.Join(t.TempDir(), "missing.db")); err == nil {
		t.Error("missing db must error")
	}
}

func TestStripHTML(t *testing.T) {
	cases := map[string]string{
		`<p>a<br/>b</p>`:                             "a b",
		`<script>var x=1;</script>text`:              "text",
		`<style>.a{color:red}</style>word`:           "word",
		`a &amp; b &lt;c&gt;`:                        "a & b <c>",
		`<div class="k1 k2" style="x:y">inner</div>`: "inner",
		// A self-closing script has no contents and no closing tag: the skip
		// it opens has to end with the tag itself, or everything after it in
		// the article is silently missing from the full-text index.
		`<script src="x.js"/>after`:         "after",
		`<style/>after`:                     "after",
		`<script/>a<script>bad()</script>b`: "a b",
		// ...but a "/>" INSIDE script contents is script text, not the end of
		// the skip.
		`<script>f(a<b/>c)</script>tail`: "tail",
		``:                               "",
	}
	for in, want := range cases {
		if got := StripHTML(in); got != want {
			t.Errorf("StripHTML(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBuildMatch(t *testing.T) {
	if got := buildMatch(`he"llo world`, "w"); got != `w:"he""llo"* w:"world"*` {
		t.Errorf("buildMatch = %q", got)
	}
	if got := buildMatch("  ", ""); got != "" {
		t.Errorf("blank input: %q", got)
	}
}

// A text.db+media.db pair must work standalone (sources deleted).
func TestStandaloneMediaPairing(t *testing.T) {
	r := &fakeReader{meta: dict.Meta{Name: "m", Format: "test", Path: "/gone"},
		entries: []dict.Entry{h("w", "<p>x</p>")}}
	base := filepath.Join(t.TempDir(), "m")
	if err := ingestFull(r, base+".text.db"); err != nil {
		t.Fatal(err)
	}
	uuid, _ := ReadMetaValue(base+".text.db", "dict_uuid")
	src := &mediaSrc{}
	if err := IngestMedia(src, []string{"a.png"}, base+".media.db", uuid, nil); err != nil {
		t.Fatal(err)
	}
	s, err := Open(base + ".text.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	rc, mime, err := s.Resource("a.png")
	if err != nil || mime != "image/png" {
		t.Fatalf("standalone media: %v %q", err, mime)
	}
	rc.Close()
	if _, _, err := s.Resource("nope"); err != dict.ErrNotFound {
		t.Errorf("missing: %v", err)
	}
}

type mediaSrc struct{ dict.Dictionary }

func (mediaSrc) Meta() dict.Meta { return dict.Meta{Name: "m", Format: "test"} }
func (mediaSrc) Resource(string) (io.ReadCloser, string, error) {
	return io.NopCloser(strings.NewReader("PNG")), "image/png", nil
}

func TestHeadwordsOnlyLevel(t *testing.T) {
	r := &fakeReader{
		meta: dict.Meta{Name: "hw", Format: "test", Path: "/x"},
		entries: []dict.Entry{
			h("corazón", `<p>órgano muscular</p>`),
		},
	}
	dbPath := filepath.Join(t.TempDir(), "hw.text.db")
	if _, err := IngestPlan(r, dbPath, Plan{}, nil); err != nil {
		t.Fatal(err)
	}
	s, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	c := s.Caps()
	if c.FTS || !c.Wildcard {
		t.Errorf("headwords level: want fts=false wildcard=true, got %+v", c)
	}
	// accent-insensitive prefix works without the full-text index
	res, err := s.PrefixContext(bg, "corazon", 5)
	if err != nil || len(res) != 1 {
		t.Fatalf("prefix-fold: %v %v", res, err)
	}
	// full-text is honestly unsupported
	if _, err := s.FullTextContext(bg, "muscular", 5); err != dict.ErrUnsupported {
		t.Errorf("fulltext: want ErrUnsupported, got %v", err)
	}
	// the article body is not a headword
	if res, _ := s.PrefixContext(bg, "muscular", 5); len(res) != 0 {
		t.Errorf("body leaked into headword search: %v", res)
	}
}

func TestBodyTextNormalization(t *testing.T) {
	r := &fakeReader{
		meta: dict.Meta{Name: "plain", Format: "test", Path: "/x"},
		entries: []dict.Entry{
			{Headwords: []string{"w"}, Body: "line1 <b>\nline2", Kind: dict.BodyText},
		},
	}
	dbPath := filepath.Join(t.TempDir(), "p.text.db")
	if err := ingestFull(r, dbPath); err != nil {
		t.Fatal(err)
	}
	s, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	res, err := s.Exact("w", 1)
	if err != nil || len(res) != 1 {
		t.Fatal(res, err)
	}
	if !strings.Contains(res[0].Body, "&lt;b&gt;") || !strings.Contains(res[0].Body, "<br/>") {
		t.Errorf("BodyText not escaped/wrapped: %q", res[0].Body)
	}
}

// Databases prepared under the pre-folder layout must keep working: they are
// moved into folders (a rename, no re-index), never deleted, and never
// overwrite a folder that already holds the same dictionary.
func TestAdoptLoose(t *testing.T) {
	db := t.TempDir()
	t.Setenv("WUDICT_DB_DIR", db)
	srcDir := t.TempDir()
	src := writeSrc(t, filepath.Join(srcDir, "Espasa.mdx"), "content")

	// (1) a flat pair whose source is recorded and still present
	loose := filepath.Join(db, "Espasa-Calpe-1f5fa4b2.text.db")
	r := &fakeReader{
		meta:    dict.Meta{Name: "Espasa", Format: "mdx", Path: src},
		entries: []dict.Entry{h("a", "<p>x</p>")},
	}
	if err := ingestFull(r, loose); err != nil {
		t.Fatal(err)
	}
	looseMedia := strings.TrimSuffix(loose, ".text.db") + ".media.db"
	writeSrc(t, looseMedia, "MEDIA")
	// (2) a flat database with no recorded source: named after the file,
	//     minus the old content-hash suffix
	orphanSrc := ""
	noSrc := filepath.Join(db, "spa-cat-index-7d9963cd.text.db")
	r2 := &fakeReader{
		meta:    dict.Meta{Name: "Spa-Cat", Format: "mdx", Path: orphanSrc},
		entries: []dict.Entry{h("b", "<p>y</p>")},
	}
	if err := ingestFull(r2, noSrc); err != nil {
		t.Fatal(err)
	}

	moved, err := AdoptLoose()
	if err != nil {
		t.Fatal(err)
	}
	if len(moved) != 2 {
		t.Fatalf("adopted %d databases, want 2: %+v", len(moved), moved)
	}
	// the recorded-source one lands where a fresh ingest of that source would
	want := filepath.Join(db, "Espasa")
	if !fsx.FileExists(TextDBPath(want)) {
		t.Fatalf("expected %s/text.db after adoption", want)
	}
	if !fsx.FileExists(MediaDBPath(want)) {
		t.Error("the paired media.db should move with its dictionary")
	}
	if fsx.FileExists(loose) || fsx.FileExists(looseMedia) {
		t.Error("adoption should move the files, not copy them")
	}
	if got, ok := PreparedFor(src); !ok || got != TextDBPath(want) {
		t.Errorf("adopted dictionary not resolvable from its source: %q %v", got, ok)
	}
	if !fsx.FileExists(filepath.Join(db, "spa-cat-index", TextDBName)) {
		t.Error("a source-less database should be named after its file, hash suffix dropped")
	}

	lib, err := Library()
	if err != nil {
		t.Fatal(err)
	}
	if len(lib) != 2 {
		t.Fatalf("Library() lists %d after adoption, want 2", len(lib))
	}
	// idempotent
	again, err := AdoptLoose()
	if err != nil || len(again) != 0 {
		t.Fatalf("second AdoptLoose = %v, %v; want none", again, err)
	}
	// nothing is deletable
	orph, err := FindLeftovers()
	if err != nil {
		t.Fatal(err)
	}
	if len(orph) != 0 {
		t.Fatalf("adopted dictionaries must not be orphans: %+v", orph)
	}
}

// A loose database whose dictionary already has a prepared folder is a true
// duplicate: left in place by adoption, and only then reported as removable.
func TestAdoptLooseKeepsExistingFolder(t *testing.T) {
	db := t.TempDir()
	t.Setenv("WUDICT_DB_DIR", db)
	srcDir := t.TempDir()
	src := writeSrc(t, filepath.Join(srcDir, "Dup.mdx"), "content")
	dir := prepare(t, src, "Dup") // current-layout folder

	loose := filepath.Join(db, "Dup-11112222.text.db")
	r := &fakeReader{
		meta:    dict.Meta{Name: "Dup", Format: "mdx", Path: src},
		entries: []dict.Entry{h("a", "<p>x</p>")},
	}
	if err := ingestFull(r, loose); err != nil {
		t.Fatal(err)
	}
	moved, err := AdoptLoose()
	if err != nil {
		t.Fatal(err)
	}
	if len(moved) != 0 {
		t.Fatalf("must not adopt over an existing folder: %+v", moved)
	}
	if !fsx.FileExists(loose) {
		t.Fatal("the loose file must be left in place, not deleted")
	}
	if !fsx.FileExists(TextDBPath(dir)) {
		t.Fatal("the existing prepared folder must be untouched")
	}
	orph, err := FindLeftovers()
	if err != nil {
		t.Fatal(err)
	}
	if len(orph) != 1 || filepath.Base(orph[0].Path) != "Dup-11112222.text.db" {
		t.Fatalf("the duplicate should be the only removable item: %+v", orph)
	}
}

// End to end: the same dictionary ingested with every codec must answer every
// mode identically.
func TestSearchIdenticalWithAndWithoutCompression(t *testing.T) {
	t.Setenv("WUDICT_DB_DIR", t.TempDir())
	body := func(w string) string {
		return "<p>" + strings.Repeat(w+" definición de ejemplo con texto repetido. ", 12) + "</p>"
	}
	entries := []dict.Entry{
		h("corazón", body("corazón")), h("corazonada", body("corazonada")),
		h("casa", body("casa")), h("Straße", body("Straße")),
	}
	defer SetCodec(codecZstd, 1)
	codecs := []struct {
		name  string
		level int
	}{{codecZstd, 1}, {codecZstd, 3}, {codecDeflate, 6}, {codecNone, 0}}
	results := map[int][][]dict.Result{}
	for ci, c := range codecs {
		SetCodec(c.name, c.level)
		p := filepath.Join(t.TempDir(), "text.db")
		r := &fakeReader{meta: dict.Meta{Name: "C", Format: "mdx", Path: "/x.mdx"}, entries: append([]dict.Entry(nil), entries...)}
		if _, err := IngestPlan(r, p, Plan{FullText: true}, nil); err != nil {
			t.Fatal(err)
		}
		s, err := Open(p)
		if err != nil {
			t.Fatal(err)
		}
		var got [][]dict.Result
		for _, q := range []func() ([]dict.Result, error){
			func() ([]dict.Result, error) { return s.Exact("corazón", 10) },
			func() ([]dict.Result, error) { return s.Prefix("cora", 10) },
			func() ([]dict.Result, error) { return s.WildcardContext(bg, "*razon*", 10) },
			func() ([]dict.Result, error) { return s.FullTextContext(bg, "definición", 10) },
		} {
			res, err := q()
			if err != nil {
				t.Fatal(err)
			}
			got = append(got, res)
		}
		s.Close()
		results[ci] = got
	}
	for ci := 1; ci < len(codecs); ci++ {
		for i := range results[0] {
			a, b := results[0][i], results[ci][i]
			if len(a) != len(b) {
				t.Fatalf("query %d: %d results with zstd-1 vs %d with %v", i, len(a), len(b), codecs[ci])
			}
			for j := range a {
				if a[j].Headword != b[j].Headword || a[j].Body != b[j].Body {
					t.Fatalf("query %d result %d differs between zstd-1 and %v", i, j, codecs[ci])
				}
			}
			if len(a) == 0 {
				t.Errorf("query %d returned nothing - the comparison proves nothing", i)
			}
		}
	}
}

// MDict repacks store expandable sections as "@"-prefixed headwords - 59 % of
// LDOCE6 No-Voice is `@collocations_woman` and friends. They must be fetchable
// by exact lookup (that is how an article's link pulls one in) and invisible
// everywhere else, or a search for "woman" returns five sections before the
// word.
func TestSubEntriesHiddenFromBrowsing(t *testing.T) {
	t.Setenv("WUDICT_DB_DIR", t.TempDir())
	p := filepath.Join(t.TempDir(), "text.db")
	r := &fakeReader{
		meta: dict.Meta{Name: "L", Format: "mdx", Path: "/x.mdx"},
		entries: []dict.Entry{
			h("woman", "<p>an adult female person</p>"),
			h("womanhood", "<p>the state of being a woman</p>"),
			h("@examples_woman", "<p>EXAMPLES: women's clothes</p>"),
			h("@collocations_woman", "<p>COLLOCATIONS: a young woman</p>"),
			h("@entrymenu_chairwoman", "<p>MENU</p>"),
			h("@", "<p>the at sign</p>"), // a bare @ IS a real headword: keep it
		},
	}
	if _, err := IngestPlan(r, p, Plan{FullText: true}, nil); err != nil {
		t.Fatal(err)
	}
	s, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// the count reports words, and records the hidden tally separately
	if s.Meta().EntryCount != 3 {
		t.Errorf("entry_count = %d, want 3 (woman, womanhood, @)", s.Meta().EntryCount)
	}
	if v, _ := ReadMetaValue(p, "sub_entries"); v != "3" {
		t.Errorf("sub_entries = %q, want 3", v)
	}

	names := func(res []dict.Result) []string {
		var out []string
		for _, r := range res {
			out = append(out, r.Headword)
		}
		return out
	}
	// a substring wildcard: the search where they would drown everything
	res, err := s.WildcardContext(bg, "*woman*", 20)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range names(res) {
		if strings.HasPrefix(n, "@") {
			t.Errorf("*woman* returned a sub-entry: %q (all: %v)", n, names(res))
		}
	}
	if len(res) != 2 {
		t.Errorf("*woman* = %v, want the two real words", names(res))
	}
	// prefix, full-text and the headword listing likewise
	if res, _ := s.Prefix("wom", 20); len(res) != 2 {
		t.Errorf("prefix wom = %v", names(res))
	}
	if res, _ := s.FullTextContext(bg, "EXAMPLES", 20); len(res) != 0 {
		t.Errorf("full-text reached a sub-entry: %v", names(res))
	}
	for _, k := range s.Keywords(0, 50) {
		if strings.HasPrefix(k, "@") && len(k) > 1 {
			t.Errorf("keyword listing exposed %q", k)
		}
	}
	// …but an article's link can still fetch one by exact lookup
	if res, err := s.Exact("@examples_woman", 1); err != nil || len(res) != 1 {
		t.Fatalf("exact lookup of a sub-entry must work: %v %v", names(res), err)
	}
	// and a bare "@" headword is not collateral damage
	if res, _ := s.Exact("@", 1); len(res) != 1 {
		t.Error(`the headword "@" must remain searchable`)
	}
	if res, _ := s.Prefix("@", 5); len(res) != 1 {
		t.Errorf(`prefix "@" should find the real entry only: %v`, names(res))
	}
}

// Packing must include the files an article references that live BESIDE the
// source rather than inside the .mdd - but only referenced ones: a dictionary
// folder often holds several dictionaries, and sweeping it would pack a
// neighbour's assets.
func TestReferencedAssets(t *testing.T) {
	t.Setenv("WUDICT_DB_DIR", t.TempDir())
	p := filepath.Join(t.TempDir(), "text.db")
	r := &fakeReader{
		meta: dict.Meta{Name: "R", Format: "mdx", Path: "/x.mdx"},
		entries: []dict.Entry{
			h("one", `<link rel="stylesheet" href="LDOCE6.css"><script src="entry.js"></script><p>a</p>`),
			h("two", `<img src="img/pic.png"><a href="bword://other">x</a><img src="https://example.com/x.png">`),
			h("three", `<audio src="sound://beep.mp3"></audio><img src="data:image/png;base64,AAA">`),
			h("four", `<img src="../outside.png"><a href="#frag">f</a><img src="LDOCE6.css">`), // dup + traversal
		},
	}
	if err := ingestFull(r, p); err != nil {
		t.Fatal(err)
	}
	got, err := ReferencedAssets(p)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"LDOCE6.css": true, "entry.js": true, "img/pic.png": true}
	for _, g := range got {
		if !want[g] {
			t.Errorf("unexpected asset %q (all: %v)", g, got)
		}
		delete(want, g)
	}
	if len(want) != 0 {
		t.Errorf("missed %v (got %v)", want, got)
	}
}

// A packed resource must be findable however the article spells it: MDD names
// are indexed lower-cased while loose files keep their real name.
func TestMediaLookupIsCaseInsensitive(t *testing.T) {
	dir := t.TempDir()
	textDB := filepath.Join(dir, "text.db")
	r := &fakeReader{
		meta:    dict.Meta{Name: "M", Format: "mdx", Path: "/x.mdx"},
		entries: []dict.Entry{h("w", `<link href="LDOCE6.css">`)},
	}
	if err := ingestFull(r, textDB); err != nil {
		t.Fatal(err)
	}
	uuid, _ := ReadMetaValue(textDB, "dict_uuid")
	mediaDB := filepath.Join(dir, "media.db")
	if err := IngestMedia(&assetDict{"LDOCE6.css": "span{color:red}"}, []string{"LDOCE6.css", "missing.png"},
		mediaDB, uuid, nil); err != nil {
		t.Fatal(err)
	}
	m, err := OpenMedia(mediaDB)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	for _, spelling := range []string{"LDOCE6.css", "ldoce6.css", "LDOCE6.CSS"} {
		rc, _, err := m.Resource(spelling)
		if err != nil {
			t.Errorf("packed asset not found as %q: %v", spelling, err)
			continue
		}
		rc.Close()
	}
	if _, _, err := m.Resource("missing.png"); err == nil {
		t.Error("an unresolvable name must not be packed")
	}
}

// assetDict serves a fixed set of resources, standing in for a format backend.
type assetDict map[string]string

func (a assetDict) Meta() dict.Meta                           { return dict.Meta{Name: "assets"} }
func (a assetDict) Caps() dict.Caps                           { return dict.Caps{} }
func (a assetDict) Close() error                              { return nil }
func (a assetDict) Exact(string, int) ([]dict.Result, error)  { return nil, nil }
func (a assetDict) Prefix(string, int) ([]dict.Result, error) { return nil, nil }
func (a assetDict) Keywords(int, int) []string                { return nil }
func (a assetDict) Resource(name string) (io.ReadCloser, string, error) {
	if body, ok := a[name]; ok {
		return io.NopCloser(strings.NewReader(body)), "text/css", nil
	}
	return nil, "", dict.ErrNotFound
}

// addRetiredTrigram gives dbPath what a database prepared with the contains
// index before D170 carries: the trigram table and its two meta keys, one of
// them naming a folding version this build no longer has.
func addRetiredTrigram(t *testing.T, dbPath string) {
	t.Helper()
	db, err := sql.Open(driverName, dsnIngest(dbPath))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, q := range []string{
		`CREATE VIRTUAL TABLE entry_trigram USING fts5(w, content='', columnsize=0, tokenize='trigram')`,
		`INSERT INTO entry_trigram(rowid, w) SELECT id, lower(w) FROM entry`,
		`INSERT OR REPLACE INTO meta(key, value) VALUES('has_trigram', '1'), ('fold_version', '0')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
}

// A database prepared with the contains index opens and searches as any
// other: the table and its meta are read past, the plan is what it was
// without them, and nothing calls it outdated.
func TestRetiredTrigramIsReadPast(t *testing.T) {
	s := testStore(t)
	path := s.meta.Path
	s.Close()
	addRetiredTrigram(t, path)

	m, err := ReadMeta(path)
	if err != nil {
		t.Fatal(err)
	}
	if why := Outdated(m); len(why) != 0 {
		t.Errorf("Outdated = %v, want nothing", why)
	}
	if got := KeptPlan(path); got != (Plan{FullText: true}) {
		t.Errorf("KeptPlan = %+v, want full text only", got)
	}
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if c := s2.Caps(); !c.FTS || !c.Wildcard {
		t.Errorf("caps = %+v", c)
	}
	if res, err := s2.WildcardContext(bg, "*razon*", 10); err != nil || len(res) == 0 {
		t.Errorf("*razon* = %v, %v", res, err)
	}
	if _, err := Fingerprint(path); err != nil {
		t.Errorf("Fingerprint: %v", err)
	}
}

// Keywords is not clamped: maxLimit bounds HTTP SEARCH results (FTS-audit #7),
// Keywords is not reachable over HTTP, and "no limit" must not become 500.
func TestKeywordsHonoursTheNoLimitContract(t *testing.T) {
	s := testStore(t)
	all := s.Keywords(0, 0)
	if len(all) == 0 {
		t.Fatal("no headwords at all")
	}
	if got := s.Keywords(0, -1); len(got) != len(all) {
		t.Errorf("n=-1 returned %d headwords, want all %d", len(got), len(all))
	}
	if got := s.Keywords(0, 2); len(got) != 2 {
		t.Errorf("n=2 returned %d headwords, want 2", len(got))
	}
	if got := s.Keywords(1, 0); len(got) != len(all)-1 {
		t.Errorf("offset=1 returned %d, want %d", len(got), len(all)-1)
	}
	if got := s.Keywords(-5, 0); len(got) != len(all) {
		t.Errorf("a negative offset must count as 0: got %d, want %d", len(got), len(all))
	}
	if got := s.Keywords(len(all)+10, 0); got != nil {
		t.Errorf("an offset past the end must be nil, got %v", got)
	}
}

// A media.db is a second SQLite handle with its own page cache and its own
// descriptors, and most sessions never ask a given dictionary for a resource
// at all. Opening it with the text.db meant a hundred prepared dictionaries
// paid for a hundred of them at startup; it is opened on the first resource
// that needs one instead (D64). A failure is remembered, so a corrupt or
// foreign media.db is not retried once per image for the life of the process.
func TestMediaOpensLazily(t *testing.T) {
	r := &fakeReader{meta: dict.Meta{Name: "m", Format: "test", Path: "/gone"},
		entries: []dict.Entry{h("w", "<p>x</p>")}}
	base := filepath.Join(t.TempDir(), "lazy")
	if err := ingestFull(r, base+".text.db"); err != nil {
		t.Fatal(err)
	}
	uuid, _ := ReadMetaValue(base+".text.db", "dict_uuid")
	if err := IngestMedia(&mediaSrc{}, []string{"a.png"}, base+".media.db", uuid, nil); err != nil {
		t.Fatal(err)
	}

	s, err := Open(base + ".text.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.media != nil {
		t.Fatal("Open must not open the sibling media.db")
	}
	if s.mediaPath == "" {
		t.Fatal("Open must still notice that a sibling media.db exists")
	}

	rc, _, err := s.Resource("a.png")
	if err != nil {
		t.Fatalf("first resource: %v", err)
	}
	rc.Close()
	if s.media == nil {
		t.Fatal("the first resource must open the media.db")
	}

	// a closed store never reopens anything, however late a request arrives
	s.Close()
	if _, _, err := s.Resource("a.png"); err == nil {
		t.Error("a resource served from a closed store")
	}
}

// A media.db belonging to some other dictionary must be rejected once, not
// once per request: the check is a uuid comparison, and mediaTried is what
// stops it becoming an open/close cycle on every image in an article.
func TestForeignMediaRejectedOnce(t *testing.T) {
	dir := t.TempDir()
	r := &fakeReader{meta: dict.Meta{Name: "m", Format: "test", Path: "/gone"},
		entries: []dict.Entry{h("w", "<p>x</p>")}}
	base := filepath.Join(dir, "own")
	if err := ingestFull(r, base+".text.db"); err != nil {
		t.Fatal(err)
	}
	// media packed against a different dictionary's uuid
	if err := IngestMedia(&mediaSrc{}, []string{"a.png"}, base+".media.db", "not-our-uuid", nil); err != nil {
		t.Fatal(err)
	}
	s, err := Open(base + ".text.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, _, err := s.Resource("a.png"); err != dict.ErrNotFound {
		t.Fatalf("foreign media.db was served: %v", err)
	}
	if s.media != nil || !s.mediaTried {
		t.Errorf("rejection not remembered: media=%v tried=%v", s.media != nil, s.mediaTried)
	}
}

// A redirect-heavy dictionary is the hard case for the counter: its rows land
// in the link-resolution pass, not in the scan, so a numerator that counted
// only articles would stop at a few percent and then jump to done. The
// count must be monotone and must finish at the total the denominator promised.
func TestIngestProgressCoversRedirects(t *testing.T) {
	const links = 3 * linkPage // several pages, so the paging itself reports
	entries := []dict.Entry{h("uno", "<p>one</p>"), h("dos", "<p>two</p>")}
	for i := 0; i < links; i++ {
		entries = append(entries, dict.Entry{
			Headwords: []string{fmt.Sprintf("alias-%d", i)},
			LinkTo:    "uno",
		})
	}
	// One redirect that resolves to nothing: it is still a record the scan
	// counted, so it must still be counted as processed.
	entries = append(entries, dict.Entry{Headwords: []string{"huérfano"}, LinkTo: "missing"})

	total := len(entries)
	r := &fakeReader{
		meta:    dict.Meta{Name: "Redirects", Format: "mdx", Path: "/nonexistent/r.mdx", EntryCount: total},
		entries: entries,
	}

	var last int
	var sawMid bool
	rep, err := IngestPlan(r, filepath.Join(t.TempDir(), "text.db"), Plan{}, func(done, tot int) {
		if tot != total {
			t.Errorf("total = %d, want %d", tot, total)
		}
		if done < last {
			t.Errorf("progress went backwards: %d after %d", done, last)
		}
		if done > tot {
			t.Errorf("progress %d exceeds total %d", done, tot)
		}
		last = done
		// Something must be reported from the middle of the link pass, not
		// only the 2 articles.
		if done > 2 && done < tot {
			sawMid = true
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Entries != 2 || rep.UnresolvedLinks != 1 {
		t.Fatalf("report = %+v, want 2 entries / 1 unresolved", rep)
	}
	if !sawMid {
		t.Error("no progress reported during link resolution")
	}
	if last != total {
		t.Errorf("final progress = %d, want %d", last, total)
	}
}

// Exact search ranks the spelling typed first and finds the others after it:
// a capital that no headword has still finds the lowercase one (D181).
func TestExactStillPrefersTheExactCase(t *testing.T) {
	s := testStore(t)
	res, err := s.Exact("Corazón", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].Headword != "corazón" {
		t.Fatalf("case-folded lookup = %+v, want the one lowercase entry", res)
	}
}

// The redirect rule: the target spelled verbatim wins, else its lowercased
// form, and the lowest entry id breaks ties in both - with Unicode folding,
// not SQLite's ASCII-only NOCASE.
func TestRedirectResolutionRule(t *testing.T) {
	entries := []dict.Entry{
		h("Apple", "<p>1</p>"), // id 1
		h("apple", "<p>2</p>"), // id 2
		h("APPLE", "<p>3</p>"), // id 3
		h("ábaco", "<p>4</p>"), // id 4
		{Headwords: []string{"to-verbatim"}, LinkTo: "APPLE"},
		{Headwords: []string{"to-folded"}, LinkTo: "aPPle"},
		{Headwords: []string{"to-unicode"}, LinkTo: "ÁBACO"},
		{Headwords: []string{"to-nothing"}, LinkTo: "pear"},
	}
	r := &fakeReader{meta: dict.Meta{Name: "R", Format: "mdx", Path: "/nonexistent/r.mdx"}, entries: entries}
	path := filepath.Join(t.TempDir(), "text.db")
	rep, err := IngestPlan(r, path, Plan{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rep.UnresolvedLinks != 1 {
		t.Errorf("UnresolvedLinks = %d, want 1", rep.UnresolvedLinks)
	}
	db, err := openRO(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for alias, want := range map[string]int64{"to-verbatim": 3, "to-folded": 1, "to-unicode": 4} {
		var got int64
		if err := db.QueryRow("SELECT entry_id FROM alias WHERE w = ?", alias).Scan(&got); err != nil {
			t.Errorf("%s: %v", alias, err)
		} else if got != want {
			t.Errorf("%s -> entry %d, want %d", alias, got, want)
		}
	}
	var n int
	if err := db.QueryRow("SELECT count(*) FROM alias WHERE w = 'to-nothing'").Scan(&n); err != nil || n != 0 {
		t.Errorf("unresolved redirect stored %d alias rows (%v)", n, err)
	}
}
