// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import "testing"

func TestRewriteEntryHTML(t *testing.T) {
	const id = "001598b628f5"
	cases := []struct {
		name, in, want string
	}{
		{"sound scheme", `href="sound://hwd/ame/6/abet.mp3"`, `href="/res/` + id + `/hwd/ame/6/abet.mp3"`},
		{"sound scheme uppercase", `href="SOUND://a.mp3"`, `href="/res/` + id + `/a.mp3"`},
		{"file scheme", `src="file://img/x.png"`, `src="/res/` + id + `/img/x.png"`},
		{"server absolute", `src="/img/x.png"`, `src="/res/` + id + `/img/x.png"`},
		{"bare relative", `src="spkr_r.png"`, `src="/res/` + id + `/spkr_r.png"`},
		{"dot relative", `src="./LAAD3.css"`, `src="/res/` + id + `/LAAD3.css"`},
		{"single quotes", `href='sound://q.mp3'`, `href='/res/` + id + `/q.mp3'`},
		{"data attr", `data="ame/abet.mp3"`, `data="/res/` + id + `/ame/abet.mp3"`},
		{"bword untouched", `href="bword://abandon"`, `href="bword://abandon"`},
		{"entry untouched", `href="entry://x"`, `href="entry://x"`},
		// the slash-less spellings are lookup links too (the client resolves
		// them); the rewriter must leave every scheme form alone
		{"bword no slashes untouched", `href="bword:abandon"`, `href="bword:abandon"`},
		{"entry no slashes untouched", `href="entry:x"`, `href="entry:x"`},
		{"bword with fragment untouched", `href="bword://word#sense2"`, `href="bword://word#sense2"`},
		{"d-link untouched", `href="d:other"`, `href="d:other"`},
		{"http untouched", `href="http://x.com/a.mp3"`, `href="http://x.com/a.mp3"`},
		{"data-uri untouched", `src="data:image/png;base64,AA"`, `src="data:image/png;base64,AA"`},
		{"fragment untouched", `href="#anchor"`, `href="#anchor"`},
		{"protocol-relative untouched", `src="//cdn.x/y.js"`, `src="//cdn.x/y.js"`},
		// idempotent over both the absolute form we emit and a stray relative one
		{"already absolute", `src="/res/` + id + `/a.png"`, `src="/res/` + id + `/a.png"`},
		{"already relative", `src="res/` + id + `/a.png"`, `src="res/` + id + `/a.png"`},
		// real refs from fixtures
		{"AHD5 triple-slash sound", `href="sound:///wavs/W0203400.wav"`, `href="/res/` + id + `/wavs/W0203400.wav"`},
		{"MW11sound spx", `<a href="sound://woman003.spx">`, `<a href="/res/` + id + `/woman003.spx">`},
		// ① word-boundary: attribute-name substrings must NOT be rewritten…
		{"metadata untouched", `<span metadata="keep.png">x</span>`, `<span metadata="keep.png">x</span>`},
		{"describedby untouched", `<i aria-describedby="d1"></i>`, `<i aria-describedby="d1"></i>`},
		// …but genuine prefixed resource attrs are (and should be) rewritten
		{"data-src rewritten", `<img data-src="lazy.png">`, `<img data-src="/res/` + id + `/lazy.png">`},
		{"xlink:href rewritten", `<image xlink:href="v.svg"/>`, `<image xlink:href="/res/` + id + `/v.svg"/>`},
		// ③ srcset + <base> removal
		{"srcset", `<img srcset="a.png 1x, sub/b.png 2x">`, `<img srcset="/res/` + id + `/a.png 1x, /res/` + id + `/sub/b.png 2x">`},
		{"base tag dropped", `<base href="http://x/"><img src="p.png">`, `<img src="/res/` + id + `/p.png">`},
		// ② url() inside inline style attr + <style> block
		{"style attr url", `<div style="background:url(bg.png) no-repeat">`, `<div style="background:url(/res/` + id + `/bg.png) no-repeat">`},
		{"style attr url quoted", `<div style='list-style:url("dot.gif")'>`, `<div style='list-style:url(/res/` + id + `/dot.gif)'>`},
		{"style block url", `<style>.x{background:url('a/b.png')}</style>`, `<style>.x{background:url(/res/` + id + `/a/b.png)}</style>`},
		{"style block data-uri untouched", `<style>.y{background:url(data:image/gif;base64,AA)}</style>`, `<style>.y{background:url(data:image/gif;base64,AA)}</style>`},
		// fast-path gate: no rewritable markers → returned verbatim; a bare
		// word that merely contains "data" must not be touched either.
		{"plain text untouched", `<p>a plain <b>definition</b> with no refs</p>`, `<p>a plain <b>definition</b> with no refs</p>`},
		{"prose data word untouched", `<p>consult the database of words</p>`, `<p>consult the database of words</p>`},
		{"mixed article", `<a href="sound://a.mp3"><img src="spkr_b.png"></a> <a href="bword://apple">apple</a>`,
			`<a href="/res/` + id + `/a.mp3"><img src="/res/` + id + `/spkr_b.png"></a> <a href="bword://apple">apple</a>`},
	}
	for _, c := range cases {
		if got := RewriteEntryHTML(c.in, id); got != c.want {
			t.Errorf("%s:\n  in   %s\n  got  %s\n  want %s", c.name, c.in, got, c.want)
		}
	}
}

// The original client-side bug: chained rewriting prefixed already-rewritten
// URLs again (/res/{d}/res/{d}/… → 404). The Go rewriter must be idempotent.
func TestRewriteEntryHTMLIdempotent(t *testing.T) {
	const id = "001598b628f5"
	once := RewriteEntryHTML(`href="sound://hwd/ame/6/abet.mp3" src="a.png"`, id)
	twice := RewriteEntryHTML(once, id)
	if once != twice {
		t.Errorf("not idempotent:\n  once  %s\n  twice %s", once, twice)
	}
}

func TestRewriteEntryHTMLEmpty(t *testing.T) {
	if got := RewriteEntryHTML("", "x"); got != "" {
		t.Errorf("empty html: got %q", got)
	}
	if got := RewriteEntryHTML(`src="a.png"`, ""); got != `src="a.png"` {
		t.Errorf("empty dictID must be a no-op, got %q", got)
	}
}
