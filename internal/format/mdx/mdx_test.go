// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package mdx

import (
	"io"
	"os"
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
