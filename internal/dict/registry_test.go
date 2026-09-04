// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package dict

import (
	"os"
	"path/filepath"
	"strings"
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
	// case-insensitive sort: nested/a before b? full paths compared - dir/b vs dir/nested/a
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

// TestOpenPreparedFolder: the folder IS the dictionary (D20 - "copy, move or
// zip it as a unit"), so it is the name every listing shows and the name a
// user types. Dispatch is by file name and extension, which a folder has
// neither of usefully: a real library folder called
// "es_es_DLE_v23.8_1_RealAcademia2026" was rejected with "unsupported
// dictionary format: .8_1_RealAcademia2026", filepath.Ext having read the
// version number as an extension.
func TestOpenPreparedFolder(t *testing.T) {
	RegisterFileName("text.db", func(path string) (Dictionary, error) { return fakeDict{}, nil })

	root := t.TempDir()
	bundle := filepath.Join(root, "es_es_DLE_v23.8_1_RealAcademia2026")
	if err := os.MkdirAll(bundle, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"text.db", "media.db", "info.txt"} {
		if err := os.WriteFile(filepath.Join(bundle, f), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Trailing separator too: shell completion produces it, and it changes
	// what filepath.Base reports.
	for _, p := range []string{bundle, bundle + string(filepath.Separator)} {
		if _, err := Open(p); err != nil {
			t.Errorf("Open(%q): %v", p, err)
		}
		if got := MainFile(p); filepath.Base(got) != "text.db" {
			t.Errorf("MainFile(%q) = %q, want its text.db", p, got)
		}
	}

	// A folder with no main file is not a dictionary, and says so in words
	// that name the folder rather than the dots in its name.
	plain := filepath.Join(root, "Loose MDX v2.1")
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(plain, "a.mdx"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := MainFile(plain); got != plain {
		t.Errorf("MainFile(%q) = %q, want it unchanged", plain, got)
	}
	_, err := Open(plain)
	if err == nil {
		t.Fatal("a folder of loose files must not open as a dictionary")
	}
	if !strings.Contains(err.Error(), "Loose MDX v2.1") || !strings.Contains(err.Error(), "folder") {
		t.Errorf("error should name the folder and say it is one: %v", err)
	}
	if strings.HasSuffix(err.Error(), "format: .1") {
		t.Errorf("error read the version number as an extension: %v", err)
	}

	// A path that does not exist is the opener's to report, named as typed.
	missing := filepath.Join(root, "gone")
	if got := MainFile(missing); got != missing {
		t.Errorf("MainFile(%q) = %q, want it unchanged", missing, got)
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

// TestDiscoverAllDedupe: overlapping roots are normal once several are allowed
// - a parent and its own subfolder, a repeat, a symlink pointing at a folder
// already listed. Each dictionary must appear exactly once (two ids would mean
// two panel rows and doubled search results), attributed to the first root
// that offered it.
func TestDiscoverAllDedupe(t *testing.T) {
	RegisterFormat(".multitest", func(path string) (Dictionary, error) { return fakeDict{}, nil })
	a, b := t.TempDir(), t.TempDir()
	sub := filepath.Join(a, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{filepath.Join(a, "one.multitest"), filepath.Join(sub, "two.multitest"), filepath.Join(b, "three.multitest")} {
		if err := os.WriteFile(f, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(t.TempDir(), "link-to-a")
	if err := os.Symlink(a, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	paths, perRoot, err := DiscoverAll([]string{a, sub, b, a, link})
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 3 {
		t.Fatalf("want 3 dictionaries, got %v", paths)
	}
	// a contributed one.multitest and sub/two.multitest; b contributed one;
	// the nested root, the repeat and the symlink contributed nothing new
	want := []RootScan{{New: 2, Total: 2}, {New: 0, Total: 1}, {New: 1, Total: 1}, {New: 0, Total: 2}, {New: 0, Total: 2}}
	for i := range want {
		if perRoot[i] != want[i] {
			t.Fatalf("per-root counts = %+v, want %+v", perRoot, want)
		}
	}
	// a missing root is skipped, not fatal
	paths2, perRoot2, err := DiscoverAll([]string{filepath.Join(t.TempDir(), "gone"), b})
	if err != nil {
		t.Fatalf("missing root should not fail: %v", err)
	}
	if len(paths2) != 1 || perRoot2[0].New != 0 || perRoot2[1].New != 1 {
		t.Errorf("paths=%v perRoot=%v", paths2, perRoot2)
	}
}

// A symlinked dictionary folder must work: filepath.WalkDir lstats its root,
// so before this was resolved a symlinked folder yielded nothing at all.
func TestDiscoverFollowsSymlinkedRoot(t *testing.T) {
	RegisterFormat(".linktest", func(path string) (Dictionary, error) { return fakeDict{}, nil })
	real := t.TempDir()
	if err := os.WriteFile(filepath.Join(real, "d.linktest"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "shortcut")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	got, err := Discover(link)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || filepath.Base(got[0]) != "d.linktest" {
		t.Fatalf("symlinked root yielded %v", got)
	}
}

// The same folder reached by a different spelling must collapse to one entry:
// otherwise it shows as several rows, is walked several times, and is written
// several times into wudict.toml. Discovery already guarantees the
// dictionaries themselves are never listed twice - this is about the folders.
func TestDedupeDirs(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "shortcut")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	other := t.TempDir()
	gone := filepath.Join(t.TempDir(), "unmounted")

	got := DedupeDirs([]string{
		real,                              // kept: first spelling
		real,                              // exact repeat
		real + string(filepath.Separator), // trailing separator
		link,                              // a symlink to the same directory
		other,                             // a genuinely different folder: kept
		gone, gone,                        // missing, listed twice: kept once
		"", "   ", // blanks dropped
	})
	want := []string{real, other, gone}
	if len(got) != len(want) {
		t.Fatalf("DedupeDirs = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("DedupeDirs = %q, want %q (order and first spelling preserved)", got, want)
		}
	}
	// nested folders are NOT duplicates: they are different directories, and
	// the overlap is reported per root rather than silently collapsed
	sub := filepath.Join(real, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := DedupeDirs([]string{real, sub}); len(got) != 2 {
		t.Errorf("a subfolder must survive dedupe: %q", got)
	}
}

// Discovery must not list a DSL abbreviation companion: its content is absorbed
// into the parent at ingest, and a card of its own is exactly the junk row the
// bug report is about. An orphan _abrv - no parent beside it - is a normal
// dictionary and must still be found.
func TestDiscoverHidesAbbrevCompanion(t *testing.T) {
	RegisterFormat(".dsl", func(path string) (Dictionary, error) { return fakeDict{}, nil })
	dir := t.TempDir()
	for _, f := range []string{"X.dsl", "X_abrv.dsl", "Y_abrv.dsl"} {
		if err := os.WriteFile(filepath.Join(dir, f), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := Discover(dir)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, p := range got {
		seen[filepath.Base(p)] = true
	}
	if len(got) != 2 || !seen["X.dsl"] || !seen["Y_abrv.dsl"] {
		t.Fatalf("Discover = %v, want X.dsl and Y_abrv.dsl only", got)
	}
	// an explicit path still opens: hiding is a discovery rule, not a ban
	if !IsDictionaryFile(filepath.Join(dir, "X_abrv.dsl")) {
		t.Error("an explicit companion path must remain openable")
	}
}
