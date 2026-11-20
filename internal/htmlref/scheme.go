// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package htmlref

import "strings"

// The app renders every article light and makes dark mode by inverting it, so
// a dictionary must never see a dark colour scheme: a stylesheet that answers
// `prefers-color-scheme: dark` with light text on a background it puts on
// <body> - which a shadow root does not have - comes out dark on dark after the
// inversion (8 of 24 such dictionaries in a 190-dictionary library, 39-74 % of
// their text under 3:1). With the app forced light and the system dark, the
// same rules show that light text on white.
//
// LightScheme rewrites the media feature, not the rules: `prefers-color-scheme:
// dark` becomes `grid` and `prefers-color-scheme: light` becomes `grid: 0`.
// Every screen is a bitmap device, so `(grid)` is constant-false and
// `(grid: 0)` constant-true, and the parenthesised feature keeps its shape -
// compound queries, comma lists and `not` keep their exact meaning with no CSS
// evaluator. Measured: Chromium 152, Android WebView 157 and 104 (matchMedia and
// @media inside a shadow root, `not (grid)` included).
//
// Matching is ASCII case-insensitive, as CSS is; CSS whitespace may surround the
// colon. A name that only ends in the feature (`--my-prefers-color-scheme`) and
// a value that only starts with one (`darker`) are left alone.
func LightScheme(s string) string {
	i := indexSchemeFeature(s, 0)
	if i < 0 {
		return s
	}
	var b strings.Builder
	last := 0
	for i >= 0 {
		end, light, ok := schemeValue(s, i+len(schemeFeature))
		if !ok {
			i = indexSchemeFeature(s, i+len(schemeFeature))
			continue
		}
		if b.Len() == 0 {
			b.Grow(len(s))
		}
		b.WriteString(s[last:i])
		if light {
			b.WriteString("grid: 0")
		} else {
			b.WriteString("grid")
		}
		last = end
		i = indexSchemeFeature(s, end)
	}
	if last == 0 {
		return s
	}
	b.WriteString(s[last:])
	return b.String()
}

// NeedsLightScheme reports whether s names the feature at all, so a caller
// holding bytes converts them only when LightScheme can change something.
func NeedsLightScheme[T string | []byte](s T) bool {
	return indexSchemeFeature(s, 0) >= 0
}

const schemeFeature = "prefers-color-scheme"

// indexSchemeFeature finds the feature name at or after from, ASCII
// case-insensitively, where it starts a name (not after an identifier byte).
func indexSchemeFeature[T string | []byte](s T, from int) int {
	n := len(schemeFeature)
	for i := max(from, 0); i+n <= len(s); i++ {
		if s[i]|0x20 != 'p' {
			continue
		}
		k := 1
		for k < n && lowerASCII(s[i+k]) == schemeFeature[k] {
			k++
		}
		if k == n && (i == 0 || !identByte(s[i-1])) {
			return i
		}
	}
	return -1
}

// schemeValue reads `<ws>:<ws>dark|light` at j and returns where the value
// ends and whether it is light.
func schemeValue(s string, j int) (end int, light, ok bool) {
	j = skipCSSSpace(s, j)
	if j >= len(s) || s[j] != ':' {
		return 0, false, false
	}
	j = skipCSSSpace(s, j+1)
	k := j
	for k < len(s) && identByte(s[k]) {
		k++
	}
	switch v := s[j:k]; {
	case strings.EqualFold(v, "dark") && isASCII(v):
		return k, false, true
	case strings.EqualFold(v, "light") && isASCII(v):
		return k, true, true
	}
	return 0, false, false
}

func skipCSSSpace(s string, j int) int {
	for j < len(s) {
		switch s[j] {
		case ' ', '\t', '\n', '\r', '\f':
			j++
		default:
			return j
		}
	}
	return j
}

// identByte is a byte that can continue a CSS identifier; any non-ASCII byte
// can.
func identByte(c byte) bool {
	return c == '-' || c == '_' || c >= 0x80 ||
		'a' <= c|0x20 && c|0x20 <= 'z' || '0' <= c && c <= '9'
}

func lowerASCII(c byte) byte {
	if 'A' <= c && c <= 'Z' {
		return c | 0x20
	}
	return c
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}
