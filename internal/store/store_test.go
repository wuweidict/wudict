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

func TestCacheBaseMemoInvalidatesOnChange(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "d.mdx")
	if err := os.WriteFile(src, []byte("first content"), 0o644); err != nil {
		t.Fatal(err)
	}
	b1 := CacheBase(src, "Dict")
	if got := CacheBase(src, "Dict"); got != b1 {
		t.Fatalf("CacheBase not stable across calls: %s vs %s", got, b1)
	}
	// changed source content (and mtime) must produce a different hash
	if err := os.WriteFile(src, []byte("second, different content"), 0o644); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(2 * time.Second)
	_ = os.Chtimes(src, future, future)
	if b2 := CacheBase(src, "Dict"); b2 == b1 {
		t.Fatalf("CacheBase did not re-hash after source change: %s", b2)
	}
	// missing file → deterministic empty-input digest, stable across calls
	miss := filepath.Join(dir, "gone.mdx")
	if CacheBase(miss, "X") != CacheBase(miss, "X") {
		t.Fatal("missing-file CacheBase not deterministic")
	}
}

// ingestTo builds a minimal real .text.db at dir/dbName recording srcPath as
// its source, for the native-root / orphan tests.
func ingestTo(t *testing.T, dir, dbName, srcPath, name string) string {
	t.Helper()
	r := &fakeReader{
		meta:    dict.Meta{Name: name, Format: "mdx", Path: srcPath},
		entries: []dict.Entry{h("a", "<p>x</p>")},
	}
	p := filepath.Join(dir, dbName)
	if err := Ingest(r, p, nil); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestStandaloneNativeDBs(t *testing.T) {
	dir := t.TempDir()
	// source vanished → standalone native dictionary
	gone := ingestTo(t, dir, "gone-1111.text.db", "/no/such/gone.mdx", "Gone")
	// source present → omitted (its external entry represents it)
	srcDir := t.TempDir()
	src := filepath.Join(srcDir, "live.mdx")
	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	ingestTo(t, dir, "live-2222.text.db", src, "Live")

	got, err := StandaloneNativeDBs(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != gone {
		t.Fatalf("want [%s] (source-gone only), got %v", gone, got)
	}
}

func TestFindOrphansSemantics(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GONOW_DB_DIR", dir) // FindOrphans scans DefaultDBDir()

	// 1. source-less native: MUST NOT be an orphan (the data-loss guard)
	ingestTo(t, dir, "native-aaaa.text.db", "/gone/x.mdx", "Native")
	// 2. superseded: source exists but the db name doesn't match its hash
	srcDir := t.TempDir()
	src := filepath.Join(srcDir, "s.mdx")
	if err := os.WriteFile(src, []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}
	ingestTo(t, dir, "wrong-name.text.db", src, "S")
	// 3. media.db with no text.db sibling
	if err := os.WriteFile(filepath.Join(dir, "loner-bbbb.media.db"), []byte("m"), 0o644); err != nil {
		t.Fatal(err)
	}

	orphs, err := FindOrphans()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, o := range orphs {
		got[filepath.Base(o.Path)] = o.Reason
	}
	if _, bad := got["native-aaaa.text.db"]; bad {
		t.Fatal("DATA LOSS: source-less native dict must NOT be an orphan")
	}
	if _, ok := got["wrong-name.text.db"]; !ok {
		t.Error("superseded ingest should be an orphan")
	}
	if _, ok := got["loner-bbbb.media.db"]; !ok {
		t.Error("media.db with no dictionary should be an orphan")
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
