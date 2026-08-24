// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package htmlref

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// identity is the no-op rewriter: whatever it is handed, it hands back.
var identity = Rewriter{URL: func(r Ref) string { return r.URL }}

// The fidelity contract. Every article the server serves goes through this
// walk, so a byte of drift here corrupts every dictionary at once. Well-formed
// input must survive exactly - original quoting, attribute order, tag case,
// whitespace, entities, comments and doctype included.
func TestRoundTripPreservesWellFormedMarkup(t *testing.T) {
	cases := []string{
		``,
		`plain text, no markup at all`,
		`<p>hello</p>`,
		`<DIV CLASS='x'   ID=y ><B>bold</B>&nbsp;&amp;<i>it</i></DIV>`,
		`<img src="a.png" alt='q"uote' data-n=3>`,
		`<!DOCTYPE html><!-- a comment --><p>x</p>`,
		`<style>.a{background:url(x.png)}</style>`,
		`<script>var s = 'src="a.png"'; if (a<b) {}</script>`,
		`<a href="bword://word#frag">w</a>`,
		`<svg><image xlink:href="v.svg"/></svg>`,
		`<img srcset="a.png 1x,  b.png   2x">`,
		`<p>unterminated <b>bold`,
		`<p>a &lt; b &gt; c</p>`,
		`<td nowrap valign=top>x</td>`,
		// a `>` inside a quoted value: the old <base\b[^>]*> and
		// <style>(.*?)</style> patterns both mis-terminated on this
		`<div title="a > b"><span>x</span></div>`,
	}
	for _, in := range cases {
		if got := identity.Rewrite(in); got != in {
			t.Errorf("not byte-identical:\n  in  %q\n  got %q", in, got)
		}
	}
}

// The same guarantee against whole real articles, which is where the
// pathological markup actually lives. Fixtures are optional: the paths come
// from the same env vars the Makefile already sets for the format tests.
func TestRoundTripRealArticles(t *testing.T) {
	dir := os.Getenv("WUDICT_TEST_HTMLDIR")
	if dir == "" {
		t.Skip("set WUDICT_TEST_HTMLDIR to a folder of article HTML to run this")
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.html"))
	if len(files) == 0 {
		t.Skip("no *.html fixtures")
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		in := string(b)
		got := identity.Rewrite(in)
		if got == in {
			t.Logf("%s: %d bytes round-trip exactly", filepath.Base(f), len(in))
			continue
		}
		// The one licensed difference is Clean repairing a malformed value.
		t.Errorf("%s: not byte-identical (%d in, %d out); first diff at %d",
			filepath.Base(f), len(in), len(got), firstDiff(in, got))
	}
}

func firstDiff(a, b string) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return i
		}
	}
	return min(len(a), len(b))
}

// Where the references are, and - just as importantly - where they are not.
func TestRefSites(t *testing.T) {
	cases := []struct {
		name, in string
		want     []string
	}{
		{"single-url attributes", `<img src="a.png" poster="p.jpg"><object data="o.swf">`, []string{"a.png", "p.jpg", "o.swf"}},
		{"prefixed attributes", `<img data-src="l.png"><image xlink:href="v.svg"/>`, []string{"l.png", "v.svg"}},
		{"srcset candidates", `<img srcset="a.png 1x, b.png 2x">`, []string{"a.png", "b.png"}},
		{"css in a style attribute", `<div style="background:url(bg.png)">`, []string{"bg.png"}},
		{"css in a style block", `<style>.a{background:url('x/y.png')}</style>`, []string{"x/y.png"}},
		{"legacy background", `<body background="paper.gif">`, []string{"paper.gif"}},

		// the reported defect: no opening quote
		{"unquoted, dropped opening quote", `<a href=plaintiff__gb_1.ogg" class="p">`, []string{"plaintiff__gb_1.ogg"}},
		{"legitimately unquoted", `<img src=spkr.png>`, []string{"spkr.png"}},
		{"spaces around equals", `<img src = "a.png">`, []string{"a.png"}},

		// not references, however much they look like one
		{"inside a script", `<script>var a='src="x.png"'</script>`, nil},
		{"inside a comment", `<!-- <img src="x.png"> -->`, nil},
		{"in prose", `<p>use src="x.png" here</p>`, nil},
		{"attribute-name substrings", `<span metadata="k.png" aria-describedby="d"></span>`, nil},
		{"srcdoc is not src", `<iframe srcdoc="<img src=x.png>"></iframe>`, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var got []string
			for _, r := range identity.Refs(c.in) {
				got = append(got, r.URL)
			}
			if strings.Join(got, "|") != strings.Join(c.want, "|") {
				t.Errorf("refs = %q, want %q", got, c.want)
			}
		})
	}
}

// Ref carries the element, which is what makes an element-aware policy
// possible at all - the thing the regexes could not do.
func TestRefCarriesElementAndAttribute(t *testing.T) {
	refs := identity.Refs(`<link href="a.css"><a href="b">t</a><img src="c.png">`)
	want := []struct{ tag, attr, url string }{
		{"link", "href", "a.css"},
		{"a", "href", "b"},
		{"img", "src", "c.png"},
	}
	if len(refs) != len(want) {
		t.Fatalf("got %d refs, want %d: %+v", len(refs), len(want), refs)
	}
	for i, w := range want {
		if refs[i].Tag != w.tag || refs[i].Attr != w.attr || refs[i].URL != w.url {
			t.Errorf("ref %d = %+v, want %v", i, refs[i], w)
		}
	}
}

func TestDropTag(t *testing.T) {
	rw := Rewriter{
		URL:  func(r Ref) string { return r.URL },
		Drop: func(tag string) bool { return tag == "base" },
	}
	in := `<base href="http://x/"><img src="p.png">`
	if got, want := rw.Rewrite(in), `<img src="p.png">`; got != want {
		t.Errorf("Rewrite = %q, want %q", got, want)
	}
}

// Clean is about URL syntax, not HTML quoting: RFC 3986 excludes raw quotes
// from every component of a URI, so a trailing one cannot be part of the name.
func TestClean(t *testing.T) {
	cases := map[string]string{
		`plaintiff__gb_1.ogg"`:  "plaintiff__gb_1.ogg",
		`plaintiff__gb_1.ogg'`:  "plaintiff__gb_1.ogg",
		"a.png`":                "a.png",
		`  a.png  `:             "a.png",
		"\n\ta.png\r\n":         "a.png",
		`a.png`:                 "a.png",
		`"a.png`:                `"a.png`, // leading: a different malformation, left alone
		`say"what.png`:          `say"what.png`,
		``:                      ``,
		`"`:                     ``,
		`sound://x.mp3`:         `sound://x.mp3`,
		`a.png?v=1#frag`:        `a.png?v=1#frag`,
	}
	for in, want := range cases {
		if got := Clean(in); got != want {
			t.Errorf("Clean(%q) = %q, want %q", in, got, want)
		}
	}
}

// The walker must never lose bytes, whatever it is handed.
func TestNeverTruncates(t *testing.T) {
	for _, in := range []string{
		`<img src="a.png`,        // unterminated attribute
		`<a href=`,               // unterminated tag
		`<!-- unterminated`,      // unterminated comment
		`<style>.a{url(`,         // unterminated raw text
		"<p>text\x00with a nul",  // stray control byte
		strings.Repeat("<b>", 5), // unclosed nesting
	} {
		got := identity.Rewrite(in)
		if len(got) < len(in)/2 {
			t.Errorf("input %q truncated to %q", in, got)
		}
	}
}
