// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"regexp"
	"strings"

	"github.com/legbehindneck/wudict/internal/dict"
	"github.com/legbehindneck/wudict/internal/htmlref"
)

// schemeRef matches any real URI scheme (http:, data:, bword:, entry:, d:, x:…).
var schemeRef = regexp.MustCompile(`(?i)^[a-z][a-z0-9+.-]*:`)

// soundOrFile matches the two pseudo-schemes dictionaries use for their own
// bundled resources; these DO get rewritten.
var soundOrFile = regexp.MustCompile(`(?i)^(?:sound|file)://`)

// subEntryRef matches a cross-reference to an MDict sub-entry (D26): a headword
// beginning with "@", addressed through an authority — `entry://@examples_woman`.
//
// That spelling is unsafe and we rewrite it to the slash-less `entry:@…`.
// With "//" the browser parses an AUTHORITY, in which "@" is the userinfo
// delimiter: `entry://@examples_woman` has empty userinfo and host
// "examples_woman", and re-serializing drops the "@" entirely. getAttribute()
// still returns the raw string, but anything that reads the .href *property*
// gets the mangled form — the status bar on hover, and, decisively, dictionary
// scripts that round-trip their own anchors (LDOCE6's entry.js does). Once the
// attribute has been rewritten in the DOM, our "@" test fails and the click
// falls through to a lookup, replacing the article with a fragment of itself
// — exactly the navigation D26 set out to prevent.
//
// Without "//" the rest is an opaque path: no authority, no userinfo, so "@"
// survives every normalization. Idempotent (the output no longer matches).
var subEntryRef = regexp.MustCompile(`(?i)^(bword|entry)://@`)

// isResourceRef decides whether one reference names a file this dictionary
// BUNDLES — something the browser will fetch — as opposed to a cross-reference
// the user may follow.
//
// This is the question the old regexes could not even ask. They matched
// `href="…"` with no idea which element it sat on, so every relative href
// became /res/{dict}/…, and `<a href="defendant">` — a headword in a slob
// dictionary, the plainest cross-reference there is — was turned into a
// resource fetch that could only 404. Element and attribute together are what
// make the answer decidable:
//
//	SITE                          | RESOURCE? | WHY
//	------------------------------|-----------|---------------------------------
//	url(...) in CSS               | always    | CSS URLs only ever name files
//	srcset candidate              | always    | images by definition
//	src, poster, data, background | always    | the browser fetches these
//	data-src, xlink:href, …       | always    | repacks stash real refs here
//	href on <link>, <image>, <use>| always    | stylesheets and SVG resources —
//	                              |           | the elements where a browser
//	                              |           | actually FETCHES an href
//	href anywhere else            | only if   | on <a> it is a cross-reference;
//	                              | it names  | on <span>/<div> it is a
//	                              | an asset  | non-standard attribute the
//	                              | file      | dictionary's own script reads,
//	                              |           | which is also a cross-reference
//	                              |           | (OALD10: <span class="xr-g"
//	                              |           | href="defendant_e">). But
//	                              |           | pronunciation audio lives on <a>
//	                              |           | too, and those scripts read
//	                              |           | this.href — so a reference that
//	                              |           | names a FILE must still be
//	                              |           | rewritten.
//
// The last row is an allowlist on purpose: the fetching elements are a short,
// closed set, while the elements a dictionary might hang its own href on are
// not. Guessing the other way around is what produced /res/{dict}/defendant.
// dict.IsAssetName draws the remaining line — `defendant` and
// `defendant__gb_1.ogg` are told apart by extension and nothing else.
func isResourceRef(r htmlref.Ref) bool {
	if r.Site != htmlref.SiteAttr {
		return true
	}
	if attrBase(r.Attr) != "href" {
		return true
	}
	switch r.Tag {
	case "link", "image", "use":
		return true
	}
	// An explicit pseudo-scheme is the author saying "this is my file".
	return soundOrFile.MatchString(r.URL) || dict.IsAssetName(r.URL)
}

// attrBase strips a data-/xlink:-style prefix, so data-src is src.
func attrBase(name string) string {
	if i := strings.LastIndexAny(name, "-:"); i >= 0 {
		return name[i+1:]
	}
	return name
}

// RewriteEntryHTML rewrites resource references inside one article's HTML so
// the browser fetches them from /res/{dictID}/….
//
// The walk is a real HTML tokenizer (internal/htmlref) rather than a set of
// regular expressions, which is what lets it see unquoted and malformed
// attributes — `<a href=plaintiff__gb_1.ogg">` is ordinary in repacked
// dictionaries and was previously invisible — and what keeps it from rewriting
// `src="…"` written inside a <script> string or inside prose.
//
// The result is a root-absolute URL (/res/{dictID}/…). wudict is served
// at the site root by design, so an absolute /res/ path resolves to the same
// origin-rooted URL in EVERY rendering context — the main page, a Shadow-DOM
// article, and (critically) a srcdoc iframe, whose base URL differs and inside
// which third-party dictionary scripts re-resolve relative refs against the
// wrong base. Absolute refs are context-insensitive, which removes the whole
// class of doubled res/{d}/res/{d}/… bugs.
//
// Left untouched: fragments (#), query-only (?), protocol-relative (//), real
// schemes (http:, https:, data:, bword:, entry:, d:, x:, …), cross-reference
// links (see isResourceRef), and anything already under /res/{dictID}/ — the
// function is idempotent and can never produce a doubled URL.
//
// <base> is dropped: in dictionary HTML it is a scraping leftover, and left in
// place it would re-root every relative reference in the article.
func RewriteEntryHTML(html, dictID string) string {
	if html == "" || dictID == "" {
		return html
	}
	// Fast path: skip the tokenizer when the article has nothing rewritable.
	// These bare substrings are a strict superset of every reference site
	// (srcset⊃src, style⊃<style, and every attribute name below), so their
	// absence guarantees no match. Plain-text definitions — common for
	// StarDict and DSL — skip the whole pipeline.
	if !strings.Contains(html, "src") && !strings.Contains(html, "href") &&
		!strings.Contains(html, "data") && !strings.Contains(html, "poster") &&
		!strings.Contains(html, "style") && !strings.Contains(html, "background") &&
		!strings.Contains(html, "longdesc") && !strings.Contains(html, "usemap") &&
		!strings.Contains(html, "<base") {
		return html
	}

	absPrefix := "/res/" + dictID + "/"
	relPrefix := "res/" + dictID + "/"

	return htmlref.Rewriter{
		Drop: func(tag string) bool { return tag == "base" },
		URL: func(r htmlref.Ref) string {
			ref := r.URL
			switch {
			case ref == "",
				strings.HasPrefix(ref, "#"),
				strings.HasPrefix(ref, "?"),
				strings.HasPrefix(ref, "//"),
				strings.HasPrefix(ref, absPrefix),
				strings.HasPrefix(ref, relPrefix):
				return ref
			case subEntryRef.MatchString(ref):
				// Must precede the scheme case, which would pass it through.
				return subEntryRef.ReplaceAllString(ref, "$1:@")
			case schemeRef.MatchString(ref) && !soundOrFile.MatchString(ref):
				return ref
			}
			if !isResourceRef(r) {
				return ref
			}
			return resURL(dictID, ref)
		},
	}.Rewrite(html)
}

// resURL maps one dictionary-internal reference to its root-absolute /res/
// URL: the sound://‌/file:// pseudo-scheme and a single leading "/" or "./"
// are stripped, mirroring the resource names the backends serve.
func resURL(dictID, ref string) string {
	ref = soundOrFile.ReplaceAllString(ref, "")
	if strings.HasPrefix(ref, "./") {
		ref = ref[2:]
	} else {
		ref = strings.TrimPrefix(ref, "/")
	}
	return "/res/" + dictID + "/" + ref
}
