// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package htmlref

import (
	"testing"

	"golang.org/x/net/html"
)

// testPolicy mirrors the shape wudict uses: a small keep set, a small drop set,
// everything else unwrapped, and an attribute allowlist.
func testPolicy() Policy {
	keep := map[string]bool{"p": true, "b": true, "a": true, "img": true,
		"div": true, "li": true, "br": true}
	drop := map[string]bool{"script": true, "style": true, "link": true, "form": true, "svg": true}
	return Policy{
		Tag: func(tag string) TagAction {
			switch {
			case drop[tag]:
				return TagDrop
			case keep[tag]:
				return TagKeep
			default:
				return TagUnwrap
			}
		},
		Attr: func(tag, name, val string) (string, bool) {
			switch name {
			case "href", "src", "title":
				return val, true
			}
			return "", false
		},
	}
}

func TestSanitize(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"script goes with its body",
			`<p>a</p><script>alert(1)</script><p>b</p>`,
			`<p>a</p><p>b</p>`},
		{"style goes with its body",
			`<style>.x{color:red}</style><p>a</p>`,
			`<p>a</p>`},
		{"link is void: dropping it must not swallow what follows",
			`<link rel="stylesheet" href="/res/x/a.css"><p>kept</p>`,
			`<p>kept</p>`},
		{"unknown element is unwrapped, its text survives",
			`<font color="red">word</font>`,
			`word`},
		{"custom element unwrapped",
			`<x-sense><p>def</p></x-sense>`,
			`<p>def</p>`},
		{"nested drop of the same tag does not end early",
			`<form>a<form>b</form>c</form><p>after</p>`,
			`<p>after</p>`},
		{"drop containing a keeper still drops the keeper",
			`<svg><p>inside</p></svg><p>outside</p>`,
			`<p>outside</p>`},
		{"event handlers are dropped",
			`<p onclick="steal()" onmouseover="x">t</p>`,
			`<p>t</p>`},
		{"presentational attributes are dropped",
			`<p class="ldoce" style="color:red" data-x="1">t</p>`,
			`<p>t</p>`},
		{"allowed attributes survive",
			`<a href="bword://run" title="go">run</a>`,
			`<a href="bword://run" title="go">run</a>`},
		{"attribute value cannot break out of its quotes",
			`<a href="x&quot; onclick=&quot;alert(1)">t</a>`,
			`<a href="x&#34; onclick=&#34;alert(1)">t</a>`},
		{"mis-nested unwrapped element leaks no stray end tag",
			`<b><font>x</b></font>`,
			`<b>x</b>`},
		{"self-closing keeper stays self-closing",
			`<img src="/res/x/a.png"/>`,
			`<img src="/res/x/a.png"/>`},
		{"void keeper is emitted without an end tag",
			`<p>a<br>b</p>`,
			`<p>a<br/>b</p>`},
		{"comments are dropped",
			`<p>a</p><!-- <script>x</script> --><p>b</p>`,
			`<p>a</p><p>b</p>`},
		{"text entities are left escaped, not double-escaped",
			`<p>a &amp; b &lt; c</p>`,
			`<p>a &amp; b &lt; c</p>`},
		{"empty input", ``, ``},
		{"text with no markup at all", `bare words`, `bare words`},
	}
	p := testPolicy()
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Sanitize(tc.in, p); got != tc.want {
				t.Errorf("Sanitize(%q)\n got %q\nwant %q", tc.in, got, tc.want)
			}
		})
	}
}

// A sanitiser must not be defeatable by markup a browser still renders. Each
// input here is a shape that has historically slipped past naive filters.
func TestSanitizeResistsEvasion(t *testing.T) {
	p := testPolicy()
	for _, in := range []string{
		`<SCRIPT>alert(1)</SCRIPT>`,
		`<ScRiPt>alert(1)</ScRiPt>`,
		`<script src="x.js"></script>`,
		`<p ONCLICK="alert(1)">t</p>`,
		`<p OnClIcK="alert(1)">t</p>`,
		`<div><script>a</script></div>`,
		`<script><script>alert(1)</script>`,
		`<p title="a" onclick=alert(1)>t</p>`,
	} {
		got := Sanitize(in, p)
		for _, bad := range []string{"alert", "onclick", "script", "ONCLICK", "SCRIPT"} {
			if containsFold(got, bad) {
				t.Errorf("Sanitize(%q) = %q - still contains %q", in, got, bad)
			}
		}
	}
}

func containsFold(s, sub string) bool {
	ls, lsub := []rune(s), []rune(sub)
	if len(lsub) > len(ls) {
		return false
	}
	lower := func(r rune) rune {
		if r >= 'A' && r <= 'Z' {
			return r + 32
		}
		return r
	}
	for i := 0; i+len(lsub) <= len(ls); i++ {
		ok := true
		for j := range lsub {
			if lower(ls[i+j]) != lower(lsub[j]) {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

// stylePolicy is testPolicy plus the CSS hook, shaped exactly as the server's
// clean policy shapes it: a hidden class is dropped with its content, a block
// class becomes a <div>, and <span> is bare-unwrapped when nothing survives.
func stylePolicy(st Styles) Policy {
	p := testPolicy()
	p.Bare = func(tag string) bool { return tag == "span" }
	p.Elem = func(act TagAction, tag string, attrs []html.Attribute) (TagAction, string) {
		switch st.Class(classAttr(attrs)) {
		case DisplayNone:
			return TagDrop, tag
		case DisplayBlock:
			if tag == "span" {
				return TagKeep, "div"
			}
			return TagKeep, tag
		}
		return act, tag
	}
	return p
}

// The bug this exists to fix: LDOCE's senses, examples and collocations are
// <span>s that are blocks only because its stylesheet says so, and its internal
// field codes are <span>s that are invisible for the same reason. Strip the
// stylesheet without reading it and the entry becomes one run of text with the
// field codes spliced into it.
func TestSanitizeAppliesStyles(t *testing.T) {
	st := Styles{"Sense": DisplayBlock, "EXAMPLE": DisplayBlock, "FIELD": DisplayNone}
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"a block class becomes a real block",
			`<span class="Sense">one</span><span class="Sense">two</span>`,
			`<div>one</div><div>two</div>`},
		{"a hidden class goes with its content",
			`<span class="FIELD">TT</span><span class="ACTIV">TRAVEL</span>`,
			`TRAVEL`},
		{"the boundary survives an attribute-less block",
			`<span class="Sense"><span>x</span></span>`,
			`<div>x</div>`},
		{"a kept attribute rides along the renamed element",
			`<span class="Sense" title="sense 1">x</span>`,
			`<div title="sense 1">x</div>`},
		{"an unstyled span is still 13 bytes of nothing",
			`<span class="HWD">x</span>`, `x`},
		{"a renamed element closes where it opened, not where the stack ends",
			`<span>x<span class="Sense">y</span>z</span>`,
			`x<div>y</div>z`},
		{"nested blocks nest",
			`<span class="Sense">a<span class="EXAMPLE">b</span></span>`,
			`<div>a<div>b</div></div>`},
		{"a hidden subtree cannot be re-opened by a block inside it",
			`<span class="FIELD">a<span class="Sense">b</span>c</span>d`,
			`d`},
		{"elements that carry their own meaning are never renamed",
			`<a href="x" class="Sense">go</a>`,
			`<a href="x">go</a>`},
		{"no class, no change", `<p>plain</p>`, `<p>plain</p>`},
	}
	p := stylePolicy(st)
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Sanitize(tc.in, p); got != tc.want {
				t.Errorf("Sanitize(%q)\n got %q\nwant %q", tc.in, got, tc.want)
			}
		})
	}
}

// Nothing a stylesheet says may talk a <script> back into the output: Elem is
// never consulted for an element the tag policy already dropped.
func TestStylesCannotRescueADroppedElement(t *testing.T) {
	p := stylePolicy(Styles{"x": DisplayBlock})
	if got := Sanitize(`<script class="x">alert(1)</script><p>a</p>`, p); got != `<p>a</p>` {
		t.Errorf("a class revived a dropped element: %q", got)
	}
}

func TestText(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"tags removed, text kept",
			`<p>hello <b>world</b></p>`, "hello world"},
		{"script and style contribute nothing",
			`<script>var a=1</script><p>def</p><style>.x{}</style>`, "def"},
		{"block boundaries become newlines",
			`<p>one</p><p>two</p>`, "one\ntwo"},
		{"list items separate",
			`<ul><li>a</li><li>b</li></ul>`, "a\nb"},
		{"entities are decoded",
			`<p>a &amp; b &mdash; c</p>`, "a & b — c"},
		{"whitespace collapses",
			"<p>a   \n\t b</p>", "a b"},
		{"no runaway blank lines",
			`<div><div><div><p>x</p></div></div></div>`, "x"},
		{"empty", ``, ``},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Text(tc.in, nil); got != tc.want {
				t.Errorf("Text(%q)\n got %q\nwant %q", tc.in, got, tc.want)
			}
		})
	}
}

// Without the stylesheet, a dictionary whose entry is one <span> per sense has
// no block tag anywhere in it and collapses to a single line.
func TestTextAppliesStyles(t *testing.T) {
	st := Styles{"Sense": DisplayBlock, "FIELD": DisplayNone}
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"styled spans break the line",
			`<span class="Sense">one</span><span class="Sense">two</span>`,
			"one\ntwo"},
		{"text after a block is not swallowed by it",
			`<span class="Sense">one</span>after`, "one\nafter"},
		{"hidden classes contribute nothing",
			`<span class="FIELD">TT</span>TRAVEL`, "TRAVEL"},
		{"a hidden subtree stays hidden throughout",
			`<span class="FIELD">a<span class="Sense">b</span>c</span>d`, "d"},
		{"block tags still work", `<p>a</p><p>b</p>`, "a\nb"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Text(tc.in, st); got != tc.want {
				t.Errorf("Text(%q)\n got %q\nwant %q", tc.in, got, tc.want)
			}
		})
	}
	// The same input without a stylesheet is the old, wrong answer - asserted
	// so the fixture cannot silently stop demonstrating the bug.
	if got := Text(`<span class="Sense">one</span><span class="Sense">two</span>`, nil); got != "onetwo" {
		t.Errorf("unstyled Text = %q, want %q", got, "onetwo")
	}
}
