// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package store

// Sequential access to a whole prepared dictionary: the read side of what
// ingest wrote. Used by `wudict dump`, which needs every entry in one pass
// rather than a query's worth of them.

// EachEntry calls fn once per entry, in ingest order, with the entry's
// display headword, its aliases, and its decoded article HTML. It stops at
// the first error fn returns and passes it back.
//
// Two cursors merged here rather than one LEFT JOIN: nothing indexes
// alias(entry_id), so the join would either sort the entire result - article
// bodies included, gigabytes of temp file on a large dictionary - or build a
// transient index over it. Streaming both tables in entry-id order sorts only
// (entry_id, word) pairs, and holds one entry's aliases at a time.
func (s *Store) EachEntry(fn func(headword string, alts []string, body string) error) error {
	entries, err := s.db.Query("SELECT id, w, m FROM entry ORDER BY id")
	if err != nil {
		return err
	}
	defer entries.Close()
	aliases, err := s.db.Query("SELECT entry_id, w FROM alias ORDER BY entry_id")
	if err != nil {
		return err
	}
	defer aliases.Close()

	var aID int64
	var aWord string
	aOK := false
	nextAlias := func() error {
		aOK = aliases.Next()
		if !aOK {
			return aliases.Err()
		}
		return aliases.Scan(&aID, &aWord)
	}
	if err := nextAlias(); err != nil {
		return err
	}

	for entries.Next() {
		var id int64
		var w string
		var body []byte // TEXT or a compressed BLOB; decodeBody tells them apart
		if err := entries.Scan(&id, &w, &body); err != nil {
			return err
		}
		// An alias whose entry is gone would otherwise be attributed to the
		// next entry that happens to follow it; skip past instead.
		for aOK && aID < id {
			if err := nextAlias(); err != nil {
				return err
			}
		}
		var alts []string
		for aOK && aID == id {
			alts = append(alts, aWord)
			if err := nextAlias(); err != nil {
				return err
			}
		}
		if err := fn(w, alts, decodeBody(body)); err != nil {
			return err
		}
	}
	if err := entries.Err(); err != nil {
		return err
	}
	return aliases.Err()
}

// MediaNames lists the resources packed in this dictionary's media.db, or
// nothing when it has none. The names are exactly the keys articles reference
// (SPEC §3), which is what makes the unpacked files line up with the HTML.
func (s *Store) MediaNames() []string {
	m := s.mediaDB()
	if m == nil {
		return nil
	}
	return m.Names()
}

// Names lists every packed resource, sorted.
func (m *Media) Names() []string {
	rows, err := m.db.Query("SELECT name FROM resource ORDER BY name")
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if rows.Scan(&n) == nil {
			out = append(out, n)
		}
	}
	return out
}
