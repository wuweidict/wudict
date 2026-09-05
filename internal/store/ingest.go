// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/wuweidict/wudict/internal/dict"
)

// Progress is called during ingest with entries processed so far and the
// total (0 when unknown).
type Progress func(done, total int)

const batchSize = 5000

// Level selects how much gets indexed for search.
type Level string

const (
	// LevelText indexes headwords AND stripped article text: fuzzy +
	// full-text search.
	LevelText Level = "text"
	// LevelHeadwords indexes headwords only: fuzzy search with a much
	// smaller database, no full-text search.
	LevelHeadwords Level = "headwords"
)

// Report summarizes one completed ingest. Diagnostics are RETURNED, never
// printed: store is a library, and only the CLI and the server know whether a
// given ingest is a foreground command the user is watching or one of fifty
// background index builds that must stay quiet (D13).
type Report struct {
	Entries         int // entries written
	UnresolvedLinks int // redirects whose target headword was never found
}

// Plan is what to build for one dictionary. Finding a headword (exact,
// prefix, accent-insensitive) always works and costs almost nothing - around
// 2 MB on a 40k-entry dictionary - so it has no switch. The two indexes that
// do cost something are opt-in per dictionary.
type Plan struct {
	FullText bool // index article text as well as headwords (the largest index)
	Contains bool // trigram over folded headwords, for substring search
}

// PlanOf maps a legacy Level onto a Plan. Contains is off: a trigram index
// serves a mode most people never use, and it more than doubles the size of an
// otherwise ~2 MB index.
func PlanOf(level Level) Plan { return Plan{FullText: level != LevelHeadwords} }

// Ingest scans r into a new text database at dbPath with full text
// indexing (see IngestPlan).
func Ingest(r dict.Reader, dbPath string, progress Progress) error {
	return IngestLevel(r, dbPath, LevelText, progress)
}

// IngestLevel scans r into a new text database, discarding the Report.
func IngestLevel(r dict.Reader, dbPath string, level Level, progress Progress) error {
	_, err := IngestPlan(r, dbPath, PlanOf(level), progress)
	return err
}

// IngestLevelReport is IngestPlan for a legacy Level.
func IngestLevelReport(r dict.Reader, dbPath string, level Level, progress Progress) (Report, error) {
	return IngestPlan(r, dbPath, PlanOf(level), progress)
}

// IngestPlan scans r into a new text database at dbPath (atomically: written
// to a temp file, renamed on success). The FTS index is built inside the same
// transaction as the data (FTS-audit #3).
func IngestPlan(r dict.Reader, dbPath string, plan Plan, progress Progress) (rep Report, err error) {
	level := LevelHeadwords
	if plan.FullText {
		level = LevelText
	}
	srcMeta := r.Meta()
	tmp := tempDBName(dbPath)
	_ = os.Remove(tmp)
	defer func() {
		if err != nil {
			_ = os.Remove(tmp)
		}
	}()
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return rep, err
	}

	db, err := sql.Open(driverName, dsnIngest(tmp))
	if err != nil {
		return rep, err
	}
	defer db.Close()
	// One connection, deliberately. An ingest is a single writer, so a pool
	// buys nothing - and the scratch tables below are TEMP tables, which live
	// on the connection that made them. A second connection would not see them.
	db.SetMaxOpenConns(1)
	// TEMP tables belong in the temp FILE, not in RAM: they exist precisely to
	// keep a 2.9M-headword ingest off the heap, and temp_store=MEMORY would put
	// them right back on it. SQLite's default is already FILE on every build we
	// ship; saying so makes it independent of how the driver was compiled.
	if _, err = db.Exec("PRAGMA temp_store = FILE"); err != nil {
		return rep, err
	}

	if _, err = db.Exec(fmt.Sprintf(`
		PRAGMA user_version = %d;
		CREATE TABLE meta(key TEXT PRIMARY KEY, value TEXT);
		CREATE TABLE entry(id INTEGER PRIMARY KEY, w TEXT NOT NULL, m TEXT NOT NULL);
		CREATE TABLE alias(w TEXT NOT NULL, entry_id INTEGER NOT NULL REFERENCES entry(id));
		CREATE VIRTUAL TABLE entry_fts USING fts5(
			w, txt, content='', columnsize=0,
			tokenize='unicode61 remove_diacritics 2');
	`, schemaVersion)); err != nil {
		return rep, err
	}
	if plan.Contains {
		if _, err = db.Exec(`CREATE VIRTUAL TABLE entry_trigram USING fts5(
			w, content='', columnsize=0, tokenize='trigram')`); err != nil {
			return rep, err
		}
	}

	tx, err := db.Begin()
	if err != nil {
		return rep, err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	insEntry, err := tx.Prepare("INSERT INTO entry(id, w, m) VALUES(?, ?, ?)")
	if err != nil {
		return rep, err
	}
	insFts, err := tx.Prepare("INSERT INTO entry_fts(rowid, w, txt) VALUES(?, ?, ?)")
	if err != nil {
		return rep, err
	}
	var insTrig *sql.Stmt
	if plan.Contains {
		if insTrig, err = tx.Prepare("INSERT INTO entry_trigram(rowid, w) VALUES(?, ?)"); err != nil {
			return rep, err
		}
	}
	insAlias, err := tx.Prepare("INSERT INTO alias(w, entry_id) VALUES(?, ?)")
	if err != nil {
		return rep, err
	}

	// Redirect resolution, on disk rather than on the heap.
	//
	// This used to be two Go maps of EVERY headword (raw and lowercased) plus a
	// slice of every pending link, all held until the scan finished. On a
	// 2.9M-entry dictionary that measured 805 MB of live heap - on a phone with
	// 52 MB free, which is a kill by the low-memory killer, not a slow ingest.
	//
	// Both are scratch tables now. wref is (headword, folded headword, id) per
	// entry; plink is one row per unresolved redirect. TEMP means SQLite keeps
	// them in its own temp file and discards them when the connection closes,
	// so nothing here reaches the text.db that ships.
	if _, err = tx.Exec(`
		CREATE TEMP TABLE wref(w TEXT NOT NULL, lw TEXT NOT NULL, id INTEGER NOT NULL);
		CREATE TEMP TABLE plink(w TEXT NOT NULL, target TEXT NOT NULL);
	`); err != nil {
		return rep, err
	}
	insWref, err := tx.Prepare("INSERT INTO wref(w, lw, id) VALUES(?, ?, ?)")
	if err != nil {
		return rep, err
	}
	insLink, err := tx.Prepare("INSERT INTO plink(w, target) VALUES(?, ?)")
	if err != nil {
		return rep, err
	}
	var id, subEntries, linkCount int64
	total := srcMeta.EntryCount

	for {
		e, rerr := r.Next()
		if errors.Is(rerr, io.EOF) {
			break
		}
		if rerr != nil {
			return rep, rerr
		}
		if len(e.Headwords) == 0 {
			continue
		}
		hw := strings.TrimSpace(e.Headwords[0])
		if hw == "" {
			continue
		}
		if e.LinkTo != "" {
			if _, err = insLink.Exec(hw, e.LinkTo); err != nil {
				return rep, err
			}
			linkCount++
			continue
		}
		body, err2 := NormalizeBody(e)
		if err2 != nil {
			return rep, err2
		}
		id++
		if _, err = insEntry.Exec(id, hw, encodeBody(body)); err != nil {
			return rep, err
		}
		txt := ""
		if level == LevelText {
			txt = StripHTML(body)
		}
		if _, err = insFts.Exec(id, hw, txt); err != nil {
			return rep, err
		}
		// trigram index over the accent/case-folded headword powers the
		// "contains" substring mode (built only when asked for).
		if insTrig != nil {
			if _, err = insTrig.Exec(id, dict.Fold(hw)); err != nil {
				return rep, err
			}
		}
		if isSubEntry(hw) {
			subEntries++
		}
		if _, err = insWref.Exec(hw, strings.ToLower(hw), id); err != nil {
			return rep, err
		}
		for _, extra := range e.Headwords[1:] {
			extra = strings.TrimSpace(extra)
			if extra != "" && extra != hw {
				if _, err = insAlias.Exec(extra, id); err != nil {
					return rep, err
				}
			}
		}
		if progress != nil && id%batchSize == 0 {
			progress(clampDone(int(id), total), total)
		}
	}

	unresolved := 0
	// records whose row has landed, in the unit `total` counts (see below).
	donerec := clampDone(int(id), total)
	if linkCount > 0 {
		// A redirect's row is written HERE, not in the loop above, and on a
		// dictionary that is mostly inflections that is most of the work: this
		// one is 69,002 articles against 2,812,317 @@@LINKs, so the scan tops
		// out at 2% of EntryCount and this pass carries the other 98%. Reported
		// in the same unit as the scan - one record, whenever its row lands -
		// so the count is monotone and, when the source header told the truth,
		// ends exactly at total.
		//
		// It is `total` that cannot be trusted: it is the source's own
		// EntryCount, and a hand-edited DSL #ENTRY_COUNT or a truncated MDX
		// will disagree with the records actually read. Clamped rather than
		// believed - a bar that reads 103% is a bug report about arithmetic,
		// while one that sits at 100% while the last records land is what the
		// client already renders as "finishing…".
		if unresolved, err = resolveLinks(tx, insAlias, func(done int) {
			donerec = clampDone(int(id)+done, total)
			if progress != nil {
				progress(donerec, total)
			}
		}); err != nil {
			return rep, err
		}
	}

	uuid := make([]byte, 16)
	_, _ = rand.Read(uuid)
	metaKV := map[string]string{
		"dict_uuid":        hex.EncodeToString(uuid),
		"name":             srcMeta.Name,
		"format":           srcMeta.Format,
		"source_path":      srcMeta.Path,
		"description":      srcMeta.Description,
		"entry_count":      fmt.Sprint(id - int64(subEntries)),
		"sub_entries":      fmt.Sprint(subEntries), // @-prefixed, hidden from browsing
		"ingest_level":     string(level),
		"has_trigram":      boolMeta(plan.Contains),      // cheap-list flag; Open feature-detects the table
		"fold_version":     fmt.Sprint(dict.FoldVersion), // which text folding built the trigram index
		"body_encoding":    bodyEncoding(),
		"created":          time.Now().UTC().Format(time.RFC3339),
		"source_sha256_1M": sourceHash(srcMeta.Path),
	}
	// Only what the SOURCE DICTIONARY declared. A language worked out from the
	// file name or a parent folder is deliberately not recorded: those are
	// recomputed at every open, so renaming a folder to add a hint takes effect
	// immediately instead of being frozen here by an ingest that may have run
	// before the source file was moved - or before it stopped existing at all.
	if srcMeta.IndexLang != "" {
		metaKV["index_lang"] = srcMeta.IndexLang
	}
	if srcMeta.ContentsLang != "" {
		metaKV["contents_lang"] = srcMeta.ContentsLang
	}
	if st, err2 := os.Stat(srcMeta.Path); err2 == nil {
		metaKV["source_size"] = fmt.Sprint(st.Size())
		metaKV["source_mtime"] = st.ModTime().UTC().Format(time.RFC3339)
	}
	// A format may have absorbed an auxiliary file of its own - DSL's
	// abbreviation glossary - and needs to record what it absorbed so a later
	// run can tell this text.db from one built without it. Never allowed to
	// overwrite a key this ingester owns.
	if ex, ok := r.(interface{ ExtraMeta() map[string]string }); ok {
		for k, v := range ex.ExtraMeta() {
			if _, taken := metaKV[k]; k != "" && !taken {
				metaKV[k] = v
			}
		}
	}
	for k, v := range metaKV {
		if _, err = tx.Exec("INSERT INTO meta(key, value) VALUES(?, ?)", k, v); err != nil {
			return rep, err
		}
	}

	if _, err = tx.Exec("CREATE INDEX idx_entry_w ON entry(w COLLATE NOCASE)"); err != nil {
		return rep, err
	}
	if _, err = tx.Exec("CREATE INDEX idx_alias_w ON alias(w COLLATE NOCASE)"); err != nil {
		return rep, err
	}
	if err = tx.Commit(); err != nil {
		return rep, err
	}
	if _, err = db.Exec("INSERT INTO entry_fts(entry_fts) VALUES('optimize')"); err != nil {
		return rep, err
	}
	if plan.Contains {
		if _, err = db.Exec("INSERT INTO entry_trigram(entry_trigram) VALUES('optimize')"); err != nil {
			return rep, err
		}
	}
	if _, err = db.Exec("ANALYZE; PRAGMA optimize;"); err != nil {
		return rep, err
	}
	if err = db.Close(); err != nil {
		return rep, err
	}
	if progress != nil {
		// donerec, not id: id counts articles, and on a redirect-heavy
		// dictionary that would end the run by snapping the bar back to 2%.
		progress(donerec, total)
	}
	rep = Report{Entries: int(id), UnresolvedLinks: unresolved}
	syncFile(tmp)
	if err = os.Rename(tmp, dbPath); err != nil {
		return rep, err
	}
	// refresh the folder receipt (derived from the meta just written; a
	// failure here must not fail an otherwise complete ingest).
	if strings.EqualFold(filepath.Base(dbPath), TextDBName) {
		_ = WriteInfo(filepath.Dir(dbPath))
	}
	return rep, nil
}

// linkPage bounds how many pending redirects are held in memory at once while
// they are resolved. The rows are read in pages rather than streamed because
// the ingest runs on a single connection: an open cursor would block the
// per-link lookups issued against the same connection.
const linkPage = 10000

// resolveLinks turns every pending redirect into an alias row and reports how
// many had no target.
//
// It reproduces the two-map lookup it replaced EXACTLY: the old code tried the
// raw headword first and fell back to the lowercased one, and in both cases
// took the lowest entry id. One folded lookup covers both - a raw match implies
// a folded match - so the candidates come back in id order and the first row
// whose headword matches the target verbatim wins, else the first row at all.
//
// Folding is Go's strings.ToLower on both sides, not SQL. SQLite's NOCASE and
// lower() are ASCII-only, so "ÁBACO" would stop resolving to "ábaco" the moment
// this moved into the query - a silent regression on exactly the accented
// languages this dictionary is written in.
// clampDone keeps a progress numerator inside the denominator the source
// header supplied. A total of 0 means "unknown" throughout this file and is
// reported as a bare count by the client, so it is left alone.
func clampDone(done, total int) int {
	if total > 0 && done > total {
		return total
	}
	return done
}

func resolveLinks(tx *sql.Tx, insAlias *sql.Stmt, done func(int)) (int, error) {
	if _, err := tx.Exec("CREATE INDEX temp.ix_wref_lw ON wref(lw)"); err != nil {
		return 0, err
	}
	sel, err := tx.Prepare("SELECT w, id FROM wref WHERE lw = ? ORDER BY id")
	if err != nil {
		return 0, err
	}
	defer sel.Close()
	page, err := tx.Prepare("SELECT rowid, w, target FROM plink WHERE rowid > ? ORDER BY rowid LIMIT ?")
	if err != nil {
		return 0, err
	}
	defer page.Close()

	type pending struct {
		w, target string
	}
	unresolved := 0
	processed := 0
	var after int64
	buf := make([]pending, 0, linkPage)
	for {
		rows, err := page.Query(after, linkPage)
		if err != nil {
			return unresolved, err
		}
		buf = buf[:0]
		for rows.Next() {
			var rowid int64
			var p pending
			if err := rows.Scan(&rowid, &p.w, &p.target); err != nil {
				rows.Close()
				return unresolved, err
			}
			after = rowid
			buf = append(buf, p)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return unresolved, err
		}
		rows.Close()
		if len(buf) == 0 {
			return unresolved, nil
		}
		for _, l := range buf {
			id, ok, err := lookupTarget(sel, l.target)
			if err != nil {
				return unresolved, err
			}
			if !ok {
				unresolved++
				continue
			}
			if _, err := insAlias.Exec(l.w, id); err != nil {
				return unresolved, err
			}
		}
		// Every link in the page is processed whether or not it resolved, so
		// the count reaches linkCount exactly and the caller's total is met.
		processed += len(buf)
		if done != nil {
			done(processed)
		}
	}
}

// lookupTarget finds the entry a redirect points at: the lowest-id entry whose
// headword equals target, else the lowest-id entry whose folded headword does.
func lookupTarget(sel *sql.Stmt, target string) (int64, bool, error) {
	rows, err := sel.Query(strings.ToLower(target))
	if err != nil {
		return 0, false, err
	}
	defer rows.Close()
	var fallback int64
	var haveFallback bool
	for rows.Next() {
		var w string
		var id int64
		if err := rows.Scan(&w, &id); err != nil {
			return 0, false, err
		}
		if w == target {
			return id, true, nil
		}
		if !haveFallback {
			fallback, haveFallback = id, true
		}
	}
	if err := rows.Err(); err != nil {
		return 0, false, err
	}
	return fallback, haveFallback, nil
}

// isSubEntry reports an MDict-style expandable section stored as a headword
// ("@examples_woman"). See notSubEntry in store.go for why they are hidden.
func isSubEntry(hw string) bool { return len(hw) > 1 && hw[0] == '@' }

func boolMeta(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

// bodyEncoding records how bodies were written, for humans reading the meta;
// the read path detects the form per row and does not consult this.
func bodyEncoding() string {
	if compressBodies {
		return "deflate"
	}
	return "plain"
}

// syncFile flushes a finished ingest temp to disk. dsnIngest runs with
// synchronous=OFF for speed, so pages may linger in the OS cache; fsyncing
// before the atomic rename ensures an interrupted shutdown cannot leave a
// torn database at the final path. Best-effort - failures are non-fatal.
func syncFile(path string) {
	if f, err := os.Open(path); err == nil {
		_ = f.Sync()
		_ = f.Close()
	}
}

// tempDBName returns a per-call unique scratch path for an ingest target
// (renamed onto dbPath atomically on success). Uniqueness lets concurrent
// ingests of the same dictionary - e.g. a background warm racing an
// on-demand open - proceed without clobbering each other's temp file.
func tempDBName(dbPath string) string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return dbPath + ".ingest." + hex.EncodeToString(b)
}

// NormalizeBody converts an ingest Entry body to HTML. Exported because
// `wudict dump` reads the same format Readers and must render their bodies
// exactly as an ingest would - a second copy of this switch would be a second
// answer to "what is this dictionary's HTML".
func NormalizeBody(e dict.Entry) (string, error) {
	switch e.Kind {
	case dict.BodyHTML:
		return e.Body, nil
	case dict.BodyText:
		return "<p>" + strings.ReplaceAll(htmlEscape(e.Body), "\n", "<br/>") + "</p>", nil
	default:
		// BodyXDXF / BodyDSL converters arrive with their formats (P3/P4).
		return "", fmt.Errorf("unsupported body kind %d", e.Kind)
	}
}

func htmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return r.Replace(s)
}

// sourceHash hashes the first 1 MiB of the source file - cheap identity
// check for stale-DB detection.
func sourceHash(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	h := sha256.New()
	_, _ = io.CopyN(h, f, 1<<20)
	return hex.EncodeToString(h.Sum(nil))
}

// Slug converts a dictionary display name into a filesystem-safe base name.
// Library folders are named after the source FILE (see FolderName); Slug
// remains for callers that need a safe name derived from a display name.
func Slug(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r > 127:
			b.WriteRune(r)
			dash = false
		default:
			if !dash && b.Len() > 0 {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	s := strings.Trim(b.String(), "-")
	if s == "" {
		return "dictionary"
	}
	if len(s) > 80 {
		s = s[:80]
	}
	return s
}

// DefaultDBDir is the cache directory for generated databases (D7).
// WUDICT_DB_DIR overrides it.
func DefaultDBDir() string {
	if dir := os.Getenv("WUDICT_DB_DIR"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".wudict/db"
	}
	return filepath.Join(home, ".wudict", "db")
}
