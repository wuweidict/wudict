// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package dict

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// Fold lowercases and strips combining marks for accent/case-insensitive
// matching. Shared by every direct backend's fold index.
func Fold(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	b.Grow(len(s))
	for _, ru := range norm.NFD.String(s) {
		if unicode.Is(unicode.Mn, ru) {
			continue
		}
		b.WriteRune(ru)
	}
	return b.String()
}
