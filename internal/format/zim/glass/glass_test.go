// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package glass

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// The fixture is a single-file glass database written by Xapian itself, the
// way libzim writes an archive's index (testdata/mkfix.cc is its source):
// 3000 documents, document i being
//
//	the house of common{x (i%5)+1} group{i%7} id{i}
//	  + "running quickly" (odd i) or "walked slowly" (even i)
//	  + "rare 日本語" (i a multiple of 100)
//
// stemmed for English, with "the" and "of" as stopwords among 400 others, so
// every assertion below restates a rule instead of comparing against a dump.
func fixtureBytes(t testing.TB) []byte {
	t.Helper()
	f, err := os.Open("testdata/small.glass.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func fixture(t testing.TB) *DB {
	t.Helper()
	b := fixtureBytes(t)
	db, err := Open(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	return db
}

const fixtureDocs = 3000

// walk returns every posting of a term as id -> wdf.
func walk(t *testing.T, db *DB, term string) (ids []uint32, wdf map[uint32]uint32) {
	t.Helper()
	pl, found, err := db.postings(term)
	if err != nil {
		t.Fatalf("%q: %v", term, err)
	}
	wdf = map[uint32]uint32{}
	if !found {
		return nil, wdf
	}
	for pl.did != 0 {
		ids = append(ids, pl.did)
		wdf[pl.did] = pl.wdf
		if _, err := pl.next(); err != nil {
			t.Fatalf("%q after %d: %v", term, ids[len(ids)-1], err)
		}
	}
	// The document-length list leaves its frequency field zero.
	if term != "" && uint32(len(ids)) != pl.docFreq {
		t.Errorf("%q: %d postings, the index says %d", term, len(ids), pl.docFreq)
	}
	return ids, wdf
}

func docsWhere(keep func(i uint32) bool) (ids []uint32) {
	for i := uint32(1); i <= fixtureDocs; i++ {
		if keep(i) {
			ids = append(ids, i)
		}
	}
	return ids
}

func TestVersionAndMetadata(t *testing.T) {
	db := fixture(t)
	if db.DocCount != fixtureDocs || db.LastDocID != fixtureDocs {
		t.Errorf("documents %d, last id %d", db.DocCount, db.LastDocID)
	}
	if db.HasPositions() {
		t.Error("an index written without positions reports them")
	}
	for key, want := range map[string]string{"language": "eng", "kind": "fulltext", "data": "fullPath", "absent": ""} {
		if got, err := db.Metadata(key); err != nil || got != want {
			t.Errorf("Metadata(%q) = %q, %v; want %q", key, got, err, want)
		}
	}
	// The stopword list is a tag longer than one item holds: it is read
	// across its components.
	stops, err := db.Metadata("stopwords")
	if err != nil {
		t.Fatal(err)
	}
	if n := len(strings.Fields(stops)); n != 402 || !strings.HasSuffix(stops, "the\nof\n") {
		t.Errorf("stopwords: %d words, ending %q", n, stops[max(len(stops)-12, 0):])
	}
}

func TestPostings(t *testing.T) {
	db := fixture(t)
	for _, c := range []struct {
		term string
		keep func(i uint32) bool
		wdf  func(i uint32) uint32
	}{
		// In every document: a list of several chunks.
		{"common", func(uint32) bool { return true }, func(i uint32) uint32 { return i%5 + 1 }},
		{"group3", func(i uint32) bool { return i%7 == 3 }, nil},
		{"run", func(i uint32) bool { return i%2 == 1 }, nil},
		{"walk", func(i uint32) bool { return i%2 == 0 }, nil},
		{"rare", func(i uint32) bool { return i%100 == 0 }, nil},
		{"id17", func(i uint32) bool { return i == 17 }, nil},
		{"id3000", func(i uint32) bool { return i == 3000 }, nil},
		// A spaceless script is held as characters and adjacent pairs.
		{"日", func(i uint32) bool { return i%100 == 0 }, nil},
		{"日本", func(i uint32) bool { return i%100 == 0 }, nil},
		{"本語", func(i uint32) bool { return i%100 == 0 }, nil},
		// Stopwords, unstemmed forms and three-character runs are not held.
		{"the", func(uint32) bool { return false }, nil},
		{"running", func(uint32) bool { return false }, nil},
		{"日本語", func(uint32) bool { return false }, nil},
		{"", func(uint32) bool { return true }, func(i uint32) uint32 { // the document lengths
			n := i%5 + 1 + 5 // common, house, group, id, two of running quickly / walked slowly
			if i%100 == 0 {
				n += 6 // rare, and five terms of 日本語
			}
			return n
		}},
	} {
		ids, wdf := walk(t, db, c.term)
		if want := docsWhere(c.keep); !slices.Equal(ids, want) {
			t.Errorf("%q: %d documents, want %d (first %v, want %v)", c.term, len(ids), len(want), head(ids), head(want))
			continue
		}
		if c.wdf != nil {
			for _, i := range ids {
				if wdf[i] != c.wdf(i) {
					t.Fatalf("%q in document %d: %d occurrences, want %d", c.term, i, wdf[i], c.wdf(i))
				}
			}
		}
	}
	var total uint64
	_, lens := walk(t, db, "")
	for _, n := range lens {
		total += uint64(n)
	}
	if total != db.TotalDocLen {
		t.Errorf("the document lengths add up to %d, the version file says %d", total, db.TotalDocLen)
	}
}

func head(ids []uint32) []uint32 { return ids[:min(len(ids), 5)] }

// skipTo from every kind of place - inside a chunk, across one, across many,
// into a gap, past the end - lands where a plain walk would.
func TestSkipTo(t *testing.T) {
	db := fixture(t)
	rng := rand.New(rand.NewSource(1))
	for _, term := range []string{"common", "group3", "run", "rare", ""} {
		ids, wdf := walk(t, db, term)
		for round := 0; round < 400; round++ {
			pl, _, err := db.postings(term)
			if err != nil {
				t.Fatal(err)
			}
			for pl.did != 0 {
				target := pl.did + 1 + uint32(rng.Intn(1+rng.Intn(900)))
				ok, err := pl.skipTo(target)
				if err != nil {
					t.Fatalf("%q skipTo(%d): %v", term, target, err)
				}
				k, _ := slices.BinarySearch(ids, target)
				if k == len(ids) {
					if ok || pl.did != 0 {
						t.Fatalf("%q skipTo(%d) = %d past the last document %d", term, target, pl.did, ids[len(ids)-1])
					}
					break
				}
				if !ok || pl.did != ids[k] || pl.wdf != wdf[ids[k]] {
					t.Fatalf("%q skipTo(%d) = %d (wdf %d), want %d (wdf %d)", term, target, pl.did, pl.wdf, ids[k], wdf[ids[k]])
				}
			}
		}
	}
	for _, c := range []struct {
		term string
		doc  uint32
		want bool
	}{{"group3", 3, true}, {"group3", 2998, false}, {"group3", 2999, true}, {"id3000", 3000, true}, {"id3000", 1, false}, {"nosuch", 5, false}} {
		if got, err := db.Has(c.term, c.doc); err != nil || got != c.want {
			t.Errorf("Has(%q, %d) = %v, %v; want %v", c.term, c.doc, got, err, c.want)
		}
	}
}

func TestDocData(t *testing.T) {
	db := fixture(t)
	for _, i := range []uint32{1, 2, 127, 128, 2999, 3000} {
		if got, err := db.DocData(i); err != nil || got != fmt.Sprintf("C/doc%d", i) {
			t.Errorf("DocData(%d) = %q, %v", i, got, err)
		}
	}
	if _, err := db.DocData(3001); !errors.Is(err, ErrCorrupt) {
		t.Errorf("DocData past the last document: %v", err)
	}
}

func TestSortablePacking(t *testing.T) {
	var prev []byte
	for _, v := range []uint32{0, 1, 0x7fff, 0x8000, 0x8001, 1<<22 - 1, 1 << 22, 1<<29 - 1, 1 << 29, 1<<32 - 1} {
		b := packSortable(nil, v)
		got, err := unpackSortable(b)
		if err != nil || got != v {
			t.Errorf("%#x packs to % x and unpacks to %#x, %v", v, b, got, err)
		}
		if prev != nil && bytes.Compare(prev, b) >= 0 {
			t.Errorf("%#x does not sort after its predecessor: % x, % x", v, prev, b)
		}
		prev = b
	}
}

func TestWords(t *testing.T) {
	for in, want := range map[string][]string{
		"the cat sat":        {"the", "cat", "sat"},
		"don't at&t o’neil":  {"don't", "at&t", "o'neil"},
		"1,5 and 3.14 a.b":   {"1,5", "and", "3.14", "a", "b"},
		"c++ c# f### fish+c": {"c++", "c#", "f###", "fish", "c"},
		"x++++ y":            {"x", "y"},
		"mother-in-law":      {"mother", "in", "law"},
		"snake_case co­op":   {"snake_case", "coop"},
		"ab日本語cd":            {"ab", "日", "日本", "本", "本語", "語", "cd"},
		"  !!  ":             nil,
		"trailing'":          {"trailing"},
	} {
		var got []string
		words(in, func(w string) { got = append(got, w) })
		if !reflect.DeepEqual(got, want) {
			t.Errorf("words(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFold(t *testing.T) {
	for in, want := range map[string]string{
		"Café ÉCOLE": "cafe ecole",
		"Ёлка й":     "елка и",
		"ΟΔΟΣ":       "οδος", // the final sigma, as ICU's Lower writes it
		"İstanbul":   "istanbul",
		"éå":       "ea",
		"plain":      "plain",
	} {
		if got := Fold(in); got != want {
			t.Errorf("Fold(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAnalyzer(t *testing.T) {
	db := fixture(t)
	a, err := db.Analyzer()
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	a.Terms("The Houses of RUNNING Café stop7 "+strings.Repeat("x", 65)+" 日本", func(term string) { got = append(got, term) })
	if want := []string{"hous", "run", "cafe", "日", "日本", "本"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Terms = %q, want %q", got, want)
	}
}

// An index stemmed for a language with no stemmer here is refused, and one in
// a language Xapian does not stem is read as the words themselves.
func TestAnalyzerLanguages(t *testing.T) {
	for language, want := range map[string]struct {
		err  error
		term string
	}{
		"eng":     {nil, "run"},
		"cat":     {ErrUnsupported, ""},
		"jpn":     {nil, "running"},
		"eng,fra": {nil, "running"},
		"":        {nil, "running"},
	} {
		b := fixtureBytes(t)
		at := bytes.Index(b, []byte("\x00\xc0languageeng"))
		if at < 0 {
			t.Fatal("the fixture's language record moved")
		}
		// Rewritten in place: the tag keeps its length, padded with what
		// lang.Normalize does not read as part of a code.
		tag := []byte(language + "   ")[:3]
		if language == "eng,fra" {
			tag = []byte("e,f")
		}
		copy(b[at+len("\x00\xc0language"):], tag)
		db, err := Open(bytes.NewReader(b), int64(len(b)))
		if err != nil {
			t.Fatal(err)
		}
		a, err := db.Analyzer()
		if !errors.Is(err, want.err) {
			t.Errorf("language %q: %v, want %v", language, err, want.err)
			continue
		}
		if err == nil {
			if got := a.Stem("running"); got != want.term {
				t.Errorf("language %q stems running to %q, want %q", language, got, want.term)
			}
		}
	}
}

func TestSearch(t *testing.T) {
	db := fixture(t)
	ctx := context.Background()
	for _, c := range []struct {
		name string
		q    *Query
		keep func(i uint32) bool
	}{
		{"term", Term("group3"), func(i uint32) bool { return i%7 == 3 }},
		{"and", And(Term("common"), Term("group3"), Term("run")), func(i uint32) bool { return i%7 == 3 && i%2 == 1 }},
		{"and of a rare term", And(Term("rare"), Term("group2")), func(i uint32) bool { return i%100 == 0 && i%7 == 2 }},
		{"and with a term the index lacks", And(Term("common"), Term("nosuch")), func(uint32) bool { return false }},
		{"or", Or(Term("rare"), Term("id5"), Term("nosuch")), func(i uint32) bool { return i%100 == 0 || i == 5 }},
		{"or of nothing", Or(), func(uint32) bool { return false }},
		{"and not", AndNot(Term("group3"), Term("run")), func(i uint32) bool { return i%7 == 3 && i%2 == 0 }},
		{"and not everything", AndNot(Term("group3"), Term("common")), func(uint32) bool { return false }},
		{"nested", And(Or(Term("group1"), Term("group2")), AndNot(Term("walk"), Term("rare"))),
			func(i uint32) bool { return (i%7 == 1 || i%7 == 2) && i%2 == 0 && i%100 != 0 }},
	} {
		want := docsWhere(c.keep)
		hits, matched, err := db.Search(ctx, c.q, fixtureDocs)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		got := make([]uint32, len(hits))
		for i, h := range hits {
			got[i] = h.DocID
			if i > 0 && worse(hits[i-1], h) {
				t.Errorf("%s: hit %d outranks the one before it", c.name, i)
			}
		}
		slices.Sort(got)
		if matched != len(want) || !slices.Equal(got, want) {
			t.Errorf("%s: %d matched, %d returned, want %d", c.name, matched, len(got), len(want))
		}
		// A limit keeps the best of them and still counts them all.
		if len(want) > 3 {
			top, n, err := db.Search(ctx, c.q, 3)
			if err != nil || n != len(want) || !reflect.DeepEqual(top, hits[:3]) {
				t.Errorf("%s with limit 3: %v, %d matched, %v", c.name, top, n, err)
			}
		}
	}
}

// BM25 as Xapian computes it. A term in every document weighs next to
// nothing, so among the documents of group3 the shortest rank first - those
// with one "common" - and of equals the lower id comes first.
func TestRanking(t *testing.T) {
	db := fixture(t)
	hits, _, err := db.Search(context.Background(), And(Term("common"), Term("group3")), fixtureDocs)
	if err != nil {
		t.Fatal(err)
	}
	for k, want := range []uint32{10, 45, 80, 115} {
		if hits[k].DocID != want {
			t.Errorf("hit %d is document %d, want %d", k, hits[k].DocID, want)
		}
	}
	if last := hits[len(hits)-1]; hits[0].Score <= last.Score || last.Score <= 0 {
		t.Errorf("best hit %+v, last %+v", hits[0], last)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := db.Search(cancelled, Term("common"), 10); !errors.Is(err, context.Canceled) {
		t.Errorf("a cancelled search: %v", err)
	}
}

func TestExpand(t *testing.T) {
	db := fixture(t)
	got, more, err := db.Expand("group", 10)
	if want := []string{"group0", "group1", "group2", "group3", "group4", "group5", "group6"}; err != nil || more || !reflect.DeepEqual(got, want) {
		t.Errorf("Expand(group) = %q, %v, %v", got, more, err)
	}
	// "common" has chunks keyed after it; they are not terms.
	if got, more, err = db.Expand("com", 10); err != nil || more || !reflect.DeepEqual(got, []string{"common"}) {
		t.Errorf("Expand(com) = %q, %v, %v", got, more, err)
	}
	if got, more, err = db.Expand("id1", 5); err != nil || !more || !reflect.DeepEqual(got, []string{"id1", "id10", "id100", "id1000", "id1001"}) {
		t.Errorf("Expand(id1, 5) = %q, %v, %v", got, more, err)
	}
	if got, more, err = db.Expand("zzz", 5); err != nil || more || got != nil {
		t.Errorf("Expand(zzz) = %q, %v, %v", got, more, err)
	}
}

// A damaged index is an error or a wrong answer, never a panic, a hang or an
// allocation the file chose: every byte of the fixture's first blocks and a
// spread of the rest is overwritten in turn, and the file is cut short at a
// spread of lengths.
func TestDamagedIndex(t *testing.T) {
	orig := fixtureBytes(t)
	use := func(b []byte, what string) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("%s: panic: %v", what, r)
			}
		}()
		db, err := Open(bytes.NewReader(b), int64(len(b)))
		if err != nil {
			return
		}
		if a, err := db.Analyzer(); err == nil {
			a.Terms("running houses", func(string) {})
		}
		ctx := context.Background()
		db.Search(ctx, And(Term("common"), Or(Term("group3"), Term("rare")), Term("run")), 20)
		db.Search(ctx, AndNot(Term("common"), Term("walk")), 20)
		db.Expand("id29", 50)
		db.Has("common", 2999)
		db.DocData(1500)
	}
	rng := rand.New(rand.NewSource(7))
	b := make([]byte, len(orig))
	flip := func(at int) {
		copy(b, orig)
		for _, v := range []byte{0x00, 0xff, orig[at] ^ 0x80, orig[at] + 1} {
			b[at] = v
			use(b, fmt.Sprintf("byte %d = %#x", at, v))
		}
	}
	for at := 0; at < 600; at++ { // the version file
		flip(at)
	}
	for n := 0; n < 1500; n++ {
		flip(rng.Intn(len(orig)))
	}
	for n := 0; n < 200; n++ {
		use(orig[:rng.Intn(len(orig))], "cut short")
	}
}
