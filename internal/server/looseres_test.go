// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"os"
	"path/filepath"
	"testing"
)

// The folder a DSL sits in answers /res/ for asset names only, like an .mdx's:
// a reference to any other kind of file there - or below it - is never served.
func TestLooseResourcesAreAssetsOnly(t *testing.T) {
	s, row, _ := settled(t)
	dir := filepath.Dir(row.Path)
	files := map[string]int{
		"private.dat":     404,
		"sub/private.dat": 404,
		"loose.css":       200,
		"sub/picture.png": 200,
	}
	for name := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for name, want := range files {
		if rec := get(s, "/res/"+row.ID+"/"+name); rec.Code != want {
			t.Errorf("/res/…/%s: %d, want %d", name, rec.Code, want)
		}
	}
}
