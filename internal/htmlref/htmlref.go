// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Package htmlref locates the resource references inside one dictionary
// article and rewrites them, using the browser's own HTML tokenizer.
//
// # Why a tokenizer and not a regular expression
//
// This package exists because two separate call sites - the server's /res/
// rewriter and the media packer - each grew their own regular expression over
// article HTML, and both encoded the same wrong assumption: that attribute
// values are quoted. They are frequently not. Real dictionary HTML is
// hand-written, machine-repacked or scraped, and this is ordinary in it:
//
//	<a href=plaintiff__gb_1.ogg" onclick="new Audio(this.href).play()">
//
// The opening quote is missing. Every browser resolves that by the WHATWG
// tokenizer's "attribute value (unquoted) state", which ends the value at
// whitespace and keeps the stray quote - so the value is `plaintiff__gb_1.ogg"`.
// A `href="([^"]+)"` pattern simply does not see the attribute at all, leaves
// it alone, and the reference escapes to the page origin.
//
// Regexes over markup fail on more than that, and all of it appears in real
// dictionaries: unquoted values (legal HTML), spaces around `=`, `>` inside a
// quoted value, `src=` written inside a JavaScript string, and `href=` written
// in prose or inside a comment. None of those are edge cases you can patch
// one at a time - they are the difference between matching text and parsing a
// document. So this package parses.
//
// # The state machine
//
// golang.org/x/net/html implements the WHATWG tokenizer, which is the state
// machine browsers use; this package is the thin layer over it that decides
// which tokens carry references. Every token is accounted for:
//
//	TOKEN                    | STATE      | ACTION
//	-------------------------|------------|------------------------------------
//	StartTag, SelfClosingTag | any        | inspect attributes (see below). If
//	                         |            | Drop says so, the element is
//	                         |            | omitted. If no value changed, the
//	                         |            | ORIGINAL BYTES are emitted, so the
//	                         |            | document is preserved exactly.
//	                         |            | A raw-text tag (<style>, <script>,
//	                         |            | <title>, <textarea>, …, self-closing
//	                         |            | or dropped included) sets the
//	                         |            | raw-text state, as in the tokenizer.
//	EndTag                   | any        | emitted as-is; leaves raw text.
//	<audio> … </audio>       | PlayLinks  | held back until its end, then
//	                         |            | emitted as a play link or as-is
//	                         |            | (see Rewriter.PlayLinks).
//	Text                     | in <style> | CSS: url(...) references rewritten.
//	Text                     | in <script>| NEVER touched. A `src="…"` inside a
//	                         |            | script is a string literal, not
//	                         |            | markup - the regexes rewrote it.
//	Text                     | otherwise  | emitted as-is. Prose that mentions
//	                         |            | href="…" is not a reference either.
//	Comment, Doctype         | any        | emitted as-is.
//	Error                    | any        | end of input, or a malformed tail
//	                         |            | the tokenizer cannot continue past.
//	                         |            | Whatever is left is emitted so the
//	                         |            | function never loses bytes.
//
// Attribute classification, per element+attribute, is NOT decided here - it is
// policy, and the two call sites want different answers. See Ref and Rewriter.
//
// # Fidelity
//
// A Rewriter whose URL function returns its input unchanged reproduces
// well-formed input byte for byte - original quoting, attribute order,
// whitespace and case all survive, because an element with no changed value is
// emitted as its original bytes rather than re-serialised.
//
// The one deliberate exception is a value Clean repairs: `href=x.ogg"` comes
// back as `href="x.ogg"`. That is not a fidelity loss but the repair itself,
// and it is applied before any policy runs, because a malformed value is a
// parsing concern rather than a caller's decision. TestRoundTrip pins both
// halves against real dictionary articles.
//
// The other exception is opt-in: with PlayLinks, an <audio> that qualifies
// becomes an <a>. One that does not keeps its bytes.
package htmlref

import (
	"strings"

	"golang.org/x/net/html"
)

// Site says where a reference was found, because the syntax differs and so
// does what a caller may legitimately do with it.
type Site int

const (
	// SiteAttr is a single-URL attribute: src, poster, href on <link>, …
	SiteAttr Site = iota
	// SiteSrcset is one candidate of a srcset list ("url 2x").
	SiteSrcset
	// SiteCSS is one url(...) inside a style="…" attribute or a <style> block.
	SiteCSS
)

// Ref is one reference, handed to Rewriter.URL for a decision.
//
// Tag and Attr are what make an element-aware policy possible, and their
// absence is why the regexes could not be correct: `href` on <link> is a
// stylesheet this dictionary bundles, while `href` on <a> is a cross-reference
// the user may click. A pattern matching `href="…"` cannot tell them apart, so
// it treated both the same and turned every cross-reference into a resource
// fetch.
type Ref struct {
	Site Site
	Tag  string // lower-case element name ("" for a bare <style> text run)
	Attr string // lower-case attribute name ("" for SiteCSS in a <style> block)
	URL  string // the reference, already through Clean
}

// Rewriter walks an article. Both hooks are optional; a zero Rewriter is an
// expensive no-op that still round-trips exactly.
type Rewriter struct {
	// URL maps one reference to its replacement. Return the input unchanged
	// to leave it alone - that is not merely allowed, it is the case that
	// preserves the original bytes.
	URL func(Ref) string

	// Drop reports whether an element should be removed entirely, by
	// lower-case tag name. wudict uses it for <base>, whose href would
	// otherwise re-root every relative reference in the article.
	Drop func(tag string) bool

	// Text maps one prose text node to its replacement, given the RAW source
	// bytes as they appear in the document - entities intact, nothing
	// unescaped. Returning the input unchanged is the case that preserves the
	// original bytes, and a nil Text is that case for free.
	//
	// It is called only for text that is prose: never inside <script>,
	// <style>, <title> or any other raw-text element (rawTextTag), and never
	// for a comment or a doctype, which are different token types entirely.
	// wudict uses it to mark full-text matches (internal/hilite), which is why
	// the hook takes raw bytes and returns raw bytes: a marker that unescaped
	// its input would have to re-escape it, and any disagreement between the
	// two would rewrite what the article says.
	Text func(raw string) string

	// TextIn is Text for a hook that reads on: rest is the document after
	// the text node, as raw source bytes. It is called in place of Text.
	// internal/hilite marks a phrase that markup splits with it.
	TextIn func(raw, rest string) string

	// PlayLinks turns a speaker icon only GoldenDict can show into one a
	// browser can. LDOCE6 (StarDict) writes every pronunciation as
	//
	//	<audio src="0hwd/bre/7/aeroplane0205.mp3"><img src="img/spkr_r.png"></audio>
	//
	// with no controls attribute. A browser lays out none of it: every UA
	// stylesheet hides audio:not([controls]), and the <img> is fallback
	// content, drawn only where <audio> is unsupported. GoldenDict's StarDict
	// reader rewrites this shape into a play link; so does this.
	//
	// The element becomes `<a class="wu-audio" href="SRC">CONTENT</a>`, the
	// artmark role DSL pronunciation already uses. It converts only when every
	// condition holds; otherwise its bytes are emitted exactly as without
	// PlayLinks, which is what a browser hides:
	//
	//	no <a> open             an anchor cannot hold an anchor
	//	no controls, autoplay,  the browser or a script acts on the element:
	//	muted, loop, id, on*,   a player is drawn, a sound plays on load, a
	//	data-* attribute        script finds it by id or reads its data
	//	a playable SRC          src, else the first child <source src>, after
	//	                        URL: relative, root-absolute, http(s), or
	//	                        data:audio/ - never a scheme a link would run
	//	an <img> in it          the author drew a button. Fallback TEXT is
	//	                        "your browser does not support audio"
	//	a CLOSED, SIMPLE body   only text, comments and the phrasing and media
	//	                        elements of fallbackTag, each closed in order,
	//	                        then </audio>. Any other tag decides where the
	//	                        parser closes the element (a parent's end tag,
	//	                        a block start), which a tokenizer cannot know,
	//	                        so it leaves the element as written. End of
	//	                        input closes it, as in the parser.
	PlayLinks bool
}

// fallbackTag is what an <audio> may contain and still become a play link:
// elements that never close an ancestor, never start a block and never hold
// interactive content. The void ones (voidTag) open nothing to close.
var fallbackTag = map[string]bool{
	"img": true, "source": true, "track": true, "br": true, "wbr": true, // void
	"span": true, "b": true, "i": true, "em": true, "strong": true, "small": true,
	"sub": true, "sup": true, "u": true, "s": true, "font": true, "abbr": true,
	"mark": true, "bdi": true, "bdo": true, "cite": true, "dfn": true, "q": true,
	"code": true, "kbd": true, "samp": true, "var": true, "time": true, "data": true,
}

// pendingAudio holds one <audio> element while Rewrite decides whether it is
// a play button (PlayLinks): the decision rests on its content, which the
// tokenizer has not reached when the start tag is read. Every token buffered
// here was rewritten exactly as it would have been outside, so giving the
// element up (abandon) emits the bytes Rewrite emits without PlayLinks.
type pendingAudio struct {
	open  string          // the start tag, as emitted if the element stays
	src   string          // its src after URL, else the first child <source>'s
	body  strings.Builder // everything between the tags, already rewritten
	stack []string        // fallbackTag elements open inside it
	img   bool            // the fallback draws a button
}

// admit reports whether start tag t keeps the element convertible, and
// records what it contributes.
func (p *pendingAudio) admit(t *html.Token) bool {
	if !fallbackTag[t.Data] {
		return false
	}
	switch t.Data {
	case "img":
		p.img = true
	case "source":
		// Only a child <source> selects the element's resource.
		if p.src == "" && len(p.stack) == 0 {
			p.src = firstAttr(t, "src")
		}
	}
	// A self-closing non-void element is a start tag to the parser.
	if !voidTag(t.Data) {
		p.stack = append(p.stack, t.Data)
	}
	return true
}

// finish emits the element as a play link when it qualifies, given its raw
// end tag ("" when end of input closed it).
func (p *pendingAudio) finish(end string) string {
	if p.img && playable(p.src) {
		return `<a class="wu-audio" href="` + html.EscapeString(p.src) + `">` + p.body.String() + `</a>`
	}
	return p.open + p.body.String() + end
}

// abandon emits what was held back, unchanged; the element stays open.
func (p *pendingAudio) abandon() string { return p.open + p.body.String() }

// playLinkSrc reports whether <audio> start tag t may become a play link, and
// its src. Attribute keys arrive lower-cased; the first src wins, as in the
// parser.
func playLinkSrc(t *html.Token) (string, bool) {
	src, seen := "", false
	for _, a := range t.Attr {
		switch k := a.Key; {
		case k == "src":
			if !seen {
				src, seen = Clean(a.Val), true
			}
		case k == "controls", k == "autoplay", k == "muted", k == "loop", k == "id",
			strings.HasPrefix(k, "on"), strings.HasPrefix(k, "data-"):
			return "", false
		}
	}
	return src, true
}

// firstAttr returns the first value of attribute name (lower-case), through
// Clean.
func firstAttr(t *html.Token, name string) string {
	for _, a := range t.Attr {
		if a.Key == name {
			return Clean(a.Val)
		}
	}
	return ""
}

// playable reports whether u may be a link's href: a reference with no
// scheme (relative, root-absolute, protocol-relative), http(s), or inline
// audio. As <audio src> a javascript: URL is inert; as <a href> it runs. A
// fragment or a query alone names this article, not a file.
func playable(u string) bool {
	if u == "" || u[0] == '#' || u[0] == '?' {
		return false
	}
	i := strings.IndexAny(u, ":/?#")
	if i < 0 || u[i] != ':' {
		return true
	}
	switch strings.ToLower(u[:i]) {
	case "http", "https":
		return true
	case "data":
		return hasPrefixFold(u[i+1:], "audio/")
	}
	return false
}

// endTagName returns the name in a raw end tag ("</SPAN >" → "SPAN") without
// allocating; it ends where the tokenizer ends it.
func endTagName(raw []byte) []byte {
	if len(raw) < 2 {
		return nil
	}
	raw = raw[2:]
	for i, c := range raw {
		switch c {
		case ' ', '\t', '\n', '\f', '\r', '/', '>':
			return raw[:i]
		}
	}
	return raw
}

// tagIs compares a raw tag name with a lower-case one, ASCII case-insensitively,
// as the tokenizer folds names.
func tagIs(name []byte, lower string) bool {
	if len(name) != len(lower) {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if 'A' <= c && c <= 'Z' {
			c += 'a' - 'A'
		}
		if c != lower[i] {
			return false
		}
	}
	return true
}

// rawTextTag reports whether an element's content is raw text rather than
// markup. It is the tokenizer's own list (x/net/html readStartTag), so the
// two cannot disagree about where markup resumes; this mirror lets us tell a
// <style> text run (CSS, rewritable) from a <script> one (never). <plaintext>
// never ends.
func rawTextTag(name string) bool {
	switch name {
	case "script", "style", "title", "textarea", "iframe", "noscript", "noembed", "noframes", "xmp", "plaintext":
		return true
	}
	return false
}

// Rewrite returns doc with every reference passed through URL.
func (rw Rewriter) Rewrite(doc string) string {
	if doc == "" {
		return doc
	}
	z := html.NewTokenizer(strings.NewReader(doc))
	var b strings.Builder
	b.Grow(len(doc) + len(doc)/8)
	out := &b            // b, or the body of the <audio> being decided
	var pa *pendingAudio // non-nil while PlayLinks holds an <audio> back
	inAnchor := false    // an <a> is open (PlayLinks only); a new <a> closes the old one in the parser
	raw := ""            // the raw-text element we are inside, "" when in markup
	pos := 0             // bytes of doc the tokenizer has yielded
	for {
		tt := z.Next()
		pos += len(z.Raw())
		switch tt {
		case html.ErrorToken:
			// io.EOF, or markup the tokenizer cannot continue past. Emit
			// whatever it did not consume: a rewriter must never silently
			// truncate an article.
			tail := z.Raw()
			if pa != nil {
				// A clean end of input closes the element, as in the parser.
				// A malformed tail would land inside the link.
				if len(tail) == 0 {
					b.WriteString(pa.finish(""))
				} else {
					b.WriteString(pa.abandon())
				}
			}
			b.Write(tail)
			return b.String()

		case html.StartTagToken, html.SelfClosingTagToken:
			// Raw() is only valid until the next Next(); Token() copies.
			t := z.Token()
			// The tokenizer enters raw text on these names whether the tag is
			// self-closing or not (the slash is ignored on a non-void element)
			// and whether or not Drop removes it; the mirror follows it.
			if raw == "" && rawTextTag(t.Data) {
				raw = t.Data
			}
			if rw.Drop != nil && rw.Drop(t.Data) {
				continue
			}
			changed := rw.rewriteAttrs(&t)
			if pa != nil {
				if pa.admit(&t) {
					writeTag(&pa.body, &t, changed, z)
					continue
				}
				b.WriteString(pa.abandon())
				pa, out = nil, &b
			}
			if rw.PlayLinks {
				switch t.Data {
				case "a":
					inAnchor = true
				case "audio":
					if src, ok := playLinkSrc(&t); ok && !inAnchor {
						pa = &pendingAudio{src: src}
						var open strings.Builder
						writeTag(&open, &t, changed, z)
						pa.open = open.String()
						out = &pa.body
						continue
					}
				}
			}
			writeTag(out, &t, changed, z)

		case html.EndTagToken:
			if raw != "" {
				// The tokenizer ends raw text only at the matching end tag,
				// and that tag is the next token it yields.
				raw = ""
				out.Write(z.Raw())
				continue
			}
			if rw.PlayLinks {
				name := endTagName(z.Raw())
				if pa != nil {
					n := len(pa.stack)
					switch {
					case n > 0 && tagIs(name, pa.stack[n-1]):
						pa.stack = pa.stack[:n-1]
						pa.body.Write(z.Raw())
						continue
					case n == 0 && tagIs(name, "audio"):
						b.WriteString(pa.finish(string(z.Raw())))
						pa, out = nil, &b
						continue
					}
					b.WriteString(pa.abandon())
					pa, out = nil, &b
				}
				if tagIs(name, "a") {
					inAnchor = false
				}
			}
			out.Write(z.Raw())

		case html.TextToken:
			// Only <style> holds rewritable text. <script> explicitly does
			// not, and neither does prose - but prose is exactly what Text
			// wants, so the two hooks are mutually exclusive by construction:
			// raw == "" IS the definition of prose here.
			switch {
			case raw == "style" && rw.URL != nil:
				out.WriteString(rewriteCSS(string(z.Raw()), func(u string) string {
					return rw.URL(Ref{Site: SiteCSS, Tag: "style", URL: u})
				}))
			case raw == "" && rw.TextIn != nil:
				// The tokenizer yields the document's bytes in order, each
				// token its own, so what it has yielded is where it stands.
				out.WriteString(rw.TextIn(string(z.Raw()), doc[min(pos, len(doc)):]))
			case raw == "" && rw.Text != nil:
				// Raw() is only valid until the next Next(); the hook is
				// called and its result written before that happens.
				out.WriteString(rw.Text(string(z.Raw())))
			default:
				out.Write(z.Raw())
			}

		default: // Comment, Doctype
			out.Write(z.Raw())
		}
	}
}

// writeTag emits start tag t: re-serialised when a value changed, otherwise
// its original bytes - original quoting, attribute order and case.
func writeTag(w *strings.Builder, t *html.Token, changed bool, z *html.Tokenizer) {
	if changed {
		w.WriteString(t.String())
	} else {
		w.Write(z.Raw())
	}
}

// rewriteAttrs applies URL to every reference-bearing attribute of t,
// reporting whether anything changed.
func (rw Rewriter) rewriteAttrs(t *html.Token) bool {
	if rw.URL == nil {
		return false
	}
	changed := false
	for i := range t.Attr {
		a := &t.Attr[i]
		name := strings.ToLower(a.Key)
		var out string
		switch {
		case name == "style":
			out = rewriteCSS(a.Val, func(u string) string {
				return rw.URL(Ref{Site: SiteCSS, Tag: t.Data, Attr: name, URL: Clean(u)})
			})
		case isSrcset(name):
			out = rewriteSrcset(a.Val, func(u string) string {
				return rw.URL(Ref{Site: SiteSrcset, Tag: t.Data, Attr: name, URL: u})
			})
		case isURLAttr(name):
			out = rw.URL(Ref{Site: SiteAttr, Tag: t.Data, Attr: name, URL: Clean(a.Val)})
		default:
			continue
		}
		if out != a.Val {
			a.Val = out
			changed = true
		}
	}
	return changed
}

// MayRef reports whether doc can hold a reference at all. It tests for the
// bare names of every reference site - srcset contains src, style is both the
// attribute and the element, and each attribute in urlAttr - so it is a
// superset: false means Rewrite would find nothing, and the tokenizer can be
// skipped.
func MayRef(doc string) bool {
	for _, s := range [...]string{"src", "href", "data", "poster", "style", "background", "longdesc", "usemap"} {
		if strings.Contains(doc, s) {
			return true
		}
	}
	return false
}

// urlAttr is every attribute whose value is a single URL. The `data-`/`xlink:`
// forms matter: repacked dictionaries habitually stash the real reference in
// data-src or data-original and swap it in with their own script.
var urlAttr = map[string]bool{
	"src": true, "href": true, "data": true, "poster": true,
	"background": true, "longdesc": true, "usemap": true,
}

// isURLAttr accepts a bare name or a prefixed one - data-src, xlink:href,
// data-original-src. The separator is required, so "metadata" is not "data"
// and "srcdoc" is not "src".
func isURLAttr(name string) bool {
	if urlAttr[name] {
		return true
	}
	if i := strings.LastIndexAny(name, "-:"); i >= 0 {
		return urlAttr[name[i+1:]]
	}
	return false
}

func isSrcset(name string) bool {
	return name == "srcset" || name == "imagesrcset" ||
		strings.HasSuffix(name, "-srcset") || strings.HasSuffix(name, ":srcset")
}

// Refs returns every reference in doc, in document order, with duplicates
// kept - deduplication is the caller's policy.
func (rw Rewriter) Refs(doc string) []Ref {
	var out []Ref
	inner := rw.URL
	Rewriter{
		Drop: rw.Drop,
		URL: func(r Ref) string {
			out = append(out, r)
			if inner != nil {
				return inner(r)
			}
			return r.URL
		},
	}.Rewrite(doc)
	return out
}
