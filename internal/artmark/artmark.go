// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Package artmark is the markup contract for articles wudict writes itself.
//
// Two kinds of article HTML reach a reader. NATIVE markup (mdx, slob, zim,
// most stardict) comes out of the dictionary with a stylesheet of its own,
// and wudict only sanitises and re-points it. SYNTHESIZED markup is authored
// here - Lingvo DSL is a tag stream with no HTML at all (internal/format/dsl),
// XDXF is an XML vocabulary that must be mapped onto elements
// (internal/format/stardict) - and there is no stylesheet anywhere, because
// nobody wrote one.
//
// Everything below exists so that the second kind is not styled by inventing
// presentation at the point of emission. Three laws:
//
//  1. A CLASS CARRIES A ROLE, NEVER A LOOK. "wu-ex" says "this is an example",
//     not "this is steelblue". The reader restyles a role once and every
//     dictionary that has that role follows; that is the whole point of the
//     Custom styles feature, and inline <font color> defeats it absolutely.
//
//  2. AN AUTHOR'S PARAMETER TRAVELS AS A CUSTOM PROPERTY, NEVER AS A CSS
//     PROPERTY. When a DSL author writes [c crimson] or [m3], that choice
//     rides along as style="--wd-c:crimson" / style="--wd-m:3" and the rule
//     below consumes it. An inline `color:crimson` could only be overridden
//     with !important; an inline --wd-c is beaten by any plain rule the user
//     writes, because the user's rule sets `color` and the author's value only
//     ever reaches `color` through this stylesheet's fallback chain.
//
//  3. DEFAULTS ARE ZERO-SPECIFICITY. Every rule here is wrapped in :where(),
//     so the dictionary's own CSS and the user's own CSS both win ties without
//     a specificity war. Ours is a floor, not a claim.
//
// The vocabulary (all classes are namespaced Prefix; a role with no rule below
// has no default look, and exists so a reader can target it):
//
//	wu-k     headword / keyword
//	wu-def   definition body
//	wu-trn   translation zone      (DSL [trn], XDXF <dtrn>)
//	wu-trs   transcription zone    (DSL [trs])
//	wu-trn-not wu-trs-not  the complements of those two (DSL [!trn] [!trs]);
//	         opposite meanings, so opposite selectors, not one class each way
//	wu-ipa   phonetic run          (DSL [t], XDXF <tr>)
//	wu-gr    grammar note          (XDXF <gr>)
//	wu-p     label / part of speech (DSL [p], XDXF <abr>)
//	wu-abbr  label carrying a title= expansion
//	wu-ex    example               (DSL [ex], XDXF <ex>)
//	wu-com   editorial comment     (DSL [com], XDXF <co>)
//	wu-lang  foreign-language zone (DSL [lang]); carries lang= when known
//	wu-sec   secondary / optional zone (DSL [*])
//	wu-acc   stress mark           (DSL ['])
//	wu-c     author-coloured run   (--wd-c)
//	wu-m     author-indented block (--wd-m, in em)
//	wu-audio wu-video wu-file  media the article points at
//	wu-sub   an article section pulled in beside a link (written by the UI)
//	wu-hl    a full-text match, marked at response time (internal/hilite)
//	wu-cur   the one match being walked to (written by the UI, not ingested)
//
// DefaultCSS is served once per page into both article surfaces (the shadow
// root and the sandboxed iframe), so an article carries none of it: a role
// costs the class attribute and nothing else, and the semantic form is
// strictly smaller in text.db than the <font>-and-inline-style form it
// replaces.
//
// Both surfaces invert wholesale in dark mode (index.html .article filter), so
// there are no dark variants here and there must not be any: a colour defined
// twice would be inverted twice.
package artmark

// Prefix namespaces every class in the vocabulary. It is also the filter the
// `clean` article format applies: a wu- class survives sanitisation because it
// is wudict's own, anything else is dropped with the rest of the presentation.
const Prefix = "wu-"

// Version is bumped when the emitted markup changes in a way that makes
// articles already ingested into a library folder look wrong under the current
// DefaultCSS. It is written into text.db meta as markup_version, and a stale
// value is reported the way a stale fold_version is - as a fact about the
// data, with a rebuild offered and never forced (internal/store).
const Version = 1

// DefaultCSS is the floor. It is injected verbatim into a JS template literal
// (index.html ARTCSS), so it must contain no backtick, no ${ and no </.
const DefaultCSS = `
:where(.wu-m){margin:0;padding-left:calc(var(--wd-m,0)*1em)}
:where(.wu-c){color:var(--wd-c,green)}
:where(.wu-p){font-style:italic;color:var(--wd-p,#2e7d32)}
:where(.wu-abbr){text-decoration:none;border-bottom:1px dotted currentColor;cursor:help}
:where(.wu-ex){color:var(--wd-ex,#4682b4)}
:where(.wu-ipa){font-family:var(--wd-ipa,"Charis SIL","Doulos SIL","Gentium Plus",Helvetica,sans-serif)}
:where(.wu-acc){text-decoration:underline}
:where(.wu-sec){opacity:.85}
:where(.wu-gr){font-style:italic;opacity:.75}
:where(.wu-com){opacity:.8}
:where(.wu-k){font-weight:600}
:where(.wu-audio,.wu-file){text-decoration:none;cursor:pointer}
:where(.wu-video){display:block;margin:.3em 0}
:where(.wu-sub){margin:.4em 0;padding:.4em .6em;border-left:3px solid var(--wd-sub,#e08600);
  background:rgba(224,134,0,.06);border-radius:0 6px 6px 0}
:where(.wu-sub-close){float:right;cursor:pointer;color:#999;font-size:12px;padding:0 .3em}
:where(.wu-sub-close:hover){color:var(--wd-sub,#e08600)}
:where(.wu-hl){background:var(--wd-hl,#ffe066);color:inherit;border-radius:2px}
/* One of the marks is where the reader was just taken; the rest are still
   the same colour, because losing sight of them is what a stronger tint on
   the current one is for. Written by the UI, never ingested, so no Version
   bump: an article already in a library folder is unaffected. */
:where(.wu-hl.wu-cur){background:var(--wd-hl-cur,#ffab40);box-shadow:0 0 0 2px var(--wd-hl-cur,#ffab40)}
`

// IsColor accepts the two forms a DSL [c] argument may legally take - a
// CSS colour keyword and a #hex - and nothing else. This is a gate, not a
// validator: the value is about to be concatenated into a style attribute, so
// what matters is that no `;`, `:`, `(`, quote or space can pass, and an
// unknown keyword that does pass merely renders as no colour at all.
func IsColor(s string) bool {
	if s == "" || len(s) > 32 {
		return false
	}
	if s[0] == '#' {
		for i := 1; i < len(s); i++ {
			c := s[i]
			if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
				return false
			}
		}
		return len(s) >= 4
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z') {
			return false
		}
	}
	return true
}
