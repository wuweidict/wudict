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
		// a `>` inside a quoted value: <base\b[^>]*> and <style>(.*?)</style>
		// patterns both mis-terminate on this
		`<div title="a > b"><span>x</span></div>`,
		// PlayLinks is opt-in: off, a controls-less <audio> is left as written
		`<audio src="a.mp3"><img src="s.png"></audio>`,
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

// An <audio> becomes a play link only where its fallback is a drawn button, its
// source is safe to link and the tokens alone say where it ends; every other
// shape is emitted exactly as without PlayLinks.
func TestPlayLinks(t *testing.T) {
	rw := Rewriter{URL: func(r Ref) string { return r.URL }, PlayLinks: true}
	const btn = `<img src="s.png">`
	link := func(href, body string) string { return `<a class="wu-audio" href="` + href + `">` + body + `</a>` }
	cases := []struct{ name, in, want string }{
		{"LDOCE6 StarDict", `<audio src="a.mp3">` + btn + `</audio>`, link("a.mp3", btn)},
		{"text around it untouched", `x <audio src="a.mp3">` + btn + `</audio>&nbsp;y`, `x ` + link("a.mp3", btn) + `&nbsp;y`},
		{"tag case kept inside", `<AUDIO SRC="a.mp3"><IMG SRC="s.png"></AUDIO>`, link("a.mp3", `<IMG SRC="s.png">`)},
		{"phrasing around the button", `<audio src="a.mp3"><span class="p"><b>` + btn + `</b> UK</span><!--c--></audio>`,
			link("a.mp3", `<span class="p"><b>`+btn+`</b> UK</span><!--c-->`)},
		{"inert attributes allowed", `<audio preload="none" title="UK" class="snd" src="a.mp3">` + btn + `</audio>`, link("a.mp3", btn)},
		{"src from the first child <source>", `<audio><source src="a.ogg"><source src="b.mp3">` + btn + `</audio>`,
			link("a.ogg", `<source src="a.ogg"><source src="b.mp3">`+btn)},
		// The browser plays nothing here (a present src, even empty, wins over
		// <source>); the link plays what the author named.
		{"empty src, child <source> supplies it", `<audio src=""><source src="b.mp3">` + btn + `</audio>`,
			link("b.mp3", `<source src="b.mp3">`+btn)},
		// Template content is inert either way; a script that clones it gets
		// a working button instead of a hidden element.
		{"inside <template>", `<template><audio src="a.mp3">` + btn + `</audio></template>`,
			`<template>` + link("a.mp3", btn) + `</template>`},
		{"first src attribute wins", `<audio src="a.mp3" src="b.mp3">` + btn + `</audio>`, link("a.mp3", btn)},
		{"value re-escaped", `<audio src="a.mp3?x=1&amp;y=&quot;2">` + btn + `</audio>`, link("a.mp3?x=1&amp;y=&#34;2", btn)},
		{"dropped opening quote repaired", `<audio src=a.mp3">` + btn + `</audio>`, link("a.mp3", btn)},
		{"root-absolute", `<audio src="/res/d/a.mp3">` + btn + `</audio>`, link("/res/d/a.mp3", btn)},
		{"https", `<audio src="HTTPS://x.org/a.mp3">` + btn + `</audio>`, link("HTTPS://x.org/a.mp3", btn)},
		{"protocol-relative", `<audio src="//x.org/a.mp3">` + btn + `</audio>`, link("//x.org/a.mp3", btn)},
		{"inline audio", `<audio src="data:audio/mpeg;base64,AAAA">` + btn + `</audio>`, link("data:audio/mpeg;base64,AAAA", btn)},
		{"colon after the path is no scheme", `<audio src="snd/a:b.mp3">` + btn + `</audio>`, link("snd/a:b.mp3", btn)},
		{"end of input closes it", `<audio src="a.mp3">` + btn, link("a.mp3", btn)},
		{"end of input closes open phrasing too", `<audio src="a.mp3"><span>` + btn, link("a.mp3", `<span>`+btn)},
		{"after a closed anchor", `<a href="w">w</a><audio src="a.mp3">` + btn + `</audio>`, `<a href="w">w</a>` + link("a.mp3", btn)},
		{"two in a row", `<audio src="a.mp3">` + btn + `</audio><audio src="b.mp3">` + btn + `</audio>`, link("a.mp3", btn) + link("b.mp3", btn)},

		// ── left as written ─────────────────────────────────────────────────
		{"controls: the browser draws a player", `<audio controls src="a.mp3">` + btn + `</audio>`, ""},
		{"autoplay", `<audio autoplay src="a.mp3">` + btn + `</audio>`, ""},
		{"muted", `<audio muted src="a.mp3">` + btn + `</audio>`, ""},
		{"loop", `<audio loop src="a.mp3">` + btn + `</audio>`, ""},
		{"id: a script plays it", `<audio id="p" src="a.mp3">` + btn + `</audio>`, ""},
		{"event handler", `<audio onended="next()" src="a.mp3">` + btn + `</audio>`, ""},
		{"data- attribute", `<audio data-src="b.mp3" src="a.mp3">` + btn + `</audio>`, ""},
		{"text fallback", `<audio src="a.mp3">Your browser does not support audio.</audio>`, ""},
		{"empty", `<audio src="a.mp3"></audio>`, ""},
		{"no source at all", `<audio>` + btn + `</audio>`, ""},
		{"<source> that is not a child", `<audio><span><source src="a.mp3"></span>` + btn + `</audio>`, ""},
		{"fragment names this article", `<audio src="#x">` + btn + `</audio>`, ""},
		{"query names this article", `<audio src="?x">` + btn + `</audio>`, ""},
		{"self-closing <a/> is still open", `<a/><audio src="a.mp3">` + btn + `</audio>`, ""},
		{"javascript: would run as a link", `<audio src="javascript:alert(1)">` + btn + `</audio>`, ""},
		{"other data: types", `<audio src="data:text/html,x">` + btn + `</audio>`, ""},
		{"unrewritten pseudo-scheme", `<audio src="sound://a.mp3">` + btn + `</audio>`, ""},
		{"inside an anchor", `<a href="a.mp3"><audio src="a.mp3">` + btn + `</audio></a>`, ""},
		{"anchor in the fallback", `<audio src="a.mp3"><a href="a.mp3">` + btn + `</a></audio>`, ""},
		{"script in the fallback", `<audio src="a.mp3">` + btn + `<script>var s="</a>"</script></audio>`, ""},
		{"nested <audio>", `<audio src="a.mp3"><audio src="b.mp3"></audio>` + btn + `</audio>`, ""},
		{"a parent's end tag closes it", `<span><audio src="a.mp3">` + btn + `</span> more <b>def</b>`, ""},
		{"a block start closes it", `<p><audio src="a.mp3">` + btn + `<p>next</audio>`, ""},
		{"mis-nested end tags", `<audio src="a.mp3"><b>` + btn + `</audio></b>`, ""},
		{"malformed tail at end of input", `<audio src="a.mp3">` + btn + `<a href=`, ""},
	}
	for _, c := range cases {
		want := c.want
		if want == "" {
			want = c.in
		}
		if got := rw.Rewrite(c.in); got != want {
			t.Errorf("%s:\n  in   %s\n  got  %s\n  want %s", c.name, c.in, got, want)
		}
	}
}

// Highlighting (the Text hook) sees fallback text exactly as it sees any prose,
// whether the element becomes a link or stays as written.
func TestPlayLinksText(t *testing.T) {
	rw := Rewriter{URL: func(r Ref) string { return r.URL }, PlayLinks: true,
		Text: func(s string) string { return "[" + s + "]" }}
	cases := []struct{ name, in, want string }{
		{"converted", `<audio src="a.mp3"><img src="s.png"> UK</audio> x`,
			`<a class="wu-audio" href="a.mp3"><img src="s.png">[ UK]</a>[ x]`},
		{"left as written", `<audio src="a.mp3">play</audio> x`, `<audio src="a.mp3">[play]</audio>[ x]`},
	}
	for _, c := range cases {
		if got := rw.Rewrite(c.in); got != c.want {
			t.Errorf("%s:\n  got  %s\n  want %s", c.name, got, c.want)
		}
	}
}

// The tokenizer reads raw text after a raw-text tag whether it is self-closing
// or not, and whether or not Drop removes it; the hooks must agree with it, or
// CSS escapes rewriting and a <script> body is handed to Text as prose.
func TestRawTextFollowsTheTokenizer(t *testing.T) {
	rw := Rewriter{
		URL:  func(r Ref) string { return "R/" + r.URL },
		Text: func(s string) string { return "[" + s + "]" },
		Drop: func(tag string) bool { return tag == "noscript" },
	}
	cases := []struct{ name, in, want string }{
		{"self-closing <script/>", `<script/>var a=1</script><p>t</p>`, `<script/>var a=1</script><p>[t]</p>`},
		{"self-closing <style/>", `<style/>a{background:url(bg.png)}</style>`, `<style/>a{background:url(R/bg.png)}</style>`},
		{"dropped raw-text element", `<noscript>x <b>y</b></noscript>z`, `x <b>y</b></noscript>[z]`},
		{"<plaintext> never ends", `<plaintext>a <b>b</b>`, `<plaintext>a <b>b</b>`},
		{"empty raw text", `<style></style><p>t</p>`, `<style></style><p>[t]</p>`},
	}
	for _, c := range cases {
		if got := rw.Rewrite(c.in); got != c.want {
			t.Errorf("%s:\n  in   %s\n  got  %s\n  want %s", c.name, c.in, got, c.want)
		}
	}
}

// Clean is about URL syntax, not HTML quoting: RFC 3986 excludes raw quotes
// from every component of a URI, so a trailing one cannot be part of the name.
func TestClean(t *testing.T) {
	cases := map[string]string{
		`plaintiff__gb_1.ogg"`: "plaintiff__gb_1.ogg",
		`plaintiff__gb_1.ogg'`: "plaintiff__gb_1.ogg",
		"a.png`":               "a.png",
		`  a.png  `:            "a.png",
		"\n\ta.png\r\n":        "a.png",
		`a.png`:                "a.png",
		`"a.png`:               `"a.png`, // leading: a different malformation, left alone
		`say"what.png`:         `say"what.png`,
		``:                     ``,
		`"`:                    ``,
		`sound://x.mp3`:        `sound://x.mp3`,
		`a.png?v=1#frag`:       `a.png?v=1#frag`,
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

// TextIn is handed each prose text node with the document after it: the two
// together, read off the document, are where the tokenizer stands.
func TestTextInRest(t *testing.T) {
	for _, doc := range []string{
		`plain`,
		`<p>one <b>two</b> three</p>`,
		`a<!-- c -->b<script>var s = "<i>x</i>";</script>c<style>p{}</style>d`,
		`<a href="x>y" title='>'>один</a> &amp; <br/>два<img src=a.png>`,
		"line\r\nbreaks <p\n class=x>and\x00nul</p> tail",
		`unclosed <b title="`,
	} {
		at := 0
		rw := Rewriter{TextIn: func(raw, rest string) string {
			i := strings.Index(doc[at:], raw+rest)
			if i < 0 || at+i+len(raw)+len(rest) != len(doc) {
				t.Errorf("%q: text %q with rest %q is not a place in the document", doc, raw, rest)
			}
			at += i + len(raw)
			return raw
		}}
		if got := rw.Rewrite(doc); got != doc {
			t.Errorf("Rewrite(%q) = %q", doc, got)
		}
	}
}
