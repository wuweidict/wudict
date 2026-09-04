// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"html"
	"net/http"
	"strings"

	"github.com/wuweidict/wudict/internal/dict"
	"github.com/wuweidict/wudict/internal/htmlref"
	"github.com/wuweidict/wudict/internal/store"
)

// What a dictionary says about itself. Six formats already parse a description
// out of their header and until now it reached only `wudict info`; DSL ships
// one in a sidecar the dictionary never sees (internal/format/dsl/ann.go). This
// is the one place the two are composed, normalised and handed to a client.
//
// Deliberately NOT a field on dictInfo: /api/dicts is built from store.ReadMeta
// or dict.Probe without opening anything (server.go:616), and an annotation of
// arbitrary size has no business on every row of that list. It is its own lazy
// endpoint, fetched when a reader opens the disclosure - and it opens nothing
// either, which is a property of aboutSourceFor and not an accident of which
// formats happen to have a prober.

type aboutInfo struct {
	ID           string `json:"id"`
	Name         string `json:"name,omitempty"`
	Format       string `json:"format,omitempty"`
	HTML         string `json:"html"`
	Source       string `json:"source,omitempty"`
	IndexLang    string `json:"indexLang,omitempty"`
	ContentsLang string `json:"contentsLang,omitempty"`
}

// aboutSource is what one dictionary declares about itself, gathered without
// opening anything: a prepared library answers from its own meta, a probeable
// format from its header, an already-open one from the backend that is already
// in memory - and anything else answers with its path alone, which is all a
// provider needs (dict.AboutForPath).
type aboutSource struct {
	name, format, desc string
	srcPath            string
	indexLang          string
	contentsLang       string
}

func (s *Server) aboutSourceFor(e *entry) aboutSource {
	if textDB, ok := preparedTextDB(e.Path); ok {
		if m, err := store.ReadMeta(textDB); err == nil {
			src := m["source_path"]
			if src == "" {
				src = e.Path
			}
			return aboutSource{
				name: dict.DisplayText(m["name"]), format: m["format"],
				desc:      dict.DisplayText(m["description"]),
				srcPath:   src,
				indexLang: m["index_lang"], contentsLang: m["contents_lang"],
			}
		}
	}
	if dict.HasProber(e.Path) {
		if m, err := dict.Probe(e.Path); err == nil {
			return aboutSource{
				name: m.Name, format: m.Format, desc: m.Description,
				srcPath: e.Path, indexLang: m.IndexLang, contentsLang: m.ContentsLang,
			}
		}
	}
	// PEEK, never open. This used to call e.open(), which reads as harmless
	// and is not: only .mdx and .ifo register probers, so every DSL, slob and
	// bgl reached this line - and dsl.Open INGESTS on first open. A GET that
	// wanted one paragraph of blurb could therefore run a multi-minute,
	// multi-hundred-megabyte library build inside the request, on a phone,
	// with nothing on screen to say why. An About is a disclosure triangle; it
	// has no business starting work.
	//
	// A dictionary that is already open costs nothing to ask, so it is still
	// asked. One that is not answers from its path, which is what the sidecar
	// provider wanted in the first place (aboutOf).
	e.dMu.RLock()
	d := e.d
	e.dMu.RUnlock()
	if d == nil {
		return aboutSource{srcPath: e.Path}
	}
	m := d.Meta()
	return aboutSource{
		name: m.Name, format: strings.TrimPrefix(m.Format, "wudict:"), desc: m.Description,
		srcPath: e.Path, indexLang: m.IndexLang, contentsLang: m.ContentsLang,
	}
}

// aboutOf composes the About for one dictionary, cheapest first: the live
// sidecar beside the source, else whatever the header put in the description,
// else nothing at all - in which case the disclosure is not rendered. It
// reports false only for that last case.
//
// The sidecar is read LIVE on every request rather than copied into the
// library at ingest, which is the deliberate inverse of the _abrv rule (D97):
// an abbreviation is baked into article bytes and so must make a stale library
// detectable, while an annotation is display text that touches no article and
// must never cost a re-ingest.
func (s *Server) aboutOf(e *entry, base string) (aboutInfo, bool) {
	src := s.aboutSourceFor(e)
	info := aboutInfo{
		ID: e.ID, Name: src.name, Format: src.format,
		IndexLang: src.indexLang, ContentsLang: src.contentsLang,
	}
	// A source that has been moved away or deleted still leaves a working
	// library behind (D2/D9); its sidecar is simply gone, and the description
	// copied at ingest is then all there is.
	if src.srcPath != "" && fileExists(src.srcPath) {
		// With a format name, ask that format's provider; without one - the
		// dictionary is neither prepared, nor probeable, nor already open -
		// ask them all, which is a handful of stats and still no open.
		a, ok := dict.AboutFor(src.format, src.srcPath)
		if !ok && src.format == "" {
			a, ok = dict.AboutForPath(src.srcPath)
		}
		if ok {
			info.HTML, info.Source = renderAbout(a, base), a.Source
			return info, info.HTML != ""
		}
	}
	if strings.TrimSpace(src.desc) != "" {
		// A header description is markup as often as not - MDX and ZIM ship
		// small HTML documents in theirs, StarDict ships plain text, and no
		// format says which. Printing tags at a reader and eating an
		// angle-bracketed word are both wrong; the tag test decides.
		info.HTML = renderAbout(dict.About{
			HTML:     looksLikeMarkup(src.desc),
			Sections: []dict.Section{{Text: src.desc}},
		}, base)
		return info, info.HTML != ""
	}
	return info, false
}

// renderAbout is the ONE normalisation point: everything a client is handed has
// been through here, so there is no client-side sanitiser anywhere to get
// wrong. Plain text is escaped and line-broken; markup is reduced by
// descriptionPolicy.
func renderAbout(a dict.About, base string) string {
	var b strings.Builder
	for _, sec := range a.Sections {
		text := strings.TrimSpace(sec.Text)
		if text == "" {
			continue
		}
		if sec.Lang != "" {
			b.WriteString("<h4>" + html.EscapeString(sec.Lang) + "</h4>")
		}
		// The HTML flag is authoritative, and a provider that says "plain" is
		// believed: an annotation written as prose may legitimately contain
		// "<Someone>" or "<see below>", and a sanitiser would silently eat it
		// as an unknown element. The guessing happens ONE level up, in aboutOf,
		// where a header description of unknown origin is judged by
		// looksLikeMarkup before it gets here.
		if a.HTML {
			b.WriteString(htmlref.Sanitize(text, descriptionPolicy(base)))
			continue
		}
		b.WriteString("<p>")
		for i, line := range strings.Split(text, "\n") {
			if i > 0 {
				b.WriteString("<br>")
			}
			b.WriteString(html.EscapeString(strings.TrimRight(line, " \t")))
		}
		b.WriteString("</p>")
	}
	return b.String()
}

// looksLikeMarkup reports whether s opens a tag anywhere: "<" immediately
// followed by a name, a closing slash or a declaration. Prose comparing two
// numbers ("a < b") does not qualify, and neither does a lone "<".
func looksLikeMarkup(s string) bool {
	for i := strings.IndexByte(s, '<'); i >= 0; {
		if i+1 < len(s) {
			switch c := s[i+1]; {
			case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c == '/', c == '!':
				return true
			}
		}
		j := strings.IndexByte(s[i+1:], '<')
		if j < 0 {
			return false
		}
		i += j + 1
	}
	return false
}

// descriptionPolicy is cleanPolicy's reduction, tightened: an annotation is a
// paragraph of prose in a panel, not an article. Emphasis, paragraphs, lists
// and links survive; images, media, tables, ids and every class do not - the
// panel's own stylesheet has to be able to lay this out, and it cannot if the
// text arrives carrying a dictionary's class names.
func descriptionPolicy(base string) htmlref.Policy {
	keep := map[string]bool{
		"p": true, "br": true, "b": true, "i": true, "em": true, "strong": true,
		"u": true, "s": true, "sub": true, "sup": true, "small": true,
		"ul": true, "ol": true, "li": true, "blockquote": true, "code": true,
		"abbr": true, "cite": true, "q": true, "a": true,
		"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	}
	return htmlref.Policy{
		Tag: func(tag string) htmlref.TagAction {
			switch {
			case cleanDrop[tag]:
				return htmlref.TagDrop
			case tag == "img", tag == "audio", tag == "video", tag == "source":
				// Not a safety call - a resource ref only resolves through
				// /res/<dict>/… and this text is not being served for one
				// dictionary's article context. Dropped rather than unwrapped
				// so no broken image is left behind.
				return htmlref.TagDrop
			case keep[tag]:
				return htmlref.TagKeep
			}
			return htmlref.TagUnwrap // unknown or layout-only: text stays
		},
		Attr: func(tag, name, val string) (string, bool) {
			switch {
			case strings.HasPrefix(name, "on"):
				return "", false
			case name == "title" || name == "lang" || name == "dir":
				return val, true
			case tag == "a" && name == "href":
				return cleanURL(val, base)
			}
			return "", false
		},
	}
}

// handleAbout serves one dictionary's About. Same-origin only - deliberately
// absent from corsAllowed (D69): an annotation names a file on the user's disk,
// which is exactly what the extension grant does not get.
func (s *Server) handleAbout(w http.ResponseWriter, r *http.Request) {
	e, err := s.reg.get(r.URL.Query().Get("dict"))
	if err != nil {
		httpErr(w, 404, "%v", err)
		return
	}
	info, _ := s.aboutOf(e, originOf(r))
	writeJSON(w, info)
}
