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
