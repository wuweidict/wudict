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

	"github.com/glowinthedark/gonow-dict/internal/dict"
)

// Progress is called during ingest with entries processed so far and the
// total (0 when unknown).
type Progress func(done, total int)

const batchSize = 5000

// Ingest scans r into a new text database at dbPath (atomically: written
// to a temp file, renamed on success). The FTS index is built inside the
// same transaction as the data (FTS-audit #3).
func Ingest(r dict.Reader, dbPath string, progress Progress) (err error) {
	srcMeta := r.Meta()
	tmp := dbPath + ".ingest"
	_ = os.Remove(tmp)
	defer func() {
		if err != nil {
			_ = os.Remove(tmp)
		}
	}()
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return err
	}

	db, err := sql.Open("sqlite3", "file:"+tmp+"?_journal_mode=OFF&_synchronous=OFF")
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
		if _, err = insFts.Exec(id, hw, StripHTML(body)); err != nil {
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
		"ingest_level":     "text",
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
	if _, err = db.Exec("ANALYZE; PRAGMA optimize;"); err != nil {
		return err
	}
	if err = db.Close(); err != nil {
		return err
	}
	if progress != nil {
		progress(int(id), total)
	}
	return os.Rename(tmp, dbPath)
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
	h := sha256.New()
	if f, err := os.Open(srcPath); err == nil {
		_, _ = io.CopyN(h, f, 1<<20)
		f.Close()
	}
	hash8 := hex.EncodeToString(h.Sum(nil))[:8]
	return filepath.Join(DefaultDBDir(), Slug(name)+"-"+hash8)
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
