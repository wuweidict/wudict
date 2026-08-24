// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package htmlref

import (
	"regexp"
	"strings"
)

// Clean repairs a reference that cannot be a URL as written.
//
// Parsing the document correctly is necessary but not sufficient. Given
//
//	<a href=plaintiff__gb_1.ogg">
//
// the tokenizer yields exactly what the browser yields - `plaintiff__gb_1.ogg"`,
// stray quote included - and that value still cannot resolve, because no such
// resource exists under that name.
//
// The repair is not a guess about HTML quoting, which the tokenizer does not
// report. It is a statement about URLs: RFC 3986 excludes `"`, `'` and a
// backtick from every component of a URI; they must be percent-encoded. A raw
// one is therefore malformed wherever it came from. The WHATWG tokenizer
// agrees from the other side - in the attribute-value-(unquoted) state those
// same characters raise "unexpected-character-in-unquoted-attribute-value",
// and the only thing that plausibly produces one is a dropped opening quote.
//
// So: strip leading and trailing whitespace (the URL parser does this too),
// then strip trailing quote characters. Only trailing - a LEADING quote would
// be a different malformation, and removing it could change which file is
// named rather than restore it.
func Clean(ref string) string {
	ref = strings.Trim(ref, " \t\n\r\f\v")
	ref = strings.TrimRight(ref, "\"'`")
	return strings.Trim(ref, " \t\n\r\f\v")
}

// cssURL matches one url(...) reference. This is a VALUE parser, not a markup
// parser: by the time it runs, the tokenizer has already established that the
// text really is CSS - inside a <style> element or a style="…" attribute - so
// a regex here is bounded and appropriate. That distinction is the point of
// this package: parse the document, then parse the leaf values.
var cssURL = regexp.MustCompile(`(?i)url\(\s*(?:"([^"]*)"|'([^']*)'|([^)\s]*))\s*\)`)

// rewriteCSS maps every url(...) reference in a fragment of CSS.
func rewriteCSS(css string, f func(string) string) string {
	if !strings.Contains(css, "url(") && !strings.Contains(css, "URL(") &&
		!strings.Contains(css, "Url(") {
		return css
	}
	return cssURL.ReplaceAllStringFunc(css, func(m string) string {
		sub := cssURL.FindStringSubmatch(m)
		ref := sub[1] + sub[2] + sub[3] // exactly one alternative can be non-empty
		nu := f(Clean(ref))
		if nu == ref {
			return m
		}
		// Unquoted output where possible: a double quote here would collide
		// with a surrounding style="…" attribute. Single quotes only when the
		// URL contains something the unquoted form cannot carry.
		if strings.ContainsAny(nu, " \t()\"'") {
			return `url('` + strings.ReplaceAll(nu, `'`, "%27") + `')`
		}
		return "url(" + nu + ")"
	})
}

// rewriteSrcset maps the URL of each "url [descriptor]" candidate, leaving the
// descriptors (1x, 480w) alone.
//
// Returns the input untouched when no candidate changed. Splitting and
// rejoining would otherwise re-space the whole list - "a.png 1x,  b.png   2x"
// came back with its padding collapsed - and a walk that reformats what it did
// not rewrite cannot promise to preserve the document.
func rewriteSrcset(val string, f func(string) string) string {
	parts := strings.Split(val, ",")
	changed := false
	for i, p := range parts {
		fields := strings.Fields(p)
		if len(fields) == 0 {
			continue
		}
		nu := f(Clean(fields[0]))
		if nu == fields[0] {
			continue
		}
		fields[0] = nu
		parts[i] = strings.Join(fields, " ")
		changed = true
	}
	if !changed {
		return val
	}
	return strings.Join(parts, ", ")
}
