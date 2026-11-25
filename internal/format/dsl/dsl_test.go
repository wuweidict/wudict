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
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/wuweidict/wudict/internal/dict"
	"github.com/wuweidict/wudict/internal/resource"
	"golang.org/x/text/encoding/charmap"
)

func TestTransformBody(t *testing.T) {
	cases := []struct{ in, want string }{
		{`[b]bold[/b]`, `<b>bold</b>`},
		{`[i]it[/i] [u]un[/u] [sup]s[/sup]`, `<i>it</i> <u>un</u> <sup>s</sup>`},
		{`[c]green[/c]`, `<span class="wu-c">green</span>`},
		{`[c darkred]x[/c]`, `<span class="wu-c" style="--wd-c:darkred">x</span>`},
		{`[m2]indent[/m]`, `<p class="wu-m" style="--wd-m:2">indent</p>`},
		// [/m2] is as common as [/m] and closes the same paragraph; matching
		// only "m" left it open and the indent ran to the end of the article.
		{`[m2]indent[/m2]`, `<p class="wu-m" style="--wd-m:2">indent</p>`},
		{`[m]bare[/m0]`, `<p class="wu-m">bare</p>`},
		{`[ex]sample[/ex]`, `<span class="wu-ex">sample</span>`},
		{`a [ref]target[/ref]`, `a <a href="entry://target">target</a>`},
		{`<<other>>`, `<a href="entry://other">other</a>`},
		{`[ref]C#[/ref] <<100%>>`, `<a href="entry://C%23">C#</a> <a href="entry://100%25">100%</a>`},
		{`[url]example.com[/url]`, `<a href="http://example.com">example.com</a>`},
		{`[p]adj.[/p]`, `<span class="wu-p">adj.</span>`},
		{`x {{comment}} y`, `x  y`},
		{`[trn]kept[/trn]`, `<span class="wu-trn">kept</span>`},
		{`5 &lt; 6? a<b`, `5 &amp;lt; 6? a&lt;b`},
		{`literal ([ ]) brackets`, `literal ([ ]) brackets`}, // corchete case
		{`[unknowntag]inner[/unknowntag]`, `inner`},
		// [m0] means margin 0, not the default indent.
		{`[m0]flush[/m]`, `<p class="wu-m">flush</p>`},
		{`[m]bare[/m]`, `<p class="wu-m">bare</p>`},
		// Not a margin tag: no digits after the 'm'.
		{`[mx]plain[/mx]`, `plain`},
		// A comment may contain a single closing brace.
		{`x {{note with } inside}} y`, `x  y`},
		// Hostile attribute content cannot break out of the quotes.
		{`[c re"d]x[/c]`, `<span class="wu-c">x</span>`},
		// A comment alone on its line takes the line with it: keeping the
		// line would put a blank line in front of the next one whenever
		// that line does not open with [m].
		{"[m1]a\n\t{{note}}\n\tb", `<p class="wu-m" style="--wd-m:1">a<br/>b</p>`},
		{"[m1]a\n\t{{note}}\n\t[m2]b", `<p class="wu-m" style="--wd-m:1">a</p><p class="wu-m" style="--wd-m:2">b</p>`},
		// ... including on the last line, where the newline it takes is the
		// one that ended the line before it.
		{"[m1]a\n\t{{note}}", `<p class="wu-m" style="--wd-m:1">a</p>`},
		// A line that keeps content keeps its line break too.
		{"[m1]a {{note}}\n\tb", `<p class="wu-m" style="--wd-m:1">a <br/>b</p>`},
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
	if !strings.Contains(got, `<a class="wu-audio" href="audio/x.mp3">`) || !strings.Contains(got, `<img align="top" src="img.png"`) {
		t.Errorf("media html: %q", got)
	}
	if len(res) != 2 || res[0] != "audio/x.mp3" || res[1] != "img.png" {
		t.Errorf("resFiles: %v", res)
	}
}

// TestTransformMediaKinds pins the whole media zone, kind by kind. It guards
// against silence: an extension that emits nothing renders a [s]video.mp4[/s]
// card as a blank gap with the file name recorded but unreachable.
func TestTransformMediaKinds(t *testing.T) {
	cases := []struct {
		in, want string
		res      []string
	}{
		// video a browser plays, inline and unfetched until pressed
		{`[s]video.mp4[/s]`,
			`<video class="wu-video" controls preload="none" src="video.mp4"></video>`,
			[]string{"video.mp4"}},
		// [video] is the x5 synonym of [s] - identical output, not a variant
		{`[video]clip.webm[/video]`,
			`<video class="wu-video" controls preload="none" src="clip.webm"></video>`,
			[]string{"clip.webm"}},
		// a document: file link, name as text, file:// so the article rewriter
		// maps it to /res/ regardless of extension
		{`[s]español.pdf[/s]`,
			`<a class="wu-file" href="file://español.pdf">&#128196; español.pdf</a>`,
			[]string{"español.pdf"}},
		// Lingvo's own video container, which no browser decodes: a link, not
		// an inline player that could never play
		{`[s]clip.avi[/s]`,
			`<a class="wu-file" href="file://clip.avi">&#128196; clip.avi</a>`,
			[]string{"clip.avi"}},
		// nor are Lingvo's Microsoft image formats <img>
		{`[s]plate.wmf[/s]`,
			`<a class="wu-file" href="file://plate.wmf">&#128196; plate.wmf</a>`,
			[]string{"plate.wmf"}},
		// an extension-less payload is a file too - never dropped
		{`[s]README[/s]`,
			`<a class="wu-file" href="file://README">&#128196; README</a>`,
			[]string{"README"}},
		// a hostile name: quotes and ampersands escape in both the attribute
		// and the text, and the class attribute cannot be broken out of
		{`[s]a"b&c.pdf[/s]`,
			`<a class="wu-file" href="file://a&quot;b&amp;c.pdf">&#128196; a"b&amp;c.pdf</a>`,
			[]string{`a"b&c.pdf`}},
		// [preview] is accepted inside the zone and has no effect; it must not
		// become part of the file name
		{`[s][preview]video.mp4[/preview][/s]`,
			`<video class="wu-video" controls preload="none" src="video.mp4"></video>`,
			[]string{"video.mp4"}},
		// audio and images are unchanged, byte for byte
		{`[s]x.mp3[/s]`, `<a class="wu-audio" href="x.mp3">&#128266;</a>`, []string{"x.mp3"}},
		{`[s]X.PNG[/s]`, `<img align="top" src="X.PNG" alt="X.PNG" />`, []string{"X.PNG"}},
		// an empty zone names no file and must not be recorded as one
		{`[s][/s]`, ``, nil},
	}
	for _, c := range cases {
		got, res, err := transformBody(c.in, "KEY")
		if err != nil {
			t.Errorf("transformBody(%q) error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("transformBody(%q)\n got %q\nwant %q", c.in, got, c.want)
		}
		if len(res) != len(c.res) {
			t.Errorf("transformBody(%q) resFiles = %v, want %v", c.in, res, c.res)
			continue
		}
		for i := range res {
			if res[i] != c.res[i] {
				t.Errorf("transformBody(%q) resFiles = %v, want %v", c.in, res, c.res)
				break
			}
		}
	}
}

func TestTransformTitle(t *testing.T) {
	keys := func(line string) string { return strings.Join(transformTitle(line).Keys, "|") }

	// An optional part is a pair of variants, and EVERY optional part is its
	// own independent pair: n of them are 2^n headwords, fully-expanded first
	// (lingvo-ref "Заголовок статьи").
	for _, c := range []struct{ in, want string }{
		{`abandonar(se)`, "abandonarse|abandonar"},
		{`(пре)вращать(ся)`, "превращаться|вращаться|превращать|вращать"},
		{`corazón`, "corazón"},
		{`a\(b`, "a(b"},
		{`a\{b\}c`, "a{b}c"},
		{`word {[i]extra[/i]}`, "word"},
		// Removing an unsorted part must not leave a double space in the key:
		// the entry would be unreachable by its own headword.
		{`sample {unsorted part} card`, "sample card"},
		// A comment is a comment in a headword line too.
		{`word {{a note}} two`, "word two"},
		// Unterminated `{` must not eat the last character of the headword.
		{`abc {[b]def`, "abc"},
		// DSL lets the space that separates the unsorted part from the headword
		// sit either inside or outside the braces.
		{`{to }go away from`, "go away from"},
		{`{to} go away from`, "go away from"},
	} {
		if got := keys(c.in); got != c.want {
			t.Errorf("transformTitle(%q) keys = %q, want %q", c.in, got, c.want)
		}
	}

	// The keys drop the brackets, the display form keeps them - that is how
	// Lingvo and GoldenDict render an optional part.
	if d := transformTitle(`abandonar(se)`).Display; d != "abandonar(se)" {
		t.Errorf("parens display: %q", d)
	}
	if d := transformTitle(`word {[i]extra[/i]}`).Display; !strings.Contains(d, "<i>extra</i>") {
		t.Errorf("curly display: %q", d)
	}
	if d := transformTitle(`sample {unsorted part} card`).Display; d != "sample unsorted part card" {
		t.Errorf("unsorted gap display: %q", d)
	}
	if d := transformTitle(`word {{a note}} two`).Display; d != "word  two" {
		t.Errorf("title comment display: %q", d)
	}
	if d := transformTitle(`abc {[b]def`).Display; d != "abc <b>def</b>" {
		t.Errorf("unterminated curly display: %q", d)
	}
	for _, line := range []string{`{to }go away from`, `{to} go away from`} {
		if d := transformTitle(line).Display; d != "to go away from" {
			t.Errorf("unsorted display %q: got %q", line, d)
		}
	}

	// Unsorted `{...}` parts nest inside optional `(...)` parts. Stress marks
	// are written this way (the tag goes in braces so it is not indexed, the
	// stressed vowel stays outside so it is), and a paren scanner blind to `{`
	// would copy the braces straight into the lookup key.
	tr := transformTitle(`удар{[']}е{[/']}ние в загол{[']}о{[/']}вке (слов{[']}а{[/']}рной стать{[']}и{[/']})`)
	if got := strings.Join(tr.Keys, "|"); got != "ударение в заголовке словарной статьи|ударение в заголовке" {
		t.Errorf("accent in parens, keys: %q", got)
	}
	if strings.Contains(tr.Display, "{") || strings.Count(tr.Display, `<span class="wu-acc">`) != 4 {
		t.Errorf("accent in parens, Display: %q", tr.Display)
	}
	if !strings.HasSuffix(tr.Display, `стать<span class="wu-acc">и</span>)`) ||
		!strings.Contains(tr.Display, `вке (слов`) {
		t.Errorf("accent in parens, brackets lost: %q", tr.Display)
	}

	// Past the cap the expansion collapses to the two extremes rather than
	// growing without bound.
	many := `a(1)b(2)c(3)d(4)e(5)f(6)g(7)`
	if got := keys(many); got != "a1b2c3d4e5f6g7|abcdefg" {
		t.Errorf("expansion cap: %q", got)
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
	if entries[2].Headwords[0] != "casa" || !strings.Contains(entries[2].Body, `entry://casa rural`) {
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
	// cheap headword index - full-text stays opt-in (D24), the same as for a
	// format with its own index
	if c := d.Caps(); c.FTS {
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
	if strings.Contains(main.Body, "{") || !strings.Contains(main.Body, `<span class="wu-acc">`) {
		t.Errorf("main body: %q", main.Body)
	}
	if !strings.Contains(main.Body, `href="entry://подстатьями"`) {
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

// A prepared DSL reaches its media through the source path alone (O8): no
// parsing, no headwords, nothing opened but the archive. The provider must
// therefore find the same containers loadSources would, and be registered so
// the server can reach it without opening the dictionary.
// Sub-card headings: the "@" may be preceded by whitespace and by DSL tags,
// the space after it is optional, several headings may be piled on consecutive
// lines to share one card, and each heading contributes its own "- " link to
// the parent article (one per expanded key). Checked against GoldenDict and
// Lingvo rendering of the same source.
func TestReaderSubCardHeadings(t *testing.T) {
	src := "#NAME\t\"Test\"\n" +
		"\n" +
		"dictionary\n" +
		"\t[m1]1) словарь\n" +
		"\t[m1]2) справочник\n" +
		"\t[*]\n" +
		"\t@ explanatory dictionary\n" +
		"\t[m1]↑ a space between \\@ and the heading is allowed\n" +
		"\t@standard dictionary\n" +
		"\t[m1]нормативный словарь\n" +
		"\t@ dictionary making\n" +
		"\t@ dictionary compiling\n" +
		"\t[m1]↑ several headings for one sub-card\n" +
		"\t[m1]@ *Служ{[']}е{[/']}бная (информ{[']}а{[/']}ция{ о словаре}) для составителя{*}\n" +
		"\t[m2]A very complex heading\n" +
		"\t[m3]@\n" +
		"\t[/*]\n"
	p := writeDSL(t, "subcards.dsl", []byte(src))
	r, err := NewReader(p)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	var got []dict.Entry
	for {
		e, err := r.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, e)
	}
	if len(got) != 5 {
		t.Fatalf("entries: %d want 5 (main + 4 sub-cards): %+v", len(got), got)
	}

	main := got[0]
	if len(main.Headwords) != 1 || main.Headwords[0] != "dictionary" {
		t.Errorf("main headwords: %q", main.Headwords)
	}
	wantLinks := []string{
		"explanatory dictionary",
		"standard dictionary",
		"dictionary making",
		"dictionary compiling",
		"*Служебная информация для составителя",
		"*Служебная для составителя",
	}
	pos := 0
	for _, w := range wantLinks {
		want := `- <a href="entry://` + w + `">`
		i := strings.Index(main.Body[pos:], want)
		if i < 0 {
			t.Fatalf("link %q missing or out of order in main body: %q", w, main.Body)
		}
		pos += i + len(want)
	}
	if n := strings.Count(main.Body, "entry://"); n != len(wantLinks) {
		t.Errorf("main body has %d links, want %d: %q", n, len(wantLinks), main.Body)
	}

	wantSubs := [][]string{
		{"explanatory dictionary"},
		{"standard dictionary"},
		{"dictionary making", "dictionary compiling"},
		{"*Служебная информация для составителя", "*Служебная для составителя"},
	}
	for i, want := range wantSubs {
		heads := got[i+1].Headwords
		if len(heads) != len(want) {
			t.Errorf("sub %d headwords: %q want %q", i, heads, want)
			continue
		}
		for j := range want {
			if heads[j] != want[j] {
				t.Errorf("sub %d headwords: %q want %q", i, heads, want)
				break
			}
		}
	}
	if b := got[1].Body; !strings.Contains(b, "a space between @ and the heading") {
		t.Errorf("sub 0 body: %q", b)
	}
	if b := got[2].Body; !strings.Contains(b, "нормативный словарь") {
		t.Errorf("sub 1 body: %q", b)
	}
	if b := got[3].Body; !strings.Contains(b, "several headings for one sub-card") {
		t.Errorf("sub 2 body: %q", b)
	}
	// The trailing "[m3]@" closes the last card; its body must not leak into
	// the parent, and the parent must not swallow the closing line.
	if b := got[4].Body; !strings.Contains(b, "A very complex heading") {
		t.Errorf("sub 3 body: %q", b)
	}
	if strings.Contains(main.Body, "A very complex heading") || strings.Contains(main.Body, "@") {
		t.Errorf("main body kept sub-card content: %q", main.Body)
	}
}

// dslEscape round-trip: a sub-headword carrying DSL metacharacters has to come
// back out of the generated [ref] unchanged.
func TestDslEscapeRoundTrip(t *testing.T) {
	for _, key := range []string{`a[b]~c`, `x\y`, `a@b`, `p<q>r`, "plain"} {
		body, _, err := transformBody("\t[m2][ref]"+dslEscape(key)+"[/ref][/m]", "head")
		if err != nil {
			t.Fatal(err)
		}
		want := `href="entry://` + escape(key) + `"`
		if !strings.Contains(body, want) {
			t.Errorf("dslEscape(%q): body %q lacks %q", key, body, want)
		}
	}
}

func TestMediaSourcesFromPathAlone(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "big.dsl")
	files := filepath.Join(dir, "big.dsl.files")
	if err := os.MkdirAll(files, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(files, "kubok.jpg"), []byte("jpeg"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The .dsl itself is never read: the file does not even exist.
	srcs := MediaSources(src)
	if len(srcs) < 2 {
		t.Fatalf("want the .files folder and the dictionary's own folder, got %d", len(srcs))
	}
	var found bool
	for _, s := range srcs {
		if rc, err := s.Open("kubok.jpg"); err == nil {
			rc.Close()
			found = true
			break
		}
	}
	if !found {
		t.Fatal("the .files folder was not among the sources")
	}
	if p, ok := resource.Get("dsl"); !ok || p.Sources == nil {
		t.Fatal("dsl registered no media provider")
	}
}

// A ".files.zip" is named after the compressed file, after the ".dsl" inside
// it, or after the bare dictionary name - all three are in the wild. Every
// spelling internal/dict lists as a companion must be one this package can
// actually open: a name listed there and unresolved here is a zip the user is
// told belongs to the dictionary, and which removal deletes, while every image
// in it 404s.
func TestMediaSourcesEveryZipSpelling(t *testing.T) {
	for _, name := range []string{"AHD5.dsl.dz.files.zip", "AHD5.dsl.files.zip", "AHD5.files.zip"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, "AHD5.dsl.dz")
			if err := os.WriteFile(src, gzipBytesDSL([]byte(sampleDSL)), 0o644); err != nil {
				t.Fatal(err)
			}
			zf, err := os.Create(filepath.Join(dir, name))
			if err != nil {
				t.Fatal(err)
			}
			zw := zip.NewWriter(zf)
			w, _ := zw.Create("pic.png")
			w.Write([]byte{1, 2, 3})
			zw.Close()
			zf.Close()

			var found bool
			srcs := MediaSources(src)
			defer func() {
				for _, s := range srcs {
					s.Close()
				}
			}()
			for _, s := range srcs {
				if rc, err := s.Open("pic.png"); err == nil {
					rc.Close()
					found = true
					break
				}
			}
			if !found {
				t.Errorf("MediaSources did not resolve %s", name)
			}
			if got := dict.CompanionMedia(src); len(got) != 1 || filepath.Base(got[0]) != name {
				t.Errorf("dict.CompanionMedia(%q) = %v, want just %s", src, got, name)
			}
		})
	}
}

func gzipBytesDSL(data []byte) []byte {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	zw.Write(data)
	zw.Close()
	return buf.Bytes()
}

// TestRefDict covers [ref dict="..."], the cross-dictionary link: the target
// dictionary is named by its #NAME, and the reader has to carry that name out
// to the UI or the link lands in the dictionary it was written in
// (lingvo-ref "Тэг [ref]···[/ref]").
func TestRefDict(t *testing.T) {
	cases := []struct{ in, want string }{
		// The spec's only [ref] attribute. target= is GoldenDict's extension
		// and still works alongside it.
		{`[ref dict="Other Dict"]Word[/ref]`,
			`<a class="wu-xref" data-dict="Other Dict" title="Other Dict" href="entry://Word">Word</a>`},
		{`[ref dict="Other Dict" target="Real Head"]Shown[/ref]`,
			`<a class="wu-xref" data-dict="Other Dict" title="Other Dict" href="entry://Real Head">Shown</a>`},
		// No dict=: an in-dictionary link, unchanged.
		{`[ref]Word[/ref]`, `<a href="entry://Word">Word</a>`},
		// An empty or whitespace dict= names nothing and must not produce a
		// cross-reference that resolves to no dictionary at all.
		{`[ref dict=" "]Word[/ref]`, `<a href="entry://Word">Word</a>`},
		// A hostile dictionary name cannot break out of either attribute.
		{`[ref dict="a<b&c"]W[/ref]`,
			`<a class="wu-xref" data-dict="a&lt;b&amp;c" title="a&lt;b&amp;c" href="entry://W">W</a>`},
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

// TestBodyCommands covers body-level constructs that must not pass through as
// text: [br], the "^" case inverter and the "]]" escape.
func TestBodyCommands(t *testing.T) {
	cases := []struct{ key, in, want string }{
		// [br] is a hard break with no closing descriptor (Lingvo x5).
		{"K", `a[br]b`, `a<br/>b`},
		// "^" inverts the case of the character after it; "^~" is the whole
		// point of it - a capitalised headword mirrored into running text.
		{"Кубарем", `Скатиться ^~ с лестницы.`, `Скатиться кубарем с лестницы.`},
		{"слово", `^~ здесь`, `Слово здесь`},
		{"K", `^abc`, `Abc`},
		{"K", `^Abc`, `abc`},
		// An escaped "^" is a literal one, and a "^" with nothing to act on
		// disappears rather than printing itself as markup.
		{"K", `a\^b`, `a^b`},
		{"K", `a^`, `a`},
		// "]]" is a literal "]", the mirror of "[[".
		{"K", `a]]b`, `a]b`},
		{"K", `[[b]]`, `[b]`},
		// A lone "]" is still emitted as-is.
		{"K", `a]b`, `a]b`},
		// "~" itself is unchanged: the mirrored headword, verbatim.
		{"Кубарем", `~ x`, `Кубарем x`},
	}
	for _, c := range cases {
		got, _, err := transformBody(c.in, c.key)
		if err != nil {
			t.Errorf("transformBody(%q) error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("transformBody(%q, key=%q)\n got %q\nwant %q", c.in, c.key, got, c.want)
		}
	}
}

// TestExpandTitleTilde: "~" in a sub-card heading is the PARENT headword, and
// the parent may itself contain title syntax that must not be re-read as such.
func TestExpandTitleTilde(t *testing.T) {
	cases := []struct{ head, parent, want string }{
		{`~ up`, `give`, `give up`},
		{`no tilde`, `give`, `no tilde`},
		{`\~ up`, `give`, `\~ up`},
		// A parent carrying optional-part or unsorted-part syntax is escaped
		// on the way in, so the child indexes one key, not four.
		{`~ off`, `(the) sun`, `\(the\) sun off`},
	}
	for _, c := range cases {
		if got := expandTitleTilde(c.head, c.parent); got != c.want {
			t.Errorf("expandTitleTilde(%q, %q) = %q, want %q", c.head, c.parent, got, c.want)
		}
	}
	// End to end: the escaped parent survives transformTitle unchanged.
	if got := transformTitle(expandTitleTilde(`~ off`, `(the) sun`)).Keys; len(got) != 1 || got[0] != `(the) sun off` {
		t.Errorf("tilde round-trip: %q", got)
	}
}

// TestReaderDirectives covers the preprocessor rules a main file may use:
// #INCLUDE pulls another file into the same dictionary, #FULL_NAME and a
// main-file #LANGUAGE are the Lingvo 6.0/7.0 spellings of #NAME and
// #INDEX_LANGUAGE, and a "#" line is a directive wherever it appears - never a
// headword, whatever follows it.
func TestReaderDirectives(t *testing.T) {
	dir := t.TempDir()
	main := filepath.Join(dir, "main.dsl")
	part := filepath.Join(dir, "part.dsl")
	// The include value is a Windows path with DOUBLED backslashes, which is
	// how the manual writes it and how every real dictionary stores it.
	if err := os.WriteFile(main, []byte(
		"#FULL_NAME\t\"Legacy Dict\"\n"+
			"#LANGUAGE\t\"English\"\t\"Russian\"\n"+
			"#INCLUDE\t\"part.dsl\"\n"+
			"alpha\n\t[m1]first[/m]\n"+
			"#INCLUDE \"missing\\\\nowhere.dsl\"\n"+
			"beta\n\t[m1]second[/m]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(part, []byte("gamma\n\t[m1]third[/m]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := NewReader(main)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if m := r.Meta(); m.Name != "Legacy Dict" || m.IndexLang != "en" {
		t.Errorf("legacy header: name=%q indexLang=%q", m.Name, m.IndexLang)
	}
	var heads []string
	for {
		e, err := r.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		heads = append(heads, e.Headwords[0])
	}
	// The include is read after the main file, and the missing one is a
	// warning, not the end of the dictionary.
	if got := strings.Join(heads, ","); got != "alpha,beta,gamma" {
		t.Errorf("entries = %q, want %q", got, "alpha,beta,gamma")
	}
}

// An #INCLUDE never leaves the including file's folder: an absolute path, a
// UNC path (on Windows a stat of one opens an SMB session) and a climbing
// relative one are each resolved by base name beside the main file, or not at
// all - never at the place they name.
func TestReaderIncludeStaysInFolder(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "dict")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(root, "outside.dsl")
	if err := os.WriteFile(outside, []byte("leaked\n\t[m1]x[/m]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "near.dsl"), []byte("near\n\t[m1]x[/m]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	main := filepath.Join(dir, "main.dsl")
	if err := os.WriteFile(main, []byte(
		"#NAME\t\"D\"\n"+
			"#INCLUDE\t\""+filepath.ToSlash(outside)+"\"\n"+
			"#INCLUDE\t\"..\\\\outside.dsl\"\n"+
			"#INCLUDE\t\"\\\\\\\\host\\\\share\\\\near.dsl\"\n"+
			"alpha\n\t[m1]a[/m]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := NewReader(main)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	var heads []string
	for {
		e, err := r.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		heads = append(heads, e.Headwords[0])
	}
	if got := strings.Join(heads, ","); got != "alpha,near" {
		t.Errorf("entries = %q, want %q", got, "alpha,near")
	}
}

// A "#" line that is not a known directive is still not an entry: Lingvo
// reserves column-0 "#" for the preprocessor, and treating one as a headword
// indexes the directive text itself.
func TestReaderUnknownDirectiveIsNotAHeadword(t *testing.T) {
	p := writeDSL(t, "d.dsl", []byte("#NAME\t\"D\"\n#SOMETHING odd\nalpha\n\t[m1]x[/m]\n"))
	r, err := NewReader(p)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	var got []dict.Entry
	for {
		e, err := r.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, e)
	}
	if len(got) != 1 || got[0].Headwords[0] != "alpha" {
		t.Errorf("entries: %+v", got)
	}
}

// A {{...}} comment zone may open in one card and close in another; every
// headword caught between the two is ignored by the compiler
// (lingvo-ref "Тэг {{···}}").
func TestReaderSpanningComment(t *testing.T) {
	p := writeDSL(t, "c.dsl", []byte("#NAME\t\"C\"\n"+
		"alpha\n\t[m1]one[/m]\n"+
		"{{ a note that runs\n"+
		"beta\n\t[m1]two[/m]\n"+
		"past a headword }}\n"+
		"gamma\n\t[m1]three[/m]\n"))
	r, err := NewReader(p)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	var heads []string
	for {
		e, err := r.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		heads = append(heads, e.Headwords[0])
	}
	if got := strings.Join(heads, ","); got != "alpha,gamma" {
		t.Errorf("entries = %q, want %q", got, "alpha,gamma")
	}
}

func TestStripLineComments(t *testing.T) {
	cases := []struct {
		line string
		in   bool
		want string
		out  bool
	}{
		// closes on its own line: gone, wherever it sits
		{`word {{ note }} two`, false, `word  two`, false},
		{`{{ whole line }}`, false, ``, false},
		{"\t{{ indented note }}", false, "\t", false},
		// opens and stays open
		{`word {{ note`, false, `word `, true},
		{`still inside`, true, ``, true},
		{`closes }} tail`, true, ` tail`, false},
		// two zones on one line, the second still open
		{`a {{x}} b {{y`, false, `a  b `, true},
		// inside a zone the escape is dead: "\}}" still closes
		{`a {{ n\}} b`, false, `a  b`, false},
		// outside one it holds, so "\{\{" opens nothing
		{`a \{\{ b`, false, `a \{\{ b`, false},
		{`plain`, false, `plain`, false},
	}
	for _, c := range cases {
		got, out := stripLineComments(c.line, c.in)
		if got != c.want || out != c.out {
			t.Errorf("stripLineComments(%q,%v) = %q,%v want %q,%v", c.line, c.in, got, out, c.want, c.out)
		}
	}
}

// TestBlankLine pins the one whitespace class that must NOT read as empty: the
// non-standard spaces an author uses to force a paragraph break past the
// space-collapsing rule.
func TestBlankLine(t *testing.T) {
	blank := []string{"", " ", "\t", " \t \t", "\v\f"}
	full := []string{"\u00a0", "\t\u00a0", "\u2003", "\t\u2002 ", "x", "\t."}
	for _, s := range blank {
		if !blankLine(s) {
			t.Errorf("blankLine(%q) = false, want true", s)
		}
	}
	for _, s := range full {
		if blankLine(s) {
			t.Errorf("blankLine(%q) = true, want false", s)
		}
	}
}

// TestReaderCommentOnlyLines covers the two placements that can produce
// "entry block without headword": an indented {{...}} block standing between
// two cards (a body-shaped run of lines belonging to no card), and a comment
// zone sitting between the directives and the first headword, where ending the
// header loop would turn the bare "{{" into a headword.
func TestReaderCommentOnlyLines(t *testing.T) {
	p := writeDSL(t, "cc.dsl", []byte("#NAME\t\"CC\"\n"+
		"#INDEX_LANGUAGE\t\"English\"\n"+
		"{{\nauthor note before any card\n}}\n"+
		"alpha\n\t[m1]one[/m]\n"+
		"\n\t{{\n\tsome text\n\t}}\n\n"+
		"beta\n\t[m1]two[/m]\n"))
	r, err := NewReader(p)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	var heads []string
	for {
		e, err := r.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		heads = append(heads, e.Headwords[0])
	}
	if got := strings.Join(heads, ","); got != "alpha,beta" {
		t.Errorf("entries = %q, want %q", got, "alpha,beta")
	}
	if m := r.Meta(); m.Name != "CC" {
		t.Errorf("name = %q, want %q", m.Name, "CC")
	}
}

// TestBodyBlankLineIdiom covers the "отбивка": a body line holding one escaped
// space, which editors reduce to a bare backslash. It must render as a line
// that survives HTML whitespace collapsing, and it must not swallow the break
// that follows it.
func TestBodyBlankLineIdiom(t *testing.T) {
	cases := []struct{ in, want string }{
		{"one\n \\\ntwo", "one<br/>&nbsp;<br/>two"},
		{"one\n \\ \ntwo", "one<br/>&nbsp;<br/>two"},
		{"one\ntwo", "one<br/>two"},
	}
	for _, c := range cases {
		got, _, err := transformBody(c.in, "k")
		if err != nil {
			t.Fatalf("transformBody(%q): %v", c.in, err)
		}
		if got != c.want {
			t.Errorf("transformBody(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// Meta.Header is every directive the name and description were not built
// from, in file order: with #NAME and #INDEX_LANGUAGE present, the legacy
// #FULL_NAME and #LANGUAGE are just more header, and #INCLUDE is never header.
func TestReaderHeader(t *testing.T) {
	p := writeDSL(t, "h.dsl", []byte(
		"#NAME\t\"Main\"\n"+
			"#FULL_NAME\t\"Main, long form\"\n"+
			"#INDEX_LANGUAGE\t\"English\"\n"+
			"#CONTENTS_LANGUAGE\t\"Basque\"\n"+
			"#ICON_FILE\t\"h.bmp\"\n"+
			"#LANGUAGE\t\"English\"\n"+
			"#EMPTY\t\"\"\n"+
			"alpha\n\t[m1]x[/m]\n"))
	r, err := NewReader(p)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	want := []dict.Field{{Name: "FULL_NAME", Value: "Main, long form"}, {Name: "ICON_FILE", Value: "h.bmp"}, {Name: "LANGUAGE", Value: "English"}}
	if got := r.Meta().Header; !reflect.DeepEqual(got, want) {
		t.Errorf("Header = %q, want %q", got, want)
	}
}
