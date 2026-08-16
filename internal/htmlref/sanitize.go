// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package htmlref

import (
	"strings"
	"unicode"

	"golang.org/x/net/html"
)

// Sanitize and Text reduce an article to what a client that is NOT wudict's own
// page can safely and cheaply render: no scripts, no dictionary stylesheets, no
// presentational attributes.
//
// Why this is a second walker rather than more hooks on Rewriter: the two have
// opposite contracts. Rewrite promises the ORIGINAL BYTES wherever a value did
// not change — original quoting, original attribute order, malformed input
// round-tripped verbatim — because it edits articles that will be rendered with
// the dictionary's own CSS, where any drift is a visual bug. Sanitize promises
// the reverse: nothing survives that was not explicitly allowed, so it must
// re-serialise everything. A walker asked for both would keep neither. They
// share this package, and rawTextTag, because they share the WHATWG tokenizer —
// the part worth having exactly once.
//
// Measured on the real corpus (q=speed, 9 heavy dictionaries, 748 KB of raw
// article): `clean` is 1.9x smaller, `text` 2.6x. The first cut of `clean`
// managed only 1.5x, because stripping attributes leaves the tag skeleton
// standing — 3,237 of one LDOCE entry's 3,796 elements are <span>, and a
// classless <span></span> is 13 bytes of nothing. Unwrapping those (Policy.Bare)
// is what took LDOCE from 63% of raw to 41%.

// TagAction says what Sanitize does with one element.
type TagAction int

const (
	// TagKeep emits the element, attributes filtered by Policy.Attr.
	TagKeep TagAction = iota
	// TagUnwrap drops the element's own tags but keeps its children, so a
	// <font> or an unknown custom element loses its markup, never its text.
	TagUnwrap
	// TagDrop removes the element, its attributes, its content and its end
	// tag — for anything whose text is not prose.
	TagDrop
)

// Policy is what Sanitize may emit. A zero Policy keeps everything, which is a
// no-op with extra steps; callers pass a real one.
type Policy struct {
	// Tag decides an element's fate by lower-case name.
	Tag func(tag string) TagAction

	// Attr filters one attribute of a kept element; false drops it. Names
	// arrive lower-cased.
	Attr func(tag, name, val string) (string, bool)

	// Elem refines what Tag decided, with the element's attributes in hand, and
	// names the tag to EMIT in its place. It is the hook through which a
	// dictionary's own CSS reaches this walker (see css.go): a <span> the
	// stylesheet displays as a block is emitted as a <div>, so a client that
	// never sees that stylesheet still gets the boundary, and one the
	// stylesheet hides is dropped rather than uncovered.
	//
	// Never consulted for an element Tag already dropped: dropping is a safety
	// decision, and no class may talk a <script> back into the output.
	// Returning "" for the tag means "unchanged".
	Elem func(act TagAction, tag string, attrs []html.Attribute) (TagAction, string)

	// Bare names elements worth keeping only for their attributes. Dictionary
	// markup is overwhelmingly <span class="…">: in one LDOCE article, 3,237
	// of 3,796 elements are spans, and once class and style are filtered away
	// each is a 13-byte <span></span> carrying nothing at all — 42 KB of pure
	// skeleton in a single entry. An element listed here with no surviving
	// attributes is unwrapped instead of emitted. Only elements with no layout
	// meaning of their own belong here; a bare <div> still starts a block.
	Bare func(tag string) bool

	// Replace returns markup to emit in place of an element Tag dropped, so a
	// policy can keep content that only an unsafe element was carrying — a DSL
	// dictionary's pronunciation, which arrives as
	// <object type="audio/x-wav" data="…">, becoming a plain <audio>. The
	// element's subtree is still discarded; the returned markup is emitted
	// VERBATIM, so a policy that builds it must escape it.
	Replace func(tag string, attrs []html.Attribute) string
}

func (p Policy) act(tag string) TagAction {
	if p.Tag == nil {
		return TagKeep
	}
	return p.Tag(tag)
}

// Sanitize rewrites doc under p. Malformed markup is resolved by the tokenizer
// exactly as a browser resolves it, and anything it cannot continue past is
// DISCARDED rather than emitted — the opposite of Rewrite's rule, and
// deliberate: a sanitiser that passes through what it failed to parse is not a
// sanitiser.
func Sanitize(doc string, p Policy) string {
	if doc == "" {
		return doc
	}
	z := html.NewTokenizer(strings.NewReader(doc))
	var b strings.Builder
	b.Grow(len(doc) / 3)

	// The dropped subtree we are inside: its tag and our depth within it, so a
	// nested <div> inside a dropped <div> cannot end the drop early.
	dropTag, dropDepth := "", 0
	// Open elements whose end tag is not simply their own: unwrapped ones
	// (emit "", the end tag is suppressed) and renamed ones (emit the new
	// name). One stack rather than two, because their relative order is what
	// decides where a renamed element closes — `<span>x<span class="Sense">y
	// </span>z</span>` must put z outside the block, and two stacks consulted
	// in a fixed order cannot tell which </span> came first.
	var pending []pendingEnd

	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			return strings.TrimSpace(b.String())
		}
		switch tt {
		case html.StartTagToken, html.SelfClosingTagToken:
			// Token() must be called BEFORE TagName(): TagName consumes the
			// name from the tokenizer's buffer, and a Token() after it comes
			// back with empty Data.
			t := z.Token()
			tag := t.Data
			if dropDepth > 0 {
				if tag == dropTag && tt == html.StartTagToken && !voidTag(tag) {
					dropDepth++
				}
				continue
			}
			act, emit := p.act(tag), tag
			if act != TagDrop && p.Elem != nil {
				if a, name := p.Elem(act, tag, t.Attr); name != "" {
					act, emit = a, name
				} else {
					act = a
				}
			}
			open := tt == html.StartTagToken && !voidTag(tag)
			switch act {
			case TagDrop:
				if p.Replace != nil {
					b.WriteString(p.Replace(tag, t.Attr))
				}
				if open {
					dropTag, dropDepth = tag, 1
				}
				continue
			case TagUnwrap:
				if open {
					pending = append(pending, pendingEnd{tag: tag})
				}
				continue
			}
			// The ATTRIBUTE policy still answers for the source element: a
			// <span> emitted as a <div> is the same span it always was, and
			// asking as though it were a div would admit attributes the policy
			// never allowed a span to carry.
			kept := filterAttrs(tag, t.Attr, p)
			// Bare asks about the EMITTED element, which is the whole point of
			// the rename: an attribute-less <span> is 13 bytes of nothing and
			// goes, an attribute-less <div> is a block boundary and stays.
			if len(kept) == 0 && p.Bare != nil && p.Bare(emit) {
				if open {
					pending = append(pending, pendingEnd{tag: tag})
				}
				continue
			}
			if open && emit != tag {
				pending = append(pending, pendingEnd{tag: tag, emit: emit})
			}
			writeOpenTag(&b, emit, kept, tt == html.SelfClosingTagToken)

		case html.EndTagToken:
			tag := z.Token().Data
			if dropDepth > 0 {
				if tag == dropTag {
					if dropDepth--; dropDepth == 0 {
						dropTag = ""
					}
				}
				continue
			}
			// The NEAREST matching open element, not the top of the stack:
			// mis-nested markup would otherwise leak a stray </b>.
			if i := lastIndexOf(pending, tag); i >= 0 {
				emit := pending[i].emit
				pending = append(pending[:i], pending[i+1:]...)
				if emit != "" {
					b.WriteString("</")
					b.WriteString(emit)
					b.WriteString(">")
				}
				continue
			}
			if p.act(tag) == TagKeep && !voidTag(tag) {
				b.WriteString("</")
				b.WriteString(tag)
				b.WriteString(">")
			}

		case html.TextToken:
			if dropDepth == 0 {
				b.Write(z.Raw()) // source text, already escaped, kept escaped
			}

			// Comment and Doctype are dropped: neither is content, and a
			// comment can carry markup a client might be tempted to revive.
		}
	}
}

// pendingEnd is one open element whose end tag is not its own: emit is the
// name to close with, or "" to suppress the end tag entirely.
type pendingEnd struct{ tag, emit string }

func lastIndexOf(s []pendingEnd, v string) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i].tag == v {
			return i
		}
	}
	return -1
}

// filterAttrs applies Policy.Attr, returning what survives. Separated from
// serialisation because Bare has to know the answer before deciding whether the
// element is worth emitting at all.
func filterAttrs(tag string, attrs []html.Attribute, p Policy) []html.Attribute {
	if p.Attr == nil {
		return attrs
	}
	var out []html.Attribute
	for _, a := range attrs {
		name := strings.ToLower(a.Key)
		val, ok := p.Attr(tag, name, a.Val)
		if !ok {
			continue
		}
		out = append(out, html.Attribute{Key: name, Val: val})
	}
	return out
}

// writeOpenTag re-serialises a kept element. Values go through
// html.EscapeString, so an attribute cannot break out of its quotes however the
// source spelled it.
func writeOpenTag(b *strings.Builder, tag string, attrs []html.Attribute, selfClosing bool) {
	b.WriteString("<")
	b.WriteString(tag)
	for _, a := range attrs {
		b.WriteString(" ")
		b.WriteString(a.Key)
		b.WriteString(`="`)
		b.WriteString(html.EscapeString(a.Val))
		b.WriteString(`"`)
	}
	if voidTag(tag) || selfClosing {
		b.WriteString("/>")
	} else {
		b.WriteString(">")
	}
}

// voidTag reports whether an element never has an end tag, so a dropped or
// unwrapped one must not start waiting for one that will never arrive.
func voidTag(name string) bool {
	switch name {
	case "area", "base", "br", "col", "embed", "hr", "img", "input", "link",
		"meta", "param", "source", "track", "wbr":
		return true
	}
	return false
}

// blockTag lists elements whose boundary is a line break in plain text.
func blockTag(name string) bool {
	switch name {
	case "p", "div", "br", "li", "tr", "dd", "dt", "h1", "h2", "h3", "h4", "h5",
		"h6", "blockquote", "pre", "table", "section", "article", "hr", "ul",
		"ol", "dl":
		return true
	}
	return false
}

// Text reduces an article to plain text: no markup, block boundaries as
// newlines, whitespace collapsed. Elements whose content is not prose
// (<script>, <style>, …) are dropped along with their text.
//
// st is the dictionary's own CSS reduced to class → display, or nil. Without
// it the only boundaries available are the block TAGS, and a dictionary whose
// entry is one <span> per sense — which is most of them — collapses into a
// single line. With it, the classes the stylesheet displays as blocks break the
// line too, and the ones it hides contribute nothing.
func Text(doc string, st Styles) string {
	if doc == "" {
		return doc
	}
	z := html.NewTokenizer(strings.NewReader(doc))
	var b strings.Builder
	b.Grow(len(doc) / 4)
	// The subtree being skipped — a raw-text element or one the stylesheet
	// hides — and our depth within it, so a nested span cannot end it early.
	skip, skipDepth := "", 0
	// Open elements that started a block because of their CLASS. Their end tag
	// carries no class, so the boundary has to be remembered here; nearest
	// match wins, as in Sanitize.
	var blocks []string
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			return collapse(b.String())
		}
		switch tt {
		case html.StartTagToken, html.SelfClosingTagToken:
			// Attributes cost an allocation to read, so they are read only when
			// there is a stylesheet to consult. Token() must precede TagName();
			// see the note in Sanitize.
			var name string
			var attrs []html.Attribute
			if len(st) > 0 {
				t := z.Token()
				name, attrs = t.Data, t.Attr
			} else {
				tag, _ := z.TagName()
				name = string(tag)
			}
			open := tt == html.StartTagToken && !voidTag(name)
			if skip != "" {
				if name == skip && open {
					skipDepth++
				}
				continue
			}
			if rawTextTag(name) && tt == html.StartTagToken {
				skip, skipDepth = name, 1
				continue
			}
			switch st.Class(classAttr(attrs)) {
			case DisplayNone:
				if open {
					skip, skipDepth = name, 1
				}
				continue
			case DisplayBlock:
				b.WriteString("\n")
				if open {
					blocks = append(blocks, name)
				}
				continue
			}
			if blockTag(name) {
				b.WriteString("\n")
			}
		case html.EndTagToken:
			tag, _ := z.TagName()
			name := string(tag)
			if skip != "" {
				if name == skip {
					if skipDepth--; skipDepth == 0 {
						skip = ""
					}
				}
				continue
			}
			if i := lastIndexOfString(blocks, name); i >= 0 {
				blocks = append(blocks[:i], blocks[i+1:]...)
				b.WriteString("\n")
				continue
			}
			if blockTag(name) {
				b.WriteString("\n")
			}
		case html.TextToken:
			if skip == "" {
				// Squeezed at the point of writing, not afterwards: source
				// newlines inside a paragraph are just formatting, and only
				// the block boundaries above may become line breaks. Doing it
				// later cannot tell the two apart.
				b.WriteString(squeeze(string(z.Text()))) // entities decoded
			}
		}
	}
}

func lastIndexOfString(s []string, v string) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == v {
			return i
		}
	}
	return -1
}

// classAttr returns an element's class attribute, or "" if it has none.
func classAttr(attrs []html.Attribute) string {
	for _, a := range attrs {
		if a.Key == "class" { // the tokenizer lower-cases attribute names
			return a.Val
		}
	}
	return ""
}

// squeeze reduces every run of whitespace to a single space, preserving one
// leading and trailing space where there was any — inline elements depend on
// it, and "<b>a</b> <i>b</i>" must not come out as "ab".
func squeeze(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	prevSpace := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			if !prevSpace {
				b.WriteByte(' ')
			}
			prevSpace = true
			continue
		}
		b.WriteRune(r)
		prevSpace = false
	}
	return b.String()
}

// collapse drops the empty lines left by nested block elements, so
// <div><div><p>x</p></div></div> is one line rather than five.
func collapse(s string) string {
	var out []string
	for _, ln := range strings.Split(s, "\n") {
		if ln = strings.TrimSpace(ln); ln != "" {
			out = append(out, ln)
		}
	}
	return strings.Join(out, "\n")
}
