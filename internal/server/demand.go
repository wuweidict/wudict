// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import "net/http"

// handleDemand prepares one dictionary because the user chose it, before there
// is anything to search.
//
// Choosing a dictionary in the selector IS the demand (D92). Until now the only
// thing that carried that signal to the server was a search of one dictionary
// (handleSearch's `demand`), so a person who picked a dictionary and then
// thought about what to type sat in front of an unprepared one, and the first
// query they eventually ran paid the whole preview cost - on a 2.9 M-entry
// dictionary, longer than any search budget. The selection alone is enough
// evidence; there is no reason to make them type first.
//
// The work itself is entirely demandIndex's: one front-lane slot, no power
// gate, cooldown on failure, idempotent under repeated selection. This handler
// only translates a dictionary id into that call and returns immediately - the
// ingest outlives the request, and the client learns it finished the same way
// it always has, from /api/dicts.
//
// Same-origin only, deliberately absent from corsAllowed (D69): it starts work
// and writes to the user's library folder, which is not what the read-only
// extension grant is for.
func (s *Server) handleDemand(w http.ResponseWriter, r *http.Request) {
	e, err := s.reg.get(r.URL.Query().Get("dict"))
	if err != nil {
		httpErr(w, 404, "%v", err)
		return
	}
	// AUTO_INDEX off means the user has said preparation is theirs to trigger,
	// and a selection is not that trigger - the dictionary panel is. Same
	// condition handleSearch applies before it demands.
	if s.AutoIndex {
		e.demandIndex()
	}
	// `indexing` means "work is in flight", and e.indexing() alone does not:
	// the demanded flag is deliberately never cleared on success (demandIndex),
	// which is harmless for the search path - a prepared dictionary is never
	// deferred again - but would make this endpoint answer "true" for a
	// dictionary prepared an hour ago, forever. One stat settles it.
	_, prepared := preparedTextDB(e.Path)
	writeJSON(w, map[string]bool{"indexing": e.indexing() && !prepared})
}
