// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Package store is the ingested backend: the wudict canonical SQLite
// format (docs/SPEC.md §2). One `text.db` per dictionary - inside its library
// folder, see library.go - holds headwords, aliases, article HTML, and the
// FTS5 index powering the full-text mode the direct backends cannot offer.
//
// Query modes are ported from draego/drae.go with the fixes catalogued in
// docs/SPEC.md §FTS-audit (stripped-text indexing, quoted-phrase MATCH
// building, clamped limits, ingest-time index build).
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"

	"github.com/wuweidict/wudict/internal/artmark"
	"github.com/wuweidict/wudict/internal/fsx"

	"github.com/wuweidict/wudict/internal/dict"
	"github.com/wuweidict/wudict/internal/logx"
	"github.com/wuweidict/wudict/internal/wildcard"
)

func init() {
	// A prepared dictionary is a FOLDER whose main file is `text.db`
	// (see library.go); `<name>.text.db` is the same database copied out of
	// its folder. Registering bare ".db" would make every internal sidecar a
	// public dictionary type: a `media.db` would open as a phantom dictionary,
	// because for uuid pairing it carries the same user_version and meta table
	// as a text.db.
	open := func(path string) (dict.Dictionary, error) { return Open(path) }
	dict.RegisterFileName(TextDBName, open)
	dict.RegisterFormat(".text.db", open)
}

// maxLimit clamps any caller-supplied result limit (FTS-audit #7).
const maxLimit = 500

var _ dict.Searcher = (*Store)(nil)

// Store is one opened .text.db. Safe for concurrent readers.
type Store struct {
	db      *sql.DB
	meta    dict.Meta
	ftsOK   bool   // article text indexed (ingest_level != "headwords")
	srcPath string // source_path recorded at ingest ("" if unknown)
	uuid    string // dict_uuid: what a sibling media.db must match
	body    *bodyDecoder

	// The sibling media.db is opened on the first resource that asks for one,
	// not at open. Most opens never serve a resource at all - a search touches
	// text only - and the media database of a large dictionary is the bigger
	// file of the pair, so eagerly attaching it meant a second SQLite handle,
	// a second page cache and a second set of descriptors per dictionary, held
	// for the life of the process, on the chance that an article referenced an
	// image. (D64)
	mediaPath  string // "" when this dictionary has no packed media
	mediaMu    sync.Mutex
	media      *Media
	mediaTried bool // opened and failed, or opened and rejected: do not retry

	// The browse strip (browse.go), computed on the first browse of this
	// handle and kept for its life: a text.db does not change under an open
	// reader, and re-walking the index for forty numbers on every page turn
	// would be the one expensive thing about turning a page.
	alphaOnce sync.Once
	alpha     []dict.Letter
	alphaErr  error

	// The statements of exact, prefix and wildcard search (wildcard.go),
	// prepared on the first search of this handle and kept for its life;
	// db.Close releases them. marksStmt is nil when no headword starts with a
	// nonspacing mark, which a text.db that does not change under an open
	// reader answers once.
	marksOnce sync.Once
	marksStmt *sql.Stmt
	marksErr  error
	scanStmts sync.Map // query text → *sql.Stmt
}

// markupVersionOf reads the article-markup version a database records. Absent
// means zero: it was built before internal/artmark existed, so its articles
// carry inline presentation (<font color>, a hard-coded padding-left) and no
// role a reader could restyle.
func markupVersionOf(m map[string]string) int {
	if s := m["markup_version"]; s != "" {
		if n, err := strconv.Atoi(s); err == nil {
			return n
		}
	}
	return 0
}

// MarkupStale reports that the articles in a prepared database were written
// with a different role vocabulary than the stylesheet now serving them
// (internal/artmark). Only formats whose HTML wudict SYNTHESIZES can be stale:
// an mdx or a slob carries its own markup and its own stylesheet, and wudict
// changing its mind about class names cannot reach it.
//
// The articles are frozen bytes, so - exactly as with an abbreviation glossary
// (AbbrevChanged) - the only repair is to build them again.
// This is a fact about the data, not a fault: a stale article still renders,
// it simply does not answer to Custom styles. Callers rebuild on a dictionary
// the user is already changing and leave the rest alone; there is deliberately
// no library-wide sweep, because a re-index of everything is the one cost that
// must never be paid on somebody's behalf (docs.local/PERF.md).
func MarkupStale(m map[string]string) bool {
	switch m["format"] {
	case "dsl", "stardict": // dsl: always synthesized. stardict: XDXF articles.
		return markupVersionOf(m) != artmark.Version
	}
	return false
}

// Open opens and validates a wudict text database.
func Open(path string) (*Store, error) {
	db, err := openRO(path)
	if err != nil {
		return nil, err
	}
	s := &Store{db: db}
	var ver int
	if err := db.QueryRow("PRAGMA user_version").Scan(&ver); err != nil {
		db.Close()
		return nil, err
	}
	if !textSchemaOK(ver) {
		db.Close()
		return nil, fmt.Errorf("%s: not a wudict database (user_version=%d, want %d to %d)", path, ver, textSchemaMin, textSchemaMax)
	}
	s.body = newBodyDecoder(ver, db)
	m, err := readMeta(db)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("%s: reading meta: %w", path, err)
	}
	// DisplayText on the way out, not on the way in: these strings were copied
	// from the source dictionary's header at ingest time, and a library
	// prepared before that leniency existed still holds whatever escaping the
	// dictionary shipped. Decoding here repairs those without a re-ingest.
	s.meta = dict.Meta{
		Name:         dict.DisplayText(m["name"]),
		Format:       "wudict:" + m["format"],
		Path:         path,
		Description:  dict.DisplayText(m["description"]),
		IndexLang:    m["index_lang"],    // declared at ingest; "" for most formats
		ContentsLang: m["contents_lang"], // DSL and BGL only, and absent from older libraries
		Header:       headerOf(m),
	}
	s.ftsOK = PlanFromMeta(m).FullText
	s.srcPath = m["source_path"]
	fmt.Sscanf(m["entry_count"], "%d", &s.meta.EntryCount)
	// standalone use: note the sibling media.db so a copied folder works
	// without the original source (D2/D9). Only its existence is checked here;
	// the uuid pairing is verified when it is first opened, which is when a
	// resource actually needs it.
	s.uuid = m["dict_uuid"]
	if sib := MediaSibling(path); sib != "" && fsx.FileExists(sib) {
		s.mediaPath = sib
	}
	return s, nil
}

// mediaDB opens the paired media database on first use, and remembers a
// failure so a dictionary whose media.db is corrupt or foreign does not retry
// on every image in every article. Nil means "this dictionary has no packed
// media", which the caller reports as a plain miss.
func (s *Store) mediaDB() *Media {
	if s.mediaPath == "" {
		return nil
	}
	s.mediaMu.Lock()
	defer s.mediaMu.Unlock()
	if s.media != nil || s.mediaTried {
		return s.media
	}
	s.mediaTried = true
	md, err := OpenMedia(s.mediaPath)
	if err != nil {
		return nil
	}
	if md.UUID != s.uuid {
		md.Close() // not our pair: a media.db left beside someone else's text.db
		return nil
	}
	s.media = md
	return md
}

// headerOf decodes the source header ingest recorded. Absent from libraries
// prepared before it was kept, and a value that will not decode is treated
// the same way: it is informational, never worth failing an open over.
func headerOf(m map[string]string) []dict.Field {
	var h []dict.Field
	if s := m["header"]; s != "" && json.Unmarshal([]byte(s), &h) != nil {
		return nil
	}
	return h
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
	db, err := openRO(dbPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	return readMeta(db)
}

func (s *Store) Meta() dict.Meta { return s.meta }

// SourcePath returns the foreign source this database was prepared from
// (empty when unrecorded). The file may no longer exist - a prepared
// dictionary stands on its own.
func (s *Store) SourcePath() string { return s.srcPath }

// UUID is the dict_uuid a sibling file must carry to be this dictionary's:
// media.db is checked against it, and so is the derived media.link.db.
func (s *Store) UUID() string { return s.uuid }

func (s *Store) Caps() dict.Caps {
	return dict.Caps{Exact: true, Prefix: true, FTS: s.ftsOK, Wildcard: true}
}

// DecoderHeld reports what the article decoder holds - 0 until a zstd
// article is read, and after a release - and when it last decoded one
// (UnixNano).
func (s *Store) DecoderHeld() (bytes, lastUse int64) { return s.body.held() }

// ReleaseDecoder drops the article decoder and reports what it held; the next
// zstd article makes it again (about 1-2 ms).
func (s *Store) ReleaseDecoder() int64 { return s.body.release() }

func (s *Store) Close() error {
	s.mediaMu.Lock()
	md := s.media
	s.media, s.mediaTried = nil, true // a closed Store never reopens anything
	s.mediaMu.Unlock()
	if md != nil {
		md.Close()
	}
	s.body.close()
	return s.db.Close()
}

// Resource serves from the attached sibling .media.db when present;
// otherwise text databases carry no binary resources (the upgraded
// server backend falls back to the original source file).
func (s *Store) Resource(name string) (io.ReadCloser, string, error) {
	if md := s.mediaDB(); md != nil {
		return md.Resource(name)
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
		var body []byte // TEXT or a compressed BLOB; s.body tells them apart
		if err := rows.Scan(&r.Headword, &body); err != nil {
			return nil, err
		}
		r.Body = s.body.decode(body)
		out = append(out, r)
	}
	return out, rows.Err()
}

// Exact and Prefix are the dict.Dictionary forms, for callers with no context
// to give; the search fan-out uses the Context forms (dict.Searcher), so that
// a query whose request has gone is interrupted inside SQLite.
func (s *Store) Exact(word string, limit int) ([]dict.Result, error) {
	return s.ExactContext(context.Background(), word, limit)
}

func (s *Store) Prefix(word string, limit int) ([]dict.Result, error) {
	return s.PrefixContext(context.Background(), word, limit)
}

// ExactContext returns the entries whose headword, or one of whose aliases,
// is word, ranked (rank, D181): spelled as typed first, then with case
// ignored, then with accents ignored too - `corazon` finds corazón, `елка`
// finds ёлка. The folded reading covers headwords only; an alias matches as
// typed or with ASCII case ignored (SQLite NOCASE). `@_` sub-entries are
// found too: an article's link fetches one by exact lookup.
func (s *Store) ExactContext(ctx context.Context, word string, limit int) ([]dict.Result, error) {
	word = strings.TrimSpace(word)
	if word == "" {
		return nil, nil
	}
	return s.ranked(ctx, wildcard.Literal(word, false), limit, scope{aliases: true, subs: true})
}

// notSubEntry excludes MDict-style sub-entries from browsing results.
//
// Repacked dictionaries store expandable sections as ordinary headwords with an
// "@" prefix - LDOCE6 No-Voice ships 95,162 of them against 65,382 real words,
// so 59 % of its "entries" are `@collocations_woman`, `@examples_woman` and the
// like. They exist to be fetched by a link inside an article, never to be
// browsed: leaving them in makes a `*woman*` search return five sub-entries
// before the word itself, and inflates every entry count.
//
// They stay reachable by EXACT lookup, which is how an article's link fetches
// one. A bare "@" (the symbol as a headword) is not hidden - only "@" followed
// by something, which is what the convention produces.
const notSubEntry = ` AND %s NOT LIKE '@_%%' `

func subEntryFilter(col string) string { return fmt.Sprintf(notSubEntry, col) }

// PrefixContext returns the entries whose headword, or one of whose aliases,
// starts with word, ranked (rank, D181): the word itself first, spelled as
// typed before its other spellings, then the keys it starts, in browse
// order - with a capital or an accent typed, the ones that keep it first.
// Accents are always ignored as well, not only when nothing matches as
// typed: `nacion` finds nación ahead of nacional, `über` the Über- nouns.
//
// The word itself leads because it is a match of its own (Spelled), not by
// the limit's luck: a headword typed in full must not hide the keys it is a
// prefix of, nor be cut by them.
//
// Ordering is by the key that MATCHED, not by the headword it belongs to: an
// alias is a way of typing its entry, so `a…` typed into a dictionary of
// phrases must not open with "(Administration of) All the Talents" - reached
// through the alias "all the talents", sorted under a bracket the user never
// typed - and must not spend the limit there either. An entry its headword
// and an alias both match is one article, filed under the better of the two.
//
// Measured on the 879k-entry OED (docs.local/PHASES.md 2026-10-22): a
// one-letter prefix reads the first n keys of its range and stops, where a
// LIKE sorted over every match cost 95-120 ms.
func (s *Store) PrefixContext(ctx context.Context, word string, limit int) ([]dict.Result, error) {
	word = strings.TrimSpace(word)
	if word == "" {
		return nil, nil
	}
	return s.ranked(ctx, wildcard.Literal(word, true), limit, scope{aliases: true})
}

// FullTextContext searches headwords and article text, ordered by BM25 rank,
// reading the input as a bag of prefix words: the single reading, for when no
// internal/ftsq plan could run.
func (s *Store) FullTextContext(ctx context.Context, query string, limit int) ([]dict.Result, error) {
	return s.FullTextMatch(ctx, buildMatch(query, ""), limit)
}

// FullTextMatch runs a composed FTS5 expression (internal/ftsq).
//
// BM25 ranking here is IDF and term frequency only: the table is declared
// columnsize=0, which discards the per-row token counts bm25() needs for length
// normalisation, so a long article is not penalised for its length. Measured on
// a 200-document probe, that reorders the middle of a result list but not its
// head. Restoring it means re-indexing every prepared dictionary to gain a
// column of token counts, which is not worth it (D115).
func (s *Store) FullTextMatch(ctx context.Context, match string, limit int) ([]dict.Result, error) {
	if !s.ftsOK {
		return nil, dict.ErrUnsupported
	}
	if match == "" {
		return nil, nil
	}
	return s.collect(s.db.QueryContext(ctx, `
		SELECT e.w, e.m FROM entry_fts f JOIN entry e ON e.id = f.rowid
		WHERE entry_fts MATCH ?1`+subEntryFilter("e.w")+`ORDER BY f.rank LIMIT ?2`, match, clamp(limit)))
}

func (s *Store) Keywords(offset, n int) []string {
	if offset < 0 {
		offset = 0
	}
	// n<=0 is "no limit" (dict.Keywords), which SQLite spells as a negative
	// LIMIT. Not clamp(n): maxLimit bounds HTTP SEARCH results (FTS-audit #7),
	// and Keywords is not reachable over HTTP at all.
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
	// The signature cannot carry it, but a read that died part way must not
	// pass for a short list: name the degradation where it happened.
	if err := rows.Err(); err != nil {
		logx.Warn("keywords: %v (returning the %d read so far)", err, len(out))
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
