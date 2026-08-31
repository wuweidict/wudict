// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package resource

import (
	"archive/zip"
	"io"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/text/encoding/charmap"
)

func cp1251(t *testing.T, s string) string {
	t.Helper()
	b, err := charmap.Windows1251.NewEncoder().String(s)
	if err != nil {
		t.Fatalf("encode %q: %v", s, err)
	}
	return b
}

// writeZip stores each name verbatim. A name that is not valid UTF-8 leaves
// the general-purpose UTF-8 flag clear, which is exactly what a Windows
// archiver produces.
func writeZip(t *testing.T, path string, names []string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	for _, n := range names {
		w, err := zw.Create(n)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(w, "body:"+n); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, s Source, name string) string {
	t.Helper()
	rc, err := s.Open(name)
	if err != nil {
		return ""
	}
	defer rc.Close()
	b, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestZipLegacyNames(t *testing.T) {
	dir := t.TempDir()
	zp := filepath.Join(dir, "d.dsl.files.zip")
	writeZip(t, zp, []string{
		cp1251(t, "кубок.jpg"),
		cp1251(t, "Кубок Provenzale Water Goblet.jpg"),
		"A set of three green glass rummer.jpg",
		"files/" + cp1251(t, "вложенный.wav"),
		"utf8/日本語.png",
	})
	z, err := OpenZip(zp)
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()

	cases := []struct {
		ask  string
		want string // the stored name whose body must come back
	}{
		{"кубок.jpg", cp1251(t, "кубок.jpg")},
		{"Кубок Provenzale Water Goblet.jpg", cp1251(t, "Кубок Provenzale Water Goblet.jpg")},
		// Case and normalization are not the article's problem.
		{"КУБОК.JPG", cp1251(t, "кубок.jpg")},
		{"A SET of three green glass rummer.jpg", "A set of three green glass rummer.jpg"},
		// A file the archive keeps in a folder the article never mentions.
		{"вложенный.wav", "files/" + cp1251(t, "вложенный.wav")},
		{"files/вложенный.wav", "files/" + cp1251(t, "вложенный.wav")},
		// A genuinely UTF-8 name still resolves as itself.
		{"日本語.png", "utf8/日本語.png"},
		// Path climbing never escapes the container.
		{"../кубок.jpg", cp1251(t, "кубок.jpg")},
	}
	for _, c := range cases {
		if got := read(t, z, c.ask); got != "body:"+c.want {
			t.Errorf("Open(%q) = %q, want body of %q", c.ask, got, c.want)
		}
	}
	if got := read(t, z, "nothing.jpg"); got != "" {
		t.Errorf("Open(missing) = %q, want miss", got)
	}

	// Display names are decoded for packing, not left as mojibake.
	names := z.List()
	found := false
	for _, n := range names {
		if n == "кубок.jpg" {
			found = true
		}
	}
	if !found {
		t.Errorf("List() = %q, want a decoded \"кубок.jpg\"", names)
	}
}

func TestZipAmbiguousBasename(t *testing.T) {
	dir := t.TempDir()
	zp := filepath.Join(dir, "a.zip")
	writeZip(t, zp, []string{"a/x.jpg", "b/x.jpg"})
	z, err := OpenZip(zp)
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	if got := read(t, z, "x.jpg"); got != "" {
		t.Errorf("ambiguous basename resolved to %q, want miss", got)
	}
	if got := read(t, z, "b/x.jpg"); got != "body:b/x.jpg" {
		t.Errorf("Open(b/x.jpg) = %q", got)
	}
}

func TestDir(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"Кубок.jpg", "plain.jpg", "sub/nested.jpg"} {
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(f)), []byte("body:"+f), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	d := NewDir(root)
	for _, c := range [][2]string{
		{"Кубок.jpg", "Кубок.jpg"},
		{"кубок.JPG", "Кубок.jpg"},
		{"plain.jpg", "plain.jpg"},
		{"nested.jpg", "sub/nested.jpg"},
		{"sub/nested.jpg", "sub/nested.jpg"},
	} {
		if got := read(t, d, c[0]); got != "body:"+c[1] {
			t.Errorf("Dir.Open(%q) = %q, want body of %q", c[0], got, c[1])
		}
	}
	if got := read(t, d, "sub"); got != "" {
		t.Errorf("Dir.Open(dir) = %q, want miss", got)
	}
	if len(d.List()) != 3 {
		t.Errorf("Dir.List() = %q, want 3 entries", d.List())
	}

	// The shared folder is exact-only: it is never walked, so it never
	// contributes a neighbouring dictionary's files to packing.
	e := NewDirExact(root)
	if got := read(t, e, "plain.jpg"); got != "body:plain.jpg" {
		t.Errorf("DirExact.Open(plain.jpg) = %q", got)
	}
	if got := read(t, e, "nested.jpg"); got != "" {
		t.Errorf("DirExact resolved a folded name: %q", got)
	}
	if e.List() != nil {
		t.Errorf("DirExact.List() = %q, want nil", e.List())
	}
}

func TestKey(t *testing.T) {
	cases := [][2]string{
		{"A/B.JPG", "a/b.jpg"},
		{"./x.jpg", "x.jpg"},
		{"/x.jpg", "x.jpg"},
		{`sub\x.jpg`, "sub/x.jpg"},
		{"../../x.jpg", "x.jpg"},
		{"", ""},
		{".", ""},
		{"é.jpg", "é.jpg"}, // NFD in, NFC out
	}
	for _, c := range cases {
		if got := Key(c[0]); got != c[1] {
			t.Errorf("Key(%q) = %q, want %q", c[0], got, c[1])
		}
	}
}

func TestReadingsDisplay(t *testing.T) {
	cases := []struct {
		raw          string
		utf8Declared bool
		want         string
	}{
		{cp1251(t, "кубок.jpg"), false, "кубок.jpg"},
		{cp1251(t, "Кубок Provenzale Water Goblet.jpg"), false, "Кубок Provenzale Water Goblet.jpg"},
		{"plain.jpg", false, "plain.jpg"},
		// Valid UTF-8 bytes are the name, flag or no flag.
		{"café.jpg", false, "café.jpg"},
		{"日本語.png", true, "日本語.png"},
	}
	for _, c := range cases {
		if got, _ := readings(c.raw, c.utf8Declared); got != c.want {
			t.Errorf("readings(%q) display = %q, want %q", c.raw, got, c.want)
		}
	}
}
