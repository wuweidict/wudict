// Copyright (C) 2026 glowinthedark
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"cmp"
	"context"
	"slices"
	"strconv"
	"sync"
	"unicode/utf8"

	"github.com/wuweidict/wudict/internal/dict"
	"github.com/wuweidict/wudict/internal/wildcard"
)

var _ dict.Speller = (*Store)(nil)

// SimilarMin is the fewest folded characters a word needs for SimilarKeys:
// below it, one edit reaches a large share of every short headword.
const SimilarMin = 3

// similarRows bounds the headwords one SimilarKeys call hands to Go. A walk
// prunes every prefix that is already too far from the word, so it reads a
// small part of its ranges; the bound is for a dictionary whose headwords
// defeat that, and what was found until then is the answer.
const similarRows = 100_000

// SimilarKeys implements dict.Speller: the headwords within maxDist edits of
// word - optimal string alignment distance: one character inserted, deleted
// or replaced, or two neighbours swapped, each one edit - compared as
// Collate's first level reads them (wildcard.Fold: case and accents
// ignored), the word itself excluded. Fewest edits first, then the length
// nearest the word's, then dictionary order; up to limit of them.
//
// The walk reads idx_entry_w in order and carries one row of the edit
// distance table per character, shared by every headword that starts alike
// (a trie over the sorted index): a headword costs only the characters past
// what it shares with the one before. Once every entry of a row exceeds
// maxDist, no headword that starts with that prefix can come within it, and
// the walk seeks past them all - the Levenshtein-automaton intersection
// (Schulz and Mihov 2002) done with the table's rows.
//
// It reads the headwords whose first character folds to the word's first: a
// misspelling rarely changes the first letter (Pollock and Zamora 1984); and
// those that start with its first two swapped. A first letter typed twice or
// left out is out of reach. The answer is kept
// per word on the open handle (similarMemo): the list asks again for a word
// the search then misses.
func (s *Store) SimilarKeys(ctx context.Context, word string, maxDist, limit int) ([]string, error) {
	q := primary(word)
	if len(q) < SimilarMin || maxDist < 1 || limit < 1 {
		return nil, nil
	}
	key := string(q) + "\x00" + strconv.Itoa(maxDist) + "\x00" + strconv.Itoa(limit)
	if out, ok := s.similar.get(key); ok {
		return out, nil
	}
	// The ranges of every character that folds to a lead, the empty ones -
	// most of the accented forms - dropped by the planner's batched count.
	leads := []rune{q[0]}
	if q[1] != q[0] {
		leads = append(leads, q[1])
	}
	var cs []cand
	var second []bool
	for i, l := range leads {
		for _, c := range wildcard.Folds(l) {
			if c >= utf8.RuneSelf || c == l { // an ASCII twin is in the lead's NOCASE range
				cs, second = append(cs, nodeAt(string(c), 1)), append(second, i == 1)
			}
		}
	}
	counts, err := s.newPlanner().count(ctx, cs)
	if err != nil {
		return nil, err
	}
	w := newEditWalk(q, maxDist, limit)
	budget := similarRows
	for i, c := range cs {
		if counts[i] == 0 {
			continue
		}
		// Under the second letter only the two swapped: a headword whose own
		// second letter is not the word's first is dead there.
		w.second = 0
		if second[i] {
			w.second = q[0]
		}
		if err := s.walkSimilar(ctx, c.r, w, &budget); err != nil {
			return nil, err
		}
	}
	w.trim()
	out := make([]string, len(w.found))
	for i, f := range w.found {
		out[i] = f.key
	}
	if budget > 0 && ctx.Err() == nil { // a cut walk is not the answer to keep
		s.similar.put(key, out)
	}
	return out, nil
}

// seekAfter is how many headwords under a dead prefix the walk reads past
// before it seeks over the rest: running a range costs about what 15
// headwords handed to Go do (~5 µs against ~0.33 µs each, wildcard.go).
const seekAfter = 16

// walkSimilar runs the walk over one range, in chunks: past a dead prefix it
// skips the headwords before the next one that can be within reach, reading
// them while that is cheaper than a seek.
func (s *Store) walkSimilar(ctx context.Context, r keyRange, w *editWalk, budget *int) error {
	lo, size := r.lo, chunkFirst
	var last, skipTo []byte
	for *budget > 0 {
		n, lim, skipped, jump := 0, min(size, *budget), 0, false
		err := s.readRange(ctx, source{}, keyRange{lo, r.hi}, lim, func(_ hit, key []byte) bool {
			n++
			last = append(last[:0], key...)
			if skipTo != nil && lessNOCASE(key, skipTo) {
				skipped++
				jump = skipped >= seekAfter
				return !jump
			}
			skipped = 0
			skipTo = w.feed(key)
			return true
		})
		*budget -= n
		switch {
		case err != nil:
			return err
		case jump || (n == lim && skipTo != nil):
			lo, size = string(skipTo), chunkFirst
		case n < lim:
			return nil
		default:
			// Past the last key read: one that NOCASE reads as equal to it is
			// the same word in other capitals, which the caller merges anyway.
			lo, size = string(last)+"\x00", min(size*2, chunkMax)
		}
	}
	return nil
}

// lessNOCASE reports whether key sorts before s as NOCASE compares them.
func lessNOCASE(key, s []byte) bool {
	for i := 0; i < len(key) && i < len(s); i++ {
		if a, b := lowerByte(key[i]), lowerByte(s[i]); a != b {
			return a < b
		}
	}
	return len(key) < len(s)
}

func lowerByte(c byte) byte {
	if 'A' <= c && c <= 'Z' {
		return c + 'a' - 'A'
	}
	return c
}

// similarKey is a headword the walk found: its distance and how far its
// length is from the word's.
type similarKey struct {
	key       string
	dist, off int
}

// editWalk is the edit distance table of one word against the headwords of
// a walk, one row per folded character of the current headword. rows[j] is
// the row after j characters; a headword that shares its first runes with
// the one before keeps their rows.
type editWalk struct {
	q     []rune
	max   int
	n     int   // len(q)
	rows  []int // depth+1 rows of n+1 entries
	keyF  []rune
	prev  []rune // the previous headword's characters
	depth []int  // the table depth after each of them
	cur   []rune
	ends  []int // the byte offset past each character of cur
	// found is the headwords within reach, cut back to the best limit
	// whenever it doubles past them (trim): a short word in a large
	// dictionary reaches thousands.
	found []similarKey
	limit int
	// second, when set, is the character a headword's second must fold to:
	// the walk of the word's first two swapped.
	second rune
	allow  []rune            // jump's scratch
	low    []byte            // jump's scratch: a character as NOCASE reads it
	skip   []byte            // jump's answer, valid until the next jump
	cands  map[rune][]string // candidates, by folded character
}

func newEditWalk(q []rune, maxDist, limit int) *editWalk {
	n := len(q)
	// No row deeper than n+maxDist+1 is ever kept: its every entry is at
	// least depth-n.
	w := &editWalk{q: q, max: maxDist, n: n, limit: limit, rows: make([]int, n+1, (n+maxDist+2)*(n+1))}
	w.reset()
	return w
}

// reset is the table for no character of a headword: row 0.
func (w *editWalk) reset() {
	w.rows, w.keyF = w.rows[:w.n+1], w.keyF[:0]
	for k := range w.rows {
		w.rows[k] = k
	}
}

// last is the distance of a headword of d folded characters from the word,
// when it is within the budget: entry n of row d, which lies in the band only
// then (step).
func (w *editWalk) last(d int) (int, bool) {
	if max(d-w.n, w.n-d) > w.max {
		return 0, false
	}
	v := w.rows[d*(w.n+1)+w.n]
	return v, v <= w.max
}

// trim keeps the best limit headwords found, each once.
func (w *editWalk) trim() {
	slices.SortFunc(w.found, func(a, b similarKey) int {
		return cmp.Or(cmp.Compare(a.dist, b.dist), cmp.Compare(a.off, b.off), collCompare(a.key, b.key))
	})
	w.found = slices.CompactFunc(w.found, func(a, b similarKey) bool { return a.key == b.key })
	w.found = w.found[:min(len(w.found), w.limit)]
}

// feed walks one headword, filing it when it is within reach. When a prefix
// of it is already too far from the word, it returns the first key, in NOCASE
// order, after it that can still be within reach (jump); nil otherwise.
func (w *editWalk) feed(key []byte) []byte {
	w.cur, w.ends = w.cur[:0], w.ends[:0]
	for at := 0; at < len(key); {
		r, size := utf8.DecodeRune(key[at:])
		at += size
		w.cur, w.ends = append(w.cur, r), append(w.ends, at)
	}
	shared := 0
	for shared < len(w.cur) && shared < len(w.prev) && w.cur[shared] == w.prev[shared] {
		shared++
	}
	d := 0
	if shared > 0 {
		d = w.depth[shared-1]
	}
	w.rows, w.keyF = w.rows[:(d+1)*(w.n+1)], w.keyF[:d]
	w.depth = w.depth[:shared]
	for i := shared; i < len(w.cur); i++ {
		if f := wildcard.Fold(w.cur[i]); f >= 0 {
			if w.step(f) > w.max || (d == 1 && w.second != 0 && f != w.second) {
				to := w.jump(key, i, d)
				w.depth = append(w.depth, d+1)
				w.prev, w.cur = w.cur[:i+1], w.prev
				return to
			}
			d++
		}
		w.depth = append(w.depth, d)
	}
	w.prev, w.cur = w.cur, w.prev
	if dist, ok := w.last(d); ok && dist > 0 {
		w.found = append(w.found, similarKey{key: string(key), dist: dist, off: max(d-w.n, w.n-d)})
		if len(w.found) >= 2*w.limit+16 {
			w.trim()
		}
	}
	return nil
}

// jump is where the walk goes on when character i of key, after d folded
// characters, ends every match: the least key, in NOCASE order, that shares
// the prefix before it and continues it with a character that can still be
// within reach - or with a mark, which Fold drops - or, with none left, the
// first key past that prefix (Lucene's "next valid string").
//
// A character can be within reach only through a row entry still inside the
// budget. The character did not get there, so the row before it has spent
// its budget everywhere: only a match (the word's character k where that
// row's entry k-1 is at the budget) or a swap keeps an entry inside it.
func (w *editWalk) jump(key []byte, i, d int) []byte {
	start := 0
	if i > 0 {
		start = w.ends[i-1]
	}
	w.skip, w.low = appendLower(w.skip[:0], key[:start]), appendLower(w.low[:0], key[start:w.ends[i]])
	cur := w.low
	w.allow = w.allow[:0]
	if d == 1 && w.second != 0 {
		w.allow = append(w.allow, w.second)
	} else {
		n := w.n
		up := w.rows[d*(n+1) : (d+1)*(n+1)]
		// up is valid in its band only (step): k-1 within max of d.
		for k := max(1, d-w.max+1); k <= min(n, d+w.max+1); k++ {
			if up[k-1] <= w.max {
				w.allow = append(w.allow, w.q[k-1])
			}
			if d >= 1 && k >= 2 && w.keyF[d-1] == w.q[k-1] && w.rows[(d-1)*(n+1)+k-2]+1 <= w.max {
				w.allow = append(w.allow, w.q[k-2])
			}
		}
	}
	best := ""
	for _, a := range w.allow {
		cs := w.candidates(a)
		// The first candidate after cur.
		j, found := slices.BinarySearchFunc(cs, cur, compareStrBytes)
		if found {
			j++
		}
		if j < len(cs) && (best == "" || cs[j] < best) {
			best = cs[j]
		}
	}
	// cur is the character's own bytes: an invalid one reads as U+FFFD, and
	// only what sorts after its bytes moves the walk on.
	if m, ok := nextMark(w.cur[i]); ok && compareStrBytes(m, cur) > 0 && (best == "" || m < best) {
		best = m
	}
	if best == "" { // succ of the prefix, which is never empty here
		last := len(w.skip) - 1
		if w.skip[last] == 0xFF { // invalid UTF-8 has no successor byte: read on
			return nil
		}
		if w.skip[last]++; 'A' <= w.skip[last] && w.skip[last] <= 'Z' {
			w.skip[last] = '['
		}
		return w.skip
	}
	w.skip = append(w.skip, best...)
	return w.skip
}

// appendLower appends b as NOCASE reads it: ASCII A-Z as a-z.
func appendLower(dst, b []byte) []byte {
	for _, c := range b {
		dst = append(dst, lowerByte(c))
	}
	return dst
}

// compareStrBytes is strings.Compare(s, string(b)) without the copy.
func compareStrBytes(s string, b []byte) int {
	for i := 0; i < len(s) && i < len(b); i++ {
		if s[i] != b[i] {
			return cmp.Compare(s[i], b[i])
		}
	}
	return cmp.Compare(len(s), len(b))
}

// candidates is every character that folds to a, as NOCASE orders them: its
// encoding, an ASCII capital read as its lowercase twin. Once per walk.
func (w *editWalk) candidates(a rune) []string {
	if cs, ok := w.cands[a]; ok {
		return cs
	}
	var cs []string
	for _, c := range wildcard.Folds(a) {
		if c >= utf8.RuneSelf || c == a {
			cs = append(cs, lowerASCII(string(c)))
		}
	}
	slices.Sort(cs)
	cs = slices.Compact(cs)
	if w.cands == nil {
		w.cands = map[rune][]string{}
	}
	w.cands[a] = cs
	return cs
}

// nextMark is the encoding of the least nonspacing mark (wildcard.Marks)
// above r.
func nextMark(r rune) (string, bool) {
	ms := wildcard.Marks()
	i, _ := slices.BinarySearchFunc(ms, r, func(m [2]rune, r rune) int { return cmp.Compare(m[1], r+1) })
	if i == len(ms) {
		return "", false
	}
	return string(max(ms[i][0], r+1)), true
}

// step appends the row for one more folded character c of the headword and
// returns its smallest entry.
//
// Only the band within max of the diagonal is computed (Ukkonen 1985): an
// entry k of row j is at least |j-k|, so outside the band it is past the
// budget whatever its value. The entries just outside it hold max+1, which is
// all the next row reads of them; the rest are never read.
func (w *editWalk) step(c rune) int {
	n := w.n
	j := len(w.keyF) + 1 // the row being added
	w.keyF = append(w.keyF, c)
	base := len(w.rows)
	w.rows = slices.Grow(w.rows, n+1)[:base+n+1]
	row, up := w.rows[base:], w.rows[base-(n+1):base]
	lo, hi := max(1, j-w.max), min(n, j+w.max)
	row[0] = j
	if lo > 1 {
		row[lo-1] = w.max + 1
	}
	if hi < n {
		row[hi+1] = w.max + 1
	}
	low := j
	for k := lo; k <= hi; k++ {
		cost := 1
		if w.q[k-1] == c {
			cost = 0
		}
		v := min(up[k]+1, row[k-1]+1, up[k-1]+cost)
		if j > 1 && k > 1 && c == w.q[k-2] && w.keyF[j-2] == w.q[k-1] {
			v = min(v, w.rows[base-2*(n+1)+k-2]+1)
		}
		row[k] = v
		low = min(low, v)
	}
	return low
}

// Distances is the optimal string alignment distance of each word from q as
// SimilarKeys measures it - case and accents ignored (wildcard.Fold) - or
// maxDist+1 for one farther: one table for all of them.
func Distances(q string, maxDist int, words []string) []int {
	w := newEditWalk(primary(q), maxDist, 0)
	out := make([]int, len(words))
	for i, word := range words {
		w.reset()
		d, far := 0, false
		for _, r := range word {
			if f := wildcard.Fold(r); f >= 0 {
				if w.step(f) > maxDist {
					far = true
					break
				}
				d++
			}
		}
		out[i] = maxDist + 1
		if v, ok := w.last(d); !far && ok {
			out[i] = v
		}
	}
	return out
}

// similarMemo keeps the last answers of SimilarKeys on an open handle, which
// a reindex replaces: nothing outlives the headwords it was read from.
type similarMemo struct {
	mu   sync.Mutex
	keys []string
	vals [][]string
}

const similarMemoSize = 16

func (m *similarMemo) get(k string) ([]string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, x := range m.keys {
		if x == k {
			return m.vals[i], true
		}
	}
	return nil, false
}

func (m *similarMemo) put(k string, v []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.keys) == similarMemoSize {
		m.keys, m.vals = m.keys[1:], m.vals[1:]
	}
	m.keys, m.vals = append(m.keys, k), append(m.vals, v)
}
