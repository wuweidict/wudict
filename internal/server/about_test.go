// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wuweidict/wudict/internal/dict"
)

const aboutBase = "http://127.0.0.1:6888"

func TestRenderAboutPlainText(t *testing.T) {
	got := renderAbout(dict.About{Sections: []dict.Section{
		{Lang: "English", Text: "A dictionary.\nBy <Someone> & Co."},
		{Lang: "Russian", Text: "Словарь."},
		{Text: "  "}, // dropped: nothing to show
	}}, aboutBase)
	want := "<h4>English</h4><p>A dictionary.<br>By &lt;Someone&gt; &amp; Co.</p>" +
		"<h4>Russian</h4><p>Словарь.</p>"
	if got != want {
		t.Errorf("renderAbout =\n%s\nwant\n%s", got, want)
	}
}

func TestRenderAboutMarkup(t *testing.T) {
	got := renderAbout(dict.About{HTML: true, Sections: []dict.Section{{Text: `<p>Big <b>book</b></p>`}}}, aboutBase)
	if got != `<p>Big <b>book</b></p>` {
		t.Errorf("renderAbout = %q", got)
	}
	// The flag is authoritative the other way too: a provider that says plain
	// is believed, so an angle-bracketed word in prose is shown, not eaten as
	// an unknown element.
	if p := renderAbout(dict.About{Sections: []dict.Section{{Text: "see <Someone>"}}}, aboutBase); p != "<p>see &lt;Someone&gt;</p>" {
		t.Errorf("plain text = %q", p)
	}
	for _, c := range []struct {
		name, in string
		gone     []string
		kept     string
	}{
		{"script", `hi<script>alert(1)</script>`, []string{"script", "alert"}, "hi"},
		{"event handler", `<p onclick="steal()">text</p>`, []string{"onclick", "steal"}, "text"},
		{"javascript href", `<a href="javascript:x()">go</a>`, []string{"javascript", "href"}, "go"},
		{"class attribute", `<p class="c1">text</p>`, []string{"class", "c1"}, "text"},
		{"image", `<p>a<img src="x.png">b</p>`, []string{"img", "x.png"}, "a"},
		{"iframe", `<iframe src="http://e.test/"></iframe>ok`, []string{"iframe", "e.test"}, "ok"},
		{"unknown element unwrapped", `<blink>still here</blink>`, []string{"blink"}, "still here"},
	} {
		out := renderAbout(dict.About{HTML: true, Sections: []dict.Section{{Text: c.in}}}, aboutBase)
		for _, bad := range c.gone {
			if strings.Contains(out, bad) {
				t.Errorf("%s: %q survived in %q", c.name, bad, out)
			}
		}
		if !strings.Contains(out, c.kept) {
			t.Errorf("%s: lost the text %q from %q", c.name, c.kept, out)
		}
	}
	// A root-absolute link is absolutised the same way an article's is, and an
	// external one passes through.
	out := renderAbout(dict.About{HTML: true, Sections: []dict.Section{
		{Text: `<a href="/setup">here</a> <a href="https://ok.test/">there</a>`},
	}}, aboutBase)
	if !strings.Contains(out, `href="`+aboutBase+`/setup"`) || !strings.Contains(out, `href="https://ok.test/"`) {
		t.Errorf("links = %q", out)
	}
}

func TestLooksLikeMarkup(t *testing.T) {
	for _, c := range []struct {
		in   string
		want bool
	}{
		{"plain prose", false},
		{"a < b and c > d", false},
		{"trailing <", false},
		{"<p>x</p>", true},
		{"text with a <br> in it", true},
		{"</div>", true},
		{"<!-- comment -->", true},
		{"5 < 6 <b>really</b>", true},
	} {
		if got := looksLikeMarkup(c.in); got != c.want {
			t.Errorf("looksLikeMarkup(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// aboutServer builds a server over one DSL dictionary, optionally with a .ann
// sidecar beside it.
func aboutServer(t *testing.T, ann string) (*Server, string) {
	t.Helper()
	s := newTestServer(t)
	e := s.reg.all()[0]
	if ann != "" {
		src := e.Path
		p := src[:len(src)-len(".dsl")] + ".ann"
		if err := os.WriteFile(p, []byte(ann), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return s, e.ID
}

func TestAboutSidecarWins(t *testing.T) {
	s, id := aboutServer(t, "#LANGUAGE \"English\"\nPublished by nobody.\n#LANGUAGE \"Spanish\"\nPublicado por nadie.\n")
	var got aboutInfo
	if rec := getJSON(t, s, "/api/about?dict="+id, &got); rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	if !strings.Contains(got.HTML, "<h4>English</h4><p>Published by nobody.</p>") ||
		!strings.Contains(got.HTML, "<h4>Spanish</h4><p>Publicado por nadie.</p>") {
		t.Errorf("html = %q, want both sections", got.HTML)
	}
	if filepath.Base(got.Source) != "test.ann" {
		t.Errorf("source = %q", got.Source)
	}
	// The synthesised "language → language" description is the FALLBACK, so it
	// must not appear once a real annotation exists.
	if strings.Contains(got.HTML, "→") {
		t.Errorf("the header synthesis leaked past the sidecar: %q", got.HTML)
	}
}

func TestAboutFallsBackToHeader(t *testing.T) {
	// sampleDSL declares no languages, so it has no description at all: the
	// endpoint answers 200 with an empty html, and the panel shows nothing.
	s, id := aboutServer(t, "")
	// The list is what draws the panel the disclosure lives in, so it is what
	// opens the dictionary - and this endpoint reads what that left behind
	// rather than opening anything itself (see below).
	getJSON(t, s, "/api/dicts", nil)
	var got aboutInfo
	if rec := getJSON(t, s, "/api/about?dict="+id, &got); rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	if got.HTML != "" {
		t.Errorf("html = %q, want empty", got.HTML)
	}
	if got.ID != id || got.Format != "dsl" {
		t.Errorf("info = %+v", got)
	}

	// One that DOES declare them: the synthesis is what a reader gets, and the
	// declared pair travels beside it.
	s2 := newTestServer(t)
	e := s2.reg.all()[0]
	if err := os.WriteFile(e.Path, []byte("#NAME \"D\"\n#INDEX_LANGUAGE \"English\"\n#CONTENTS_LANGUAGE \"Russian\"\n\ndog\n\tcanine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	getJSON(t, s2, "/api/dicts", nil)
	var got2 aboutInfo
	getJSON(t, s2, "/api/about?dict="+e.ID, &got2)
	if !strings.Contains(got2.HTML, "English") || !strings.Contains(got2.HTML, "Russian") {
		t.Errorf("html = %q, want the declared pair", got2.HTML)
	}
	if got2.IndexLang != "en" || got2.ContentsLang != "ru" {
		t.Errorf("langs = %q/%q, want en/ru", got2.IndexLang, got2.ContentsLang)
	}
}

// The About endpoint must never be the thing that opens a dictionary. DSL
// INGESTS inside Open, so an /api/about that opened turned a disclosure
// triangle into a full library build inside a GET - minutes of CPU and
// hundreds of megabytes on a phone, with nothing on screen to explain it.
func TestAboutNeverOpens(t *testing.T) {
	s, id := aboutServer(t, "#LANGUAGE \"English\"\nPublished by nobody.\n")
	e := s.reg.all()[0]
	var got aboutInfo
	if rec := getJSON(t, s, "/api/about?dict="+id, &got); rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	e.dMu.RLock()
	opened := e.d != nil
	e.dMu.RUnlock()
	if opened {
		t.Error("/api/about opened the dictionary")
	}
	if _, prepared := preparedTextDB(e.Path); prepared {
		t.Error("/api/about ingested the dictionary")
	}
	// and it still answers: the sidecar is resolved from the source PATH, which
	// is all a provider has ever needed.
	if !strings.Contains(got.HTML, "Published by nobody.") {
		t.Errorf("html = %q, want the sidecar text", got.HTML)
	}
}

func TestAboutUnknownDict(t *testing.T) {
	s := newTestServer(t)
	if rec := getJSON(t, s, "/api/about?dict=nosuch", nil); rec.Code != 404 {
		t.Errorf("status %d, want 404", rec.Code)
	}
}
