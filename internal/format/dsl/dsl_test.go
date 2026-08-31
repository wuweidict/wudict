// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package dsl

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/wuweidict/wudict/internal/dict"
	"golang.org/x/text/encoding/charmap"
)

func TestTransformBody(t *testing.T) {
	cases := []struct{ in, want string }{
		{`[b]bold[/b]`, `<b>bold</b>`},
		{`[i]it[/i] [u]un[/u] [sup]s[/sup]`, `<i>it</i> <u>un</u> <sup>s</sup>`},
		{`[c]green[/c]`, `<font color="green">green</font>`},
		{`[c darkred]x[/c]`, `<font color="darkred">x</font>`},
		{`[m2]indent[/m]`, `<p style="padding-left:2em;margin:0">indent</p>`},
		{`[ex]sample[/ex]`, `<span class="ex"><font color="steelblue">sample</font></span>`},
		{`a [ref]target[/ref]`, `a <a href="bword://target">target</a>`},
		{`<<other>>`, `<a href="bword://other">other</a>`},
		{`[url]example.com[/url]`, `<a href="http://example.com">example.com</a>`},
		{`[p]adj.[/p]`, `<i class="p"><font color="green">adj.</font></i>`},
		{`x {{comment}} y`, `x  y`},
		{`[trn]kept[/trn]`, `kept`},
		{`5 &lt; 6? a<b`, `5 &amp;lt; 6? a&lt;b`},
		{`literal ([ ]) brackets`, `literal ([ ]) brackets`}, // corchete case
		{`[unknowntag]inner[/unknowntag]`, `inner`},
		// [m0] means margin 0, not the default indent.
		{`[m0]flush[/m]`, `<p style="padding-left:0em;margin:0">flush</p>`},
		{`[m]bare[/m]`, `<p style="padding-left:0.3em;margin:0">bare</p>`},
		// Not a margin tag: no digits after the 'm'.
		{`[mx]plain[/mx]`, `plain`},
		// A comment may contain a single closing brace.
		{`x {{note with } inside}} y`, `x  y`},
		// Hostile attribute content cannot break out of the quotes.
		{`[c re"d]x[/c]`, `<font color="re&quot;d">x</font>`},
		// A comment alone on its line takes the line with it: keeping the
		// line would put a blank line in front of the next one whenever
		// that line does not open with [m].
		{"[m1]a\n\t{{note}}\n\tb", `<p style="padding-left:1em;margin:0">a<br/>b`},
		{"[m1]a\n\t{{note}}\n\t[m2]b", `<p style="padding-left:1em;margin:0">a<p style="padding-left:2em;margin:0">b`},
		// ... including on the last line, where the newline it takes is the
		// one that ended the line before it.
		{"[m1]a\n\t{{note}}", `<p style="padding-left:1em;margin:0">a`},
		// A line that keeps content keeps its line break too.
		{"[m1]a {{note}}\n\tb", `<p style="padding-left:1em;margin:0">a <br/>b`},
	}
	for _, c := range cases {
		got, _, err := transformBody(c.in, "KEY")
		if err != nil {
			t.Errorf("transformBody(%q) error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("transformBody(%q)\n got %q\nwant %q", c.in, got, c.want)
		}
	}
}

func TestTransformBodyUTF8(t *testing.T) {
	got, _, err := transformBody(`[b]corazón[/b] órgano`, "corazón")
	if err != nil || got != `<b>corazón</b> órgano` {
		t.Errorf("utf8 mangled: %q err=%v", got, err)
	}
	got, _, _ = transformBody(`véase ~`, "corazón")
	if got != `véase corazón` {
		t.Errorf("tilde subst: %q", got)
	}
}

func TestTransformMedia(t *testing.T) {
	got, res, _ := transformBody(`[s]audio/x.mp3[/s][s]img.png[/s]`, "")
	if !strings.Contains(got, `<a class="wudict-audio" href="audio/x.mp3">`) || !strings.Contains(got, `<img align="top" src="img.png"`) {
		t.Errorf("media html: %q", got)
	}
	if len(res) != 2 || res[0] != "audio/x.mp3" || res[1] != "img.png" {
		t.Errorf("resFiles: %v", res)
	}
}

func TestTransformTitle(t *testing.T) {
	tr := transformTitle(`abandonar(se)`)
	if tr.Full != "abandonar(se)"[:9]+"se" || tr.Alt != "abandonar" {
		t.Errorf("parens: %+v", tr)
	}
	// The keys drop the brackets, the display form keeps them - that is how
	// Lingvo and GoldenDict render an optional part.
	if tr.Display != "abandonar(se)" {
		t.Errorf("parens display: %q", tr.Display)
	}
	tr = transformTitle(`word {[i]extra[/i]}`)
	if tr.Full != "word" || !strings.Contains(tr.Display, "<i>extra</i>") {
		t.Errorf("curly: %+v", tr)
	}
	tr = transformTitle(`corazón`)
	if tr.Full != "corazón" || tr.Alt != "corazón" {
		t.Errorf("utf8 title: %+v", tr)
	}
	tr = transformTitle(`a\(b`)
	if tr.Full != "a(b" {
		t.Errorf("escaped paren: %+v", tr)
	}
	// Unsorted `{...}` parts nest inside optional `(...)` parts. Stress marks
	// are written this way (the tag goes in braces so it is not indexed, the
	// stressed vowel stays outside so it is), and a paren scanner blind to `{`
	// used to copy the braces straight into the lookup key.
	tr = transformTitle(`удар{[']}е{[/']}ние в загол{[']}о{[/']}вке (слов{[']}а{[/']}рной стать{[']}и{[/']})`)
	if tr.Full != "ударение в заголовке словарной статьи" {
		t.Errorf("accent in parens, Full: %q", tr.Full)
	}
	if tr.Alt != "ударение в заголовке" {
		t.Errorf("accent in parens, Alt: %q", tr.Alt)
	}
	if strings.Contains(tr.Display, "{") || strings.Count(tr.Display, `<u class="accent">`) != 4 {
		t.Errorf("accent in parens, Display: %q", tr.Display)
	}
	if !strings.HasSuffix(tr.Display, `стать<u class="accent">и</u>)`) ||
		!strings.Contains(tr.Display, `вке (слов`) {
		t.Errorf("accent in parens, brackets lost: %q", tr.Display)
	}
	// Removing an unsorted part must not leave a double space in the key:
	// the entry would be unreachable by its own headword.
	tr = transformTitle(`sample {unsorted part} card`)
	if tr.Full != "sample card" || tr.Alt != "sample card" {
		t.Errorf("unsorted gap: %+v", tr)
	}
	if tr.Display != "sample unsorted part card" {
		t.Errorf("unsorted gap display: %q", tr.Display)
	}
	// A comment is a comment in a headword line too.
	tr = transformTitle(`word {{a note}} two`)
	if tr.Full != "word two" || tr.Display != "word  two" {
		t.Errorf("title comment: %+v", tr)
	}
	// Unterminated `{` must not eat the last character of the headword.
	tr = transformTitle(`abc {[b]def`)
	if tr.Full != "abc" || tr.Display != "abc <b>def" {
		t.Errorf("unterminated curly: %+v", tr)
	}
	// Escapes still win over every construct.
	tr = transformTitle(`a\{b\}c`)
	if tr.Full != "a{b}c" {
		t.Errorf("escaped braces: %+v", tr)
	}
	// DSL lets the space that separates the unsorted part from the headword
	// sit either inside or outside the braces.
	for _, line := range []string{`{to }go away from`, `{to} go away from`} {
		tr = transformTitle(line)
		if tr.Full != "go away from" || tr.Alt != "go away from" {
			t.Errorf("unsorted key %q: %+v", line, tr)
		}
		if tr.Display != "to go away from" {
			t.Errorf("unsorted display %q: got %q", line, tr.Display)
		}
	}
}

const sampleDSL = "#NAME \"Mini Diccionario\"\n" +
	"#INDEX_LANGUAGE \"Spanish\"\n" +
	"#CONTENTS_LANGUAGE \"Spanish\"\n" +
	"\n" +
	"corazón\n" +
	"\t[b]1.[/b] órgano muscular\n" +
	"\t[b]2.[/b] centro de algo\n" +
	"\n" +
	"amar(se)\n" +
	"\tsentir amor: [i]véase ~[/i]\n" +
	"\n" +
	"casa\n" +
	"\tvivienda\n" +
	"\t@ casa rural\n" +
	"\tvivienda en el campo\n" +
	"\t@\n"

func writeDSL(t *testing.T, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func utf16le(s string) []byte {
	var b bytes.Buffer
	b.Write([]byte{0xFF, 0xFE})
	for _, r := range s {
		if r < 0x10000 {
			binary.Write(&b, binary.LittleEndian, uint16(r))
		} else {
			r -= 0x10000
			binary.Write(&b, binary.LittleEndian, uint16(0xD800+(r>>10)))
			binary.Write(&b, binary.LittleEndian, uint16(0xDC00+(r&0x3FF)))
		}
	}
	return b.Bytes()
}

func readAllEntries(t *testing.T, path string) []dict.Entry {
	t.Helper()
	r, err := NewReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if r.Meta().Name != "Mini Diccionario" {
		t.Fatalf("meta name: %+v", r.Meta())
	}
	var out []dict.Entry
	for {
		e, err := r.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, e)
	}
	return out
}

func checkEntries(t *testing.T, entries []dict.Entry) {
	t.Helper()
	if len(entries) != 4 { // corazón, amar(se), casa, casa rural
		t.Fatalf("want 4 entries, got %d: %+v", len(entries), entries)
	}
	if entries[0].Headwords[0] != "corazón" || !strings.Contains(entries[0].Body, "órgano muscular") {
		t.Errorf("entry0: %+v", entries[0])
	}
	if !strings.Contains(entries[0].Body, "<br/>") {
		t.Errorf("multi-line body needs <br/>: %q", entries[0].Body)
	}
	// amar(se): Full + Alt variants, ~ replaced by first term
	if len(entries[1].Headwords) != 2 || entries[1].Headwords[0] != "amarse" || entries[1].Headwords[1] != "amar" {
		t.Errorf("entry1 headwords: %v", entries[1].Headwords)
	}
	if !strings.Contains(entries[1].Body, "véase amarse") {
		t.Errorf("tilde: %q", entries[1].Body)
	}
	// casa: sub-entry split off and linked
	if entries[2].Headwords[0] != "casa" || !strings.Contains(entries[2].Body, `bword://casa rural`) {
		t.Errorf("entry2: %+v", entries[2])
	}
	if entries[3].Headwords[0] != "casa rural" || !strings.Contains(entries[3].Body, "vivienda en el campo") {
		t.Errorf("entry3 (sub): %+v", entries[3])
	}
}

func TestReaderUTF8(t *testing.T) {
	checkEntries(t, readAllEntries(t, writeDSL(t, "mini.dsl", []byte(sampleDSL))))
}

func TestReaderUTF8BOM(t *testing.T) {
	data := append([]byte{0xEF, 0xBB, 0xBF}, []byte(sampleDSL)...)
	checkEntries(t, readAllEntries(t, writeDSL(t, "mini.dsl", data)))
}

func TestReaderUTF16LE(t *testing.T) {
	checkEntries(t, readAllEntries(t, writeDSL(t, "mini.dsl", utf16le(sampleDSL))))
}

func TestReaderUTF16LENoBOM(t *testing.T) {
	checkEntries(t, readAllEntries(t, writeDSL(t, "mini.dsl", utf16le(sampleDSL)[2:])))
}

func TestReaderDz(t *testing.T) {
	var b bytes.Buffer
	gw := gzip.NewWriter(&b)
	gw.Write(utf16le(sampleDSL))
	gw.Close()
	checkEntries(t, readAllEntries(t, writeDSL(t, "mini.dsl.dz", b.Bytes())))
}

func TestResourceZip(t *testing.T) {
	dir := t.TempDir()
	dslPath := filepath.Join(dir, "mini.dsl")
	os.WriteFile(dslPath, []byte(sampleDSL), 0o644)

	// build mini.dsl.files.zip with one resource
	zf, err := os.Create(dslPath + ".files.zip")
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(zf)
	w, _ := zw.Create("audio/es.mp3")
	w.Write([]byte{1, 2, 3})
	zw.Close()
	zf.Close()

	t.Setenv("WUDICT_DB_DIR", t.TempDir()) // keep auto-ingest out of the user cache
	d, err := Open(dslPath)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	rc, mimeType, err := d.Resource("audio/es.mp3")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(rc)
	rc.Close()
	if mimeType != "audio/mpeg" || len(data) != 3 {
		t.Errorf("resource: %q %v", mimeType, data)
	}
	if _, _, err := d.Resource("../evil"); err == nil {
		t.Error("traversal must be rejected")
	}
	// caps: DSL prepares itself so it can be searched at all, but only the
	// cheap headword index - full-text and contains stay opt-in (D24), the
	// same as for a format with its own index
	if c := d.Caps(); c.Contains || c.FTS {
		t.Errorf("caps: %+v", c)
	}
	if res, err := d.Exact("corazon", 5); err != nil || len(res) != 1 {
		t.Errorf("folded exact via store: %v %v", res, err)
	}
}

// A DSL keeps its media in "<name>.dsl.files.zip" or in the matching
// "<name>.dsl.files" folder, and Lingvo's own archivers wrote the zip entry
// names in the machine's code page without recording which one. Both places
// must serve the name the article actually spells.
func TestResourceContainers(t *testing.T) {
	dir := t.TempDir()
	dslPath := filepath.Join(dir, "mini.dsl")
	os.WriteFile(dslPath, []byte(sampleDSL), 0o644)

	cp1251 := func(s string) string {
		b, err := charmap.Windows1251.NewEncoder().String(s)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}

	zf, err := os.Create(dslPath + ".files.zip")
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(zf)
	for _, n := range []string{cp1251("кубок.jpg"), "rummer.jpg"} {
		w, _ := zw.Create(n)
		w.Write([]byte{1, 2, 3})
	}
	zw.Close()
	zf.Close()

	files := dslPath + ".files"
	if err := os.Mkdir(files, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"Кубок Provenzale.jpg", "goblet.jpg"} {
		if err := os.WriteFile(filepath.Join(files, n), []byte{4, 5}, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	t.Setenv("WUDICT_DB_DIR", t.TempDir())
	d, err := Open(dslPath)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	for _, c := range []struct {
		name string
		size int
	}{
		{"кубок.jpg", 3},            // cp1251 zip entry, asked for in UTF-8
		{"rummer.jpg", 3},           // plain zip entry
		{"Кубок Provenzale.jpg", 2}, // the .files folder, which used to be ignored
		{"goblet.jpg", 2},           // ditto, Latin name: also broken before
		{"GOBLET.JPG", 2},           // case is not the article's problem
	} {
		rc, mimeType, err := d.Resource(c.name)
		if err != nil {
			t.Errorf("Resource(%q): %v", c.name, err)
			continue
		}
		b, _ := io.ReadAll(rc)
		rc.Close()
		if len(b) != c.size || mimeType != "image/jpeg" {
			t.Errorf("Resource(%q) = %d bytes %q, want %d bytes image/jpeg", c.name, len(b), mimeType, c.size)
		}
	}
	if _, _, err := d.Resource("missing.jpg"); err == nil {
		t.Error("missing resource must not resolve")
	}

	// Packing sees decoded names from both containers, and nothing from the
	// folder the .dsl merely sits in.
	want := []string{"goblet.jpg", "rummer.jpg", "Кубок Provenzale.jpg", "кубок.jpg"}
	got := d.Resources()
	sort.Strings(want)
	if !slices.Equal(got, want) {
		t.Errorf("Resources() = %q, want %q", got, want)
	}
}

// Integration against the real DSL; skips unless WUDICT_TEST_DSL is set.
func TestIntegrationRealDSL(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	p := os.Getenv("WUDICT_TEST_DSL")
	if p == "" {
		t.Skip("WUDICT_TEST_DSL not set")
	}
	if _, err := os.Stat(p); err != nil {
		t.Skipf("%s not readable", p)
	}
	r, err := NewReader(p)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	n := 0
	for n < 200 {
		e, err := r.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("entry %d: %v", n, err)
		}
		if len(e.Headwords) == 0 || e.Headwords[0] == "" {
			t.Fatalf("entry %d: empty headword", n)
		}
		n++
	}
	if n == 0 {
		t.Fatal("no entries parsed")
	}
}

// TestReaderHeaderTabsAndSubEntries covers the two reader-level rules that
// Lingvo's own sample.dsl exercises and a space-separated fixture does not:
// header lines may use a tab as the key/value separator, and a sub-entry
// headword obeys the same (...)/{...} rules as a top-level one.
func TestReaderHeaderTabsAndSubEntries(t *testing.T) {
	src := "#NAME\t\"Tabbed\"\n" +
		"#INDEX_LANGUAGE\t\"Russian\"\n" +
		"#CONTENTS_LANGUAGE\t\"Russian\"\n" +
		"\n" +
		"удар{[']}е{[/']}ние (слов{[']}а{[/']}рное)\n" +
		"\tтело статьи\n" +
		"\t@ подстать{[']}я(ми)\n" +
		"\tтело подстатьи\n" +
		"\t@\n"
	p := writeDSL(t, "tabbed.dsl", []byte(src))
	r, err := NewReader(p)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	if m := r.Meta(); m.Name != "Tabbed" || m.Description != "Russian → Russian" {
		t.Errorf("tab-separated header: %+v", m)
	}

	main, err := r.Next()
	if err != nil {
		t.Fatal(err)
	}
	wantMain := []string{"ударение словарное", "ударение"}
	if len(main.Headwords) != 2 || main.Headwords[0] != wantMain[0] || main.Headwords[1] != wantMain[1] {
		t.Errorf("main headwords: %q want %q", main.Headwords, wantMain)
	}
	if strings.Contains(main.Body, "{") || !strings.Contains(main.Body, `<u class="accent">`) {
		t.Errorf("main body: %q", main.Body)
	}
	if !strings.Contains(main.Body, `href="bword://подстатьями"`) {
		t.Errorf("sub-entry back-reference missing: %q", main.Body)
	}

	sub, err := r.Next()
	if err != nil {
		t.Fatal(err)
	}
	wantSub := []string{"подстатьями", "подстатья"}
	if len(sub.Headwords) != 2 || sub.Headwords[0] != wantSub[0] || sub.Headwords[1] != wantSub[1] {
		t.Errorf("sub headwords: %q want %q", sub.Headwords, wantSub)
	}
}
