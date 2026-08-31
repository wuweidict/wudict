// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"fmt"
	"net/http"
	"strings"

	"golang.org/x/net/html"

	"github.com/wuweidict/wudict/internal/dict"
	"github.com/wuweidict/wudict/internal/htmlref"
)

// Article formats for /api/search (D61). An article body is the dictionary's
// own HTML, and wudict's page renders it with the dictionary's own CSS and
// scripts. A client that is NOT that page - a browser extension showing a
// hover popup, a script, another front-end - wants neither, and pays heavily
// for both. Measured over 748 KB of real articles: `clean` is 1.9x smaller than
// raw and `text` 2.6x, and `clean` removes every stylesheet and script request
// an article would otherwise force - 82 of them for one LDOCE entry. Reducing
// here rather than in every client saves the bytes on the wire instead of after
// it, and means the reduction is written once.
const (
	formatRaw   = "raw"   // verbatim; what the desktop UI needs, and the default
	formatClean = "clean" // structural markup + media, no scripts/styles/presentation
	formatText  = "text"  // no markup at all
)

func parseFormat(s string) (string, error) {
	switch s {
	case "", formatRaw:
		return formatRaw, nil
	case formatClean:
		return formatClean, nil
	case formatText:
		return formatText, nil
	}
	return "", fmt.Errorf("unknown format %q (raw, clean, text)", s)
}

// cleanKeep is what survives `clean`: structure, emphasis and media. Chosen so
// a definition keeps the distinctions that carry meaning - sense lists,
// examples set apart by emphasis, tables of inflections, pronunciation audio -
// while losing everything that only carries a look.
var cleanKeep = map[string]bool{
	"p": true, "div": true, "br": true, "hr": true, "span": true,
	"ul": true, "ol": true, "li": true, "dl": true, "dt": true, "dd": true,
	"table": true, "thead": true, "tbody": true, "tfoot": true,
	"tr": true, "th": true, "td": true, "caption": true,
	"b": true, "i": true, "em": true, "strong": true, "u": true, "s": true,
	"sub": true, "sup": true, "small": true, "code": true, "pre": true,
	"blockquote": true, "abbr": true, "cite": true, "q": true, "mark": true,
	"ruby": true, "rt": true, "rp": true, "wbr": true,
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"a": true, "img": true, "audio": true, "video": true, "source": true,
}

// cleanDrop is removed WITH its content, because that content is not prose.
// Everything not in either map is unwrapped - its tags go, its text stays -
// so an unknown or custom element can never silently swallow a definition.
var cleanDrop = map[string]bool{
	"script": true, "style": true, "link": true, "meta": true, "base": true,
	"title": true, "head": true, "iframe": true, "object": true, "embed": true,
	"applet": true, "form": true, "input": true, "button": true, "select": true,
	"option": true, "textarea": true, "noscript": true, "template": true,
	"svg": true, "math": true, "canvas": true,
}

var cleanGlobalAttr = map[string]bool{
	"title": true, "lang": true, "dir": true, "id": true,
}

// cleanAttrOK is an allowlist per element, not a denylist: an article is
// third-party HTML, and the set of attributes that can carry behaviour is not
// enumerable in advance.
func cleanAttrOK(tag, name string) bool {
	if strings.HasPrefix(name, "on") {
		return false // every event handler, including ones not yet invented
	}
	if cleanGlobalAttr[name] {
		return true
	}
	switch tag {
	case "a":
		return name == "href"
	case "img":
		return name == "src" || name == "alt" || name == "width" || name == "height"
	case "audio", "video":
		return name == "src" || name == "controls" || name == "preload" ||
			name == "width" || name == "height"
	case "source":
		return name == "src" || name == "type"
	case "td", "th":
		return name == "colspan" || name == "rowspan" || name == "headers"
	case "col", "colgroup":
		return name == "span"
	}
	return false
}

// cleanURL decides what a kept href/src may point at, and absolutises what
// only means something on wudict's own origin.
func cleanURL(v, base string) (string, bool) {
	t := strings.TrimSpace(v)
	low := strings.ToLower(t)
	switch {
	case strings.HasPrefix(low, "javascript:"), strings.HasPrefix(low, "vbscript:"):
		return "", false
	case strings.HasPrefix(low, "data:"):
		// An image is inert; a data: document or script is a navigation
		// target that would run in whatever origin the client embedded us in.
		if strings.HasPrefix(low, "data:image/") {
			return t, true
		}
		return "", false
	case strings.HasPrefix(t, "//"):
		return "", false // protocol-relative: resolves against the HOST page
	case strings.HasPrefix(t, "/"):
		// wudict's own root-absolute refs (/res/…, D14). Correct inside our
		// page, meaningless in a client that renders this anywhere else - so
		// the payload is made self-contained here rather than each client
		// being told to rewrite it.
		return base + t, true
	}
	// http(s), the dictionary's own bword:/entry: cross-reference schemes,
	// "#frag", and bare relative headwords all pass through: the client
	// decides what to do with a link, we only decide it cannot execute.
	return t, true
}

// blockTagFor is the element to emit for one the dictionary's CSS displays as
// a block. Only elements that carry no meaning of their own are renamed: a
// <span> is a hook for a class and nothing else, and an unknown element was
// going to be unwrapped anyway. Everything the keep-list names is left alone -
// renaming an <a> to a <div> would trade a boundary for a link, and a <td> for
// its table.
func blockTagFor(tag string) string {
	if tag == "span" || !cleanKeep[tag] {
		return "div"
	}
	return tag
}

func attrValue(attrs []html.Attribute, name string) string {
	for _, a := range attrs {
		if a.Key == name { // the tokenizer lower-cases attribute names
			return a.Val
		}
	}
	return ""
}

// cleanPolicy is the reduction, plus whatever the dictionary's own stylesheet
// says about layout (st, possibly nil - see internal/htmlref/css.go). Without
// it the tag skeleton is all there is to go on, and for a dictionary written as
// nested <span class="…"> that is nothing: every sense, example and collocation
// runs into the next, and content the stylesheet hid becomes visible.
func cleanPolicy(base string, st htmlref.Styles) htmlref.Policy {
	p := htmlref.Policy{
		Tag: func(tag string) htmlref.TagAction {
			switch {
			case cleanDrop[tag]:
				return htmlref.TagDrop
			case cleanKeep[tag]:
				return htmlref.TagKeep
			default:
				return htmlref.TagUnwrap
			}
		},
		Attr: func(tag, name, val string) (string, bool) {
			if !cleanAttrOK(tag, name) {
				return "", false
			}
			if name == "href" || name == "src" {
				return cleanURL(val, base)
			}
			return val, true
		},
		// A <span> that has lost its class is 13 bytes of nothing, and
		// dictionaries are made of them. Not <div>: a bare one still opens a
		// block, and unwrapping it would run separate senses together.
		Bare:    func(tag string) bool { return tag == "span" },
		Replace: audioObject(base),
	}
	// Left nil when there is no stylesheet, so a dictionary without one pays
	// nothing at all for this: the walker never calls a hook it does not have.
	if len(st) > 0 {
		p.Elem = func(act htmlref.TagAction, tag string, attrs []html.Attribute) (htmlref.TagAction, string) {
			class := attrValue(attrs, "class")
			if class == "" {
				return act, tag
			}
			switch st.Class(class) {
			case htmlref.DisplayNone:
				// The dictionary hides this - LDOCE's <span class="FIELD">TT
				// </span> field codes, which surface as "TTTRAVEL" the moment
				// the stylesheet is dropped. Hidden is what the author meant.
				return htmlref.TagDrop, tag
			case htmlref.DisplayBlock:
				return htmlref.TagKeep, blockTagFor(tag)
			}
			return act, tag
		}
	}
	return p
}


func audioObject(base string) func(string, []html.Attribute) string {
	return func(tag string, attrs []html.Attribute) string {
		if tag != "object" {
			return ""
		}
		var typ, data string
		for _, a := range attrs {
			switch strings.ToLower(a.Key) {
			case "type":
				typ = a.Val
			case "data":
				data = a.Val
			}
		}
		if !strings.HasPrefix(strings.ToLower(typ), "audio/") {
			return ""
		}
		u, ok := cleanURL(data, base)
		if !ok || u == "" {
			return ""
		}
		return `<audio src="` + html.EscapeString(u) + `" controls="controls"></audio>`
	}
}

// applyFormat reduces one article body. `raw` returns it untouched and costs
// nothing, which is what keeps the desktop UI's path exactly as it was.
func applyFormat(body, format, base string, st htmlref.Styles) string {
	switch format {
	case formatClean:
		return htmlref.Sanitize(body, cleanPolicy(base, st))
	case formatText:
		return htmlref.Text(body, st)
	}
	return body
}

// ParseArticleFormat validates a `format` name for a caller that is not an
// HTTP request - the CLI. Same three names, same error text, so `wudict lookup
// -format cleen` and `?format=cleen` are wrong in the same words.
func ParseArticleFormat(s string) (string, error) { return parseFormat(s) }

// FormatArticles applies the /api/search reduction (D61) to results looked up
// outside the server, in place. `wudict lookup -format clean` and
// `GET /api/search?format=clean` then produce the same bytes because they are
// the same code - a second implementation of "clean" would diverge on the first
// dictionary that stresses either one.
//
// The pipeline is the handler's, in the handler's order: rewrite the
// dictionary's internal references first so they are named, derive the
// dictionary's own display table from the stylesheets the first article links,
// then reduce. base is prepended to wudict's root-absolute refs (/res/…); ""
// leaves them as they are, which is the honest answer when no server is running
// to serve them.
//
// `raw` returns immediately without rewriting
func FormatArticles(d dict.Dictionary, format, base string, rs []dict.Result) {
	if format == formatRaw || len(rs) == 0 {
		return
	}
	id := pathID(d.Meta().Path)
	for i := range rs {
		rs[i].Body = RewriteEntryHTML(rs[i].Body, id)
	}
	var st htmlref.Styles
	if names := stylesheetNames(rs[0].Body, id); len(names) > 0 {
		st = stylesFrom(d, names)
	}
	for i := range rs {
		rs[i].Body = applyFormat(rs[i].Body, format, base, st)
	}
}

// originOf is the base a client reached us on, so absolutised references point
// back at the address that actually worked for them - not at a hard-coded
// 127.0.0.1 that would be wrong for anyone running wudict on another host.
func originOf(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}
