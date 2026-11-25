// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package dict

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestCompanionFont(t *testing.T) {
	type c struct {
		name   string
		files  []string // created in the folder; the first is the main file
		want   string   // base name of the font found, "" for none
		folder bool
	}
	cases := []c{
		{"mdx stem", []string{"Oxford.mdx", "Oxford.ttf"}, "Oxford.ttf", false},
		{"compressed dsl names the dictionary", []string{"ru-en.dsl.dz", "ru-en.otf"}, "ru-en.otf", false},
		{"stardict", []string{"star.ifo", "star.idx", "star.woff2"}, "star.woff2", false},
		{"split zim", []string{"wiki.zimaa", "wiki.woff"}, "wiki.woff", false},
		{"slob", []string{"x.slob", "x.ttf"}, "x.ttf", false},
		{"wudict markdown keeps its stem", []string{"g.wudict.md", "g.wudict.ttf"}, "g.wudict.ttf", false},
		{"upper-case extension", []string{"Oxford.mdx", "Oxford.TTF"}, "Oxford.TTF", false},
		{"fixed order, ttf first", []string{"Oxford.mdx", "Oxford.woff", "Oxford.ttf"}, "Oxford.ttf", false},
		{"stem font beats the folder's", []string{"Oxford.mdx", "font.ttf", "Oxford.otf"}, "Oxford.otf", false},
		{"folder font", []string{"Oxford.mdx", "font.woff2"}, "font.woff2", true},
		{"folder font, upper case", []string{"Oxford.mdx", "FONT.TTF"}, "FONT.TTF", true},
		{"a dictionary called font shares it", []string{"font.mdx", "font.ttf"}, "font.ttf", true},
		{"a neighbour's font is not ours", []string{"Oxford.mdx", "Collins.ttf"}, "", false},
		{"collections are not used", []string{"Oxford.mdx", "Oxford.ttc"}, "", false},
		{"no font", []string{"Oxford.mdx"}, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for _, f := range tc.files {
				touch(t, filepath.Join(dir, f))
			}
			got, folder := CompanionFont(filepath.Join(dir, tc.files[0]))
			// a case-insensitive filesystem answers the lower-case probe first
			name := ""
			if got != "" {
				name = filepath.Base(got)
			}
			if !strings.EqualFold(name, tc.want) {
				t.Errorf("font = %q, want %q", got, tc.want)
			}
			if folder != tc.folder {
				t.Errorf("folder = %v, want %v", folder, tc.folder)
			}
		})
	}
	if p, _ := CompanionFont(""); p != "" {
		t.Errorf("CompanionFont(\"\") = %q", p)
	}
}

func TestCompanionCSS(t *testing.T) {
	cases := []struct {
		name  string
		files []string // the first is the main file
		want  []string // base names, cascade order
		own   string
	}{
		{"dsl, both", []string{"ru-en.dsl.dz", "custom.css", "ru-en.css"}, []string{"custom.css", "ru-en.css"}, "ru-en.css"},
		{"stardict own", []string{"star.ifo", "star.css"}, []string{"star.css"}, "star.css"},
		{"slob folder only", []string{"x.slob", "custom.css"}, []string{"custom.css"}, ""},
		{"wudict markdown keeps its stem", []string{"g.wudict.md", "g.wudict.css"}, []string{"g.wudict.css"}, "g.wudict.css"},
		{"upper case", []string{"x.bgl", "CUSTOM.CSS", "x.CSS"}, []string{"CUSTOM.CSS", "x.CSS"}, "x.CSS"},
		{"mdx: the folder's only", []string{"Oxford.mdx", "Oxford.css", "custom.css"}, []string{"custom.css"}, ""},
		{"a dictionary called custom", []string{"custom.dsl", "custom.css"}, []string{"custom.css"}, ""},
		{"a neighbour's is not ours", []string{"a.dsl", "b.css"}, nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for _, f := range tc.files {
				touch(t, filepath.Join(dir, f))
			}
			css, own := CompanionCSS(filepath.Join(dir, tc.files[0]))
			var got []string
			for _, p := range css {
				got = append(got, filepath.Base(p))
			}
			// a case-insensitive filesystem answers the lower-case probe first
			if len(got) != len(tc.want) {
				t.Fatalf("css = %q, want %q", got, tc.want)
			}
			for i := range got {
				if !strings.EqualFold(got[i], tc.want[i]) {
					t.Errorf("css = %q, want %q", got, tc.want)
				}
			}
			if !strings.EqualFold(filepath.Base(own), tc.own) && !(own == "" && tc.own == "") {
				t.Errorf("own = %q, want %q", own, tc.own)
			}
		})
	}
}

// Removal lists the dictionary's own font and never the folder's, which every
// other dictionary there is shown in too.
func TestSourceFilesFont(t *testing.T) {
	dir := t.TempDir()
	mdx := touch(t, filepath.Join(dir, "Oxford.mdx"))
	own := touch(t, filepath.Join(dir, "Oxford.ttf"))
	if got := SourceFiles(mdx); !slices.Contains(got, own) {
		t.Errorf("SourceFiles = %v, want it to list %s", got, own)
	}

	// the folder's, with a neighbour: never listed
	dir = t.TempDir()
	mdx = touch(t, filepath.Join(dir, "Oxford.mdx"))
	touch(t, filepath.Join(dir, "cambridge.bgl"))
	shared := touch(t, filepath.Join(dir, "font.ttf"))
	if got := SourceFiles(mdx); slices.Contains(got, shared) {
		t.Errorf("SourceFiles = %v, must not list the folder's %s", got, shared)
	}

	// the same for stylesheets, in a format whose own one is new
	dir = t.TempDir()
	dsl := touch(t, filepath.Join(dir, "ru-en.dsl"))
	touch(t, filepath.Join(dir, "wiktionary.ifo"))
	ownCSS := touch(t, filepath.Join(dir, "ru-en.css"))
	folderCSS := touch(t, filepath.Join(dir, "custom.css"))
	got := SourceFiles(dsl)
	if !slices.Contains(got, ownCSS) || slices.Contains(got, folderCSS) {
		t.Errorf("SourceFiles = %v, want %s and never %s", got, ownCSS, folderCSS)
	}

	// the sole dictionary of its folder - what an import leaves - takes the
	// folder's files with it, so the emptied folder can go too
	dir = t.TempDir()
	dsl = touch(t, filepath.Join(dir, "dahl.dsl"))
	touch(t, filepath.Join(dir, "readme.txt"))
	soleCSS := touch(t, filepath.Join(dir, "custom.css"))
	soleFont := touch(t, filepath.Join(dir, "font.woff2"))
	got = SourceFiles(dsl)
	if !slices.Contains(got, soleCSS) || !slices.Contains(got, soleFont) {
		t.Errorf("SourceFiles = %v, want %s and %s", got, soleCSS, soleFont)
	}
}
