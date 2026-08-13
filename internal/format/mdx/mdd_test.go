// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package mdx

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wuweidict/wudict/internal/dict"
)

// testMDD resolves a real .mdd from the .mdx fixture's companions.
func testMDD(t *testing.T) string {
	t.Helper()
	mdx := os.Getenv("WUDICT_TEST_MDX")
	if mdx == "" {
		t.Skip("set WUDICT_TEST_MDX to run this")
	}
	for _, p := range companionMdds(mdx) {
		return p
	}
	t.Skipf("%s has no companion .mdd", filepath.Base(mdx))
	return ""
}

// An .mdd opens on its own, with no .mdx anywhere in sight. That is the whole
// point: a user may hold only the resource file, and an .mdx may have several
// companions whose union is not what naming one file asked for.
func TestMDDOpensAlone(t *testing.T) {
	p := testMDD(t)
	d, err := dict.Open(p) // through the registry, as the CLI does
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	m := d.Meta()
	if m.Format != "mdd" {
		t.Errorf("format = %q, want mdd", m.Format)
	}
	if m.EntryCount == 0 {
		t.Fatal("no entries")
	}
	if c := d.Caps(); c.Exact || c.Prefix || c.Contains || c.FTS {
		t.Errorf("an .mdd has no headwords, so nothing is searchable: %+v", c)
	}
	if r, _ := d.Exact("anything", 5); len(r) != 0 {
		t.Errorf("Exact returned %d results from a resource container", len(r))
	}
}

// Whatever `keys` prints, `res` must accept — unchanged. That contract is what
// makes the two commands usable together, and it is why names keep their
// original case (lookup is case-insensitive) and forward slashes.
func TestMDDKeysRoundTripThroughResource(t *testing.T) {
	p := testMDD(t)
	d, err := dict.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	names := d.Keywords(0, 5)
	if len(names) == 0 {
		t.Fatal("Keywords returned nothing")
	}
	for _, n := range names {
		if strings.ContainsAny(n, "\\") || strings.HasPrefix(n, "/") {
			t.Errorf("name %q is not in the normalised forward-slash form", n)
		}
		rc, mimeType, err := d.Resource(n)
		if err != nil {
			t.Fatalf("keys printed %q but res cannot fetch it: %v", n, err)
		}
		b, _ := io.ReadAll(rc)
		rc.Close()
		if len(b) == 0 {
			t.Errorf("%s: empty payload", n)
		}
		if mimeType == "" {
			t.Logf("%s: no MIME (unknown extension)", n)
		}
		// case-insensitive, as MDD keys are stored case-preserving but
		// referenced loosely by articles
		if _, _, err := d.Resource(strings.ToUpper(n)); err != nil {
			t.Errorf("%s: upper-case lookup failed: %v", n, err)
		}
	}
}

// The same (offset, n) contract as every other backend (D42).
func TestMDDKeywordsWindow(t *testing.T) {
	p := testMDD(t)
	d, err := dict.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	all := d.Keywords(0, 0)
	if len(all) < 3 {
		t.Skip("too few entries to window")
	}
	if got := d.Keywords(0, -1); len(got) != len(all) {
		t.Errorf("n=-1 gave %d, want all %d", len(got), len(all))
	}
	if got := d.Keywords(0, 2); len(got) != 2 || got[0] != all[0] || got[1] != all[1] {
		t.Errorf("n=2 window wrong: %v", got)
	}
	if got := d.Keywords(1, 2); len(got) != 2 || got[0] != all[1] {
		t.Errorf("offset=1 window wrong: %v", got)
	}
	if got := d.Keywords(len(all)+5, 0); got != nil {
		t.Errorf("offset past the end must be nil, got %d names", len(got))
	}
	// sorted, so the listing is stable and diffable
	for i := 1; i < len(all); i++ {
		if all[i-1] > all[i] {
			t.Fatalf("names not sorted at %d: %q > %q", i, all[i-1], all[i])
		}
	}
}

// Openable by name, invisible to a folder scan: a companion .mdd must never
// become a dictionary in its own right.
func TestMDDIsNotDiscovered(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"x.mdd", "x.1.mdd", "x.2.mdd"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("not really an mdd"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	found, err := dict.Discover(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 0 {
		t.Errorf("discovery picked up %v — .mdd files are a dictionary's companions, not dictionaries", found)
	}
}
