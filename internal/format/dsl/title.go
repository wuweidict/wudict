// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package dsl

import "strings"

// titleResult carries the two headword variants of one DSL title line
// plus its display form. Port of pyglossary's TitleTransformer:
//
//	(...)  optional part: kept in Full, absent from Alt, bracketed in Display
//	{...}  unsorted part: rendered only into Display (markup allowed)
//	{{..}} comment: dropped from all three
//	\x     escaped char
type titleResult struct {
	Full    string // headword with optional parts unwrapped
	Alt     string // headword with optional parts removed
	Display string // HTML display title (unsorted parts rendered)
}

// transformTitle parses one headword line. The three constructs are
// independent and may be nested in either order, so this is one flat loop
// with a paren flag rather than a paren scanner with its own alphabet:
// `(слов{[']}а{[/']}рной)` puts an unsorted stress mark inside an optional
// part, and a paren loop that did not know about `{` would copy the braces
// verbatim into the lookup key and make the entry unfindable.
func transformTitle(line string) titleResult {
	var full, alt, display strings.Builder
	pos := 0
	inParen := false

	// Headword variants stay raw (they are lookup keys); only the
	// display form is HTML-escaped. Bytes are appended raw so multi-byte
	// UTF-8 sequences survive (string(byte) would mangle them).
	escByte := func(b *strings.Builder, c byte) {
		switch c {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		default:
			b.WriteByte(c)
		}
	}
	// add appends one indexable byte: always to Full and Display, and to
	// Alt only outside an optional part.
	add := func(c byte) {
		full.WriteByte(c)
		if !inParen {
			alt.WriteByte(c)
		}
		escByte(&display, c)
	}

	for pos < len(line) {
		c := line[pos]
		pos++
		switch c {
		case '\\':
			if pos >= len(line) {
				add(c)
				break
			}
			add(line[pos])
			pos++
		case '(':
			if inParen { // stray '(': literal, DSL does not nest optional parts
				add(c)
				break
			}
			// The brackets themselves are index syntax, so they never reach a
			// key - but they are content in the display form, which is what
			// Lingvo and GoldenDict show: "abandonar(se)", not "abandonarse".
			// Without them the reader cannot tell which part is optional.
			inParen = true
			display.WriteByte(c)
		case ')':
			if !inParen {
				add(c)
				break
			}
			inParen = false
			display.WriteByte(c)
		case '{':
			// `{{...}}` is a comment even in a headword: consume, emit nothing.
			if pos < len(line) && line[pos] == '{' {
				if i := strings.Index(line[pos+1:], "}}"); i >= 0 {
					pos += 1 + i + 2
				} else {
					pos = len(line)
				}
				break
			}
			start := pos
			depth := 1
			for pos < len(line) && depth > 0 {
				b := line[pos]
				pos++
				switch b {
				case '\\':
					if pos < len(line) {
						pos++
					}
				case '{':
					depth++
				case '}':
					depth--
				}
			}
			inner := line[start:] // unterminated `{`: take the rest verbatim
			if depth == 0 {
				inner = line[start : pos-1]
			}
			// Fragment, not body: the space in `{headword } suffix` is the word
			// separator and must be preserved.
			if html, _, err := transformFragment(inner, ""); err == nil {
				display.WriteString(html)
			}
		default:
			add(c)
		}
	}
	// The two keys get their interior whitespace collapsed, the display form
	// does not. Deleting an unsorted or optional part leaves the spaces that
	// surrounded it behind - `sample {unsorted part} card` keys as
	// "sample  card", which nobody can type - and a key exists only to be
	// matched. Display is the opposite case: its spacing is the author's.
	return titleResult{
		Full:    collapseSpace(full.String()),
		Alt:     collapseSpace(alt.String()),
		Display: strings.TrimSpace(display.String()),
	}
}

// collapseSpace trims and squeezes runs of whitespace to a single space.
func collapseSpace(s string) string {
	if !strings.ContainsAny(s, " \t\n\v\f\r\u0085\u00a0") {
		return s
	}
	return strings.Join(strings.Fields(s), " ")
}
