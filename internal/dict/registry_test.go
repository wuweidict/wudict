// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package dict

import (
	"os"
	"path/filepath"
	"testing"
)

type fakeDict struct{ Dictionary }

func TestRegistryOpenAndDiscover(t *testing.T) {
	RegisterFormat(".faketest", func(path string) (Dictionary, error) { return fakeDict{}, nil })

	if _, err := Open("x.unregistered"); err == nil {
		t.Error("unregistered extension must error")
	}
	if _, err := Open("x.FAKETEST"); err != nil {
		t.Errorf("extension match must be case-insensitive: %v", err)
	}

	dir := t.TempDir()
	sub := filepath.Join(dir, "nested")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"b.faketest", "nested/a.faketest", "ignore.txt"} {
		if err := os.WriteFile(filepath.Join(dir, f), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := Discover(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 dicts, got %v", got)
	}
	// case-insensitive sort: nested/a before b? full paths compared — dir/b vs dir/nested/a
	if filepath.Base(got[0]) != "b.faketest" || filepath.Base(got[1]) != "a.faketest" {
		t.Errorf("unexpected order/content: %v", got)
	}
}

// TestMultiPartSuffixMatch: a multi-part suffix (".foo.gz") must win over any
// shorter one, and a file whose only trailing match is an unregistered
// multi-part suffix (a StarDict ".dict.dz" companion) must NOT be discovered
// or opened as a dictionary.
func TestMultiPartSuffixMatch(t *testing.T) {
	RegisterFormat(".foo.gz", func(path string) (Dictionary, error) { return fakeDict{}, nil })
	RegisterFormat(".primary", func(path string) (Dictionary, error) { return fakeDict{}, nil })

	// the compressed dict opens; the foreign companion does not
	if _, err := Open("/x/dict.foo.gz"); err != nil {
		t.Errorf(".foo.gz should open: %v", err)
	}
	if _, err := Open("/x/dict.other.gz"); err == nil {
		t.Error("an unregistered .other.gz must not be treated as a dictionary")
	}

	dir := t.TempDir()
	for _, f := range []string{"real.primary", "book.foo.gz", "book.other.gz", "book.idx"} {
		if err := os.WriteFile(filepath.Join(dir, f), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := Discover(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, p := range got {
		names[filepath.Base(p)] = true
	}
	if !names["real.primary"] || !names["book.foo.gz"] {
		t.Errorf("expected real.primary and book.foo.gz discovered: %v", got)
	}
	if names["book.other.gz"] || names["book.idx"] {
		t.Errorf("companion files must not be discovered: %v", got)
	}
}

// TestBundleFileNameAndSidecars: a prepared-dictionary folder is recognized by
// its exact main file name (text.db), so such a folder can be handed to
// someone and dropped straight into a dictionary folder. The sidecars beside
// it must stay invisible: registering the bare ".db" extension is what once
// made a media.db open as a phantom dictionary, and an unrelated "context.db"
// must not be swallowed by suffix matching either.
func TestBundleFileNameAndSidecars(t *testing.T) {
	RegisterFileName("text.db", func(path string) (Dictionary, error) { return fakeDict{}, nil })
	RegisterFormat(".text.db", func(path string) (Dictionary, error) { return fakeDict{}, nil })

	for _, ok := range []string{"/lib/AHD5/text.db", "/lib/AHD5.text.db", "/lib/TEXT.DB"} {
		if _, err := Open(ok); err != nil {
			t.Errorf("Open(%q) should succeed: %v", ok, err)
		}
	}
	for _, bad := range []string{"/lib/AHD5/media.db", "/lib/AHD5.media.db", "/lib/context.db", "/lib/notes.db"} {
		if _, err := Open(bad); err == nil {
			t.Errorf("Open(%q) must not resolve to a dictionary", bad)
		}
	}

	dir := t.TempDir()
	bundle := filepath.Join(dir, "Handed To Me")
	if err := os.MkdirAll(bundle, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"text.db", "media.db", "info.txt"} {
		if err := os.WriteFile(filepath.Join(bundle, f), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := Discover(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || filepath.Base(got[0]) != "text.db" {
		t.Fatalf("a dropped-in dictionary folder should yield exactly its text.db, got %v", got)
	}
}

// TestExcludeDirAndSameDir: the library is skipped whole by discovery, which
// covers both "DICT_DIR contains DB_DIR" and symlinked spellings of it.
func TestExcludeDirAndSameDir(t *testing.T) {
	RegisterFormat(".exctest", func(path string) (Dictionary, error) { return fakeDict{}, nil })
	root := t.TempDir()
	lib := filepath.Join(root, "db")
	if err := os.MkdirAll(lib, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "real.exctest"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lib, "prepared.exctest"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	if got, _ := Discover(root); len(got) != 2 {
		t.Fatalf("precondition: want 2 before excluding, got %v", got)
	}
	saved := excludedDirs
	t.Cleanup(func() { excludedDirs = saved })
	ExcludeDir(filepath.Join(root, ".", "db")) // unclean spelling of the same dir

	got, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || filepath.Base(got[0]) != "real.exctest" {
		t.Fatalf("library subtree must be skipped, got %v", got)
	}
	if !SameDir(lib, filepath.Join(root, "db")) {
		t.Error("SameDir should equate a dir with its unclean spelling")
	}
	if SameDir(root, lib) {
		t.Error("SameDir must not equate a parent with its child")
	}
}
