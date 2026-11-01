// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package mdx

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wuweidict/wudict/internal/dict"
)

func TestFold(t *testing.T) {
	cases := map[string]string{
		"Corazón":  "corazon",
		"ÁÉÍÓÚÜÑ":  "aeiouun",
		"straße":   "straße", // ß is not a combining mark: preserved
		"already":  "already",
		"Ître-Œuf": "itre-œuf",
	}
	for in, want := range cases {
		if got := fold(in); got != want {
			t.Errorf("fold(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseStylesheet(t *testing.T) {
	m := parseStylesheet("1\r\n<b>\r\n</b>\r\n2\r\n<i>\r\n</i>")
	want := map[int][2]string{1: {"<b>", "</b>"}, 2: {"<i>", "</i>"}}
	if len(m) != 2 || m[1] != want[1] || m[2] != want[2] {
		t.Errorf("styles = %v, want %v", m, want)
	}
	// MdxBuilder's sample: an empty end string, and a token above 255 costs
	// only its own triplet.
	m = parseStylesheet("2\n<br>\n\n300\n<x>\n</x>\n3\n<f>\n</f>\n")
	if len(m) != 2 || m[2] != [2]string{"<br>", ""} || m[3] != [2]string{"<f>", "</f>"} {
		t.Errorf("styles = %v", m)
	}
	if parseStylesheet("  \n ") != nil {
		t.Error("blank stylesheet should parse to nil")
	}
}

// TestSubstituteStylesheet pins MDict's reFormat (docs/mdict/SEMANTICS.md
// §3.1), the manual's own example first.
func TestSubstituteStylesheet(t *testing.T) {
	manual := map[int][2]string{
		1: {"<font size=5>", "</font>"},
		2: {"<br>", ""},
		3: {`<font face="Kingsoft Phonetic Plain, Tahoma">`, "</font>"},
	}
	for _, tc := range []struct {
		name, in, want string
		styles         map[int][2]string
	}{
		{"manual example", "`1`whole&#96;s`2`<p>`3`(hol,hJl; houl)",
			`<font size=5>whole&#96;s</font><br><p><font face="Kingsoft Phonetic Plain, Tahoma">(hol,hJl; houl)</font>`, manual},
		{"text before the first token is kept", "plain `1`bold", "plain <font size=5>bold</font>", manual},
		{"an undefined token keeps its text", "a`9`x`1`y", "ax<font size=5>y</font>", manual},
		{"whitespace is left alone", "`1`line\r\n", "<font size=5>line\r\n</font>", manual},
		{"an empty segment still gets its strings", "`1``2`z", "<font size=5></font><br>z", manual},
		{"a trailing token emits nothing", "x`1`", "x", manual},
		{"no token, no style 0", "just text", "just text", manual},
		{"style 0 wraps the lead", "a`1`b", "[a]<font size=5>b</font>", map[int][2]string{0: {"[", "]"}, 1: manual[1]}},
		{"no stylesheet strips the tokens", "`1`a`2`b", "ab", nil},
		{"a lone backtick is text", "it`s `x` fine", "it`s `x` fine", manual},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := substituteStylesheet(tc.in, tc.styles); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// TestTextToHTML pins MDict's rendering of a plain-text dictionary.
func TestTextToHTML(t *testing.T) {
	for in, want := range map[string]string{
		"a < b & \"c\" > d":     "a &lt; b &amp; &quot;c&quot; &gt; d",
		"line one\nline two":    "line one<br>line two",
		`adj.好\nadj.贼好`:         "adj.好<br>adj.贼好",
		`drop \x and \é here`:   "drop  and  here",
		"trailing backslash \\": "trailing backslash ",
		"<b>not markup</b>":     "&lt;b&gt;not markup&lt;/b&gt;",
	} {
		if got := textToHTML(in); got != want {
			t.Errorf("textToHTML(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestArticle: a redirect is recognised before the content type applies; a
// plain-text dictionary is escaped; StyleSheet tokens apply only when the
// dictionary is compact.
func TestArticle(t *testing.T) {
	styles := map[int][2]string{1: {"<b>", "</b>"}}
	for _, tc := range []struct {
		name          string
		d             *Dict
		raw, body, to string
		link          bool
	}{
		{"html", &Dict{styles: styles}, "`1`x <i>y</i>\x00", "`1`x <i>y</i>", "", false},
		{"compact", &Dict{compact: true, styles: styles}, "`1`x", "<b>x</b>", "", false},
		{"plain text", &Dict{plain: true, compact: true, styles: styles}, "a<b\nc", "a&lt;b<br>c", "", false},
		{"redirect in a plain-text dictionary", &Dict{plain: true}, "@@@LINK=target\r\n", "", "target", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, to, link := tc.d.article([]byte(tc.raw))
			if body != tc.body || to != tc.to || link != tc.link {
				t.Errorf("article(%q) = %q, %q, %v; want %q, %q, %v", tc.raw, body, to, link, tc.body, tc.to, tc.link)
			}
		})
	}
}

// testMdx returns the integration-test dictionary path, skipping when the
// WUDICT_TEST_MDX env var is unset or the file is missing.
func testMdx(t *testing.T) string {
	t.Helper()
	p := os.Getenv("WUDICT_TEST_MDX")
	if p == "" {
		t.Skip("WUDICT_TEST_MDX not set; skipping integration test")
	}
	if _, err := os.Stat(p); err != nil {
		t.Skipf("WUDICT_TEST_MDX=%s not readable: %v", p, err)
	}
	return p
}

// BenchmarkExactWarm measures a repeated in-process lookup - with the record
// block cache this should be microseconds; without it, each op re-opened the
// file and re-decompressed the whole record block (~1 ms, per the audit).
func BenchmarkExactWarm(b *testing.B) {
	p := os.Getenv("WUDICT_TEST_MDX")
	if p == "" {
		b.Skip("WUDICT_TEST_MDX not set")
	}
	d, err := Open(p)
	if err != nil {
		b.Fatal(err)
	}
	defer d.Close()
	keys := d.Keywords(d.Meta().EntryCount/2, 1)
	if len(keys) == 0 {
		b.Skip("no keywords")
	}
	w := keys[0]
	if _, err := d.Exact(w, 1); err != nil { // warm the block cache
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := d.Exact(w, 1); err != nil {
			b.Fatal(err)
		}
	}
}

func TestIntegrationOpenLookup(t *testing.T) {
	d, err := Open(testMdx(t))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	m := d.Meta()
	if m.EntryCount == 0 || m.Name == "" || m.Format != "mdx" {
		t.Fatalf("bad meta: %+v", m)
	}

	// every dictionary must resolve some early headword exactly
	keys := d.Keywords(m.EntryCount/2, 5)
	if len(keys) == 0 {
		t.Fatal("no keywords at mid offset")
	}
	res, err := d.Exact(keys[0], 10)
	if err != nil || len(res) == 0 {
		t.Fatalf("Exact(%q): res=%d err=%v", keys[0], len(res), err)
	}
	if strings.TrimSpace(res[0].Body) == "" {
		t.Errorf("Exact(%q): empty body", keys[0])
	}
	if strings.HasPrefix(res[0].Body, "@@@LINK=") {
		t.Errorf("Exact(%q): unresolved @@@LINK leaked to output", keys[0])
	}

	// prefix on a truncated headword must return something
	if len(keys[0]) > 3 {
		pres, err := d.Prefix(keys[0][:3], 10)
		if err != nil || len(pres) == 0 {
			t.Errorf("Prefix(%q): res=%d err=%v", keys[0][:3], len(pres), err)
		}
	}

	// missing word behaves, does not panic
	if r, _ := d.Exact("zzzz-no-such-word-zzzz", 5); len(r) != 0 {
		t.Errorf("expected no results, got %d", len(r))
	}
}

func TestIntegrationResource(t *testing.T) {
	d, err := Open(testMdx(t))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	idx := d.resourceIndex()
	if len(idx) == 0 {
		t.Skip("dictionary has no .mdd resources")
	}
	var name string
	for k := range idx {
		name = k
		break
	}
	rc, _, err := d.Resource(name)
	if err != nil {
		t.Fatalf("Resource(%q): %v", name, err)
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil || len(data) == 0 {
		t.Fatalf("Resource(%q): %d bytes, err=%v", name, len(data), err)
	}
	if _, _, err := d.Resource("no/such/resource.xyz"); err == nil {
		t.Error("missing resource should error")
	}
	if _, _, err := d.Resource("../../etc/passwd"); err == nil {
		t.Error("path traversal must be rejected")
	}
}

// MDict serves files sitting next to the .mdx - that is how repacks ship their
// stylesheet and scripts (LDOCE6 keeps LDOCE6.css and entry.js loose, with no
// .mdd at all). Packed resources still win; the disk is only consulted on a
// miss, and only for things a dictionary legitimately loads.
func TestLooseSiblingAssets(t *testing.T) {
	dir := t.TempDir()
	mdxPath := filepath.Join(dir, "d.mdx")
	d := &Dict{meta: dict.Meta{Path: mdxPath}}

	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("LDOCE6.css", "span{color:red}")
	write("entry.js", "function toggle(){}")
	write("secrets.env", "TOKEN=hunter2")
	if err := os.MkdirAll(filepath.Join(dir, "img"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(filepath.Join("img", "a.png"), "PNG")

	read := func(name string) (string, string, error) {
		rc, mt, err := d.Resource(name)
		if err != nil {
			return "", "", err
		}
		defer rc.Close()
		b, _ := io.ReadAll(rc)
		return string(b), mt, nil
	}

	// served, with the case of the name preserved (a case-sensitive filesystem
	// would not find "ldoce6.css")
	if body, mt, err := read("LDOCE6.css"); err != nil || body != "span{color:red}" {
		t.Fatalf("loose css: %q %q %v", body, mt, err)
	} else if !strings.Contains(mt, "css") {
		t.Errorf("mime = %q, want text/css", mt)
	}
	if body, _, err := read("entry.js"); err != nil || body != "function toggle(){}" {
		t.Fatalf("loose js: %q %v", body, err)
	}
	if body, _, err := read("img/a.png"); err != nil || body != "PNG" {
		t.Fatalf("loose file in a subfolder: %q %v", body, err)
	}

	// not an asset type a dictionary loads: never served, even though it exists
	if _, _, err := read("secrets.env"); err == nil {
		t.Error("a sibling file outside the asset allowlist must not be served")
	}
	// traversal stays blocked
	for _, bad := range []string{"../outside.css", "../../etc/passwd", "/etc/passwd"} {
		if _, _, err := read(bad); err == nil {
			t.Errorf("traversal not blocked: %q", bad)
		}
	}
	// a name that matches nothing is still a plain miss
	if _, _, err := read("nope.css"); err == nil {
		t.Error("a missing file must report not-found")
	}

	// The loose file wins over a packed one of the same name, as in MDict:
	// that is how a user overrides a dictionary's stylesheet. The packed entry
	// here has no container behind it, so reaching it would fail the read.
	d.resOnce.Do(func() { d.resMap = map[string]resHit{"ldoce6.css": {}} })
	if body, _, err := read("LDOCE6.css"); err != nil || body != "span{color:red}" {
		t.Fatalf("loose file over a packed one: %q %v", body, err)
	}

	// The folder listing that spares packed lookups a failed open must not
	// hide a file dropped in later: it is re-validated once its time is up.
	if _, _, err := read("late.css"); err == nil {
		t.Fatal("late.css found before it exists")
	}
	write("late.css", "b{}")
	d.loose.at = time.Now().Add(-looseTTL)
	if body, _, err := read("late.css"); err != nil || body != "b{}" {
		t.Fatalf("a file added after the listing: %q %v", body, err)
	}
	// Names compare as a case-insensitive filesystem compares them.
	if !d.loose.mayBeLoose(dir, "ldoce6.CSS") || d.loose.mayBeLoose(dir, "absent/x.png") {
		t.Error("listing comparison")
	}
}

// TestLinkTarget pins redirect detection against the record shapes real
// dictionaries ship, including the wrapped one that once put the literal text
// "@@@LINK=pandore" on screen under the headword the user had searched for
// (the OED repack on freemdict gives every record the same HTML shell).
func TestLinkTarget(t *testing.T) {
	cases := []struct {
		name   string
		body   string
		target string
		isLink bool
	}{
		{"bare", "@@@LINK=pandore", "pandore", true},
		{"trailing crlf and nul", "@@@LINK=pandore\r\n\x00", "pandore", true},
		{"wrapped in the article shell",
			"<head><link rel=\"stylesheet\" type=\"text/css\" href=\"OED.css\"/></head>\r\n" +
				"<div class=\"entry\">@@@LINK=pandore</div entry>\r\n",
			"pandore", true},
		{"wrapped with a stylesheet block",
			"<head><style>.entry{color:#000}</style></head><div>@@@LINK=take off</div>",
			"take off", true},
		{"entity in the target", "<div>@@@LINK=AT&amp;T</div>", "AT&T", true},
		{"target padded", "<div>  @@@LINK= pandore </div>", "pandore", true},
		{"redirect to nowhere", "<div>@@@LINK=</div>", "", true},
		// The article keeps its body: text on either side of the marker is
		// exactly what tells a real entry from a pointer.
		{"mentioned after prose", "<div>A redirect is written @@@LINK=word</div>", "", false},
		{"prose after the marker", "<div>@@@LINK=word</div><p>and then some prose</p>", "", false},
		{"no marker", "<div class=\"entry\">pandora, a lute</div>", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			target, ok := linkTarget(c.body)
			if ok != c.isLink || target != c.target {
				t.Errorf("linkTarget(%q) = %q, %v; want %q, %v", c.body, target, ok, c.target, c.isLink)
			}
		})
	}
}
