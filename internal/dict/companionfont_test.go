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

// Removal lists the dictionary's own font and never the folder's, which every
// other dictionary there is shown in too.
func TestSourceFilesFont(t *testing.T) {
	dir := t.TempDir()
	mdx := touch(t, filepath.Join(dir, "Oxford.mdx"))
	own := touch(t, filepath.Join(dir, "Oxford.ttf"))
	if got := SourceFiles(mdx); !slices.Contains(got, own) {
		t.Errorf("SourceFiles = %v, want it to list %s", got, own)
	}

	dir = t.TempDir()
	mdx = touch(t, filepath.Join(dir, "Oxford.mdx"))
	shared := touch(t, filepath.Join(dir, "font.ttf"))
	if got := SourceFiles(mdx); slices.Contains(got, shared) {
		t.Errorf("SourceFiles = %v, must not list the folder's %s", got, shared)
	}
}
