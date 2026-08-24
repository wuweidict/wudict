// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package htmlref

import "strings"

// A dictionary article's layout is not in its tags. LDOCE's `destination`
// entry is 3,796 elements of which 3,237 are <span>, and every boundary a
// reader sees - sense from sense, example from definition, one collocation
// from the next - exists only because `ldoce.css` says
// `.ldoceEntry .Sense { display: block }`. Strip the stylesheet and the
// classes, as Sanitize must, and the entry becomes one run of text:
// "…going tosomebody's destination". The same stylesheet hides the internal
// field codes (`.FIELD { display: none }`), so stripping it also UNCOVERS
// content the dictionary never meant to show - "TTTRAVEL" is `<span
// class="FIELD">TT</span><span class="ACTIV">TRAVEL</span>`.
//
// So `clean` and `text` cannot decide layout from tag names alone; the
// information they need is in the dictionary's own CSS, which is a resource of
// the same dictionary. ParseCSS reduces a stylesheet to the only question those
// two formats can act on - does this class start a new block, is it hidden, or
// neither - and Styles answers it per element. Everything else in CSS is a
// look, which is exactly what these formats exist to discard.
//
// This is deliberately NOT a CSS engine. There is no cascade, no specificity,
// no inheritance and no DOM: one flat class → Display table, built once per
// dictionary. Where the real cascade would need context we do not have, the
// resolution rules below (see mergeDisplay) fail toward keeping content and
// toward adding a boundary, because a spurious line break is a blemish and a
// swallowed definition is a bug.

// Display is the layout role a class gives an element, reduced to the three
// answers a markup-shaped client can act on. The values are ordered by
// PRECEDENCE, low to high, and that ordering is load-bearing: merging two rules
// for the same class, or two classes on one element, takes the larger.
//
//   - Unset loses to everything, so an unknown class contributes nothing.
//   - Block beats Inline because a boundary that should not be there costs a
//     line break, while a missing one runs two senses together.
//   - Inline beats None because dropping content is the only irreversible
//     answer here: a class declared `none` in one context and visible in
//     another stays visible.
type Display uint8

const (
	DisplayUnset  Display = iota // no rule seen (the zero value)
	DisplayNone                  // display:none - the dictionary hides this
	DisplayInline                // explicitly inline: no boundary
	DisplayBlock                 // block, list-item, table-*, flex, grid: a boundary
)

// Styles maps a CSS class name to its resolved Display. A nil or empty Styles
// is the "no stylesheet" case and every consumer must treat it as a no-op, so
// that a dictionary without CSS costs exactly what it cost before.
type Styles map[string]Display

// Class resolves one element's `class` attribute - the whole space-separated
// value - to the display its dictionary gives it. Highest precedence wins, so
// class="collo COLLOC" is a block if either of them is.
//
// Written as an index walk rather than strings.Fields: this runs once per
// element of every article of every format=clean response, and Fields would
// allocate a slice each time. Slicing a string and looking the slice up in a
// map allocates nothing.
func (s Styles) Class(attr string) Display {
	if len(s) == 0 || attr == "" {
		return DisplayUnset
	}
	best := DisplayUnset
	for i := 0; i < len(attr); {
		for i < len(attr) && isSpaceByte(attr[i]) {
			i++
		}
		j := i
		for j < len(attr) && !isSpaceByte(attr[j]) {
			j++
		}
		if j > i {
			if d := s[attr[i:j]]; d > best {
				best = d
			}
		}
		i = j
	}
	return best
}

// Limits. A stylesheet is third-party input like everything else here: these
// bound what a hostile or merely enormous one can cost. maxClasses is far above
// any real dictionary (LDOCE's 35 KB stylesheet yields a few hundred) and
// maxNestDepth far above any real @media nesting.
const (
	maxClasses   = 20000
	maxNestDepth = 8
)

// ParseCSS folds one stylesheet into st, which may be nil, and returns the
// result - so several stylesheets of one dictionary accumulate into one table.
// Anything it cannot parse is skipped, never guessed at: this is a reduction,
// and a wrong entry is worse than a missing one.
func ParseCSS(css string, st Styles) Styles {
	if css == "" {
		return st
	}
	p := cssParser{s: stripComments(css), out: st}
	p.rules(0)
	return p.out
}

type cssParser struct {
	s   string
	i   int
	out Styles
}

// rules walks a sequence of rules, returning at the end of input or at the '}'
// that closes the enclosing at-rule body.
func (p *cssParser) rules(depth int) {
	for p.i < len(p.s) {
		p.skipSpace()
		if p.i >= len(p.s) {
			return
		}
		switch p.s[p.i] {
		case '}':
			p.i++
			return
		case ';':
			p.i++ // stray semicolon between rules
			continue
		}
		prelude, stop := p.scanTo("{};")
		switch stop {
		case 0, '}':
			return // truncated or malformed: nothing further can be trusted
		case ';':
			continue // an at-rule with no body: @import, @charset, @namespace
		}
		if strings.HasPrefix(strings.TrimSpace(prelude), "@") {
			// @media/@supports/… wrap more rules and must be walked into,
			// because a dictionary can perfectly well put its layout inside
			// one. @font-face/@keyframes/@page contain declarations that are
			// not about any element, and are skipped whole.
			if depth < maxNestDepth && nestedAtRule(prelude) {
				p.rules(depth + 1)
			} else {
				p.block()
			}
			continue
		}
		if d := displayOf(p.block()); d != DisplayUnset {
			p.apply(prelude, d)
		}
	}
}

// apply records one rule's display against every class it can be attributed to
// with confidence.
func (p *cssParser) apply(selectors string, d Display) {
	for _, sel := range splitTop(selectors, ',') {
		classes := finalClasses(sel)
		if len(classes) == 0 {
			continue
		}
		// A compound like `.collo.COLLOC` applies only when BOTH classes are
		// present, which a flat table cannot express. Recording it against each
		// class over-applies - harmless for a boundary, and not harmless at all
		// for `display:none`, which would drop content on the strength of half
		// a selector. So the boundary is taken and the hiding is not.
		if len(classes) > 1 && d == DisplayNone {
			continue
		}
		for _, c := range classes {
			if cur, ok := p.out[c]; ok {
				if d > cur {
					p.out[c] = d
				}
				continue
			}
			if p.out == nil {
				p.out = make(Styles, 64)
			}
			if len(p.out) >= maxClasses {
				return
			}
			p.out[c] = d
		}
	}
}

// scanTo advances to the first byte of stop that is not inside a string,
// returning the text before it and the byte itself (0 at end of input).
func (p *cssParser) scanTo(stop string) (string, byte) {
	start := p.i
	for p.i < len(p.s) {
		switch c := p.s[p.i]; c {
		case '"', '\'':
			p.skipString()
		case '\\':
			p.i += 2 // an escape can hide any delimiter, including a quote
		default:
			if strings.IndexByte(stop, c) >= 0 {
				text := p.s[start:p.i]
				p.i++
				return text, c
			}
			p.i++
		}
	}
	return p.s[start:p.i], 0
}

// block consumes a brace-delimited body, balanced, and returns its contents.
func (p *cssParser) block() string {
	start, depth := p.i, 1
	for p.i < len(p.s) {
		switch c := p.s[p.i]; c {
		case '"', '\'':
			p.skipString()
		case '\\':
			p.i += 2
		case '{':
			depth++
			p.i++
		case '}':
			depth--
			p.i++
			if depth == 0 {
				return p.s[start : p.i-1]
			}
		default:
			p.i++
		}
	}
	return p.s[start:p.i]
}

// skipString consumes a quoted value. An unterminated one ends at the newline,
// as CSS itself specifies, so a stray quote cannot swallow the rest of the file.
func (p *cssParser) skipString() {
	q := p.s[p.i]
	p.i++
	for p.i < len(p.s) {
		c := p.s[p.i]
		if c == '\\' {
			p.i += 2
			continue
		}
		p.i++
		if c == q || c == '\n' {
			return
		}
	}
}

func (p *cssParser) skipSpace() {
	for p.i < len(p.s) && isSpaceByte(p.s[p.i]) {
		p.i++
	}
}

// stripComments removes /* … */ once, up front, so no other scanner has to know
// comments exist. A comment may legally sit between any two tokens - inside a
// selector, inside a value - and handling it everywhere else would mean handling
// it everywhere.
func stripComments(css string) string {
	if !strings.Contains(css, "/*") {
		return css
	}
	var b strings.Builder
	b.Grow(len(css))
	for i := 0; i < len(css); {
		if css[i] == '/' && i+1 < len(css) && css[i+1] == '*' {
			end := strings.Index(css[i+2:], "*/")
			if end < 0 {
				break // unterminated: the rest of the file is comment
			}
			i += 2 + end + 2
			continue
		}
		b.WriteByte(css[i])
		i++
	}
	return b.String()
}

// nestedAtRule reports whether an at-rule's body holds RULES (which we walk)
// rather than declarations (which belong to no element, so we skip them).
func nestedAtRule(prelude string) bool {
	s := strings.TrimSpace(prelude)
	if !strings.HasPrefix(s, "@") {
		return false
	}
	s = s[1:]
	i := 0
	for i < len(s) && !isSpaceByte(s[i]) && s[i] != '(' {
		i++
	}
	switch strings.ToLower(s[:i]) {
	case "media", "supports", "document", "-moz-document", "layer", "container", "scope":
		return true
	}
	return false
}

// displayOf finds the display a declaration block sets. Last one wins, which is
// the cascade's own rule within a single block. Scanning stops at a nested rule
// (CSS nesting): its declarations belong to a different selector.
func displayOf(body string) Display {
	out := DisplayUnset
	for i := 0; i < len(body); {
		j, depth := i, 0
		for j < len(body) {
			c := body[j]
			if c == '"' || c == '\'' {
				j = skipStringAt(body, j)
				continue
			}
			if c == '{' || (c == ';' && depth == 0) {
				break
			}
			switch c {
			case '(':
				depth++
			case ')':
				if depth > 0 {
					depth--
				}
			}
			j++
		}
		if j < len(body) && body[j] == '{' {
			return out
		}
		if d := declDisplay(body[i:j]); d != DisplayUnset {
			out = d
		}
		i = j + 1
	}
	return out
}

func declDisplay(decl string) Display {
	name, val, ok := strings.Cut(decl, ":")
	if !ok || !strings.EqualFold(strings.TrimSpace(name), "display") {
		return DisplayUnset
	}
	return displayValue(val)
}

// displayValue maps a display value to the three answers that matter. The
// two-value syntax (`display: block flow`, `display: inline flow-root`) is read
// from its outer role, which is the first keyword - except `none`, which is
// only ever alone but is worth finding wherever it sits.
func displayValue(v string) Display {
	if i := strings.IndexByte(v, '!'); i >= 0 {
		v = v[:i] // !important
	}
	f := strings.Fields(strings.ToLower(v))
	if len(f) == 0 {
		return DisplayUnset
	}
	for _, w := range f {
		if w == "none" {
			return DisplayNone
		}
	}
	switch head := f[0]; {
	case head == "inherit", head == "initial", head == "unset", head == "revert",
		head == "revert-layer":
		// Says nothing without a DOM to inherit through, and guessing would be
		// worse than the silence.
		return DisplayUnset
	case strings.HasPrefix(head, "inline"), head == "contents",
		head == "ruby", strings.HasPrefix(head, "ruby-"):
		return DisplayInline
	}
	return DisplayBlock
}

// finalClasses returns the classes of a selector's rightmost compound - the
// part that names the element the rule actually styles - or nothing when the
// selector is one this flat model must not pretend to understand.
//
// Refused, all for the same reason: the answer would depend on state or context
// that a per-class table cannot carry.
//
//	:hover, ::before   a rule that applies only sometimes
//	[data-x]           a rule that applies only to some elements
//	#id                names one element, not a class of them
//	\                  an escaped class name; the article's class attribute
//	                   holds the unescaped form, so matching would need a
//	                   CSS unescaper to be correct
func finalClasses(sel string) []string {
	sel = strings.TrimSpace(sel)
	if sel == "" || strings.ContainsAny(sel, `\"'`) {
		return nil
	}
	// The rightmost compound: everything after the last top-level combinator.
	start, depth := 0, 0
	for i := 0; i < len(sel); i++ {
		switch c := sel[i]; c {
		case '(', '[':
			depth++
		case ')', ']':
			if depth > 0 {
				depth--
			}
		case ' ', '\t', '\n', '\r', '\f', '>', '+', '~':
			if depth == 0 {
				start = i + 1
			}
		}
	}
	compound := sel[start:]
	if compound == "" || strings.ContainsAny(compound, ":[]()#") {
		return nil
	}
	parts := strings.Split(compound, ".")
	if len(parts) < 2 {
		return nil // no class at all: an element or universal selector
	}
	classes := parts[1:]
	for _, c := range classes {
		if c == "" {
			return nil // ".." or a trailing dot: malformed
		}
	}
	return classes
}

// splitTop splits on sep at nesting depth zero, so a comma inside :is(a, b) or
// inside an attribute value does not end a selector.
func splitTop(s string, sep byte) []string {
	var out []string
	start, depth := 0, 0
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case '"', '\'':
			i = skipStringAt(s, i) - 1
		case '(', '[':
			depth++
		case ')', ']':
			if depth > 0 {
				depth--
			}
		case sep:
			if depth == 0 {
				out = append(out, s[start:i])
				start = i + 1
			}
		}
	}
	return append(out, s[start:])
}

// skipStringAt returns the index just past the quoted run beginning at i.
func skipStringAt(s string, i int) int {
	q := s[i]
	for i++; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case q:
			return i + 1
		case '\n':
			return i + 1
		}
	}
	return i
}

func isSpaceByte(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f'
}
