// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"html"
	"strings"
)

// StripHTML reduces article HTML to plain text for FTS indexing
// (FTS-audit #1: indexing raw HTML pollutes matches with tag/attribute
// tokens). It drops tags entirely, skips <script>/<style> contents,
// unescapes entities, and collapses whitespace. It is deliberately
// forgiving: dictionary HTML is frequently malformed.
func StripHTML(s string) string {
	var b strings.Builder
	b.Grow(len(s) / 2)
	inTag := false
	skipUntil := "" // closing tag we are skipping contents to
	// opening reports that the tag currently being consumed is the one that
	// STARTED the skip, so that a self-closing "<script/>" - which has no
	// contents and no "</script>" anywhere after it - does not swallow the
	// rest of the article. Scoped to that one tag: a "/>" inside script text
	// must not end the skip.
	opening := false
	i := 0
	for i < len(s) {
		c := s[i]
		if inTag {
			if c == '>' {
				inTag = false
				if opening {
					opening = false
					if i > 0 && s[i-1] == '/' {
						skipUntil = ""
					}
				}
			}
			i++
			continue
		}
		if c == '<' {
			rest := s[i:]
			if skipUntil != "" {
				if len(rest) >= len(skipUntil) && strings.EqualFold(rest[:len(skipUntil)], skipUntil) {
					skipUntil = ""
				}
				inTag = true
				i++
				continue
			}
			lower := rest
			if len(lower) > 8 {
				lower = lower[:8]
			}
			lower = strings.ToLower(lower)
			if strings.HasPrefix(lower, "<script") {
				skipUntil, opening = "</script", true
			} else if strings.HasPrefix(lower, "<style") {
				skipUntil, opening = "</style", true
			}
			inTag = true
			b.WriteByte(' ')
			i++
			continue
		}
		if skipUntil == "" {
			b.WriteByte(c)
		}
		i++
	}
	return strings.Join(strings.Fields(html.UnescapeString(b.String())), " ")
}
