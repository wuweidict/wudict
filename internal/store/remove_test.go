// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func writeFile(t *testing.T, path string, n int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, n), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRemovePreparedDeletesFolder(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WUDICT_DB_DIR", root)
	dir := filepath.Join(root, "Oxford")
	writeFile(t, filepath.Join(dir, TextDBName), 1000)
	writeFile(t, filepath.Join(dir, MediaDBName), 2000)
	writeFile(t, filepath.Join(dir, "info.txt"), 24)

	n, err := RemovePrepared(dir)
	if err != nil {
		t.Fatalf("RemovePrepared: %v", err)
	}
	if n != 3024 {
		t.Errorf("freed = %d, want 3024", n)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("folder still there: %v", err)
	}
}

// Everything the library does not directly contain is refused, whatever it is.
func TestRemovePreparedGuards(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WUDICT_DB_DIR", root)
	outside := t.TempDir()
	writeFile(t, filepath.Join(outside, "keep.txt"), 10)
	nested := filepath.Join(root, "Oxford", "sub")
	writeFile(t, filepath.Join(nested, "x"), 10)
	writeFile(t, filepath.Join(root, "loose.db"), 10)

	for _, c := range []struct{ name, dir string }{
		{"empty", ""},
		{"the library itself", root},
		{"outside the library", outside},
		{"a grandchild, not a dictionary folder", nested},
		{"a file, not a folder", filepath.Join(root, "loose.db")},
		{"a folder that does not exist", filepath.Join(root, "nope")},
	} {
		if _, err := RemovePrepared(c.dir); err == nil {
			t.Errorf("%s: RemovePrepared(%q) succeeded, want refusal", c.name, c.dir)
		}
	}
	// nothing was touched
	for _, p := range []string{filepath.Join(outside, "keep.txt"), filepath.Join(nested, "x")} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s was deleted: %v", p, err)
		}
	}
}

// A symlink planted in the library must be rejected rather than followed:
// os.RemoveAll would unlink the link and report success, and a future
// implementation following it would delete a tree outside the library.
func TestRemovePreparedRejectsSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privilege on Windows")
	}
	root := t.TempDir()
	t.Setenv("WUDICT_DB_DIR", root)
	target := t.TempDir()
	writeFile(t, filepath.Join(target, "precious"), 10)
	link := filepath.Join(root, "Oxford")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := RemovePrepared(link); err == nil {
		t.Fatal("removing a symlink succeeded, want refusal")
	}
	if _, err := os.Stat(filepath.Join(target, "precious")); err != nil {
		t.Errorf("target was touched: %v", err)
	}
}

func TestTreeSize(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a"), 100)
	writeFile(t, filepath.Join(dir, "sub", "b"), 250)
	if got := TreeSize(dir); got != 350 {
		t.Errorf("TreeSize(dir) = %d, want 350", got)
	}
	if got := TreeSize(filepath.Join(dir, "a")); got != 100 {
		t.Errorf("TreeSize(file) = %d, want 100", got)
	}
	if got := TreeSize(filepath.Join(dir, "missing")); got != 0 {
		t.Errorf("TreeSize(missing) = %d, want 0", got)
	}
}
