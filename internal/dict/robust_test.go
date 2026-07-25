// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package dict

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestOpenNeverPanics: corrupt and truncated files must produce errors,
// not panics — the registered opener may blow up internally; Open must
// contain it.
func TestOpenNeverPanics(t *testing.T) {
	RegisterFormat(".panics", func(path string) (Dictionary, error) {
		var b []byte
		_ = b[5] // deliberate out-of-range panic, as a broken parser would
		return nil, nil
	})
	RegisterReader(".panics", func(path string) (Reader, error) {
		panic("parser exploded")
	})
	if _, err := Open("x.panics"); err == nil {
		t.Fatal("panic must surface as error")
	} else if got := err.Error(); got == "" {
		t.Fatal("empty error")
	}
	if _, err := OpenReader("x.panics"); err == nil {
		t.Fatal("reader panic must surface as error")
	}
}

// Truncations of real container headers across registered formats must
// error cleanly. The format packages register themselves in their own
// tests; here we synthesize minimal corrupt files for each extension the
// core knows how to fail on.
func TestCorruptFilesError(t *testing.T) {
	dir := t.TempDir()
	cases := map[string][]byte{
		"empty.panics": {},
	}
	for name, data := range cases {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := Open(p); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
	// unknown extension stays a clean error
	if _, err := Open(filepath.Join(dir, "x.unknownext")); err == nil {
		t.Error("unknown ext must error")
	}
	_ = fmt.Sprint() // keep fmt for future cases
}
