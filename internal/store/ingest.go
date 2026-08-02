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

	"github.com/legbehindneck/wudict/internal/dict"
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
// prefix, accent-insensitive) always works and costs almost nothing — around
// 2 MB on a 40k-entry dictionary — so it has no switch. The two indexes that
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

// IngestLevelReport scans r into a new text database at dbPath (atomically:
// written to a temp file, renamed on success). The FTS index is built
// inside the same transaction as the data (FTS-audit #3).
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

	type pendingLink struct{ w, target string }
	var links []pendingLink
	idByWord := map[string]int64{} // headword -> first entry id
	idByFold := map[string]int64{} // lowercased headword -> first entry id
	var id, subEntries int64
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
			links = append(links, pendingLink{w: hw, target: e.LinkTo})
			continue
		}
		body, err2 := normalizeBody(e)
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
		if _, ok := idByWord[hw]; !ok {
			idByWord[hw] = id
		}
		lw := strings.ToLower(hw)
		if _, ok := idByFold[lw]; !ok {
			idByFold[lw] = id
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
			progress(int(id), total)
		}
	}

	unresolved := 0
	for _, l := range links {
		target, ok := idByWord[l.target]
		if !ok {
			target, ok = idByFold[strings.ToLower(l.target)]
		}
		if !ok {
			unresolved++
			continue
		}
		if _, err = insAlias.Exec(l.w, target); err != nil {
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
		"has_trigram":      boolMeta(plan.Contains), // cheap-list flag; Open feature-detects the table
		"body_encoding":    bodyEncoding(),
		"created":          time.Now().UTC().Format(time.RFC3339),
		"source_sha256_1M": sourceHash(srcMeta.Path),
	}
	if st, err2 := os.Stat(srcMeta.Path); err2 == nil {
		metaKV["source_size"] = fmt.Sprint(st.Size())
		metaKV["source_mtime"] = st.ModTime().UTC().Format(time.RFC3339)
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
		progress(int(id), total)
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
// torn database at the final path. Best-effort — failures are non-fatal.
func syncFile(path string) {
	if f, err := os.Open(path); err == nil {
		_ = f.Sync()
		_ = f.Close()
	}
}

// tempDBName returns a per-call unique scratch path for an ingest target
// (renamed onto dbPath atomically on success). Uniqueness lets concurrent
// ingests of the same dictionary — e.g. a background warm racing an
// on-demand open — proceed without clobbering each other's temp file.
func tempDBName(dbPath string) string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return dbPath + ".ingest." + hex.EncodeToString(b)
}

// normalizeBody converts an ingest Entry body to HTML.
func normalizeBody(e dict.Entry) (string, error) {
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

// sourceHash hashes the first 1 MiB of the source file — cheap identity
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
