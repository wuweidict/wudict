// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/wuweidict/wudict/internal/dict"
)

// browsePageSize is how many headwords one browse page holds, and it is a
// constant rather than a parameter on purpose: the page number is the whole
// address of a browse view, so a bookmark taken today has to mean the same
// place tomorrow. A client-chosen size would make ?p=37 a different page for
// every reader who changed it.
//
// 300 is a screenful of columns on a desktop and a short scroll on a phone,
// which is the trade a paper page makes too.
const browsePageSize = 300

// browseResp is one page of the word list plus everything needed to draw the
// chrome around it, so a page turn is one request. The strip travels with
// every page (a kilobyte) instead of being a second endpoint the client has to
// sequence against the first.
type browseResp struct {
	Dict  string `json:"dict"`
	Name  string `json:"name"`
	Total int    `json:"total"`
	Page  int    `json:"page"`  // 1-based, as the URL spells it
	Pages int    `json:"pages"` // 0 when the dictionary is empty
	Size  int    `json:"size"`
	// At is the row this view was ASKED for, which is not the row it starts
	// at: a jump snaps down to the page grid, so a page reached by asking for
	// K usually opens in the tail of J. The client highlights the strip by
	// this, or clicking K lights up J.
	At int `json:"at"`
	// Word and Match say where a jump (`at`) landed, so the page can mark it
	// (D203): the headword the typed word spells (match "exact", accents and
	// case aside) or starts ("prefix"), or with "none" the row the typed word
	// would stand before ("" past the last). Absent for a page turn.
	Word     string        `json:"word,omitempty"`
	Match    string        `json:"match,omitempty"`
	Words    []string      `json:"words"`
	Alphabet []dict.Letter `json:"alphabet"`
}

// handleBrowse serves the dictionary as an ordered list of headwords: the
// reading view, as opposed to /api/search's answering view.
//
// Index-backed only (dict.Browser). The order, the offsets and the strip are
// three reads of idx_entry_w; a direct backend would have to sort its whole
// headword list in memory to answer any of them, which is both the slow path
// and the memory spike preparation exists to remove. So an unprepared
// dictionary is answered with 409 and what the client needs to say about it,
// not with a worse browse.
//
// Same-origin, deliberately absent from corsAllowed (D69): the extension grant
// is search, and a full headword dump is not a lookup.
func (s *Server) handleBrowse(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	e, err := s.reg.get(q.Get("dict"))
	if err != nil {
		httpErr(w, 404, "%v", err)
		return
	}
	// Checked BEFORE the open, not after: opening an unprepared dictionary
	// materialises its entire direct backend - up to a gigabyte of headword
	// map on the big ones - and that is a ruinous price for the answer "not
	// yet".
	if _, ok := validPrepared(e.Path); !ok {
		browseUnprepared(w, e)
		return
	}
	d, err := e.open()
	if err != nil {
		httpErr(w, 500, "%v", err)
		return
	}
	b, ok := d.(dict.Browser)
	if !ok { // prepared on disk, but this handle is not the prepared backend
		browseUnprepared(w, e)
		return
	}
	alphabet, err := b.Alphabet()
	if err != nil {
		httpErr(w, 500, "browsing %s: %v", e.ID, err)
		return
	}
	// The strip partitions the whole list, so its counts ARE the total - no
	// COUNT(*) of its own, and no way for the two to disagree.
	//
	// Summed, not taken from the tail chip: chips merge runs that are far
	// apart (A holds both "Ä…" and "ä…", with half the Latin supplement in
	// between), so the last chip in strip order is not always the one the last
	// headword falls under, and offset+count of it would report a dictionary
	// short of its own ending.
	total := 0
	for _, l := range alphabet {
		total += l.Count
	}

	offset, mark := 0, -1
	var land dict.Landing
	if at := strings.TrimSpace(q.Get("at")); at != "" {
		// A jump lands on the PAGE holding the word, not on the word: the
		// reader asked to be put down in the list there, and a page that
		// started mid-letter every time would have no stable address. The
		// word itself is named in the answer, and the page marks it.
		if land, err = b.Jump(r.Context(), at); err != nil {
			httpErr(w, 500, "browsing %s: %v", e.ID, err)
			return
		}
		mark = land.Offset
		offset = mark - mark%browsePageSize
	} else if p, _ := strconv.Atoi(q.Get("p")); p > 1 {
		offset = (p - 1) * browsePageSize
	}
	// Past the end - a stale bookmark, or a jump past the last headword -
	// shows the last page rather than an empty one or an error.
	if total > 0 && offset >= total {
		offset = (total - 1) / browsePageSize * browsePageSize
	}
	// A mark outside the page it produced is a stale bookmark or a jump past
	// the end; the page's own start is then the only honest answer.
	if mark < offset || mark >= offset+browsePageSize {
		mark = offset
	}
	words, err := b.Page(offset, browsePageSize)
	if err != nil {
		httpErr(w, 500, "browsing %s: %v", e.ID, err)
		return
	}
	if words == nil {
		words = []string{}
	}
	if alphabet == nil {
		alphabet = []dict.Letter{}
	}
	writeJSON(w, browseResp{
		Dict:     e.ID,
		Name:     d.Meta().Name,
		Total:    total,
		Page:     offset/browsePageSize + 1,
		Pages:    (total + browsePageSize - 1) / browsePageSize,
		Size:     browsePageSize,
		At:       mark,
		Word:     land.Word,
		Match:    land.Match,
		Words:    words,
		Alphabet: alphabet,
	})
}

// browseUnprepared answers the one state the browse page cannot draw a list
// for. `indexing` separates "wait" from "act": work already in flight is
// something to poll, and its absence means the reader has to ask for the index
// in the dictionary panel (which is also the only place that can ask for it
// when AUTO_INDEX is off).
func browseUnprepared(w http.ResponseWriter, e *entry) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusConflict)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(map[string]any{
		"error":    "this dictionary has no index yet",
		"name":     e.probeName(), // probe only: opening is the price this answer avoids
		"prepared": false,
		"indexing": e.indexing(),
	})
}

// handleBrowsePage serves the browse view. Like the lemma installer, no state
// is baked in: the page asks /api/browse for everything it draws, so the same
// bytes serve every dictionary and the browser caches nothing that can go
// stale against the library.
func (s *Server) handleBrowsePage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(browseHTML)
}
