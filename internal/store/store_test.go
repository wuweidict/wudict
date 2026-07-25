// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/glowinthedark/gonow-dict/internal/dict"
)

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
	if !s.Caps().Fuzzy || !s.Caps().FTS {
		t.Error("store must advertise fuzzy+fts")
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

func TestFuzzyAccentInsensitive(t *testing.T) {
	s := testStore(t)
	res, err := s.Fuzzy("corazon", 10) // no accent
	if err != nil || len(res) < 2 {
		t.Fatalf("Fuzzy corazon: %d results, err=%v", len(res), err)
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
	if !c.Fuzzy || c.FTS {
		t.Errorf("caps: want fuzzy=true fts=false, got %+v", c)
	}
	// fuzzy over headwords still works, accent-insensitive
	res, err := s.Fuzzy("corazon", 5)
	if err != nil || len(res) != 1 {
		t.Fatalf("fuzzy: %v %v", res, err)
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
