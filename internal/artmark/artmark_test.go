// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package artmark

import (
	"strings"
	"testing"
)

// DefaultCSS is injected into a JS template literal AND into an HTML <style>
// inside a srcdoc document. Three sequences would end one of those contexts
// early, and the failure would be silent: the page would still load, with the
// article stylesheet truncated at the offending byte.
func TestDefaultCSSIsInjectionSafe(t *testing.T) {
	for _, bad := range []string{"`", "${", "</", "\\"} {
		if strings.Contains(DefaultCSS, bad) {
			t.Errorf("DefaultCSS contains %q, which ends the context it is injected into", bad)
		}
	}
	if strings.Count(DefaultCSS, "{") != strings.Count(DefaultCSS, "}") {
		t.Error("unbalanced braces in DefaultCSS")
	}
}

// Every default must be zero-specificity, or the dictionary's own CSS and the
// reader's Custom styles have to fight it (law 3).
func TestDefaultCSSSelectorsAreZeroSpecificity(t *testing.T) {
	for _, line := range strings.Split(DefaultCSS, "\n") {
		i := strings.Index(line, "{")
		if i < 0 {
			continue
		}
		sel := strings.TrimSpace(line[:i])
		if sel == "" {
			continue // continuation of a declaration block
		}
		if !strings.HasPrefix(sel, ":where(") || !strings.HasSuffix(sel, ")") {
			t.Errorf("selector %q is not wrapped in :where()", sel)
		}
	}
}

func TestIsColor(t *testing.T) {
	ok := []string{"green", "steelblue", "DarkRed", "#abc", "#AABBCC", "#11223344"}
	bad := []string{
		"", "re\"d", "red;position:fixed", "rgb(1,2,3)", "var(--x)",
		"#", "#ab", "#xyz", "red blue", "url(x)", "expression(1)",
		strings.Repeat("a", 33),
	}
	for _, s := range ok {
		if !IsColor(s) {
			t.Errorf("IsColor(%q) = false, want true", s)
		}
	}
	for _, s := range bad {
		if IsColor(s) {
			t.Errorf("IsColor(%q) = true, want false", s)
		}
	}
}
