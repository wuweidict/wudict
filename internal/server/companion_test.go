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

// folderOf is the /folder/ prefix of the folder p sits in.
func folderOf(p string) string { return folderURL + pathID(filepath.Dir(p)) + "/" }

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
	if !strings.HasPrefix(row.Font, folderOf(fontPath)+filepath.Base(fontPath)+"?v=") {
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

	// not a font: said once, in the row and the About
	writeLater(t, fontPath, []byte("<html>not a font</html>"), 6*time.Second)
	_, rows, _ = dictsWith(t, s, `"stale"`)
	if len(rows) != 1 || rows[0].Font != "" || !strings.Contains(rows[0].FontError, filepath.Base(fontPath)) {
		t.Fatalf("a bad font: %+v", rows)
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
	if rec := get(s, folderOf(fontPath)+filepath.Base(fontPath)); rec.Code != 404 {
		t.Errorf("a deleted font was served: %d", rec.Code)
	}
}

// The stylesheets: the folder's custom.css, then the dictionary's own, served
// in the light scheme the articles render in; an edit in place is a new URL.
func TestDictionaryCSS(t *testing.T) {
	s, row, tag := settled(t)
	custom := filepath.Join(filepath.Dir(row.Path), "custom.css")
	own := dict.Stem(row.Path) + ".css"
	writeLater(t, custom, []byte(".a{color:red}"), 2*time.Second)
	writeLater(t, own, []byte("@media (prefers-color-scheme: dark){.b{color:white}}"), 3*time.Second)

	_, rows, tag := dictsWith(t, s, tag)
	if len(rows) != 1 || len(rows[0].CSS) != 2 ||
		!strings.HasPrefix(rows[0].CSS[0], folderOf(custom)+"custom.css?v=") ||
		!strings.HasPrefix(rows[0].CSS[1], folderOf(own)+filepath.Base(own)+"?v=") {
		t.Fatalf("row css = %q", rows[0].CSS)
	}
	first := rows[0].CSS

	rec := get(s, first[1])
	if rec.Code != 200 || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/css") {
		t.Fatalf("GET %s: %d %q", first[1], rec.Code, rec.Header().Get("Content-Type"))
	}
	// versioned: cached a day; named by the stylesheet itself: revalidated
	if cc := rec.Header().Get("Cache-Control"); cc != "public, max-age=86400" {
		t.Errorf("versioned Cache-Control = %q", cc)
	}
	if cc := get(s, folderOf(own)+filepath.Base(own)).Header().Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("unversioned Cache-Control = %q", cc)
	}
	if strings.Contains(rec.Body.String(), "prefers-color-scheme: dark") {
		t.Errorf("the stylesheet was not given the light scheme: %s", rec.Body.String())
	}

	writeLater(t, own, []byte(".b{color:blue}"), 5*time.Second)
	_, rows, _ = dictsWith(t, s, tag)
	if len(rows) != 1 || len(rows[0].CSS) != 2 || rows[0].CSS[1] == first[1] || rows[0].CSS[0] != first[0] {
		t.Fatalf("an edited stylesheet: %q (was %q)", rows[0].CSS, first)
	}
}

// Dictionaries sharing a folder's font.ttf or custom.css share its URL: the
// page fetches and decodes each file once however many of them it shows.
func TestFolderFilesAreOneURL(t *testing.T) {
	isolatedDBDir(t)
	dir := t.TempDir()
	for _, n := range []string{"a.dsl", "b.dsl"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte(strings.Replace(sampleDSL, "Server Test Dict", n, 1)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for n, b := range map[string]string{"font.woff2": "wOF2OTTO....", "custom.css": ".x{}"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte(b), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	reg, err := NewRegistry([]string{dir}, false)
	if err != nil {
		t.Fatal(err)
	}
	closeBackends(t, reg)
	s := New(reg)
	_, rows, _ := dictsWith(t, s, "")
	if len(rows) != 2 || rows[0].Font == "" || rows[0].Font != rows[1].Font ||
		len(rows[0].CSS) != 1 || rows[0].CSS[0] != rows[1].CSS[0] {
		t.Fatalf("two dictionaries, one font.woff2 and one custom.css: %+v", rows)
	}
	if rec := get(s, rows[0].Font); rec.Code != 200 || rec.Header().Get("Content-Type") != "font/woff2" {
		t.Errorf("GET %s: %d %q", rows[0].Font, rec.Code, rec.Header().Get("Content-Type"))
	}
}

// /folder/ answers for the folder of a registered dictionary, by exact name,
// assets only - the same guard as /res/ - and needs no derivation first: a
// page holding a list from before a restart is answered too.
func TestFolderRoute(t *testing.T) {
	s, row, _ := settled(t)
	dir := filepath.Dir(row.Path)
	for n, b := range map[string]string{"bg.png": "PNG", "private.dat": "not an asset"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte(b), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	e, err := s.reg.get(row.ID)
	if err != nil {
		t.Fatal(err)
	}
	e.companions.Store(nil) // as a fresh process holds it
	for target, want := range map[string]int{
		folderOf(row.Path) + "bg.png":      200,
		folderOf(row.Path) + "private.dat": 404,
		// the mux answers an unclean path with a redirect to the clean one,
		// which is /folder/x.png: no key, nothing served
		folderOf(row.Path) + "../x.png":   307,
		folderOf(row.Path):                404,
		folderURL + "000000000000/bg.png": 404,
		folderURL + pathID(dir):           404,
	} {
		if rec := get(s, target); rec.Code != want {
			t.Errorf("GET %s: %d, want %d", target, rec.Code, want)
		}
	}
}

// Only a dictionary with a folder beside it has companions: not the app's own,
// and not one whose path is its prepared database.
func TestCompanionsNeedASourceFolder(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"text.ttf", "font.ttf", "x.ttf", "custom.css", "x.css"} {
		if err := os.WriteFile(filepath.Join(dir, n), testFont("F"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, e := range []*entry{
		{Path: filepath.Join(dir, "text.db")},
		{Path: filepath.Join(dir, "x.dsl"), builtin: true},
	} {
		if c := e.resolveCompanions(""); c.font != "" || len(c.css) != 0 {
			t.Errorf("%s (builtin %v) resolved %+v", e.Path, e.builtin, c)
		}
	}
	c := (&entry{Path: filepath.Join(dir, "x.dsl")}).resolveCompanions("")
	if filepath.Base(c.font) != "x.ttf" || len(c.css) != 2 {
		t.Errorf("a source resolved %+v", c)
	}
}
