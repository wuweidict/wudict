// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package htmlref

import (
	"strings"
	"testing"
)

func TestLightScheme(t *testing.T) {
	for _, c := range []struct{ name, in, want string }{
		{"dark", "@media (prefers-color-scheme: dark){a{color:#ddd}}", "@media (grid){a{color:#ddd}}"},
		{"light", "@media (prefers-color-scheme:light){a{}}", "@media (grid: 0){a{}}"},
		{"compound", "@media screen and (prefers-color-scheme: dark) and (max-width: 600px){}", "@media screen and (grid) and (max-width: 600px){}"},
		{"comma list", "@media (prefers-color-scheme: dark), print{}", "@media (grid), print{}"},
		{"not", "@media not (prefers-color-scheme: dark){}", "@media not (grid){}"},
		{"case", "@MEDIA (PREFERS-COLOR-SCHEME : DARK){}", "@MEDIA (grid){}"},
		{"whitespace", "(prefers-color-scheme:\n\t dark)", "(grid)"},
		{"import", "@import url(d.css) (prefers-color-scheme: dark);", "@import url(d.css) (grid);"},
		{"script", "matchMedia('(prefers-color-scheme: dark)').matches", "matchMedia('(grid)').matches"},
		{"several", "(prefers-color-scheme: dark){} (prefers-color-scheme: light){}", "(grid){} (grid: 0){}"},
		{"at start", "prefers-color-scheme: dark", "grid"},
		{"boolean form", "@media (prefers-color-scheme){}", "@media (prefers-color-scheme){}"},
		{"other value", "(prefers-color-scheme: no-preference)", "(prefers-color-scheme: no-preference)"},
		{"longer value", "(prefers-color-scheme: darker)", "(prefers-color-scheme: darker)"},
		{"hyphenated value", "(prefers-color-scheme: dark-mode)", "(prefers-color-scheme: dark-mode)"},
		{"custom property", "--my-prefers-color-scheme: dark;", "--my-prefers-color-scheme: dark;"},
		{"no colon", "prefers-color-scheme dark", "prefers-color-scheme dark"},
		{"non-ASCII fold", "(prefers-color-scheme: darK)", "(prefers-color-scheme: darK)"},
		{"bad then good", "(prefers-color-scheme: x) (prefers-color-scheme: dark)", "(prefers-color-scheme: x) (grid)"},
		{"empty", "", ""},
		{"untouched", "a{color:red}", "a{color:red}"},
	} {
		if got := LightScheme(c.in); got != c.want {
			t.Errorf("%s: LightScheme(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
		if NeedsLightScheme(c.in) != NeedsLightScheme([]byte(c.in)) {
			t.Errorf("%s: NeedsLightScheme differs between string and []byte", c.name)
		}
	}
	if NeedsLightScheme("a{color:red}") || !NeedsLightScheme([]byte("x (Prefers-Color-Scheme: dark)")) {
		t.Error("NeedsLightScheme misreports")
	}
}

// The structural half: an article's <style>, <script> and media attributes
// change; its prose does not, because a dictionary that documents CSS states
// the very text the rewrite looks for.
func TestRewriterLightScheme(t *testing.T) {
	doc := `<link rel="stylesheet" href="a.css" media="(prefers-color-scheme: dark)">` +
		`<style>@media (prefers-color-scheme: dark){p{color:#ddd;background:url(bg.png)}}</style>` +
		`<script>if(matchMedia("(prefers-color-scheme: dark)").matches){}</script>` +
		`<p>Use prefers-color-scheme: dark to detect it.</p>`

	if got := (Rewriter{}).Rewrite(doc); got != doc {
		t.Fatalf("without LightScheme the article must round-trip:\n%s", got)
	}

	got := Rewriter{LightScheme: true}.Rewrite(doc)
	for _, want := range []string{`media="(grid)"`, `@media (grid){p{color:#ddd;background:url(bg.png)}}`,
		`matchMedia("(grid)")`, `<p>Use prefers-color-scheme: dark to detect it.</p>`} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}

	// Both passes over one <style>: the scheme and the references.
	got = Rewriter{LightScheme: true, URL: func(r Ref) string { return "/res/d/" + r.URL }}.Rewrite(doc)
	if !strings.Contains(got, `@media (grid){p{color:#ddd;background:url(/res/d/bg.png)}}`) {
		t.Errorf("style lost one of its two rewrites:\n%s", got)
	}
}
