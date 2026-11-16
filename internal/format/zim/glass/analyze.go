// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package glass

import (
	"errors"
	"strings"
	"unicode"

	"github.com/blevesearch/snowballstem"
	"github.com/blevesearch/snowballstem/arabic"
	"github.com/blevesearch/snowballstem/danish"
	"github.com/blevesearch/snowballstem/dutch"
	"github.com/blevesearch/snowballstem/english"
	"github.com/blevesearch/snowballstem/finnish"
	"github.com/blevesearch/snowballstem/french"
	"github.com/blevesearch/snowballstem/german"
	"github.com/blevesearch/snowballstem/hungarian"
	"github.com/blevesearch/snowballstem/italian"
	"github.com/blevesearch/snowballstem/norwegian"
	"github.com/blevesearch/snowballstem/portuguese"
	"github.com/blevesearch/snowballstem/romanian"
	"github.com/blevesearch/snowballstem/russian"
	"github.com/blevesearch/snowballstem/spanish"
	"github.com/blevesearch/snowballstem/swedish"
	"github.com/blevesearch/snowballstem/turkish"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
	"golang.org/x/text/unicode/norm"

	"github.com/wuweidict/wudict/internal/lang"
)

// A libzim index holds neither the words of an article nor their spellings:
// the text is lowercased and stripped of accents (zim::removeAccents), cut
// into words by Xapian's TermGenerator, stopwords are dropped (STOP_ALL), and
// what is stored is each word's Snowball stem (STEM_ALL) - with no stemmer
// when Xapian has none for the archive's language (xapianWorker.cpp,
// xapianIndexer.cpp). A query has to be put through the same steps or it asks
// for terms the index never held.

// ErrUnsupported reports an index this reader cannot reproduce the terms of:
// it is stemmed for a language whose stemmer is not carried here. The archive
// is intact and searchable by libzim; it cannot be searched from here.
var ErrUnsupported = errors.New("glass: the index is stemmed for a language this reader has no stemmer for")

// maxTermBytes is TermGenerator's max_word_length: a longer word is not
// indexed.
const maxTermBytes = 64

type stemmer func(*snowballstem.Env) bool

// stemmers are the languages Xapian 1.4.0 stems, by the ISO 639-1 code ICU
// reduces the archive's language to, that have a stemmer here.
var stemmers = map[string]stemmer{
	"ar": arabic.Stem,
	"da": danish.Stem,
	"nl": dutch.Stem,
	"en": english.Stem,
	"fi": finnish.Stem,
	"fr": french.Stem,
	"de": german.Stem,
	"hu": hungarian.Stem,
	"it": italian.Stem,
	"nb": norwegian.Stem,
	"nn": norwegian.Stem,
	"no": norwegian.Stem,
	"pt": portuguese.Stem,
	"ro": romanian.Stem,
	"ru": russian.Stem,
	"es": spanish.Stem,
	"sv": swedish.Stem,
	"tr": turkish.Stem,
}

// unstemmable are the languages some release of Xapian stems and this reader
// does not, or not as that release did: an index in one of them holds stems
// that cannot be reproduced here. A language in neither table has no Xapian
// stemmer in any release, and its index holds the words as they are.
var unstemmable = map[string]bool{
	"hy": true, "eu": true, "ca": true, "cs": true, "eo": true, "et": true,
	"el": true, "hi": true, "id": true, "ga": true, "lt": true, "ne": true,
	"fa": true, "pl": true, "sr": true, "st": true, "ta": true, "yi": true,
}

// Analyzer turns text into the terms one index holds for it.
type Analyzer struct {
	stem  stemmer
	stops map[string]struct{}
}

// Analyzer reads the index's own account of how it was built: its language
// and its stopword list.
func (db *DB) Analyzer() (*Analyzer, error) {
	language, err := db.Metadata("language")
	if err != nil {
		return nil, err
	}
	a := &Analyzer{}
	// ICU reduces an ISO 639-2/3 code to its two-letter form, and anything
	// it cannot read as one language - "eng,fra", "mul" - to no stemmer.
	if code := lang.Normalize(language); code != "" {
		if unstemmable[code] {
			return nil, ErrUnsupported
		}
		a.stem = stemmers[code]
	}
	stops, err := db.Metadata("stopwords")
	if err != nil {
		return nil, err
	}
	for w := range strings.SplitSeq(stops, "\n") {
		if w != "" {
			if a.stops == nil {
				a.stops = map[string]struct{}{}
			}
			a.stops[w] = struct{}{}
		}
	}
	return a, nil
}

var lower = cases.Lower(language.Und)

// Fold is zim::removeAccents, ICU's "Lower; NFD; [:M:] remove; NFC".
func Fold(s string) string {
	s = norm.NFD.String(lower.String(s))
	clean := true
	for _, r := range s {
		if unicode.Is(unicode.M, r) {
			clean = false
			break
		}
	}
	if !clean {
		var b strings.Builder
		b.Grow(len(s))
		for _, r := range s {
			if !unicode.Is(unicode.M, r) {
				b.WriteRune(r)
			}
		}
		s = b.String()
	}
	return norm.NFC.String(s)
}

// Terms calls fn with the index term of every word of text, in order: folded,
// split the way the index was, stopwords and over-long words left out,
// stemmed. A word of a script written without spaces yields its characters
// and its pairs of adjacent characters, as the index holds them.
func (a *Analyzer) Terms(text string, fn func(term string)) {
	words(Fold(text), func(w string) {
		if len(w) > maxTermBytes {
			return
		}
		if _, stop := a.stops[w]; stop {
			return
		}
		if w = a.Stem(w); w != "" {
			fn(w)
		}
	})
}

// Stem returns the index term of one folded word.
func (a *Analyzer) Stem(word string) string {
	if a.stem == nil {
		return word
	}
	env := snowballstem.NewEnv(word)
	a.stem(env)
	return env.Current()
}

// wordChar is Xapian::Unicode::is_wordchar: letters, marks, numbers and
// connector punctuation.
func wordChar(r rune) bool {
	return unicode.In(r, unicode.L, unicode.M, unicode.N, unicode.Pc)
}

// cjk is CJK::codepoint_is_cjk of Xapian 1.4 (cjk-tokenizer.cc): the blocks
// whose text the index holds as one- and two-character terms.
func cjk(r rune) bool {
	switch {
	case r < 0x2E80:
		return false
	case r <= 0x2EFF, // CJK Radicals Supplement
		r >= 0x3000 && r <= 0x9FFF,   // CJK Symbols and Punctuation .. Unified Ideographs
		r >= 0xA700 && r <= 0xA71F,   // Modifier Tone Letters
		r >= 0xAC00 && r <= 0xD7AF,   // Hangul Syllables
		r >= 0xF900 && r <= 0xFAFF,   // CJK Compatibility Ideographs
		r >= 0xFE30 && r <= 0xFE4F,   // CJK Compatibility Forms
		r >= 0xFF00 && r <= 0xFFEF,   // Halfwidth and Fullwidth Forms
		r >= 0x20000 && r <= 0x2A6DF, // Unified Ideographs Extension B
		r >= 0x2F800 && r <= 0x2FA1F: // Compatibility Ideographs Supplement
		return true
	}
	return false
}

const ignore = -1 // an infix character that joins a word without being part of it

// infix is check_infix: a character that continues a word when a word
// character follows it.
func infix(r rune) rune {
	switch r {
	case '\'', '&', 0xb7, 0x5f4, 0x2027:
		return r
	case 0x2019, 0x201b:
		return '\''
	case 0x200c, 0x200d, 0x2060, 0xfeff, 0xad:
		return ignore
	}
	return 0
}

// infixDigit is check_infix_digit: the same between two digits.
func infixDigit(r rune) rune {
	switch r {
	case ',', '.', ';', 0x037e, 0x0589, 0x060D, 0x07F8, 0x2044, 0xFE10, 0xFE13, 0xFE14:
		return r
	case 0x200b, 0x200c, 0x200d, 0x2060, 0xfeff:
		return ignore
	}
	return 0
}

// words cuts lowercased text into words as Xapian's TermGenerator does
// (termgenerator_internal.cc, parse_terms). The rule for initials - U.S.A. -
// needs capitals and never fires on text libzim has lowercased.
func words(text string, fn func(string)) {
	rs := []rune(text)
	n := len(rs)
	var term []rune
	for i := 0; ; {
		for i < n && !wordChar(rs[i]) {
			i++
		}
		if i == n {
			return
		}
		if cjk(rs[i]) {
			// The run's characters, each followed by its pair with the next.
			for ; i < n && cjk(rs[i]) && wordChar(rs[i]); i++ {
				fn(string(rs[i]))
				if i+1 < n && cjk(rs[i+1]) && wordChar(rs[i+1]) {
					fn(string(rs[i : i+2]))
				}
			}
			continue
		}
		term = term[:0]
		ended := false // the word ran into the text's end or a spaceless script
	word:
		for {
			var prev rune
			for {
				term = append(term, rs[i])
				prev = rs[i]
				i++
				if i == n || cjk(rs[i]) {
					ended = true
					break word
				}
				if !wordChar(rs[i]) {
					break
				}
			}
			if i+1 >= n || !wordChar(rs[i+1]) || cjk(rs[i+1]) {
				break
			}
			var in rune
			if unicode.Is(unicode.Nd, prev) && unicode.Is(unicode.Nd, rs[i+1]) {
				in = infixDigit(rs[i])
			} else {
				in = infix(rs[i])
			}
			if in == 0 {
				break
			}
			if in != ignore {
				term = append(term, in)
			}
			i++
		}
		if !ended {
			// Up to three trailing + or # belong to the word (C++, C#),
			// unless a word follows at once: fish+chips is not "fish+".
			base, count := len(term), 0
			for i < n && (rs[i] == '+' || rs[i] == '#') {
				if count++; count > 3 {
					term = term[:base]
					break
				}
				term = append(term, rs[i])
				i++
			}
			if i < n && wordChar(rs[i]) {
				term = term[:base]
			}
		}
		fn(string(term))
	}
}
