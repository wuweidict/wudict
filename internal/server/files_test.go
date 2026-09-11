// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type filesResp struct {
	Files []struct {
		Name  string `json:"name"`
		Size  int64  `json:"size"`
		MTime int64  `json:"mtime"`
		Type  string `json:"type"`
		URL   string `json:"url"`
	} `json:"files"`
	Dir      string `json:"dir"`
	Writable bool   `json:"writable"`
	Total    int64  `json:"total"`
	Limits   struct {
		File  int64 `json:"file"`
		Total int64 `json:"total"`
		Count int   `json:"count"`
	} `json:"limits"`
}

func postFile(t *testing.T, s *Server, query string, body []byte) (filesResp, int) {
	t.Helper()
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, newRequest("POST", "/api/files?"+query, bytes.NewReader(body)))
	var out filesResp
	if rec.Code == 200 {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("POST /api/files?%s: bad JSON (%v): %s", query, err, rec.Body.String())
		}
	}
	return out, rec.Code
}

func names(r filesResp) []string {
	out := make([]string, 0, len(r.Files))
	for _, f := range r.Files {
		out = append(out, f.Name)
	}
	return out
}

// The grammar is the whole traversal defence: nothing downstream cleans a
// path, because a name that cannot hold a separator is not a path. Table
// rather than prose, because every rejected shape here is one that reached a
// filesystem in somebody else's upload endpoint.
func TestUserFileNameGrammar(t *testing.T) {
	for _, c := range []struct {
		name string
		ok   bool
	}{
		{"w1.jpg", true},
		{"a", true},
		{"9.png", true},
		{"My-Wallpaper_2.JPEG", true},
		{"noextension", true},
		{"a.b.c.tar.gz", true},
		{strings.Repeat("x", 64), true},

		{"", false},
		{strings.Repeat("x", 65), false},
		{".hidden", false},        // a leading dot hides the file from its own manager
		{"..", false},             // and is the traversal token besides
		{"../etc/passwd", false},  // separators, both kinds
		{"..\\windows\\x", false}, //
		{"a/b", false},            //
		{"-lead.css", false},      // must start with a letter or digit
		{"_lead.css", false},      //
		{"a b.png", false},        // spaces: the URL would need escaping
		{"a\x00.png", false},      // NUL, which some C layer below would truncate at
		{"café.png", false},       // non-ASCII: one byte sequence, many spellings
		{"a:b.png", false},        // an NTFS alternate data stream
		{"a%2e%2e.png", false},    // percent-encoding is decoded before we see it
		{"a.png?x=1", false},      // query, which is not part of a name
		{"a\n.png", false},        // header injection shapes
		{"a\r.png", false},        //
	} {
		if got := isUserFileName(c.name); got != c.ok {
			t.Errorf("isUserFileName(%q) = %v, want %v", c.name, got, c.ok)
		}
	}
}

// Upload, list, fetch, replace, delete - the whole life of one file, through
// the HTTP surface rather than the helpers, because the caps, the name check
// and the routing are all part of what has to work.
func TestUserFileRoundTrip(t *testing.T) {
	s, dir := newStyleServer(t)

	var first filesResp
	getJSON(t, s, "/api/files", &first)
	if len(first.Files) != 0 {
		t.Fatalf("a fresh install already has files: %v", names(first))
	}
	if !first.Writable || first.Dir != filepath.Join(dir, assetsDirName) {
		t.Fatalf("dir/writable wrong: %+v", first)
	}
	if first.Limits.File != maxUserFileBytes || first.Limits.Total != maxUserFilesBytes ||
		first.Limits.Count != maxUserFiles {
		t.Fatalf("limits not reported: %+v", first.Limits)
	}

	png := []byte("\x89PNG\r\n\x1a\nfirst")
	got, code := postFile(t, s, "name=w1.png", png)
	if code != 200 {
		t.Fatalf("upload: status %d", code)
	}
	if len(got.Files) != 1 || got.Files[0].Name != "w1.png" {
		t.Fatalf("upload did not list the file: %v", names(got))
	}
	if got.Files[0].URL != "/files/w1.png" || got.Files[0].Type != "image/png" {
		t.Fatalf("row wrong: %+v", got.Files[0])
	}
	if got.Total != int64(len(png)) {
		t.Errorf("total = %d, want %d", got.Total, len(png))
	}
	// On disk, under the stylesheets, so the whole customisation surface is
	// one folder to back up or carry (D32).
	onDisk, err := os.ReadFile(filepath.Join(dir, assetsDirName, "w1.png"))
	if err != nil || !bytes.Equal(onDisk, png) {
		t.Fatalf("not stored beside the stylesheets: %v", err)
	}

	rec := getJSON(t, s, "/files/w1.png", nil)
	if rec.Code != 200 || !bytes.Equal(rec.Body.Bytes(), png) {
		t.Fatalf("GET /files/w1.png: status %d, %d bytes", rec.Code, rec.Body.Len())
	}

	// A second upload onto the same name is refused: the user's stylesheet
	// already points at it, and an overwrite they did not ask for cannot be
	// undone.
	if _, code := postFile(t, s, "name=w1.png", []byte("second")); code != 409 {
		t.Fatalf("silent overwrite: status %d, want 409", code)
	}
	got, code = postFile(t, s, "name=w1.png&replace=1", []byte("second"))
	if code != 200 || len(got.Files) != 1 {
		t.Fatalf("replace: status %d, %v", code, names(got))
	}
	rec = getJSON(t, s, "/files/w1.png", nil)
	if rec.Body.String() != "second" {
		t.Errorf("replace did not take: %q", rec.Body.String())
	}

	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, newRequest("DELETE", "/api/files?name=w1.png", nil))
	if rec.Code != 200 {
		t.Fatalf("delete: status %d", rec.Code)
	}
	if getJSON(t, s, "/files/w1.png", nil).Code != 404 {
		t.Error("the file is still served after being deleted")
	}
	// Deleting what is gone is a success: the caller asked for it to be gone.
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, newRequest("DELETE", "/api/files?name=w1.png", nil))
	if rec.Code != 200 {
		t.Errorf("second delete: status %d, want 200", rec.Code)
	}
}

// What the store refuses, and with which status - the numbers the page turns
// into sentences.
func TestUserFileRefusals(t *testing.T) {
	s, _ := newStyleServer(t)

	for _, c := range []struct {
		what, query string
		body        []byte
		want        int
	}{
		{"no name", "", []byte("x"), 400},
		{"traversal", "name=" + "../x.png", []byte("x"), 400},
		{"leading dot", "name=.x", []byte("x"), 400},
		{"empty body", "name=ok.png", nil, 400},
		{"over the per-file cap", "name=big.bin", bytes.Repeat([]byte("x"), maxUserFileBytes+1), 413},
	} {
		if _, code := postFile(t, s, c.query, c.body); code != c.want {
			t.Errorf("%s: status %d, want %d", c.what, code, c.want)
		}
	}
	// Exactly at the cap is accepted: a limit nobody can reach is a different
	// limit from the one reported.
	if _, code := postFile(t, s, "name=edge.bin", bytes.Repeat([]byte("x"), maxUserFileBytes)); code != 200 {
		t.Errorf("a file exactly at the cap was refused: status %d", code)
	}
	if _, code := postFile(t, s, "name=edge.bin&replace=1", []byte("small")); code != 200 {
		t.Errorf("replacing a large file with a small one was refused: status %d", code)
	}
}

// The folder's own cap, which is not the per-file one and not the count: a
// handful of large files can fill it while both of those are still far away.
//
// The filler is written straight to disk, sparse, because the assertion is
// about the SIZES the handler adds up - pushing 64 MiB through the endpoint
// to prove it would only be a slower way of measuring the same arithmetic.
func TestUserFileTotalCap(t *testing.T) {
	s, dir := newStyleServer(t)
	assets := filepath.Join(dir, assetsDirName)
	if err := os.MkdirAll(assets, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i*maxUserFileBytes < maxUserFilesBytes; i++ {
		p := filepath.Join(assets, fmt.Sprintf("big%d.bin", i))
		f, err := os.Create(p)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.Truncate(maxUserFileBytes); err != nil {
			t.Fatal(err)
		}
		f.Close()
	}
	var full filesResp
	getJSON(t, s, "/api/files", &full)
	if full.Total != maxUserFilesBytes || len(full.Files) >= maxUserFiles {
		t.Fatalf("filler wrong: %d bytes in %d files", full.Total, len(full.Files))
	}
	if _, code := postFile(t, s, "name=one-more.txt", []byte("x")); code != 413 {
		t.Errorf("the folder cap did not hold: status %d, want 413", code)
	}
	// Room made is room usable: the cap is measured against the store as it
	// will be, not as it was.
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, newRequest("DELETE", "/api/files?name=big0.bin", nil))
	if rec.Code != 200 {
		t.Fatalf("delete: status %d", rec.Code)
	}
	if _, code := postFile(t, s, "name=one-more.txt", []byte("x")); code != 200 {
		t.Errorf("after freeing 8 MiB: status %d, want 200", code)
	}
}

// The count cap is separate from the byte cap, because 64 one-byte files must
// not be a way around "64 files".
func TestUserFileCountCap(t *testing.T) {
	s, _ := newStyleServer(t)
	for i := 0; i < maxUserFiles; i++ {
		if _, code := postFile(t, s, fmt.Sprintf("name=f%d.txt", i), []byte("x")); code != 200 {
			t.Fatalf("file %d refused: status %d", i, code)
		}
	}
	if _, code := postFile(t, s, "name=one-too-many.txt", []byte("x")); code != 413 {
		t.Errorf("the count cap did not hold: status %d, want 413", code)
	}
	// Replacing one of the 64 is not a 65th.
	if _, code := postFile(t, s, "name=f0.txt&replace=1", []byte("xx")); code != 200 {
		t.Errorf("replace at the count cap: status %d, want 200", code)
	}
}

// The headers /files/ serves under. The store holds arbitrary bytes named by
// the user, so the declared type has to be the only type - and it is declared
// from the extension, never sniffed from the content.
func TestUserFileServing(t *testing.T) {
	s, _ := newStyleServer(t)

	for _, c := range []struct{ name, want string }{
		{"a.png", "image/png"},
		{"a.woff2", "font/woff2"},
		{"a.css", "text/css"},
		{"a.js", "text/javascript"},
		{"a.pdf", "application/pdf"},
		// No extension, and one nobody registered: the store takes any type,
		// so "I do not know" has to be an answer and not a refusal.
		{"README", "application/octet-stream"},
		{"a.iso", "application/octet-stream"},
	} {
		if _, code := postFile(t, s, "name="+c.name, []byte("<html>not markup</html>")); code != 200 {
			t.Fatalf("%s: upload status %d", c.name, code)
		}
		rec := getJSON(t, s, "/files/"+c.name, nil)
		if rec.Code != 200 {
			t.Fatalf("%s: status %d", c.name, rec.Code)
		}
		if got := rec.Header().Get("Content-Type"); got != c.want {
			t.Errorf("%s: Content-Type %q, want %q", c.name, got, c.want)
		}
		if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("%s: nosniff missing (%q)", c.name, got)
		}
		// Uncached, like the stylesheets and the res/ overrides: these are
		// files the user is actively replacing.
		if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
			t.Errorf("%s: Cache-Control %q, want no-cache", c.name, got)
		}
	}

	// Absent, and every shape that is not a name this store can hold.
	for _, p := range []string{
		"/files/nothing.png",
		"/files/",
		"/files/sub/a.png",
		"/files/.hidden",
		"/files/%2e%2e/%2e%2e/etc/passwd",
		"/files/..%2f..%2fetc%2fpasswd",
	} {
		if code := getJSON(t, s, p, nil).Code; code != 404 {
			t.Errorf("GET %s: status %d, want 404", p, code)
		}
	}
}

// A file put there by hand that the manager could not address is skipped
// rather than listed: a row whose Delete button cannot work is worse than no
// row. Directories likewise - the store is flat.
func TestUserFileListSkipsWhatItCannotAddress(t *testing.T) {
	s, dir := newStyleServer(t)
	assets := filepath.Join(dir, assetsDirName)
	if err := os.MkdirAll(filepath.Join(assets, "subdir"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"ok.png", ".hidden", "has space.png", "café.png"} {
		if err := os.WriteFile(filepath.Join(assets, n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var got filesResp
	getJSON(t, s, "/api/files", &got)
	if len(got.Files) != 1 || got.Files[0].Name != "ok.png" {
		t.Fatalf("listed %v, want only [ok.png]", names(got))
	}
}

// No config directory at all: the same case that leaves state in memory. The
// editor says so rather than failing to save, so the list has to answer and
// the upload has to give it a status it can explain.
func TestUserFilesWithoutConfigDir(t *testing.T) {
	s := newTestServer(t) // StyleDir left empty

	var got filesResp
	rec := getJSON(t, s, "/api/files", &got)
	if rec.Code != 200 || got.Writable || len(got.Files) != 0 || got.Dir != "" {
		t.Fatalf("status %d, %+v", rec.Code, got)
	}
	if _, code := postFile(t, s, "name=w1.png", []byte("x")); code != 409 {
		t.Errorf("upload without a config directory: status %d, want 409", code)
	}
	if getJSON(t, s, "/files/w1.png", nil).Code != 404 {
		t.Error("something was served from a store that does not exist")
	}
}

// ?style=off exists to get back into an app a stylesheet has hidden. It omits
// the STYLESHEETS; it must not also withhold the files, or the way back in
// would be a page whose every image is missing.
func TestUserFileIgnoresStyleOff(t *testing.T) {
	s, _ := newStyleServer(t)
	if _, code := postFile(t, s, "name=w1.png", []byte("bytes")); code != 200 {
		t.Fatalf("upload status %d", code)
	}
	rec := getJSON(t, s, "/files/w1.png?style=off", nil)
	if rec.Code != 200 || rec.Body.String() != "bytes" {
		t.Errorf("?style=off withheld a file: status %d, %q", rec.Code, rec.Body.String())
	}
}
