// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"io"
	"slices"
	"strings"

	"golang.org/x/net/html"

	"github.com/wuweidict/wudict/internal/dict"
	"github.com/wuweidict/wudict/internal/htmlref"
)

// `clean` and `text` need to know which of a dictionary's classes are blocks
// and which are hidden, and the only place that is written down is the
// dictionary's own stylesheet - a resource of the same dictionary, named by the
// article's own <link>. This reads it once per dictionary and keeps the reduced
// table (class → display, a few hundred entries, 10–25 KB) on the entry.
//
// In memory, never on disk. The table is derived data whose source can change
// under it - re-preparing or repacking a dictionary would silently invalidate a
// persisted copy - and rebuilding it costs about what reading it back would: a
// blob read and one linear pass, ~1–3 ms all told, once. Persisting it would
// mean a fourth artifact in the library folder (D20 fixes three) plus a
// cache-coherence protocol, to save a millisecond.
//
// Its lifetime is the open backend's: entry.drop and entry.reopen forget it, so
// the janitor and the Android trim path shed it along with everything else, and
// a re-prepared dictionary reads its stylesheet again rather than trusting a
// table built from the previous one.

const (
	// A dictionary links one stylesheet, occasionally two. Four is generous
	// and bounds an article that links a hundred of them.
	maxStyleSheets = 4
	// Total CSS read per dictionary. LDOCE's is 35 KB; the largest repacked
	// MDX stylesheets run to a few hundred. 2 MB is the point past which this
	// is no longer a stylesheet and we stop paying for it.
	maxStyleBytes = 2 << 20
)

// articleStyles returns the display table for one dictionary, deriving it from
// the stylesheets body links to on first use. Nil when the dictionary has no
// stylesheet, which every consumer treats as "no information" rather than "no
// classes".
func (s *Server) articleStyles(id, body string) htmlref.Styles {
	e, err := s.reg.get(id)
	if err != nil {
		return nil
	}
	return e.styles(body)
}

func (e *entry) styles(body string) htmlref.Styles {
	// One lock for the whole derivation, not just the map write: two searches
	// hitting a cold dictionary at once should read its stylesheet once. The
	// wait is the same milliseconds the first caller pays anyway.
	e.styleMu.Lock()
	defer e.styleMu.Unlock()
	if e.styleDone {
		return e.style
	}
	// Set before the work, not after: a dictionary whose stylesheet is missing
	// or unreadable must not re-attempt the lookup on every single article.
	e.styleDone = true
	e.style = e.readStyles(body)
	return e.style
}

// forgetStyles drops the table so the next use derives it again. Called
// wherever the backend it was read from is closed or replaced.
func (e *entry) forgetStyles() {
	e.styleMu.Lock()
	e.styleDone, e.style = false, nil
	e.styleMu.Unlock()
}

func (e *entry) readStyles(body string) htmlref.Styles {
	names := stylesheetNames(body, e.ID)
	if len(names) == 0 {
		return nil
	}
	d, err := e.open()
	if err != nil {
		return nil
	}
	if e.reg != nil {
		// open() may have lazily opened a source backend for resources; the
		// janitor has to be told, exactly as handleResource tells it.
		e.reg.nudge()
	}
	return stylesFrom(d, names)
}

// stylesFrom reads the named stylesheets out of an already-open backend. Split
// from the entry method so a caller with no registry - the CLI, which opens one
// dictionary by path - derives the same table from the same code.
func stylesFrom(d dict.Dictionary, names []string) htmlref.Styles {
	budget := maxStyleBytes
	var st htmlref.Styles
	for _, n := range names {
		rc, _, err := d.Resource(n)
		if err != nil {
			continue // a linked stylesheet the dictionary does not bundle
		}
		css, err := io.ReadAll(io.LimitReader(rc, int64(budget)))
		rc.Close()
		if err != nil || len(css) == 0 {
			continue
		}
		budget -= len(css)
		st = htmlref.ParseCSS(string(css), st)
		if budget <= 0 {
			break
		}
	}
	return st
}

// stylesheetNames lists the dictionary resources an article links as
// stylesheets, as resource names. The hrefs have already been rewritten to
// /res/{id}/… by RewriteEntryHTML, which is what makes them identifiable: any
// other href is a stylesheet we do not host and cannot read.
//
// Only the first article of a dictionary is ever scanned. Dictionaries link
// their stylesheet from every entry - it is emitted by the converter, not
// written per article - so scanning more would cost a tokenizer pass per
// request to discover the same one name.
func stylesheetNames(body, id string) []string {
	prefix := "/res/" + id + "/"
	z := html.NewTokenizer(strings.NewReader(body))
	var out []string
	for len(out) < maxStyleSheets {
		tt := z.Next()
		if tt == html.ErrorToken {
			break
		}
		if tt != html.StartTagToken && tt != html.SelfClosingTagToken {
			continue
		}
		t := z.Token()
		if t.Data != "link" {
			continue
		}
		var rel, href string
		for _, a := range t.Attr {
			switch a.Key {
			case "rel":
				rel = a.Val
			case "href":
				href = a.Val
			}
		}
		// rel is a token LIST: `rel="alternate stylesheet"`, and repacked
		// dictionaries spell it in every case there is.
		if !strings.Contains(strings.ToLower(rel), "stylesheet") ||
			!strings.HasPrefix(href, prefix) {
			continue
		}
		name := href[len(prefix):]
		if i := strings.IndexAny(name, "?#"); i >= 0 {
			name = name[:i] // a cache-busting query is not part of the name
		}
		if name == "" || slices.Contains(out, name) {
			continue
		}
		out = append(out, name)
	}
	return out
}
