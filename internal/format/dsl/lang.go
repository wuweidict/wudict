// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package dsl

import (
	"strconv"
	"strings"

	"github.com/wuweidict/wudict/internal/lang"
)

// [lang] names its language in one of two ways (lingvo-ref "Тэг
// [lang]···[/lang]"): by Lingvo's English name, [lang name=Russian], or by a
// number, [lang id=1049].
//
// Both reach the reader untranslated, as data-lang: the author's own token is
// the only thing a Custom styles rule can target without ambiguity, and two
// tokens that mean different things to the author - id=1034 and id=3082 are
// Spanish under two collations - must stay two selectors.
//
// lang= is added ON TOP when the token resolves, because it is not a selector:
// it is what picks the font, the hyphenation table and the screen reader's
// voice. An unresolved token simply gets no lang=, which is the correct
// answer for markup nobody can attribute.

// dslLangNames are the spellings where Lingvo's own name table disagrees with
// lang.FromDeclared, either because the name is not English ("Kirgiz") or
// because the suffix names a script and so a different written language.
// Everything else - SpanishModernSort, GermanNewSpelling, PortugueseStandard,
// ChinesePRC - already resolves there by longest-name prefix.
var dslLangNames = map[string]string{
	"kirgiz":           "ky",
	"azerilatin":       "az-Latn",
	"azericyrillic":    "az-Cyrl",
	"uzbeklatin":       "uz-Latn",
	"uzbekcyrillic":    "uz-Cyrl",
	"serbiancyrillic":  "sr-Cyrl",
	"serbianlatin":     "sr-Latn",
	"norwegianbokmal":  "nb",
	"norwegiannynorsk": "nn",
	"chineseprc":       "zh-Hans",
	"armenianwestern":  "hyw",
}

// dslLangIDs are Lingvo's "Old ID" numbering, a 1..30 table predating the
// Windows LCIDs it now prints alongside them. The two namespaces overlap only
// in principle: every LCID in the table is >= 1025, so a small number is an
// old id and a large one is an LCID, and no dictionary has to say which.
var dslLangIDs = map[int]string{
	1: "en", 2: "ru", 3: "de", 4: "fr", 5: "it",
	6: "es", 7: "es", 8: "pt", 9: "eu", 10: "nl",
	11: "fi", 12: "sv", 13: "da", 14: "nb", 15: "af",
	16: "id", 17: "sw", 18: "cs", 19: "hu", 20: "pl",
	21: "be", 22: "bg", 23: "sr-Cyrl", 24: "uk", 25: "nn",
	26: "de", 27: "tr", 28: "zh-Hans", 29: "zh-Hant", 30: "la",
}

// langAttrs renders the attributes of a [lang] zone: data-lang with what the
// author wrote, and lang= with what that means, when it means anything.
func langAttrs(attrs map[string]string) string {
	raw, tag := "", ""
	if v := strings.TrimSpace(attrs["name"]); v != "" {
		raw, tag = v, langFromName(v)
	} else if v := strings.TrimSpace(attrs["id"]); v != "" {
		raw, tag = v, langFromID(v)
	}
	out := ""
	if raw != "" {
		out += " data-lang=" + quoteAttr(raw)
	}
	if tag != "" {
		out += " lang=" + quoteAttr(tag)
	}
	return out
}

func langFromName(v string) string {
	if t, ok := dslLangNames[strings.ToLower(v)]; ok {
		return t
	}
	return lang.FromDeclared(v)
}

func langFromID(v string) string {
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return ""
	}
	if t, ok := dslLangIDs[n]; ok {
		return t
	}
	return lang.FromLCID(n)
}
