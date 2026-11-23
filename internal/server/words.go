// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/wuweidict/wudict/internal/facet"
)

// The reader's two word lists beside wudict.toml: the headwords they looked
// up (history.txt) and the ones they starred (favourites.txt). Plain text, one
// headword per line, most recent first - a file to read, edit, diff or carry
// to another tool as it is. They describe the person, not the browser, so
// they live on the server and follow the reader to a phone on the LAN or a
// different port, as state.json does.
//
// A history word is a headword: opening one searches the way typing it does.
// A favourite is a headword in the dictionary it was starred in (D180 Am. 3),
// written "word<TAB>dict id"; a line without the tab is a favourite of the
// word alone, which is what a hand-written line and a list from before the
// dictionary was recorded are, and what a favourite whose dictionary has gone
// opens as. Words are compared without case (sameWord), as the search they
// open compares them: "CASA" and "casa" are one entry, kept in the spelling
// last used.

const (
	HistoryFileName   = "history.txt"
	FavouritesFileName = "favourites.txt"

	// maxWordRunes drops what is not a headword: a pasted paragraph.
	maxWordRunes = 256
	// maxDictBytes bounds a favourite's dictionary id: pathID's 12 hex digits
	// or a built-in's fixed id, with room to spare.
	maxDictBytes = 64
	// maxHistory is how many lookups are kept; the oldest goes first.
	maxHistory = 500
	// maxFavourites only bounds a hostile or runaway client; nobody stars
	// this many words by hand.
	maxFavourites = 10000
	// maxWordsBytes bounds either file: maxFavourites lines of maxWordRunes
	// runes at 4 bytes and a dictionary id is 11 MB; real lists are a few KB.
	maxWordsBytes = 12 << 20
)

// favourite is one line of favourites.txt. Dict "" is the word alone.
type favourite struct {
	Word string `json:"word"`
	Dict string `json:"dict,omitempty"`
}

// wordLists are the two files, and the lock that makes a change to one of
// them a whole read-modify-write.
type wordLists struct {
	history    ownedFile[[]string]
	favourites ownedFile[[]favourite]
	mu         sync.Mutex
}

// wordsFile is one of the two lists' files: a closure over s.User, as
// groups.ini is, because the CLI sets the folder after New.
func wordsFile[T any](s *Server, name string, path func(UserDir) string, parse func(string) ([]T, []facet.Problem)) ownedFile[[]T] {
	return ownedFile[[]T]{
		name: name, path: func() string { return path(s.User) }, max: maxWordsBytes,
		recheck: time.Second, perm: 0o600, // what a person looked up is theirs alone
		parse:  parse,
		absent: func() []T { return nil },
	}
}

func (s *Server) initWords() {
	s.words.history = wordsFile(s, HistoryFileName, UserDir.History, parseWords)
	s.words.favourites = wordsFile(s, FavouritesFileName, UserDir.Favourites, parseFavourites)
}

// cleanWord is w as a stored line, or "" when it is not a headword: line
// breaks and tabs become spaces (a line is one word), the ends are trimmed,
// and invalid UTF-8 or more than maxWordRunes runes is refused.
func cleanWord(w string) string {
	if !utf8.ValidString(w) {
		return "" // checked first: strings.Map would turn a bad byte into U+FFFD
	}
	w = strings.TrimSpace(strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' {
			return ' '
		}
		return r
	}, w))
	if utf8.RuneCountInString(w) > maxWordRunes {
		return ""
	}
	return w
}

// parseWords reads a list: one word per line, blank and invalid lines
// skipped, the first of two equal lines kept. It never fails - a hand edit
// that breaks a line costs that line.
func parseWords(text string) ([]string, []facet.Problem) {
	var out []string
	seen := map[string]bool{}
	for line := range strings.Lines(text) {
		if w := cleanWord(line); w != "" && !seen[strings.ToLower(w)] {
			seen[strings.ToLower(w)] = true
			out = append(out, w)
		}
	}
	return out, nil
}

// cleanDict is d as a stored dictionary id, ok false when it cannot be one:
// an id is a token, so any space or control character, invalid UTF-8 or more
// than maxDictBytes refuses it. "" is the word alone, and fine.
func cleanDict(d string) (string, bool) {
	if len(d) > maxDictBytes || !utf8.ValidString(d) {
		return "", false
	}
	for _, r := range d {
		if r <= ' ' || r == 0x7f {
			return "", false
		}
	}
	return d, true
}

// parseFavourites reads favourites.txt: "word" or "word<TAB>dict id" per line,
// blank and invalid lines skipped, the first of two equal lines kept. A line
// whose dictionary part is not an id keeps its word. It never fails.
func parseFavourites(text string) ([]favourite, []facet.Problem) {
	var out []favourite
	seen := map[string]bool{}
	for line := range strings.Lines(text) {
		w, d, _ := strings.Cut(strings.TrimRight(line, "\r\n"), "\t")
		f := favourite{Word: cleanWord(w)}
		if f.Word == "" {
			continue
		}
		f.Dict, _ = cleanDict(strings.TrimSpace(d))
		if k := favKey(f); !seen[k] {
			seen[k] = true
			out = append(out, f)
		}
	}
	return out, nil
}

// sameWord is the page's comparison too (index.html, wordKey): lower-cased,
// so a word typed in capitals is the entry already there.
func sameWord(a, b string) bool { return strings.ToLower(a) == strings.ToLower(b) }

// favKey is the identity of a favourite: its word without case, and its
// dictionary exactly (an id is not a word).
func favKey(f favourite) string { return strings.ToLower(f.Word) + "\t" + f.Dict }

// putFavourite is list with f moved to the front (on) or taken out (off), at
// most limit long, and whether that changed anything. Starring a word in a
// dictionary replaces the word alone: the precise favourite says all the
// other did. Taking out the word alone (Dict "") takes out every favourite of
// that word, which is how a reader removes a word from the list itself.
func putFavourite(list []favourite, f favourite, on bool, limit int) ([]favourite, bool) {
	out := make([]favourite, 0, len(list)+1)
	if on {
		out = append(out, f)
	}
	k := favKey(f)
	for _, x := range list {
		same := favKey(x) == k ||
			(sameWord(x.Word, f.Word) && ((on && f.Dict != "" && x.Dict == "") || (!on && f.Dict == "")))
		if !same {
			out = append(out, x)
		}
	}
	out = out[:min(len(out), limit)]
	return out, !slices.Equal(out, list)
}

func joinFavourites(list []favourite) string {
	var b strings.Builder
	for _, f := range list {
		b.WriteString(f.Word)
		if f.Dict != "" {
			b.WriteByte('\t')
			b.WriteString(f.Dict)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// putWord is list with w moved to the front (on) or taken out (off), at most
// limit long, and whether that changed anything.
func putWord(list []string, w string, on bool, limit int) ([]string, bool) {
	out := make([]string, 0, len(list)+1)
	if on {
		out = append(out, w)
	}
	for _, x := range list {
		if !sameWord(x, w) {
			out = append(out, x)
		}
	}
	out = out[:min(len(out), limit)]
	return out, !slices.Equal(out, list)
}

func joinWords(list []string) string {
	if len(list) == 0 {
		return ""
	}
	return strings.Join(list, "\n") + "\n"
}

// historyOff is the reader's "Keep history" switch, off. Read here as well
// as on the page, so a page loaded before the switch was turned off cannot
// go on recording.
func (s *Server) historyOff() bool {
	u := s.reg.prefs.UI()
	return u != nil && u.HistoryOff
}

func (s *Server) writeWords(w http.ResponseWriter) {
	// [] on the wire, never null
	favs, hist := s.words.favourites.now().val, s.words.history.now().val
	if favs == nil {
		favs = []favourite{}
	}
	if hist == nil {
		hist = []string{}
	}
	writeJSON(w, map[string]any{"favourites": favs, "history": hist})
}

// GET /api/words - both lists, most recent first.
func (s *Server) handleWords(w http.ResponseWriter, r *http.Request) {
	s.writeWords(w)
}

// POST /api/words - put one word at the front of a list, or take it out,
// and answer both lists as they now stand. A favourite names the dictionary
// it was starred in (dict); without one it is the word alone. A change that
// changes nothing (the word already first) writes nothing.
func (s *Server) handleSaveWord(w http.ResponseWriter, r *http.Request) {
	var req struct {
		List string `json:"list"` // "history" | "favourites"
		Word string `json:"word"`
		Dict string `json:"dict"` // favourites only
		On   bool   `json:"on"`
	}
	if !decodeJSON(w, r, &req, 4<<10) {
		return
	}
	if req.List != "history" && req.List != "favourites" {
		httpErr(w, http.StatusBadRequest, "list must be history or favourites")
		return
	}
	word := cleanWord(req.Word)
	if word == "" {
		httpErr(w, http.StatusBadRequest, "not a word: empty, or longer than %d characters", maxWordRunes)
		return
	}
	dict, ok := cleanDict(req.Dict)
	if !ok || (dict != "" && req.List == "history") {
		httpErr(w, http.StatusBadRequest, "dict must be a dictionary id, and only for favourites")
		return
	}
	if !(req.List == "history" && req.On && s.historyOff()) {
		s.words.mu.Lock()
		var err error
		if req.List == "history" {
			f := &s.words.history
			if list, changed := putWord(f.fresh().val, word, req.On, maxHistory); changed {
				_, err = f.save(joinWords(list), false)
			}
		} else {
			f := &s.words.favourites
			if list, changed := putFavourite(f.fresh().val, favourite{word, dict}, req.On, maxFavourites); changed {
				_, err = f.save(joinFavourites(list), false)
			}
		}
		s.words.mu.Unlock()
		if err != nil {
			httpErr(w, http.StatusInternalServerError, "%s", "could not save: "+err.Error())
			return
		}
	}
	s.writeWords(w)
}

// DELETE /api/words?list=history - empty a list: its file is removed.
func (s *Server) handleClearWords(w http.ResponseWriter, r *http.Request) {
	var remove func() error
	switch r.URL.Query().Get("list") {
	case "history":
		remove = func() error { _, err := s.words.history.remove(); return err }
	case "favourites":
		remove = func() error { _, err := s.words.favourites.remove(); return err }
	default:
		httpErr(w, http.StatusBadRequest, "list must be history or favourites")
		return
	}
	s.words.mu.Lock()
	err := remove()
	s.words.mu.Unlock()
	if err != nil {
		httpErr(w, http.StatusInternalServerError, "%s", "could not remove: "+err.Error())
		return
	}
	s.writeWords(w)
}
