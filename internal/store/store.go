// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Package store is the ingested backend: the wudict canonical SQLite
// format (docs/SPEC.md §2). One `text.db` per dictionary - inside its library
// folder, see library.go - holds headwords, aliases, article HTML, and the
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
	"github.com/wuweidict/wudict/internal/artmark"
	"io"
	"strconv"
	"strings"
	"sync"

	"github.com/wuweidict/wudict/internal/dict"
)

func init() {
	// A prepared dictionary is a FOLDER whose main file is `text.db`
	// (see library.go); `<name>.text.db` is the same database copied out of
	// its folder. Registering bare ".db" - as this once did - made every
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
	uuid       string // dict_uuid: what a sibling media.db must match

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
}

// foldVersionOf reads the folding version a database records.
//
// A database written before versioning existed carries no key, and was
// necessarily built by version 1 - the folding in force when the key was
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
// words whose folding changed. Only the trigram index is affected - see
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

// markupVersionOf reads the article-markup version a database records. Absent
// means zero: it was built before internal/artmark existed, so its articles
// carry the presentation the transformer used to inline (<font color>, a
// hard-coded padding-left) and no role a reader could restyle.
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
// (server/registry.go abbrevStale) - the only repair is to build them again.
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
	if ver != schemaVersion {
		db.Close()
		return nil, fmt.Errorf("%s: not a wudict database (user_version=%d, want %d)", path, ver, schemaVersion)
	}
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
		ContentsLang: m["contents_lang"], // DSL only, and absent from older libraries
	}
	s.ftsOK = m["ingest_level"] != string(LevelHeadwords)
	s.srcPath = m["source_path"]
	// feature-detect the trigram "contains" index rather than gating on
	// schema version, so older .text.db (and standalone native dicts whose
	// source is gone) keep opening - they simply lack the contains mode.
	var trig int
	db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='entry_trigram'`).Scan(&trig)
	s.hasTrigram = trig > 0
	// keyed on the table actually present, not on the has_trigram flag, so a
	// database whose meta and schema disagree is judged by its schema
	s.staleFold = s.hasTrigram && foldVersionOf(m) != dict.FoldVersion
	fmt.Sscanf(m["entry_count"], "%d", &s.meta.EntryCount)
	// standalone use: note the sibling media.db so a copied folder works
	// without the original source (D2/D9). Only its existence is checked here;
	// the uuid pairing is verified when it is first opened, which is when a
	// resource actually needs it.
	s.uuid = m["dict_uuid"]
	if sib := MediaSibling(path); sib != "" && fileExists(sib) {
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
	return dict.Caps{Exact: true, Prefix: true, Contains: s.hasTrigram, FTS: s.ftsOK}
}

// ContainsStale reports that the trigram index was built by a different text
// folding than the running one. Contains stays enabled - see FoldStale.
func (s *Store) ContainsStale() bool { return s.staleFold }

func (s *Store) Close() error {
	s.mediaMu.Lock()
	md := s.media
	s.media, s.mediaTried = nil, true // a closed Store never reopens anything
	s.mediaMu.Unlock()
	if md != nil {
		md.Close()
	}
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
	// Both passes compare COLLATE NOCASE because that is the collation the
	// only indexes on these columns are built with (ingest.go): a bare
	// case-sensitive `w = ?1` matches no index and full-scans a table whose
	// rows carry the article bodies inline. The case-sensitive pass gets its
	// exactness from the second, uncollated comparison, applied to the handful
	// of rows the index already narrowed to.
	const q = `
		SELECT e.w, e.m FROM entry e WHERE e.w = ?1 COLLATE NOCASE %[1]s
		UNION ALL
		SELECT e.w, e.m FROM alias a JOIN entry e ON e.id = a.entry_id WHERE a.w = ?1 COLLATE NOCASE %[2]s
		LIMIT ?2`
	res, err := s.collect(s.db.Query(fmt.Sprintf(q, "AND e.w = ?1", "AND a.w = ?1"), word, n))
	if err != nil || len(res) > 0 {
		return res, err
	}
	res, err = s.collect(s.db.Query(fmt.Sprintf(q, "", ""), word, n))
	if err != nil || len(res) > 0 {
		return res, err
	}
	// accent-fold fallback, APPROXIMATING the direct backends: exact phrase
	// over the diacritic-stripping tokenizer, then keep only whole-headword
	// folded matches.
	//
	// Not parity, but not for the reason this comment used to give. It claimed
	// dict.Fold reaches the letters carrying their stroke or slash INSIDE the
	// codepoint (o/, d-, l/, h-) where FTS5's remove_diacritics does not, and
	// offered "dansk" finding "đansk" as the difference. dict.Fold is
	// ToLower + NFD + drop-Mn (dict/fold.go), and đ has no canonical
	// decomposition - so Fold("đansk") is "đansk", neither side finds it, and
	// the direct backends, which call the same Fold, do not find it either.
	//
	// What actually differs is narrower: FTS5 tokenizes and folds with its own
	// unicode61 rules, this filter re-checks with dict.Fold, and the two agree
	// on combining marks but need not agree on every token boundary. Closing
	// even that would mean a second folded column in the schema, and folding
	// stroke letters would mean a fold table and a FoldVersion bump that
	// invalidates every trigram index in the library. Neither is worth it.
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
// "@" prefix - LDOCE6 No-Voice ships 95,162 of them against 65,382 real words,
// so 59 % of its "entries" are `@collocations_woman`, `@examples_woman` and the
// like. They exist to be fetched by a link inside an article, never to be
// browsed: leaving them in makes a `contains` search for "woman" return five
// sub-entries before the word itself, and inflates every entry count.
//
// They stay reachable by EXACT lookup, which is how an article's link fetches
// one. A bare "@" (the symbol as a headword) is not hidden - only "@" followed
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

// Prefix returns every headword starting with word, ordered by headword, the
// exact match first (LIKE input escaped - FTS-audit #5).
//
// The exact match is NOT a short-circuit, though it was until a headword typed
// in full was reported as hiding its own siblings: "starts with" answered a
// complete headword with that one article and dropped every longer key under
// it, while the same word mistyped with a double space returned all of them.
// The LIKE pattern already contains the exact row - a string is a prefix of
// itself - and ORDER BY w puts it first, because a proper prefix sorts before
// everything it prefixes. Exact survives one step lower, as the fallback whose
// NOCASE and folded passes reach what a LIKE that is case-sensitive outside
// ASCII cannot.
//
// Ordering is by the key that MATCHED, not by the headword it belongs to: an
// alias is a way of typing its entry, so `a…` typed into a dictionary of
// phrases must not open with "(Administration of) All the Talents" - reached
// through the alias "all the talents", sorted under a bracket the user never
// typed - and must not spend the LIMIT there either. NOCASE first, then binary
// as the tiebreak, so a capitalized headword and its lowercase twin stay
// adjacent and the word typed in full still sorts ahead of the keys it
// prefixes.
//
// The sort runs over (id, key) and the bodies are joined back onto the LIMIT
// rows that survive it. Selecting `m` into the sorted subquery instead pulls
// every matching article into the temp b-tree: measured on the 879k-entry OED
// (docs.local/PERF.md), a one-letter prefix cost 1.1 s ("a") and 2.4 s ("s")
// that way against 95 ms and 120 ms this way - and with the short-circuit gone,
// one-letter prefixes are no longer a rare path but every first keystroke.
func (s *Store) Prefix(word string, limit int) ([]dict.Result, error) {
	word = strings.TrimSpace(word)
	if word == "" {
		return nil, nil
	}
	n := clamp(limit)
	pat := escapeLike(word) + "%"
	// GROUP BY id, not UNION: an entry its own headword AND an alias match is
	// one article, filed under the earlier of the two keys.
	res, err := s.collect(s.db.Query(`
		SELECT e.w, e.m FROM entry e JOIN (
			SELECT id, min(k) AS k FROM (
				SELECT e2.id AS id, e2.w AS k FROM entry e2
					WHERE e2.w LIKE ?1 ESCAPE '\'`+subEntryFilter("e2.w")+`
				UNION ALL
				SELECT e3.id AS id, a.w AS k FROM alias a JOIN entry e3 ON e3.id = a.entry_id
					WHERE a.w LIKE ?1 ESCAPE '\'`+subEntryFilter("a.w")+`
			) GROUP BY id ORDER BY k COLLATE NOCASE, k LIMIT ?2
		) m ON e.id = m.id ORDER BY m.k COLLATE NOCASE, m.k`, pat, n))
	if err != nil || len(res) > 0 {
		return res, err
	}
	// Nothing starts with the word as typed: fall back to the exact passes
	// (NOCASE over entry and alias, then folded), and finally to a
	// diacritic-insensitive prefix over the FTS `w` column, so `corazon`
	// typed without the ó still prefix-matches `corazón…`.
	// entry_fts always indexes `w`, even at headwords level.
	if res, err := s.Exact(word, limit); err != nil || len(res) > 0 {
		return res, err
	}
	return s.Fuzzy(word, n)
}

// Fuzzy is the accent/case-insensitive prefix-phrase engine (FTS5 unicode61
// remove_diacritics tokenizer, ordered by headword). It is no longer a
// standalone search mode - its behaviour is folded into Prefix, which calls
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
	// headword (accent-sensitive - acceptable for the <3-char contains edge).
	return s.collect(s.db.Query(`
		SELECT e.w, e.m FROM entry e WHERE e.w LIKE ?1 ESCAPE '\'`+subEntryFilter("e.w")+`ORDER BY e.w LIMIT ?2`,
		"%"+escapeLike(word)+"%", n))
}

// FullText searches headwords and article text, ordered by BM25 rank. It reads
// the input as a bag of prefix words - the last rung of the ladder - and is the
// path for callers that do not plan (dict.FullTextPlanner is the one that
// does).
func (s *Store) FullText(query string, limit int) ([]dict.Result, error) {
	return s.FullTextMatch(buildMatch(query, ""), limit)
}

// FullTextMatch runs a composed FTS5 expression (dict.FullTextPlanner).
//
// BM25 ranking here is IDF and term frequency only: the table is declared
// columnsize=0, which discards the per-row token counts bm25() needs for length
// normalisation, so a long article is not penalised for its length. Measured on
// a 200-document probe, that reorders the middle of a result list but not its
// head. Restoring it means re-indexing every prepared dictionary to gain a
// column of token counts, which is not worth it (D115).
func (s *Store) FullTextMatch(match string, limit int) ([]dict.Result, error) {
	if !s.ftsOK {
		return nil, dict.ErrUnsupported
	}
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
	// turning "no limit" into "500" - maxLimit exists to bound HTTP SEARCH
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
