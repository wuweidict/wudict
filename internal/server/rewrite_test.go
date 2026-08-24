// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import "testing"

// The cases are whole ELEMENTS, not bare `href="…"` fragments as they once
// were. That is not cosmetic: the rewriter parses a document now instead of
// matching text in it, so an attribute has to be attached to something. The
// old shape was itself a symptom - a pattern that matches `href="…"` anywhere
// also matches it in prose, in a comment and inside a <script> string, and did.
func TestRewriteEntryHTML(t *testing.T) {
	const id = "001598b628f5"
	R := "/res/" + id + "/"
	cases := []struct {
		name, in, want string
	}{
		// ── pseudo-schemes and relative forms all land under /res/ ──────────
		{"sound scheme", `<a href="sound://hwd/ame/6/abet.mp3">x</a>`, `<a href="` + R + `hwd/ame/6/abet.mp3">x</a>`},
		{"sound scheme uppercase", `<a href="SOUND://a.mp3">x</a>`, `<a href="` + R + `a.mp3">x</a>`},
		{"AHD5 triple-slash sound", `<a href="sound:///wavs/W0203400.wav">x</a>`, `<a href="` + R + `wavs/W0203400.wav">x</a>`},
		{"MW11 spx", `<a href="sound://woman003.spx">`, `<a href="` + R + `woman003.spx">`},
		{"file scheme", `<img src="file://img/x.png">`, `<img src="` + R + `img/x.png">`},
		{"server absolute", `<img src="/img/x.png">`, `<img src="` + R + `img/x.png">`},
		{"bare relative", `<img src="spkr_r.png">`, `<img src="` + R + `spkr_r.png">`},
		{"dot relative", `<link href="./LAAD3.css">`, `<link href="` + R + `LAAD3.css">`},
		{"object data", `<object data="ame/abet.mp3"></object>`, `<object data="` + R + `ame/abet.mp3"></object>`},

		// ── the reported bug: a dropped opening quote ───────────────────────
		// The tokenizer reads this exactly as a browser does - unquoted value,
		// ending at whitespace, stray quote included - and Clean removes the
		// quote, which RFC 3986 forbids in a URI anyway. The old regex, which
		// required a quoted value, could not see the attribute at all.
		{"unquoted with dropped opening quote (OALD10)",
			`<a onclick="new Audio(this.href).play()" href=plaintiff__gb_1.ogg" class="pron">`,
			`<a onclick="new Audio(this.href).play()" href="` + R + `plaintiff__gb_1.ogg" class="pron">`},
		{"legitimately unquoted value", `<img src=spkr.png>`, `<img src="` + R + `spkr.png">`},
		{"spaces around equals", `<img src = "a.png">`, `<img src="` + R + `a.png">`},

		// ── element-aware: a link is not a resource ─────────────────────────
		// <a href="defendant"> is a headword in a slob dictionary. The old
		// regex turned it into /res/{dict}/defendant, which could only 404.
		{"bare cross-reference on <a> untouched", `<a href="defendant">defendant</a>`, `<a href="defendant">defendant</a>`},
		{"pathy cross-reference on <a> untouched", `<a href="collocations/plaintiff">c</a>`, `<a href="collocations/plaintiff">c</a>`},
		{"audio on <a> IS a resource", `<a href="plaintiff__gb_1.ogg">p</a>`, `<a href="` + R + `plaintiff__gb_1.ogg">p</a>`},
		{"image on <a> IS a resource", `<a href="plate.jpg">p</a>`, `<a href="` + R + `plate.jpg">p</a>`},
		{"stylesheet on <link> IS a resource", `<link rel="stylesheet" href="oald10.css">`, `<link rel="stylesheet" href="` + R + `oald10.css">`},
		// OALD10 hangs its cross-references on a <span>, not an <a>. An href
		// on a non-fetching element is still a reference, not a resource.
		{"href on a <span> untouched", `<span class="xr-g" href="defendant_e">d</span>`, `<span class="xr-g" href="defendant_e">d</span>`},
		{"href on a <span> naming a file IS a resource", `<span href="beep.mp3">b</span>`, `<span href="` + R + `beep.mp3">b</span>`},
		{"svg use IS a resource", `<use href="icons.svg#play"/>`, `<use href="` + R + `icons.svg#play"/>`},

		// ── schemes and shapes left alone ───────────────────────────────────
		{"bword untouched", `<a href="bword://abandon">a</a>`, `<a href="bword://abandon">a</a>`},
		{"entry untouched", `<a href="entry://x">a</a>`, `<a href="entry://x">a</a>`},
		{"bword no slashes untouched", `<a href="bword:abandon">a</a>`, `<a href="bword:abandon">a</a>`},
		{"bword with fragment untouched", `<a href="bword://word#sense2">a</a>`, `<a href="bword://word#sense2">a</a>`},
		{"d-link untouched", `<a href="d:other">a</a>`, `<a href="d:other">a</a>`},
		{"http untouched", `<a href="http://x.com/a.mp3">a</a>`, `<a href="http://x.com/a.mp3">a</a>`},
		{"data-uri untouched", `<img src="data:image/png;base64,AA">`, `<img src="data:image/png;base64,AA">`},
		{"fragment untouched", `<a href="#anchor">a</a>`, `<a href="#anchor">a</a>`},
		{"protocol-relative untouched", `<script src="//cdn.x/y.js"></script>`, `<script src="//cdn.x/y.js"></script>`},
		{"already absolute", `<img src="` + R + `a.png">`, `<img src="` + R + `a.png">`},
		{"already relative", `<img src="res/` + id + `/a.png">`, `<img src="res/` + id + `/a.png">`},

		// ── attribute-name discrimination ───────────────────────────────────
		{"metadata untouched", `<span metadata="keep.png">x</span>`, `<span metadata="keep.png">x</span>`},
		{"describedby untouched", `<i aria-describedby="d1"></i>`, `<i aria-describedby="d1"></i>`},
		{"srcdoc untouched", `<iframe srcdoc="<p>hi</p>"></iframe>`, `<iframe srcdoc="<p>hi</p>"></iframe>`},
		{"data-src rewritten", `<img data-src="lazy.png">`, `<img data-src="` + R + `lazy.png">`},
		{"xlink:href rewritten", `<image xlink:href="v.svg"/>`, `<image xlink:href="` + R + `v.svg"/>`},
		{"legacy background rewritten", `<body background="paper.gif">`, `<body background="` + R + `paper.gif">`},

		// ── lists, CSS, <base> ──────────────────────────────────────────────
		{"srcset", `<img srcset="a.png 1x, sub/b.png 2x">`, `<img srcset="` + R + `a.png 1x, ` + R + `sub/b.png 2x">`},
		{"base tag dropped", `<base href="http://x/"><img src="p.png">`, `<img src="` + R + `p.png">`},
		{"style attr url", `<div style="background:url(bg.png) no-repeat">`, `<div style="background:url(` + R + `bg.png) no-repeat">`},
		{"style block url", `<style>.x{background:url('a/b.png')}</style>`, `<style>.x{background:url(` + R + `a/b.png)}</style>`},
		{"style block data-uri untouched", `<style>.y{background:url(data:image/gif;base64,AA)}</style>`, `<style>.y{background:url(data:image/gif;base64,AA)}</style>`},

		// ── things that only LOOK like references ───────────────────────────
		// Each of these was rewritten by the regex, because it matched text.
		{"src inside a script string", `<script>var a='src="x.png"';</script>`, `<script>var a='src="x.png"';</script>`},
		{"href inside a comment", `<!-- <img src="old.png"> --><b>x</b>`, `<!-- <img src="old.png"> --><b>x</b>`},
		{"href written in prose", `<p>write src="a.png" to embed</p>`, `<p>write src="a.png" to embed</p>`},
		{"plain text untouched", `<p>a plain <b>definition</b> with no refs</p>`, `<p>a plain <b>definition</b> with no refs</p>`},
		{"prose data word untouched", `<p>consult the database of words</p>`, `<p>consult the database of words</p>`},

		// ── a whole line of real article markup ─────────────────────────────
		{"mixed article",
			`<a href="sound://a.mp3"><img src="spkr_b.png"></a> <a href="bword://apple">apple</a>`,
			`<a href="` + R + `a.mp3"><img src="` + R + `spkr_b.png"></a> <a href="bword://apple">apple</a>`},
	}
	for _, c := range cases {
		if got := RewriteEntryHTML(c.in, id); got != c.want {
			t.Errorf("%s:\n  in   %s\n  got  %s\n  want %s", c.name, c.in, got, c.want)
		}
	}
}

// The original client-side bug: chained rewriting prefixed already-rewritten
// URLs again (/res/{d}/res/{d}/… → 404). The Go rewriter must be idempotent,
// including over the repair - running it twice must not keep re-serialising.
func TestRewriteEntryHTMLIdempotent(t *testing.T) {
	const id = "001598b628f5"
	for _, in := range []string{
		`<a href="sound://hwd/ame/6/abet.mp3">x</a><img src="a.png">`,
		`<a href=plaintiff__gb_1.ogg" class="pron">p</a>`,
		`<div style="background:url(bg.png)"><img srcset="a.png 1x"></div>`,
	} {
		once := RewriteEntryHTML(in, id)
		twice := RewriteEntryHTML(once, id)
		if once != twice {
			t.Errorf("not idempotent:\n  in    %s\n  once  %s\n  twice %s", in, once, twice)
		}
	}
}

// An article that references nothing must come back exactly as it went in,
// byte for byte - the rewriter parses and re-emits every article on every
// search, so any drift here corrupts every dictionary at once.
func TestRewriteEntryHTMLPreservesUntouchedMarkup(t *testing.T) {
	const id = "001598b628f5"
	for _, in := range []string{
		`<div CLASS='x'   id=y ><b>bold</b>&nbsp;&amp; <i>it</i></div>`,
		`<p>text with <a href="bword://w">a link</a> and an &eacute;</p>`,
		`<span data-x="1" DATA-Y='2'>unquoted=ok</span>`,
	} {
		if got := RewriteEntryHTML(in, id); got != in {
			t.Errorf("markup not preserved:\n  in  %s\n  got %s", in, got)
		}
	}
}
