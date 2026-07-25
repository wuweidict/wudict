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
	"sync"
	"time"

	"github.com/glowinthedark/gonow-dict/internal/dict"
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

// Ingest scans r into a new text database at dbPath with full text
// indexing (see IngestLevel).
func Ingest(r dict.Reader, dbPath string, progress Progress) error {
	return IngestLevel(r, dbPath, LevelText, progress)
}

// IngestLevel scans r into a new text database at dbPath (atomically:
// written to a temp file, renamed on success). The FTS index is built
// inside the same transaction as the data (FTS-audit #3).
func IngestLevel(r dict.Reader, dbPath string, level Level, progress Progress) (err error) {
	srcMeta := r.Meta()
	tmp := tempDBName(dbPath)
	_ = os.Remove(tmp)
	defer func() {
		if err != nil {
			_ = os.Remove(tmp)
		}
	}()
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return err
	}

	db, err := sql.Open(driverName, dsnIngest(tmp))
	if err != nil {
		return err
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
		CREATE VIRTUAL TABLE entry_trigram USING fts5(
			w, content='', columnsize=0, tokenize='trigram');
	`, schemaVersion)); err != nil {
		return err
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	insEntry, err := tx.Prepare("INSERT INTO entry(id, w, m) VALUES(?, ?, ?)")
	if err != nil {
		return err
	}
	insFts, err := tx.Prepare("INSERT INTO entry_fts(rowid, w, txt) VALUES(?, ?, ?)")
	if err != nil {
		return err
	}
	insTrig, err := tx.Prepare("INSERT INTO entry_trigram(rowid, w) VALUES(?, ?)")
	if err != nil {
		return err
	}
	insAlias, err := tx.Prepare("INSERT INTO alias(w, entry_id) VALUES(?, ?)")
	if err != nil {
		return err
	}

	type pendingLink struct{ w, target string }
	var links []pendingLink
	idByWord := map[string]int64{} // headword -> first entry id
	idByFold := map[string]int64{} // lowercased headword -> first entry id
	var id int64
	total := srcMeta.EntryCount

	for {
		e, rerr := r.Next()
		if errors.Is(rerr, io.EOF) {
			break
		}
		if rerr != nil {
			return rerr
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
			return err2
		}
		id++
		if _, err = insEntry.Exec(id, hw, body); err != nil {
			return err
		}
		txt := ""
		if level == LevelText {
			txt = StripHTML(body)
		}
		if _, err = insFts.Exec(id, hw, txt); err != nil {
			return err
		}
		// trigram index over the accent/case-folded headword powers the
		// "contains" substring mode.
		if _, err = insTrig.Exec(id, dict.Fold(hw)); err != nil {
			return err
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
					return err
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
			return err
		}
	}
	if unresolved > 0 {
		fmt.Fprintf(os.Stderr, "ingest: %d unresolved link targets (skipped)\n", unresolved)
	}

	uuid := make([]byte, 16)
	_, _ = rand.Read(uuid)
	metaKV := map[string]string{
		"dict_uuid":        hex.EncodeToString(uuid),
		"name":             srcMeta.Name,
		"format":           srcMeta.Format,
		"source_path":      srcMeta.Path,
		"description":      srcMeta.Description,
		"entry_count":      fmt.Sprint(id),
		"ingest_level":     string(level),
		"has_trigram":      "1", // entry_trigram present → contains mode (cheap-list flag)
		"created":          time.Now().UTC().Format(time.RFC3339),
		"source_sha256_1M": sourceHash(srcMeta.Path),
	}
	if st, err2 := os.Stat(srcMeta.Path); err2 == nil {
		metaKV["source_size"] = fmt.Sprint(st.Size())
		metaKV["source_mtime"] = st.ModTime().UTC().Format(time.RFC3339)
	}
	for k, v := range metaKV {
		if _, err = tx.Exec("INSERT INTO meta(key, value) VALUES(?, ?)", k, v); err != nil {
			return err
		}
	}

	if _, err = tx.Exec("CREATE INDEX idx_entry_w ON entry(w COLLATE NOCASE)"); err != nil {
		return err
	}
	if _, err = tx.Exec("CREATE INDEX idx_alias_w ON alias(w COLLATE NOCASE)"); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if _, err = db.Exec("INSERT INTO entry_fts(entry_fts) VALUES('optimize')"); err != nil {
		return err
	}
	if _, err = db.Exec("INSERT INTO entry_trigram(entry_trigram) VALUES('optimize')"); err != nil {
		return err
	}
	if _, err = db.Exec("ANALYZE; PRAGMA optimize;"); err != nil {
		return err
	}
	if err = db.Close(); err != nil {
		return err
	}
	if progress != nil {
		progress(int(id), total)
	}
	syncFile(tmp)
	return os.Rename(tmp, dbPath)
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

// CacheBase returns `<dbdir>/<slug>-<hash8>` for a source file: the
// shared base path for its cached text.db / media.db. The hash keys the
// cache to the exact source content, so a changed source re-ingests and
// same-named dictionaries in different formats never collide.
func CacheBase(srcPath, name string) string {
	return filepath.Join(DefaultDBDir(), Slug(name)+"-"+cacheHash8(srcPath))
}

// srcHashCache memoizes CacheBase's 1 MiB source-content hash, keyed by
// source path and invalidated when the file's size or mtime changes.
// CacheBase runs on every dictionary open and every /api/dicts row, so
// recomputing the SHA-256 (open + read 1 MiB + hash) each time was pure
// repeated work.
var srcHashCache sync.Map // srcPath -> srcHashEntry

type srcHashEntry struct {
	size  int64
	mtime time.Time
	hash8 string
}

// cacheHash8 is the memoized first-8-hex-chars of the SHA-256 over a
// source's first 1 MiB. A missing/unreadable file hashes to the empty-input
// digest, exactly as the previous inline CacheBase code did; only successful
// stats are cached, so a file that appears later is picked up on the next call.
func cacheHash8(srcPath string) string {
	st, statErr := os.Stat(srcPath)
	if statErr == nil {
		if v, ok := srcHashCache.Load(srcPath); ok {
			if e := v.(srcHashEntry); e.size == st.Size() && e.mtime.Equal(st.ModTime()) {
				return e.hash8
			}
		}
	}
	h := sha256.New()
	if f, err := os.Open(srcPath); err == nil {
		_, _ = io.CopyN(h, f, 1<<20)
		f.Close()
	}
	hash8 := hex.EncodeToString(h.Sum(nil))[:8]
	if statErr == nil {
		srcHashCache.Store(srcPath, srcHashEntry{size: st.Size(), mtime: st.ModTime(), hash8: hash8})
	}
	return hash8
}

// Slug converts a dictionary display name into a filesystem-safe base
// name for `<slug>.text.db` (D9).
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
// GONOW_DB_DIR overrides it.
func DefaultDBDir() string {
	if dir := os.Getenv("GONOW_DB_DIR"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".gonow-dict/db"
	}
	return filepath.Join(home, ".gonow-dict", "db")
}
