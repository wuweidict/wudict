// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/wuweidict/wudict/internal/artmark"
	"github.com/wuweidict/wudict/internal/basedir"
	"github.com/wuweidict/wudict/internal/htmlref"

	"github.com/wuweidict/wudict/internal/dict"
	"github.com/wuweidict/wudict/internal/sysmem"
)

// Progress is called during ingest with entries processed so far and the
// total (0 when unknown).
type Progress func(done, total int)

const batchSize = 5000

// The meta `ingest_level` values: whether article text was indexed for
// full-text search. On-disk format - read only through PlanFromMeta.
const (
	levelText      = "text"
	levelHeadwords = "headwords"
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
// prefix, wildcard, accent-insensitive) always works and costs almost
// nothing - around 2 MB on a 40k-entry dictionary - so it has no switch. The
// one index that does cost something is opt-in per dictionary.
type Plan struct {
	FullText bool // index article text as well as headwords (the largest index)
}

// IngestPlan scans r into a new text database at dbPath (atomically: written
// to a temp file, renamed on success). The FTS index is built inside the same
// transaction as the data (FTS-audit #3). It returns only once nothing it
// started still reads r.
func IngestPlan(r dict.Reader, dbPath string, plan Plan, progress Progress) (rep Report, err error) {
	return ingestPlan(r, dbPath, r.Meta().Path, plan, progress)
}

// ingestPlan is IngestPlan for the source a preparation was asked for, src:
// the path whose urgency chooses the lane (D182) - Reconcile's, which for a
// folder is not the file the reader names.
func ingestPlan(r dict.Reader, dbPath, src string, plan Plan, progress Progress) (rep Report, err error) {
	srcMeta := r.Meta()
	// Decided before anything is written, while the database being replaced
	// is still the one at dbPath (keptUUID, stale.go).
	uuid := keptUUID(dbPath, srcMeta.Path)
	if uuid == "" {
		b := make([]byte, 16)
		_, _ = rand.Read(b)
		uuid = hex.EncodeToString(b)
	}
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

	var acct sysmem.Account
	defer acct.Close()
	indexed := false
	if kib := indexFirstKiB(srcMeta, &acct); kib > 0 {
		if _, err = db.Exec(fmt.Sprintf("PRAGMA cache_size = -%d", kib)); err != nil {
			return rep, err
		}
		indexed = true
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
	if indexed {
		if _, err = tx.Exec("CREATE INDEX idx_entry_w ON entry(w COLLATE NOCASE)"); err != nil {
			return rep, err
		}
	}

	// Redirect resolution, on disk rather than on the heap.
	//
	// Two Go maps of EVERY headword (raw and lowercased) plus a slice of every
	// pending link, held until the scan finishes, measured 805 MB of live heap on
	// a 2.9M-entry dictionary (docs.local/PERF.md §9) - on a phone with 52 MB
	// free, a kill by the low-memory killer rather than a slow ingest.
	//
	// So both are scratch tables. wref is (headword, folded headword, id) per
	// entry, written only when a redirect needs it (fillWref); plink is one
	// row per unresolved redirect, its target folded the same way. TEMP means
	// SQLite keeps them in its own temp file and discards them when the
	// connection closes, so nothing here reaches the text.db that ships.
	if _, err = tx.Exec(`
		CREATE TEMP TABLE wref(w TEXT NOT NULL, lw TEXT NOT NULL, id INTEGER NOT NULL);
		CREATE TEMP TABLE plink(w TEXT NOT NULL, target TEXT NOT NULL, lt TEXT NOT NULL);
	`); err != nil {
		return rep, err
	}
	total := srcMeta.EntryCount
	w := newWriter(tx, progress, total)
	defer w.close()
	if err = fill(r, src, srcMeta.Path, plan, w); err != nil {
		return rep, err
	}
	if err = w.flush(); err != nil {
		return rep, err
	}
	id, subEntries, linkCount := w.id, w.subEntries, w.linkCount

	unresolved := 0
	// records whose row has landed, in the unit `total` counts (see below).
	donerec := clampDone(int(id), total)
	if linkCount > 0 {
		if !indexed {
			if _, err = tx.Exec("CREATE INDEX idx_entry_w ON entry(w COLLATE NOCASE)"); err != nil {
				return rep, err
			}
			indexed = true
		}
		if err = fillWref(tx); err != nil {
			return rep, err
		}
		// A redirect's row is written HERE, not while the entries are read
		// (fill), and on a dictionary that is mostly inflections that is most
		// of the work: this one is 69,002 articles against 2,812,317 @@@LINKs,
		// so the scan tops out at 2% of EntryCount and this pass carries the
		// other 98%. Reported
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
		if unresolved, err = resolveLinks(tx, linkCount, func(done int) {
			donerec = clampDone(int(id)+done, total)
			if progress != nil {
				progress(donerec, total)
			}
		}); err != nil {
			return rep, err
		}
	}

	metaKV := map[string]string{
		"dict_uuid":        uuid,
		"name":             srcMeta.Name,
		"format":           srcMeta.Format,
		"source_path":      srcMeta.Path,
		"description":      srcMeta.Description,
		"entry_count":      fmt.Sprint(id - int64(subEntries)),
		"sub_entries":      fmt.Sprint(subEntries), // @-prefixed, hidden from browsing
		"ingest_level":     ingestLevel(plan),
		"markup_version":   fmt.Sprint(artmark.Version), // which role markup the articles were written with
		"ingest_version":   fmt.Sprint(IngestVersion),   // which IngestPlan wrote this (stale.go)
		"body_encoding":    bodyEncoding(),
		"created":          time.Now().UTC().Format(time.RFC3339),
		"source_sha256_1M": sourceHash(srcMeta.Path),
	}
	// which Reader produced the entries; a format that registered no version
	// records none, and Outdated has no opinion about it
	if rv := dict.ReaderVersion(srcMeta.Format); rv > 0 {
		metaKV["reader_version"] = fmt.Sprint(rv)
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
	// The rest of the source header, so a prepared folder whose source is
	// gone still says who wrote it. JSON, one key: the table is key/value and
	// the header is ordered and may repeat a name.
	if len(srcMeta.Header) > 0 {
		if b, err2 := json.Marshal(srcMeta.Header); err2 == nil {
			metaKV["header"] = string(b)
		}
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

	if !indexed {
		if _, err = tx.Exec("CREATE INDEX idx_entry_w ON entry(w COLLATE NOCASE)"); err != nil {
			return rep, err
		}
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

// linkPage is how many pending redirects one resolving statement covers: the
// unit of progress during the link pass.
const linkPage = 10000

// clampDone keeps a progress numerator inside the denominator the source
// header supplied. A total of 0 means "unknown" throughout this file and is
// reported as a bare count by the client, so it is left alone.
func clampDone(done, total int) int {
	if total > 0 && done > total {
		return total
	}
	return done
}

// resolveLinks turns every pending redirect into an alias row and reports how
// many had no target.
//
// The rule: the raw headword first, else the lowercased one, and in both cases
// the lowest entry id. One folded lookup covers both - a raw match implies
// a folded match - so the candidates are ordered verbatim-first, then by id.
//
// Folding is Go's strings.ToLower on both sides, done at insert time (wref.lw,
// plink.lt), not SQL. SQLite's NOCASE and lower() are ASCII-only, so in the
// query "ÁBACO" would not resolve to "ábaco" - silently wrong on exactly the
// accented languages a dictionary is written in. With both sides folded the
// match itself is plain equality, so the whole pass runs inside SQLite: one
// statement per page instead of a query and an insert per redirect.
//
// plink is a fresh TEMP table written once in order, so its rowids are
// exactly 1..total and a page is a rowid range.
func resolveLinks(tx *sql.Tx, total int64, done func(int)) (int, error) {
	if _, err := tx.Exec("CREATE INDEX temp.ix_wref_lw ON wref(lw)"); err != nil {
		return 0, err
	}
	ins, err := tx.Prepare(`
		INSERT INTO alias(w, entry_id)
		SELECT w, eid FROM (
			SELECT p.rowid AS r, p.w AS w, (
				SELECT x.id FROM wref x WHERE x.lw = p.lt
				ORDER BY x.w <> p.target, x.id LIMIT 1
			) AS eid
			FROM plink p WHERE p.rowid > ?1 AND p.rowid <= ?2
		) WHERE eid IS NOT NULL ORDER BY r`)
	if err != nil {
		return 0, err
	}
	defer ins.Close()
	var resolved int64
	for lo := int64(0); lo < total; lo += linkPage {
		hi := min(lo+linkPage, total)
		res, err := ins.Exec(lo, hi)
		if err != nil {
			return int(lo - resolved), err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return int(lo - resolved), err
		}
		resolved += n
		if done != nil {
			done(int(hi))
		}
	}
	return int(total - resolved), nil
}

// isSubEntry reports an MDict-style expandable section stored as a headword
// ("@examples_woman"). See notSubEntry in store.go for why they are hidden.
func isSubEntry(hw string) bool { return len(hw) > 1 && hw[0] == '@' }

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
//
// Opened for writing, not os.Open: on Windows Sync is FlushFileBuffers, which
// requires write access and fails with "Access is denied" on a read-only
// handle - an error ignored here would make the flush silently nothing.
func syncFile(path string) {
	if f, err := os.OpenFile(path, os.O_RDWR, 0); err == nil {
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

// defaultCacheKiB is SQLite's default page cache (cache_size -2000).
const defaultCacheKiB = 2000

// indexFirstKiB decides whether the headword index is built before the rows
// (D182, 8A): when the source averages more than 2 KiB per entry its articles
// are large, and an index built after the rows reads all of them again (3.5
// GB on a 17 GB OED) - but built during the inserts it needs a page cache that
// holds it, about 64 bytes an entry with room to spare, and only a budget that
// fits that pays for it. It returns the page cache in KiB, or 0 to build the
// index after the rows.
func indexFirstKiB(m dict.Meta, acct *sysmem.Account) int {
	if m.EntryCount <= 0 {
		return 0
	}
	fi, err := os.Stat(m.Path)
	if err != nil || fi.IsDir() || fi.Size()/int64(m.EntryCount) <= 2048 {
		return 0
	}
	need := 2 * int64(m.EntryCount) * 64
	if need>>10 <= defaultCacheKiB || need > sysmem.Budget()/4 || !acct.TryAcquire(need) {
		return 0 // an index the default cache holds is built after the rows as cheaply
	}
	return int(need >> 10)
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
		return "<p>" + strings.ReplaceAll(htmlref.EscapeText(e.Body), "\n", "<br/>") + "</p>", nil
	default:
		// BodyXDXF / BodyDSL converters arrive with their formats (P3/P4).
		return "", fmt.Errorf("unsupported body kind %d", e.Kind)
	}
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

// dbDir is the library folder the configuration chose (SetDBDir); nil when
// none was, which leaves the environment and the default to decide.
var dbDir atomic.Pointer[string]

// SetDBDir sets the library folder (config DB_DIR, already resolved through
// flag > env > toml). "" clears it.
func SetDBDir(dir string) {
	if dir == "" {
		dbDir.Store(nil)
		return
	}
	dbDir.Store(&dir)
}

// DefaultDBDir is the library folder for prepared dictionaries (D7): the
// configured one (SetDBDir), else $WUDICT_DB_DIR, else db/ in the user's data
// folder - ~/.wudict/db, or $XDG_DATA_HOME/wudict/db (internal/basedir).
func DefaultDBDir() string {
	if dir := dbDir.Load(); dir != nil {
		return *dir
	}
	if dir := os.Getenv("WUDICT_DB_DIR"); dir != "" {
		return dir
	}
	if d := basedir.Current().Data; d != "" {
		return filepath.Join(d, "db")
	}
	return filepath.Join(basedir.LegacyName, "db")
}
