// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The colour tokens at the top of index.html are the only place the chrome's
// colours are chosen, so the contrast of every pair that carries text is held
// here rather than re-measured by eye after each edit. Ratios are WCAG 2.x
// relative-luminance contrast, used as a yardstick (D107: no conformance
// claim). "pending" rows are known shortfalls with the fix that closes them
// (docs.local/UX-PLAN.md P2); a pending row that starts passing fails the
// test until it moves to the enforced list, so the list only ever tightens.

type tokenPair struct {
	fg, bg string
	min    float64
	themes string // "light", "dark" or "both"
	fix    string // pending rows only
}

var enforcedPairs = []tokenPair{
	{"--fg", "--bg", 4.5, "both", ""},
	{"--fg", "--bg-card", 4.5, "both", ""},
	{"--fg", "--accent-soft", 4.5, "both", ""},
	{"--fg-soft", "--bg", 4.5, "both", ""},
	{"--fg-soft", "--bg-card", 4.5, "both", ""},
	{"--hl-fg", "--hl", 4.5, "both", ""},
	{"--safe", "--bg-card", 4.5, "both", ""},
	{"--safe", "--safe-soft", 4.5, "both", ""},
	{"--link", "--bg-card", 4.5, "dark", ""},
	{"--link", "--bg", 4.5, "dark", ""},
	{"--link", "--accent-soft", 4.5, "dark", ""},
	{"--danger", "--bg-card", 4.5, "dark", ""},
	{"--danger", "--danger-soft", 4.5, "dark", ""},
	{"--accent", "--bg-card", 3, "dark", ""},
	{"--accent", "--bg", 3, "dark", ""},
}

var pendingPairs = []tokenPair{
	{"--accent", "--bg-card", 3, "light", "P2 2.1 (A1): --accent-text #d68000 for accent-coloured text"},
	{"--accent", "--bg", 3, "light", "P2 2.1 (A1): --accent-text #d68000 for accent-coloured text"},
	{"--fg-faint", "--bg-card", 3.5, "both", "P2 2.2 (B1): --fg-faint #8d887f light, #7b7770 dark"},
	{"--fg-faint", "--bg", 3.5, "both", "P2 2.2 (B1): --fg-faint #8d887f light, #7b7770 dark"},
	{"--link", "--bg-card", 4.5, "light", "P2: --link a step darker"},
	{"--link", "--bg", 4.5, "light", "P2: --link a step darker"},
	{"--link", "--accent-soft", 4.5, "light", "P2: --link a step darker"},
	{"--danger", "--bg-card", 4.5, "light", "P2: --danger a step darker"},
	{"--danger", "--danger-soft", 4.5, "light", "P2: --danger a step darker"},
}

func TestTokenContrast(t *testing.T) {
	light, dark, darkAuto := colourTokens(t, string(indexHTMLSrc))

	// The dark palette is written twice - for an explicit theme and for the
	// OS preference - and the two must be one palette.
	for k, v := range dark {
		if darkAuto[k] != v {
			t.Errorf("dark token %s: %q under [data-theme=dark] but %q under prefers-color-scheme", k, v, darkAuto[k])
		}
	}
	if len(dark) != len(darkAuto) {
		t.Errorf("dark palettes differ in size: %d vs %d tokens", len(dark), len(darkAuto))
	}

	themes := map[string]map[string]string{"light": light, "dark": dark}
	ratio := func(theme string, p tokenPair) float64 {
		tok := themes[theme]
		if theme == "dark" { // a token the dark block does not redefine keeps its light value
			tok = map[string]string{}
			for k, v := range light {
				tok[k] = v
			}
			for k, v := range dark {
				tok[k] = v
			}
		}
		a, err := hexLuminance(tok[p.fg])
		if err != nil {
			t.Fatalf("%s %s: %v", theme, p.fg, err)
		}
		b, err := hexLuminance(tok[p.bg])
		if err != nil {
			t.Fatalf("%s %s: %v", theme, p.bg, err)
		}
		hi, lo := math.Max(a, b), math.Min(a, b)
		return (hi + 0.05) / (lo + 0.05)
	}
	each := func(p tokenPair, f func(theme string)) {
		for _, th := range []string{"light", "dark"} {
			if p.themes == "both" || p.themes == th {
				f(th)
			}
		}
	}
	for _, p := range enforcedPairs {
		each(p, func(th string) {
			if r := ratio(th, p); r < p.min {
				t.Errorf("%s: %s on %s is %.2f:1, below %.1f:1", th, p.fg, p.bg, r, p.min)
			}
		})
	}
	for _, p := range pendingPairs {
		each(p, func(th string) {
			if r := ratio(th, p); r >= p.min {
				t.Errorf("%s: %s on %s now reaches %.2f:1 - move it from pendingPairs to enforcedPairs", th, p.fg, p.bg, r)
			} else {
				t.Logf("pending %s: %s on %s %.2f:1 < %.1f:1 - %s", th, p.fg, p.bg, r, p.min, p.fix)
			}
		})
	}
}

var (
	cssComment = regexp.MustCompile(`(?s)/\*.*?\*/`)
	cssToken   = regexp.MustCompile(`(--[a-z0-9-]+)\s*:\s*([^;]+);`)
)

// colourTokens returns the light palette (the first :root block), the dark
// palette under html[data-theme="dark"], and the dark palette under the
// prefers-color-scheme query.
func colourTokens(t *testing.T, page string) (light, dark, darkAuto map[string]string) {
	t.Helper()
	block := func(open string) map[string]string {
		i := strings.Index(page, open)
		if i < 0 {
			t.Fatalf("index.html has no %q block", open)
		}
		body := page[i+len(open):]
		j := strings.IndexByte(body, '}')
		if j < 0 {
			t.Fatalf("%q block is not closed", open)
		}
		out := map[string]string{}
		for _, m := range cssToken.FindAllStringSubmatch(cssComment.ReplaceAllString(body[:j], ""), -1) {
			out[m[1]] = strings.TrimSpace(m[2])
		}
		if len(out) == 0 {
			t.Fatalf("%q block declares no tokens", open)
		}
		return out
	}
	return block(":root{"), block(`html[data-theme="dark"]{`), block("html:not([data-theme]){")
}

// hexLuminance is WCAG 2.x relative luminance of a #rgb or #rrggbb colour.
func hexLuminance(c string) (float64, error) {
	h := strings.TrimPrefix(strings.TrimSpace(c), "#")
	if len(h) == 3 {
		h = string([]byte{h[0], h[0], h[1], h[1], h[2], h[2]})
	}
	if len(h) != 6 || !strings.HasPrefix(strings.TrimSpace(c), "#") {
		return 0, fmt.Errorf("not a #rgb/#rrggbb colour: %q", c)
	}
	n, err := strconv.ParseUint(h, 16, 32)
	if err != nil {
		return 0, fmt.Errorf("not a hex colour: %q", c)
	}
	ch := func(v uint64) float64 {
		s := float64(v) / 255
		if s <= 0.03928 {
			return s / 12.92
		}
		return math.Pow((s+0.055)/1.055, 2.4)
	}
	return 0.2126*ch(n>>16&0xff) + 0.7152*ch(n>>8&0xff) + 0.0722*ch(n&0xff), nil
}
