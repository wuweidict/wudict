// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/wuweidict/wudict/internal/dict"
)

// TestSample pins where a draw lands: u picks an id, and an id that is a
// sub-entry is drawn again rather than walked past.
func TestSample(t *testing.T) {
	ctx := context.Background()
	s := browseStore(t, []string{"alpha", "@_sub", "beta", "gamma", "@_tail"})
	const max = ^uint64(0)
	words := map[string]bool{"alpha": true, "beta": true, "gamma": true}
	// Ids 1..5; u lands on id 1 + ⌊u/2⁶⁴ × 5⌋. A sub-entry's draw lands on
	// whichever word the next value names, so only its kind is pinned.
	for _, tc := range []struct {
		name string
		u    uint64
		want string // "" = any word
	}{
		{"the first id", 0, "alpha"},
		{"a sub-entry", 1 << 62, ""},  // id 2, @_sub
		{"an entry", 2 << 62, "beta"}, // id 3
		{"the last word", 3 << 62, "gamma"},
		{"the last id, a sub-entry", max, ""}, // id 5, @_tail
	} {
		got, err := s.Sample(ctx, tc.u)
		if err != nil || (tc.want != "" && got != tc.want) || !words[got] {
			t.Errorf("%s: Sample(%#x) = %q, %v; want %q", tc.name, tc.u, got, err, tc.want)
		}
	}

	// The word after a long run of sub-entries is as likely as any other:
	// walking past the run would give it 31 draws in 34.
	run := []string{"a"}
	for i := range 30 {
		run = append(run, fmt.Sprintf("@_s%02d", i))
	}
	long := browseStore(t, append(run, "b", "c", "d"))
	count := map[string]int{}
	u := uint64(1)
	for range 4000 {
		u = splitmix64(u)
		w, err := long.Sample(ctx, u)
		if err != nil {
			t.Fatal(err)
		}
		count[w]++
	}
	// 0.88¹⁶ = 13 % of draws miss every probe and walk on to "b": ~35 %.
	for _, w := range []string{"a", "c", "d"} {
		if count[w] < 700 || count[w] > 1300 {
			t.Errorf("%q drawn %d times in 4000, want about 870 (%v)", w, count[w], count)
		}
	}
	if count["b"] > 1800 {
		t.Errorf("the word after the run drawn %d times in 4000: walked past (%v)", count["b"], count)
	}
	// Every word is reachable, and only words.
	seen := map[string]bool{}
	for i := range uint64(64) {
		w, err := s.Sample(ctx, i<<58)
		if err != nil {
			t.Fatal(err)
		}
		seen[w] = true
	}
	if len(seen) != 3 || !seen["alpha"] || !seen["beta"] || !seen["gamma"] {
		t.Errorf("draws reached %v, want alpha, beta and gamma only", seen)
	}

	only := browseStore(t, []string{"@_only"})
	if _, err := only.Sample(ctx, 0); !errors.Is(err, dict.ErrNotFound) {
		t.Errorf("a dictionary of sub-entries: err = %v, want ErrNotFound", err)
	}
}

func TestHas(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	for w, want := range map[string]bool{
		"corazón":      true,
		"CORAZON":      true, // as exact search: case and accents ignored
		"cuestionarse": true, // an alias
		"interrogar":   true, // a redirect is an alias
		"corazo":       false,
		"nada":         false,
		"":             false,
		"  ":           false,
	} {
		if got, err := s.Has(ctx, w); err != nil || got != want {
			t.Errorf("Has(%q) = %v, %v; want %v", w, got, err, want)
		}
	}
}
