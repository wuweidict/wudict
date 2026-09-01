// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package lang

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// FromTitle reads the language out of a dictionary's own TITLE, the third and
// weakest naming source after the declared field and the path. A title is the
// one label almost every dictionary carries and almost none of them omit the
// language from: "Hagen's Comprehensive paradigm (Ru-Ru)", "Larousse Compact
// English-Spanish", "Dahl's Russian Dictionary".
//
// Two things are looked for, and the one that appears FIRST in the string wins,
// because a title states its own side before the other one - "Spanish-English"
// indexes Spanish, "English-Spanish" indexes English, and the pair is written
// in that order by universal convention:
//
//  1. an English language NAME as a whole word ("Russian", "Latin", "English");
//  2. a language CODE, but only where the title marks it as one - a pair
//     ("Ru-Ru", "rus-rus", "En-Ru") or a parenthesis holding nothing else
//     ("(ru)").
//
// A bare two- or three-letter token elsewhere in a title is NOT read as a code,
// and that restriction is the whole reason this function is safe to apply to
// free text: "is", "it", "no", "be", "am", "he", "la", "id", "ta", "pa", "ne",
// "ms", "ka", "ha" and "my" are all ISO 639-1 codes AND ordinary English words,
// so "A la carte" would be Latin and "What it is" Italian. Inside a pair or a
// parenthesis the token is deliberate; loose in a sentence it is a coincidence.
//
// Only when neither fires does SCRIPT decide, and only for Russian: a title
// with more Cyrillic letters than Latin ones ("Брокгауз и Ефрон") is Russian.
// It is last on purpose - "Apresyan (En-Ru)" is an English index whose title is
// half Cyrillic in other editions, and the code pair must get there first.
//
// Returns "" when the title says nothing, which is the common case and a real
// answer.
func FromTitle(title string) string {
	if strings.TrimSpace(title) == "" {
		return ""
	}
	code, pos := titleName(title)
	if c, p := titleCode(title); c != "" && (code == "" || p < pos) {
		code = c
	}
	if code != "" {
		return code
	}
	return cyrillicMajority(title)
}

// titleName finds the earliest whole-word English language name. namesDesc is
// longest-first, so at one position "Norwegian Bokmal" is preferred over
// "Norwegian" - a strict < keeps the first (longest) name found at a tie.
func titleName(title string) (string, int) {
	low := strings.ToLower(title)
	best, pos := "", -1
	for _, n := range namesDesc {
		from := 0
		for {
			i := strings.Index(low[from:], n)
			if i < 0 {
				break
			}
			i += from
			if wholeWord(low, i, i+len(n)) {
				if pos < 0 || i < pos {
					best, pos = nameToISO[n], i
				}
				break // later occurrences of the same name cannot be earlier
			}
			from = i + 1
		}
	}
	return best, pos
}

// titleCode finds the earliest code that the title itself marks as a code.
func titleCode(title string) (string, int) {
	toks := letterTokens(title)
	for i, t := range toks {
		c := asCode(t.s)
		if c == "" {
			continue
		}
		// A pair: "Ru-Ru", "En-Ru", "rus-rus". BOTH sides must be codes, so
		// "co-operative" and "de-facto" are not language attributions.
		if i+1 < len(toks) && isPairJoin(title[t.end:toks[i+1].start]) && asCode(toks[i+1].s) != "" {
			return c, t.start
		}
		// A parenthesis holding this token and nothing else: "(ru)".
		if soleInParens(title, t) {
			return c, t.start
		}
	}
	return "", -1
}

// asCode accepts only what a language code looks like - two or three ASCII
// letters - so a full word is left to titleName, where it needs no marking.
func asCode(s string) string {
	if n := len(s); n != 2 && n != 3 {
		return ""
	}
	for i := 0; i < len(s); i++ {
		if c := s[i]; c < 'a' || c > 'z' {
			if c < 'A' || c > 'Z' {
				return ""
			}
		}
	}
	return Normalize(s)
}

// isPairJoin is the separator between the two halves of a pair: one hyphen,
// dash or underscore, optionally spaced ("En - Ru" is still a pair, "En Ru" is
// two words that happen to sit together).
func isPairJoin(sep string) bool {
	s := strings.TrimSpace(sep)
	if utf8.RuneCountInString(s) != 1 {
		return false
	}
	r, _ := utf8.DecodeRuneInString(s)
	return r == '-' || r == '_' || r == '‐' || r == '‑' ||
		r == '‒' || r == '–' || r == '—' || r == '−'
}

// soleInParens reports whether tok is the entire content of a bracketed group,
// which is the other place a bare code is unambiguous. Square and curly
// brackets count: "[ru]" is the same statement written differently.
func soleInParens(title string, tok token) bool {
	open := strings.LastIndexAny(title[:tok.start], "([{")
	if open < 0 || strings.TrimSpace(title[open+1:tok.start]) != "" {
		return false
	}
	rest := title[tok.end:]
	close := strings.IndexAny(rest, ")]}")
	return close >= 0 && strings.TrimSpace(rest[:close]) == ""
}

type token struct {
	s          string
	start, end int // byte offsets into the original title
}

// letterTokens splits a title into runs of letters. Digits and punctuation are
// separators: a code is a word, and "18th" is not one.
func letterTokens(s string) []token {
	var out []token
	start := -1
	for i, r := range s {
		if unicode.IsLetter(r) {
			if start < 0 {
				start = i
			}
			continue
		}
		if start >= 0 {
			out = append(out, token{s[start:i], start, i})
			start = -1
		}
	}
	if start >= 0 {
		out = append(out, token{s[start:], start, len(s)})
	}
	return out
}

// wholeWord guards a substring match against the letters around it, so
// "Slovenian" is not found inside "Slovenians" and "no" is not found inside
// "notes". Digits are not letters, so "English2" still matches - a version
// number does not change the language.
func wholeWord(s string, start, end int) bool {
	if start > 0 {
		r, _ := utf8.DecodeLastRuneInString(s[:start])
		if unicode.IsLetter(r) {
			return false
		}
	}
	if end < len(s) {
		r, _ := utf8.DecodeRuneInString(s[end:])
		if unicode.IsLetter(r) {
			return false
		}
	}
	return true
}

// cyrillicMajority is the last resort and the only content-based rule in this
// package: a title written in Cyrillic is a Russian dictionary's title.
//
// Russian only. Ukrainian, Bulgarian, Serbian and Macedonian are written in the
// same script, and no letter frequency distinguishes them reliably at title
// length - but wudict has no lemmatizer for any of them, so the alternatives
// are "call it Russian" and "call it nothing", and the second one silently
// falls back to ENGLISH for a Cyrillic dictionary. Being wrong here costs one
// failed probe (D82); being silent costs a wrong-language lemmatizer.
func cyrillicMajority(title string) string {
	cyr, lat := 0, 0
	for _, r := range title {
		switch {
		case unicode.Is(unicode.Cyrillic, r):
			cyr++
		case r < utf8.RuneSelf && unicode.IsLetter(r):
			lat++
		}
	}
	if cyr > lat && cyr > 0 {
		return "ru"
	}
	return ""
}
