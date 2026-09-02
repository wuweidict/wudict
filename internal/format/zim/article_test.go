// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package zim

import "testing"

func TestArticleBody(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{
			"strips the shell, hoists the stylesheet",
			`<!DOCTYPE html><html><head><title>t</title>` +
				`<link rel="stylesheet" href="a.css"><link rel="icon" href="f.png">` +
				`</head><body class="mw">text</body></html>`,
			`<link rel="stylesheet" href="a.css">text`,
		},
		{
			"several stylesheets keep their order",
			`<head><link rel="stylesheet" href="1.css"><LINK REL="stylesheet" HREF="2.css"></head><body>x</body>`,
			`<link rel="stylesheet" href="1.css"><LINK REL="stylesheet" HREF="2.css">x`,
		},
		{"uppercase tags", `<HTML><BODY>hi</BODY></HTML>`, `hi`},
		{"attributes on body", `<body data-x=">">y</body>`, `">y`},
		// No <body> at all: a devdocs or zimit fragment must survive whole
		// rather than be emptied.
		{"no body element", `<div>fragment</div>`, `<div>fragment</div>`},
		{"unclosed body", `<html><body>tail`, `tail`},
		{"empty", ``, ``},
		{"body-like text only", `just words`, `just words`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := articleBody(tc.in); got != tc.want {
				t.Errorf("articleBody(%q)\n = %q\nwant %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestBwordRef(t *testing.T) {
	cases := []struct {
		name  string
		ref   string
		newNS bool
		want  string
	}{
		{"sibling path", "cat", true, "bword://cat"},
		{"percent-encoded", "odrasl%C4%83", true, "bword://odraslă"},
		{"underscore is a space", "New_York", true, "bword://New York"},
		{"leading ./", "./cat", true, "bword://cat"},
		{"fragment is kept", "cat#Etymology", true, "bword://cat#Etymology"},
		{"fragment only", "#Etymology", true, "#Etymology"},
		{"absolute", "https://example.org/x", true, "https://example.org/x"},
		{"protocol relative", "//example.org/x", true, "//example.org/x"},
		{"mailto", "mailto:a@b.c", true, "mailto:a@b.c"},
		{"data uri", "data:text/plain,x", true, "data:text/plain,x"},
		{"query is a live reference", "index.php?title=x", true, "index.php?title=x"},
		{"empty", "", true, ""},
		// A sub-path is a stored resource or a captured URL, never a headword.
		{"sub-path", "_res_/a.css", true, "_res_/a.css"},
		{"new scheme keeps its namespace", "../C/cat", true, "../C/cat"},
		// Pre-6.1: the namespace segment survives the relative trim, and only
		// the article namespace is a lookup.
		{"old scheme article", "../A/cat", false, "bword://cat"},
		{"old scheme image", "../I/p.png", false, "../I/p.png"},
		{"old scheme layout", "../-/s.css", false, "../-/s.css"},
		{"old scheme sibling", "cat", false, "bword://cat"},
		{"bad escape is left as written", "a%zz", true, "bword://a%zz"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := bwordRef(tc.ref, tc.newNS); got != tc.want {
				t.Errorf("bwordRef(%q, %v) = %q, want %q", tc.ref, tc.newNS, got, tc.want)
			}
		})
	}
}

func TestPathVariants(t *testing.T) {
	cases := []struct {
		word string
		want []string
	}{
		{"cat", []string{"cat", "Cat"}},
		{"new york", []string{"new york", "new_york", "New york", "New_york", "New_York"}},
		{"Cat", []string{"Cat"}},
		{"ăsta", []string{"ăsta", "Ăsta"}},
	}
	for _, tc := range cases {
		got := pathVariants(tc.word)
		if len(got) != len(tc.want) {
			t.Fatalf("pathVariants(%q) = %v, want %v", tc.word, got, tc.want)
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Fatalf("pathVariants(%q) = %v, want %v", tc.word, got, tc.want)
			}
		}
	}
}

func TestTrimRelative(t *testing.T) {
	cases := map[string]string{
		"./a":       "a",
		"../a":      "a",
		"../../a/b": "a/b",
		"/a":        "a",
		"a":         "a",
		"":          "",
		"...":       "...",
		"././":      "",
	}
	for in, want := range cases {
		if got := trimRelative(in); got != want {
			t.Errorf("trimRelative(%q) = %q, want %q", in, got, want)
		}
	}
}
