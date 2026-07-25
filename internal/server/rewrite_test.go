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
		{"sound scheme", `href="sound://hwd/ame/6/abet.mp3"`, `href="res/` + id + `/hwd/ame/6/abet.mp3"`},
		{"sound scheme uppercase", `href="SOUND://a.mp3"`, `href="res/` + id + `/a.mp3"`},
		{"file scheme", `src="file://img/x.png"`, `src="res/` + id + `/img/x.png"`},
		{"server absolute", `src="/img/x.png"`, `src="res/` + id + `/img/x.png"`},
		{"bare relative", `src="spkr_r.png"`, `src="res/` + id + `/spkr_r.png"`},
		{"dot relative", `src="./LAAD3.css"`, `src="res/` + id + `/LAAD3.css"`},
		{"single quotes", `href='sound://q.mp3'`, `href='res/` + id + `/q.mp3'`},
		{"data attr", `data="ame/abet.mp3"`, `data="res/` + id + `/ame/abet.mp3"`},
		{"bword untouched", `href="bword://abandon"`, `href="bword://abandon"`},
		{"entry untouched", `href="entry://x"`, `href="entry://x"`},
		{"d-link untouched", `href="d:other"`, `href="d:other"`},
		{"http untouched", `href="http://x.com/a.mp3"`, `href="http://x.com/a.mp3"`},
		{"data-uri untouched", `src="data:image/png;base64,AA"`, `src="data:image/png;base64,AA"`},
		{"fragment untouched", `href="#anchor"`, `href="#anchor"`},
		{"protocol-relative untouched", `src="//cdn.x/y.js"`, `src="//cdn.x/y.js"`},
		// idempotent over both the relative form we emit and a stray absolute one
		{"already relative", `src="res/` + id + `/a.png"`, `src="res/` + id + `/a.png"`},
		{"already absolute", `src="/res/` + id + `/a.png"`, `src="/res/` + id + `/a.png"`},
		{"mixed article", `<a href="sound://a.mp3"><img src="spkr_b.png"></a> <a href="bword://apple">apple</a>`,
			`<a href="res/` + id + `/a.mp3"><img src="res/` + id + `/spkr_b.png"></a> <a href="bword://apple">apple</a>`},
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
