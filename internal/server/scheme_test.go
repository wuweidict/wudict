// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A dictionary's stylesheets, scripts and SVGs reach the article with their
// colour-scheme queries made light (htmlref.LightScheme); HTML and files past
// the bound are served as stored.
func TestResourcesSeeALightScheme(t *testing.T) {
	big := "@media (prefers-color-scheme: dark){}" + strings.Repeat(" ", maxSchemeRewrite)
	s := newDictWithResources(t, map[string][]byte{
		"style.css": []byte("@media (prefers-color-scheme: dark){.e{color:#ddd}}"),
		"app.js":    []byte(`if(matchMedia('(prefers-color-scheme: light)').matches){}`),
		"pic.svg":   []byte(`<svg><style>@media (prefers-color-scheme: dark){path{fill:#fff}}</style></svg>`),
		"page.html": []byte(`<p>prefers-color-scheme: dark</p>`),
		"big.css":   []byte(big),
	})
	id := getDicts(t, s, "/api/dicts")[0].ID
	get := func(name string) string {
		t.Helper()
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, newRequest("GET", "/res/"+id+"/"+name, nil))
		if rec.Code != 200 {
			t.Fatalf("%s: %d", name, rec.Code)
		}
		return rec.Body.String()
	}
	for name, want := range map[string]string{
		"style.css": "@media (grid){.e{color:#ddd}}",
		"app.js":    `if(matchMedia('(grid: 0)').matches){}`,
		"pic.svg":   `<svg><style>@media (grid){path{fill:#fff}}</style></svg>`,
		"page.html": `<p>prefers-color-scheme: dark</p>`,
	} {
		if got := get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	if got := get("big.css"); got != big {
		t.Errorf("a file past the bound must stream unchanged (got %d bytes, want %d)", len(got), len(big))
	}
}

// An override renders in the same article as the file it replaces.
func TestOverrideSeesALightScheme(t *testing.T) {
	s := newDictWithResources(t, map[string][]byte{"style.css": []byte("a{}")})
	d := getDicts(t, s, "/api/dicts")[0]
	e, err := s.reg.get(d.ID)
	if err != nil {
		t.Fatal(err)
	}
	textDB, ok := e.preparedDB()
	if !ok {
		t.Skip("the test dictionary has no library folder to hold an override")
	}
	dir := filepath.Join(filepath.Dir(textDB), "res")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "style.css"), []byte("@media (prefers-color-scheme: dark){a{}}"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, newRequest("GET", "/res/"+d.ID+"/style.css", nil))
	if got := rec.Body.String(); got != "@media (grid){a{}}" {
		t.Errorf("override = %q", got)
	}
}

// An article whose only colour-scheme query is in a script has no reference
// for the fast path to see; it must still reach the rewriter.
func TestArticleScriptSeesALightScheme(t *testing.T) {
	in := `<p>x</p><script>matchMedia("(prefers-color-scheme: dark)")</script>`
	if got := RewriteEntryHTML(in, "d1"); !strings.Contains(got, `matchMedia("(grid)")`) {
		t.Errorf("RewriteEntryHTML = %q", got)
	}
}
