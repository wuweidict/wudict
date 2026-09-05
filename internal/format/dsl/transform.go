// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Package dsl implements ABBYY Lingvo DSL dictionaries: plain-text
// markup with no native index, so the direct backend transparently
// ingests into a text.db on first open (SPEC §1). Markup semantics are
// ported from pyglossary/plugins/dsl (lex.py, transform.py, title.py).
package dsl

import (
	"path"
	"strings"

	"github.com/wuweidict/wudict/internal/store"
)

// stripComments removes `{{...}}` comments. A comment may itself contain a
// single `}`, so the terminator is the first `}}` rather than "no closing
// brace at all". Escaped braces (`\{\{`) are not a comment: the backslash
// sits between them, so no `{{` exists, and an unterminated `{{` is literal
// text - a real one in an article body is a typo, not a request to delete
// the rest of the entry.
//
// A comment that stood alone on its line takes the line with it. Deleting
// only the braces leaves the line's indentation and its newline behind, and
// run() turns that leftover empty line into a <br/>.

func stripComments(text string) string {
	if !strings.Contains(text, "{{") {
		return text
	}
	out := make([]byte, 0, len(text))
	lineStart := 0 // index in out where the current line begins
	commented := false
	dropLine := func() bool {
		if !commented {
			return false
		}
		for _, c := range out[lineStart:] {
			switch c {
			case ' ', '\t', '\r', '\v', '\f':
			default:
				return false
			}
		}
		return true
	}
	for i := 0; i < len(text); {
		if text[i] == '{' && i+1 < len(text) && text[i+1] == '{' {
			if j := strings.Index(text[i+2:], "}}"); j >= 0 {
				i += 2 + j + len("}}")
				commented = true
				continue
			}
		}
		if text[i] == '\n' {
			if dropLine() {
				out = out[:lineStart] // the line and its newline both go
			} else {
				out = append(out, '\n')
				lineStart = len(out)
			}
			commented = false
			i++
			continue
		}
		out = append(out, text[i])
		i++
	}
	// A comment on the last line has no newline of its own to drop, so it
	// takes the one that ended the line before it; left in place that would
	// render as a trailing <br/>.
	if dropLine() {
		out = out[:lineStart]
		if lineStart > 0 && out[lineStart-1] == '\n' {
			out = out[:lineStart-1]
		}
	}
	return string(out)
}

const exampleColor = "steelblue"

// transformer converts one entry body from DSL markup to HTML.
// State-machine port of pyglossary's Transformer/lexRoot family.
type transformer struct {
	input      string
	pos        int
	out        strings.Builder
	label      strings.Builder
	labelOpen  bool
	currentKey string
	resFiles   []string
	abbrev     *abbrevMap
}

// transformBody renders a whole DSL entry body to HTML. currentKey replaces
// `~`. The surrounding whitespace is the file's own indentation, not content,
// so it is dropped - but only here, at the outer edge of a complete article.
func transformBody(text, currentKey string) (html string, resFiles []string, err error) {
	return transformBodyAbbrev(text, currentKey, nil)
}

// transformBodyAbbrev is transformBody with the dictionary's abbreviation
// glossary in hand, so [p] labels it knows can carry their expansion. A nil map
// is the no-companion case and produces byte-identical output.
func transformBodyAbbrev(text, currentKey string, ab *abbrevMap) (html string, resFiles []string, err error) {
	html, resFiles, err = transformFragmentAbbrev(text, currentKey, ab)
	if err != nil {
		return "", nil, err
	}
	return strings.TrimSpace(html), resFiles, nil
}

// transformFragment is transformBody without the trim, for markup that is
// concatenated with text on either side of it.
func transformFragment(text, currentKey string) (html string, resFiles []string, err error) {
	return transformFragmentAbbrev(text, currentKey, nil)
}

func transformFragmentAbbrev(text, currentKey string, ab *abbrevMap) (html string, resFiles []string, err error) {
	tr := &transformer{
		input:      stripComments(text),
		currentKey: currentKey,
		abbrev:     ab,
	}
	if err := tr.run(); err != nil {
		return "", nil, err
	}
	if tr.labelOpen {
		tr.closeLabel()
	}
	return tr.out.String(), tr.resFiles, nil
}

func (tr *transformer) end() bool  { return tr.pos >= len(tr.input) }
func (tr *transformer) next() byte { c := tr.input[tr.pos]; tr.pos++; return c }

func (tr *transformer) follows(s string) bool {
	return strings.HasPrefix(tr.input[tr.pos:], s)
}

func (tr *transformer) skipAny(chars string) {
	for tr.pos < len(tr.input) && strings.IndexByte(chars, tr.input[tr.pos]) >= 0 {
		tr.pos++
	}
}

func (tr *transformer) addHTML(s string) {
	if tr.labelOpen {
		tr.label.WriteString(s)
	} else {
		tr.out.WriteString(s)
	}
}

func (tr *transformer) addText(s string) { tr.addHTML(escape(s)) }

// addTextByte appends one raw input byte (escaped when HTML-special).
// Bytes of multi-byte UTF-8 sequences pass through untouched - the
// builder reassembles them; converting via string(c) would mangle them.
func (tr *transformer) addTextByte(c byte) {
	switch c {
	case '&':
		tr.addHTML("&amp;")
	case '<':
		tr.addHTML("&lt;")
	case '>':
		tr.addHTML("&gt;")
	default:
		if tr.labelOpen {
			tr.label.WriteByte(c)
		} else {
			tr.out.WriteByte(c)
		}
	}
}

var (
	textEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	attrEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
)

func escape(s string) string { return textEscaper.Replace(s) }

// quoteAttr renders a value as a complete double-quoted attribute. Every
// attribute built from dictionary content must go through this and not
// escape(): a `"` inside a colour name or a media file name would otherwise
// close the attribute and start a new one.
func quoteAttr(s string) string { return `"` + attrEscaper.Replace(s) + `"` }

// closeLabel flushes a [p] label. When the dictionary's abbreviation companion
// knows this label, the whole coloured run is wrapped in an <abbr> carrying the
// expansion: the browser draws its own tooltip, no client code is involved, and
// both the element and the title= survive `-format clean`. The class is for
// styling only and is dropped there, which is why it carries nothing the reader
// needs. With no expansion the bytes are exactly what they always were.
func (tr *transformer) closeLabel() {
	label := tr.label.String()
	tr.out.WriteString(`<i class="p">`)
	if exp, ok := tr.abbrev.lookup(collapseSpace(store.StripHTML(label))); ok {
		tr.out.WriteString(`<abbr class="wudict-abbr" title=` + quoteAttr(exp) + `>`)
		tr.out.WriteString(`<font color="green">` + label + "</font></abbr>")
	} else {
		tr.out.WriteString(`<font color="green">` + label + "</font>")
	}
	tr.out.WriteString("</i>")
	tr.label.Reset()
	tr.labelOpen = false
}

// run is the lexRoot loop.
func (tr *transformer) run() error {
	for !tr.end() {
		c := tr.next()
		switch c {
		case '\\':
			if tr.end() {
				tr.addTextByte(c)
				return nil
			}
			e := tr.next()
			switch {
			case e == ' ':
				tr.addHTML("&nbsp;")
			case (e == '<' || e == '>') && tr.follows(string(e)):
				tr.next()
				tr.addText(string(e) + string(e))
			default:
				tr.addTextByte(e)
			}
		case '[':
			if err := tr.lexTag(); err != nil {
				return err
			}
		case ']':
			// stray close bracket: emitted as-is (pyglossary parity)
			tr.addHTML(string(c))
		case '~':
			tr.addText(tr.currentKey)
		case '\n':
			tr.skipAny(" \t")
			if !tr.follows("[m") {
				tr.addHTML("<br/>")
			}
		case '<':
			if tr.follows("<") {
				tr.next()
				tr.lexRefText(nil)
			} else {
				tr.addTextByte(c)
			}
		default:
			tr.addTextByte(c)
		}
	}
	return nil
}

// lexTag parses "[...]": tag name, optional attributes, then dispatch.
// Malformed tags (unclosed, empty - e.g. a literal "[ ]" in article
// text) degrade to literal text instead of failing the entry.
func (tr *transformer) lexTag() error {
	open := tr.pos - 1 // position of '['
	start := tr.pos
	var attrs map[string]string
	var tag string
	for {
		if tr.end() { // unclosed '[': emit the rest literally
			tr.addText(tr.input[open:])
			return nil
		}
		c := tr.next()
		if c == '[' && tr.pos-1 == start { // "[[" is a literal '['
			tr.addHTML("[")
			return nil
		}
		if c == ' ' || c == '\t' {
			tag = tr.input[start : tr.pos-1]
			tr.skipAny(" \t")
			attrs = tr.lexAttrs()
			break
		}
		if c == ']' {
			tag = tr.input[start : tr.pos-1]
			break
		}
	}
	if strings.TrimSpace(tag) == "" || tag == "/" {
		tr.addText(tr.input[open:tr.pos])
		return nil
	}
	return tr.processTag(tag, attrs)
}

// lexAttrs parses attributes up to (and including) the closing ']'.
// EOF-tolerant: returns what was collected.
func (tr *transformer) lexAttrs() map[string]string {
	attrs := map[string]string{}
	name := ""
	for {
		if tr.end() {
			if name != "" {
				attrs[name] = ""
			}
			return attrs
		}
		c := tr.next()
		if c == ']' {
			if name != "" {
				attrs[name] = ""
			}
			return attrs
		}
		if c == '=' {
			tr.skipAny(" \t")
			attrs[name] = tr.lexAttrValue()
			name = ""
			continue
		}
		if c == ' ' || c == '\t' {
			if name != "" {
				attrs[name] = ""
				name = ""
			}
			tr.skipAny(" \t")
			continue
		}
		name += tr.input[tr.pos-1 : tr.pos]
	}
}

func (tr *transformer) lexAttrValue() string {
	if tr.end() {
		return ""
	}
	c := tr.next()
	quote := byte(0)
	var val strings.Builder
	if c == '\'' || c == '"' {
		quote = c
	} else {
		val.WriteByte(c)
	}
	for {
		if tr.end() {
			return val.String()
		}
		c = tr.next()
		if c == '\\' {
			if tr.end() {
				return val.String()
			}
			val.WriteByte(tr.next())
			continue
		}
		if c == ']' {
			tr.pos--
			return val.String()
		}
		if quote != 0 && c == quote {
			return val.String()
		}
		if quote == 0 && (c == ' ' || c == '\t') {
			return val.String()
		}
		val.WriteByte(c)
	}
}

// isMarginTag reports whether tag is [m] or [m] followed only by digits.
func isMarginTag(tag string) bool {
	if tag == "" || tag[0] != 'm' {
		return false
	}
	for i := 1; i < len(tag); i++ {
		if tag[i] < '0' || tag[i] > '9' {
			return false
		}
	}
	return true
}

func (tr *transformer) processTag(tag string, attrs map[string]string) error {
	if tag[0] == '/' {
		tr.closeTag(tag[1:])
		return nil
	}
	tag = strings.SplitN(tag, " ", 2)[0]

	switch {
	case tag == "ref":
		tr.lexRefText(attrs)
	case tag == "url":
		tr.lexURLText(attrs)
	case tag == "s", tag == "video":
		// [video] is an undocumented exact synonym of [s], accepted by the
		// Lingvo x5 compiler (lingvo-ref "Тэги мультимедиа"): same syntax, same
		// effect, so the same lexer - not a second one that could drift.
		tr.lexTagS()
	case tag == "c":
		color := "green"
		for k, v := range attrs {
			if v == "" {
				color = k
				break
			}
		}
		tr.addHTML(`<font color=` + quoteAttr(color) + `>`)
	case isMarginTag(tag):
		// [m0]..[m9] set the left margin to N spaces; [m0] means 0, and a
		// bare [m] means the default indent. Only digits reach the CSS -
		// anything else would emit a length like "padding-left:arkem".
		padding := "0.3"
		if len(tag) > 1 {
			padding = tag[1:]
		}
		tr.addHTML(`<p style="padding-left:` + padding + `em;margin:0">`)
	case tag == "p":
		tr.labelOpen = true
	case tag == "*":
		tr.addHTML(`<span class="sec">`)
	case tag == "ex":
		tr.addHTML(`<span class="ex"><font color="` + exampleColor + `">`)
	case tag == "t":
		tr.addHTML(`<font face="Helvetica" class="dsl_t">`)
	case tag == "i":
		tr.addHTML("<i>")
	case tag == "b":
		tr.addHTML("<b>")
	case tag == "u":
		tr.addHTML("<u>")
	case tag == "'":
		tr.addHTML(`<u class="accent">`)
	case tag == "sup":
		tr.addHTML("<sup>")
	case tag == "sub":
		tr.addHTML("<sub>")
	case tag == "trn", tag == "!trn", tag == "trs", tag == "!trs",
		tag == "lang", tag == "com", tag == "preview":
		// stripped wrappers. [preview] is legal only INSIDE [s]/[video], where
		// lexTagS consumes it; the compiler accepts it and it has no effect
		// (lingvo-ref "Тэг [preview]···[/preview]"), so a stray one outside a
		// media zone is dropped rather than printed.
	default:
		// unknown tag: dropped, content kept (pyglossary logs a warning)
	}
	return nil
}

func (tr *transformer) closeTag(tag string) {
	// [/m2] is as common in the wild as [/m], and both close the same <p>: the
	// digit belongs to the margin, not to the element. Matching only "m" left
	// the paragraph open, so every following line inherited the indent to the
	// end of the article.
	if isMarginTag(tag) {
		tr.addHTML("</p>")
		return
	}
	switch tag {
	case "b":
		tr.addHTML("</b>")
	case "u", "'":
		tr.addHTML("</u>")
	case "i":
		tr.addHTML("</i>")
	case "sup":
		tr.addHTML("</sup>")
	case "sub":
		tr.addHTML("</sub>")
	case "c", "t":
		tr.addHTML("</font>")
	case "p":
		tr.closeLabel()
	case "*":
		tr.addHTML("</span>")
	case "ex":
		tr.addHTML("</font></span>")
	}
}

// lexRefText handles [ref]...[/ref] and <<...>>: an in-dictionary link.
func (tr *transformer) lexRefText(attrs map[string]string) {
	text := tr.collectRefText(true)
	target := attrs["target"]
	if target == "" {
		target = text
	}
	tr.addHTML("<a href=" + quoteAttr("bword://"+target) + ">" + escape(text) + "</a>")
}

func (tr *transformer) lexURLText(attrs map[string]string) {
	text := tr.collectRefText(false)
	target := attrs["target"]
	if target == "" {
		target = text
	}
	if !strings.Contains(target, "://") {
		target = "http://" + target
	}
	tr.addHTML("<a href=" + quoteAttr(target) + ">" + escape(text) + "</a>")
}

// collectRefText gathers text until '[', '>>' (when doubleAngle) or EOF.
func (tr *transformer) collectRefText(doubleAngle bool) string {
	var b strings.Builder
	for !tr.end() {
		c := tr.next()
		if c == '\\' {
			if tr.end() {
				break
			}
			b.WriteByte(tr.next())
			continue
		}
		if c == '[' {
			tr.pos--
			break
		}
		if doubleAngle && c == '>' && tr.follows(">") {
			tr.next()
			break
		}
		b.WriteByte(c)
	}
	return b.String()
}

// mediaKind is what a browser can do with one [s] payload.
type mediaKind int

const (
	mediaFile mediaKind = iota // the default: hand it over as a link
	mediaAudio
	mediaImage
	mediaVideo
)

// mediaExt classifies the media zone by extension. Lingvo's [s] supports
// images, sound AND video, and an author may
// put anything else in the .files folder besides - a PDF plate, a document.
// Only what a browser can actually render or decode is named here; everything
// else is deliberately absent and becomes a file link.
//
// The formats Lingvo names but a browser cannot handle are the reason this is
// an allowlist rather than a "video/ prefix" test: .avi, .wmv, .flv, .mkv and
// .mpg are Lingvo's and GoldenDict's own video formats, and an inline <video>
// pointed at one is a permanently broken player. As a link they reach the
// system player instead, which is exactly what Lingvo does with them. Same
// for its .pcx, .dcx, .wmf and .emf images, which no browser draws.
var mediaExt = map[string]mediaKind{
	// Sound. Lingvo documents .wav alone; the rest arrive from GoldenDict-era
	// dictionaries, and .spx is transcoded to WAV when served (D18).
	"wav": mediaAudio, "mp3": mediaAudio, "ogg": mediaAudio,
	"spx": mediaAudio, "m4a": mediaAudio,
	// Images.
	"bmp": mediaImage, "gif": mediaImage, "ico": mediaImage,
	"jpeg": mediaImage, "jpg": mediaImage, "png": mediaImage,
	"svg": mediaImage, "tif": mediaImage, "tiff": mediaImage,
	"webp": mediaImage, "avif": mediaImage,
	// Video.
	"mp4": mediaVideo, "webm": mediaVideo, "ogv": mediaVideo,
	"mov": mediaVideo, "m4v": mediaVideo, "3gp": mediaVideo,
}

// lexTagS handles [s]file[/s] (and its [video] synonym): one media file,
// rendered by kind, its name recorded for the resource set.
//
// Every kind renders something. Emitting nothing for an unrecognised extension
// - which is what this did - loses the file silently: the article shows a gap
// where the author put a video or a PDF, and no part of the pipeline
// downstream can recover a reference that was never written.
func (tr *transformer) lexTagS() {
	fname := tr.collectMediaName()
	if fname == "" {
		return
	}
	switch mediaExt[strings.TrimPrefix(strings.ToLower(path.Ext(fname)), ".")] {
	case mediaAudio:
		// A link, not GoldenDict's `<object type="audio/x-wav">`. That spelling
		// is a plugin-era embedding vector: `clean` has to drop it as unsafe
		// and rescue the URL back out (server/articleformat.go audioObject),
		// the shadow-DOM renderer needs a handler that exists for this one
		// element, and the iframe renderer has no such handler at all - so DSL
		// pronunciation was simply dead there. An anchor needs none of it: the
		// server's rewriter already recognises a media href on <a> and points
		// it at /res/, both renderers already play such a link, `clean` keeps
		// it as an ordinary link, and no inline handler is emitted (the
		// sanitiser strips every on* attribute, by design).
		//
		// [s] carries no link text of its own, so the glyph is the affordance.
		tr.addHTML(`<a class="wudict-audio" href=` + quoteAttr(fname) + `>&#128266;</a>`)
	case mediaImage:
		tr.addHTML(`<img align="top" src=` + quoteAttr(fname) + ` alt=` + quoteAttr(fname) + ` />`)
	case mediaVideo:
		// preload="none" is what makes this affordable: a card may carry
		// several videos of tens of megabytes each, and nothing is fetched
		// until the reader presses play. src is a fetch site, so the article
		// rewriter points it at /res/{dict}/ with no special case, and `clean`
		// already keeps <video src|controls> (server/articleformat.go).
		tr.addHTML(`<video class="wudict-video" controls preload="none" src=` + quoteAttr(fname) + `></video>`)
	default:
		// Anything else - a PDF, a document, one of Lingvo's own formats no
		// browser handles. The `file://` pseudo-scheme is the author saying
		// "this names MY file": the rewriter honours it (server/rewrite.go
		// isResourceRef) and rewrites the link to /res/ whatever the
		// extension, so a dictionary's own container is served in full without
		// widening dict.IsAssetName - which is also the allowlist for files
		// lying LOOSE beside an .mdx, a boundary this must not touch.
		//
		// The file name is the link text because it is all there is: [s] has no
		// text of its own, and a bare glyph would not say what it opens.
		tr.addHTML(`<a class="wudict-file" href=` + quoteAttr("file://"+fname) +
			`>&#128196; ` + escape(fname) + `</a>`)
	}
	tr.resFiles = append(tr.resFiles, fname)
}

// collectMediaName reads the file name filling the media zone. Only a bare
// name with an extension is legal there - no path, no nested tags, no spaces
// around it (lingvo-ref) - so the scan stops at the first `[`, and the one tag
// the compiler does accept inside is skipped: [preview] was rejected during
// x5's development and has no effect, but a dictionary written against that
// compiler still contains it, and reading it as part of the name would ask the
// container for a file called "[preview]x.avi".
func (tr *transformer) collectMediaName() string {
	var b strings.Builder
	for !tr.end() {
		if tr.follows("[preview]") {
			tr.pos += len("[preview]")
			continue
		}
		if tr.follows("[/preview]") {
			tr.pos += len("[/preview]")
			continue
		}
		c := tr.next()
		if c == '[' {
			tr.pos--
			break
		}
		b.WriteByte(c)
	}
	return strings.TrimSpace(b.String())
}
