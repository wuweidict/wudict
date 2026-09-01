// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package morph

import (
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestNormalize(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "kot\tkota\tkotów\n", "kot\tkota\tkotów\n"},
		{"no trailing newline", "kot\tkota", "kot\tkota\n"},
		{"crlf", "kot\tkota\r\npies\tpsa\r\n", "kot\tkota\npies\tpsa\n"},
		{"bom", "\xef\xbb\xbfkot\tkota\n", "kot\tkota\n"},
		{"lower cased", "Kot\tKota\tKOTÓW\n", "kot\tkota\tkotów\n"},
		{"blank lines dropped", "\n\nkot\tkota\n\n", "kot\tkota\n"},
		// The reason this file exists: golem fails the WHOLE load on one of
		// these, so a hand-edited list must not be able to cost a language.
		{"single field dropped", "kot\npies\tpsa\n", "pies\tpsa\n"},
		{"whitespace-only line dropped", "   \npies\tpsa\n", "pies\tpsa\n"},
		{"empty fields collapsed", "kot\t\t\tkota\n", "kot\tkota\n"},
		{"leading tab", "\tkot\tkota\n", "kot\tkota\n"},
		{"stray spaces trimmed", " kot \t kota \n", "kot\tkota\n"},
		{"inner space kept", "kot domowy\tkota domowego\n", "kot domowy\tkota domowego\n"},
		{"lone lemma with tab dropped", "kot\t\n", ""},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := string(normalize([]byte(tt.in))); got != tt.want {
				t.Errorf("normalize(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// TestNormalizeFeedsGolem is the contract the whole file rests on: whatever
// normalize emits, golem accepts. A parser that rejects one line rejects the
// language, so this asserts the filter is complete rather than merely tidy.
func TestNormalizeFeedsGolem(t *testing.T) {
	raw := "\xef\xbb\xbfKot\tkota\r\n\nbroken\n   \npies\t\t psa \r\nlone\t\n"
	dir := t.TempDir()
	path := filepath.Join(dir, "pl.txt")
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	c := New(1, dir)
	if !c.Supports("pl") {
		t.Fatal("pl not supported after installing pl.txt")
	}
	if got, ok := c.Lemma("pl", "Kota"); !ok || got != "kot" {
		t.Errorf(`Lemma("pl","Kota") = %q,%v; want "kot",true`, got, ok)
	}
	if got, ok := c.Lemma("pl", "psa"); !ok || got != "pies" {
		t.Errorf(`Lemma("pl","psa") = %q,%v; want "pies",true`, got, ok)
	}
	if _, ok := c.Lemma("pl", "broken"); ok {
		t.Error(`"broken" was a one-field line: it must not be indexed`)
	}
}

func TestLemmaStem(t *testing.T) {
	tests := []struct {
		name string
		stem string
		ok   bool
	}{
		{"pl.txt", "pl", true},
		{"PL.TXT", "pl", true},
		{"pol.tsv", "pol", true},
		{"polish.txt.gz", "polish", true},
		{"ru.tsv.gz", "ru", true},
		{"README.md", "", false},
		{"pl", "", false},
		{"pl.txt.zip", "", false},
		{".txt", "", false},
		{".txt.gz", "", false},
		{"notes.txt.gz.bak", "", false},
	}
	for _, tt := range tests {
		stem, ok := lemmaStem(tt.name)
		if stem != tt.stem || ok != tt.ok {
			t.Errorf("lemmaStem(%q) = %q,%v; want %q,%v", tt.name, stem, ok, tt.stem, tt.ok)
		}
	}
}

func TestScanDir(t *testing.T) {
	dir := t.TempDir()
	write := func(name string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte("a\tb\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("pl.txt")
	write("polish.tsv") // same language, later by name: ignored
	write("czech.txt.gz")
	write("turkish.TSV")
	write("README.md")
	write("nosuchlanguage.txt")
	if err := os.Mkdir(filepath.Join(dir, "sv.txt"), 0o755); err != nil {
		t.Fatal(err)
	}

	got := scanDir(dir)
	want := map[string]string{
		"pl": filepath.Join(dir, "pl.txt"),
		"cs": filepath.Join(dir, "czech.txt.gz"),
		"tr": filepath.Join(dir, "turkish.TSV"),
	}
	if len(got) != len(want) {
		t.Fatalf("scanDir = %v, want %v", got, want)
	}
	for code, path := range want {
		if got[code] != path {
			t.Errorf("scanDir[%q] = %q, want %q", code, got[code], path)
		}
	}

	if scanDir("") != nil {
		t.Error(`scanDir("") must be nil`)
	}
	if scanDir(filepath.Join(dir, "nope")) != nil {
		t.Error("a missing folder is the normal case, not an error")
	}
	if scanDir(filepath.Join(dir, "README.md")) != nil {
		t.Error("a file where a folder was configured must be ignored")
	}
}

func TestFilePackGzip(t *testing.T) {
	dir := t.TempDir()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write([]byte("Kot\tkota\n")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "pl.txt.gz")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	b, err := (filePack{code: "pl", path: path}).GetResource()
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "kot\tkota\n" {
		t.Errorf("GetResource = %q", b)
	}

	// Named .gz but not gzip: an error, not a lemma list of binary noise.
	bad := filepath.Join(dir, "cs.txt.gz")
	if err := os.WriteFile(bad, []byte("kot\tkota\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := (filePack{code: "cs", path: bad}).GetResource(); err == nil {
		t.Error("a non-gzip .gz must fail to load")
	}
	if _, err := (filePack{code: "cs", path: filepath.Join(dir, "gone.txt")}).GetResource(); err == nil {
		t.Error("a missing file must fail to load")
	}
}

// TestFilePackCap points a compression bomb at the folder: 65 MB of one
// repeated line is a few kilobytes on disk, and would be several hundred
// megabytes of heap once golem indexed it.
func TestFilePackCap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pl.txt.gz")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw, err := gzip.NewWriterLevel(f, gzip.BestSpeed)
	if err != nil {
		t.Fatal(err)
	}
	chunk := bytes.Repeat([]byte("kot\tkota\n"), 1<<16) // 576 KB
	for n := 0; n <= MaxPackBytes; n += len(chunk) {
		if _, err := zw.Write(chunk); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	_, err = (filePack{code: "pl", path: path}).GetResource()
	if err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Fatalf("oversized lemma data: err = %v, want a size refusal", err)
	}

	// And the refusal must reach the caller as "no lemma", not as a panic or
	// a half-built lemmatizer.
	c := New(1, dir)
	if _, ok := c.Lemma("pl", "kota"); ok {
		t.Error("a rejected file must yield no lemma")
	}
}

// TestFileOverridesBuiltin is the precedence rule. It also proves the built-in
// pack is not loaded when a file supplies the language: "knew" answers from
// the fixture, and the 7 MB en pack never appears.
func TestFileOverridesBuiltin(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "en.txt"), []byte("KNOWN\tknew\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := New(1, dir)
	if got, ok := c.Lemma("en", "knew"); !ok || got != "known" {
		t.Errorf(`Lemma("en","knew") = %q,%v; want "known",true (the file, not the built-in)`, got, ok)
	}
}

func TestDisabledIgnoresFolder(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pl.txt"), []byte("kot\tkota\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := New(0, dir)
	if c.Supports("pl") || c.Supports("en") {
		t.Error("MORPH_CACHE=0 must not report support for anything")
	}
	if _, ok := c.Lemma("pl", "kota"); ok {
		t.Error("MORPH_CACHE=0 must not lemmatize")
	}
}

func TestSupportsNil(t *testing.T) {
	var c *Cache
	if c.Supports("en") {
		t.Error("a nil cache supports nothing")
	}
}

// TestRescan is the hook `wudict lemmas download` leaves for a running server
// (D88): a language installed after the cache was built becomes searchable
// without a restart, and one loaded already is not torn out from under the
// searches holding it.
func TestRescan(t *testing.T) {
	dir := t.TempDir()
	c := New(2, dir)
	if c.Supports("pl") {
		t.Fatal("pl before anything was installed")
	}

	if err := os.WriteFile(filepath.Join(dir, "pl.txt"), []byte("kot\tkota\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if c.Supports("pl") {
		t.Error("the index must not be re-read on every Supports")
	}
	c.Rescan()
	if !c.Supports("pl") {
		t.Fatal("Rescan did not pick up pl.txt")
	}
	if got, ok := c.Lemma("pl", "kota"); !ok || got != "kot" {
		t.Errorf(`Lemma("pl","kota") = %q,%v; want "kot",true`, got, ok)
	}

	// Replacing the file under a loaded language leaves the loaded pack alone.
	if err := os.WriteFile(filepath.Join(dir, "pl.txt"), []byte("pies\tpsa\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c.Rescan()
	if got, _ := c.Lemma("pl", "kota"); got != "kot" {
		t.Errorf("a loaded pack must survive Rescan; got %q", got)
	}

	// Removing it stops new loads, and a disabled cache never looks at all.
	if err := os.Remove(filepath.Join(dir, "pl.txt")); err != nil {
		t.Fatal(err)
	}
	c.Rescan()
	if c.Supports("pl") {
		t.Error("Rescan did not notice the file was gone")
	}
	off := New(0, dir)
	off.Rescan()
	if off.Supports("pl") {
		t.Error("MORPH_CACHE=0 must ignore Rescan")
	}
}

// TestRescanRace guards the reason Cache.files is an atomic pointer: Supports
// runs for every dictionary of a search that found nothing, so it must not
// take a lock that a folder rescan holds. Run under -race.
func TestRescanRace(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pl.txt"), []byte("kot\tkota\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := New(2, dir)

	done := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				default:
				}
				c.Supports("pl")
				c.Lemma("pl", "kota")
			}
		}()
	}
	for i := 0; i < 200; i++ {
		c.Rescan()
	}
	close(done)
	wg.Wait()
}
