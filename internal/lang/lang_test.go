// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package lang

import (
	"path/filepath"
	"testing"
)

func TestNormalize(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"en", "en"}, {"EN", "en"}, {"eng", "en"},
		{"de", "de"}, {"ger", "de"}, {"deu", "de"}, {"German", "de"},
		{"fre", "fr"}, {"fra", "fr"},
		{"SpaNISH", "es"}, {"castilian", "es"},
		{"rus", "ru"}, {"Russian", "ru"},
		{"Espanol", ""}, {"Deutsch", ""}, {"", ""}, {"  ", ""},
		{"oxford", ""}, {"big", ""}, {"xxx", ""},
		{"Other", ""}, {"Other Russian languages", ""},
	} {
		if got := Normalize(tc.in); got != tc.want {
			t.Errorf("Normalize(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestFromDeclared(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"English", "en"},
		{"Russian", "ru"},
		{"Ukrainian", "uk"},
		{"SpanishModernSort", "es"},
		{"SpanishTraditionalSort", "es"},
		{"GermanNewSpelling", "de"},
		{"Malayalam", "ml"}, // must not be read as "Malay"
		{"Norwegian Bokmal", "nb"},
		{"Other", ""},
		{"Other Russian languages", ""},
		{"Other Eastern-European languages", ""},
		{"", ""},
	} {
		if got := FromDeclared(tc.in); got != tc.want {
			t.Errorf("FromDeclared(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestFromPathStem(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "dicts")
	for _, tc := range []struct{ path, want string }{
		{"/dicts/en-en-oxford.mdx", "en"},
		{"/dicts/en-es-apresyan.mdx", "en"},
		{"/dicts/eng-eng-apresyan.mdx", "en"},
		{"/dicts/eng_eng_apresyan.mdx", "en"},
		{"/dicts/spa-spa-moliner.mdx", "es"}, // 639-2, both tokens 3-letter
		{"/dicts/ger-eng-duden.mdx", "de"},   // bibliographic
		{"/dicts/deu-eng-duden.mdx", "de"},   // terminological: same answer
		{"/dicts/russian-apresyan.mdx", "ru"},
		{"/dicts/Spanish.Collins.mdx", "es"},
		{"/dicts/de dictionary.mdx", "de"},
		{"/dicts/ru-en.dsl.dz", "ru"},
		{"/dicts/oxford-en.mdx", ""}, // suffix, not prefix
		{"/dicts/apresyan-russian.mdx", ""},
		{"/dicts/collins.mdx", ""},
		{"/dicts/english.mdx", "en"}, // whole stem is the token
	} {
		if got := FromPath(tc.path, []string{root}); got != tc.want {
			t.Errorf("FromPath(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

func TestFromPathAncestors(t *testing.T) {
	roots := []string{"/dicts"}
	for _, tc := range []struct {
		path, want string
	}{
		{"/dicts/Spanish/collins.mdx", "es"},
		{"/dicts/spanish/collins.mdx", "es"},
		{"/dicts/SpaNISH/collins.mdx", "es"},
		{"/dicts/es/collins.mdx", "es"},
		{"/dicts/es/big/collins.mdx", "es"},    // walks up
		{"/dicts/Espanol/collins.mdx", ""},     // endonym
		{"/dicts/spanish-old/collins.mdx", ""}, // whole name only
		{"/dicts/misc/collins.mdx", ""},
	} {
		if got := FromPath(tc.path, roots); got != tc.want {
			t.Errorf("FromPath(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}

	// The root itself may carry the hint...
	if got := FromPath("/dicts/es/misc/collins.mdx", []string{"/dicts/es"}); got != "es" {
		t.Errorf("root folder hint = %q, want es", got)
	}
	// ...but nothing above the root is ever consulted.
	if got := FromPath("/no/dicts/misc/collins.mdx", []string{"/no/dicts"}); got != "" {
		t.Errorf("above-root ancestor = %q, want empty", got)
	}
	// Unrooted: only the immediate parent speaks.
	if got := FromPath("/no/misc/collins.mdx", nil); got != "" {
		t.Errorf("unrooted grandparent = %q, want empty", got)
	}
	if got := FromPath("/no/es/collins.mdx", nil); got != "es" {
		t.Errorf("unrooted parent = %q, want es", got)
	}
}
