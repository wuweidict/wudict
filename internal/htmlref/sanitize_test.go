// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package htmlref

import "testing"

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
				t.Errorf("Sanitize(%q) = %q — still contains %q", in, got, bad)
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
			if got := Text(tc.in); got != tc.want {
				t.Errorf("Text(%q)\n got %q\nwant %q", tc.in, got, tc.want)
			}
		})
	}
}
