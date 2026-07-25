// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Package store is the ingested backend: the gonow-dict canonical SQLite
// format (docs/SPEC.md §2). One `<slug>.text.db` per dictionary holds
// headwords, aliases, article HTML, and a contentless FTS5 index that
// powers the fuzzy and full-text modes the direct backends cannot offer.
//
// Query modes are ported from draego/drae.go with the fixes catalogued in
// docs/SPEC.md §FTS-audit (stripped-text indexing, quoted-phrase MATCH
// building, escaped LIKE, clamped limits, ingest-time index build).
package store

import (
	"database/sql"
	"fmt"
	"io"
	"strings"

	"github.com/glowinthedark/gonow-dict/internal/dict"
)

func init() {
	dict.RegisterFormat(".db", func(path string) (dict.Dictionary, error) { return Open(path) })
}

// maxLimit clamps any caller-supplied result limit (FTS-audit #7).
const maxLimit = 500

const schemaVersion = 1

// Store is one opened .text.db. Safe for concurrent readers.
type Store struct {
	db    *sql.DB
	meta  dict.Meta
	ftsOK bool // article text indexed (ingest_level != "headwords")
}

// Open opens and validates a gonow-dict text database.
func Open(path string) (*Store, error) {
	db, err := sql.Open(driverName, dsnRO(path))
	if err != nil {
		return nil, err
	}
	s := &Store{db: db}
	var ver int
	if err := db.QueryRow("PRAGMA user_version").Scan(&ver); err != nil {
		db.Close()
		return nil, err
	}
	if ver != schemaVersion {
		db.Close()
		return nil, fmt.Errorf("%s: not a gonow-dict database (user_version=%d, want %d)", path, ver, schemaVersion)
	}
	m, err := readMeta(db)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("%s: reading meta: %w", path, err)
	}
	s.meta = dict.Meta{
		Name:        m["name"],
		Format:      "gonow:" + m["format"],
		Path:        path,
		Description: m["description"],
	}
	s.ftsOK = m["ingest_level"] != string(LevelHeadwords)
	fmt.Sscanf(m["entry_count"], "%d", &s.meta.EntryCount)
	return s, nil
}

func readMeta(db *sql.DB) (map[string]string, error) {
	rows, err := db.Query("SELECT key, value FROM meta")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		m[k] = v
	}
	return m, rows.Err()
}

func (s *Store) Meta() dict.Meta { return s.meta }

func (s *Store) Caps() dict.Caps {
	return dict.Caps{Exact: true, Prefix: true, Fuzzy: true, FTS: s.ftsOK}
}

func (s *Store) Close() error { return s.db.Close() }

// Resource: text databases carry no binary resources; media lives in the
// companion .media.db (Phase 5) or the original source file.
func (s *Store) Resource(name string) (io.ReadCloser, string, error) {
	return nil, "", dict.ErrNotFound
}

func clamp(n int) int {
	if n <= 0 || n > maxLimit {
		return maxLimit
	}
	return n
}

func (s *Store) collect(rows *sql.Rows, err error) ([]dict.Result, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []dict.Result
	for rows.Next() {
		var r dict.Result
		if err := rows.Scan(&r.Headword, &r.Body); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Exact returns entries (and alias targets) whose headword equals word;
// when the case-sensitive pass is empty it retries COLLATE NOCASE.
func (s *Store) Exact(word string, limit int) ([]dict.Result, error) {
	word = strings.TrimSpace(word)
	if word == "" {
		return nil, nil
	}
	n := clamp(limit)
	const q = `
		SELECT e.w, e.m FROM entry e WHERE e.w = ?1 %[1]s
		UNION ALL
		SELECT e.w, e.m FROM alias a JOIN entry e ON e.id = a.entry_id WHERE a.w = ?1 %[1]s
		LIMIT ?2`
	res, err := s.collect(s.db.Query(fmt.Sprintf(q, ""), word, n))
	if err != nil || len(res) > 0 {
		return res, err
	}
	res, err = s.collect(s.db.Query(fmt.Sprintf(q, "COLLATE NOCASE"), word, n))
	if err != nil || len(res) > 0 {
		return res, err
	}
	// accent-fold fallback (parity with the direct backends): exact
	// phrase over the diacritic-stripping tokenizer, then keep only
	// whole-headword folded matches.
	match := buildExactMatch(word, "w")
	if match == "" {
		return nil, nil
	}
	res, err = s.collect(s.db.Query(`
		SELECT e.w, e.m FROM entry_fts f JOIN entry e ON e.id = f.rowid
		WHERE entry_fts MATCH ?1 ORDER BY e.w LIMIT ?2`, match, n))
	if err != nil {
		return nil, err
	}
	key := dict.Fold(word)
	var out []dict.Result
	for _, r := range res {
		if dict.Fold(r.Headword) == key {
			out = append(out, r)
		}
	}
	return out, nil
}

// buildExactMatch is buildMatch without the prefix star: whole-token
// phrase matching.
func buildExactMatch(input, column string) string {
	var parts []string
	for _, tok := range strings.Fields(strings.TrimSpace(input)) {
		tok = strings.ReplaceAll(tok, `"`, `""`)
		p := `"` + tok + `"`
		if column != "" {
			p = column + ":" + p
		}
		parts = append(parts, p)
	}
	return strings.Join(parts, " ")
}

// Prefix returns exact matches if any, else prefix matches ordered by
// headword (LIKE input escaped — FTS-audit #5).
func (s *Store) Prefix(word string, limit int) ([]dict.Result, error) {
	word = strings.TrimSpace(word)
	if word == "" {
		return nil, nil
	}
	if res, err := s.Exact(word, limit); err != nil || len(res) > 0 {
		return res, err
	}
	n := clamp(limit)
	pat := escapeLike(word) + "%"
	return s.collect(s.db.Query(`
		SELECT w, m FROM (
			SELECT e.w AS w, e.m AS m FROM entry e WHERE e.w LIKE ?1 ESCAPE '\'
			UNION
			SELECT e.w AS w, e.m AS m FROM alias a JOIN entry e ON e.id = a.entry_id WHERE a.w LIKE ?1 ESCAPE '\'
		) ORDER BY w LIMIT ?2`, pat, n))
}

// Fuzzy is the FTS5 headword mode: accent/case-insensitive prefix-phrase
// match via the unicode61 remove_diacritics tokenizer, ordered by
// headword (deliberate — FTS-audit #4).
func (s *Store) Fuzzy(word string, limit int) ([]dict.Result, error) {
	match := buildMatch(word, "w")
	if match == "" {
		return nil, nil
	}
	return s.collect(s.db.Query(`
		SELECT e.w, e.m FROM entry_fts f JOIN entry e ON e.id = f.rowid
		WHERE entry_fts MATCH ?1 ORDER BY e.w LIMIT ?2`, match, clamp(limit)))
}

// FullText searches headwords and article text, ordered by BM25 rank.
func (s *Store) FullText(query string, limit int) ([]dict.Result, error) {
	if !s.ftsOK {
		return nil, dict.ErrUnsupported
	}
	match := buildMatch(query, "")
	if match == "" {
		return nil, nil
	}
	return s.collect(s.db.Query(`
		SELECT e.w, e.m FROM entry_fts f JOIN entry e ON e.id = f.rowid
		WHERE entry_fts MATCH ?1 ORDER BY f.rank LIMIT ?2`, match, clamp(limit)))
}

func (s *Store) Keywords(offset, n int) []string {
	if offset < 0 {
		offset = 0
	}
	rows, err := s.db.Query("SELECT w FROM entry ORDER BY id LIMIT ? OFFSET ?", clamp(n), offset)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var w string
		if rows.Scan(&w) == nil {
			out = append(out, w)
		}
	}
	return out
}

// buildMatch converts free user input into a safe FTS5 MATCH expression:
// every whitespace token becomes a quoted prefix phrase ("tok"*, internal
// quotes doubled), optionally column-filtered. Raw user text never
// reaches the MATCH parser, so operators like -, *, ^, NEAR, OR are
// matched literally instead of exploding (FTS-audit #2).
func buildMatch(input, column string) string {
	var parts []string
	for _, tok := range strings.Fields(strings.TrimSpace(input)) {
		tok = strings.ReplaceAll(tok, `"`, `""`)
		p := `"` + tok + `"*`
		if column != "" {
			p = column + ":" + p
		}
		parts = append(parts, p)
	}
	return strings.Join(parts, " ")
}

// escapeLike escapes LIKE wildcards in user input for use with ESCAPE '\'.
func escapeLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	return strings.ReplaceAll(s, `_`, `\_`)
}
