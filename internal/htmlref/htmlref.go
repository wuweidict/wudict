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
//	                         |            | Entering <style>/<script>/<title>/
//	                         |            | <textarea> sets the raw-text state.
//	EndTag                   | any        | emitted as-is; leaves raw text.
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
}

// rawTextTag reports whether an element's content is raw text rather than
// markup. The tokenizer already switches to those states; this mirror lets us
// tell a <style> text run (CSS, rewritable) from a <script> one (never).
func rawTextTag(name string) bool {
	switch name {
	case "script", "style", "title", "textarea", "iframe", "noscript", "noembed", "noframes", "xmp":
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
	raw := "" // the raw-text element we are inside, "" when in markup
	for {
		switch z.Next() {
		case html.ErrorToken:
			// io.EOF, or markup the tokenizer cannot continue past. Emit
			// whatever it did not consume: a rewriter must never silently
			// truncate an article.
			b.Write(z.Raw())
			return b.String()

		case html.StartTagToken, html.SelfClosingTagToken:
			// Raw() is only valid until the next Next(); Token() copies.
			t := z.Token()
			if rw.Drop != nil && rw.Drop(t.Data) {
				continue
			}
			if raw == "" && rawTextTag(t.Data) && t.Type == html.StartTagToken {
				raw = t.Data
			}
			if rw.rewriteAttrs(&t) {
				b.WriteString(t.String()) // re-serialised: values changed
			} else {
				b.Write(z.Raw()) // untouched: original bytes, original quoting
			}

		case html.EndTagToken:
			if raw != "" {
				if name, _ := z.TagName(); string(name) == raw {
					raw = ""
				}
			}
			b.Write(z.Raw())

		case html.TextToken:
			// Only <style> holds rewritable text. <script> explicitly does
			// not, and neither does prose.
			if raw == "style" && rw.URL != nil {
				b.WriteString(rewriteCSS(string(z.Raw()), func(u string) string {
					return rw.URL(Ref{Site: SiteCSS, Tag: "style", URL: u})
				}))
			} else {
				b.Write(z.Raw())
			}

		default: // Comment, Doctype
			b.Write(z.Raw())
		}
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
