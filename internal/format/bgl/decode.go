// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package bgl

import (
	"regexp"
	"strings"
)

// Defaults mirror pyglossary Reader options (we do not expose them as config).
const (
	partOfSpeechColor  = "007000"
	processHTMLInKey   = true
	noControlSeqInDefi = false
)

var (
	reCharset       = regexp.MustCompile(`(?i)<charset\s+c=['"]?(\w)['"]?>|</charset>`)
	reStripSlashAlt = regexp.MustCompile(`(^|\s)/(\w)`)
	ltrimCutset     = " \t\r\n\f\v"
)

// defiFields holds the parts split out of one definition (main body + the
// trailing field set introduced by a 0x14 marker).
type defiFields struct {
	bDefi        []byte
	partOfSpeech string
	bTitle       []byte
	bTitleTrans  []byte
	bTrans50     []byte
	codeTrans50  byte
	bTrans60     []byte
	codeTrans60  byte
}

// decodeTextBlock decodes one homogeneous text run in the given encoding.
func (r *Reader) decodeTextBlock(enc string, b []byte) string {
	if enc == "babylon-reference" {
		return decodeBabylonRef(b)
	}
	if enc == "cp1252" {
		b = replaceAsciiCharRefs(b)
	}
	return decodeBytes(enc, b)
}

// decodeCharsetTags decodes HTML text honoring embedded <charset c=X>…</charset>
// tags (each switches the encoding of the enclosed run). Returns the decoded
// text and whether only the default encoding was used. Port of
// reader_charset.decodeCharsetTags.
func (r *Reader) decodeCharsetTags(bText []byte, defaultEncoding string) (string, bool) {
	cur := func(stack []string) string {
		if len(stack) > 0 {
			return stack[len(stack)-1]
		}
		return defaultEncoding
	}
	var sb strings.Builder
	var stack []string
	defaultOnly := true
	pos := 0
	for _, m := range reCharset.FindAllSubmatchIndex(bText, -1) {
		if m[0] > pos {
			enc := cur(stack)
			sb.WriteString(r.decodeTextBlock(enc, bText[pos:m[0]]))
			if enc != defaultEncoding {
				defaultOnly = false
			}
		}
		if bText[m[0]+1] == '/' { // </charset>
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		} else { // <charset c=X>
			c := byte(0)
			if m[2] >= 0 {
				c = lowerByte(bText[m[2]])
			}
			switch c {
			case 't':
				stack = append(stack, "babylon-reference")
			case 'u':
				stack = append(stack, "utf-8")
			case 'k', 'e':
				stack = append(stack, r.sourceEncoding)
			case 'g':
				stack = append(stack, "gbk")
			default:
				stack = append(stack, defaultEncoding)
			}
		}
		pos = m[1]
	}
	if pos < len(bText) {
		enc := cur(stack)
		sb.WriteString(r.decodeTextBlock(enc, bText[pos:]))
		if enc != defaultEncoding {
			defaultOnly = false
		}
	}
	return sb.String(), defaultOnly
}

func lowerByte(b byte) byte {
	if b >= 'A' && b <= 'Z' {
		return b + 32
	}
	return b
}

// processKey decodes an entry headword to plain text (Babylon renders keys as
// literal text, so embedded HTML tags are stripped). Port of processKey.
func (r *Reader) processKey(bWord []byte) string {
	bWord = stripDollarIndexes(bWord)
	u := decodeBytes(r.sourceEncoding, bWord)
	if processHTMLInKey {
		u = replaceHTMLEntities(u, false)
		u = stripHTMLTags(u)
	}
	u = removeControlChars(u)
	u = removeNewlines(u)
	return strings.TrimSpace(u)
}

// processAlternativeKey is processKey for alternates, plus stripping a "/"
// that Babylon puts before words. Port of processAlternativeKey.
func (r *Reader) processAlternativeKey(bWord []byte) string {
	bWord = stripDollarIndexes(bWord)
	u := decodeBytes(r.sourceEncoding, bWord)
	u = reStripSlashAlt.ReplaceAllString(u, "$1$2")
	if processHTMLInKey {
		u = stripHTMLTags(u)
		u = replaceHTMLEntities(u, false)
	}
	u = removeControlChars(u)
	u = removeNewlines(u)
	return strings.TrimLeft(u, ltrimCutset)
}

// plainTitle renders a definition's title field (0x18) as literal text, the
// way processKey renders a key - Babylon draws a headword as text, not markup.
// Empty when the entry carries no title.
func (r *Reader) plainTitle(bTitle []byte) string {
	if len(bTitle) == 0 {
		return ""
	}
	s, _ := r.decodeCharsetTags(bTitle, r.sourceEncoding)
	s = replaceHTMLEntities(s, false)
	s = stripHTMLTags(s)
	s = removeControlChars(s)
	s = removeNewlines(s)
	return strings.TrimSpace(s)
}

// renderDefi turns a definition's collected fields into HTML, prepending the
// part of speech, title and transcriptions. Port of reader_defi.processDefi
// (the collect step is split out so a caller that also wants the title -
// decodeEntry - pays for the field scan once).
func (r *Reader) renderDefi(f defiFields) string {
	uDefi, _ := r.decodeCharsetTags(f.bDefi, r.targetEncoding)
	uDefi = fixImgLinks(uDefi)
	uDefi = replaceHTMLEntities(uDefi, true)
	uDefi = removeControlChars(uDefi)
	uDefi = normalizeNewlines(uDefi)
	uDefi = strings.TrimSpace(uDefi)

	decodeField := func(b []byte) string {
		s, _ := r.decodeCharsetTags(b, r.sourceEncoding)
		return removeControlChars(replaceHTMLEntities(s, true))
	}
	var uTitle, uTitleTrans, uTrans50, uTrans60 string
	if len(f.bTitle) > 0 {
		uTitle = decodeField(f.bTitle)
	}
	if len(f.bTitleTrans) > 0 {
		uTitleTrans = decodeField(f.bTitleTrans)
	}
	if len(f.bTrans50) > 0 && f.codeTrans50 == 0x1B {
		uTrans50 = decodeField(f.bTrans50)
	}
	if len(f.bTrans60) > 0 && f.codeTrans60 == 0x1B {
		uTrans60 = decodeField(f.bTrans60)
	}

	var sb strings.Builder
	if f.partOfSpeech != "" || uTitle != "" {
		if f.partOfSpeech != "" {
			sb.WriteString(`<font color="#` + partOfSpeechColor + `">`)
			sb.WriteString(escapeXML(f.partOfSpeech))
			sb.WriteString(`</font>`)
		}
		if uTitle != "" {
			if sb.Len() > 0 {
				sb.WriteString(" ")
			}
			sb.WriteString(uTitle)
		}
		sb.WriteString("<br>\n")
	}
	if uTitleTrans != "" {
		sb.WriteString(uTitleTrans + "<br>\n")
	}
	if uTrans50 != "" {
		sb.WriteString("[" + uTrans50 + "]<br>\n")
	}
	if uTrans60 != "" {
		sb.WriteString("[" + uTrans60 + "]<br>\n")
	}
	sb.WriteString(uDefi)

	out := sb.String()
	out = strings.TrimSuffix(out, "<br>")
	out = strings.TrimSuffix(out, "<BR>")
	return out
}

// findDefiFieldsStart locates the 0x14 that begins the definition's trailing
// field set, skipping a 0x14 that is immediately followed by a space (those
// occur inside article text). Port of findDefiFieldsStart.
func (r *Reader) findDefiFieldsStart(b []byte) int {
	if noControlSeqInDefi {
		return -1
	}
	index := -1
	for {
		found := -1
		for j := index + 1; j < len(b)-1; j++ {
			if b[j] == 0x14 {
				found = j
				break
			}
		}
		if found == -1 {
			return -1
		}
		if b[found+1] != 0x20 {
			return found
		}
		index = found
	}
}

// collectDefiFields splits a definition into its main body and trailing fields.
// Port of reader_defi.collectDefiFields (unknown/rare fields are parsed only to
// advance the cursor correctly). It returns as soon as it hits a malformed or
// unknown control char, keeping whatever was collected.
func (r *Reader) collectDefiFields(bDefi []byte) defiFields {
	f := defiFields{}
	d0 := r.findDefiFieldsStart(bDefi)
	if d0 == -1 {
		f.bDefi = bDefi
		return f
	}
	f.bDefi = bDefi[:d0]
	i := d0 + 1
	for i < len(bDefi) {
		switch c := bDefi[i]; {
		case c == 0x02: // part of speech
			if i+1 >= len(bDefi) {
				return f
			}
			if p, ok := partOfSpeechByCode[bDefi[i+1]]; ok {
				f.partOfSpeech = p
			} else {
				return f
			}
			i += 2
		case c == 0x06: // one byte
			if i+1 >= len(bDefi) {
				return f
			}
			i += 2
		case c == 0x07: // two bytes
			if i+3 > len(bDefi) {
				return f
			}
			i += 3
		case c == 0x13: // length-prefixed
			if i+1 >= len(bDefi) {
				return f
			}
			n := int(bDefi[i+1])
			i += 2
			if n == 0 {
				continue
			}
			if i+n > len(bDefi) {
				return f
			}
			i += n
		case c == 0x18: // title
			if i+1 >= len(bDefi) {
				return f
			}
			i++
			n := int(bDefi[i])
			i++
			if n == 0 {
				continue
			}
			if i+n > len(bDefi) {
				return f
			}
			f.bTitle = bDefi[i : i+n]
			i += n
		case c == 0x1A: // length-prefixed
			if i+1 >= len(bDefi) {
				return f
			}
			n := int(bDefi[i+1])
			i += 2
			if n == 0 {
				continue
			}
			if i+n > len(bDefi) {
				return f
			}
			i += n
		case c == 0x28: // title with transcription
			if i+2 >= len(bDefi) {
				return f
			}
			i++
			n := uintBE(bDefi[i : i+2])
			i += 2
			if n == 0 {
				continue
			}
			if i+n > len(bDefi) {
				return f
			}
			f.bTitleTrans = bDefi[i : i+n]
			i += n
		case c == 0x50: // transcription 50
			if i+2 >= len(bDefi) {
				return f
			}
			f.codeTrans50 = bDefi[i+1]
			n := int(bDefi[i+2])
			i += 3
			if n == 0 {
				continue
			}
			if i+n > len(bDefi) {
				return f
			}
			f.bTrans50 = bDefi[i : i+n]
			i += n
		case c == 0x60: // transcription 60
			if i+4 > len(bDefi) {
				return f
			}
			f.codeTrans60 = bDefi[i+1]
			i += 2
			n := uintBE(bDefi[i : i+2])
			i += 2
			if n == 0 {
				continue
			}
			if i+n > len(bDefi) {
				return f
			}
			f.bTrans60 = bDefi[i : i+n]
			i += n
		case c >= 0x40 && c <= 0x4F: // unknown field: <one byte><text>
			n := int(c) - 0x3F
			if i+2+n > len(bDefi) {
				return f
			}
			i += 2 + n
		default:
			return f // unknown control char
		}
	}
	return f
}
