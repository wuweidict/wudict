// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"errors"
	"fmt"
	"math/rand"
	"slices"
	"strings"
	"testing"

	"github.com/wuweidict/wudict/internal/dict"
	"github.com/wuweidict/wudict/internal/wildcard"
)

// wildcardWords holds what the first-character ranges can lose: accented and
// non-Latin initials (which NOCASE sorts after every ASCII headword), a
// leading combining mark, NFD-stored text, Hangul, characters outside the
// BMP, invalid UTF-8, case twins, GLOB's own `[`, and an `@` sub-entry that
// no search may return.
var wildcardWords = []string{
	"apple", "Apple", "APPLE", "apples", "pineapple", "apple pie", "Äpple",
	"cat", "Cat", "cot", "cut", "coat", "çat", "Ćat", "́cat", "ct",
	"café", "café", "Café", "CAFÉ", "cafe", "Köln", "koln",
	"March", "march", "MARCH", "жук", "Жук", "йод", "Йод", "ёлка",
	"한국", "한", "1990s", "[a]", "a]b", "[[x", "x*y", "q?r",
	"@_sub", "@", "ab\xff", "zz", "Zebra", "ǅemal", "džemal",
	"𐐨x", "𐐀x", "ſtar", "star", "by the way", "ice cream", "~tilde", "\x7fdel",
}

func wildcardStore(t *testing.T) *Store {
	t.Helper()
	entries := make([]dict.Entry, len(wildcardWords))
	for i, w := range wildcardWords {
		entries[i] = h(w, "<p>"+w+"</p>")
	}
	r := &fakeReader{meta: dict.Meta{Name: "Wildcard Fixture", Format: "mdx", Path: "/nonexistent/wildcard.mdx"}, entries: entries}
	dbPath := t.TempDir() + "/wildcard.text.db"
	if _, err := IngestPlan(r, dbPath, Plan{}, nil); err != nil {
		t.Fatal(err)
	}
	s, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func headwords(rs []dict.Result) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.Headword
	}
	return out
}

func TestWildcard(t *testing.T) {
	s := wildcardStore(t)
	if !s.Caps().Wildcard {
		t.Fatal("a headword-only database does not offer wildcard search")
	}
	for _, tc := range []struct {
		pattern string
		want    []string // as a set: TestWildcardAgainstBruteForce checks the order
	}{
		{"c?t", []string{"cat", "Cat", "cot", "cut", "çat", "Ćat", "́cat"}},
		{"apple", []string{"apple", "Apple", "APPLE", "Äpple"}},
		{"*apple", []string{"apple", "Apple", "APPLE", "pineapple", "Äpple"}},
		{"cafe", []string{"cafe", "café", "café", "Café", "CAFÉ"}},
		{"koln", []string{"koln", "Köln"}},
		{"иод", []string{"йод", "Йод"}},
		{"ж*", []string{"жук", "Жук"}},
		{"한?", []string{"한국"}},
		{"* *", []string{"apple pie", "by the way", "ice cream"}},
		{"[*", []string{"[[x", "[a]"}},
		{"x*y", []string{"x*y"}},
		{"@*", []string{"@"}}, // never the `@_` sub-entry
		{"ab?", []string{"ab\xff"}},
		{"~*", []string{"~tilde"}},
		// A capital or an accent ranks the matches; it removes none.
		{"March", []string{"March", "march", "MARCH"}},
		{"Café", []string{"cafe", "caf\u00e9", "cafe\u0301", "Café", "CAFÉ"}},
		{"Cafe", []string{"cafe", "caf\u00e9", "cafe\u0301", "Café", "CAFÉ"}},
		{"Ж?к", []string{"жук", "Жук"}},
		{"ǅ*", []string{"ǅemal"}}, // ǅ folds to ǆ, one character; dž is two
		{"[[*", []string{"[[x"}},
	} {
		got, err := s.WildcardContext(bg, tc.pattern, 100)
		if err != nil {
			t.Fatalf("%q: %v", tc.pattern, err)
		}
		hw := headwords(got)
		slices.Sort(hw)
		slices.Sort(tc.want)
		if !slices.Equal(hw, tc.want) {
			t.Errorf("%q = %q, want %q", tc.pattern, hw, tc.want)
		}
	}
}

func TestWildcardBodiesAndLimit(t *testing.T) {
	s := wildcardStore(t)
	got, err := s.WildcardContext(bg, "c?t", 2)
	if err != nil {
		t.Fatal(err)
	}
	if hw := headwords(got); !slices.Equal(hw, []string{"cat", "Cat"}) {
		t.Fatalf("limit 2 = %q, want the first two in browse order", hw)
	}
	if got[1].Body != "<p>Cat</p>" {
		t.Errorf("body of %q = %q", got[1].Headword, got[1].Body)
	}
	if got, err := s.WildcardContext(bg, "  ", 10); err != nil || got != nil {
		t.Errorf("blank pattern = %v, %v", got, err)
	}
	if _, err := s.WildcardContext(bg, strings.Repeat("a", wildcard.MaxLen+1), 10); !errors.Is(err, wildcard.ErrTooLong) {
		t.Errorf("long pattern: err = %v, want ErrTooLong", err)
	}
}

// bkey is one key a search can match: an entry's headword or an alias.
type bkey struct {
	id, row int64
	key     string
	alias   bool
}

// allKeys reads every key of the store in index order; subs keeps the `@_`
// sub-entries.
func allKeys(t *testing.T, s *Store, subs bool) []bkey {
	t.Helper()
	var out []bkey
	for _, q := range []string{
		"SELECT id, id, w, 0 FROM entry ORDER BY w COLLATE NOCASE, id",
		"SELECT entry_id, rowid, w, 1 FROM alias ORDER BY w COLLATE NOCASE, rowid",
	} {
		rows, err := s.db.Query(q)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var k bkey
			if err := rows.Scan(&k.id, &k.row, &k.key, &k.alias); err != nil {
				t.Fatal(err)
			}
			if subs || !isSubEntry(k.key) {
				out = append(out, k)
			}
		}
		rows.Close()
	}
	return out
}

// rankIDs is rank as the brute force states it: the entries, in order.
func rankIDs(s *Store, p wildcard.Pattern, n int, sc scope) ([]int64, error) {
	hits, err := s.rank(bg, p, n, sc)
	return entryIDs(hits), err
}

// bruteRank is rank by its definition (D181), over every key: the class of
// each key, then dictionary order by the key (collCompare), a headword before an alias with
// the same NOCASE key, each entry once. An alias is read only NOCASE-equal
// to the word (the spelled classes) or NOCASE-prefixed by it (the rest).
func bruteRank(p wildcard.Pattern, keys []bkey, n int, aliases bool) []int64 {
	var (
		spelled       wildcard.Pattern
		tiers0, tiers []wildcard.Reading
	)
	if sp, ok := p.Spelled(); p.Literal() {
		spelled, tiers0 = p, p.SpelledTiers()
	} else if ok {
		spelled, tiers0, tiers = sp, sp.SpelledTiers(), p.Tiers()
	} else {
		tiers = p.Tiers()
	}
	l := newLayout(tiers0, tiers)
	head := lowerASCII(p.Head())
	type ck struct {
		bkey
		class int
		prim  []rune
	}
	var all []ck
	var m0s, m1s *wildcard.Matcher
	if tiers0 != nil {
		m0s = spelled.MatcherFor(tiers0)
	}
	if tiers != nil {
		m1s = p.MatcherFor(tiers)
	}
	for _, k := range keys {
		var m0, m1 bool // may take the spelled classes, the rest
		switch {
		case !k.alias:
			m0, m1 = true, true
		case !aliases || head == "":
			continue
		default:
			lk := lowerASCII(k.key)
			m0, m1 = lk == head, strings.HasPrefix(lk, head)
		}
		// a key that spells the word is filed with it
		c := -1
		if m0 && tiers0 != nil {
			if t := m0s.Tier([]byte(k.key)); t >= 0 {
				c = l.spelled[t]
			}
		}
		if c < 0 && m1 && tiers != nil {
			if t := m1s.Tier([]byte(k.key)); t >= 0 {
				c = l.rest[t]
			}
		}
		if c >= 0 {
			all = append(all, ck{k, c, primary(k.key)})
		}
	}
	slices.SortStableFunc(all, func(a, b ck) int {
		if a.class != b.class {
			return a.class - b.class
		}
		if c := compareRunes(a.prim, b.prim); c != 0 {
			return c
		}
		if c := collTie(a.key, b.key); c != 0 {
			return c
		}
		if a.alias != b.alias {
			if a.alias {
				return 1
			}
			return -1
		}
		return int(a.row - b.row)
	})
	seen := map[int64]bool{}
	var ids []int64
	for _, k := range all {
		if !seen[k.id] && len(ids) < n {
			seen[k.id] = true
			ids = append(ids, k.id)
		}
	}
	return ids
}

// TestWildcardAgainstBruteForce is the soundness test of the planner, the
// passes and the merge: for every pattern, the answer must be exactly the
// first entries bruteRank finds reading every headword.
//
//   - the seek reads only part of the index, so a range that lost a
//     headword shows up here;
//   - the as-typed reading is SQLite GLOB when it ranks first, so a
//     difference between GLOB and the Go matcher shows up here;
//   - the passes stop early, so a stop that came too soon shows up here.
func TestWildcardAgainstBruteForce(t *testing.T) {
	s := wildcardStore(t)
	keys := allKeys(t, s, false)
	patterns := []string{"*", "?", "??", "c*", "C*", "*t", "*T", "ä*", "Ä*", "é*", "É*", "́*",
		"[a]", "[*]", "a]*", "*]*", "q?r", "x?y", "ſ*", "s*", "𐐨*", "𐐀*", "ǅ*", "dž*", "한*", "й*", "Й*", "ё*", "е*",
		"cafe", "Cafe", "café", "Café", "cafe*", "Café*", "March", "march", "MARCH*", "apple*", "Apple*", "*apple"}
	rng := rand.New(rand.NewSource(7))
	alpha := []rune("acpeltkoCAÄäéÉçжЖ한[]*?ſ𐐀 ")
	for range 3000 {
		p := make([]rune, 1+rng.Intn(5))
		for j := range p {
			p[j] = alpha[rng.Intn(len(alpha))]
		}
		patterns = append(patterns, string(p))
	}
	for _, n := range []int{maxLimit, 2} {
		for _, pat := range patterns {
			if pat = strings.TrimSpace(pat); pat == "" {
				continue
			}
			p, err := wildcard.Parse(pat)
			if err != nil {
				t.Fatal(err)
			}
			got, err := rankIDs(s, p, n, scope{})
			if err != nil {
				t.Fatalf("%q: %v", pat, err)
			}
			if want := bruteRank(p, keys, n, false); !slices.Equal(got, want) {
				t.Fatalf("%q (limit %d) = %v, brute force %v", pat, n, got, want)
			}
		}
	}
}

// denseStore builds a fixture whose prefixes are dense enough for the seek
// to split them (planSeek, denseRows): stems shared by hundreds of
// headwords, Latin with accents and Cyrillic, marks stored after letters,
// case twins, `@_` sub-entries, aliases of the same shape, and one long
// stem that runs the seek into its budget.
func denseStore(t *testing.T) *Store {
	t.Helper()
	rng := rand.New(rand.NewSource(11))
	stems := []string{"ca", "cas", "casa", "caf", "na", "nac", "nacion", "über", "ub", "ар", "при", "приве", "привет", "ел", "ёл", "е", "ь"}
	latin := []rune("acsnoéáñüuBCÁ")
	cyr := []rune("приветлкаёйИП")
	word := func() string {
		st := stems[rng.Intn(len(stems))]
		tail := latin
		if st[0] >= 0x80 && st != "über" {
			tail = cyr
		}
		var b strings.Builder
		for _, r := range st {
			if rng.Intn(9) == 0 {
				b.WriteString(strings.ToUpper(string(r)))
			} else {
				b.WriteRune(r)
			}
			if rng.Intn(14) == 0 {
				b.WriteRune([]rune{0x301, 0x308}[rng.Intn(2)])
			}
		}
		for range rng.Intn(5) {
			b.WriteRune(tail[rng.Intn(len(tail))])
		}
		if rng.Intn(40) == 0 {
			return "@_" + b.String()
		}
		return b.String()
	}
	const long = "приверединовать"
	var entries []dict.Entry
	for i := range 2500 {
		w := word()
		if i%50 == 0 {
			w = long + string(cyr[rng.Intn(len(cyr))])
		}
		e := dict.Entry{Headwords: []string{w}, Body: "<p>" + w + "</p>", Kind: dict.BodyHTML}
		for range rng.Intn(3) {
			e.Headwords = append(e.Headwords, word())
		}
		entries = append(entries, e)
	}
	for range 60 {
		w := long + string(cyr[rng.Intn(len(cyr))]) + string(cyr[rng.Intn(len(cyr))])
		entries = append(entries, h(w, "<p>"+w+"</p>"))
	}
	// words reached by dozens of their own forms, as in a dictionary that
	// lists every inflection: the aliases of one entry must count once
	for _, w := range []string{"zcasa", "zcosa", "Zcasa", "zcásar"} {
		e := dict.Entry{Headwords: []string{w}, Body: "<p>" + w + "</p>", Kind: dict.BodyHTML}
		for i := range 40 {
			e.Headwords = append(e.Headwords, fmt.Sprintf("zc%c%02d", []rune("aáoA")[rng.Intn(4)], i))
		}
		entries = append(entries, e)
	}
	entries = append(entries, h("zcx", "<p>zcx</p>"))
	// one entry's forms fill the start of a range; another's only alias comes after them
	many := dict.Entry{Headwords: []string{"qq1"}, Body: "<p>qq1</p>", Kind: dict.BodyHTML}
	for i := range 40 {
		many.Headwords = append(many.Headwords, fmt.Sprintf("zcb%02d", i))
	}
	entries = append(entries, many, dict.Entry{Headwords: []string{"qq2", "zcz"}, Body: "<p>qq2</p>", Kind: dict.BodyHTML})
	r := &fakeReader{meta: dict.Meta{Name: "Dense", Format: "mdx", Path: "/nonexistent/dense.mdx"}, entries: entries}
	dbPath := t.TempDir() + "/dense.text.db"
	if _, err := IngestPlan(r, dbPath, Plan{}, nil); err != nil {
		t.Fatal(err)
	}
	s, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// TestRankAgainstBruteForce holds all three modes to bruteRank on a fixture
// dense enough that the seek splits its ranges and follows the run several
// characters deep - and, with a budget cut to a few ranges, stops early.
func TestRankAgainstBruteForce(t *testing.T) {
	s := denseStore(t)
	for _, budget := range []int{seekBudget, 6} {
		defer func(b int) { seekBudget = b }(seekBudget)
		seekBudget = budget
		rankAgainstBruteForce(t, s)
	}
}

func rankAgainstBruteForce(t *testing.T, s *Store) {
	browse, all := allKeys(t, s, false), allKeys(t, s, true)
	rng := rand.New(rand.NewSource(5))
	var words []string
	for _, k := range all {
		if rng.Intn(30) == 0 {
			r := []rune(k.key)
			words = append(words, string(r[:1+rng.Intn(len(r))]))
		}
	}
	words = append(words, "zc", "Zc", "zcá", "zca", "zcb", "zcasa", "nacion", "Nacion", "nación", "über", "Über", "uber", "привет", "Привет", "елка", "ёлка",
		"при", "приве", "приверединовать", "Приверединовать", "c", "C", "ca", "Ca", "cá", "e", "е", "ь")
	alpha := []rune("acsnoéáüuCÁприветёлкИП*?")
	var pats []string
	for range 600 {
		p := make([]rune, 1+rng.Intn(6))
		for j := range p {
			p[j] = alpha[rng.Intn(len(alpha))]
		}
		pats = append(pats, string(p))
	}
	for _, w := range words {
		pats = append(pats, w+"*", "*"+w, w)
	}
	for _, n := range []int{25, 3, 1} {
		for _, w := range words {
			if w = strings.TrimSpace(w); w == "" {
				continue
			}
			for _, prefix := range []bool{false, true} {
				p := wildcard.Literal(w, prefix)
				sc, keys := scope{aliases: true, subs: !prefix}, all
				if prefix {
					keys = browse
				}
				got, err := rankIDs(s, p, n, sc)
				if err != nil {
					t.Fatal(err)
				}
				if want := bruteRank(p, keys, n, true); !slices.Equal(got, want) {
					t.Fatalf("%q prefix %v limit %d = %v, brute force %v", w, prefix, n, got, want)
				}
			}
		}
		for _, pat := range pats {
			p, err := wildcard.Parse(pat)
			if err != nil {
				t.Fatal(err)
			}
			got, err := rankIDs(s, p, n, scope{})
			if err != nil {
				t.Fatal(err)
			}
			if want := bruteRank(p, browse, n, false); !slices.Equal(got, want) {
				t.Fatalf("wildcard %q limit %d = %v, brute force %v", pat, n, got, want)
			}
		}
	}
}

// An entry the aliases reach in a spelled class first and in a better class
// of the rest later moves up: `Café*` ranks the alias Café au lait (as
// typed) above the alias café (the spelled word, case ignored).
func TestRankAliasMovesUp(t *testing.T) {
	entries := []dict.Entry{
		{Headwords: []string{"x1", "café", "Café au lait"}, Body: "<p>x1</p>", Kind: dict.BodyHTML},
		{Headwords: []string{"x2", "café"}, Body: "<p>x2</p>", Kind: dict.BodyHTML},
		{Headwords: []string{"x3", "Café crème"}, Body: "<p>x3</p>", Kind: dict.BodyHTML},
	}
	r := &fakeReader{meta: dict.Meta{Name: "A", Format: "mdx", Path: "/nonexistent/a.mdx"}, entries: entries}
	dbPath := t.TempDir() + "/a.text.db"
	if _, err := IngestPlan(r, dbPath, Plan{}, nil); err != nil {
		t.Fatal(err)
	}
	s, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for n, want := range map[int][]string{3: {"x1", "x3", "x2"}, 2: {"x1", "x3"}, 1: {"x1"}} {
		got, err := s.PrefixContext(bg, "Café", n)
		if err != nil {
			t.Fatal(err)
		}
		if hw := headwords(got); !slices.Equal(hw, want) {
			t.Errorf("limit %d: %q, want %q", n, hw, want)
		}
	}
}

// cpBytes orders like UTF-8 at every length boundary, one past the last code
// point included.
func TestCpBytes(t *testing.T) {
	for _, r := range []rune{0x80, 0x7FF, 0x800, 0xFFFF, 0x10000, 0x10FFFF} {
		if got := cpBytes(r); got != string(r) {
			t.Errorf("cpBytes(%U) = %q, want %q", r, got, string(r))
		}
	}
	if top := cpBytes(0x110000); top <= string(rune(0x10FFFF)) {
		t.Errorf("cpBytes(U+110000) = %q does not sort past U+10FFFF", top)
	}
	if s := cpBytes(0xD800); s <= string(rune(0xD7FF)) || s >= string(rune(0xE000)) {
		t.Errorf("cpBytes(U+D800) = %q is out of order", s)
	}
}

// TestLayout pins the order of the classes: by reading, the spelled word
// first within each, the spelled word as typed first of all.
func TestLayout(t *testing.T) {
	for _, tc := range []struct {
		pattern       string
		spelled, rest []int
	}{
		{"cafe", []int{0, 1}, nil},             // exact: as typed, folded
		{"café*", []int{0, 1, 3}, []int{2, 4}}, // as typed; café… then Café…; cafe…
		{"Café*", []int{0, 2, 4}, []int{1, 3, 5}},
		{"Par*", []int{0, 2}, []int{1, 3}},
		{"nacion*", []int{0, 1}, []int{2}},
		{"c?t", nil, []int{0}},
		{"C?t", nil, []int{0, 1}},
	} {
		p, _ := wildcard.Parse(tc.pattern)
		var tiers0, tiers []wildcard.Reading
		if sp, ok := p.Spelled(); p.Literal() {
			tiers0 = p.SpelledTiers()
		} else if ok {
			tiers0, tiers = sp.SpelledTiers(), p.Tiers()
		} else {
			tiers = p.Tiers()
		}
		l := newLayout(tiers0, tiers)
		if !slices.Equal(l.spelled, tc.spelled) || !slices.Equal(l.rest, tc.rest) {
			t.Errorf("%q: spelled %v rest %v, want %v %v", tc.pattern, l.spelled, l.rest, tc.spelled, tc.rest)
		}
	}
}

// TestPlanSeekSplits: on the dense fixture the seek follows the run - one
// range per ASCII step, the folds and the mark gaps at a non-ASCII one - and
// stops at its budget; a lead with few headwords is read whole.
func TestPlanSeekSplits(t *testing.T) {
	s := denseStore(t)
	for _, tc := range []struct {
		pattern  string
		min, max int // ranges after joining
	}{
		{"nacion", 6, 30},
		{"привет", 10, 1 + seekBudget + 20},
		{"приверединоватьx", 10, 1 + seekBudget + 20},
		{"ьx", 1, 4},
	} {
		sk, err := s.newPlanner().seek(bg, wildcard.Literal(tc.pattern, false))
		if err != nil {
			t.Fatal(err)
		}
		if n := len(sk.leaves); n < tc.min || n > tc.max {
			t.Errorf("%q: %d leaves, want %d..%d", tc.pattern, n, tc.min, tc.max)
		}
	}
}

// TestPlanSeek pins what the planner reads on a sparse index, where every
// lead is read whole and an empty one not at all; TestRankAgainstBruteForce
// proves that it loses nothing.
func TestPlanSeek(t *testing.T) {
	s := wildcardStore(t)
	all := keyRange{}
	// of c and the characters that fold to it (ç Ç, Ć ć … č, Ḉ ḉ), the
	// fixture has c, ç and Ć
	cat := []keyRange{{"c", "d"}, {"ç", "è"}, {"Ć", "ć"}}
	for _, tc := range []struct {
		pattern string
		want    []keyRange
		marks   bool
	}{
		{"*at", []keyRange{all}, false},
		{"?at", []keyRange{all}, false},
		{"cat", cat, true},
		{"Cat", cat, true}, // case ranks: the plan is the folded one
		{"çat*", cat, true},
		{"жук", []keyRange{{"Ж", "З"}, {"ж", "з"}}, true},
		{"vqx", nil, true},                      // no headword starts with v
		{"�*", []keyRange{{lo: "\x80"}}, false}, // what invalid UTF-8 reads as
		{"́", nil, true},                        // a match is all marks
		{"́*", []keyRange{all}, false},
	} {
		p, err := wildcard.Parse(tc.pattern)
		if err != nil {
			t.Fatal(err)
		}
		got, err := s.newPlanner().seek(bg, p)
		if err != nil {
			t.Fatal(err)
		}
		var ranges []keyRange
		for _, l := range got.leaves {
			ranges = append(ranges, l.r)
		}
		if !slices.Equal(ranges, tc.want) || got.marks != tc.marks {
			t.Errorf("%q: ranges %q marks %v, want %q %v", tc.pattern, ranges, got.marks, tc.want, tc.marks)
		}
	}
}

// markGaps leaves out the mark-free span around the folds and covers every
// other non-ASCII continuation.
func TestMarkGaps(t *testing.T) {
	got := markGaps("п", wildcard.Folds('р'))
	want := []keyRange{
		{"п\x80", "п\u0370"},   // U+0080..U+036F: the combining diacriticals
		{"п\u0483", succ("п")}, // U+0483 onward: the Cyrillic combining marks first
	}
	if !slices.Equal(got, want) {
		t.Errorf("markGaps = %q, want %q", got, want)
	}
}

// spanRanges splits at the UTF-8 length boundaries, inside which the
// encoding keeps code point order.
func TestSpanRanges(t *testing.T) {
	got := spanRanges(0x7FE, 0x10001)
	want := []keyRange{
		{"\u07fe", "\xdf\xc0"},
		{"\u0800", "\xef\xbf\xc0"},
		{"\U00010000", "\xf0\x90\x80\x82"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("spanRanges = %q, want %q", got, want)
	}
}

// TestWildcardTruncatedUTF8 pins the one place the two matchers differ: a
// multi-byte sequence cut short is one character to SQLite and one per byte
// to Go. The as-typed reading of a pattern with a capital is SQLite's, the
// others Go's, so each finds the headword in its own reading.
func TestWildcardTruncatedUTF8(t *testing.T) {
	entries := []dict.Entry{h("AB\xe2\x82", "<p>x</p>")}
	r := &fakeReader{meta: dict.Meta{Name: "T", Format: "mdx", Path: "/nonexistent/t.mdx"}, entries: entries}
	dbPath := t.TempDir() + "/t.text.db"
	if _, err := IngestPlan(r, dbPath, Plan{}, nil); err != nil {
		t.Fatal(err)
	}
	s, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, tc := range []struct {
		pattern string
		sqlite  bool // the hit is SQLite's: Go matches it in no reading
	}{{"AB?", true}, {"AB??", false}} {
		got, err := s.WildcardContext(bg, tc.pattern, 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 {
			t.Errorf("%q matched %d", tc.pattern, len(got))
		}
		p, _ := wildcard.Parse(tc.pattern)
		if p.Matcher().Match([]byte("AB\xe2\x82")) == tc.sqlite {
			t.Errorf("Go %q agrees with SQLite; the divergence this test pins is gone", tc.pattern)
		}
	}
}

// TestWildcardReadsTheIndexOnly: the scans must stay on idx_entry_w, in its
// order. A plan that reads the entry table reads every article, and a sort
// would read a whole range before the first headword.
func TestWildcardReadsTheIndexOnly(t *testing.T) {
	s := wildcardStore(t)
	plan := func(q string, args ...any) string {
		t.Helper()
		rows, err := s.db.Query("EXPLAIN QUERY PLAN "+q, args...)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var plan []string
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			plan = append(plan, detail)
		}
		return strings.Join(plan, "; ")
	}
	for _, k := range []keyRange{{}, {lo: "c", hi: "d"}, {lo: "\x80"}} {
		cond, bounds := k.where()
		for _, glob := range []bool{false, true} {
			for _, tail := range []bool{false, true} {
				for _, subs := range []bool{false, true} {
					var args []any
					if glob {
						args = append(args, "c*")
					}
					if tail {
						args = append(args, []byte("n"), []byte("N"))
					}
					q := entryScan(cond, glob, tail, subs) + " LIMIT ?"
					if got := plan(q, append(append(args, bounds...), 64)...); !strings.Contains(got, "COVERING INDEX idx_entry_w") || strings.Contains(got, "TEMP B-TREE") {
						t.Errorf("%s\nplan: %s", q, got)
					}
				}
			}
		}
	}
	// the planner's count walks the index, never the table
	if got := plan(probeQuery(2), "c", "d", "\x80", "\xff"); strings.Count(got, "COVERING INDEX idx_entry_w") != 2 {
		t.Errorf("count plan: %s", got)
	}
	// The aliases: their index, in its order. The fixture holds thousands,
	// so the planner's statistics (ANALYZE at ingest) prefer it, as on a
	// real dictionary; on a handful of rows a scan is the right plan.
	d := denseStore(t)
	for _, k := range []keyRange{{lo: "ca", hi: "cb"}, {lo: "при", hi: "прк"}} {
		cond, bounds := k.where()
		for _, glob := range []bool{false, true} {
			args := bounds
			if glob {
				args = append([]any{"Ca*"}, bounds...)
			}
			q := aliasScan(cond, glob, false) + " LIMIT ?"
			rows, err := d.db.Query("EXPLAIN QUERY PLAN "+q, append(args, 64)...)
			if err != nil {
				t.Fatal(err)
			}
			var steps []string
			for rows.Next() {
				var id, parent, unused int
				var detail string
				if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
					t.Fatal(err)
				}
				steps = append(steps, detail)
			}
			rows.Close()
			if got := strings.Join(steps, "; "); !strings.Contains(got, "idx_alias_w") || strings.Contains(got, "TEMP B-TREE") {
				t.Errorf("%s\nplan: %s", q, got)
			}
		}
	}
	// The marks: a seek per range, never a scan of the whole index.
	if got := plan(marksQuery()); strings.Contains(got, "SCAN e") {
		t.Errorf("marks query scans a whole table or index\nplan: %s", got)
	}
}

// The pattern is the whole headword (D167): the other spellings an entry
// lists, its redirects and its `@_` keys are not matched - exact and prefix
// search find those.
func TestWildcardHeadwordsOnly(t *testing.T) {
	entries := []dict.Entry{
		{Headwords: []string{"go", "went", "gone"}, Body: "<p>go</p>", Kind: dict.BodyHTML},
		{Headwords: []string{"run", "runs"}, Body: "<p>run</p>", Kind: dict.BodyHTML},
		{Headwords: []string{"Zoo", "a zebra"}, Body: "<p>Zoo</p>", Kind: dict.BodyHTML},
		{Headwords: []string{"zebra", "@_zebra"}, Body: "<p>zebra</p>", Kind: dict.BodyHTML},
		{Headwords: []string{"weg"}, LinkTo: "go"},
		{Headwords: []string{"́wendy"}, LinkTo: "run"}, // an alias led by a combining mark
	}
	r := &fakeReader{meta: dict.Meta{Name: "Aliases", Format: "mdx", Path: "/nonexistent/alias.mdx"}, entries: entries}
	dbPath := t.TempDir() + "/alias.text.db"
	if _, err := IngestPlan(r, dbPath, Plan{}, nil); err != nil {
		t.Fatal(err)
	}
	s, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	// the aliases are there: prefix search reads them
	if got, err := s.PrefixContext(bg, "went", 10); err != nil || len(got) != 1 || got[0].Headword != "go" {
		t.Fatalf("prefix went = %+v, %v", got, err)
	}
	for _, tc := range []struct {
		pattern string
		want    []string // in order
	}{
		{"we*", nil}, // went, weg and ́wendy are aliases
		{"wen?", nil},
		{"*zebra", []string{"zebra"}}, // not by "a zebra", and @_zebra is no key
		{"z*", []string{"zebra", "Zoo"}},
		{"g*", []string{"go"}},
		{"run*", []string{"run"}},
	} {
		got, err := s.WildcardContext(bg, tc.pattern, 100)
		if err != nil {
			t.Fatalf("%q: %v", tc.pattern, err)
		}
		if hw := headwords(got); !slices.Equal(hw, tc.want) {
			t.Errorf("%q = %q, want %q", tc.pattern, hw, tc.want)
		}
	}
}
