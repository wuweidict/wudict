// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// openFileArgs is what stands between a desktop file association and the
// "unknown command" path, so both answers matter: a dictionary must resolve to
// serve flags, and anything else must resolve to nil so Main still reports a
// typo instead of quietly starting a server on the current directory.
func TestOpenFileArgs(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.mdx", "a.mdd", "b.SLOB", "c.dsl", "d.bgl", "e.txt", "dir.mdx"} {
		p := filepath.Join(sub, name)
		if name == "dir.mdx" {
			if err := os.Mkdir(p, 0o755); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	tests := []struct {
		name string
		arg  string
		want bool // want serve flags rather than nil
	}{
		{"mdx", "a.mdx", true},
		{"uppercase extension", "b.SLOB", true},
		{"dsl", "c.dsl", true},
		{"bgl", "d.bgl", true},
		{"mdd is a resource container, not a dictionary", "a.mdd", false},
		{"unrelated extension", "e.txt", false},
		{"missing file", "gone.mdx", false},
		{"directory named like a dictionary", "dir.mdx", false},
		{"bare word (a mistyped command)", "lookupp", false},
		{"empty", "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			arg := tc.arg
			if arg != "" {
				arg = filepath.Join(sub, arg)
			}
			got := openFileArgs(arg)
			if (got != nil) != tc.want {
				t.Fatalf("openFileArgs(%q) = %v, want non-nil=%v", arg, got, tc.want)
			}
			if !tc.want {
				return
			}
			if len(got) != 2 || got[0] != "--dict-dir" {
				t.Fatalf("openFileArgs(%q) = %v, want [--dict-dir <folder>]", arg, got)
			}
			// The FOLDER, not the file: companions (.mdd beside .mdx, .idx
			// beside .ifo) only pair up through a folder scan.
			if got[1] != sub {
				t.Fatalf("served %q, want the file's folder %q", got[1], sub)
			}
		})
	}
}
