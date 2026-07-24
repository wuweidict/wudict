package dsl

import "strings"

// titleResult carries the two headword variants of one DSL title line
// plus its display form. Port of pyglossary's TitleTransformer:
//
//	(...)  optional part: kept in Full, absent from Alt
//	{...}  unsorted part: rendered only into Display (markup allowed)
//	\x     escaped char
type titleResult struct {
	Full    string // headword with optional parts unwrapped
	Alt     string // headword with optional parts removed
	Display string // HTML display title (unsorted parts rendered)
}

func transformTitle(line string) titleResult {
	var full, alt, display strings.Builder
	pos := 0
	end := func() bool { return pos >= len(line) }
	next := func() byte { c := line[pos]; pos++; return c }

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
	addBoth := func(c byte) { // outside parens: all three
		full.WriteByte(c)
		alt.WriteByte(c)
		escByte(&display, c)
	}
	addFull := func(c byte) { // inside parens: full + display only
		full.WriteByte(c)
		escByte(&display, c)
	}

	for !end() {
		c := next()
		switch c {
		case '\\':
			if end() {
				addBoth(c)
				break
			}
			addBoth(next())
		case '(':
			for !end() {
				c = next()
				if c == '\\' {
					if end() {
						break
					}
					addFull(next())
					continue
				}
				if c == ')' {
					break
				}
				addFull(c)
			}
		case '{':
			start := pos
			depth := 1
			for !end() && depth > 0 {
				c = next()
				if c == '\\' && !end() {
					next()
					continue
				}
				if c == '{' {
					depth++
				} else if c == '}' {
					depth--
				}
			}
			inner := line[start : pos-1]
			if html, _, err := transformBody(inner, ""); err == nil {
				display.WriteString(html)
			}
		default:
			addBoth(c)
		}
	}
	return titleResult{
		Full:    strings.TrimSpace(full.String()),
		Alt:     strings.TrimSpace(alt.String()),
		Display: strings.TrimSpace(display.String()),
	}
}
