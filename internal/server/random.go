// Copyright (C) 2026 glowinthedark
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"context"
	"math/bits"
	"math/rand/v2"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/wuweidict/wudict/internal/dict"
	"github.com/wuweidict/wudict/internal/store"
)

// The random word (D202): one headword drawn from the dictionaries a request
// names, shown under the empty search box. A uniform draw from a real library
// is a dud about every second time - a place name, a phrase, an inflected
// form, an abbreviation (measured on 2,900 draws from 9 prepared
// dictionaries, docs.local/PHASES.md 2026-11-19) - so a drawn word must pass
// three tests before it is shown: its shape, its capitals, and its lemma. A
// word a second dictionary of its language also lists is preferred; one only
// its own dictionary has is kept in case no other turns up. Only headwords
// are read: no test needs an article.
const (
	randomTries  = 12 // draws before the first word that passed is taken
	breadthProbe = 8  // other dictionaries asked whether they list a drawn word
	breadthOpens = 8  // of those, how many a request may open only to ask
	habitSample  = 24 // headwords read to learn whether a dictionary is in capitals
	randomBudget = 3 * time.Second
)

// nounCapitals are the languages that write every noun with a capital, so a
// capitalised headword there is a word, where elsewhere it is a name (the
// encyclopaedias: Segovia, Callimachus). ISO 639-1, as lemmaLangOf gives it.
var nounCapitals = map[string]bool{"de": true, "lb": true}

type randomResponse struct {
	Word string `json:"word"`
	Dict string `json:"dict"`
	Name string `json:"name"`
}

// handleRandom answers GET /api/random?dict=…: `dict` as /api/search reads it.
// 204 when none of the dictionaries can be drawn from - none is indexed, none
// holds words (lexical), or nothing drawn passed - which the page answers by
// showing no word.
//
// Same-origin, deliberately absent from corsAllowed (D69): the extension grant
// is lookup.
func (s *Server) handleRandom(w http.ResponseWriter, r *http.Request) {
	entries, ok := s.searchEntries(w, r.URL.Query().Get("dict"))
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), randomBudget)
	defer cancel()
	d := &drawer{
		n:     len(entries),
		open:  func(i int) *source { return s.randomSource(entries[i]) },
		lang:  func(i int) string { return s.entryLang(entries[i]) },
		held:  func(i int) bool { return entries[i].held() },
		off:   func(i int) bool { return s.reg.prefs != nil && s.reg.prefs.Off(entries[i].ID, entries[i].Path) },
		rnd:   rand.Uint64,
		lemma: s.Morph.LemmaLoaded, // never loads a pack
		memo:  &s.random,
	}
	got, ok := d.draw(ctx)
	if !ok {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(w, randomResponse{Word: got.word, Dict: got.src.id, Name: got.src.name})
}

// source is a dictionary a word may be drawn from: indexed, open, and lexical.
type source struct {
	id, name string
	lang     string // the lemma data its headwords are read with
	stamp    string // the prepared database's identity, for randomMemo
	sm       dict.Sampler
}

// randomSource opens an entry for drawing, or reports nil when it cannot be
// drawn from. The index is checked BEFORE the open, as browse does: opening a
// dictionary that is not indexed builds its direct backend, up to a gigabyte
// of headword map, and a draw never reads one.
func (s *Server) randomSource(e *entry) *source {
	if e.builtin { // the wudict howto: every headword is a phrase
		return nil
	}
	db, ok := validPrepared(e.Path)
	if !ok {
		return nil
	}
	d, err := e.open()
	if err != nil {
		return nil
	}
	sm, ok := d.(dict.Sampler)
	if !ok { // indexed on disk, but this handle is not the prepared backend
		return nil
	}
	m := d.Meta()
	if !lexical(m) {
		return nil
	}
	return &source{id: e.ID, name: m.Name, lang: s.langOf(m.IndexLang, e.Path, m.Name), stamp: dbStamp(db), sm: sm}
}

// langOf is lemmaLangOf with English for no answer, as lemmaWave reads it:
// the one language assumed without evidence.
func (s *Server) langOf(declared, path, name string) string {
	if code := s.lemmaLangOf(declared, path, name); code != "" {
		return code
	}
	return "en"
}

// dbStamp is a prepared database's identity: its path, size and time.
func dbStamp(db string) string {
	if fi, err := os.Stat(db); err == nil {
		return db + "|" + strconv.FormatInt(fi.Size(), 10) + "|" + strconv.FormatInt(fi.ModTime().UnixNano(), 10)
	}
	return db
}

// held reports an entry whose dictionary is open now: asking it costs no open.
func (e *entry) held() bool {
	e.dMu.RLock()
	defer e.dMu.RUnlock()
	return e.d != nil
}

// entryLang is the language of an indexed entry's headwords, read from its
// prepared database's meta table and kept, so the breadth test can choose the
// dictionaries of a word's language without opening the others. "" when the
// entry is not indexed. What /api/dicts derives (dictInfo.LemmaLang) is the
// same answer, but it is not there after a restart: a page holding its list
// is answered 304 and no row is made.
func (s *Server) entryLang(e *entry) string {
	db := backingDB(e.Path)
	if db == "" || e.builtin {
		return ""
	}
	stamp := dbStamp(db)
	if code, ok := s.random.lang(e.ID, stamp); ok {
		return code
	}
	m, err := store.ReadMeta(db)
	if err != nil {
		return ""
	}
	code := s.langOf(m["index_lang"], e.Path, dict.DisplayText(m["name"]))
	s.random.setLang(e.ID, stamp, code)
	return code
}

// lexical reports whether a dictionary holds words rather than articles about
// things: every format but ZIM, and a ZIM archive only when it is a
// Wiktionary. Kiwix names and tags an archive by its project
// ("wiktionary_ca_all", "wiktionary;_category:wiktionary"); the rest of its
// catalogue - Wikipedia, DevDocs, Stack Exchange - is titled by names and
// phrases, not words.
func lexical(m dict.Meta) bool {
	if strings.TrimPrefix(m.Format, "wudict:") != "zim" {
		return true
	}
	if strings.Contains(strings.ToLower(m.Name), "wiktionary") {
		return true
	}
	for _, f := range m.Header {
		if (f.Name == "Name" || f.Name == "Tags") && strings.Contains(strings.ToLower(f.Value), "wiktionary") {
			return true
		}
	}
	return false
}

// drawer is one request's draw: the dictionaries it names, each opened the
// first time it is drawn from or asked about, so a draw over a whole library
// opens a few of them rather than all.
type drawer struct {
	n     int
	open  func(i int) *source // nil: not a dictionary a word can be drawn from
	lang  func(i int) string  // its headwords' language, without an open; "" unknown
	held  func(i int) bool    // open already, so asking it opens nothing
	off   func(i int) bool    // not enabled: never opened only to be asked
	rnd   func() uint64
	lemma func(code, word string) (string, bool)
	memo  *randomMemo

	srcs     []*source
	opened   []bool
	live     []int     // the dictionaries still in the running
	newOpens int       // dictionaries opened only for the breadth test
	langs    []*string // lang(i), asked once a request: wide runs once per word that passes
}

type pick struct {
	word string
	src  *source
}

// index is a uniform choice in [0, n): the high word of u×n.
func index(u uint64, n int) int {
	hi, _ := bits.Mul64(u, uint64(n))
	return int(hi)
}

func (d *drawer) get(i int) *source {
	if !d.opened[i] {
		d.opened[i], d.srcs[i] = true, d.open(i)
	}
	return d.srcs[i]
}

// langOf is lang(i), asked once per request: entryLang costs a stat and a
// read of the library folder's claim per call, and wide asks about every
// dictionary in the pool once per word that passes.
func (d *drawer) langOf(i int) string {
	if d.langs[i] == nil {
		l := d.lang(i)
		d.langs[i] = &l
	}
	return *d.langs[i]
}

// drop removes live[k]. Order does not matter: every choice is random.
func (d *drawer) drop(k int) {
	d.live[k] = d.live[len(d.live)-1]
	d.live = d.live[:len(d.live)-1]
}

// draw picks a dictionary uniformly, then an entry in it uniformly - each
// dictionary is as likely as any other, so one 900,000-entry dictionary does
// not crowd out the rest - until a word passes and a second dictionary lists
// it. A dictionary found to hold nothing drawable leaves the running without
// costing a try, so the loop ends: each turn either spends a try or shortens
// the list.
func (d *drawer) draw(ctx context.Context) (pick, bool) {
	d.srcs, d.opened, d.langs = make([]*source, d.n), make([]bool, d.n), make([]*string, d.n)
	d.live = make([]int, d.n)
	for i := range d.live {
		d.live[i] = i
	}
	var kept pick
	found := false
	for tries := 0; tries < randomTries && len(d.live) > 0 && ctx.Err() == nil; {
		k := index(d.rnd(), len(d.live))
		src := d.get(d.live[k])
		if src == nil {
			d.drop(k)
			continue
		}
		tries++
		w, err := src.sm.Sample(ctx, d.rnd())
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			// Empty, or failing: either way not one to draw from again in
			// this request, where it would spend a try every time.
			d.drop(k)
			continue
		}
		w = strings.TrimSpace(w)
		if !d.fits(ctx, src, w) {
			continue
		}
		if d.wide(ctx, d.live[k], w) {
			return pick{w, src}, true
		}
		if !found {
			kept, found = pick{w, src}, true
		}
	}
	return kept, found
}

// fits applies the tests a drawn word must pass in its own dictionary.
func (d *drawer) fits(ctx context.Context, src *source, w string) bool {
	if !wordShape(w) {
		return false
	}
	switch caseOf(w) {
	case caseMixed:
		return false // McDonald, iPhone: names, not words
	case caseTitle:
		// Every noun in German, a name everywhere else - however many of a
		// dictionary's headwords have one: an encyclopaedia's do.
		if !nounCapitals[src.lang] {
			return false
		}
	case caseCaps:
		// An abbreviation, unless the dictionary files everything in
		// capitals (Dahl): its own habit decides.
		if d.memo.of(ctx, src, d.rnd) != caseCaps {
			return false
		}
	}
	// An inflected form whose lemma the dictionary also lists is a pointer to
	// that entry: "jostled", "remontants" (Chambers files 4 in 10 of its
	// words this way). A form whose lemma it lacks is its own entry.
	if lem, ok := d.lemma(src.lang, w); ok {
		if has, err := src.sm.Has(ctx, lem); err == nil && has {
			return false
		}
	}
	// Lemma data lists the common words, and a dictionary's rare words are
	// the ones it lacks: "shysters", "workboxes" and "devisals" come back
	// as themselves. Their endings give them away instead.
	for _, stem := range stems(src.lang, w) {
		if has, err := src.sm.Has(ctx, stem); err == nil && has {
			return false
		}
	}
	return true
}

// stems strips the regular endings of a lower-case word, for the languages
// whose endings it knows: the English plural, past and -ing forms, the
// Romance plural. Each stem is only a question for the dictionary - a word is
// a form only when its own dictionary lists the stem - so a wrong cut costs
// one index probe, and a right one that removes a real word ("feed", from
// "fee") costs a word that was common anyway. Stems under 3 letters are not
// asked.
func stems(lang, w string) []string {
	if caseOf(w) != caseLower {
		return nil
	}
	var out []string
	add := func(s string) {
		if len([]rune(s)) >= 3 {
			out = append(out, s)
		}
	}
	// cut drops an ending and adds what it leaves, then that stem without a
	// doubled final letter (running → run, stopped → stop).
	cut := func(end, then string) {
		s := strings.TrimSuffix(w, end)
		add(s + then)
		if then == "" {
			if r := []rune(s); len(r) >= 2 && r[len(r)-1] == r[len(r)-2] {
				add(string(r[:len(r)-1]))
			}
		}
	}
	plural := func() {
		switch {
		case strings.HasSuffix(w, "es"):
			cut("es", "")
			cut("s", "")
		case strings.HasSuffix(w, "s") && !strings.HasSuffix(w, "ss") &&
			!strings.HasSuffix(w, "us") && !strings.HasSuffix(w, "is"):
			cut("s", "")
		}
	}
	switch lang {
	case "en":
		switch {
		case strings.HasSuffix(w, "ies"):
			cut("ies", "y")
		case strings.HasSuffix(w, "ied"):
			cut("ied", "y")
		case strings.HasSuffix(w, "ed"):
			cut("ed", "")
			cut("d", "")
		case strings.HasSuffix(w, "ing"):
			cut("ing", "")
			cut("ing", "e")
		default:
			plural()
		}
	case "es", "ca", "fr", "it", "pt", "ro":
		plural()
	}
	return out
}

// wide reports whether another dictionary of the same language lists the word,
// asking at most breadthProbe of them. A word two dictionaries list is an
// established word, not a typo or a one-off coinage, and opening it shows more
// than one article. Only the same language counts: an English and a Spanish
// dictionary share their loanwords, and preferring those would make "fiasco"
// the random word of every bilingual library. With no other dictionary of the
// language to ask, every word is wide.
//
// What it costs is bounded by what it opens, since an opened dictionary stays
// open: the language comes from entryLang, which opens nothing; dictionaries
// open already are asked first; one that is not enabled is never opened for
// this; and a request opens at most breadthOpens. Measured before these
// bounds: 27 opens a request on average over a 185-dictionary library, 185 at
// most, 35 MB held.
func (d *drawer) wide(ctx context.Context, from int, w string) bool {
	lang := d.srcs[from].lang
	var open, closed []int
	for _, j := range d.live {
		switch {
		case j == from || d.langOf(j) != lang:
		case d.opened[j] || d.held(j):
			open = append(open, j)
		case !d.off(j):
			closed = append(closed, j)
		}
	}
	asked := 0
	for _, group := range [][]int{open, closed} {
		for len(group) > 0 && asked < breadthProbe && ctx.Err() == nil {
			k := index(d.rnd(), len(group))
			j := group[k]
			group[k] = group[len(group)-1]
			group = group[:len(group)-1]
			if !d.opened[j] && !d.held(j) {
				if d.newOpens >= breadthOpens {
					continue
				}
				d.newOpens++
			}
			src := d.get(j)
			if src == nil {
				continue
			}
			asked++
			if has, err := src.sm.Has(ctx, w); err == nil && has {
				return true
			}
		}
	}
	return asked == 0
}

// wordShape is one word: letters (any script) and the marks on them, with a
// hyphen or an apostrophe only between two letters; no digit, space or other
// sign. 4 to 18 letters, or 1 to 8 in the scripts that write a word in a few
// characters (Han, kana, Hangul). Under 4 an alphabetic headword is mostly an
// abbreviation; over 18, a compound nobody looks up.
func wordShape(w string) bool {
	letters, short, joined := 0, false, true // joined: the previous rune was a joiner (or there was none)
	for _, r := range w {
		switch {
		case unicode.IsLetter(r):
			letters++
			joined = false
			if unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul) {
				short = true
			}
		case unicode.IsMark(r):
			if letters == 0 {
				return false
			}
		case r == '-' || r == '\'' || r == '’':
			if joined {
				return false
			}
			joined = true
		default:
			return false
		}
	}
	if joined { // empty, or ending on a joiner
		return false
	}
	if short {
		return letters <= 8
	}
	return letters >= 4 && letters <= 18
}

// wordCase is how a word is capitalised.
type wordCase uint8

const (
	caseNone  wordCase = iota // a script without capitals
	caseLower                 // casa
	caseTitle                 // Haus, Valdivia
	caseCaps                  // ВРЕМЯ, LVAD
	caseMixed                 // McDonald, iPhone
)

func caseOf(w string) wordCase {
	upper, lower, first := 0, 0, true
	firstUpper := false
	for _, r := range w {
		if !unicode.IsLetter(r) {
			continue
		}
		switch {
		case unicode.IsUpper(r) || unicode.IsTitle(r): // ǅ, ǈ: a capital (wildcard.go reads them so)
			upper++
			if first {
				firstUpper = true
			}
		case unicode.IsLower(r):
			lower++
		}
		first = false
	}
	switch {
	case upper == 0 && lower == 0:
		return caseNone
	case upper == 0:
		return caseLower
	case lower == 0:
		return caseCaps
	case upper == 1 && firstUpper:
		return caseTitle
	}
	return caseMixed
}

// randomMemo remembers, per dictionary id, what the random word learns about a
// dictionary and would otherwise learn again on every request: how it
// capitalises its headwords and their language. Each answer is checked against
// the prepared database it came from (dbStamp), so a reindexed dictionary is
// learned again. Bounded by the size of the library. The zero value is ready.
type randomMemo struct {
	mu    sync.Mutex
	habit map[string]caseHabit
	langs map[string]langMemo
}

type caseHabit struct {
	stamp string
	c     wordCase
}

type langMemo struct{ stamp, code string }

func (h *randomMemo) lang(id, stamp string) (string, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	x, ok := h.langs[id]
	return x.code, ok && x.stamp == stamp
}

func (h *randomMemo) setLang(id, stamp, code string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.langs == nil {
		h.langs = map[string]langMemo{}
	}
	h.langs[id] = langMemo{stamp, code}
}

// of is the capitalisation most of a dictionary's headwords have, learned from
// habitSample of them: lower case in most dictionaries, a capital initial in
// a German one or an encyclopaedia, all capitals in Dahl's. caseNone when its
// script has no capitals - or when it could not be read, which then admits no
// all-capitals word at all.
func (h *randomMemo) of(ctx context.Context, src *source, rnd func() uint64) wordCase {
	h.mu.Lock()
	x, ok := h.habit[src.id]
	h.mu.Unlock()
	if ok && x.stamp == src.stamp {
		return x.c
	}
	var n [caseMixed + 1]int
	read := 0
	for i := 0; i < habitSample && ctx.Err() == nil; i++ {
		w, err := src.sm.Sample(ctx, rnd())
		if err != nil {
			continue
		}
		n[caseOf(strings.TrimSpace(w))]++
		read++
	}
	c := caseNone
	for _, k := range []wordCase{caseLower, caseTitle, caseCaps} { // ties go to lower case
		if n[k] > n[c] || (c == caseNone && n[k] > 0) {
			c = k
		}
	}
	if read < habitSample { // cut short: answer, but learn it again next time
		return c
	}
	h.mu.Lock()
	if h.habit == nil {
		h.habit = map[string]caseHabit{}
	}
	h.habit[src.id] = caseHabit{src.stamp, c}
	h.mu.Unlock()
	return c
}
