// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package dict

import "testing"

func TestDisplayText(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "Collins COBUILD", "Collins COBUILD"},
		{"numeric ref left by the XML layer", "Webster&#x27;s Revised Unabridged Dictionary",
			"Webster's Revised Unabridged Dictionary"},
		{"decimal ref", "Webster&#39;s", "Webster's"},
		{"double-escaped by the builder", "Oxford Advanced Learner&amp;#x27;s Dictionary 10th",
			"Oxford Advanced Learner's Dictionary 10th"},
		{"named entity", "Fish &amp; Chips", "Fish & Chips"},
		{"one ampersand is not an entity", "AT&T", "AT&T"},
		{"already decoded stays put", "Learner's", "Learner's"},
		{"empty", "", ""},
		// Bounded at two passes: a value escaped three deep decodes twice and
		// then stops, which is the point - the loop can never be driven.
		{"bounded", "&amp;amp;amp;lt;", "&amp;lt;"},
	}
	for _, c := range cases {
		if got := DisplayText(c.in); got != c.want {
			t.Errorf("%s: DisplayText(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}
