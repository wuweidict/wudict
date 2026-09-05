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

// A res/ folder shared by several StarDict dictionaries belongs to none of
// them. SourceFiles feeds removal, so listing it here would delete the images
// of every sibling in the folder along with the one dictionary the user asked
// to remove.
func TestSourceFilesStarDictSharedRes(t *testing.T) {
	dir := t.TempDir()
	ifo := touch(t, filepath.Join(dir, "star.ifo"))
	touch(t, filepath.Join(dir, "star.idx"))
	touch(t, filepath.Join(dir, "other.ifo")) // a second dictionary, same folder
	touch(t, filepath.Join(dir, "other.idx"))
	touch(t, filepath.Join(dir, "res", "img.png"))
	touch(t, filepath.Join(dir, "res.zip"))

	got := SourceFiles(ifo)
	want := []string{
		filepath.Join(dir, "star.ifo"),
		filepath.Join(dir, "star.idx"),
	}
	eqPaths(t, got, want)
}

// The sole dictionary in a folder does own the res/ beside it - including the
// zip form, and including a mixed-case .IFO on a case-insensitive filesystem.
func TestSourceFilesStarDictSoleResZip(t *testing.T) {
	dir := t.TempDir()
	ifo := touch(t, filepath.Join(dir, "star.IFO"))
	touch(t, filepath.Join(dir, "res.zip"))

	got := SourceFiles(ifo)
	want := []string{
		filepath.Join(dir, "star.IFO"),
		filepath.Join(dir, "res.zip"),
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

// An "_abrv.dsl" is a glossary map for the dictionary of the same stem, not a
// dictionary of its own. Pairing must survive both compressed spellings and the
// case a Windows-made dictionary arrives in - and must NOT claim an orphan,
// which is a real standalone dictionary that merely ends in _abrv.
func TestAbbrevCompanion(t *testing.T) {
	dir := t.TempDir()
	main := touch(t, filepath.Join(dir, "AmericanaEnRu.dsl"))
	abrv := touch(t, filepath.Join(dir, "AmericanaEnRu_abrv.dsl"))

	if got, ok := AbbrevCompanion(main); !ok || got != abrv {
		t.Errorf("AbbrevCompanion(main) = %q, %v; want %q, true", got, ok, abrv)
	}
	if !IsAbbrevCompanion(abrv) {
		t.Error("the companion beside its parent must be recognized")
	}
	if IsAbbrevCompanion(main) {
		t.Error("the parent is not a companion")
	}
	// a companion has no companion of its own: no recursion into _abrv_abrv
	if got, ok := AbbrevCompanion(abrv); ok {
		t.Errorf("AbbrevCompanion(companion) = %q, want none", got)
	}

	// compressed spellings pair too
	dz := t.TempDir()
	dzMain := touch(t, filepath.Join(dz, "ru-en.dsl.dz"))
	dzAbrv := touch(t, filepath.Join(dz, "ru-en_abrv.dsl.dz"))
	if got, ok := AbbrevCompanion(dzMain); !ok || got != dzAbrv {
		t.Errorf("AbbrevCompanion(.dz) = %q, %v; want %q, true", got, ok, dzAbrv)
	}
	// the suffix test is case-insensitive: a Windows-made "_ABRV.DSL" is the
	// same companion. Only the SUFFIX - the parent must exist as spelled, so
	// this stays correct on a case-sensitive filesystem.
	up := t.TempDir()
	touch(t, filepath.Join(up, "Collins.dsl"))
	if !IsAbbrevCompanion(touch(t, filepath.Join(up, "Collins_ABRV.DSL"))) {
		t.Error("uppercase _ABRV.DSL must pair with its parent")
	}

	// ORPHAN: no parent beside it, so it stays an ordinary dictionary
	lone := touch(t, filepath.Join(t.TempDir(), "Glossary_abrv.dsl"))
	if IsAbbrevCompanion(lone) {
		t.Error("an _abrv with no parent must remain a dictionary")
	}
	// a file literally named "_abrv.dsl" names no parent stem at all
	if IsAbbrevCompanion(touch(t, filepath.Join(t.TempDir(), "_abrv.dsl"))) {
		t.Error("bare _abrv.dsl has no parent")
	}
	// a non-DSL main file has no abbreviation companion
	mdx := touch(t, filepath.Join(dir, "Oxford.mdx"))
	touch(t, filepath.Join(dir, "Oxford_abrv.dsl"))
	if got, ok := AbbrevCompanion(mdx); ok {
		t.Errorf("AbbrevCompanion(mdx) = %q, want none", got)
	}
}
