// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wuweidict/wudict/internal/resource"
)

func TestLinkSibling(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{filepath.Join("lib", "AHD5", "text.db"), filepath.Join("lib", "AHD5", "media.link.db")},
		{filepath.Join("lib", "AHD5.text.db"), filepath.Join("lib", "AHD5.media.link.db")},
		{filepath.Join("lib", "something.db"), ""},
	} {
		if got := LinkSibling(tc.in); got != tc.want {
			t.Errorf("LinkSibling(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// container writes a stand-in .mdd and returns the Part describing it.
func container(t *testing.T, path string, body string) resource.Part {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return resource.Part{Path: path, Size: st.Size(), MTime: st.ModTime().Unix()}
}

func TestLinksRoundTrip(t *testing.T) {
	dir := t.TempDir()
	part := container(t, filepath.Join(dir, "x.mdd"), "container bytes")
	db := LinkDBPath(dir)
	links := []resource.Link{
		{Name: "audio/word.mp3", MIME: "audio/mpeg", Part: 0, Off: 10, Size: 40},
		{Name: "last.png", MIME: "image/png", Part: 0, Off: 90, Size: -1},
	}
	if err := WriteLinks(db, []resource.Part{part}, links, "uuid-1"); err != nil {
		t.Fatal(err)
	}

	l, err := OpenLinks(db, "uuid-1")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if got := l.Parts(); len(got) != 1 || got[0] != part {
		t.Fatalf("Parts() = %+v, want %+v", got, part)
	}
	for _, want := range links {
		// Looked up the way an article names it - mixed case, backslashes -
		// because the stored name is the folded key, not the article's spelling.
		got, ok := l.Lookup("\\" + want.Name)
		if !ok {
			t.Fatalf("Lookup(%q) missed", want.Name)
		}
		if got != want {
			t.Fatalf("Lookup(%q) = %+v, want %+v", want.Name, got, want)
		}
	}
	if _, ok := l.Lookup("no/such.png"); ok {
		t.Error("a name nobody recorded must miss")
	}
	if _, ok := l.Lookup(""); ok {
		t.Error("an empty name must miss")
	}
}

// The safety property the whole design rests on: offsets belong to the exact
// bytes they were taken from. A container that changed must make the cache
// unusable, because the offsets would still resolve - to the wrong file.
func TestLinksRejectChangedContainer(t *testing.T) {
	for _, tc := range []struct {
		name  string
		spoil func(t *testing.T, path string)
	}{
		{"size differs", func(t *testing.T, path string) {
			if err := os.WriteFile(path, []byte("a different container"), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{"mtime differs", func(t *testing.T, path string) {
			// Same length, different moment: a recompressed container that
			// happens to keep its size is exactly the case size alone misses.
			when := time.Now().Add(-2 * time.Hour)
			if err := os.Chtimes(path, when, when); err != nil {
				t.Fatal(err)
			}
		}},
		{"container gone", func(t *testing.T, path string) {
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			mdd := filepath.Join(dir, "x.mdd")
			part := container(t, mdd, "container bytes")
			db := LinkDBPath(dir)
			if err := WriteLinks(db, []resource.Part{part},
				[]resource.Link{{Name: "a.png", Part: 0, Off: 0, Size: 4}}, "uuid-1"); err != nil {
				t.Fatal(err)
			}
			tc.spoil(t, mdd)
			l, err := OpenLinks(db, "uuid-1")
			if err == nil {
				l.Close()
				t.Fatal("a changed container must invalidate the locator")
			}
		})
	}
}

func TestLinksRejectForeignUUID(t *testing.T) {
	dir := t.TempDir()
	part := container(t, filepath.Join(dir, "x.mdd"), "container bytes")
	db := LinkDBPath(dir)
	if err := WriteLinks(db, []resource.Part{part},
		[]resource.Link{{Name: "a.png", Part: 0, Off: 0, Size: 4}}, "uuid-1"); err != nil {
		t.Fatal(err)
	}
	if l, err := OpenLinks(db, "uuid-2"); err == nil {
		l.Close()
		t.Fatal("a locator belonging to another dictionary must be rejected")
	}
}
