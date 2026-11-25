// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package intake

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"testing"
)

// writeFiles lays out a folder of loose files and returns its path. Bodies are
// distinct so a wrong file being read shows up as a wrong size.
func writeFiles(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func plainNames(t *testing.T, path string) []string {
	t.Helper()
	a, err := OpenPlain(path)
	if err != nil {
		t.Fatalf("OpenPlain(%s): %v", path, err)
	}
	defer a.Close()
	var out []string
	for _, e := range a.Entries() {
		out = append(out, e.Name)
	}
	sort.Strings(out)
	return out
}

func plainCandidates(t *testing.T, path string) []Candidate {
	t.Helper()
	a, err := OpenPlain(path)
	if err != nil {
		t.Fatalf("OpenPlain(%s): %v", path, err)
	}
	defer a.Close()
	got, err := Sniff(a)
	if err != nil {
		t.Fatalf("Sniff: %v", err)
	}
	return got
}

// A loose .mdx brings its .mdd and leaves the neighbours alone: the folder is
// a download folder, and the other dictionary in it is not part of this one.
func TestOpenPlainGroupsSiblingsOnly(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"Oxford.mdx":   "main",
		"Oxford.mdd":   "media",
		"Oxford.1.mdd": "more media",
		"Collins.mdx":  "another dictionary",
		"readme.txt":   "not a dictionary",
	})

	want := []string{"Oxford.1.mdd", "Oxford.mdd", "Oxford.mdx"}
	got := plainNames(t, filepath.Join(dir, "Oxford.mdx"))
	if len(got) != len(want) {
		t.Fatalf("entries = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("entries = %v, want %v", got, want)
		}
	}

	c := plainCandidates(t, filepath.Join(dir, "Oxford.mdx"))
	if len(c) != 1 || c[0].Name != "Oxford" || !c[0].Complete() {
		t.Fatalf("candidates = %+v", c)
	}
}

// Handing over the SECOND half is the same import: a user who downloads the
// .mdd after the .mdx is not starting a different job.
func TestOpenPlainFromCompanion(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"Oxford.mdx": "main",
		"Oxford.mdd": "media",
	})
	c := plainCandidates(t, filepath.Join(dir, "Oxford.mdd"))
	if len(c) != 1 || c[0].Name != "Oxford" || !c[0].Complete() {
		t.Fatalf("candidates = %+v", c)
	}
}

// StarDict: the index files are found by stem, the resource folder by its own
// name, and both belong to the dictionary.
func TestOpenPlainStarDictResources(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"abc.ifo":     "StarDict's dict ifo file\n",
		"abc.idx":     "idx",
		"abc.dict.dz": "dict",
		"abc.syn":     "syn",
		"res.zip":     "shared media",
		"res/img.png": "png",
	})
	got := plainNames(t, filepath.Join(dir, "abc.ifo"))
	want := map[string]bool{
		"abc.ifo": true, "abc.idx": true, "abc.dict.dz": true, "abc.syn": true,
		"res.zip": true, "res/img.png": true,
	}
	if len(got) != len(want) {
		t.Fatalf("entries = %v", got)
	}
	for _, n := range got {
		if !want[n] {
			t.Fatalf("unexpected entry %q in %v", n, got)
		}
	}
	c := plainCandidates(t, filepath.Join(dir, "abc.ifo"))
	if len(c) != 1 || !c[0].Complete() {
		t.Fatalf("candidates = %+v", c)
	}
}

// A .ifo on its own is a header describing files that are not there. It is
// reported as incomplete, naming what is missing, rather than installed into a
// dictionary that opens and answers nothing.
func TestOpenPlainLoneStarDictIsIncomplete(t *testing.T) {
	dir := writeFiles(t, map[string]string{"abc.ifo": "StarDict's dict ifo file\n"})
	c := plainCandidates(t, filepath.Join(dir, "abc.ifo"))
	if len(c) != 1 || c[0].Complete() {
		t.Fatalf("a lone .ifo must be incomplete: %+v", c)
	}
	if len(c[0].Missing) == 0 {
		t.Error("Missing must name the files, or the user cannot go and fetch them")
	}
}

// res.zip beside an .mdx belongs to no StarDict dictionary and must not be
// dragged into one.
func TestOpenPlainSharedResourcesNeedStarDict(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"Oxford.mdx": "main",
		"res.zip":    "somebody else's media",
	})
	for _, n := range plainNames(t, filepath.Join(dir, "Oxford.mdx")) {
		if n == "res.zip" {
			t.Fatal("res.zip was attached to an MDX dictionary")
		}
	}
}

// A file that belongs to no dictionary is refused here, not reported as an
// archive holding nothing: the two are different answers to the user.
func TestOpenPlainRefusesUnrelatedFile(t *testing.T) {
	dir := writeFiles(t, map[string]string{"notes.txt": "hello"})
	if _, err := OpenPlain(filepath.Join(dir, "notes.txt")); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("err = %v, want ErrUnsupported", err)
	}
	if _, err := OpenPlain(dir); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("a directory: err = %v, want ErrUnsupported", err)
	}
	if _, err := OpenPlain(filepath.Join(dir, "gone.mdx")); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("a missing file: err = %v, want ErrUnsupported", err)
	}
}

// Open serves only what was listed. The list is the contract the extractor
// relies on, and a name that was never offered must not resolve.
func TestPlainOpenRefusesUnlistedName(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"Oxford.mdx": "main",
		"other.txt": "not yours",
	})
	a, err := OpenPlain(filepath.Join(dir, "Oxford.mdx"))
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	for _, name := range []string{"other.txt", "../other.txt", "./Oxford.mdx"} {
		if rc, err := a.Open(name); err == nil {
			rc.Close()
			t.Errorf("Open(%q) succeeded", name)
		}
	}
	rc, err := a.Open("Oxford.mdx")
	if err != nil {
		t.Fatalf("Open of a listed entry: %v", err)
	}
	rc.Close()
}

// Installable is what the URL and upload paths gate on: loose dictionary files
// are as importable as the bundles, and nothing else is.
func TestInstallable(t *testing.T) {
	cases := map[string]bool{
		"bundle.zip":  true,
		"bundle.7z":   true,
		"Oxford.mdx":  true,
		"Oxford.mdd":  true,
		"abc.ifo":     true,
		"abc.dsl.dz":  true,
		"words.slob":  true,
		"old.bgl":     true,
		"wiki.zim":    true,
		"readme.txt":  false,
		"setup.exe":   false,
		"":            false,
		"archive.rar": false,
	}
	for name, want := range cases {
		if got := Installable(name); got != want {
			t.Errorf("Installable(%q) = %v, want %v", name, got, want)
		}
	}
}

// The download folder after the user accepted the extras: the stylesheet and
// the script go in with the dictionary, and a neighbour's do not.
func TestOpenPlainTakesLooseAssets(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"OED.mdx":     "main",
		"OED.mdd":     "media",
		"OED.css":     "style",
		"OED.js":      "script",
		"Collins.mdx": "other",
		"Collins.css": "other style",
		"style.css":   "shared",
	})
	got := plainNames(t, filepath.Join(dir, "OED.mdx"))
	want := []string{"OED.css", "OED.js", "OED.mdd", "OED.mdx"}
	if !equal(got, want) {
		t.Fatalf("entries = %v, want %v", got, want)
	}
	cands := plainCandidates(t, filepath.Join(dir, "OED.mdx"))
	if len(cands) != 1 || len(cands[0].Missing) != 0 {
		t.Fatalf("candidates = %+v", cands)
	}
}

// The folder's custom.css and font.ttf go with a dictionary imported from it:
// it was shown with them there, and its own install folder is a new folder.
func TestOpenPlainTakesFolderFiles(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"dahl.dsl":   "main",
		"custom.css": "shared style",
		"font.woff2": "shared font",
		"other.css":  "a neighbour's",
	})
	got := plainNames(t, filepath.Join(dir, "dahl.dsl"))
	want := []string{"custom.css", "dahl.dsl", "font.woff2"}
	if !equal(got, want) {
		t.Fatalf("entries = %v, want %v", got, want)
	}
	cands := plainCandidates(t, filepath.Join(dir, "dahl.dsl"))
	if len(cands) != 1 || !slices.Contains(cands[0].Files, "custom.css") {
		t.Fatalf("candidates = %+v", cands)
	}
}
