// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package mdx

import (
	"github.com/glowinthedark/gonow-dict/internal/dict"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFold(t *testing.T) {
	cases := map[string]string{
		"Corazón":  "corazon",
		"ÁÉÍÓÚÜÑ":  "aeiouun",
		"straße":   "straße", // ß is not a combining mark: preserved
		"already":  "already",
		"Ître-Œuf": "itre-œuf",
	}
	for in, want := range cases {
		if got := fold(in); got != want {
			t.Errorf("fold(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseStylesheet(t *testing.T) {
	s := "1\r\n<b>\r\n</b>\r\n2\r\n<i>\r\n</i>"
	m := parseStylesheet(s)
	if len(m) != 2 {
		t.Fatalf("want 2 styles, got %d: %v", len(m), m)
	}
	if m["1"] != [2]string{"<b>", "</b>"} || m["2"] != [2]string{"<i>", "</i>"} {
		t.Errorf("unexpected styles: %v", m)
	}
	if parseStylesheet("  \n ") != nil {
		t.Error("blank stylesheet should parse to nil")
	}
}

func TestSubstituteStylesheet(t *testing.T) {
	styles := map[string][2]string{"1": {"<b>", "</b>"}, "2": {"<i>", "</i>"}}
	got := substituteStylesheet("plain `1`bold`2`italic", styles)
	want := "plain <b>bold</b><i>italic</i>"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	// unknown style id: marker consumed, text dropped per original semantics
	got = substituteStylesheet("a`9`x", map[string][2]string{"1": {"<b>", "</b>"}})
	if got != "a" {
		t.Errorf("unknown style: got %q", got)
	}
}

// testMdx returns the integration-test dictionary path, skipping when the
// GONOW_TEST_MDX env var is unset or the file is missing.
func testMdx(t *testing.T) string {
	t.Helper()
	p := os.Getenv("GONOW_TEST_MDX")
	if p == "" {
		t.Skip("GONOW_TEST_MDX not set; skipping integration test")
	}
	if _, err := os.Stat(p); err != nil {
		t.Skipf("GONOW_TEST_MDX=%s not readable: %v", p, err)
	}
	return p
}

// BenchmarkExactWarm measures a repeated in-process lookup — with the record
// block cache this should be microseconds; without it, each op re-opened the
// file and re-decompressed the whole record block (~1 ms, per the audit).
func BenchmarkExactWarm(b *testing.B) {
	p := os.Getenv("GONOW_TEST_MDX")
	if p == "" {
		b.Skip("GONOW_TEST_MDX not set")
	}
	d, err := Open(p)
	if err != nil {
		b.Fatal(err)
	}
	defer d.Close()
	keys := d.Keywords(d.Meta().EntryCount/2, 1)
	if len(keys) == 0 {
		b.Skip("no keywords")
	}
	w := keys[0]
	if _, err := d.Exact(w, 1); err != nil { // warm the block cache
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := d.Exact(w, 1); err != nil {
			b.Fatal(err)
		}
	}
}

func TestIntegrationOpenLookup(t *testing.T) {
	d, err := Open(testMdx(t))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	m := d.Meta()
	if m.EntryCount == 0 || m.Name == "" || m.Format != "mdx" {
		t.Fatalf("bad meta: %+v", m)
	}

	// every dictionary must resolve some early headword exactly
	keys := d.Keywords(m.EntryCount/2, 5)
	if len(keys) == 0 {
		t.Fatal("no keywords at mid offset")
	}
	res, err := d.Exact(keys[0], 10)
	if err != nil || len(res) == 0 {
		t.Fatalf("Exact(%q): res=%d err=%v", keys[0], len(res), err)
	}
	if strings.TrimSpace(res[0].Body) == "" {
		t.Errorf("Exact(%q): empty body", keys[0])
	}
	if strings.HasPrefix(res[0].Body, "@@@LINK=") {
		t.Errorf("Exact(%q): unresolved @@@LINK leaked to output", keys[0])
	}

	// prefix on a truncated headword must return something
	if len(keys[0]) > 3 {
		pres, err := d.Prefix(keys[0][:3], 10)
		if err != nil || len(pres) == 0 {
			t.Errorf("Prefix(%q): res=%d err=%v", keys[0][:3], len(pres), err)
		}
	}

	// missing word behaves, does not panic
	if r, _ := d.Exact("zzzz-no-such-word-zzzz", 5); len(r) != 0 {
		t.Errorf("expected no results, got %d", len(r))
	}
}

func TestIntegrationResource(t *testing.T) {
	d, err := Open(testMdx(t))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	idx := d.resourceIndex()
	if len(idx) == 0 {
		t.Skip("dictionary has no .mdd resources")
	}
	var name string
	for k := range idx {
		name = k
		break
	}
	rc, _, err := d.Resource(name)
	if err != nil {
		t.Fatalf("Resource(%q): %v", name, err)
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil || len(data) == 0 {
		t.Fatalf("Resource(%q): %d bytes, err=%v", name, len(data), err)
	}
	if _, _, err := d.Resource("no/such/resource.xyz"); err == nil {
		t.Error("missing resource should error")
	}
	if _, _, err := d.Resource("../../etc/passwd"); err == nil {
		t.Error("path traversal must be rejected")
	}
}

// MDict serves files sitting next to the .mdx — that is how repacks ship their
// stylesheet and scripts (LDOCE6 keeps LDOCE6.css and entry.js loose, with no
// .mdd at all). Packed resources still win; the disk is only consulted on a
// miss, and only for things a dictionary legitimately loads.
func TestLooseSiblingAssets(t *testing.T) {
	dir := t.TempDir()
	mdxPath := filepath.Join(dir, "d.mdx")
	d := &Dict{meta: dict.Meta{Path: mdxPath}}

	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("LDOCE6.css", "span{color:red}")
	write("entry.js", "function toggle(){}")
	write("secrets.env", "TOKEN=hunter2")
	if err := os.MkdirAll(filepath.Join(dir, "img"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(filepath.Join("img", "a.png"), "PNG")

	read := func(name string) (string, string, error) {
		rc, mt, err := d.Resource(name)
		if err != nil {
			return "", "", err
		}
		defer rc.Close()
		b, _ := io.ReadAll(rc)
		return string(b), mt, nil
	}

	// served, with the case of the name preserved (a case-sensitive filesystem
	// would not find "ldoce6.css")
	if body, mt, err := read("LDOCE6.css"); err != nil || body != "span{color:red}" {
		t.Fatalf("loose css: %q %q %v", body, mt, err)
	} else if !strings.Contains(mt, "css") {
		t.Errorf("mime = %q, want text/css", mt)
	}
	if body, _, err := read("entry.js"); err != nil || body != "function toggle(){}" {
		t.Fatalf("loose js: %q %v", body, err)
	}
	if body, _, err := read("img/a.png"); err != nil || body != "PNG" {
		t.Fatalf("loose file in a subfolder: %q %v", body, err)
	}

	// not an asset type a dictionary loads: never served, even though it exists
	if _, _, err := read("secrets.env"); err == nil {
		t.Error("a sibling file outside the asset allowlist must not be served")
	}
	// traversal stays blocked
	for _, bad := range []string{"../outside.css", "../../etc/passwd", "/etc/passwd"} {
		if _, _, err := read(bad); err == nil {
			t.Errorf("traversal not blocked: %q", bad)
		}
	}
	// a name that matches nothing is still a plain miss
	if _, _, err := read("nope.css"); err == nil {
		t.Error("a missing file must report not-found")
	}
}
