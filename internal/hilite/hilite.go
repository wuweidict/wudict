// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Package hilite marks the spans a full-text query matched inside an article's
// HTML.
//
// It exists because SQLite cannot do this job. entry_fts is declared with an
// empty content= and columnsize=0 (store/ingest.go), and FTS5's highlight() on a
// contentless table returns an EMPTY STRING with no error - a silent nothing,
// not a failure a test would catch. And even with the content stored, the
// indexed `txt` column is StripHTML(body): tags gone, entities unescaped,
// whitespace collapsed. Offsets into that string have no computable relation
// to the article HTML that is actually served, so no amount of SQL reaches
// the bytes a reader looks at.
//
// The design constraint is that this must cost approximately nothing. It does,
// because it is not a pass of its own: Mark is installed as the Text hook of
// the htmlref.Rewriter that already walks every article body on the way out
// (server/rewrite.go), so highlighting adds no parse, no traversal and no copy
// of the document. What is left is this file - one forward scan per text node,
// no allocation until the first mark is actually emitted.
//
// # What is marked is what was asked
//
// A mark is not "a word from the search box". It is the projection of the
// executed query's own spans onto the text, and the unit of that projection is
// the PHRASE: internal/ftsq compiles one query into both the FTS5 MATCH
// expression and the phrases handed here, so the two cannot disagree about
// what matched. `Физика в конспектах` run as a phrase paints one span across
// three tokens; the same words run as a bag of terms paint three. Marking
// terms while matching phrases is the classic defect - Lucene calls the cure
// "highlight phrases strictly" and has it on by default.
//
// Within a phrase every token but the last must match WHOLE, because that is
// what an FTS5 phrase means; a trailing prefix star relaxes the last one only.
// A matched token is always marked whole - the token is what FTS5 matched, and
// half-marked words read as a rendering fault. Folding is case- and
// diacritic-insensitive, per rune and without allocating, to approximate the
// unicode61 remove_diacritics 2 tokenizer the index was built with.
package hilite

import (
	"math/bits"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

const (
	// maxPhrases is dictated by the alive-set being one uint64. A query with
	// more than 64 phrases is not a query, and the extra ones are dropped
	// rather than allowed to cost a second word of state per text node.
	maxPhrases = 64

	// MaxMarks bounds one article's output growth. A one-character term is a
	// legal prefix query that matches nearly every word, and the reader is no
	// better served by ten thousand marks than by five hundred. Past the cap
	// marking stops; the ARTICLE IS STILL EMITTED WHOLE - the tail is copied
	// verbatim, never truncated.
	MaxMarks = 500

	// maxEntity bounds the entity guard's lookahead. "&thickapprox;" is 13
	// bytes; anything longer is not an entity worth protecting, and an
	// unbounded scan on a '&' in prose would be quadratic.
	maxEntity = 14

	openTag  = `<mark class="wu-hl">`
	closeTag = `</mark>`
)

// Term is one token of a query as the user typed it. Prefix says the token was
// left open (FTS5's trailing star), which only the last token of a phrase can
// be - see NewPhrases.
type Term struct {
	Text   string
	Prefix bool
}

// Phrase is a run of tokens that must appear adjacently, in order. A one-token
// Phrase is an ordinary term; that is the only shape a bag-of-words query
// produces.
type Phrase []Term

// pat is one compiled token: folded runes, plus whether the token it matches
// may be longer than the pattern.
type pat struct {
	runes  []rune
	prefix bool
}

// Terms is one query compiled for marking. Compile once per request, then
// share it across every result of every dictionary: it is read-only.
type Terms struct {
	phrases [][]pat // deduplicated, in query order; each is non-empty
	full    uint64  // the all-alive mask for len(phrases)
}

// NewPhrases compiles the phrases a query matched with. It returns nil when
// there is nothing to mark, which every caller may pass straight to Mark.
//
// A term that folds to nothing (punctuation) is dropped from its phrase rather
// than voiding it, because the tokenizer that built the index drops it too.
// Prefix survives only on the last surviving token: FTS5 cannot express an
// open token in the middle of a phrase, so neither may this.
func NewPhrases(ps []Phrase) *Terms {
	var t Terms
	for _, p := range ps {
		var c []pat
		for _, term := range p {
			r := foldTerm(term.Text)
			if len(r) == 0 {
				continue
			}
			c = append(c, pat{runes: r, prefix: term.Prefix})
		}
		if len(c) == 0 {
			continue
		}
		for i := range c[:len(c)-1] {
			c[i].prefix = false
		}
		// A lone open one-rune token is a wildcard, not a word (worthMarking).
		// Inside a phrase it is neither: its neighbours pin it down, which is
		// exactly how `Физика в конспектах` keeps its в and marks nothing else.
		if len(c) == 1 && c[0].prefix && !worthMarking(c[0].runes) {
			continue
		}
		if containsPhrase(t.phrases, c) {
			continue
		}
		t.phrases = append(t.phrases, c)
		if len(t.phrases) == maxPhrases {
			break
		}
	}
	if len(t.phrases) == 0 {
		return nil
	}
	t.full = ^uint64(0) >> (maxPhrases - len(t.phrases))
	return &t
}

// Compile folds a bare word sequence into independent prefix terms - the
// bag-of-words reading of a query, and the shape of the ladder's last rung.
func Compile(query string) *Terms {
	fields := strings.Fields(query)
	ps := make([]Phrase, 0, len(fields))
	for _, f := range fields {
		ps = append(ps, Phrase{{Text: f, Prefix: true}})
	}
	return NewPhrases(ps)
}

// Mark returns s with every matching span wrapped in <mark class="wu-hl">.
//
// s is ONE HTML text node, handed over as the raw source bytes. Everything
// outside a match is copied byte for byte - entities, encoding and all - so
// marking cannot alter what the article says, only what is wrapped around it.
// When nothing matches, the input string is returned and nothing is allocated.
//
// A phrase is therefore matched WITHIN one text node. A phrase the dictionary
// broke across markup - `<b>Физика</b> в конспектах` - is not marked, which
// under-marks rather than mis-marks: the alternative is a <mark> that opens in
// one element and closes in another, which is not HTML.
func (t *Terms) Mark(s string) string {
	if t == nil || s == "" {
		return s
	}
	var b strings.Builder
	last, marks, i := 0, 0, 0
	for i < len(s) && marks < MaxMarks {
		// An entity is never a token and never part of one. Without this,
		// a query for "n" would emit "&<mark>nbsp</mark>;" - which renders as
		// the literal text "&nbsp;" and silently corrupts the article.
		if s[i] == '&' {
			if j := entityEnd(s, i); j > i {
				i = j
				continue
			}
		}
		r, sz := utf8.DecodeRuneInString(s[i:])
		if !isTokenRune(r) {
			i += sz
			continue
		}
		start := i
		tokEnd, hit := t.firstToken(s, i)
		i = tokEnd
		if end := t.longestMatch(s, tokEnd, hit); end > start {
			if b.Cap() == 0 {
				b.Grow(len(s) + len(s)/8)
			}
			b.WriteString(s[last:start])
			b.WriteString(openTag)
			b.WriteString(s[start:end])
			b.WriteString(closeTag)
			last, marks, i = end, marks+1, end
		}
	}
	if marks == 0 {
		return s
	}
	b.WriteString(s[last:])
	return b.String()
}

// firstToken scans the token starting at i and reports where it ends, together
// with the set of phrases whose FIRST token this is - each judged by its own
// rule, a prefix pattern accepting a longer token and an exact one not.
func (t *Terms) firstToken(s string, i int) (end int, hit uint64) {
	alive, pos := t.full, 0
	for i < len(s) {
		if s[i] == '&' {
			if j := entityEnd(s, i); j > i {
				break // an entity ends the token; "caf&eacute;" marks "caf"
			}
		}
		r, sz := utf8.DecodeRuneInString(s[i:])
		if !isTokenRune(r) {
			break
		}
		i += sz
		f, keep := foldRune(r)
		if !keep {
			continue // combining mark: folded away, does not advance pos
		}
		if alive != 0 {
			// Exhausted here means matched with the token still running: a
			// prefix pattern is satisfied, an exact one is not.
			var done uint64
			alive, done = t.retire(alive, pos)
			hit |= t.prefixOnly(done)
			alive = t.step(alive, pos, f)
		}
		pos++
	}
	// Exhausted exactly at the token's end satisfies either kind.
	if alive != 0 {
		_, done := t.retire(alive, pos)
		hit |= done
	}
	return i, hit
}

// longestMatch extends each candidate phrase over the tokens after the first
// and returns the end of the longest one that completes, or 0 for none. The
// longest wins so that a phrase is never shadowed by one of its own words.
func (t *Terms) longestMatch(s string, afterFirst int, hit uint64) int {
	best := 0
	for m := hit; m != 0; {
		j := bits.TrailingZeros64(m)
		m &^= 1 << uint(j)
		end, ok := afterFirst, true
		for _, want := range t.phrases[j][1:] {
			start := nextTokenStart(s, end)
			if start < 0 {
				ok = false
				break
			}
			if end, ok = matchTokenAt(s, start, want); !ok {
				break
			}
		}
		if ok && end > best {
			best = end
		}
	}
	return best
}

// retire removes from the live set every phrase whose first pattern is fully
// consumed at pos, and returns those separately: they have matched, and the
// caller decides what that is worth given where the token ended.
func (t *Terms) retire(alive uint64, pos int) (uint64, uint64) {
	var done uint64
	for m := alive; m != 0; {
		j := bits.TrailingZeros64(m)
		m &^= 1 << uint(j)
		if len(t.phrases[j][0].runes) == pos {
			done |= 1 << uint(j)
			alive &^= 1 << uint(j)
		}
	}
	return alive, done
}

// prefixOnly reduces a set to those phrases whose first pattern is open.
func (t *Terms) prefixOnly(set uint64) uint64 {
	var out uint64
	for m := set; m != 0; {
		j := bits.TrailingZeros64(m)
		m &^= 1 << uint(j)
		if t.phrases[j][0].prefix {
			out |= 1 << uint(j)
		}
	}
	return out
}

// step advances every still-live phrase's first pattern by one folded rune and
// reports the survivors.
func (t *Terms) step(alive uint64, pos int, f rune) uint64 {
	for m := alive; m != 0; {
		j := bits.TrailingZeros64(m)
		m &^= 1 << uint(j)
		r := t.phrases[j][0].runes
		if pos >= len(r) || r[pos] != f {
			alive &^= 1 << uint(j)
		}
	}
	return alive
}

// nextTokenStart is where the next token begins at or after i, or -1. What it
// skips over - spaces, punctuation, entities - is exactly what unicode61
// treats as a separator, which is why "физика, в конспектах" still satisfies
// the phrase: FTS5 counts token positions, not characters.
func nextTokenStart(s string, i int) int {
	for i < len(s) {
		if s[i] == '&' {
			if j := entityEnd(s, i); j > i {
				i = j
				continue
			}
			i++
			continue
		}
		r, sz := utf8.DecodeRuneInString(s[i:])
		if isTokenRune(r) {
			return i
		}
		i += sz
	}
	return -1
}

// matchTokenAt tests one pattern against the whole token starting at i,
// returning the token's end either way - the caller has no cheaper way to find
// it, and a failed phrase still has to resume past this token.
func matchTokenAt(s string, i int, p pat) (int, bool) {
	pos, mism := 0, false
	for i < len(s) {
		if s[i] == '&' {
			if j := entityEnd(s, i); j > i {
				break
			}
		}
		r, sz := utf8.DecodeRuneInString(s[i:])
		if !isTokenRune(r) {
			break
		}
		i += sz
		f, keep := foldRune(r)
		if !keep {
			continue
		}
		switch {
		case mism:
		case pos == len(p.runes):
			if !p.prefix {
				mism = true // the token runs past a closed pattern
			}
			pos++
		case p.runes[pos] != f:
			mism = true
		default:
			pos++
		}
	}
	return i, !mism && pos >= len(p.runes)
}

// isTokenRune mirrors unicode61's token/separator split (categories L* and N*).
// Combining marks join it deliberately: an article stored in decomposed form
// would otherwise have every accented word split in two, and foldRune drops
// them anyway - which is what remove_diacritics 2 does to the index.
//
// The ASCII gate is not decoration. unicode.Is(unicode.Mn, r) has no fast path
// and searches a range table on every call, so without it the commonest rune
// in a dictionary - a lower-case Latin letter - paid for a binary search
// through the combining-mark tables before being classified. Measured by
// BenchmarkMark on a 9 kB article: 103 -> 142 MB/s when marking, 128 -> 173
// MB/s when nothing matches.
func isTokenRune(r rune) bool {
	if r < utf8.RuneSelf {
		return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9'
	}
	return unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.Is(unicode.Mn, r)
}

// foldRune lowercases one rune and reduces it to its canonical base, reporting
// false for a rune that folds to nothing. It is dict.Fold's rule applied per
// rune instead of per string, which is what keeps the scan allocation-free.
//
// Query terms are folded through the same function, so the two sides are
// consistent by construction. Where per-rune and per-string NFD differ - a
// Hangul syllable decomposes into several non-combining jamo - this stays
// syllable-granular. That makes a Korean prefix mark conservative, never
// wrong, and never disagrees with the terms it is compared against.
func foldRune(r rune) (rune, bool) {
	if r < utf8.RuneSelf {
		if r >= 'A' && r <= 'Z' {
			return r + ('a' - 'A'), true
		}
		return r, true
	}
	r = unicode.ToLower(r)
	if unicode.Is(unicode.Mn, r) {
		return 0, false
	}
	var buf [4]byte
	n := utf8.EncodeRune(buf[:], r)
	d := norm.NFD.Properties(buf[:n]).Decomposition()
	if len(d) == 0 {
		return r, true // no canonical decomposition: already a base
	}
	f, _ := utf8.DecodeRune(d)
	switch {
	case f == utf8.RuneError:
		return r, true
	case unicode.Is(unicode.Mn, f):
		return 0, false
	}
	return f, true
}

// foldTerm reduces one query word to its FIRST token run, folded.
//
// The scan only ever compares a term against a token, so a term that is not a
// token cannot match and must not occupy one of the 64 slots: "!!!" yields
// nothing, and "-run" yields "run" rather than being thrown away for its
// leading dash. A word that spans a separator is cut at it - "don't" becomes
// "don" - which marks less than the query asked for and never more. Marking
// both halves independently would light up every stray "t" in the article.
func foldTerm(s string) []rune {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if !isTokenRune(r) {
			if len(out) > 0 {
				break // end of the first run
			}
			continue // still skipping leading separators
		}
		if f, keep := foldRune(r); keep {
			out = append(out, f)
		}
	}
	return out
}

// entityEnd returns the byte just past the character reference starting at
// s[i] ('&'), or 0 when what follows is not one. "&" in prose is ordinary
// text and must stay ordinary text.
func entityEnd(s string, i int) int {
	end := i + maxEntity
	if end > len(s) {
		end = len(s)
	}
	for j := i + 1; j < end; j++ {
		switch c := s[j]; {
		case c == ';':
			if j == i+1 {
				return 0 // "&;" is not a reference
			}
			return j + 1
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '#':
		default:
			return 0
		}
	}
	return 0
}

// worthMarking rejects a lone open term that would mark far more than the user
// asked about. A bag-of-words rung matches every token as a prefix, and a
// one-rune prefix of a spaced script is a wildcard, not a word: `Физика в
// конспектах`, relaxed to its words, marks в but also Введение, Вода, время,
// всё and вокруг, drowning the two terms that carry the question. The same is
// true of a stray "a" or "I" in English.
//
// It is NOT true where a script has no spaces. There a single character IS a
// word - 水, か, 한 - and dropping it would silence highlighting for exactly
// the queries that are hardest to eyeball. So the rule is one rune AND a
// spaced script, never one rune alone - and never inside a phrase, where the
// neighbouring tokens already say where the word is.
//
// The term still takes part in the search; this only decides what is painted.
func worthMarking(r []rune) bool {
	if len(r) > 1 {
		return true
	}
	c := r[0]
	return unicode.Is(unicode.Han, c) || unicode.Is(unicode.Hiragana, c) ||
		unicode.Is(unicode.Katakana, c) || unicode.Is(unicode.Hangul, c)
}

func containsPhrase(set [][]pat, p []pat) bool {
	for _, e := range set {
		if samePhrase(e, p) {
			return true
		}
	}
	return false
}

func samePhrase(a, b []pat) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].prefix != b[i].prefix || len(a[i].runes) != len(b[i].runes) {
			return false
		}
		for j := range a[i].runes {
			if a[i].runes[j] != b[i].runes[j] {
				return false
			}
		}
	}
	return true
}
