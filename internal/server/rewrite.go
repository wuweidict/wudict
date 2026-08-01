// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"regexp"
	"strings"
)

// attrRef matches a single-URL resource attribute (src/href/data/poster, and
// prefixed variants such as xlink:href or data-src — those carry real
// resource refs too). The leading \b word boundary keeps the name from
// matching *inside* an unrelated attribute: it must not rewrite the "data" in
// metadata="…". \b is zero-width, so two attributes with no whitespace
// between them are still both matched. Go regexp has no backreferences, so
// the two quote styles are separate alternatives (groups 2 and 3).
var attrRef = regexp.MustCompile(`(?i)\b(src|href|data|poster)=(?:"([^"]+)"|'([^']+)')`)

// srcsetRef matches a srcset attribute — a comma-separated list of
// "url [descriptor]" candidates, rewritten one candidate at a time.
var srcsetRef = regexp.MustCompile(`(?i)\bsrcset=(?:"([^"]+)"|'([^']+)')`)

// styleAttrRef / styleBlockRef / cssURLRef reach url(...) references inside
// *inline* CSS (a style="…" attribute or a <style>…</style> element). URLs
// inside *external* stylesheets need no rewriting — the browser resolves them
// against the stylesheet's own /res/… URL, which is already correct.
var (
	styleAttrRef  = regexp.MustCompile(`(?i)\bstyle=(?:"([^"]*)"|'([^']*)')`)
	styleBlockRef = regexp.MustCompile(`(?is)(<style\b[^>]*>)(.*?)(</style>)`)
	cssURLRef     = regexp.MustCompile(`(?i)url\(\s*['"]?([^'")]+?)['"]?\s*\)`)
)

// baseTagRef matches a <base …> element. In dictionary HTML these are almost
// always a web-scraping leftover; left in place, a <base href> hijacks the
// resolution of every relative URL in the article (inside the Shadow-DOM host
// and the srcdoc iframe alike), so we drop them entirely.
var baseTagRef = regexp.MustCompile(`(?i)<base\b[^>]*>`)

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
// falls through to lookupWord, replacing the article with a fragment of itself
// — exactly the navigation D26 set out to prevent.
//
// Without "//" the rest is an opaque path: no authority, no userinfo, so "@"
// survives every normalization. Idempotent (the output no longer matches).
var subEntryRef = regexp.MustCompile(`(?i)^(bword|entry)://@`)

// RewriteEntryHTML rewrites resource references inside one article's HTML so
// the browser fetches them from /res/{dictID}/…. It handles:
//
//   - src/href/data/poster (and xlink:href, data-src, …) attributes
//   - srcset candidate lists
//   - url(...) refs inside inline style="…" and <style> blocks
//   - and it strips any <base> tag first (a scraping leftover)
//
// sound:// and file:// pseudo-URLs, server-absolute paths ("/img/x.png"), and
// bare relative paths ("spkr.png", "./a") all map to /res/{dictID}/….
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
// schemes (http:, https:, data:, bword:, entry:, d:, x:, …), and anything
// already under /res/{dictID}/ (or a stray relative res/{dictID}/) — the
// function is idempotent and can never produce a doubled URL.
//
// This runs server-side (each reference visited once) so the logic is
// unit-tested here rather than living in embedded page JavaScript.
func RewriteEntryHTML(html, dictID string) string {
	if html == "" || dictID == "" {
		return html
	}
	// Fast path: skip all regex work when the article has nothing rewritable.
	// These bare substrings are a strict superset of every pattern below
	// (attrRef names, srcset⊃src, style⊃<style, and <base), so their absence
	// guarantees no match — false positives only cost a no-op regex pass, the
	// same as before. Plain-text definitions (common for StarDict/DSL) skip
	// the whole pipeline.
	if !strings.Contains(html, "src") && !strings.Contains(html, "href") &&
		!strings.Contains(html, "data") && !strings.Contains(html, "poster") &&
		!strings.Contains(html, "style") && !strings.Contains(html, "<base") {
		return html
	}
	// Drop <base> first: everything below (and the browser) must resolve
	// relative refs against the page, not a leftover scraped base URL.
	html = baseTagRef.ReplaceAllString(html, "")

	absPrefix := "/res/" + dictID + "/"
	relPrefix := "res/" + dictID + "/"

	// rewrite maps one reference to its /res/ URL, or returns it unchanged
	// (ok=false) when it is a fragment, query, protocol-relative URL, a real
	// scheme, or already under /res/{dict}/.
	rewrite := func(ref string) (string, bool) {
		switch {
		case ref == "",
			strings.HasPrefix(ref, "#"),
			strings.HasPrefix(ref, "?"),
			strings.HasPrefix(ref, "//"),
			strings.HasPrefix(ref, absPrefix),
			strings.HasPrefix(ref, relPrefix):
			return ref, false
		case subEntryRef.MatchString(ref):
			// Must precede the scheme case, which would pass it through.
			return subEntryRef.ReplaceAllString(ref, "$1:@"), true
		case schemeRef.MatchString(ref) && !soundOrFile.MatchString(ref):
			return ref, false
		}
		return resURL(dictID, ref), true
	}

	// src / href / data / poster (and prefixed variants).
	html = attrRef.ReplaceAllStringFunc(html, func(m string) string {
		sub := attrRef.FindStringSubmatch(m)
		name := sub[1]
		ref, quote := sub[2], `"`
		if ref == "" {
			ref, quote = sub[3], `'`
		}
		nu, _ := rewrite(ref)
		return name + "=" + quote + nu + quote
	})

	// srcset candidate lists.
	html = srcsetRef.ReplaceAllStringFunc(html, func(m string) string {
		sub := srcsetRef.FindStringSubmatch(m)
		val, quote := sub[1], `"`
		if val == "" {
			val, quote = sub[2], `'`
		}
		name := m[:strings.IndexByte(m, '=')]
		return name + "=" + quote + rewriteSrcset(val, rewrite) + quote
	})

	// url(...) inside inline style="…" attributes.
	html = styleAttrRef.ReplaceAllStringFunc(html, func(m string) string {
		sub := styleAttrRef.FindStringSubmatch(m)
		val, quote := sub[1], `"`
		if sub[1] == "" && sub[2] != "" {
			val, quote = sub[2], `'`
		}
		name := m[:strings.IndexByte(m, '=')]
		return name + "=" + quote + rewriteCSSURLs(val, rewrite) + quote
	})

	// url(...) inside <style>…</style> blocks.
	html = styleBlockRef.ReplaceAllStringFunc(html, func(m string) string {
		sub := styleBlockRef.FindStringSubmatch(m)
		return sub[1] + rewriteCSSURLs(sub[2], rewrite) + sub[3]
	})

	return html
}

// rewriteSrcset rewrites the URL of each "url [descriptor]" candidate in a
// srcset value, leaving descriptors (1x, 480w, …) untouched.
func rewriteSrcset(val string, rewrite func(string) (string, bool)) string {
	parts := strings.Split(val, ",")
	for i, p := range parts {
		fields := strings.Fields(p)
		if len(fields) == 0 {
			continue
		}
		if nu, ok := rewrite(fields[0]); ok {
			fields[0] = nu
		}
		parts[i] = strings.Join(fields, " ")
	}
	return strings.Join(parts, ", ")
}

// rewriteCSSURLs rewrites every url(...) reference in a fragment of CSS.
// It emits the unquoted form (safe for our /res/ URLs and, crucially, free of
// the double quotes that would collide with a surrounding style="…" attribute)
// and falls back to single quotes only if the URL contains awkward characters.
func rewriteCSSURLs(css string, rewrite func(string) (string, bool)) string {
	return cssURLRef.ReplaceAllStringFunc(css, func(u string) string {
		ref := cssURLRef.FindStringSubmatch(u)[1]
		nu, ok := rewrite(strings.TrimSpace(ref))
		if !ok {
			return u
		}
		if strings.ContainsAny(nu, " \t()\"'") {
			return `url('` + strings.ReplaceAll(nu, `'`, "%27") + `')`
		}
		return "url(" + nu + ")"
	})
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
