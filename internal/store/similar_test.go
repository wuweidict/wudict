// Copyright (C) 2026 glowinthedark
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/wuweidict/wudict/internal/wildcard"
)

// osa is the textbook optimal string alignment distance over folded runes:
// the reference the walk must agree with.
func osa(a, b []rune) int {
	d := make([][]int, len(a)+1)
	for i := range d {
		d[i] = make([]int, len(b)+1)
		d[i][0] = i
	}
	for j := range d[0] {
		d[0][j] = j
	}
	for i := 1; i <= len(a); i++ {
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			d[i][j] = min(d[i-1][j]+1, d[i][j-1]+1, d[i-1][j-1]+cost)
			if i > 1 && j > 1 && a[i-1] == b[j-2] && a[i-2] == b[j-1] {
				d[i][j] = min(d[i][j], d[i-2][j-2]+1)
			}
		}
	}
	return d[len(a)][len(b)]
}

func TestDistance(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want int
	}{
		{"recieve", "receive", 1},   // a swap is one edit
		{"Haushlat", "Haushalt", 1}, // capitals ignored, swap
		{"acommodate", "accommodate", 1},
		{"Kohler", "Köhler", 0}, // accents ignored
		{"café", "café", 0},
		{"ca", "abc", 3}, // OSA, not unrestricted Damerau: no edit inside a swap
		{"", "abc", 3},
		{"genehmigst", "genehmigt", 1},
	} {
		if got := Distances(tc.a, 3, []string{tc.b})[0]; got != tc.want {
			t.Errorf("Distances(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
	// One table for many words, each from row 0; farther than the budget
	// reads as budget+1.
	got := Distances("recieve", 1, []string{"receive", "house", "recieve!", "", "reciever"})
	if want := []int{1, 2, 1, 2, 1}; !slices.Equal(got, want) {
		t.Errorf("Distances(recieve, 1) = %v, want %v", got, want)
	}
}

// similarWords is a fixture dense enough that the walk reads several chunks,
// seeks past dead prefixes and meets capitals, accents and sub-entries.
func similarWords() []string {
	words := []string{
		"Haus", "Haushalt", "Haushälter", "haushalten", "Hausherr", "hausen",
		"receive", "receiver", "recipe", "recite", "deceive", "eceive",
		"accommodate", "accommodation", "acommodate",
		"Köhler", "Kohle", "kohler",
		"@_Haushalt", // a sub-entry: never listed
		"genehmigen", "genehmigt", "genehmigte", "Genehmigung",
		// A jump must land on these: a combining mark Fold drops, capitals
		// and accents outside ASCII, NOCASE's own order.
		"háusle", "HÄUSER", "hausé", "haušalt", "Hausȁlt", "rkán", "RKÄN", "rkän",
	}
	for _, a := range "abcdefghk" {
		for _, b := range "aeiouhr" {
			for _, c := range "lmnst" {
				words = append(words, fmt.Sprintf("h%c%c%cung", a, b, c), fmt.Sprintf("r%c%c%c", a, b, c))
			}
		}
	}
	return words
}

// bruteSimilar is what SimilarKeys must answer, read off every headword.
func bruteSimilar(words []string, word string, maxDist, limit int) []string {
	q := primary(word)
	type cand struct {
		w         string
		dist, off int
	}
	var cs []cand
	for _, w := range words {
		f := primary(w)
		swapped := len(f) > 1 && f[0] == q[1] && f[1] == q[0]
		if strings.HasPrefix(w, "@_") || len(f) == 0 || (f[0] != q[0] && !swapped) {
			continue
		}
		if d := osa(q, f); d > 0 && d <= maxDist {
			cs = append(cs, cand{w, d, max(len(f)-len(q), len(q)-len(f))})
		}
	}
	slices.SortFunc(cs, func(a, b cand) int {
		if a.dist != b.dist {
			return a.dist - b.dist
		}
		if a.off != b.off {
			return a.off - b.off
		}
		return collCompare(a.w, b.w)
	})
	out := []string{}
	for _, c := range cs {
		if len(out) == limit {
			break
		}
		out = append(out, c.w)
	}
	return out
}

func TestSimilarKeys(t *testing.T) {
	words := similarWords()
	s := browseStore(t, words)
	ctx := context.Background()
	for _, tc := range []struct {
		word string
		dist int
		want []string // nil: only checked against brute force
	}{
		{"Haushlat", 1, []string{"Haushalt"}},
		{"recieve", 1, []string{"receive"}},
		{"acommodate", 1, []string{"accommodate"}},
		{"genehmigst", 1, []string{"genehmigt"}},
		{"erceive", 1, []string{"receive"}},                  // the first two swapped: the second letter's range
		{"eceive", 1, []string{}},                            // a first letter dropped is out of reach
		{"kohlre", 1, []string{"kohler", "Köhler", "Kohle"}}, // the length nearest first
		{"Köhlr", 1, nil},
		{"haushalt", 2, nil},
		{"hbal", 2, nil},
		{"rkhn", 2, nil},
		{"zzz", 2, []string{}},
	} {
		got, err := s.SimilarKeys(ctx, tc.word, tc.dist, 50)
		if err != nil {
			t.Fatal(err)
		}
		if got == nil {
			got = []string{}
		}
		want := bruteSimilar(words, tc.word, tc.dist, 50)
		if !slices.Equal(got, want) {
			t.Errorf("SimilarKeys(%q, %d) = %q, brute force %q", tc.word, tc.dist, got, want)
		}
		if tc.want != nil && !slices.Equal(got[:min(len(tc.want), len(got))], tc.want) {
			t.Errorf("SimilarKeys(%q, %d) = %q, want it to start with %q", tc.word, tc.dist, got, tc.want)
		}
		// The memo answers the same.
		again, _ := s.SimilarKeys(ctx, tc.word, tc.dist, 50)
		if !slices.Equal(again, got) && !(len(again) == 0 && len(got) == 0) {
			t.Errorf("SimilarKeys(%q) again = %q, first %q", tc.word, again, got)
		}
	}
	if got, _ := s.SimilarKeys(ctx, "ha", 1, 5); got != nil {
		t.Errorf("a word under SimilarMin: %q", got)
	}
	if got, _ := s.SimilarKeys(ctx, "rbal", 2, 3); !slices.Equal(got, bruteSimilar(words, "rbal", 2, 3)) || len(got) != 3 {
		t.Errorf("limit 3: %q", got)
	}
}

// Every third prefix of the fixture, with every edit budget, against brute
// force: the pruning, the band and the seeks may skip nothing within reach,
// and a limit of 5 keeps trimming what the walk has found.
func TestSimilarKeysExhaustive(t *testing.T) {
	words := similarWords()
	s := browseStore(t, words)
	seen := map[string]bool{}
	asked := 0
	for _, w := range words {
		f := []rune(strings.ToLower(w))
		for n := SimilarMin; n <= len(f); n++ {
			q := string(f[:n]) + "x"
			if seen[q] || strings.HasPrefix(q, "@") || wildcard.Fold(f[0]) < 0 {
				continue
			}
			seen[q] = true
			if asked++; asked%3 != 0 {
				continue
			}
			for _, d := range []int{1, 2} {
				got, err := s.SimilarKeys(context.Background(), q, d, 5)
				if err != nil {
					t.Fatal(err)
				}
				if got == nil {
					got = []string{}
				}
				if want := bruteSimilar(words, q, d, 5); !slices.Equal(got, want) {
					t.Fatalf("SimilarKeys(%q, %d) = %q, brute force %q", q, d, got, want)
				}
			}
		}
	}
}
