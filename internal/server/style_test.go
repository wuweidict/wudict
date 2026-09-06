// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newStyleServer is newTestServer with somewhere to keep the user's own
// stylesheets, which is what the CLI wires up from the config file in effect.
func newStyleServer(t *testing.T) (*Server, string) {
	t.Helper()
	s := newTestServer(t)
	s.StyleDir = filepath.Join(t.TempDir(), StyleDirName)
	return s, s.StyleDir
}

// The <link> the server injects, as a needle. Not the bare path: the page's
// own script names that path too (it disables this element while the editor is
// open), so a substring test on it would match the page that links nothing.
const userCSSLink = `<link rel="stylesheet" href="/style/` + appCSSName

type styleResp struct {
	App      string `json:"app"`
	Article  string `json:"article"`
	Dir      string `json:"dir"`
	Writable bool   `json:"writable"`
}

func putStyle(t *testing.T, s *Server, body string) (styleResp, int) {
	t.Helper()
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, newRequest("PUT", "/api/style", strings.NewReader(body)))
	var out styleResp
	if rec.Code == 200 {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("PUT /api/style: bad JSON (%v): %s", err, rec.Body.String())
		}
	}
	return out, rec.Code
}

// The trap this feature inherits from /api/prefs: the App tab and the Article
// tab are one editor sending different bodies, so an omitted field is "not
// mine to touch" and an empty one is "delete it". Conflating them loses the
// other box's work on every save.
func TestStyleRoundTrip(t *testing.T) {
	s, dir := newStyleServer(t)

	var first styleResp
	getJSON(t, s, "/api/style", &first)
	if first.App != "" || first.Article != "" {
		t.Fatalf("a fresh install already has styles: %+v", first)
	}
	if !first.Writable || first.Dir != dir {
		t.Fatalf("dir=%q writable=%v, want %q true", first.Dir, first.Writable, dir)
	}

	if got, code := putStyle(t, s, `{"app":"html{--bg:#f4ecd8}"}`); code != 200 ||
		got.App != "html{--bg:#f4ecd8}" {
		t.Fatalf("PUT app: %d %+v", code, got)
	}
	// article omitted above, so it must still be empty; now set it alone
	got, code := putStyle(t, s, `{"article":":host{line-height:1.75}"}`)
	if code != 200 {
		t.Fatalf("PUT article: %d", code)
	}
	if got.App != "html{--bg:#f4ecd8}" {
		t.Errorf("saving the article box erased the app box: %q", got.App)
	}
	if got.Article != ":host{line-height:1.75}" {
		t.Errorf("article=%q", got.Article)
	}

	// on disk, under the names the docs and the CLI help promise
	for name, want := range map[string]string{
		appCSSName:     "html{--bg:#f4ecd8}",
		articleCSSName: ":host{line-height:1.75}",
	} {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("%s not written: %v", name, err)
		}
		if string(b) != want {
			t.Errorf("%s = %q, want %q", name, b, want)
		}
	}

	// empty REMOVES: "I cleared the box" and "a file containing nothing" must
	// not be two states that read differently and behave the same.
	if got, code := putStyle(t, s, `{"app":""}`); code != 200 || got.App != "" {
		t.Fatalf("PUT empty app: %d %+v", code, got)
	}
	if _, err := os.Stat(filepath.Join(dir, appCSSName)); !os.IsNotExist(err) {
		t.Errorf("clearing the box left the file behind (%v)", err)
	}
	if got.Article == "" {
		t.Error("clearing the app box also cleared the article box")
	}
}

// The two files are served the way a res/ override is: uncached, so an edit
// made in an external editor lands on the next reload; and present-but-empty
// rather than 404, because a stylesheet that says nothing is not a missing
// resource and a 404 would send someone hunting for a bug.
func TestStyleFilesAreServedUncached(t *testing.T) {
	s, dir := newStyleServer(t)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, articleCSSName), []byte("p{margin:0}"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct{ path, body string }{
		{"/style/article.css", "p{margin:0}"},
		{"/style/app.css", ""}, // absent
		{"/style/article.css?style=off", ""},
	} {
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, newRequest("GET", tc.path, nil))
		if rec.Code != 200 {
			t.Errorf("GET %s = %d, want 200", tc.path, rec.Code)
			continue
		}
		if got := rec.Body.String(); got != tc.body {
			t.Errorf("GET %s = %q, want %q", tc.path, got, tc.body)
		}
		if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
			t.Errorf("GET %s Cache-Control = %q, want no-cache", tc.path, got)
		}
		if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/css") {
			t.Errorf("GET %s Content-Type = %q", tc.path, got)
		}
	}

	// Only the two names exist. Nothing else under /style/ is a file the user
	// put there, so nothing else may be read out of that folder.
	for _, p := range []string{"/style/", "/style/secret.txt", "/style/sub/app.css"} {
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, newRequest("GET", p, nil))
		if rec.Code != 404 {
			t.Errorf("GET %s = %d, want 404", p, rec.Code)
		}
	}
}

// No config file and no home directory: the same case that already makes
// settings in-memory. The feature must report that it cannot save rather than
// writing a folder somewhere nobody asked for - or panicking.
func TestStyleWithoutAConfigFolder(t *testing.T) {
	s := newTestServer(t) // StyleDir left empty
	var got styleResp
	getJSON(t, s, "/api/style", &got)
	if got.Writable || got.Dir != "" {
		t.Fatalf("%+v, want writable=false and no dir", got)
	}
	if _, code := putStyle(t, s, `{"app":"html{}"}`); code != 409 {
		t.Errorf("PUT with nowhere to save = %d, want 409", code)
	}
	// and the page is still served, unstyled
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, newRequest("GET", "/", nil))
	if rec.Code != 200 || strings.Contains(rec.Body.String(), userCSSLink) {
		t.Errorf("GET / = %d, and it links a stylesheet that cannot exist", rec.Code)
	}
}

func TestStyleTooLarge(t *testing.T) {
	s, _ := newStyleServer(t)
	big := strings.Repeat("a", maxUserCSSBytes+1)
	if _, code := putStyle(t, s, `{"app":"`+big+`"}`); code != 413 {
		t.Errorf("PUT of an oversized stylesheet = %d, want 413", code)
	}
}

// The page names app.css by a hash of its bytes (D45) and revalidates, so the
// ETag has to move when the stylesheet does - otherwise a cached page keeps
// asking for the previous URL and the edit never appears.
func TestIndexTracksTheUserStylesheet(t *testing.T) {
	s, dir := newStyleServer(t)
	get := func(q string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, newRequest("GET", "/"+q, nil))
		return rec
	}

	bare := get("")
	if strings.Contains(bare.Body.String(), userCSSLink) {
		t.Error("the page links app.css when there is no app.css")
	}
	if strings.Contains(bare.Body.String(), "{{USERCSS}}") {
		t.Error("the placeholder survived into the served page")
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(css string) string {
		if err := os.WriteFile(filepath.Join(dir, appCSSName), []byte(css), 0o644); err != nil {
			t.Fatal(err)
		}
		return userCSSLink + "?v=" + assetTag([]byte(css))
	}

	wantHref := write("html{--bg:#f4ecd8}")
	first := get("")
	if !strings.Contains(first.Body.String(), wantHref) {
		t.Fatalf("the page does not link %s", wantHref)
	}
	if first.Header().Get("ETag") == bare.Header().Get("ETag") {
		t.Error("the ETag did not move when a stylesheet appeared")
	}

	wantHref = write("html{--bg:#eee}")
	second := get("")
	if !strings.Contains(second.Body.String(), wantHref) {
		t.Fatalf("an edited stylesheet is still requested at its old URL, want %s", wantHref)
	}
	if second.Header().Get("ETag") == first.Header().Get("ETag") {
		t.Error("the ETag did not move when the stylesheet's content changed")
	}

	// The escape hatch: a stylesheet may hide the very editor that would undo
	// it, so this one URL must serve the page with none of it applied.
	off := get("?style=off")
	if strings.Contains(off.Body.String(), userCSSLink) {
		t.Error("?style=off still links the user stylesheet")
	}
}
