// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wuweidict/wudict/internal/dict"
)

// testFont is a file dict.CheckFont takes for an OpenType font; tag makes two
// of them differ.
func testFont(tag string) []byte {
	return append([]byte("OTTO\x00\x01\x00\x00"), tag...)
}

// writeLater writes p and moves its time (and its folder's) forward, so a
// coarse filesystem clock cannot hide the change from the row key.
func writeLater(t *testing.T, p string, data []byte, by time.Duration) {
	t.Helper()
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(by)
	for _, q := range []string{p, filepath.Dir(p)} {
		if err := os.Chtimes(q, at, at); err != nil {
			t.Fatal(err)
		}
	}
}

func get(s *Server, target string, hdr ...string) *httptest.ResponseRecorder {
	req := newRequest("GET", target, nil)
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	return rec
}

// The whole path a font takes: dropped beside the source, it is in the row,
// served, replaced, refused when it is not a font, and gone when deleted.
func TestDictionaryFont(t *testing.T) {
	s, row, tag := settled(t)
	if row.Font != "" || row.FontError != "" {
		t.Fatalf("no font yet, but the row says %q / %q", row.Font, row.FontError)
	}
	fontPath := dict.Stem(row.Path) + ".otf"
	font := testFont("one")

	writeLater(t, fontPath, font, 2*time.Second)
	code, rows, tag := dictsWith(t, s, tag)
	if code != 200 || len(rows) != 1 {
		t.Fatalf("a font added: %d, %d rows", code, len(rows))
	}
	row = rows[0]
	if !strings.HasPrefix(row.Font, fontURL+pathID(fontPath)+"?v=") {
		t.Fatalf("row font = %q", row.Font)
	}
	if tag == "" {
		t.Error("the answer that found the font was not offered for keeping")
	}

	rec := get(s, row.Font)
	if rec.Code != 200 || rec.Body.String() != string(font) {
		t.Fatalf("GET %s: %d, %d bytes", row.Font, rec.Code, rec.Body.Len())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "font/otf" {
		t.Errorf("Content-Type = %q", ct)
	}
	if rec := get(s, row.Font, "Range", "bytes=0-3"); rec.Code != 206 || rec.Body.String() != "OTTO" {
		t.Errorf("Range: %d %q", rec.Code, rec.Body.String())
	}

	// replaced in place: a new URL, so a cached copy is never shown
	writeLater(t, fontPath, testFont("two"), 4*time.Second)
	_, rows, _ = dictsWith(t, s, tag)
	if len(rows) != 1 || rows[0].Font == "" || rows[0].Font == row.Font {
		t.Fatalf("a replaced font: %+v", rows)
	}

	// not a font: said once, in the row and the About, and never served
	writeLater(t, fontPath, []byte("<html>not a font</html>"), 6*time.Second)
	_, rows, _ = dictsWith(t, s, `"stale"`)
	if len(rows) != 1 || rows[0].Font != "" || !strings.Contains(rows[0].FontError, filepath.Base(fontPath)) {
		t.Fatalf("a bad font: %+v", rows)
	}
	if rec := get(s, fontURL+pathID(fontPath)); rec.Code != 404 {
		t.Errorf("a bad font was served: %d", rec.Code)
	}
	if rec := get(s, "/api/about?dict="+row.ID); !strings.Contains(rec.Body.String(), "is not a font wudict can use") {
		t.Errorf("About does not say so: %s", rec.Body.String())
	}

	// deleted: nothing left of it
	if err := os.Remove(fontPath); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(8 * time.Second)
	os.Chtimes(filepath.Dir(fontPath), later, later)
	_, rows, _ = dictsWith(t, s, `"stale"`)
	if len(rows) != 1 || rows[0].Font != "" || rows[0].FontError != "" {
		t.Fatalf("a deleted font: %+v", rows)
	}
	if rec := get(s, fontURL+pathID(fontPath)); rec.Code != 404 {
		t.Errorf("a deleted font was served: %d", rec.Code)
	}
}

// Dictionaries sharing a folder's font.ttf share its URL: the page fetches
// and decodes the file once however many of them it shows.
func TestFolderFontIsOneURL(t *testing.T) {
	isolatedDBDir(t)
	dir := t.TempDir()
	for _, n := range []string{"a.dsl", "b.dsl"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte(strings.Replace(sampleDSL, "Server Test Dict", n, 1)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "font.woff2"), []byte("wOF2OTTO...."), 0o644); err != nil {
		t.Fatal(err)
	}
	reg, err := NewRegistry([]string{dir}, false)
	if err != nil {
		t.Fatal(err)
	}
	closeBackends(t, reg)
	s := New(reg)
	_, rows, _ := dictsWith(t, s, "")
	if len(rows) != 2 || rows[0].Font == "" || rows[0].Font != rows[1].Font {
		t.Fatalf("two dictionaries, one font.woff2: %q / %q", rows[0].Font, rows[1].Font)
	}
	if rec := get(s, rows[0].Font); rec.Code != 200 || rec.Header().Get("Content-Type") != "font/woff2" {
		t.Errorf("GET %s: %d %q", rows[0].Font, rec.Code, rec.Header().Get("Content-Type"))
	}
}

// A page holding a list from before a restart asks for a font no row key or
// derivation in this process has resolved; it is found and checked then.
func TestFontServedBeforeDerivation(t *testing.T) {
	s, row, _ := settled(t)
	p := dict.Stem(row.Path) + ".ttf"
	writeLater(t, p, testFont("early"), 2*time.Second)
	e, err := s.reg.get(row.ID)
	if err != nil {
		t.Fatal(err)
	}
	e.font.Store(nil) // as a fresh process holds it
	if rec := get(s, fontURL+pathID(p)); rec.Code != 200 {
		t.Errorf("GET before any derivation: %d", rec.Code)
	}
	if rec := get(s, fontURL+"000000000000"); rec.Code != 404 {
		t.Errorf("unknown font: %d", rec.Code)
	}
}

// Only a dictionary with a folder beside it has one: not the app's own, and
// not one whose path is its prepared database.
func TestResolveFontNeedsASourceFolder(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"text.ttf", "font.ttf", "x.ttf"} {
		if err := os.WriteFile(filepath.Join(dir, n), testFont("F"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, e := range []*entry{
		{Path: filepath.Join(dir, "text.db")},
		{Path: filepath.Join(dir, "x.mdx"), builtin: true},
	} {
		if p := e.resolveFont(); p != "" {
			t.Errorf("%s (builtin %v) resolved %s", e.Path, e.builtin, p)
		}
	}
	if p := (&entry{Path: filepath.Join(dir, "x.mdx")}).resolveFont(); filepath.Base(p) != "x.ttf" {
		t.Errorf("a source resolved %q", p)
	}
}
