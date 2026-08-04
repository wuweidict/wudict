// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Package store is the ingested backend: the wudict canonical SQLite
// format (docs/SPEC.md §2). One `text.db` per dictionary — inside its library
// folder, see library.go — holds headwords, aliases, article HTML, and the
// FTS5 indexes powering the contains and full-text modes the direct backends
// cannot offer.
//
// Query modes are ported from draego/drae.go with the fixes catalogued in
// docs/SPEC.md §FTS-audit (stripped-text indexing, quoted-phrase MATCH
// building, escaped LIKE, clamped limits, ingest-time index build).
package store

import (
	"database/sql"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/legbehindneck/wudict/internal/dict"
)

func init() {
	// A prepared dictionary is a FOLDER whose main file is `text.db`
	// (see library.go); `<name>.text.db` is the same database copied out of
	// its folder. Registering bare ".db" — as this once did — made every
	// internal sidecar a public dictionary type: a `media.db` opened as a
	// phantom dictionary, because for uuid pairing it carries the same
	// user_version and meta table as a text.db.
	open := func(path string) (dict.Dictionary, error) { return Open(path) }
	dict.RegisterFileName(TextDBName, open)
	dict.RegisterFormat(".text.db", open)
}

// maxLimit clamps any caller-supplied result limit (FTS-audit #7).
const maxLimit = 500

const schemaVersion = 1

// Store is one opened .text.db. Safe for concurrent readers.
type Store struct {
	db         *sql.DB
	meta       dict.Meta
	ftsOK      bool   // article text indexed (ingest_level != "headwords")
	srcPath    string // source_path recorded at ingest ("" if unknown)
	hasTrigram bool   // entry_trigram present → "contains" substring search
	staleFold  bool   // trigram built by a different dict.FoldVersion
	media      *Media // sibling .media.db, when present and uuid-paired
}

// foldVersionOf reads the folding version a database records.
//
// A database written before versioning existed carries no key, and was
// necessarily built by version 1 — the folding in force when the key was
// introduced. Defaulting to 1 rather than 0 is the whole reason this does not
// declare every existing library stale on the day it ships.
func foldVersionOf(m map[string]string) int {
	if s := m["fold_version"]; s != "" {
		if n, err := strconv.Atoi(s); err == nil {
			return n
		}
	}
	return 1
}

// FoldStale reports that a prepared database's trigram index was built by a
// different text folding than the one running now, so "contains" may miss
// words whose folding changed. Only the trigram index is affected — see
// dict.FoldVersion for why nothing else is.
//
// It is a fact ABOUT the data, not a verdict on it: a folding change usually
// affects one narrow class of characters, so a stale index is still correct
// for every dictionary that contains none of them. Callers report it and offer
// a rebuild; nobody disables the mode or re-indexes a hundred dictionaries on
// the strength of it.
func FoldStale(m map[string]string) bool {
	return m["has_trigram"] == "1" && foldVersionOf(m) != dict.FoldVersion
}

// Open opens and validates a wudict text database.
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
		return nil, fmt.Errorf("%s: not a wudict database (user_version=%d, want %d)", path, ver, schemaVersion)
	}
	m, err := readMeta(db)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("%s: reading meta: %w", path, err)
	}
	s.meta = dict.Meta{
		Name:        m["name"],
		Format:      "wudict:" + m["format"],
		Path:        path,
		Description: m["description"],
	}
	s.ftsOK = m["ingest_level"] != string(LevelHeadwords)
	s.srcPath = m["source_path"]
	// feature-detect the trigram "contains" index rather than gating on
	// schema version, so older .text.db (and standalone native dicts whose
	// source is gone) keep opening — they simply lack the contains mode.
	var trig int
	db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='entry_trigram'`).Scan(&trig)
	s.hasTrigram = trig > 0
	// keyed on the table actually present, not on the has_trigram flag, so a
	// database whose meta and schema disagree is judged by its schema
	s.staleFold = s.hasTrigram && foldVersionOf(m) != dict.FoldVersion
	fmt.Sscanf(m["entry_count"], "%d", &s.meta.EntryCount)
	// standalone use: attach the sibling media.db so a copied folder works
	// without the original source (D2/D9); uuid mismatch = not our pair
	if sib := MediaSibling(path); sib != "" {
		if md, err := OpenMedia(sib); err == nil {
			if md.UUID == m["dict_uuid"] {
				s.media = md
			} else {
				md.Close()
			}
		}
	}
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

// ReadMeta opens a wudict database read-only and returns its whole meta
// table. Used by the cheap dictionary-list path to read name/entry_count/
// ingest_level without opening the heavy direct backend.
func ReadMeta(dbPath string) (map[string]string, error) {
	db, err := sql.Open(driverName, dsnRO(dbPath))
	if err != nil {
		return nil, err
	}
	defer db.Close()
	return readMeta(db)
}

func (s *Store) Meta() dict.Meta { return s.meta }

// SourcePath returns the foreign source this database was prepared from
// (empty when unrecorded). The file may no longer exist — a prepared
// dictionary stands on its own.
func (s *Store) SourcePath() string { return s.srcPath }

func (s *Store) Caps() dict.Caps {
	return dict.Caps{Exact: true, Prefix: true, Contains: s.hasTrigram, FTS: s.ftsOK}
}

// ContainsStale reports that the trigram index was built by a different text
// folding than the running one. Contains stays enabled — see FoldStale.
func (s *Store) ContainsStale() bool { return s.staleFold }

func (s *Store) Close() error {
	if s.media != nil {
		s.media.Close()
	}
	return s.db.Close()
}

// Resource serves from the attached sibling .media.db when present;
// otherwise text databases carry no binary resources (the upgraded
// server backend falls back to the original source file).
func (s *Store) Resource(name string) (io.ReadCloser, string, error) {
	if s.media != nil {
		return s.media.Resource(name)
	}
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
		var body []byte // TEXT or a compressed BLOB; decodeBody tells them apart
		if err := rows.Scan(&r.Headword, &body); err != nil {
			return nil, err
		}
		r.Body = decodeBody(body)
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

// notSubEntry excludes MDict-style sub-entries from browsing results.
//
// Repacked dictionaries store expandable sections as ordinary headwords with an
// "@" prefix — LDOCE6 No-Voice ships 95,162 of them against 65,382 real words,
// so 59 % of its "entries" are `@collocations_woman`, `@examples_woman` and the
// like. They exist to be fetched by a link inside an article, never to be
// browsed: leaving them in makes a `contains` search for "woman" return five
// sub-entries before the word itself, and inflates every entry count.
//
// They stay reachable by EXACT lookup, which is how an article's link fetches
// one. A bare "@" (the symbol as a headword) is not hidden — only "@" followed
// by something, which is what the convention produces.
const notSubEntry = ` AND %s NOT LIKE '@_%%' `

func subEntryFilter(col string) string { return fmt.Sprintf(notSubEntry, col) }

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
	res, err := s.collect(s.db.Query(`
		SELECT w, m FROM (
			SELECT e.w AS w, e.m AS m FROM entry e WHERE e.w LIKE ?1 ESCAPE '\'`+subEntryFilter("e.w")+`
			UNION
			SELECT e.w AS w, e.m AS m FROM alias a JOIN entry e ON e.id = a.entry_id WHERE a.w LIKE ?1 ESCAPE '\'`+subEntryFilter("a.w")+`
		) ORDER BY w LIMIT ?2`, pat, n))
	if err != nil || len(res) > 0 {
		return res, err
	}
	// accent/case-fold parity with the direct backends: when the raw
	// prefix finds nothing, retry as a diacritic-insensitive prefix over
	// the FTS `w` column (so `corazon` still prefix-matches `corazón…`).
	// entry_fts always indexes `w`, even at headwords level.
	return s.Fuzzy(word, n)
}

// Fuzzy is the accent/case-insensitive prefix-phrase engine (FTS5 unicode61
// remove_diacritics tokenizer, ordered by headword). It is no longer a
// standalone search mode — its behaviour is folded into Prefix, which calls
// it as the accent-insensitive fallback (FTS-audit #4).
func (s *Store) Fuzzy(word string, limit int) ([]dict.Result, error) {
	match := buildMatch(word, "w")
	if match == "" {
		return nil, nil
	}
	return s.collect(s.db.Query(`
		SELECT e.w, e.m FROM entry_fts f JOIN entry e ON e.id = f.rowid
		WHERE entry_fts MATCH ?1`+subEntryFilter("e.w")+`ORDER BY e.w LIMIT ?2`, match, clamp(limit)))
}

// Contains is the substring/typo-tolerant headword mode, backed by the FTS5
// trigram index over accent/case-folded headwords. The trigram tokenizer
// needs at least 3 characters; shorter queries fall back to a folded LIKE.
// Ordered by headword. Requires a trigram-indexed database (re-ingest older
// ones); unsupported otherwise.
func (s *Store) Contains(word string, limit int) ([]dict.Result, error) {
	word = strings.TrimSpace(word)
	if word == "" {
		return nil, nil
	}
	if !s.hasTrigram {
		return nil, dict.ErrUnsupported
	}
	folded := dict.Fold(word)
	if folded == "" {
		return nil, nil
	}
	n := clamp(limit)
	if len([]rune(folded)) >= 3 {
		phrase := `"` + strings.ReplaceAll(folded, `"`, `""`) + `"`
		res, err := s.collect(s.db.Query(`
			SELECT e.w, e.m FROM entry_trigram t JOIN entry e ON e.id = t.rowid
			WHERE entry_trigram MATCH ?1`+subEntryFilter("e.w")+`ORDER BY e.w LIMIT ?2`, phrase, n))
		if err != nil || len(res) > 0 {
			return res, err
		}
	}
	// short query (< 3 chars) or trigram miss: LIKE substring on the raw
	// headword (accent-sensitive — acceptable for the <3-char contains edge).
	return s.collect(s.db.Query(`
		SELECT e.w, e.m FROM entry e WHERE e.w LIKE ?1 ESCAPE '\'`+subEntryFilter("e.w")+`ORDER BY e.w LIMIT ?2`,
		"%"+escapeLike(word)+"%", n))
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
		WHERE entry_fts MATCH ?1`+subEntryFilter("e.w")+`ORDER BY f.rank LIMIT ?2`, match, clamp(limit)))
}

func (s *Store) Keywords(offset, n int) []string {
	if offset < 0 {
		offset = 0
	}
	// n<=0 is "no limit" (dict.Keywords), which SQLite spells as a negative
	// LIMIT. This used to pass clamp(n), capping every browse at 500 and
	// turning "no limit" into "500" — maxLimit exists to bound HTTP SEARCH
	// results (FTS-audit #7), and Keywords is not reachable over HTTP at all.
	limit := n
	if limit <= 0 {
		limit = -1
	}
	rows, err := s.db.Query("SELECT w FROM entry WHERE w NOT LIKE '@_%' ORDER BY id LIMIT ? OFFSET ?", limit, offset)
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
