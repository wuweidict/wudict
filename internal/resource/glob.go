// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package resource

import (
	"errors"
	"strings"
)

// Glob selects resource names by wildcards, for `wudict res`:
//
//   - "*" is any number of characters within one folder or file name;
//   - "?" is one character;
//   - "**", as a whole name between slashes, is any number of folders, none
//     included;
//   - a trailing slash, "a/", is everything in folder a, at any depth.
//
// A pattern without a slash matches the file name at any depth ("*.ogg" is
// every .ogg); a pattern with one is matched from the top. Both sides are
// compared by Key, so case, Unicode normalization, the separator and a leading
// slash do not count - a lookup ignores them too. Accents count: café.mp3 and
// cafe.mp3 are two files, which is why this is not wildcard.Parse.
//
// There are no [classes] and no escapes: names such as "word [1].mp3" are
// ordinary, and a name with a literal * or ? is reached by its exact spelling,
// which `wudict res` tries before reading anything as a pattern.
type Glob struct {
	segs []globSeg
}

// globSeg is one name between slashes; any marks a whole "**".
type globSeg struct {
	any bool
	r   []rune
}

// IsPattern reports whether s is to be read by NewGlob rather than as one
// name: it holds a wildcard, or ends in a separator.
func IsPattern(s string) bool {
	return strings.ContainsAny(s, "*?") || EndsInSeparator(s)
}

// EndsInSeparator reports whether s names a folder: "audio/", `audio\`.
func EndsInSeparator(s string) bool {
	return strings.HasSuffix(s, "/") || strings.HasSuffix(s, `\`)
}

// NewGlob compiles pattern. An empty pattern is an error; "/" alone is every
// resource.
func NewGlob(pattern string) (*Glob, error) {
	folder := EndsInSeparator(pattern)
	k := Key(pattern)
	if k == "" {
		if folder {
			return &Glob{segs: []globSeg{{any: true}}}, nil
		}
		return nil, errors.New("empty pattern")
	}
	parts := strings.Split(k, "/")
	switch {
	case folder:
		parts = append(parts, "**")
	case len(parts) == 1:
		parts = append([]string{"**"}, parts...)
	}
	g := &Glob{segs: make([]globSeg, 0, len(parts))}
	for _, p := range parts {
		if p == "**" {
			// "**/**" is "**": collapsing keeps the match linear in the
			// number of segments
			if n := len(g.segs); n > 0 && g.segs[n-1].any {
				continue
			}
			g.segs = append(g.segs, globSeg{any: true})
			continue
		}
		g.segs = append(g.segs, globSeg{r: []rune(p)})
	}
	return g, nil
}

// Match reports whether name is selected.
func (g *Glob) Match(name string) bool { return g.MatchKey(Key(name)) }

// MatchKey is Match for a name already folded by Key, for a caller testing
// one name against several patterns: the fold is the costly half of a match.
func (g *Glob) MatchKey(k string) bool {
	if k == "" {
		return false
	}
	return matchSegs(g.segs, strings.Split(k, "/"))
}

// matchSegs matches whole names. A "**" in the middle or at the start spans
// zero or more names; a trailing one spans one or more, so "audio/" selects
// what is in the folder and not a file called "audio".
func matchSegs(p []globSeg, n []string) bool {
	for len(p) > 0 {
		if p[0].any {
			p = p[1:]
			if len(p) == 0 {
				return len(n) > 0
			}
			for i := range n {
				if matchSegs(p, n[i:]) {
					return true
				}
			}
			return false
		}
		if len(n) == 0 || !matchName(p[0].r, []rune(n[0])) {
			return false
		}
		p, n = p[1:], n[1:]
	}
	return len(n) == 0
}

// matchName matches one name against * and ?, backtracking to the last * on a
// mismatch: linear in practice, quadratic at worst. The * test comes before
// the literal test, so a * in the pattern stays a wildcard where the name
// holds a * too.
func matchName(p, s []rune) bool {
	px, sx := 0, 0
	star, mark := -1, 0
	for sx < len(s) {
		switch {
		case px < len(p) && p[px] == '*':
			star, mark = px, sx
			px++
		case px < len(p) && (p[px] == '?' || p[px] == s[sx]):
			px++
			sx++
		case star >= 0:
			px = star + 1
			mark++
			sx = mark
		default:
			return false
		}
	}
	for px < len(p) && p[px] == '*' {
		px++
	}
	return px == len(p)
}
