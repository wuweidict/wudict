// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/wuweidict/wudict/internal/dict"
	"github.com/wuweidict/wudict/internal/wildcard"
)

// WildcardContext returns the entries whose headword fits a wildcard pattern
// (internal/wildcard, D167), ranked (rank, D181). Entry headwords only:
// aliases and `@` sub-entries are not matched (D174).
func (s *Store) WildcardContext(ctx context.Context, pattern string, limit int) ([]dict.Result, error) {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return nil, nil
	}
	p, err := wildcard.Parse(pattern)
	if err != nil {
		return nil, err
	}
	return s.ranked(ctx, p, limit, scope{})
}

// scope is what a search reads besides the entry headwords.
type scope struct {
	aliases bool // the other spellings an entry lists: exact and prefix search
	subs    bool // `@_` sub-entries: exact search, how an article's link fetches one
}

// ranked runs rank and reads the articles of what it found.
func (s *Store) ranked(ctx context.Context, p wildcard.Pattern, limit int, sc scope) ([]dict.Result, error) {
	ids, err := s.rank(ctx, p, clamp(limit), sc)
	if err != nil || len(ids) == 0 {
		return nil, err
	}
	return s.entriesByID(ctx, ids)
}

// rank returns the first n entries a pattern matches, best first (D181).
// Case and accents rank, they never exclude. The order is by reading, in
// Tiers order - as typed first when a capital was typed, accents kept first
// when one was - and within one reading:
//
//   - first the word the pattern spells - all of it for a query without
//     wildcards, every `*` empty for one with stars and no `?` (Spelled):
//     `nacion*` lists nación before nacional;
//   - then everything else it matches;
//   - in dictionary order (collated), by the key that matched.
//
// The spelled word as typed leads them all, a lowercase query included
// (SpelledTiers): `march` lists march before March. layout numbers these
// classes.
//
// Every class is one list per table, filled by passes over the index in
// order, each stopping as soon as the classes it fills settle the first n:
//
//   - the spelled word: one pass over its seek (planner.seek), read whole,
//     since its matches can sit anywhere in it - *nación* sorts after every
//     ASCII continuation of *nacion*;
//   - the as-typed reading, when it ranks first and the pattern has a head:
//     SQLite's GLOB over the range of that head, so only the hits leave
//     SQLite;
//   - the other readings: one pass over the pattern's seek, matched in Go.
//
// Aliases (exact and prefix) go through SQLite's own NOCASE comparison and
// GLOB only, never the folded reading: folding every alias in a range is
// what D174 measured 2-80x slower.
func (s *Store) rank(ctx context.Context, p wildcard.Pattern, n int, sc scope) ([]int64, error) {
	spelled, ok := p, p.Literal()
	if !ok {
		spelled, ok = p.Spelled()
	}
	var tiers0, tiers []wildcard.Reading
	if ok {
		tiers0 = spelled.SpelledTiers()
	}
	if !p.Literal() {
		tiers = p.Tiers()
	}
	l := newLayout(tiers0, tiers)
	pl := s.newPlanner()
	ent := newRanker(n, l.classes)
	if tiers0 != nil {
		if err := s.entryPass(ctx, pl, ent, spelled, tiers0, l.spelled, sc.subs); err != nil {
			return nil, err
		}
	}
	// GLOB reads the range of the pattern's head; without a head it would
	// read the whole index in a pass of its own, which the Go pass reads
	// anyway: there, the as-typed reading is Go's.
	glob := tiers != nil && tiers[0] == wildcard.AsTyped && p.Head() != ""
	if tiers != nil {
		rest := l.rest
		if glob {
			if !ent.settled(rest[0]) {
				if err := s.entryGlob(ctx, ent, p, rest[0], sc.subs); err != nil {
					return nil, err
				}
			}
			rest = l.afterGlob()
		}
		if !ent.settled(slices.Min(rest)) {
			if err := s.entryPass(ctx, pl, ent, p, tiers, rest, sc.subs); err != nil {
				return nil, err
			}
		}
	}
	head := lowerASCII(p.Head())
	if !sc.aliases || head == "" {
		return merge(n, ent.lists, nil), nil
	}
	al := newRanker(n, l.classes)
	if tiers0 != nil {
		k := keyRange{head, head + "\x01"} // the keys NOCASE-equal to the word
		if err := s.aliasPass(ctx, al, k, head, "", spelled.MatcherFor(tiers0), l.spelled, sc.subs); err != nil {
			return nil, err
		}
	}
	if tiers != nil {
		k := keyRange{head, succ(head)} // the keys that start with it
		rest := l.rest
		if glob {
			if !al.settled(rest[0]) {
				if err := s.aliasPass(ctx, al, k, head, p.Glob(), nil, rest[:1], sc.subs); err != nil {
					return nil, err
				}
			}
			rest = l.afterGlob()
		}
		if !al.settled(slices.Min(rest)) {
			if err := s.aliasPass(ctx, al, k, head, "", p.MatcherFor(tiers), rest, sc.subs); err != nil {
				return nil, err
			}
		}
	}
	return merge(n, ent.lists, al.lists), nil
}

// layout numbers rank's classes, best first: the class of each reading of
// the spelled word (SpelledTiers) and of the rest of the pattern (Tiers).
// Without a rest - a query with no wildcard - the spelled word's readings
// are the classes. Otherwise each reading of Tiers holds two, the spelled
// word's and the rest's, and the spelled word as typed, when Tiers does not
// begin with it, comes before them all.
type layout struct {
	spelled, rest []int
	classes       int
}

func newLayout(tiers0, tiers []wildcard.Reading) layout {
	var l layout
	next := func() int { l.classes++; return l.classes - 1 }
	if tiers == nil {
		for range tiers0 {
			l.spelled = append(l.spelled, next())
		}
		return l
	}
	if tiers0 != nil && tiers0[0] == wildcard.AsTyped && tiers[0] != wildcard.AsTyped {
		l.spelled = append(l.spelled, next())
	}
	for range tiers {
		if tiers0 != nil {
			l.spelled = append(l.spelled, next())
		}
		l.rest = append(l.rest, next())
	}
	return l
}

// afterGlob is the rest's classes for the Go pass that follows the GLOB
// pass: a headword Go reads as typed and GLOB did not - a cut UTF-8
// sequence (TestWildcardTruncatedUTF8) - files with the next reading.
func (l layout) afterGlob() []int { return append([]int{l.rest[1]}, l.rest[1:]...) }

// hit is one matched key: the entry, the row it was read from - the entry's
// own id for a headword, the alias row's id for an alias - and the key.
type hit struct {
	id, row int64
	key     string
}

// ranker gathers one table's matches by class, best class first. What it
// counts is entries, not rows: an entry the table reaches by several keys -
// the aliases of one word - counts once, in its best class.
//
// A class holds at most n entries, and a pass stops once the classes up to
// the lowest it fills hold n. That is enough for merge: the n entries of a
// full list are n distinct entries of the merged classes up to it, so the
// merged first n end within it, in either table.
type ranker struct {
	n     int
	lists [][]hit
	in    []map[int64]struct{} // the entries of each list
	best  map[int64]int        // each entry's best class
	per   []int                // the entries whose best class is each class
	taken map[int64]struct{}   // rows already filed, by the passes before
}

func newRanker(n, classes int) *ranker {
	r := &ranker{n: n, lists: make([][]hit, classes), in: make([]map[int64]struct{}, classes),
		best: map[int64]int{}, per: make([]int, classes), taken: map[int64]struct{}{}}
	for i := range r.in {
		r.in[i] = map[int64]struct{}{}
	}
	return r
}

// upto counts the entries in the classes up to and including c.
func (r *ranker) upto(c int) int {
	t := 0
	for _, k := range r.per[:c+1] {
		t += k
	}
	return t
}

// settled reports that the classes before c hold n entries: a pass that
// fills c and later classes cannot change the first n.
func (r *ranker) settled(c int) bool { return c > 0 && r.upto(c-1) >= r.n }

// file puts h in class c unless a pass has classed its row already, or its
// entry is in that class already, at an earlier key; a class holds at most n
// entries. A row is classed once, by the first pass that matches it, kept or
// not: a later pass must not move a row a full class turned away into a
// better one. It reports whether the pass can stop: the classes up to stop
// hold n.
func (r *ranker) file(h hit, c, stop int) bool {
	_, filed := r.taken[h.row]
	r.taken[h.row] = struct{}{}
	_, dup := r.in[c][h.id]
	if !filed && !dup && len(r.lists[c]) < r.n {
		r.in[c][h.id] = struct{}{}
		r.lists[c] = append(r.lists[c], h)
		if b, ok := r.best[h.id]; !ok || c < b {
			if ok {
				r.per[b]--
			}
			r.best[h.id] = c
			r.per[c]++
		}
	}
	return r.upto(stop) >= r.n
}

// merge interleaves the two tables' lists of each class by matched key, in
// dictionary order - a headword before an alias with the same key - and
// keeps every entry once,
// at its best place: an entry its headword and an alias both match is one
// article.
func merge(n int, ent, al [][]hit) []int64 {
	seen := map[int64]struct{}{}
	var ids []int64
	for c := range ent {
		x := ent[c]
		var y []hit
		if al != nil {
			y = al[c]
		}
		for len(x)+len(y) > 0 && len(ids) < n {
			var h hit
			if len(y) == 0 || (len(x) > 0 && collCompare(x[0].key, y[0].key) <= 0) {
				h, x = x[0], x[1:]
			} else {
				h, y = y[0], y[1:]
			}
			if _, ok := seen[h.id]; !ok {
				seen[h.id] = struct{}{}
				ids = append(ids, h.id)
			}
		}
	}
	return ids
}

// keyRange is a slice of a NOCASE index (idx_entry_w, idx_alias_w): lo
// inclusive, hi exclusive; an empty bound is open.
type keyRange struct{ lo, hi string }

// where renders the range as a condition on w, and the arguments of its
// bounds.
func (k keyRange) where() (string, []any) {
	switch {
	case k.lo == "" && k.hi == "":
		return "", nil
	case k.hi == "":
		return " AND w >= ? COLLATE NOCASE", []any{k.lo}
	}
	return " AND w >= ? COLLATE NOCASE AND w < ? COLLATE NOCASE", []any{k.lo, k.hi}
}

// succ is the first string past every string that starts with s, which is
// not empty, in NOCASE order: a UTF-8 sequence ends in a byte below 0xC0, so
// its last byte plus one is a byte; one that NOCASE would read as a lowercase
// letter - after `@` - is `[`, the next byte NOCASE leaves as it is.
func succ(s string) string {
	b := s[len(s)-1] + 1
	if 'A' <= b && b <= 'Z' {
		b = '['
	}
	return s[:len(s)-1] + string([]byte{b})
}

// lowerASCII is what NOCASE compares: ASCII A-Z read as a-z, every other
// byte as it is.
func lowerASCII(s string) string {
	b := []byte(s)
	for i, c := range b {
		if 'A' <= c && c <= 'Z' {
			b[i] = c + 'a' - 'A'
		}
	}
	return string(b)
}

// seek is the part of idx_entry_w a pattern reads: leaves, sorted and
// disjoint, and whether the headwords led by a nonspacing mark are read too
// (marks).
type seek struct {
	leaves []leaf
	marks  bool
}

// Running a range costs ~5 µs on mattn, ~6.4 on the pure-Go driver, against
// ~0.33 µs for each headword it hands to Go (docs.local/PHASES.md
// 2026-10-22), so the planner splits only what is dense, and counts the
// candidates of one step in one statement.
const (
	denseRows  = 48 // a range with at most this many headwords is read whole
	probeBatch = 32 // ranges one statement counts
	probeSmall = 8  // ... or this many, for a step with few candidates
)

// seekBudget is the number of candidate ranges counted for one plan; past
// it, what is left is read as it stands. A variable, for the tests.
var seekBudget = 192

// planner plans the seeks of one search. It remembers what it counted: the
// spelled word's seek and the pattern's share their first steps.
type planner struct {
	s      *Store
	counts map[keyRange]int
}

func (s *Store) newPlanner() *planner { return &planner{s: s, counts: map[keyRange]int{}} }

// A cand is a range the planner considers; its keys all start with p. A node
// holds the headwords that start with p, whose characters fold to run[:j]; a
// lump, the non-ASCII continuations of p where the run goes on with an ASCII
// character, split when dense; a gap, continuations that may be a mark, read
// whole.
type cand struct {
	kind int
	p    string
	j    int
	r    keyRange
}

const (
	candNode = iota
	candLump
	candGap
)

func nodeAt(p string, j int) cand { return cand{kind: candNode, p: p, j: j, r: prefixRange(p)} }

// seek returns the slices of idx_entry_w that hold every headword the
// pattern can match in any reading. Each of them folds to something that
// starts with the pattern's folded run (wildcard.Pattern.Run), so its first
// character folds to the run's first, and so on: the plan follows the run
// one character a step, splitting only what is dense and dropping what is
// empty.
//
//   - The lead: the range of each character that folds to the run's first
//     (wildcard.Folds: c, C, ç, Ć, …), and the headwords led by a mark, which
//     Fold drops (D167 Am. 2). NOCASE folds ASCII A-Z only, so "c" and "C"
//     are one range and ç is another.
//   - A dense node, where the run goes on with an ASCII character: its range
//     and one lump for every non-ASCII continuation (an accented form, a
//     mark), the lump split into the forms that fold to it and the marks
//     when it is dense too; where the run goes on with a non-ASCII
//     character: the range of each character that folds to it - a capital
//     included, which a dictionary that files its headwords in capitals
//     reads - and the gaps a mark can be in (markGaps).
//   - The end: the run itself, and it followed by marks, when the pattern
//     ends there; everything that starts with it otherwise.
//
// A pattern that starts with a wildcard reads the whole index; a lead of
// U+FFFD, also what invalid UTF-8 reads as, every non-ASCII headword.
func (pl *planner) seek(ctx context.Context, p wildcard.Pattern) (seek, error) {
	run, end := p.Run()
	switch {
	case len(run) == 0 && end: // nothing but marks: a match is all marks
		return seek{marks: true}, nil
	case len(run) == 0:
		return seek{leaves: []leaf{{}}}, nil
	case run[0] == utf8.RuneError:
		return seek{leaves: []leaf{{r: keyRange{lo: "\x80"}}}}, nil
	}
	leafOf := func(c cand) []leaf {
		if c.kind == candNode && c.j == len(run) && end {
			return []leaf{{r: keyRange{c.p, c.p + "\x01"}, q: c.p}, {r: keyRange{c.p + "\x80", succ(c.p)}, q: c.p}}
		}
		return []leaf{{r: c.r, q: c.p}}
	}
	var level []cand
	for _, c := range wildcard.Folds(run[0]) {
		if c >= utf8.RuneSelf || c == run[0] { // an ASCII twin is in the lead's NOCASE range
			level = append(level, nodeAt(string(c), 1))
		}
	}
	var out []leaf
	probed := 0
	for len(level) > 0 {
		if probed += len(level); probed > seekBudget {
			for _, c := range level {
				out = append(out, leafOf(c)...)
			}
			break
		}
		counts, err := pl.count(ctx, level)
		if err != nil {
			return seek{}, err
		}
		var next []cand
		for i, c := range level {
			switch n := counts[i]; {
			case n == 0:
			case n <= denseRows || c.kind == candGap || c.j == len(run) || run[c.j] == utf8.RuneError:
				out = append(out, leafOf(c)...)
			case c.kind == candLump:
				next = append(next, foldsAfter(c.p, run[c.j], c.j)...)
			case run[c.j] < utf8.RuneSelf:
				next = append(next, nodeAt(c.p+string(run[c.j]), c.j+1),
					cand{kind: candLump, p: c.p, j: c.j, r: keyRange{c.p + "\x80", succ(c.p)}})
			default:
				next = append(next, foldsAfter(c.p, run[c.j], c.j)...)
			}
		}
		level = next
	}
	slices.SortFunc(out, func(a, b leaf) int { return strings.Compare(a.r.lo, b.r.lo) })
	return seek{leaves: out, marks: true}, nil
}

// foldsAfter is what continues p where the run's character j is l: a node
// for each non-ASCII character that folds to l, and the gaps a mark can be
// in. An ASCII l's own node is not among them: it is the lump's sibling.
func foldsAfter(p string, l rune, j int) []cand {
	var folds []rune
	for _, c := range wildcard.Folds(l) {
		if c >= utf8.RuneSelf {
			folds = append(folds, c)
		}
	}
	out := make([]cand, 0, len(folds)+3)
	for _, c := range folds {
		out = append(out, nodeAt(p+string(c), j+1))
	}
	for _, g := range markGaps(p, folds) {
		out = append(out, cand{kind: candGap, p: p, r: g})
	}
	return out
}

// count returns how many headwords each candidate's range holds, up to one
// past denseRows, walking the index only.
func (pl *planner) count(ctx context.Context, cs []cand) ([]int, error) {
	out := make([]int, len(cs))
	var miss []int
	for i, c := range cs {
		if n, ok := pl.counts[c.r]; ok {
			out[i] = n
		} else {
			miss = append(miss, i)
		}
	}
	for len(miss) > 0 {
		// Two statement shapes, padded with empty ranges, not one per count:
		// every connection keeps the statements it ran prepared.
		k := min(len(miss), probeBatch)
		size := probeBatch
		if k <= probeSmall {
			size = probeSmall
		}
		st, err := pl.s.scanStmt(probeQuery(size))
		if err != nil {
			return nil, err
		}
		args := make([]any, 0, 2*size)
		vals := make([]int, size)
		dst := make([]any, size)
		for x := range size {
			if x < k {
				args = append(args, cs[miss[x]].r.lo, cs[miss[x]].r.hi)
			} else {
				args = append(args, "\xff", "\xff")
			}
			dst[x] = &vals[x]
		}
		if err := st.QueryRowContext(ctx, args...).Scan(dst...); err != nil {
			return nil, err
		}
		for x, i := range miss[:k] {
			out[i] = vals[x]
			pl.counts[cs[i].r] = vals[x]
		}
		miss = miss[k:]
	}
	return out, nil
}

// probeQuery counts the headwords of k ranges, each up to one past
// denseRows: one statement, k index seeks.
func probeQuery(k int) string {
	one := fmt.Sprintf("(SELECT count(*) FROM (SELECT 1 FROM entry WHERE w >= ? COLLATE NOCASE AND w < ? COLLATE NOCASE LIMIT %d))", denseRows+1)
	return "SELECT " + strings.TrimSuffix(strings.Repeat(one+",", k), ",")
}

// prefixRange is the range of the keys that start with x, x in NOCASE form.
func prefixRange(x string) keyRange { return keyRange{x, succ(x)} }

// markGaps returns the ranges of the headwords that continue p with a
// non-ASCII character that may be a nonspacing mark: everything but the
// mark-free spans around folds (wildcard.Unmarked), which hold the folds
// themselves - read on their own ranges - and characters no match continues
// with. Byte order is code point order, so a span is a range of encodings.
func markGaps(p string, folds []rune) []keyRange {
	spans := make([][2]rune, 0, len(folds))
	for _, c := range folds {
		lo, hi := wildcard.Unmarked(c)
		spans = append(spans, [2]rune{lo, hi})
	}
	slices.SortFunc(spans, func(a, b [2]rune) int { return int(a[0] - b[0]) })
	var out []keyRange
	lo := p + "\x80"
	for _, sp := range spans {
		if hi := p + cpBytes(sp[0]); lo < hi {
			out = append(out, keyRange{lo, hi})
		}
		lo = max(lo, p+cpBytes(sp[1]+1))
	}
	if top := succ(p); lo < top {
		out = append(out, keyRange{lo, top})
	}
	return out
}

// cpBytes encodes a code point the way UTF-8 orders it, surrogates included,
// for a range bound; one past the last code point encodes past every valid
// sequence.
func cpBytes(r rune) string {
	switch {
	case r < 0x800:
		return string([]byte{0xC0 | byte(r>>6), 0x80 | byte(r&0x3F)})
	case r < 0x10000:
		return string([]byte{0xE0 | byte(r>>12), 0x80 | byte(r>>6&0x3F), 0x80 | byte(r&0x3F)})
	}
	return string([]byte{0xF0 | byte(r>>18), 0x80 | byte(r>>12&0x3F), 0x80 | byte(r>>6&0x3F), 0x80 | byte(r&0x3F)})
}

// joinRanges sorts rs and joins the ones that touch or overlap: adjacent
// characters (É Ê Ë) make one range, the hi of one being the lo of the next.
func joinRanges(rs []keyRange) []keyRange {
	slices.SortFunc(rs, func(a, b keyRange) int { return strings.Compare(a.lo, b.lo) })
	out := rs[:0]
	for _, k := range rs {
		if n := len(out); n > 0 && k.lo <= out[n-1].hi {
			out[n-1].hi = max(out[n-1].hi, k.hi)
			continue
		}
		out = append(out, k)
	}
	return out
}

// spanRanges is the range of the headwords starting with a character in
// lo..hi, both non-ASCII, as one range per UTF-8 length: within one length
// the encoding keeps code point order. A UTF-8 sequence ends in a
// continuation byte (0x80-0xBF), so hi's last byte plus one is the first
// string past every string with hi's prefix.
func spanRanges(lo, hi rune) []keyRange {
	var out []keyRange
	for _, edge := range []rune{0x7FF, 0xFFFF, 0x10FFFF} {
		if lo > edge {
			continue
		}
		top := min(hi, edge)
		h := string(top)
		out = append(out, keyRange{string(lo), h[:len(h)-1] + string([]byte{h[len(h)-1] + 1})})
		if hi <= edge {
			break
		}
		lo = edge + 1
	}
	return out
}

// entryScan is the statement that streams one range of headwords in index
// order. With glob its first argument is the GLOB pattern, and only the
// headwords GLOB accepts leave SQLite; with tail its next two are the bytes
// a headword may end with (Pattern.Tail), and only those, or the ones ending
// in a non-ASCII character, leave SQLite - half the cost of a row, on mattn,
// for the rows no reading matches; with subs the `@_` sub-entries are read
// too.
func entryScan(cond string, glob, tail, subs bool) string {
	w := browseWhere
	if subs {
		w = "1"
	}
	if glob {
		w += " AND w GLOB ?"
	}
	if tail {
		w += " AND (substr(CAST(w AS BLOB), -1) IN (?, ?) OR substr(CAST(w AS BLOB), -1) >= x'80')"
	}
	return "SELECT id, w FROM entry WHERE " + w + cond + browseOrder
}

// aliasScan is entryScan over the alias table: the row, its entry, its key.
func aliasScan(cond string, glob, subs bool) string {
	w := browseWhere
	if subs {
		w = "1"
	}
	if glob {
		w += " AND w GLOB ?"
	}
	return "SELECT rowid, entry_id, w FROM alias WHERE " + w + cond + browseOrder
}

// marksQuery reads every headword that starts with a nonspacing mark: one
// indexed range per wildcard.Marks range, joined in one statement, because
// there are a few hundred of them and almost all are empty. Its bounds are
// constants, written as text literals from their bytes.
var marksQuery = sync.OnceValue(func() string {
	var rs []keyRange
	for _, m := range wildcard.Marks() {
		rs = append(rs, spanRanges(m[0], m[1])...)
	}
	lit := func(s string) string { return fmt.Sprintf("CAST(x'%x' AS TEXT)", s) }
	var b strings.Builder
	b.WriteString("WITH r(lo, hi) AS (VALUES ")
	for i, k := range joinRanges(rs) {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString("(" + lit(k.lo) + "," + lit(k.hi) + ")")
	}
	b.WriteString(") SELECT id, w FROM (" +
		"SELECT e.id AS id, e.w AS w FROM r JOIN entry e ON e.w >= r.lo COLLATE NOCASE AND e.w < r.hi COLLATE NOCASE" +
		") WHERE " + browseWhere + browseOrder)
	return b.String()
})

// markHit is a headword led by a nonspacing mark that a pass matched, and
// the reading it matched in.
type markHit struct {
	hit
	tier int
}

// wildcardMarks returns the headwords starting with a nonspacing mark that m
// matches, in NOCASE order. Such headwords are rare - a dictionary of affixes
// written with a leading combining mark - so they are read whole and merged
// into the range scan.
func (s *Store) wildcardMarks(ctx context.Context, m *wildcard.Matcher) ([]markHit, error) {
	s.marksOnce.Do(func() {
		var found bool
		if s.marksErr = s.db.QueryRow("SELECT EXISTS(" + marksQuery() + ")").Scan(&found); s.marksErr == nil && found {
			s.marksStmt, s.marksErr = s.db.Prepare(marksQuery())
		}
	})
	if s.marksStmt == nil || s.marksErr != nil {
		return nil, s.marksErr
	}
	rows, err := s.marksStmt.QueryContext(ctx)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []markHit
	for rows.Next() {
		var h markHit
		if err := rows.Scan(&h.id, &h.key); err != nil {
			return nil, err
		}
		if h.tier = m.Tier([]byte(h.key)); h.tier >= 0 {
			h.row = h.id
			out = append(out, h)
		}
	}
	return out, rows.Err()
}

// scanStmt is the prepared statement for one query shape - a few dozen of
// them, each run once per range, a search reading up to a few dozen ranges.
func (s *Store) scanStmt(q string) (*sql.Stmt, error) {
	if st, ok := s.scanStmts.Load(q); ok {
		return st.(*sql.Stmt), nil
	}
	st, err := s.db.Prepare(q)
	if err != nil {
		return nil, err
	}
	if prev, loaded := s.scanStmts.LoadOrStore(q, st); loaded {
		st.Close()
		return prev.(*sql.Stmt), nil
	}
	return st, nil
}

// entryPass reads the headwords of p's seek and the ones led by a mark, and
// files each one under classOf the first of tiers it matches, in dictionary
// order (collated).
func (s *Store) entryPass(ctx context.Context, pl *planner, r *ranker, p wildcard.Pattern, tiers []wildcard.Reading, classOf []int, subs bool) error {
	m := p.MatcherFor(tiers)
	sk, err := pl.seek(ctx, p)
	if err != nil {
		return err
	}
	var marks []markHit
	if sk.marks {
		if marks, err = s.wildcardMarks(ctx, m); err != nil {
			return err
		}
	}
	src := source{subs: subs}
	if c, ok := p.Tail(); ok {
		u := c
		if 'a' <= c && c <= 'z' {
			u -= 'a' - 'A'
		}
		src.ends = []any{[]byte{c}, []byte{u}}
	}
	return s.collated(ctx, r, src, sk.leaves, marks, m, classOf)
}

// entryGlob files under class c the headwords that match p as typed, by
// SQLite's GLOB over the range of p's head: only the hits leave SQLite.
func (s *Store) entryGlob(ctx context.Context, r *ranker, p wildcard.Pattern, c int, subs bool) error {
	head := lowerASCII(p.Head())
	return s.collated(ctx, r, source{subs: subs, glob: p.Glob()}, []leaf{{r: prefixRange(head), q: head}}, nil, nil, []int{c})
}

// aliasPass reads the aliases of one range, whose keys all start with head:
// with glob, every row GLOB accepts goes under classOf[0]; otherwise each
// row under classOf the first reading of m it matches; in dictionary order
// (collated).
func (s *Store) aliasPass(ctx context.Context, r *ranker, k keyRange, head, glob string, m *wildcard.Matcher, classOf []int, subs bool) error {
	return s.collated(ctx, r, source{alias: true, subs: subs, glob: glob}, []leaf{{r: k, q: head}}, nil, m, classOf)
}

// entriesByID reads the headword and article of each id, in the order given.
// The bodies are read only here, for the rows that matched, never during the
// scan: rows carry their article inline.
func (s *Store) entriesByID(ctx context.Context, ids []int64) ([]dict.Result, error) {
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := s.db.QueryContext(ctx,
		"SELECT id, w, m FROM entry WHERE id IN (?"+strings.Repeat(",?", len(ids)-1)+")", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byID := make(map[int64]dict.Result, len(ids))
	for rows.Next() {
		var (
			id   int64
			r    dict.Result
			body []byte // TEXT or a compressed BLOB; s.body tells them apart
		)
		if err := rows.Scan(&id, &r.Headword, &body); err != nil {
			return nil, err
		}
		r.Body = s.body.decode(body)
		byID[id] = r
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]dict.Result, 0, len(ids))
	for _, id := range ids {
		if r, ok := byID[id]; ok {
			out = append(out, r)
		}
	}
	return out, nil
}
