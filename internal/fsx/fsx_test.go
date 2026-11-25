// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package fsx

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"testing/iotest"
)

func TestReadBounded(t *testing.T) {
	dir := t.TempDir()
	small := filepath.Join(dir, "small")
	os.WriteFile(small, []byte("hello"), 0o644)
	big := filepath.Join(dir, "big")
	os.WriteFile(big, []byte(strings.Repeat("x", 11)), 0o644)
	tests := []struct {
		name, path string
		want       string
		err        error
	}{
		{"fits", small, "hello", nil},
		{"exactly the limit", filepath.Join(dir, "ten"), strings.Repeat("y", 10), nil},
		{"too large", big, "", ErrTooLarge},
		{"missing", filepath.Join(dir, "none"), "", fs.ErrNotExist},
		{"a directory", dir, "", ErrNotRegular},
	}
	os.WriteFile(filepath.Join(dir, "ten"), []byte(strings.Repeat("y", 10)), 0o644)
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b, err := ReadBounded(tc.path, 10)
			if tc.err != nil {
				if !errors.Is(err, tc.err) {
					t.Fatalf("err = %v, want %v", err, tc.err)
				}
				return
			}
			if err != nil || string(b) != tc.want {
				t.Fatalf("got %q, %v", b, err)
			}
		})
	}
}

func mode(t *testing.T, p string) fs.FileMode {
	t.Helper()
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Mode().Perm()
}

func TestWriteAtomic(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix modes")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "sub", "f.txt")
	if err := WriteAtomic(p, []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != "one" || mode(t, p) != 0o644 {
		t.Fatalf("new file: %q %v", b, mode(t, p))
	}
	os.Chmod(p, 0o640)
	WriteAtomic(p, []byte("two"), 0o644)
	if mode(t, p) != 0o640 {
		t.Errorf("an existing mode is not kept: %v", mode(t, p))
	}
	os.Chmod(p, 0o000)
	WriteAtomic(p, []byte("three"), 0o644)
	if mode(t, p)&0o600 != 0o600 {
		t.Errorf("a replaced mode-000 file is not readable by its owner: %v", mode(t, p))
	}
	os.Chmod(p, 0o644)
	if err := WriteAtomicExact(p, []byte("data"), 0o600); err != nil || mode(t, p) != 0o600 {
		t.Errorf("Exact did not force the mode: %v %v", mode(t, p), err)
	}
	// a symlink is written through, not replaced
	target := filepath.Join(dir, "target")
	os.WriteFile(target, []byte("old"), 0o644)
	link := filepath.Join(dir, "link")
	os.Symlink(target, link)
	WriteAtomic(link, []byte("new"), 0o644)
	if fi, _ := os.Lstat(link); fi.Mode()&os.ModeSymlink == 0 {
		t.Error("the link was replaced by a file")
	}
	if b, _ := os.ReadFile(target); string(b) != "new" {
		t.Errorf("target = %q", b)
	}
	// a failing stream leaves the previous file, and no temp file behind
	n, err := WriteAtomicFrom(target, iotest.ErrReader(errors.New("boom")), 0o644)
	if err == nil || n != 0 {
		t.Fatalf("a failing stream: n=%d err=%v", n, err)
	}
	if b, _ := os.ReadFile(target); string(b) != "new" {
		t.Errorf("the previous file changed: %q", b)
	}
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), ".") {
			t.Errorf("temp file left behind: %s", e.Name())
		}
	}
}

func TestExistsAndRemove(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "f")
	os.WriteFile(f, nil, 0o644)
	if !FileExists(f) || FileExists(dir) || FileExists("") || FileExists(filepath.Join(dir, "none")) {
		t.Error("FileExists")
	}
	if !DirExists(dir) || DirExists(f) || DirExists("") {
		t.Error("DirExists")
	}
	if err := RemoveIfExists(f); err != nil || FileExists(f) {
		t.Errorf("RemoveIfExists: %v", err)
	}
	if err := RemoveIfExists(f); err != nil {
		t.Errorf("removing what is gone: %v", err)
	}
}

func TestSamePath(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "f")
	os.WriteFile(f, nil, 0o644)
	// filepath.Rel from the working directory fails across Windows volumes
	// (checkout on D:, TempDir on C:); a chdir makes the relative spelling exist.
	t.Chdir(dir)
	if !SamePath(f, "f") || !SamePath(f, filepath.Join(dir, ".", "f")) {
		t.Error("the same file, spelled two ways")
	}
	if SamePath(f, "") || SamePath(f, filepath.Join(dir, "g")) {
		t.Error("different files")
	}
	if runtime.GOOS != "windows" {
		link := filepath.Join(dir, "link")
		os.Symlink(f, link)
		if !SamePath(f, link) {
			t.Error("a symlink to the file")
		}
	}
}
