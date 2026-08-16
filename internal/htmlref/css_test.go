// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package htmlref

import "testing"

func TestParseCSS(t *testing.T) {
	tests := []struct {
		name string
		css  string
		want Styles
	}{
		{"plain class", `.Sense { display: block }`,
			Styles{"Sense": DisplayBlock}},
		{"descendant selector attributes to the rightmost compound",
			`.ldoceEntry .Sense { display: block }`,
			Styles{"Sense": DisplayBlock}},
		{"child and sibling combinators too",
			`.a > .b { display: block } .c + .d { display: none } .e ~ .f { display: none }`,
			Styles{"b": DisplayBlock, "d": DisplayNone, "f": DisplayNone}},
		{"selector list", `.a, .b span.c { display: block }`,
			Styles{"a": DisplayBlock, "c": DisplayBlock}},
		{"hidden classes are recorded", `.FIELD { display: none }`,
			Styles{"FIELD": DisplayNone}},
		{"other declarations are ignored", `.x { color: red; font-size: 12px }`, nil},
		{"display among other declarations",
			`.x { color: red; display: block; margin: 0 }`,
			Styles{"x": DisplayBlock}},
		{"last display in a block wins",
			`.x { display: block; display: inline }`,
			Styles{"x": DisplayInline}},
		{"!important is not part of the value", `.x { display: block !important }`,
			Styles{"x": DisplayBlock}},
		{"comments are not content", `/* .a { display: block } */ .b { display: block }`,
			Styles{"b": DisplayBlock}},
		{"a brace inside a string does not end the block",
			`.x { content: "}" ; display: block }`,
			Styles{"x": DisplayBlock}},
		{"pseudo-classes are refused: they apply only sometimes",
			`.x:hover { display: block } .y::before { display: block }`, nil},
		{"attribute selectors are refused: they apply only to some elements",
			`.x[data-k] { display: none }`, nil},
		{"id selectors name one element, not a class", `#hw.x { display: block }`, nil},
		{"escaped class names are refused", `.a\:b { display: none }`, nil},
		{"element and universal selectors carry no class",
			`div { display: block } * { display: none }`, nil},
		{"@media is walked into", `@media screen { .x { display: block } }`,
			Styles{"x": DisplayBlock}},
		{"nested at-rules are walked into",
			`@supports (display:grid) { @media print { .x { display: block } } }`,
			Styles{"x": DisplayBlock}},
		{"@keyframes declarations belong to no element",
			`@keyframes spin { from { display: block } to { display: none } } .x { display: none }`,
			Styles{"x": DisplayNone}},
		{"@font-face is skipped whole",
			`@font-face { font-family: x; src: url(a.woff) } .y { display: block }`,
			Styles{"y": DisplayBlock}},
		{"@import has no body to skip",
			`@import url("other.css"); .x { display: block }`,
			Styles{"x": DisplayBlock}},
		{"two-value display reads its outer role",
			`.a { display: block flow } .b { display: inline flow-root }`,
			Styles{"a": DisplayBlock, "b": DisplayInline}},
		{"table and flex roles are boundaries",
			`.a { display: table-cell } .b { display: flex } .c { display: list-item }`,
			Styles{"a": DisplayBlock, "b": DisplayBlock, "c": DisplayBlock}},
		{"inline variants are not",
			`.a { display: inline-block } .b { display: contents }`,
			Styles{"a": DisplayInline, "b": DisplayInline}},
		{"inherit says nothing without a DOM", `.x { display: inherit }`, nil},
		// A browser closes an open block at end of input rather than throwing
		// the rule away, and so does this.
		{"truncated file still yields its last rule", `.x { display: block`,
			Styles{"x": DisplayBlock}},
		{"unterminated selector does not panic", `.x`, nil},
		{"empty", ``, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseCSS(tc.css, nil)
			if len(got) != len(tc.want) {
				t.Fatalf("ParseCSS(%q) = %v, want %v", tc.css, got, tc.want)
			}
			for k, v := range tc.want {
				if got[k] != v {
					t.Errorf("ParseCSS(%q)[%q] = %v, want %v", tc.css, k, got[k], v)
				}
			}
		})
	}
}

// The merge rules are the whole safety argument for a flat table: it has no
// cascade, so where two rules disagree it must fail toward keeping content and
// toward adding a boundary.
func TestParseCSSMergesConservatively(t *testing.T) {
	tests := []struct {
		name string
		css  string
		want Display
	}{
		{"block anywhere wins over inline",
			`.x { display: inline } .ldoceEntry .x { display: block }`, DisplayBlock},
		{"order does not matter",
			`.ldoceEntry .x { display: block } .x { display: inline }`, DisplayBlock},
		{"visible anywhere beats hidden",
			`.x { display: none } .print .x { display: inline }`, DisplayInline},
		{"hidden only where nothing else claims it",
			`.x { display: none } .x { display: none }`, DisplayNone},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ParseCSS(tc.css, nil)["x"]; got != tc.want {
				t.Errorf("ParseCSS(%q)[x] = %v, want %v", tc.css, got, tc.want)
			}
		})
	}

	// …but it may never HIDE on the strength of half a selector, because the
	// table cannot record "only when both classes are present".
	if got := ParseCSS(`.collo.COLLOC { display: none }`, nil); len(got) != 0 {
		t.Errorf("a multi-class compound hid a class on its own: %v", got)
	}
	if got := ParseCSS(`.collo.COLLOC { display: block }`, nil); got["collo"] != DisplayBlock || got["COLLOC"] != DisplayBlock {
		t.Errorf("a multi-class compound lost its boundary: %v", got)
	}
}

func TestParseCSSAccumulates(t *testing.T) {
	st := ParseCSS(`.a { display: block }`, nil)
	st = ParseCSS(`.b { display: none }`, st)
	if st["a"] != DisplayBlock || st["b"] != DisplayNone {
		t.Errorf("a second stylesheet did not fold into the first: %v", st)
	}
}

func TestStylesClass(t *testing.T) {
	st := Styles{"blk": DisplayBlock, "hid": DisplayNone, "inl": DisplayInline}
	tests := []struct {
		attr string
		want Display
	}{
		{"", DisplayUnset},
		{"unknown", DisplayUnset},
		{"blk", DisplayBlock},
		{"hid", DisplayNone},
		{"collo blk", DisplayBlock},
		{"  blk   hid  ", DisplayBlock}, // highest precedence wins
		{"hid inl", DisplayInline},      // visible beats hidden
		{"hid unknown", DisplayNone},
		{"blkish", DisplayUnset}, // whole tokens only, never a prefix
	}
	for _, tc := range tests {
		if got := st.Class(tc.attr); got != tc.want {
			t.Errorf("Class(%q) = %v, want %v", tc.attr, got, tc.want)
		}
	}
	if got := Styles(nil).Class("blk"); got != DisplayUnset {
		t.Errorf("nil Styles answered %v", got)
	}
}

// A stylesheet is third-party input like everything else: it may be nonsense,
// and nonsense must cost a wrong answer at worst, never a hang or a panic.
func TestParseCSSSurvivesGarbage(t *testing.T) {
	for _, css := range []string{
		`}`, `{`, `{}`, `;;;`, `.x{`, `.x}`, `@media`, `@media {`, `@media { .x {`,
		`.x { display: }`, `.x { : block }`, `.x { display }`,
		`/*`, `.x { content: "unterminated ; display: block }`,
		`.x { content: 'a\'b' ; display: block }`,
		`@media { @media { @media { @media { @media { @media { @media { @media { @media { .x { display: block } } } } } } } } } }`,
	} {
		ParseCSS(css, nil) // must return
	}
}
