// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package dict

import (
	"math"
	"testing"
)

// KeywordRange is the one place the (offset, n) contract is decided, so the
// readings that used to differ between backends are pinned here: a negative n
// means "everything" rather than a panic, and a huge n cannot overflow into a
// negative slice bound.
func TestKeywordRange(t *testing.T) {
	const total = 10
	cases := []struct {
		name          string
		total, off, n int
		lo, hi        int
		ok            bool
	}{
		{"a plain window", total, 2, 3, 2, 5, true},
		{"n exactly reaches the end", total, 7, 3, 7, 10, true},
		{"n past the end is clamped", total, 7, 99, 7, 10, true},
		{"n == 0 means everything from offset", total, 4, 0, 4, 10, true},
		{"n < 0 means everything, and must not panic", total, 0, -1, 0, 10, true},
		{"negative offset counts as zero", total, -5, 3, 0, 3, true},
		{"negative offset and no limit", total, -5, 0, 0, 10, true},
		{"offset at the end", total, 10, 5, 0, 0, false},
		{"offset past the end", total, 99, 5, 0, 0, false},
		{"an empty dictionary", 0, 0, 0, 0, 0, false},
		// the arithmetic that panicked: offset+n overflowed to a negative
		// slice bound, which make() rejected as "cap out of range"
		{"n at MaxInt cannot overflow", total, 3, math.MaxInt, 3, 10, true},
		{"offset at MaxInt", total, math.MaxInt, 5, 0, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lo, hi, ok := KeywordRange(tc.total, tc.off, tc.n)
			if ok != tc.ok || (ok && (lo != tc.lo || hi != tc.hi)) {
				t.Fatalf("KeywordRange(%d,%d,%d) = %d,%d,%v; want %d,%d,%v",
					tc.total, tc.off, tc.n, lo, hi, ok, tc.lo, tc.hi, tc.ok)
			}
			if ok && (lo < 0 || hi > tc.total || lo > hi) {
				t.Fatalf("unusable slice bounds [%d:%d] for total %d", lo, hi, tc.total)
			}
		})
	}
}
