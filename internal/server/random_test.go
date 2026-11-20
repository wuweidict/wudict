// Copyright (C) 2026 glowinthedark
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"context"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"

	"github.com/wuweidict/wudict/internal/dict"
)

func TestWordShape(t *testing.T) {
	for w, want := range map[string]bool{
		"psychopomp":          true,
		"airt":                true,  // 4 letters: the floor
		"elk":                 false, // 3: mostly abbreviations at this length
		"free-spirited":       true,
		"o'clock":             true,
		"rock’n’roll":         true,
		"Kauderwelsch":        true,
		"ПОДСОБЛЯТЬ":          true,
		"corazón":             true,
		"corazón":            true, // decomposed: the mark is not a letter
		"中国":                  true,
		"字":                   true,
		"コンピューター":             true,
		"나라":                  true,
		"silver iodide":       false, // a phrase
		"LVAD2":               false,
		"архан.":              false, // an abbreviation
		"-ish":                false,
		"ab--cd":              false,
		"word-":               false,
		"'tis":                false,
		"":                    false,
		"́abc":                false, // a mark before any letter
		"hippopotomonstroses": false, // 19 letters
		"中华人民共和国国务院":          false, // 10 Han characters
	} {
		if got := wordShape(w); got != want {
			t.Errorf("wordShape(%q) = %v, want %v", w, got, want)
		}
	}
}

func TestCaseOf(t *testing.T) {
	for w, want := range map[string]wordCase{
		"casa":      caseLower,
		"o'clock":   caseLower,
		"Haus":      caseTitle,
		"Valdivia":  caseTitle,
		"ВРЕМЯ":     caseCaps,
		"LVAD":      caseCaps,
		"McDonald":  caseMixed,
		"iPhone":    caseMixed,
		"Jean-Paul": caseMixed,
		"ǅamija":    caseTitle, // a titlecase digraph is a capital
		"中国":        caseNone,
		"":          caseNone,
	} {
		if got := caseOf(w); got != want {
			t.Errorf("caseOf(%q) = %v, want %v", w, got, want)
		}
	}
}

func TestLexical(t *testing.T) {
	zim := func(name string, header ...dict.Field) dict.Meta {
		return dict.Meta{Name: name, Format: "wudict:zim", Header: header}
	}
	for _, tc := range []struct {
		name string
		m    dict.Meta
		want bool
	}{
		{"an mdx", dict.Meta{Name: "Collins", Format: "wudict:mdx"}, true},
		{"a dsl, not indexed", dict.Meta{Name: "Apresyan", Format: "dsl"}, true},
		{"a wiktionary by its machine name", zim("Viccionari", dict.Field{Name: "Name", Value: "wiktionary_ca_all"}), true},
		{"a wiktionary by its tags", zim("Wikționar", dict.Field{Name: "Tags", Value: "wiktionary;_category:wiktionary"}), true},
		{"a wiktionary by its title", zim("Wiktionary (es)"), true},
		{"a wiki", zim("Bitcoin Wiki", dict.Field{Name: "Name", Value: "bitcoin_en_all"}, dict.Field{Name: "Tags", Value: "bitcoin;_pictures:no"}), false},
		{"documentation", zim("HTTP Docs", dict.Field{Name: "Tags", Value: "devdocs;http"}), false},
		{"a zim, not indexed", dict.Meta{Name: "Wikipedia", Format: "zim"}, false},
	} {
		if got := lexical(tc.m); got != tc.want {
			t.Errorf("%s: lexical = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// fakeSampler draws its words in turn, whatever u says, so a test reads as a
// script; Has answers from the words plus has.
type fakeSampler struct {
	words []string
	has   []string
	next  int
}

func (f *fakeSampler) Sample(_ context.Context, _ uint64) (string, error) {
	if len(f.words) == 0 {
		return "", dict.ErrNotFound
	}
	w := f.words[f.next%len(f.words)]
	f.next++
	return w, nil
}

func (f *fakeSampler) Has(_ context.Context, w string) (bool, error) {
	eq := func(x string) bool { return strings.EqualFold(x, w) }
	return slices.ContainsFunc(f.words, eq) || slices.ContainsFunc(f.has, eq), nil
}

// testDrawer draws from fake dictionaries, nil meaning one that cannot be
// drawn from, with seeded randomness and a lemma for "jostled". Each lists its
// headwords in langs[i] (all "en" when langs is short); none is open or off.
func testDrawer(seed uint64, langs []string, dicts ...*fakeSampler) *drawer {
	srcs := make([]*source, len(dicts))
	lang := func(i int) string {
		if i < len(langs) {
			return langs[i]
		}
		return "en"
	}
	for i, f := range dicts {
		if f != nil {
			srcs[i] = &source{id: string(rune('a' + i)), lang: lang(i), stamp: "1", sm: f}
		}
	}
	return &drawer{
		n:    len(srcs),
		open: func(i int) *source { return srcs[i] },
		lang: lang,
		held: func(int) bool { return false },
		off:  func(int) bool { return false },
		rnd:  rand.New(rand.NewPCG(seed, 1)).Uint64,
		lemma: func(code, w string) (string, bool) {
			if code == "en" && w == "jostled" {
				return "jostle", true
			}
			return "", false
		},
		memo: &randomMemo{},
	}
}

func drawIn(seed uint64, lang string, dicts ...*fakeSampler) (pick, bool) {
	langs := make([]string, len(dicts))
	for i := range langs {
		langs[i] = lang
	}
	return testDrawer(seed, langs, dicts...).draw(context.Background())
}

func drawFrom(seed uint64, dicts ...*fakeSampler) (pick, bool) {
	return drawIn(seed, "en", dicts...)
}

func TestDraw(t *testing.T) {
	for seed := range uint64(20) {
		// Every dud of the measured sample is passed over for the one word.
		duds := &fakeSampler{
			words: []string{"Valdivia", "LVAD", "en este documento", "jostled", "psychopomp"},
			has:   []string{"jostle"},
		}
		if got, ok := drawFrom(seed, duds); !ok || got.word != "psychopomp" {
			t.Errorf("seed %d: duds drew %q, %v; want psychopomp", seed, got.word, ok)
		}

		// A capital is a German noun, and a name in any language that does
		// not capitalise nouns, however many of the dictionary's headwords
		// have one (an encyclopaedia's do). An all-capitals headword is a word
		// in Dahl's, where every headword is in capitals.
		de := &fakeSampler{words: []string{"Haus", "Kanzleideutsch", "sanft", "LKW"}}
		if got, ok := drawIn(seed, "de", de); !ok || got.word == "LKW" {
			t.Errorf("seed %d: German drew %q, %v", seed, got.word, ok)
		}
		encyclopaedia := &fakeSampler{words: []string{"Segovia", "Callimachus", "Lappeenranta", "glacier"}}
		if got, ok := drawFrom(seed, encyclopaedia); !ok || got.word != "glacier" {
			t.Errorf("seed %d: encyclopaedia drew %q, %v; want glacier", seed, got.word, ok)
		}
		dahl := &fakeSampler{words: []string{"ВРЕМЯ", "КУШАТЬ", "архан."}}
		if got, ok := drawIn(seed, "ru", dahl); !ok || got.word == "архан." {
			t.Errorf("seed %d: Dahl drew %q, %v", seed, got.word, ok)
		}

		// A word a second dictionary lists wins over one only its own has.
		narrow := &fakeSampler{words: []string{"beldam"}, has: []string{"psychopomp"}}
		wide := &fakeSampler{words: []string{"psychopomp"}}
		if got, ok := drawFrom(seed, narrow, wide); !ok || got.word != "psychopomp" || got.src.id != "b" {
			t.Errorf("seed %d: breadth drew %q from %v, %v", seed, got.word, got.src, ok)
		}
		// With no such word, the first that passed is kept.
		a, b := &fakeSampler{words: []string{"beldam"}}, &fakeSampler{words: []string{"airt"}}
		if got, ok := drawFrom(seed, a, b); !ok || (got.word != "beldam" && got.word != "airt") {
			t.Errorf("seed %d: narrow drew %q, %v", seed, got.word, ok)
		}
		// A dictionary that cannot be drawn from is not one to compare with.
		if got, ok := drawFrom(seed, nil, &fakeSampler{words: []string{"beldam"}}, nil); !ok || got.word != "beldam" {
			t.Errorf("seed %d: beside undrawable dictionaries drew %q, %v", seed, got.word, ok)
		}
	}
	// Nothing to draw: the loop ends, with nothing.
	for name, dicts := range map[string][]*fakeSampler{
		"no dictionary":        nil,
		"none drawable":        {nil, nil},
		"empty":                {{}},
		"nothing passes":       {{words: []string{"LVAD", "silver iodide"}}},
		"empty beside nothing": {{}, nil},
	} {
		if got, ok := drawFrom(1, dicts...); ok {
			t.Errorf("%s: drew %q", name, got.word)
		}
	}
}

func TestStems(t *testing.T) {
	for _, tc := range []struct {
		lang, w string
		want    []string
	}{
		{"en", "shysters", []string{"shyster"}},
		{"en", "workboxes", []string{"workbox", "workboxe"}},
		{"en", "irresistibilities", []string{"irresistibility"}},
		{"en", "desulphured", []string{"desulphur", "desulphure"}},
		{"en", "stopped", []string{"stopp", "stop", "stoppe"}},
		{"en", "running", []string{"runn", "run", "runne"}},
		{"en", "making", []string{"mak", "make"}},
		{"en", "glass", nil},
		{"en", "virus", nil},
		{"en", "crisis", nil},
		{"en", "psychopomp", nil},
		{"en", "Shysters", nil}, // capitalised: not asked
		{"es", "méritos", []string{"mérito"}},
		{"es", "noticias", []string{"noticia"}},
		{"de", "Häuser", nil},
		{"ru", "слова", nil},
	} {
		if got := stems(tc.lang, tc.w); !slices.Equal(got, tc.want) {
			t.Errorf("stems(%s, %q) = %q, want %q", tc.lang, tc.w, got, tc.want)
		}
	}
	// A rare plural the lemma data does not list is passed over.
	for seed := range uint64(10) {
		chambers := &fakeSampler{words: []string{"shysters", "workboxes", "psychopomp"}, has: []string{"shyster", "workbox"}}
		if got, ok := drawFrom(seed, chambers); !ok || got.word != "psychopomp" {
			t.Errorf("seed %d: drew %q, %v; want psychopomp", seed, got.word, ok)
		}
	}
}

// Breadth counts the same language only: a loanword both an English and a
// Spanish dictionary list is not preferred over any other word.
func TestDrawBreadthSameLanguage(t *testing.T) {
	for seed := range uint64(20) {
		en := &fakeSampler{words: []string{"fiasco", "psychopomp", "beldam"}}
		es := &fakeSampler{words: []string{"sobaco"}, has: []string{"fiasco"}}
		d := testDrawer(seed, []string{"en", "es"}, en, es)
		// Each dictionary is alone in its language, so the first word that
		// passes is taken - whichever it is, never because of the other.
		got, ok := d.draw(context.Background())
		if !ok {
			t.Fatalf("seed %d: nothing drawn", seed)
		}
		first := map[string]string{"a": "fiasco", "b": "sobaco"}[got.src.id]
		if got.word != first {
			t.Errorf("seed %d: drew %q from %s, want its first word %q", seed, got.word, got.src.id, first)
		}
	}
}

// What the breadth test may open: nothing of another language, nothing that
// is off and closed, open dictionaries first, and at most breadthOpens new
// ones. Index 0 is the word's own dictionary.
func TestWideBounds(t *testing.T) {
	ctx := context.Background()
	setup := func(n int, langs []string, held, off map[int]bool, has map[int]bool) (*drawer, *[]int) {
		fakes := make([]*fakeSampler, n)
		for i := range fakes {
			fakes[i] = &fakeSampler{words: []string{"x" + string(rune('a'+i))}}
			if has[i] {
				fakes[i].has = []string{"psychopomp"}
			}
		}
		d := testDrawer(1, langs, fakes...)
		var opened []int
		open := d.open
		d.open = func(i int) *source { opened = append(opened, i); return open(i) }
		d.held = func(i int) bool { return held[i] }
		d.off = func(i int) bool { return off[i] }
		d.srcs, d.opened, d.live, d.langs = make([]*source, n), make([]bool, n), make([]int, n), make([]*string, n)
		for i := range d.live {
			d.live[i] = i
		}
		d.get(0) // the word's own, drawn from
		opened = opened[:0]
		return d, &opened
	}

	// Another language is never opened, so a word alone in its language is wide.
	d, opened := setup(3, []string{"en", "es", "ru"}, nil, nil, map[int]bool{1: true, 2: true})
	if !d.wide(ctx, 0, "psychopomp") || len(*opened) != 0 {
		t.Errorf("other languages: opened %v", *opened)
	}
	// A dictionary that is off and closed is not opened for this.
	d, opened = setup(2, nil, nil, map[int]bool{1: true}, map[int]bool{1: true})
	if !d.wide(ctx, 0, "psychopomp") || len(*opened) != 0 {
		t.Errorf("off: opened %v", *opened)
	}
	// One that is off but open already is asked.
	d, _ = setup(2, nil, map[int]bool{1: true}, map[int]bool{1: true}, nil)
	if d.wide(ctx, 0, "psychopomp") {
		t.Error("off and open: not asked")
	}
	// An open dictionary that lists the word answers before any is opened.
	d, opened = setup(6, nil, map[int]bool{5: true}, nil, map[int]bool{5: true})
	if !d.wide(ctx, 0, "psychopomp") || len(*opened) != 1 || (*opened)[0] != 5 {
		t.Errorf("open first: opened %v", *opened)
	}
	// Twenty closed dictionaries, none with the word: at most breadthOpens opened.
	d, opened = setup(21, nil, nil, nil, nil)
	if d.wide(ctx, 0, "psychopomp") || len(*opened) != breadthOpens || d.newOpens != breadthOpens {
		t.Errorf("cap: opened %d (%v), newOpens %d, want %d", len(*opened), *opened, d.newOpens, breadthOpens)
	}
}

func TestCaseHabitRelearned(t *testing.T) {
	h := &randomMemo{}
	ctx, rnd := context.Background(), rand.New(rand.NewPCG(1, 1)).Uint64
	src := &source{id: "a", stamp: "1", sm: &fakeSampler{words: []string{"Haus", "Baum", "sanft"}}}
	if c := h.of(ctx, src, rnd); c != caseTitle {
		t.Fatalf("German habit = %v, want title", c)
	}
	// Reindexed: the same id, another database.
	src = &source{id: "a", stamp: "2", sm: &fakeSampler{words: []string{"haus", "baum", "Sanft"}}}
	if c := h.of(ctx, src, rnd); c != caseLower {
		t.Errorf("after a reindex, habit = %v, want lower", c)
	}
}

func TestRandomEndpoint(t *testing.T) {
	s := newTestServer(t)
	id := idOf(t, s, "test.dsl")
	// Not indexed yet: nothing is opened, nothing drawn.
	if rec := getJSON(t, s, "/api/random?dict="+id, nil); rec.Code != 204 {
		t.Fatalf("before indexing: status %d, want 204", rec.Code)
	}
	preparedDSL(t, s)
	for _, q := range []string{"dict=" + id, "dict=nope," + id, ""} {
		var got randomResponse
		rec := getJSON(t, s, "/api/random?"+q, &got)
		if rec.Code != 200 || (got.Word != "casa" && got.Word != "corazón") || got.Dict != id || got.Name == "" {
			t.Errorf("%q: status %d, %+v", q, rec.Code, got)
		}
	}
	if rec := getJSON(t, s, "/api/random?dict=nope", nil); rec.Code != 404 {
		t.Errorf("unknown dictionary: status %d, want 404", rec.Code)
	}
}
