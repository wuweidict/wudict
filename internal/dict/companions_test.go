// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package dict

import (
	"os"
	"path/filepath"
	"testing"
)

// touch creates an empty file, making parent folders as needed.
func touch(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestStemAndMainExt(t *testing.T) {
	cases := []struct{ src, stem, ext string }{
		{"/d/Oxford.mdx", "/d/Oxford", ".mdx"},
		{"/d/ru-en.dsl", "/d/ru-en", ".dsl"},
		{"/d/ru-en.dsl.dz", "/d/ru-en", ".dsl.dz"},
		{"/d/star.ifo", "/d/star", ".ifo"},
		{"/d/x.slob", "/d/x", ".slob"},
		{"/d/x.bgl", "/d/x", ".bgl"},
	}
	for _, c := range cases {
		if got := stem(c.src); got != c.stem {
			t.Errorf("stem(%q) = %q, want %q", c.src, got, c.stem)
		}
		if got := mainExt(c.src); got != c.ext {
			t.Errorf("mainExt(%q) = %q, want %q", c.src, got, c.ext)
		}
	}
}

func TestSourceFilesMDX(t *testing.T) {
	dir := t.TempDir()
	mdx := touch(t, filepath.Join(dir, "Oxford.mdx"))
	touch(t, filepath.Join(dir, "Oxford.mdd"))
	touch(t, filepath.Join(dir, "Oxford.2.mdd"))
	touch(t, filepath.Join(dir, "Oxford.3.mdd"))
	// a gap: .5.mdd must not be picked up after .4 is missing
	touch(t, filepath.Join(dir, "Oxford.5.mdd"))
	// a NEIGHBOUR with a different stem must never be swept in
	touch(t, filepath.Join(dir, "Collins.mdx"))
	touch(t, filepath.Join(dir, "Collins.mdd"))

	got := SourceFiles(mdx)
	want := []string{
		filepath.Join(dir, "Oxford.mdx"),
		filepath.Join(dir, "Oxford.mdd"),
		filepath.Join(dir, "Oxford.2.mdd"),
		filepath.Join(dir, "Oxford.3.mdd"),
	}
	eqPaths(t, got, want)
}

func TestSourceFilesStarDict(t *testing.T) {
	dir := t.TempDir()
	ifo := touch(t, filepath.Join(dir, "star.ifo"))
	touch(t, filepath.Join(dir, "star.idx"))
	touch(t, filepath.Join(dir, "star.dict.dz"))
	touch(t, filepath.Join(dir, "star.syn"))
	touch(t, filepath.Join(dir, "res", "img.png"))
	touch(t, filepath.Join(dir, "star.unrelated"))

	got := SourceFiles(ifo)
	want := []string{
		filepath.Join(dir, "star.ifo"),
		filepath.Join(dir, "star.idx"),
		filepath.Join(dir, "star.dict.dz"),
		filepath.Join(dir, "star.syn"),
		filepath.Join(dir, "res"),
	}
	eqPaths(t, got, want)
}

func TestSourceFilesDSLCompressed(t *testing.T) {
	dir := t.TempDir()
	dsl := touch(t, filepath.Join(dir, "ru-en.dsl.dz"))
	touch(t, filepath.Join(dir, "ru-en_abrv.dsl"))
	touch(t, filepath.Join(dir, "ru-en.dsl.files.zip"))

	got := SourceFiles(dsl)
	want := []string{
		filepath.Join(dir, "ru-en.dsl.dz"),
		filepath.Join(dir, "ru-en_abrv.dsl"),
		filepath.Join(dir, "ru-en.dsl.files.zip"),
	}
	eqPaths(t, got, want)
}

func TestSourceFilesSingleFileFormats(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"x.slob", "x.bgl"} {
		p := touch(t, filepath.Join(dir, name))
		got := SourceFiles(p)
		if len(got) != 1 || got[0] != p {
			t.Errorf("SourceFiles(%q) = %v, want just the file itself", p, got)
		}
	}
	if got := SourceFiles(""); got != nil {
		t.Errorf("SourceFiles(\"\") = %v, want nil", got)
	}
}

// A main file that has since been deleted is still reported, so a caller
// always learns what it asked about.
func TestSourceFilesMissingMain(t *testing.T) {
	p := filepath.Join(t.TempDir(), "gone.mdx")
	got := SourceFiles(p)
	if len(got) != 1 || got[0] != p {
		t.Errorf("SourceFiles(missing) = %v, want [%q]", got, p)
	}
}

func eqPaths(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}
