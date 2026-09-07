// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package lang

// Windows LCIDs. Lingvo DSL writes them in [lang id=1049] (and Babylon BGL
// carries the same numbering), so the mapping belongs here rather than in one
// format package.
//
// An LCID is a primary language in its low ten bits and a sublanguage - the
// country - above them: 1033 (en-US) and 2057 (en-GB) share primary 0x09.
// Country is not language, so the table below is keyed by the PRIMARY id and
// the 187 documented locales collapse to a hundred-odd rows. The handful of
// LCIDs where the sublanguage does change the written language - script for
// Chinese, Serbian, Azeri and Uzbek, and Norwegian's two written standards -
// are listed exactly in lcidExact and consulted first.

// lcidExact are the LCIDs whose sublanguage selects a different written form
// of the same primary language, plus two entries the primary rule cannot
// reach. Values are BCP-47.
var lcidExact = map[int]string{
	// Chinese: script follows the region.
	1028: "zh-Hant", // Taiwan
	3076: "zh-Hant", // Hong Kong SAR
	5124: "zh-Hant", // Macau SAR
	2052: "zh-Hans", // PRC
	4100: "zh-Hans", // Singapore

	// Norwegian: two written standards, not two countries.
	1044: "nb",
	2068: "nn",

	// Serbo-Croatian primary (0x1a): four labels, two scripts.
	1050: "hr",
	5146: "bs",
	2074: "sr-Latn",
	3098: "sr-Cyrl",

	2092: "az-Cyrl",
	1068: "az-Latn",
	2115: "uz-Cyrl",
	1091: "uz-Latn",

	// Primary 0x3c is Scottish Gaelic; the Irish sublanguage is a different
	// language.
	2108: "ga",

	// Western Armenian has its own DSL name and its own 639-3 code; the
	// primary rule would flatten it onto hy.
	32811: "hyw",

	// Kyrgyz. The Lingvo DSL table prints 1595 for Kirgiz, which is not a
	// Kyrgyz LCID at all (its primary is Sami) - but a DSL written from that
	// table says 1595, so it is read as what its author meant.
	1595: "ky",
	1088: "ky",
}

// lcidPrimary maps the low ten bits of an LCID to BCP-47. Codes are ISO 639-1
// where one exists and ISO 639-3 otherwise; this is a wider set than
// languages (tables.go), because labelling an article's language costs
// nothing when wudict cannot lemmatize it.
var lcidPrimary = map[int]string{
	0x01: "ar", 0x02: "bg", 0x03: "ca", 0x04: "zh", 0x05: "cs",
	0x06: "da", 0x07: "de", 0x08: "el", 0x09: "en", 0x0a: "es",
	0x0b: "fi", 0x0c: "fr", 0x0d: "he", 0x0e: "hu", 0x0f: "is",
	0x10: "it", 0x11: "ja", 0x12: "ko", 0x13: "nl", 0x14: "no",
	0x15: "pl", 0x16: "pt", 0x17: "rm", 0x18: "ro", 0x19: "ru",
	0x1a: "hr", 0x1b: "sk", 0x1c: "sq", 0x1d: "sv", 0x1e: "th",
	0x1f: "tr", 0x20: "ur", 0x21: "id", 0x22: "uk", 0x23: "be",
	0x24: "sl", 0x25: "et", 0x26: "lv", 0x27: "lt", 0x28: "tg",
	0x29: "fa", 0x2a: "vi", 0x2b: "hy", 0x2c: "az", 0x2d: "eu",
	0x2e: "hsb", 0x2f: "mk", 0x30: "st", 0x31: "ts", 0x32: "tn",
	0x33: "ve", 0x34: "xh", 0x35: "zu", 0x36: "af", 0x37: "ka",
	0x38: "fo", 0x39: "hi", 0x3a: "mt", 0x3b: "se", 0x3c: "gd",
	0x3d: "yi", 0x3e: "ms", 0x3f: "kk", 0x40: "ky", 0x41: "sw",
	0x42: "tk", 0x43: "uz", 0x44: "tt", 0x45: "bn", 0x46: "pa",
	0x47: "gu", 0x48: "or", 0x49: "ta", 0x4a: "te", 0x4b: "kn",
	0x4c: "ml", 0x4d: "as", 0x4e: "mr", 0x4f: "sa", 0x50: "mn",
	0x51: "bo", 0x52: "cy", 0x53: "km", 0x54: "lo", 0x55: "my",
	0x56: "gl", 0x57: "kok", 0x58: "mni", 0x59: "sd", 0x5a: "syr",
	0x5b: "si", 0x5e: "am", 0x60: "ks", 0x61: "ne", 0x62: "fy",
	0x64: "fil", 0x65: "dv", 0x66: "bin", 0x70: "ig", 0x74: "gn",
	0x76: "la", 0x77: "so", 0x81: "mi",
}

// FromLCID resolves a Windows LCID to a BCP-47 tag suitable for an HTML lang
// attribute: "ru", "en", "zh-Hant". "" when the id names no language.
//
// The result may carry a script subtag, so it is NOT a lemmatizer key; a
// caller that wants one takes the part before the first "-" and passes it
// through Normalize.
func FromLCID(id int) string {
	if id <= 0 {
		return ""
	}
	if t, ok := lcidExact[id]; ok {
		return t
	}
	return lcidPrimary[id&0x3ff]
}
