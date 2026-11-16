// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"bytes"
	"container/heap"
	"context"
	"database/sql"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/wuweidict/wudict/internal/wildcard"
)

// Within one reading, rank lists its matches in dictionary order (D181
// Am. 1), the order of DIN 5007-1 (Variante 1, for dictionaries), of the
// Duden ("Die Umlaute ä, ö, ü, äu werden wie die nicht umgelauteten Vokale
// … behandelt. Kleinbuchstaben werden vor Großbuchstaben eingeordnet") and
// of the Unicode Collation Algorithm's three levels (UTS #10: role < Role <
// rôle):
//
//  1. the letters, case and accents ignored (wildcard.Fold): kohlen before
//     Kohler and Köhler, which tie here;
//  2. between words that tie there, the one without the accent first:
//     Kohler before Köhler, cafe before café;
//  3. between words that still tie, lowercase before capital: über before
//     Über, march before March.
//
// ß is a letter of its own, not ss (O2): Maß sorts after Masse and Mast.

// Collate is collCompare for a list made of several dictionaries' keys: the
// order each of them returns its own in.
func Collate(a, b string) int { return collCompare(a, b) }

// collCompare orders two keys in dictionary order, then by their bytes.
func collCompare(a, b string) int {
	if c := compareRunes(primary(a), primary(b)); c != 0 {
		return c
	}
	return collTie(a, b)
}

// primary is a key's first-level form: every character through Fold, the
// ones it drops left out.
func primary(s string) []rune {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if f := wildcard.Fold(r); f >= 0 {
			out = append(out, f)
		}
	}
	return out
}

func compareRunes(a, b []rune) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return int(a[i]) - int(b[i])
		}
	}
	return len(a) - len(b)
}

// collTie orders two keys with the same primary form: accents (the
// lowercased characters, marks included, an unaccented letter's code point
// below its accented forms'), then case (lowercase first, character by
// character), then bytes.
func collTie(a, b string) int {
	la, lb := []rune(strings.ToLower(a)), []rune(strings.ToLower(b))
	if c := compareRunes(la, lb); c != 0 {
		return c
	}
	ra, rb := []rune(a), []rune(b)
	for i := 0; i < len(ra) && i < len(rb); i++ {
		if ca, cb := capital(ra[i]), capital(rb[i]); ca != cb {
			if ca {
				return 1
			}
			return -1
		}
	}
	return strings.Compare(a, b)
}

func capital(r rune) bool { return unicode.IsUpper(r) || unicode.IsTitle(r) }

// leaf is one slice of an index a pass reads, and the bytes every key in it
// starts with, ASCII lowercased: what lets the pass tell how far each key
// agrees with the others.
type leaf struct {
	r keyRange
	q string

	// The rows of r a chunk has read already, and the last of them, after
	// which reading resumes: a block a chunk ended inside is not read twice.
	pre    []crow
	resume *crow
}

// crow is a row a pass has read: tier -1 when the matcher rejects it.
type crow struct {
	h    hit
	tier int
}

// source is the table a pass reads and what SQLite filters before a row
// leaves it: the `@_` sub-entries, the as-typed GLOB, the last byte
// (Pattern.Tail).
type source struct {
	alias bool
	subs  bool
	glob  string
	ends  []any
}

// query is the statement that reads up to a number of rows of one range in
// index order, and its arguments but the number.
func (src source) query(k keyRange) (string, []any) {
	cond, bounds := k.where()
	var (
		q    string
		args []any
	)
	if src.glob != "" {
		args = append(args, src.glob)
	}
	if src.alias {
		q = aliasScan(cond, src.glob != "", src.subs)
	} else {
		q = entryScan(cond, src.glob != "", src.ends != nil, src.subs)
		args = append(args, src.ends...)
	}
	return q + " LIMIT ?", append(args, bounds...)
}

// The chunks a pass reads a range in: small first, since most searches want
// five results, then larger, since a pass that keeps reading is one whose
// matches are sparse.
const (
	chunkFirst = 64
	chunkMax   = 1024
)

// pqItem is a row a pass has read and matched, or a range it has not read
// yet. lb is a row's primary form, or a lower bound of the primary forms of
// a range's rows: the primary form of the bytes they all start with, or
// more when the range is known to continue past them with larger letters.
type pqItem struct {
	lb   []rune
	rng  *leaf
	h    hit
	tier int
}

type pq []pqItem

func (p pq) Len() int { return len(p) }

// Less puts a range before every row its lower bound does not exceed, so
// that a row leaves the queue only when no unread row can sort before it.
func (p pq) Less(i, j int) bool {
	a, b := p[i], p[j]
	if c := compareRunes(a.lb, b.lb); c != 0 {
		return c < 0
	}
	if (a.rng != nil) != (b.rng != nil) {
		return a.rng != nil
	}
	if a.rng != nil {
		return a.rng.r.lo < b.rng.r.lo
	}
	if c := collTie(a.h.key, b.h.key); c != 0 {
		return c < 0
	}
	return a.h.row < b.h.row
}
func (p pq) Swap(i, j int) { p[i], p[j] = p[j], p[i] }
func (p *pq) Push(x any)   { *p = append(*p, x.(pqItem)) }
func (p *pq) Pop() any {
	old := *p
	x := old[len(old)-1]
	*p = old[:len(old)-1]
	return x
}

// collated is one pass: it reads the leaves and files every row m matches
// - every row, without m - under classOf the reading it matched in, in
// dictionary order, and stops once the classes up to the lowest it fills
// hold n.
//
// A range is read a chunk at a time in index order. The rows of a chunk fall
// into blocks by the character after the range's common bytes; index order
// keeps a block together, so every block before the chunk's last is
// complete and its rows can be queued. The last block becomes a range of its
// own, one character longer, and what follows it becomes ranges by what the
// next character can fold to (remainder). A row leaves the queue in
// dictionary order because nothing still unread can sort before it.
func (s *Store) collated(ctx context.Context, r *ranker, src source, leaves []leaf, marks []markHit, m *wildcard.Matcher, classOf []int) error {
	low := slices.Min(classOf)
	q := &pq{}
	for i := range leaves {
		heap.Push(q, pqItem{lb: primary(leaves[i].q), rng: &leaves[i]})
	}
	for _, h := range marks {
		heap.Push(q, pqItem{lb: primary(h.key), h: h.hit, tier: h.tier})
	}
	size := chunkFirst
	for q.Len() > 0 {
		it := heap.Pop(q).(pqItem)
		if it.rng == nil {
			if r.file(it.h, classOf[it.tier], low) {
				return nil
			}
			continue
		}
		if err := s.expand(ctx, q, src, *it.rng, m, size); err != nil {
			return err
		}
		size = min(2*size, chunkMax)
	}
	return nil
}

// expand reads one chunk of a range and queues its complete blocks' matches
// and the ranges that cover the rest. The chunk's last block becomes a range
// carrying the rows read of it; a chunk that is one block is split again one
// character deeper, on the rows already read, until it is not. No row is
// read twice, but the few NOCASE-equal to the one reading resumes after.
func (s *Store) expand(ctx context.Context, q *pq, src source, lf leaf, m *wildcard.Matcher, size int) error {
	rows := lf.pre
	k := lf.r
	if lf.resume != nil {
		k.lo = max(k.lo, lf.resume.h.key)
	}
	got := 0
	err := s.readRange(ctx, src, k, size, func(h hit, w []byte) {
		got++
		if r := lf.resume; r != nil && h.row <= r.h.row && lowerASCII(string(w)) == lowerASCII(r.h.key) {
			return // read before, by the chunk that ended here
		}
		t := 0
		if m != nil {
			t = m.Tier(w)
		}
		h.key = string(w)
		rows = append(rows, crow{h, t})
	})
	if err != nil {
		return err
	}
	push := func(rs []crow) {
		for _, r := range rs {
			if r.tier >= 0 {
				heap.Push(q, pqItem{lb: primary(r.h.key), h: r.h, tier: r.tier})
			}
		}
	}
	if got < size {
		push(rows)
		return nil
	}
	last := rows[len(rows)-1]
	for {
		// rows[first:] is the last block at this depth.
		cur, end := blockOf([]byte(last.h.key), lf.q)
		first := len(rows) - 1
		for first > 0 {
			b, e := blockOf([]byte(rows[first-1].h.key), lf.q)
			if e != end || !bytes.Equal(b, cur) {
				break
			}
			first--
		}
		foldq := primary(lf.q)
		if end {
			// The keys equal to the common bytes fill the chunk: read them
			// all, then the rest after them.
			eq := keyRange{lf.q, lf.q + "\x01"}
			err := s.readRange(ctx, src, eq, -1, func(h hit, w []byte) {
				t := 0
				if m != nil {
					if t = m.Tier(w); t < 0 {
						return
					}
				}
				h.key = string(w)
				heap.Push(q, pqItem{lb: primary(h.key), h: h, tier: t})
			})
			if err != nil {
				return err
			}
			remainder(q, lf, "\x00", foldq)
			return nil
		}
		push(rows[:first])
		child := lf.q + string(cur)
		remainder(q, lf, string(cur), foldq)
		next := leaf{r: clampRange(keyRange{child, succ(child)}, lf.r), q: child, pre: rows[first:], resume: &last}
		if first > 0 {
			heap.Push(q, pqItem{lb: primary(next.q), rng: &next})
			return nil
		}
		lf, rows = next, next.pre
	}
}

// remainder queues the ranges of lf's keys whose character after lf.q
// sorts after c in the index, each with the lower bound its characters
// allow: for ASCII, the next character; for the run of non-ASCII characters
// up to the next nonspacing mark (wildcard.Unmarked), the least they fold to
// (wildcard.MinFold, or U+FFFD, what invalid UTF-8 there reads as); for the
// rest - a mark folds to nothing, so the character after it counts - lf.q's
// own form.
func remainder(q *pq, lf leaf, c string, foldq []rune) {
	with := func(r rune) []rune { return append(slices.Clone(foldq), r) }
	start := succ(lf.q + c)
	var s rune
	from := start
	switch r, size := utf8.DecodeRuneInString(c); {
	case size == 1 && r < utf8.RuneSelf:
		pushRange(q, clampRange(keyRange{start, lf.q + "\x80"}, lf.r), lf.q, with(r+1))
		s, from = utf8.RuneSelf, lf.q+"\x80"
	case r == utf8.RuneError && size == 1:
		pushRange(q, clampRange(keyRange{start, ""}, lf.r), lf.q, foldq)
		return
	default:
		s = r + 1
	}
	if s > unicode.MaxRune || wildcard.InMarks(s) {
		pushRange(q, clampRange(keyRange{from, ""}, lf.r), lf.q, foldq)
		return
	}
	_, e := wildcard.Unmarked(s)
	bound := lf.q + cpBytes(e+1)
	pushRange(q, clampRange(keyRange{from, bound}, lf.r), lf.q, with(min(wildcard.MinFold(s, e), utf8.RuneError)))
	pushRange(q, clampRange(keyRange{bound, ""}, lf.r), lf.q, foldq)
}

// clampRange is k inside within; an empty hi is open.
func clampRange(k, within keyRange) keyRange {
	k.lo = max(k.lo, within.lo)
	switch {
	case k.hi == "":
		k.hi = within.hi
	case within.hi != "":
		k.hi = min(k.hi, within.hi)
	}
	return k
}

// pushRange queues a range unless it is empty.
func pushRange(q *pq, k keyRange, prefix string, lb []rune) {
	if k.hi != "" && k.lo >= k.hi {
		return
	}
	heap.Push(q, pqItem{lb: lb, rng: &leaf{r: k, q: prefix}})
}

// blockOf returns the character of w after the common bytes q, ASCII
// lowercased - one byte for invalid UTF-8 - or end when w is q.
func blockOf(w []byte, q string) (b []byte, end bool) {
	if len(w) <= len(q) {
		return nil, true
	}
	rest := w[len(q):]
	r, size := utf8.DecodeRune(rest)
	if size == 1 && r < utf8.RuneSelf {
		if 'A' <= r && r <= 'Z' {
			r += 'a' - 'A'
		}
		return []byte{byte(r)}, false
	}
	return rest[:size], false
}

// readRange reads up to limit rows of one range in index order (all of them
// for a negative limit) and hands each to fn. The key's bytes are valid only
// during the call.
func (s *Store) readRange(ctx context.Context, src source, k keyRange, limit int, fn func(h hit, w []byte)) error {
	text, args := src.query(k)
	st, err := s.scanStmt(text)
	if err != nil {
		return err
	}
	rows, err := st.QueryContext(ctx, append(args, limit)...)
	if err != nil {
		return err
	}
	defer rows.Close()
	var (
		h hit
		w sql.RawBytes
	)
	for rows.Next() {
		if src.alias {
			err = rows.Scan(&h.row, &h.id, &w)
		} else {
			err = rows.Scan(&h.id, &w)
			h.row = h.id
		}
		if err != nil {
			return err
		}
		fn(h, w)
	}
	return rows.Err()
}
