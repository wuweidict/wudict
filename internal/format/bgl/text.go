// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package bgl

import (
	"bytes"
	"html"
	"regexp"
	"strconv"
	"strings"
)

// Faithful ports of pyglossary babylon_bgl/bgl_text.py text helpers.

var (
	reStripTags    = regexp.MustCompile(`(?:<[/a-zA-Z][^>]*(?:>|$))+`)
	reControlChars = regexp.MustCompile("[\x00-\x08\x0c\x0e-\x1f]")
	reNewlines     = regexp.MustCompile(`[\r\n]+`)
	reEntity       = regexp.MustCompile(`(?i)&#x([0-9a-f]+);?|&#([0-9]+);?|&([a-z][a-z0-9]*);`)
	reAsciiCharRef = regexp.MustCompile(`(?i)&#[0-9a-fx]+;`)
	re4Hex         = regexp.MustCompile(`^[0-9a-fA-F]{4}$`)
)

// stripHTMLTags replaces runs of HTML tags with a single space (used to turn
// an HTML-bearing headword into plain text — Babylon does not render keys).
func stripHTMLTags(s string) string { return reStripTags.ReplaceAllString(s, " ") }

func removeControlChars(s string) string { return reControlChars.ReplaceAllString(s, "") }

func removeNewlines(s string) string { return reNewlines.ReplaceAllString(s, " ") }

func normalizeNewlines(s string) string { return reNewlines.ReplaceAllString(s, "\n") }

// fixImgLinks strips the \x1e / \x1f markers Babylon wraps around img src names.
func fixImgLinks(s string) string {
	return strings.NewReplacer("\x1e", "", "\x1f", "").Replace(s)
}

func escapeXML(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

// replaceHTMLEntities converts &#NN; / &#xHH; / &name; references to their
// characters. Unknown named entities are kept verbatim (Babylon dictionaries
// contain many non-standard ones that it renders literally). When escapeResult
// is true (definitions), a converted character that is <, > or & is re-escaped
// so it can't inject markup; keys pass escapeResult=false.
func replaceHTMLEntities(s string, escapeResult bool) string {
	return reEntity.ReplaceAllStringFunc(s, func(m string) string {
		low := strings.ToLower(m)
		var r rune = -1
		switch {
		case strings.HasPrefix(low, "&#x"):
			if v, err := strconv.ParseInt(strings.TrimRight(m[3:], ";"), 16, 32); err == nil {
				r = rune(v)
			}
		case strings.HasPrefix(m, "&#"):
			if v, err := strconv.ParseInt(strings.TrimRight(m[2:], ";"), 10, 32); err == nil {
				r = rune(v)
			}
		default: // &name;
			u := html.UnescapeString(m)
			if u == m {
				return m // unknown named entity: keep as-is
			}
			if escapeResult {
				return escapeXML(u)
			}
			return u
		}
		if r <= 0 {
			return "�"
		}
		if escapeResult && (r == '<' || r == '>' || r == '&') {
			return escapeXML(string(r))
		}
		return string(r)
	})
}

// replaceAsciiCharRefs replaces &#NNN; / &#xHH; refs whose code is 128..255
// with the raw byte, BEFORE decoding a cp1252 text block (matches pyglossary).
func replaceAsciiCharRefs(b []byte) []byte {
	return reAsciiCharRef.ReplaceAllFunc(b, func(ref []byte) []byte {
		low := bytes.ToLower(ref)
		var code int64 = -1
		var err error
		if bytes.HasPrefix(low, []byte("&#x")) {
			code, err = strconv.ParseInt(string(ref[3:len(ref)-1]), 16, 32)
		} else {
			code, err = strconv.ParseInt(string(ref[2:len(ref)-1]), 10, 32)
		}
		if err != nil || code < 128 || code > 255 {
			return ref
		}
		return []byte{byte(code)}
	})
}

// decodeBabylonRef decodes a <charset c=T> block: ";"-separated 4-hex-digit
// character references.
func decodeBabylonRef(b []byte) string {
	var sb strings.Builder
	for _, part := range bytes.Split(b, []byte(";")) {
		if len(part) == 0 || !re4Hex.Match(part) {
			continue
		}
		v, _ := strconv.ParseInt(string(part), 16, 32)
		sb.WriteRune(rune(v))
	}
	return sb.String()
}

// stripDollarIndexes removes Babylon's `$<index>$` / `$$…` key padding
// sequences. Port of pyglossary stripDollarIndexes (drops the strip count).
func stripDollarIndexes(b []byte) []byte {
	var out []byte
	i := 0
	for {
		d0 := bytes.IndexByte(b[i:], '$')
		if d0 == -1 {
			out = append(out, b[i:]...)
			break
		}
		d0 += i
		d1 := bytes.IndexByte(b[d0+1:], '$')
		if d1 == -1 {
			out = append(out, b[i:]...)
			break
		}
		d1 += d0 + 1

		if d1 == d0+1 { // "$$" — a run of dollar signs
			out = append(out, b[i:d0]...)
			i = d1 + 1
			for i < len(b) && b[i] == '$' {
				i++
			}
			if i >= len(b) {
				break
			}
			continue
		}
		// non-digit between the pair → not an index; keep up to d1
		if bytes.IndexFunc(b[d0+1:d1], func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
			out = append(out, b[i:d1]...)
			i = d1
			continue
		}
		// a real "$<digits>$" index: drop it
		out = append(out, b[i:d0]...)
		i = d1 + 1
	}
	return out
}
