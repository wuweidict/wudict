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
			// A term the tokenizer cuts in several - medium-to-large,
			// don't - is that many adjacent tokens, as it is in the index.
			for _, r := range foldTerm(term.Text) {
				c = append(c, pat{runes: r, prefix: term.Prefix})
			}
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

// CompilePhrase folds a word sequence into ONE phrase: the words adjacent and
// in order, the last one left open. It is the reading of a find field - `take
// of` finds "take off" and "takes of" does not - where Compile's is the
// reading of a bag of search terms.
//
// Every term is handed over open; NewPhrases keeps the star on the last token
// that survives folding, so trailing punctuation cannot close the phrase.
func CompilePhrase(text string) *Terms {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return nil
	}
	p := make(Phrase, len(fields))
	for i, f := range fields {
		p[i] = Term{Text: f, Prefix: true}
	}
	return NewPhrases([]Phrase{p})
}

// Mark marks one text node that stands alone: Marker.Mark with no article
// around it.
func (t *Terms) Mark(s string) string {
	if t == nil || s == "" {
		return s
	}
	m := Marker{t: t}
	return m.Mark(s, "")
}

// Marker marks the text nodes of ONE article, in document order. It is what
// carries a phrase across the markup that splits it: the zero value is not
// usable, and a Marker is not shared between articles or goroutines.
type Marker struct {
	t     *Terms
	marks int
	// pending are the tokens that complete a phrase begun in an earlier
	// node, which the nodes that follow open with (lookahead).
	pending []pat
}

// Marker returns a marker for one article; nil for nil Terms.
func (t *Terms) Marker() *Marker {
	if t == nil {
		return nil
	}
	return &Marker{t: t}
}

// maxLook bounds how far past a text node the rest of a phrase is looked
// for: the markup between two words of a phrase is a few tags.
const maxLook = 2048

// Mark returns s with every matching span wrapped in <mark class="wu-hl">.
//
// s is ONE HTML text node, handed over as the raw source bytes, and rest is
// the document after it. Everything outside a match is copied byte for byte -
// entities, encoding and all - so marking cannot alter what the article says,
// only what is wrapped around it. When nothing matches, the input string is
// returned and nothing is allocated.
//
// The index reads an article with its tags taken out, so a phrase the
// dictionary broke across markup - `<b>Физика</b> в конспектах` - matches
// there, and is marked here: each stretch of it in its own text node gets its
// own <mark>, because one that opened in one element and closed in another
// would not be HTML. rest is read only when a phrase has begun and the node
// has ended, and only maxLook bytes of it.
func (m *Marker) Mark(s, rest string) string {
	if m == nil || s == "" {
		return s
	}
	t := m.t
	var b strings.Builder
	last, i := 0, 0
	if len(m.pending) > 0 {
		if start, end := m.resume(s); end > 0 {
			b.Grow(len(s) + len(s)/8)
			b.WriteString(s[:start])
			b.WriteString(openTag)
			b.WriteString(s[start:end])
			b.WriteString(closeTag)
			last, i = end, end
			m.marks++
		}
	}
	marks := m.marks
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
		if hit == 0 {
			continue
		}
		end, open := t.longestMatch(s, tokEnd, hit)
		if end == 0 && open.phrase >= 0 && rest != "" {
			// The node ended inside a phrase: marked when what follows the
			// markup completes it, and the following nodes are told.
			if tail := t.phrases[open.phrase][open.matched:]; lookahead(rest, tail) {
				end, m.pending = open.end, tail
			}
		}
		if end > start {
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
	if marks == m.marks && last == 0 {
		return s
	}
	m.marks = marks
	b.WriteString(s[last:])
	return b.String()
}

// resume marks the tokens this node opens with that complete a phrase begun
// in an earlier one, and returns their span; end is 0 when the node has no
// token, or does not continue the phrase after all.
func (m *Marker) resume(s string) (start, end int) {
	start = -1
	for i := 0; len(m.pending) > 0; {
		at := nextTokenStart(s, i)
		if at < 0 {
			break // the phrase goes on in a later node
		}
		tokEnd, ok := matchTokenAt(s, at, m.pending[0])
		if !ok {
			m.pending = nil
			return 0, 0
		}
		if start < 0 {
			start = at
		}
		end, i = tokEnd, tokEnd
		m.pending = m.pending[1:]
	}
	return start, end
}

// lookahead reports whether the document after a text node opens - past its
// markup - with tokens that match pats, in order. It reads text the way Mark
// does and steps over tags, comments and the contents of raw-text elements,
// which no text node is made of.
func lookahead(rest string, pats []pat) bool {
	if len(rest) > maxLook {
		rest = rest[:maxLook]
	}
	for i := 0; i < len(rest); {
		switch c := rest[i]; {
		case c == '<':
			j := tagEnd(rest, i)
			if j < 0 {
				return false
			}
			i = j
		case c == '&':
			if j := entityEnd(rest, i); j > i {
				i = j
			} else {
				i++
			}
		default:
			r, sz := utf8.DecodeRuneInString(rest[i:])
			if !isTokenRune(r) {
				i += sz
				continue
			}
			end, ok := matchTokenAt(rest, i, pats[0])
			if !ok {
				return false
			}
			if pats = pats[1:]; len(pats) == 0 {
				return true
			}
			i = end
		}
	}
	return false
}

// tagEnd returns the byte past the markup that starts at s[i] ('<'): a tag, a
// comment, or a raw-text element with its contents. A '<' that starts none of
// these is text and is stepped over alone; -1 means the markup does not end
// in s.
func tagEnd(s string, i int) int {
	if i+1 >= len(s) {
		return -1
	}
	switch c := s[i+1]; {
	case c == '!' && strings.HasPrefix(s[i:], "<!--"):
		if j := strings.Index(s[i+4:], "-->"); j >= 0 {
			return i + 4 + j + 3
		}
		return -1
	case c == '!' || c == '?':
		if j := strings.IndexByte(s[i:], '>'); j >= 0 {
			return i + j + 1
		}
		return -1
	case c == '/' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z':
	default:
		return i + 1
	}
	// The tag's name, then its attributes: a '>' inside a quoted value does
	// not close it.
	n := i + 1
	if s[n] == '/' {
		n++
	}
	nameEnd := n
	for nameEnd < len(s) && s[nameEnd] != '>' && s[nameEnd] != '/' && s[nameEnd] > ' ' {
		nameEnd++
	}
	j := nameEnd
	for quote := byte(0); ; j++ {
		if j >= len(s) {
			return -1
		}
		switch c := s[j]; {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '>':
			if s[i+1] != '/' && rawText(s[n:nameEnd]) {
				return rawEnd(s, j+1, s[n:nameEnd])
			}
			return j + 1
		}
	}
}

// rawText is the tokenizer's list of elements whose content is not markup
// (x/net/html readStartTag; htmlref.rawTextTag is the same list).
func rawText(name string) bool {
	switch strings.ToLower(name) {
	case "script", "style", "title", "textarea", "iframe", "noscript", "noembed", "noframes", "xmp", "plaintext":
		return true
	}
	return false
}

// rawEnd returns the byte past the end tag that closes the raw-text element
// name, whose content starts at i.
func rawEnd(s string, i int, name string) int {
	for {
		j := strings.Index(s[i:], "</")
		if j < 0 {
			return -1
		}
		i += j + 2
		if len(s)-i >= len(name) && strings.EqualFold(s[i:i+len(name)], name) {
			if k := strings.IndexByte(s[i:], '>'); k >= 0 {
				return i + k + 1
			}
			return -1
		}
	}
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

// openPhrase is a phrase the text ran out under: its first matched tokens
// are the last tokens of the node, ending at end.
type openPhrase struct {
	phrase  int // -1 for none
	matched int
	end     int
}

// longestMatch extends each candidate phrase over the tokens after the first
// and returns the end of the longest one that completes, or 0 for none. The
// longest wins so that a phrase is never shadowed by one of its own words.
// open is the longest phrase that did not complete only because the node
// ended first.
func (t *Terms) longestMatch(s string, afterFirst int, hit uint64) (best int, open openPhrase) {
	open.phrase = -1
	for m := hit; m != 0; {
		j := bits.TrailingZeros64(m)
		m &^= 1 << uint(j)
		end, ok := afterFirst, true
		for k, want := range t.phrases[j][1:] {
			start := nextTokenStart(s, end)
			if start < 0 {
				if k+1 > open.matched {
					open = openPhrase{phrase: j, matched: k + 1, end: end}
				}
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
	return best, open
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
		case pos >= len(p.runes):
			// The token runs past the pattern. An open pattern accepts the
			// rest of it - that is the whole content of the trailing star -
			// and a closed one is mismatched from here on.
			//
			// `>=`, not `==`: pos keeps counting past the pattern's length,
			// so a token longer than an OPEN pattern by more than one rune
			// arrives here a second time. Reading it as `==` let the second
			// rune fall through to the comparison below and index the pattern
			// out of range - `"de miedo"*` against *miedoso*, `"de mala
			// leche"*` against *lechería* - which panicked on the search
			// worker's own goroutine, where net/http's recover cannot reach
			// it, and took the process down with it.
			if !p.prefix {
				mism = true
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

// foldTerm reduces one query word to its token runs, folded.
//
// The scan only ever compares a pattern against a token, so a term that is
// not a token cannot match and must not occupy one of the 64 slots: "!!!"
// yields nothing, and "-run" yields "run" rather than being thrown away for
// its leading dash. A word that spans a separator is the tokens on either
// side of it, adjacent - "don't" is don then t, as the index holds it - so it
// marks "don't" and no stray "t".
func foldTerm(s string) [][]rune {
	var runs [][]rune
	var cur []rune
	for _, r := range s {
		if !isTokenRune(r) {
			if len(cur) > 0 {
				runs, cur = append(runs, cur), nil
			}
			continue
		}
		if f, keep := foldRune(r); keep {
			cur = append(cur, f)
		}
	}
	if len(cur) > 0 {
		runs = append(runs, cur)
	}
	return runs
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
