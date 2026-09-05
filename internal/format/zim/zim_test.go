// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package zim

import (
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/wuweidict/wudict/internal/dict"
)

func openSample(t *testing.T) *Dict {
	t.Helper()
	d, err := Open(writeZIM(t, sample()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func TestMeta(t *testing.T) {
	d := openSample(t)
	m := d.Meta()
	if m.Name != "Test ZIM" {
		t.Errorf("Name = %q, want %q", m.Name, "Test ZIM")
	}
	if m.Format != "zim" {
		t.Errorf("Format = %q", m.Format)
	}
	if m.Description != "A test file" {
		t.Errorf("Description = %q", m.Description)
	}
	if m.IndexLang != "ro" {
		t.Errorf("IndexLang = %q, want ro", m.IndexLang)
	}
	// Every C/ entry, resources included - the same range Keywords browses,
	// so a count and a browse can never disagree.
	if m.EntryCount != 6 {
		t.Errorf("EntryCount = %d, want 6", m.EntryCount)
	}
	if got := d.c.articleCount(); got != 3 {
		t.Errorf("M/Counter articles = %d, want 3", got)
	}
	if c := d.c.contentNS(); c != 'C' {
		t.Errorf("contentNS = %q, want C", c)
	}
	if !d.SelfIndexed() {
		t.Error("SelfIndexed = false")
	}
	if w := d.PreviewBytes(); w <= clusterCacheBytes {
		t.Errorf("PreviewBytes = %d, want > cache size", w)
	}
}

func TestExact(t *testing.T) {
	d := openSample(t)
	cases := []struct {
		name     string
		word     string
		headword string
		wantBody string // substring, "" = expect no hit
	}{
		{"plain", "cat", "cat", "odrasl"},
		// The path spells a space as '_' and MediaWiki upper-cases the first
		// letter; neither costs a resident index, only another binary search.
		{"underscored", "New York", "New_York", "big"},
		{"lowercased", "new york", "New_York", "big"},
		{"redirect", "kitty", "kitty", "odrasl"},
		{"whitespace", "  cat  ", "cat", "odrasl"},
		{"missing", "nothing here", "", ""},
		{"empty", "", "", ""},
		// A stored blob that is not an article is never a search hit.
		{"resource", "f.png", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := d.Exact(tc.word, 10)
			if err != nil {
				t.Fatal(err)
			}
			if tc.headword == "" {
				if len(res) != 0 {
					t.Fatalf("got %d results, want none", len(res))
				}
				return
			}
			if len(res) != 1 {
				t.Fatalf("got %d results, want 1", len(res))
			}
			if res[0].Headword != tc.headword {
				t.Errorf("Headword = %q, want %q", res[0].Headword, tc.headword)
			}
			if tc.wantBody != "" && !strings.Contains(res[0].Body, tc.wantBody) {
				t.Errorf("Body = %q, want to contain %q", res[0].Body, tc.wantBody)
			}
		})
	}
}

func TestExactBodyIsRewritten(t *testing.T) {
	d := openSample(t)
	res, err := d.Exact("cat", 1)
	if err != nil || len(res) != 1 {
		t.Fatalf("Exact: %v, %d results", err, len(res))
	}
	body := res[0].Body
	if strings.Contains(body, "<html") || strings.Contains(body, "DOCTYPE") {
		t.Errorf("document shell survived: %q", body)
	}
	// The head's stylesheet is hoisted; its icon link is not.
	if !strings.Contains(body, `href="./_res_/a.css"`) {
		t.Errorf("stylesheet not hoisted: %q", body)
	}
	if strings.Contains(body, "f.png\"") && strings.Contains(body, `rel="icon"`) {
		t.Errorf("icon link hoisted: %q", body)
	}
	// The cross-reference becomes a lookup, percent-decoded.
	if !strings.Contains(body, `href="bword://odraslă"`) {
		t.Errorf("anchor not rewritten: %q", body)
	}
	// The image reference stays a resource reference for htmlref.
	if !strings.Contains(body, `src="f.png"`) {
		t.Errorf("img src touched: %q", body)
	}
}

func TestPrefix(t *testing.T) {
	d := openSample(t)
	// An exact hit wins outright, as it does for every other backend.
	res, err := d.Prefix("cat", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].Headword != "cat" {
		t.Fatalf("Prefix(cat) = %+v", res)
	}
	// No exact hit: scan forward in byte order. "_res_/a.css" sorts inside
	// this range and must not be returned.
	res, err = d.Prefix("ca", 10)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range res {
		got = append(got, r.Headword)
	}
	if len(got) != 2 || got[0] != "cat" || got[1] != "catalog" {
		t.Fatalf("Prefix(ca) = %v, want [cat catalog]", got)
	}
	if res, err = d.Prefix("ca", 1); err != nil || len(res) != 1 {
		t.Fatalf("Prefix limit ignored: %v, %d results", err, len(res))
	}
	if res, err = d.Prefix("zzz", 10); err != nil || len(res) != 0 {
		t.Fatalf("Prefix(zzz) = %v, %v", res, err)
	}
}

func TestKeywords(t *testing.T) {
	d := openSample(t)
	all := d.Keywords(0, 0)
	// Path byte order: 'N' < '_' < 'c' < 'f' < 'k'. Only articles and
	// redirects are headwords; the css and the png are not.
	want := []string{"New_York", "cat", "catalog", "kitty"}
	if strings.Join(all, "|") != strings.Join(want, "|") {
		t.Fatalf("Keywords = %v, want %v", all, want)
	}
	if got := d.Keywords(1, 2); len(got) > 2 {
		t.Errorf("Keywords(1,2) = %v", got)
	}
	if got := d.Keywords(-5, 3); len(got) == 0 {
		t.Error("negative offset returned nothing")
	}
	if got := d.Keywords(9999, 10); got != nil {
		t.Errorf("Keywords past the end = %v, want nil", got)
	}
}

func TestResources(t *testing.T) {
	d := openSample(t)
	// Both live in the extended (8-byte offset) cluster, so reading them
	// also exercises that width.
	rc, mime, err := d.Resource("f.png")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(rc)
	rc.Close()
	if string(b) != "\x89PNG\r\n\x1a\n" || mime != "image/png" {
		t.Errorf("Resource(f.png) = %q, %q", b, mime)
	}
	// The relative form an article writes must resolve to the same blob.
	rc, mime, err = d.Resource("./_res_/a.css")
	if err != nil {
		t.Fatal(err)
	}
	b, _ = io.ReadAll(rc)
	rc.Close()
	if string(b) != "b{color:red}" || mime != "text/css" {
		t.Errorf("Resource(a.css) = %q, %q", b, mime)
	}
	if _, _, err := d.Resource("absent.png"); !errors.Is(err, dict.ErrNotFound) {
		t.Errorf("missing resource err = %v, want ErrNotFound", err)
	}
	if _, _, err := d.Resource(""); !errors.Is(err, dict.ErrNotFound) {
		t.Errorf("empty resource err = %v, want ErrNotFound", err)
	}
	list := d.Resources()
	if strings.Join(list, "|") != "_res_/a.css|f.png" {
		t.Errorf("Resources = %v", list)
	}
}

func TestReader(t *testing.T) {
	r, err := NewReader(writeZIM(t, sample()))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if r.Meta().Name != "Test ZIM" {
		t.Errorf("Meta not available before Next: %+v", r.Meta())
	}
	var heads []string
	links := map[string]string{}
	for {
		e, err := r.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if len(e.Headwords) != 1 {
			t.Fatalf("headwords = %v", e.Headwords)
		}
		heads = append(heads, e.Headwords[0])
		if e.LinkTo != "" {
			links[e.Headwords[0]] = e.LinkTo
			continue
		}
		if e.Kind != dict.BodyHTML || e.Body == "" {
			t.Errorf("%q: Kind=%v body=%q", e.Headwords[0], e.Kind, e.Body)
		}
	}
	// Articles in cluster order (cat is in cluster 0), redirects last.
	want := []string{"cat", "New_York", "catalog", "kitty"}
	if strings.Join(heads, "|") != strings.Join(want, "|") {
		t.Fatalf("Next order = %v, want %v", heads, want)
	}
	if links["kitty"] != "cat" {
		t.Errorf("redirect target = %q, want cat", links["kitty"])
	}
	if _, err := r.Next(); !errors.Is(err, io.EOF) {
		t.Errorf("Next after EOF = %v", err)
	}
}

// Every codec the format defines and this builder can write. The corpus only
// contains 1 and 5, but a file from an older writer must still open.
func TestCodecs(t *testing.T) {
	for _, codec := range []byte{0, 1, 2, 4, 5} {
		f := sample()
		for i := range f.clusters {
			f.clusters[i].codec = codec
		}
		d, err := Open(writeZIM(t, f))
		if err != nil {
			t.Fatalf("codec %d: %v", codec, err)
		}
		res, err := d.Exact("catalog", 1)
		if err != nil || len(res) != 1 || !strings.Contains(res[0].Body, "list") {
			t.Errorf("codec %d: %v, %+v", codec, err, res)
		}
		d.Close()
	}
}

// A pre-6.1 file: articles live in A/, resources in I/, and cross-references
// carry the namespace segment.
func TestOldNamespaces(t *testing.T) {
	f := bFile{
		major: 6, minor: 0,
		clusters: []bCluster{{codec: 1}},
		entries: []bEntry{
			{ns: 'A', path: "dog", title: "dog", mime: "text/html",
				body: []byte(`<html><body><a href="../A/wolf">w</a><a href="../I/p.png">p</a></body></html>`)},
			{ns: 'A', path: "wolf", mime: "text/html", body: []byte("<html><body>howl</body></html>")},
			{ns: 'I', path: "p.png", mime: "image/png", body: []byte("PNG")},
			{ns: 'M', path: "Title", mime: "text/plain", body: []byte("Old")},
		},
	}
	d, err := Open(writeZIM(t, f))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if d.c.contentNS() != 'A' || d.c.newNamespaces() {
		t.Fatalf("contentNS = %q, newNamespaces = %v", d.c.contentNS(), d.c.newNamespaces())
	}
	res, err := d.Exact("dog", 1)
	if err != nil || len(res) != 1 {
		t.Fatalf("Exact(dog) = %v, %+v", err, res)
	}
	if !strings.Contains(res[0].Body, `href="bword://wolf"`) {
		t.Errorf("A/ reference not rewritten: %q", res[0].Body)
	}
	if !strings.Contains(res[0].Body, `href="../I/p.png"`) {
		t.Errorf("I/ reference rewritten as a lookup: %q", res[0].Body)
	}
	// The image namespace is only reachable on old-scheme files.
	if _, _, err := d.Resource("p.png"); err != nil {
		t.Errorf("Resource(p.png): %v", err)
	}
	// And this is the spelling the article above actually carries, so it is
	// the one the server asks for: the namespace segment survives the
	// relative trim, while the dirent stores the path without it.
	for _, name := range []string{"I/p.png", "../I/p.png", "./I/p.png"} {
		if _, _, err := d.Resource(name); err != nil {
			t.Errorf("Resource(%q): %v", name, err)
		}
	}
	// A prefix that names no namespace we search is part of the path, not a
	// namespace, and must not be eaten.
	if _, _, err := d.Resource("Z/p.png"); err == nil {
		t.Errorf("Resource(Z/p.png) resolved")
	}
	if list := d.Resources(); strings.Join(list, "|") != "p.png" {
		t.Errorf("Resources = %v", list)
	}
}

// Hostile input: every prefix of a valid file must be rejected without a
// panic, a hang, or a wild read.
func TestTruncatedRejected(t *testing.T) {
	full := buildZIM(t, sample())
	for _, n := range []int{0, 4, 40, 79, 80, 200, len(full) / 2, len(full) - 40} {
		p := writeTruncated(t, full[:n])
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("%d bytes: PANIC %v", n, r)
				}
			}()
			d, err := Open(p)
			if err == nil {
				d.Close()
				t.Errorf("%d bytes: opened without error", n)
			}
		}()
	}
}

func TestGarbageRejected(t *testing.T) {
	for _, b := range [][]byte{
		[]byte("not a zim file at all"),
		make([]byte, headerSize), // right size, wrong magic
	} {
		if d, err := Open(writeTruncated(t, b)); err == nil {
			d.Close()
			t.Errorf("%q opened without error", b[:min(len(b), 8)])
		}
	}
}

func writeTruncated(t *testing.T, b []byte) string {
	t.Helper()
	p := t.TempDir() + "/x.zim"
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// Integration against a real ZIM; skips unless WUDICT_TEST_ZIM is set.
func TestRealFile(t *testing.T) {
	p := os.Getenv("WUDICT_TEST_ZIM")
	if p == "" {
		t.Skip("WUDICT_TEST_ZIM not set")
	}
	if _, err := os.Stat(p); err != nil {
		t.Skipf("WUDICT_TEST_ZIM unreadable: %v", err)
	}
	d, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	m := d.Meta()
	t.Logf("%s: %d entries, lang %q, %d articles claimed, %d MiB preview",
		m.Name, m.EntryCount, m.IndexLang, d.c.articleCount(), d.PreviewBytes()>>20)
	if m.Name == "" || m.EntryCount == 0 {
		t.Fatalf("meta looks empty: %+v", m)
	}
	kws := d.Keywords(m.EntryCount/2, 20)
	if len(kws) == 0 {
		t.Fatal("Keywords returned nothing mid-file")
	}
	// Every headword the file offers must be findable by the same lookup a
	// user's query takes - the one property the whole binary-search design
	// rests on.
	for _, k := range kws {
		res, err := d.Exact(k, 1)
		if err != nil {
			t.Fatalf("Exact(%q): %v", k, err)
		}
		if len(res) == 0 {
			t.Errorf("Exact(%q) found nothing", k)
		}
	}
	if res, err := d.Prefix(kws[0][:1], 5); err != nil || len(res) == 0 {
		t.Errorf("Prefix(%q) = %d results, %v", kws[0][:1], len(res), err)
	}
}
