// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package bgl

import (
	"strings"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/korean"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"
	"golang.org/x/text/transform"
)

// charsetByCode maps a Babylon charset code (block type 3, codes 0x1A/0x1B, and
// block type 0 code 8) to a Windows code page name. Ported from pyglossary
// babylon_bgl/bgl_charset.py.
var charsetByCode = map[byte]string{
	0x41: "cp1252", // Default / Latin
	0x42: "cp1252", // Latin
	0x43: "cp1250", // Eastern European
	0x44: "cp1251", // Cyrillic
	0x45: "cp932",  // Japanese
	0x46: "cp950",  // Traditional Chinese
	0x47: "cp936",  // Simplified Chinese
	0x48: "cp1257", // Baltic
	0x49: "cp1253", // Greek
	0x4A: "cp949",  // Korean
	0x4B: "cp1254", // Turkish
	0x4C: "cp1255", // Hebrew
	0x4D: "cp1256", // Arabic
	0x4E: "cp874",  // Thai
}

// language is one entry of the positional language table (index = code).
type language struct {
	name     string
	encoding string
}

// languageByCode is indexed by the Babylon language code (block type 3, codes
// 0x07 source / 0x08 target). Ported from pyglossary babylon_bgl/bgl_language.py.
var languageByCode = []language{
	{"English", "cp1252"}, {"French", "cp1252"}, {"Italian", "cp1252"},
	{"Spanish", "cp1252"}, {"Dutch", "cp1252"}, {"Portuguese", "cp1252"},
	{"German", "cp1252"}, {"Russian", "cp1251"}, {"Japanese", "cp932"},
	{"Chinese", "cp950"}, {"Chinese", "cp936"}, {"Greek", "cp1253"},
	{"Korean", "cp949"}, {"Turkish", "cp1254"}, {"Hebrew", "cp1255"},
	{"Arabic", "cp1256"}, {"Thai", "cp874"}, {"Other", "cp1252"},
	{"Chinese", "cp936"}, {"Chinese", "cp950"},
	{"Other Eastern-European languages", "cp1250"},
	{"Other Western-European languages", "cp1252"},
	{"Other Russian languages", "cp1251"},
	{"Other Japanese languages", "cp932"},
	{"Other Baltic languages", "cp1257"},
	{"Other Greek languages", "cp1253"},
	{"Other Korean dialects", "cp949"},
	{"Other Turkish dialects", "cp1254"},
	{"Other Thai dialects", "cp874"},
	{"Polish", "cp1250"}, {"Hungarian", "cp1250"}, {"Czech", "cp1250"},
	{"Lithuanian", "cp1257"}, {"Latvian", "cp1257"}, {"Catalan", "cp1252"},
	{"Croatian", "cp1250"}, {"Serbian", "cp1250"}, {"Slovak", "cp1250"},
	{"Albanian", "cp1252"}, {"Urdu", "cp1256"}, {"Slovenian", "cp1250"},
	{"Estonian", "cp1252"}, {"Bulgarian", "cp1250"}, {"Danish", "cp1252"},
	{"Finnish", "cp1252"}, {"Icelandic", "cp1252"}, {"Norwegian", "cp1252"},
	{"Romanian", "cp1252"}, {"Swedish", "cp1252"}, {"Ukrainian", "cp1251"},
	{"Belarusian", "cp1251"}, {"Persian", "cp1256"}, {"Basque", "cp1252"},
	{"Macedonian", "cp1250"}, {"Afrikaans", "cp1252"}, {"Faroese", "cp1252"},
	{"Latin", "cp1252"}, {"Esperanto", "cp1254"}, {"Tamazight", "cp1252"},
	{"Armenian", "cp1252"}, {"Hindi", "cp1252"}, {"Somali", "cp1252"},
}

// partOfSpeechByCode maps a Babylon definition field 0x02 code to a label.
// Ported from pyglossary babylon_bgl/bgl_pos.py.
var partOfSpeechByCode = map[byte]string{
	0x30: "noun", 0x31: "adjective", 0x32: "verb", 0x33: "adverb",
	0x34: "interjection", 0x35: "pronoun", 0x36: "preposition",
	0x37: "conjunction", 0x38: "suffix", 0x39: "prefix", 0x3A: "article",
	0x3B: "", 0x3C: "abbreviation",
	0x3D: "masculine noun and adjective",
	0x3E: "feminine noun and adjective",
	0x3F: "masculine and feminine noun and adjective",
	0x40: "feminine noun", 0x41: "masculine and feminine noun",
	0x42: "masculine noun", 0x43: "numeral", 0x44: "participle",
}

// encodingByName resolves a code-page name to an x/text Encoding. utf-8 is
// handled by the caller (decodeBytes); anything unknown falls back to cp1252.
func encodingByName(name string) encoding.Encoding {
	switch strings.ToLower(name) {
	case "cp1250":
		return charmap.Windows1250
	case "cp1251":
		return charmap.Windows1251
	case "cp1252":
		return charmap.Windows1252
	case "cp1253":
		return charmap.Windows1253
	case "cp1254":
		return charmap.Windows1254
	case "cp1255":
		return charmap.Windows1255
	case "cp1256":
		return charmap.Windows1256
	case "cp1257":
		return charmap.Windows1257
	case "cp1258":
		return charmap.Windows1258
	case "cp874":
		return charmap.Windows874
	case "cp932":
		return japanese.ShiftJIS
	case "cp936", "gbk", "gb18030":
		return simplifiedchinese.GBK
	case "cp950":
		return traditionalchinese.Big5
	case "cp949":
		return korean.EUCKR
	}
	return charmap.Windows1252
}

// decodeBytes converts bytes in the named encoding to a UTF-8 string,
// tolerating invalid input (replacing offending bytes) so one bad byte never
// aborts a whole article - mirroring pyglossary's "ignore"/"replace" decoding.
func decodeBytes(name string, b []byte) string {
	switch strings.ToLower(name) {
	case "", "utf-8", "utf8":
		return strings.ToValidUTF8(string(b), "�")
	}
	dec := encodingByName(name).NewDecoder()
	var sb strings.Builder
	for len(b) > 0 {
		out, n, err := transform.Bytes(dec, b)
		sb.Write(out)
		if err == nil {
			break
		}
		// skip the offending byte, mark it, and continue
		sb.WriteRune('�')
		if n < len(b) {
			b = b[n+1:]
		} else {
			b = nil
		}
		dec.Reset()
	}
	return sb.String()
}
