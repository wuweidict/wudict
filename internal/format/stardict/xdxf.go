// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package stardict

import (
	"encoding/xml"
	"fmt"
	"io"
	"strings"

	"github.com/wuweidict/wudict/internal/artmark"
)

// xdxfToHTML converts XDXF article markup (StarDict sametypesequence 'x')
// to HTML. It maps the common tags and drops unknown ones, keeping their
// text content - dictionaries in the wild use loose XDXF, so the parser
// is tolerant. Reference semantics: pyglossary/pyglossary/xdxf.
func xdxfToHTML(src string) string {
	dec := xml.NewDecoder(strings.NewReader("<root>" + src + "</root>"))
	dec.Strict = false
	dec.AutoClose = xml.HTMLAutoClose
	dec.Entity = xml.HTMLEntity

	var b strings.Builder
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			// malformed markup: fall back to escaped source
			return "<p>" + htmlEscape(src) + "</p>"
		}
		switch t := tok.(type) {
		case xml.StartElement:
			b.WriteString(xdxfOpen(t))
		case xml.EndElement:
			b.WriteString(xdxfClose(t.Name.Local))
		case xml.CharData:
			b.WriteString(htmlEscape(string(t)))
		}
	}
	return b.String()
}

func attr(e xml.StartElement, name string) string {
	for _, a := range e.Attr {
		if strings.EqualFold(a.Name.Local, name) {
			return a.Value
		}
	}
	return ""
}

func xdxfOpen(e xml.StartElement) string {
	switch e.Name.Local {
	case "k":
		return `<div class="wu-k">`
	case "b":
		return "<b>"
	case "i":
		return "<i>"
	case "u":
		return "<u>"
	case "sub":
		return "<sub>"
	case "sup":
		return "<sup>"
	case "tt":
		return "<tt>"
	case "br":
		return "<br/>"
	case "c":
		// The colour is the author's parameter, carried as --wd-c for
		// internal/artmark to consume; an unusable value is simply no colour.
		if c := attr(e, "c"); artmark.IsColor(c) {
			return `<span class="wu-c" style="--wd-c:` + c + `">`
		}
		return `<span class="wu-c">`
	case "ex":
		return `<span class="wu-ex">`
	case "co":
		return `<span class="wu-com">(`
	case "abr", "abbr":
		return `<span class="wu-p">`
	case "dtrn":
		return `<span class="wu-trn">`
	case "tr":
		return `<span class="wu-ipa">[`
	case "kref", "iref":
		href := attr(e, "href")
		if href == "" {
			return `<a href="">` // filled by the text content; UI rewrites
		}
		return fmt.Sprintf(`<a href="%s">`, htmlEscape(href))
	case "rref":
		return "" // resource reference: text content is the file name
	case "blockquote", "def":
		return `<div class="wu-def">`
	case "sr", "pos", "gr":
		return `<span class="wu-gr">`
	case "nu", "mrkd":
		return ""
	default:
		return ""
	}
}

func xdxfClose(name string) string {
	switch name {
	case "k":
		return "</div>"
	case "b":
		return "</b>"
	case "i":
		return "</i>"
	case "u":
		return "</u>"
	case "sub":
		return "</sub>"
	case "sup":
		return "</sup>"
	case "tt":
		return "</tt>"
	case "br":
		return ""
	case "c", "ex", "dtrn":
		return "</span>"
	case "co":
		return ")</span>"
	case "abr", "abbr":
		return "</span>"
	case "tr":
		return "]</span>"
	case "kref", "iref":
		return "</a>"
	case "rref":
		return ""
	case "blockquote", "def":
		return "</div>"
	case "sr", "pos", "gr":
		return "</span>"
	default:
		return ""
	}
}
