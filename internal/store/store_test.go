// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/glowinthedark/gonow-dict/internal/dict"
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
	if err := Ingest(r, TextDBPath(dir), nil); err != nil {
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
		"/d/AHD5-2017.slob":      "AHD5-2017",
		"/d/Oxford Advanced.mdx": "Oxford Advanced",
		"/d/big.dsl.dz":          "big",
		"/d/we:ird/na*me.ifo":    "na-me",
		"/d/trailing. ":          "trailing",
		"/d/.mdx":                "dictionary",
	}
	for in, want := range cases {
		if got := FolderName(in); got != want {
			t.Errorf("FolderName(%q) = %q, want %q", in, got, want)
		}
	}
	if got := FolderName("/d/" + strings.Repeat("x", 200) + ".mdx"); len(got) > 80 {
		t.Errorf("FolderName did not cap length: %d", len(got))
	}
}

// Same file name, different formats — the case that exists in real libraries
// (AHD5-2017.slob and AHD5-2017.mdx). Each must own its own folder, and each
// must keep resolving to its own folder afterwards.
func TestClaimDirCollision(t *testing.T) {
	db := t.TempDir()
	t.Setenv("GONOW_DB_DIR", db)
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
	t.Setenv("GONOW_DB_DIR", t.TempDir())
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
	t.Setenv("GONOW_DB_DIR", db)
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
	if byName["Live"].Entries != 1 || !byName["Live"].FullText || !byName["Live"].Contains {
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
// from — the claim written at allocation time is the ownership record — or the
// prepared dictionary is orphaned from its source and silently rebuilt forever.
func TestOwnershipSurvivesBogusReaderPath(t *testing.T) {
	t.Setenv("GONOW_DB_DIR", t.TempDir())
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
	if err := Ingest(r, TextDBPath(dir), nil); err != nil {
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

func TestFindOrphansSemantics(t *testing.T) {
	db := t.TempDir()
	t.Setenv("GONOW_DB_DIR", db)
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

	orphs, err := FindOrphans()
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
	for _, want := range []string{"stray", "old-aaaa.text.db", "old-aaaa.media.db", "text.db.ingest.abc123"} {
		if _, ok := got[want]; !ok {
			t.Errorf("%s should be an orphan (got %v)", want, got)
		}
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

func testStore(t *testing.T) *Store {
	t.Helper()
	r := &fakeReader{
		meta: dict.Meta{Name: "Test Diccionario", Format: "mdx", Path: "/nonexistent/test.mdx"},
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
	if err := Ingest(r, dbPath, nil); err != nil {
		t.Fatal(err)
	}
	s, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestIngestAndMeta(t *testing.T) {
	s := testStore(t)
	m := s.Meta()
	if m.Name != "Test Diccionario" || m.Format != "gonow:mdx" {
		t.Errorf("bad meta: %+v", m)
	}
	if m.EntryCount != 6 { // 8 raw - 2 pure links
		t.Errorf("entry_count = %d, want 6", m.EntryCount)
	}
	if !s.Caps().Contains || !s.Caps().FTS {
		t.Error("store must advertise contains+fts")
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

// P-E: an accent/case-stripped prefix that the raw LIKE pass cannot match
// must still resolve via the folded FTS fallback, matching the direct
// backends (so "coraz" typed without the ó still finds corazón/corazonada).
func TestPrefixFoldedFallback(t *testing.T) {
	s := testStore(t)
	// "córaz" (extra accent) does not LIKE-prefix any headword, but folds
	// to the same key as corazón/corazonada.
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

func TestFuzzyAccentInsensitive(t *testing.T) {
	s := testStore(t)
	res, err := s.Fuzzy("corazon", 10) // no accent
	if err != nil || len(res) < 2 {
		t.Fatalf("Fuzzy corazon: %d results, err=%v", len(res), err)
	}
}

func TestContains(t *testing.T) {
	s := testStore(t)
	// substring in the MIDDLE of a headword (trigram, ≥3 chars) — not a prefix
	res, err := s.Contains("razon", 10)
	if err != nil {
		t.Fatalf("Contains razon: %v", err)
	}
	got := map[string]bool{}
	for _, r := range res {
		got[r.Headword] = true
	}
	if !got["corazón"] || !got["corazonada"] {
		t.Errorf("contains 'razon' should match corazón/corazonada, got %v", res)
	}
	// accent-insensitive: folded query matches accented headword
	if r, _ := s.Contains("orazó", 10); len(r) == 0 {
		t.Error("contains should be accent-insensitive")
	}
	// short (<3 char) query falls back to LIKE, still returns something
	if r, _ := s.Contains("re", 10); len(r) == 0 {
		t.Error("short contains query should fall back to LIKE and match 'pregunta'")
	}
}

func TestFullText(t *testing.T) {
	s := testStore(t)
	res, err := s.FullText("muscular", 10)
	if err != nil || len(res) != 1 || res[0].Headword != "corazón" {
		t.Fatalf("FullText muscular: %v %v", res, err)
	}
	// tag/script content must NOT be indexed (FTS-audit #1)
	if res, _ := s.FullText("evil", 10); len(res) != 0 {
		t.Errorf("script content leaked into FTS: %v", res)
	}
	if res, _ := s.FullText("div", 10); len(res) != 0 {
		t.Errorf("tag names leaked into FTS: %v", res)
	}
	// multi-token = implicit AND
	res, err = s.FullText("órgano muscular", 10)
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
		if _, err := s.Fuzzy(q, 5); err != nil {
			t.Errorf("Fuzzy(%q) errored: %v", q, err)
		}
		if _, err := s.Contains(q, 5); err != nil {
			t.Errorf("Contains(%q) errored: %v", q, err)
		}
		if _, err := s.FullText(q, 5); err != nil {
			t.Errorf("FullText(%q) errored: %v", q, err)
		}
	}
}

func TestClampAndKeywords(t *testing.T) {
	s := testStore(t)
	if _, err := s.Prefix("c", -5); err != nil {
		t.Errorf("negative limit: %v", err)
	}
	if _, err := s.FullText("de", 1<<30); err != nil {
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
		``: "",
	}
	for in, want := range cases {
		if got := StripHTML(in); got != want {
			t.Errorf("StripHTML(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSlug(t *testing.T) {
	cases := map[string]string{
		"Diccionario de la lengua española (2016)": "Diccionario-de-la-lengua-española-2016",
		"a/b\\c:d": "a-b-c-d",
		"---":      "dictionary",
		"":         "dictionary",
		"ok_name":  "ok-name",
	}
	for in, want := range cases {
		if got := Slug(in); got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
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
	if err := Ingest(r, base+".text.db", nil); err != nil {
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
	if err := IngestLevel(r, dbPath, LevelHeadwords, nil); err != nil {
		t.Fatal(err)
	}
	s, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	c := s.Caps()
	if !c.Contains || c.FTS {
		t.Errorf("caps: want contains=true fts=false, got %+v", c)
	}
	// accent-insensitive prefix (the old "fuzzy" engine) still works
	res, err := s.Fuzzy("corazon", 5)
	if err != nil || len(res) != 1 {
		t.Fatalf("prefix-fold: %v %v", res, err)
	}
	// full-text is honestly unsupported
	if _, err := s.FullText("muscular", 5); err != dict.ErrUnsupported {
		t.Errorf("fulltext: want ErrUnsupported, got %v", err)
	}
	// article body must NOT be indexed even via fuzzy w: prefix trickery
	if res, _ := s.Fuzzy("muscular", 5); len(res) != 0 {
		t.Errorf("body leaked into headwords index: %v", res)
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
	if err := Ingest(r, dbPath, nil); err != nil {
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
	t.Setenv("GONOW_DB_DIR", db)
	srcDir := t.TempDir()
	src := writeSrc(t, filepath.Join(srcDir, "Espasa.mdx"), "content")

	// (1) a flat pair whose source is recorded and still present
	loose := filepath.Join(db, "Espasa-Calpe-1f5fa4b2.text.db")
	r := &fakeReader{
		meta:    dict.Meta{Name: "Espasa", Format: "mdx", Path: src},
		entries: []dict.Entry{h("a", "<p>x</p>")},
	}
	if err := Ingest(r, loose, nil); err != nil {
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
	if err := Ingest(r2, noSrc, nil); err != nil {
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
	if !fileExists(TextDBPath(want)) {
		t.Fatalf("expected %s/text.db after adoption", want)
	}
	if !fileExists(MediaDBPath(want)) {
		t.Error("the paired media.db should move with its dictionary")
	}
	if fileExists(loose) || fileExists(looseMedia) {
		t.Error("adoption should move the files, not copy them")
	}
	if got, ok := PreparedFor(src); !ok || got != TextDBPath(want) {
		t.Errorf("adopted dictionary not resolvable from its source: %q %v", got, ok)
	}
	if !fileExists(filepath.Join(db, "spa-cat-index", TextDBName)) {
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
	orph, err := FindOrphans()
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
	t.Setenv("GONOW_DB_DIR", db)
	srcDir := t.TempDir()
	src := writeSrc(t, filepath.Join(srcDir, "Dup.mdx"), "content")
	dir := prepare(t, src, "Dup") // current-layout folder

	loose := filepath.Join(db, "Dup-11112222.text.db")
	r := &fakeReader{
		meta:    dict.Meta{Name: "Dup", Format: "mdx", Path: src},
		entries: []dict.Entry{h("a", "<p>x</p>")},
	}
	if err := Ingest(r, loose, nil); err != nil {
		t.Fatal(err)
	}
	moved, err := AdoptLoose()
	if err != nil {
		t.Fatal(err)
	}
	if len(moved) != 0 {
		t.Fatalf("must not adopt over an existing folder: %+v", moved)
	}
	if !fileExists(loose) {
		t.Fatal("the loose file must be left in place, not deleted")
	}
	if !fileExists(TextDBPath(dir)) {
		t.Fatal("the existing prepared folder must be untouched")
	}
	orph, err := FindOrphans()
	if err != nil {
		t.Fatal(err)
	}
	if len(orph) != 1 || filepath.Base(orph[0].Path) != "Dup-11112222.text.db" {
		t.Fatalf("the duplicate should be the only removable item: %+v", orph)
	}
}
