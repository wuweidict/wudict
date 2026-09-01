// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"strings"
	"testing"

	"github.com/aaaton/golem/v4"
	"github.com/aaaton/golem/v4/dicts/en"
)

func TestGroup(t *testing.T) {
	tests := []struct {
		name  string
		in    string
		want  string
		lines int
	}{
		{"pairs collapse", "know\tknew\nknow\tknown\nknow\tknows\n",
			"know\tknew\tknown\tknows\n", 1},
		{"first-seen order kept", "b\tb1\na\ta1\nb\tb2\n",
			"b\tb1\tb2\na\ta1\n", 2},
		{"lower cased", "Know\tKNEW\n", "know\tknew\n", 1},
		{"crlf stripped", "know\tknew\r\nknow\tknown\r\n", "know\tknew\tknown\n", 1},
		{"bom stripped", "\xef\xbb\xbfknow\tknew\n", "know\tknew\n", 1},
		{"duplicate forms dropped", "know\tknew\nknow\tknew\n", "know\tknew\n", 1},
		{"self form dropped", "know\tknow\tknew\n", "know\tknew\n", 1},
		{"empty fields dropped", "know\t\t\tknew\n", "know\tknew\n", 1},
		{"short lines dropped", "know\n\n\t\nknow\tknew\n", "know\tknew\n", 1},
		{"self only line dropped", "know\tknow\n", "", 0},
		{"no trailing newline", "know\tknew", "know\tknew\n", 1},
		{"nothing usable", "\n\n", "", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, n := group([]byte(tt.in))
			if string(got) != tt.want || n != tt.lines {
				t.Fatalf("group(%q) = %q, %d; want %q, %d", tt.in, got, n, tt.want, tt.lines)
			}
			// The manifest publishes a hash of these bytes, so the same input
			// has to produce the same output every time.
			if again, _ := group([]byte(tt.in)); string(again) != string(got) {
				t.Fatalf("group is not deterministic: %q vs %q", again, got)
			}
			// And grouping already-grouped data has to be a no-op, which is
			// what makes a regenerated catalogue comparable to the last one.
			if idem, _ := group(got); string(idem) != string(got) {
				t.Fatalf("group(group(x)) = %q, want %q", idem, got)
			}
		})
	}
}

// TestGroupMatchesGolem is the check that switching the source from golem's Go
// modules to michmech's raw lists is not a behaviour change (D88).
//
// golem's published en pack IS michmech's English list, grouped by golem's own
// cmd/simplify. Expanding it back to one pair per line reconstructs the shape
// this tool now reads, and re-grouping it here must answer identically for
// every word in the language - tens of thousands of them, against the reference
// data itself rather than a fixture somebody typed.
func TestGroupMatchesGolem(t *testing.T) {
	ref, err := en.New().GetResource()
	if err != nil {
		t.Fatal(err)
	}
	want, err := golem.New(bytePack{"en", ref})
	if err != nil {
		t.Fatal(err)
	}

	var pairs strings.Builder
	var words []string
	for _, line := range strings.Split(string(ref), "\n") {
		f := strings.Split(line, "\t")
		if len(f) < 2 {
			continue
		}
		words = append(words, f...)
		for _, form := range f[1:] {
			pairs.WriteString(f[0])
			pairs.WriteByte('\t')
			pairs.WriteString(form)
			pairs.WriteByte('\n')
		}
	}
	if len(words) < 50000 {
		t.Fatalf("only %d words read from the reference pack", len(words))
	}

	data, lines := group([]byte(pairs.String()))
	if lines == 0 {
		t.Fatal("regrouping produced nothing")
	}
	got, err := golem.New(bytePack{"en", data})
	if err != nil {
		t.Fatal(err)
	}
	// Compared through TrimSpace on both sides, because the reference pack has
	// rows with a leading space (" furtherst") - keys nothing can ever look up.
	// This tool trims them, which is what internal/morph already does to any
	// file it reads, so the difference is one wudict cannot observe.
	for _, w := range words {
		key := strings.TrimSpace(w)
		if key == "" {
			continue
		}
		a := strings.TrimSpace(got.LemmaLower(key))
		b := strings.TrimSpace(want.LemmaLower(w))
		if a != b {
			t.Fatalf("%q lemmatizes to %q, golem's own pack says %q", w, a, b)
		}
	}

	// It should also be smaller than the pairs it came from - that saving is
	// the whole reason for grouping.
	if len(data) >= pairs.Len() {
		t.Fatalf("grouped %d bytes from %d pairs bytes", len(data), pairs.Len())
	}
}

func TestHeapMB(t *testing.T) {
	mb, err := heapMB("xx", []byte("know\tknew\tknown\n"))
	if err != nil {
		t.Fatal(err)
	}
	if mb != 1 {
		t.Fatalf("heapMB of a one-line pack = %d, want the 1 MB floor", mb)
	}
	if _, err := heapMB("xx", []byte("broken\n")); err == nil {
		t.Fatal("heapMB must surface the load golem would refuse")
	}
}

func TestHuman(t *testing.T) {
	for in, want := range map[int64]string{
		0: "0 B", 512: "512 B", 1024: "1 KB", 243117: "237 KB", 2 << 20: "2.0 MB",
	} {
		if got := human(in); got != want {
			t.Errorf("human(%d) = %q, want %q", in, got, want)
		}
	}
}
