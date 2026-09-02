// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package zim

import (
	"net/url"
	"regexp"
	"strings"

	"github.com/wuweidict/wudict/internal/htmlref"
)

// A ZIM article is a complete HTML DOCUMENT - doctype, <html>, a <head> full
// of stylesheet links, then a page shell around the content. Two transforms
// turn one into a wudict article body. Both belong here rather than in the
// server: converting a format's native markup is the format package's job
// (stardict/xdxf.go, dsl/transform.go), while mapping resource references to
// /res/ is htmlref's, and this deliberately leaves that half alone.

var (
	// linkTag matches a <link> element in the document head. The head of a
	// ZIM article is machine-generated and well-formed, which is the only
	// reason a pattern is enough here; the article BODY is parsed with a real
	// tokenizer below, because that is where malformed markup lives.
	linkTag = regexp.MustCompile(`(?is)<link\b[^>]*>`)

	// absoluteRef matches a reference that already names a scheme
	// ("https:", "mailto:", "data:").
	absoluteRef = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.\-]*:`)
)

// articleHTML renders one stored article as a wudict article body.
func (d *Dict) articleHTML(raw []byte) string {
	return rewriteLinks(articleBody(string(raw)), d.c.newNamespaces())
}

// articleBody extracts the inner HTML of <body>, keeping the head's
// stylesheet <link>s in front of it so the article still renders.
//
// This is 22% smaller than the stored document on a real wiktionary and drops
// per-entry page chrome, but it is not scraper-specific: a document with no
// <body> - which is what devdocs and zimit captures can contain - is returned
// whole rather than emptied.
func articleBody(s string) string {
	lower := strings.ToLower(s)
	i := strings.Index(lower, "<body")
	if i < 0 {
		return s
	}
	j := strings.IndexByte(s[i:], '>')
	if j < 0 {
		return s
	}
	start := i + j + 1
	end := strings.LastIndex(lower, "</body>")
	if end < start {
		end = len(s)
	}

	var styles strings.Builder
	for _, tag := range linkTag.FindAllString(s[:i], -1) {
		if strings.Contains(strings.ToLower(tag), "stylesheet") {
			styles.WriteString(tag)
		}
	}
	return styles.String() + s[start:end]
}

// rewriteLinks turns a ZIM cross-reference into a wudict lookup.
//
// Article anchors are relative, percent-encoded sibling paths -
// `<a rel="mw:WikiLink" href="odrasl%C4%83">`. Nothing downstream can do
// anything useful with that: the server's /res/ rewriter correctly declines to
// touch href on <a> (it is a cross-reference, not a resource), so the link
// would reach the browser as a relative URL and resolve against the page
// origin. `bword://` is the scheme the UI navigates on, so that is what these
// become.
//
// src and <link href> are deliberately NOT touched here - they are real
// resource references and belong to htmlref, which maps them to /res/ exactly
// as it does for every other format.
func rewriteLinks(body string, newNS bool) string {
	if !strings.Contains(body, "href") {
		return body
	}
	return htmlref.Rewriter{
		URL: func(r htmlref.Ref) string {
			if r.Site != htmlref.SiteAttr || r.Tag != "a" || r.Attr != "href" {
				return r.URL
			}
			return bwordRef(r.URL, newNS)
		},
	}.Rewrite(body)
}

// bwordRef maps one relative article path to a bword:// lookup, or returns it
// unchanged when it is not one.
func bwordRef(ref string, newNS bool) string {
	if ref == "" || strings.HasPrefix(ref, "#") || strings.HasPrefix(ref, "//") ||
		absoluteRef.MatchString(ref) {
		return ref
	}
	path, frag := ref, ""
	if i := strings.IndexByte(path, '#'); i >= 0 {
		path, frag = path[:i], path[i:]
	}
	if strings.IndexByte(path, '?') >= 0 {
		return ref // a query is a live-web reference, not a stored entry
	}
	path = trimRelative(path)
	// Pre-6.1 files write cross-references as "../A/Word" and resources as
	// "../I/pic.png", so the namespace segment survives the trim above and
	// has to be dropped - and only the article namespace is a lookup.
	if !newNS && len(path) > 2 && path[1] == '/' {
		if path[0] != 'A' {
			return ref
		}
		path = path[2:]
	}
	if path == "" || strings.Contains(path, "/") {
		return ref // a sub-path is a resource or a capture URL, not a headword
	}
	if u, err := url.PathUnescape(path); err == nil {
		path = u
	}
	// Paths spell a space as '_'; the headword a user (or another dictionary)
	// would recognise does not.
	path = strings.ReplaceAll(path, "_", " ")
	return "bword://" + path + frag
}

// trimRelative strips the leading "./", "../" and "/" segments a stored
// article uses to point at a sibling.
func trimRelative(p string) string {
	for {
		switch {
		case strings.HasPrefix(p, "./"):
			p = p[2:]
		case strings.HasPrefix(p, "../"):
			p = p[3:]
		case strings.HasPrefix(p, "/"):
			p = p[1:]
		default:
			return p
		}
	}
}
