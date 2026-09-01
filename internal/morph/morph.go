// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Package morph turns an inflected word into its dictionary form (O3).
//
// It is used on one path only: a search that found nothing anywhere. That is
// what makes it cheap to be wrong - a candidate is never shown, it is looked up
// in the real headword index first, so a bad lemma costs one B-tree probe on a
// query that had already failed.
//
// # What is built in, and what is not
//
// English, and only English (D87). Every other language is a file the user
// puts in LEMMA_DIR - see file.go, which is also what may replace the English
// one. The six golem packs wudict used to compile in were 9 MB of an 11 MB
// binary, charged to every user and every phone for languages most of them do
// not read; the set of languages people keep dictionaries in has no end, and a
// folder is the only shape that scales to it.
//
// English is the exception because it is the language that is assumed when a
// dictionary declares none (see internal/lang), so it is the one that has to
// answer with nothing installed - and it is by far the cheapest of the six to
// carry: 312 KB of binary against ru's 2.7 MB.
//
// # Why lemma data is loaded lazily
//
// Measured, on a real build of the six golem dicts:
//
//	en 7.0 MB / 25 ms    fr 27.8 MB / ~80 ms    it 34.7 MB / ~90 ms
//	de 35.3 MB / ~90 ms  es 60.0 MB / ~150 ms   ru 65.0 MB / 157 ms
//
// All six resident is 230 MB of heap. Android's default PREVIEW_MEMORY - the
// budget for every unprepared DICTIONARY - is 64 MB, which one Russian pack
// exceeds on its own. Installing a language changes where those bytes come
// from, not how many there are, so lemma data is loaded on first use and
// evicted by an LRU of Cache.max entries, and a phone with MORPH_CACHE=1 holds
// one language at a time. Nothing else in wudict can evict them and they can
// evict nothing else: the registry's janitor owns dictionaries, this owns
// lemma data, and coupling the two would let a search for a Russian word drop
// a dictionary the next keystroke needs.
//
// # Why only LemmaLower
//
// golem's Lemmas() sorts the slice it hands back - which is the same slice it
// keeps - so one call permanently rewrites the lemmatizer's answer for that
// word (Lemma("saw") returns "see", then "saw" forever after), and concurrent
// calls race. Lemma() lower-cases on every call. LemmaLower is the only entry
// point that is both correct and read-only, so it is the only one used, and
// this package does not expose the others.
package morph

import (
	"errors"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/aaaton/golem/v4"
	"github.com/aaaton/golem/v4/dicts/en"
)

// packs is the built-in language list: English, and nothing else (D87). Keys
// are ISO 639-1, as internal/lang returns them. Every other language is a file
// in LEMMA_DIR (see file.go), which may also replace this one.
var packs = map[string]func() golem.LanguagePack{
	"en": en.New,
}

// Supports reports whether this cache will lemmatize a language - because it
// is built in, or because a file in LEMMA_DIR supplies it (D87). Cheap:
// nothing is loaded and nothing is stat'ed, so a caller can group dictionaries
// by language before paying for any of them. It is a method rather than a
// package function precisely because the answer depends on the cache: on what
// the user installed, and on whether MORPH_CACHE left lemmatization on at all.
func (c *Cache) Supports(code string) bool {
	if !c.Enabled() {
		return false // MORPH_CACHE=0: no language is supported, including the built-ins
	}
	if _, ok := c.installed()[code]; ok {
		return true
	}
	_, ok := packs[code]
	return ok
}

type entry struct {
	once sync.Once
	lem  *golem.Lemmatizer
	err  error
}

// load is the expensive part, and runs OUTSIDE Cache.mu - a 157 ms decompress
// under the map lock would stall every other search in the process. It takes
// the pack as a thunk for the same reason: opening and reading a file on disk
// belongs inside the Once, not in the caller that is holding nothing.
func (e *entry) load(open func() golem.LanguagePack) (*golem.Lemmatizer, error) {
	e.once.Do(func() {
		p := open()
		if p == nil {
			e.err = errors.New("no lemma data for this language")
			return
		}
		e.lem, e.err = golem.New(p)
	})
	return e.lem, e.err
}

// Cache holds up to max lemmatizers, evicting least-recently-used.
type Cache struct {
	max int
	dir string // LEMMA_DIR, kept so Rescan knows what to re-read

	// files is the LEMMA_DIR index: ISO 639-1 code -> path. Read on every
	// Supports - which is once per dictionary on every search that found
	// nothing - and written only by Rescan, so it is swapped whole through an
	// atomic pointer rather than guarded by c.mu. A reader gets one map or the
	// other, never a half-built one, and pays nothing for the possibility.
	files atomic.Pointer[map[string]string]

	mu    sync.Mutex
	kept  map[string]*entry
	order []string // least-recently-used first
}

// installed returns the current LEMMA_DIR index, never written by the caller.
func (c *Cache) installed() map[string]string {
	if m := c.files.Load(); m != nil {
		return *m
	}
	return nil
}

// Rescan re-reads LEMMA_DIR, so a language installed while the process is
// running becomes searchable without a restart (`wudict lemmas download`).
//
// Packs already loaded are left alone: a file that replaced one under a
// running server is picked up the next time that language is loaded, not by
// tearing a lemmatizer out from under the searches holding it. A disabled
// cache stays disabled - MORPH_CACHE=0 does not look at the folder at all.
func (c *Cache) Rescan() {
	if !c.Enabled() {
		return
	}
	m := scanDir(c.dir)
	c.files.Store(&m)
}

// New returns a cache holding at most max language packs, reading installed
// lemma files from dir (LEMMA_DIR; "" or a missing folder = built-ins only).
// max <= 0 disables lemmatization entirely: Lemma always reports no answer, no
// pack is ever loaded and the folder is not even looked at, which is what
// MORPH_CACHE=0 buys.
func New(max int, dir string) *Cache {
	c := &Cache{max: max, dir: dir, kept: map[string]*entry{}}
	c.Rescan()
	return c
}

// Enabled reports whether this cache will lemmatize anything.
func (c *Cache) Enabled() bool { return c != nil && c.max > 0 }

// Max is how many packs may be resident at once (MORPH_CACHE). The installer
// page shows it beside each language's measured cost, because "90 MB" means
// something different when one pack is held than when two are.
func (c *Cache) Max() int {
	if c == nil {
		return 0
	}
	return c.max
}

// take returns the entry for code, creating it and evicting as needed. The
// lock is held only for the bookkeeping; loading happens after it is dropped.
//
// Evicting an entry that another goroutine is using is safe: it holds a
// pointer, so the pack stays alive for exactly as long as it is being read and
// is collected afterwards. The cost of evicting a pack still in flight is that
// the next caller reloads it, not a use-after-free.
func (c *Cache) take(code string) *entry {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.kept[code]
	if !ok {
		e = &entry{}
		c.kept[code] = e
	}
	c.touch(code)
	for len(c.order) > c.max {
		oldest := c.order[0]
		c.order = c.order[1:]
		delete(c.kept, oldest)
	}
	return e
}

// touch moves code to the most-recently-used end. Called with c.mu held.
func (c *Cache) touch(code string) {
	for i, k := range c.order {
		if k == code {
			c.order = append(c.order[:i], c.order[i+1:]...)
			break
		}
	}
	c.order = append(c.order, code)
}

// Lemma returns the dictionary form of word in language code, and whether that
// form is worth searching for: false when the language is unsupported, when
// the cache is disabled, when the pack fails to load, and - the common case -
// when the word is already its own lemma or is simply unknown, since golem
// returns the input unchanged for both.
//
// word may be in any case; the result is lower case, which is what the
// headword indexes match on (they are NOCASE).
func (c *Cache) Lemma(code, word string) (string, bool) {
	if !c.Enabled() || !c.Supports(code) {
		return "", false
	}
	w := strings.ToLower(strings.TrimSpace(word))
	if w == "" {
		return "", false
	}
	lem, err := c.take(code).load(func() golem.LanguagePack { return c.pack(code) })
	if err != nil || lem == nil {
		return "", false
	}
	if out := lem.LemmaLower(w); out != w {
		return out, true
	}
	if code == "ru" {
		return yoRetry(lem, w)
	}
	return "", false
}

// yoRetry works around the ru pack being keyed on ё spellings while Russian is
// written with е nearly everywhere: "идёт" lemmatizes, "идет" - the spelling a
// user actually types - does not, and the loss is silent.
//
// One substitution at a time, at most three positions, and only after the
// plain lookup has already failed: ё is a rare letter and a word carrying two
// of them is rarer still, so this is a handful of map probes on a query that
// found nothing, not a combinatorial walk. Words with more than three е are
// left alone rather than given a budget nobody can justify.
func yoRetry(lem *golem.Lemmatizer, w string) (string, bool) {
	const ye, yo = 'е', 'ё'
	r := []rune(w)
	n := 0
	for _, c := range r {
		if c == ye {
			n++
		}
	}
	if n == 0 || n > 3 {
		return "", false
	}
	for i, c := range r {
		if c != ye {
			continue
		}
		r[i] = yo
		cand := string(r)
		r[i] = ye
		if out := lem.LemmaLower(cand); out != cand {
			return out, true
		}
	}
	return "", false
}
