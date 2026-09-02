// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package mdx

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"

	gomdict "github.com/wuweidict/wudict/internal/gomdict"
	"github.com/wuweidict/wudict/internal/resource"
)

// linkSize collapses two spellings of "to the end of the block" into one, and
// must not collapse a genuinely empty record with them.
func TestLinkSize(t *testing.T) {
	for _, tc := range []struct {
		name       string
		start, end int64
		want       int64
	}{
		{"ordinary", 100, 140, 40},
		{"v1v2 last entry", 100, 0, -1},
		{"v3 last entry", 100, -1, -1},
		{"only entry, v1v2", 0, 0, -1},
		{"empty record", 100, 100, 0},
	} {
		got := linkSize(&gomdict.MDictKeywordEntry{
			RecordStartOffset: tc.start, RecordEndOffset: tc.end,
		})
		if got != tc.want {
			t.Errorf("%s: linkSize(%d,%d) = %d, want %d", tc.name, tc.start, tc.end, got, tc.want)
		}
	}
}

// The loose-file source is a security boundary: it shares a folder with every
// other dictionary, so it answers for assets and nothing else.
func TestMediaSourcesAllowlist(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"style.css", "secrets.env", "pic.png", "notes.md"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte(n), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	srcs := MediaSources(filepath.Join(dir, "x.mdx"))
	if len(srcs) != 1 {
		t.Fatalf("want one source, got %d", len(srcs))
	}
	for _, tc := range []struct {
		name string
		ok   bool
	}{
		{"style.css", true},
		{"pic.png", true},
		{"secrets.env", false}, // present, but not on the asset allowlist
		{"notes.md", false},
		{"../secrets.env", false}, // still not an asset after the climb is folded
		{"no-such.png", false},
	} {
		rc, err := srcs[0].Open(tc.name)
		if (err == nil) != tc.ok {
			t.Errorf("Open(%q): err=%v, want ok=%v", tc.name, err, tc.ok)
		}
		if rc != nil {
			rc.Close()
		}
	}
}

// The integration proof: every resource served through recorded locations must
// be byte-identical to the same resource served by the fully opened backend -
// including the last entry of each container, which is the one carrying the
// end-of-block sentinel.
func TestIntegrationLinkedResourcesMatch(t *testing.T) {
	d, err := Open(testMdx(t))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if len(d.mdds) == 0 {
		t.Skip("dictionary has no .mdd resources")
	}

	parts, links, err := d.MediaLinks()
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) != len(d.mdds) || len(links) == 0 {
		t.Fatalf("MediaLinks: %d parts (want %d), %d links", len(parts), len(d.mdds), len(links))
	}
	fet, err := OpenFetcher(parts)
	if err != nil {
		t.Fatal(err)
	}
	defer fet.Close()

	// A spread across the whole container, plus the final entry of each part -
	// sampled rather than exhaustive because a large .mdd holds hundreds of
	// thousands of files and this must stay a test, not a benchmark.
	pick := map[int]bool{0: true, len(links) - 1: true}
	for i := 0; i < len(links); i += max(1, len(links)/200) {
		pick[i] = true
	}
	last := map[int]int{}
	for i, l := range links {
		last[l.Part] = i
	}
	for _, i := range last {
		pick[i] = true
	}

	for i := range pick {
		l := links[i]
		rc, _, err := d.Resource(l.Name)
		if err != nil {
			t.Fatalf("direct Resource(%q): %v", l.Name, err)
		}
		want, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatalf("direct read %q: %v", l.Name, err)
		}
		got, err := fet.Fetch(l.Part, l.Off, l.Size)
		if err != nil {
			t.Fatalf("linked Fetch(%q): %v", l.Name, err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("%q: linked %d bytes != direct %d bytes", l.Name, len(got), len(want))
		}
		if l.Name != resource.Key(l.Name) {
			t.Fatalf("%q is not stored in key form", l.Name)
		}
	}
}
