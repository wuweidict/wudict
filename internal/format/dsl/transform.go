// Package dsl implements ABBYY Lingvo DSL dictionaries: plain-text
// markup with no native index, so the direct backend transparently
// ingests into a text.db on first open (SPEC §1). Markup semantics are
// ported from pyglossary/plugins/dsl (lex.py, transform.py, title.py).
package dsl

import (
	"path"
	"regexp"
	"strings"
)

var reCommentBlock = regexp.MustCompile(`\{\{[^}]*\}\}`)

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
}

// transformBody renders DSL body markup to HTML. currentKey replaces `~`.
func transformBody(text, currentKey string) (html string, resFiles []string, err error) {
	tr := &transformer{
		input:      reCommentBlock.ReplaceAllString(text, ""),
		currentKey: currentKey,
	}
	if err := tr.run(); err != nil {
		return "", nil, err
	}
	if tr.labelOpen {
		tr.closeLabel()
	}
	return strings.TrimSpace(tr.out.String()), tr.resFiles, nil
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
// Bytes of multi-byte UTF-8 sequences pass through untouched — the
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

func escape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return r.Replace(s)
}

func quoteAttr(s string) string {
	return `"` + strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;").Replace(s) + `"`
}

func (tr *transformer) closeLabel() {
	tr.out.WriteString(`<i class="p"><font color="green">` + tr.label.String() + "</font></i>")
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
// Malformed tags (unclosed, empty — e.g. a literal "[ ]" in article
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
	case tag == "s":
		tr.lexTagS()
	case tag == "c":
		color := "green"
		for k, v := range attrs {
			if v == "" {
				color = k
				break
			}
		}
		tr.addHTML(`<font color="` + escape(color) + `">`)
	case tag[0] == 'm':
		padding := "0.3"
		if len(tag) > 1 && tag[1:] != "0" {
			padding = tag[1:]
		}
		tr.addHTML(`<p style="padding-left:` + escape(padding) + `em;margin:0">`)
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
		tag == "lang", tag == "com":
		// stripped wrappers
	default:
		// unknown tag: dropped, content kept (pyglossary logs a warning)
	}
	return nil
}

func (tr *transformer) closeTag(tag string) {
	switch tag {
	case "m":
		tr.addHTML("</p>")
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

// lexTagS handles [s]file[/s]: audio object or inline image; the file
// name is recorded for the resource set.
func (tr *transformer) lexTagS() {
	var b strings.Builder
	for !tr.end() {
		c := tr.next()
		if c == '[' {
			tr.pos--
			break
		}
		b.WriteByte(c)
	}
	fname := b.String()
	ext := strings.TrimPrefix(strings.ToLower(path.Ext(fname)), ".")
	switch ext {
	case "wav", "mp3", "ogg", "spx":
		tr.addHTML(`<object type="audio/x-wav" data="` + escape(fname) +
			`" width="40" height="40"><param name="autoplay" value="false" /></object>`)
	case "bmp", "gif", "ico", "jpeg", "jpg", "png", "svg", "tif", "tiff", "webp":
		tr.addHTML(`<img align="top" src="` + escape(fname) + `" alt="` + escape(fname) + `" />`)
	}
	tr.resFiles = append(tr.resFiles, fname)
}
