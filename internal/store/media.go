// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"bytes"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/glowinthedark/gonow-dict/internal/dict"
)

// Media is one opened `media.db` (SPEC §3): binary resources
// packed at ingest=full, paired to its text.db by dict_uuid.
type Media struct {
	db   *sql.DB
	UUID string
}

func OpenMedia(path string) (*Media, error) {
	db, err := sql.Open(driverName, dsnRO(path))
	if err != nil {
		return nil, err
	}
	var ver int
	if err := db.QueryRow("PRAGMA user_version").Scan(&ver); err != nil {
		db.Close()
		return nil, err
	}
	if ver != schemaVersion {
		db.Close()
		return nil, fmt.Errorf("%s: not a gonow-dict media database", path)
	}
	m, err := readMeta(db)
	if err != nil {
		db.Close()
		return nil, err
	}
	return &Media{db: db, UUID: m["dict_uuid"]}, nil
}

func (m *Media) Close() error { return m.db.Close() }

func (m *Media) Resource(name string) (io.ReadCloser, string, error) {
	var mime string
	var data []byte
	err := m.db.QueryRow("SELECT mime, data FROM resource WHERE name = ?", name).Scan(&mime, &data)
	if err == sql.ErrNoRows {
		// MDD names are indexed lower-cased while an article may reference
		// mixed case (and loose files are packed under their real name), so a
		// miss retries case-insensitively rather than 404-ing on spelling.
		err = m.db.QueryRow("SELECT mime, data FROM resource WHERE name = ? COLLATE NOCASE", name).Scan(&mime, &data)
	}
	if err == sql.ErrNoRows {
		return nil, "", dict.ErrNotFound
	}
	if err != nil {
		return nil, "", err
	}
	return io.NopCloser(bytes.NewReader(data)), mime, nil
}

// assetRef matches a resource reference in article HTML. Quoted forms only:
// unquoted attributes are rare in dictionary markup and would drag in noise.
var assetRef = regexp.MustCompile(`(?i)(?:href|src|data|poster)\s*=\s*["']([^"']+)["']`)

// ReferencedAssets returns the relative resource names an already-prepared
// dictionary's articles refer to. Packing uses it to include files that live
// beside the .mdx rather than inside the .mdd (a repack's stylesheet and
// scripts) — but only the ones actually referenced: dictionary folders often
// hold several dictionaries, so sweeping the directory would pack a neighbour's
// assets.
//
// Reads the prepared text.db rather than re-parsing the source: the bodies are
// already there, and packing media is an explicit, minutes-long operation where
// one decompression pass costs nothing.
func ReferencedAssets(textDB string) ([]string, error) {
	db, err := sql.Open(driverName, dsnRO(textDB))
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.Query("SELECT m FROM entry")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	const maxDistinct = 20000 // a pathological article must not blow up memory
	seen := map[string]bool{}
	var out []string
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return out, err
		}
		for _, m := range assetRef.FindAllStringSubmatch(decodeBody(raw), -1) {
			name := strings.TrimSpace(m[1])
			if !isRelativeAsset(name) || seen[name] {
				continue
			}
			seen[name] = true
			out = append(out, name)
			if len(out) >= maxDistinct {
				return out, nil
			}
		}
	}
	return out, rows.Err()
}

// isRelativeAsset keeps references that name a file belonging to this
// dictionary: no scheme (http:, data:, bword:, sound:…), no fragment or query
// only, nothing climbing out of the folder.
func isRelativeAsset(s string) bool {
	if s == "" || strings.HasPrefix(s, "#") || strings.HasPrefix(s, "?") || strings.HasPrefix(s, "//") {
		return false
	}
	if schemeRef.MatchString(s) {
		return false
	}
	if strings.Contains(s, "..") {
		return false
	}
	return true
}

// schemeRef matches a real URI scheme at the start of a reference.
var schemeRef = regexp.MustCompile(`(?i)^[a-z][a-z0-9+.-]*:`)

// IngestMedia packs every resource of d into a media.db at dbPath,
// stamped with dictUUID (must be the paired text.db's dict_uuid).
func IngestMedia(d dict.Dictionary, names []string, dbPath, dictUUID string, progress Progress) (err error) {
	tmp := tempDBName(dbPath)
	_ = os.Remove(tmp)
	defer func() {
		if err != nil {
			_ = os.Remove(tmp)
		}
	}()

	db, err := sql.Open(driverName, dsnIngest(tmp))
	if err != nil {
		return err
	}
	defer db.Close()

	if _, err = db.Exec(fmt.Sprintf(`
		PRAGMA user_version = %d;
		CREATE TABLE meta(key TEXT PRIMARY KEY, value TEXT);
		CREATE TABLE resource(name TEXT PRIMARY KEY, mime TEXT, data BLOB);
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
	m := d.Meta()
	for k, v := range map[string]string{
		"dict_uuid": dictUUID,
		"name":      m.Name,
		"format":    m.Format,
	} {
		if _, err = tx.Exec("INSERT INTO meta(key, value) VALUES(?, ?)", k, v); err != nil {
			return err
		}
	}
	ins, err := tx.Prepare("INSERT OR IGNORE INTO resource(name, mime, data) VALUES(?, ?, ?)")
	if err != nil {
		return err
	}
	for i, name := range names {
		rc, mime, rerr := d.Resource(name)
		if rerr != nil {
			continue // missing/corrupt resource: skip, keep packing
		}
		data, rerr := io.ReadAll(rc)
		rc.Close()
		if rerr != nil {
			continue
		}
		if _, err = ins.Exec(name, mime, data); err != nil {
			return err
		}
		if progress != nil && (i+1)%100 == 0 {
			progress(i+1, len(names))
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if err = db.Close(); err != nil {
		return err
	}
	if progress != nil {
		progress(len(names), len(names))
	}
	syncFile(tmp)
	if err = os.Rename(tmp, dbPath); err != nil {
		return err
	}
	// the receipt now has media to describe (best-effort, see IngestLevel).
	if strings.EqualFold(filepath.Base(dbPath), MediaDBName) {
		_ = WriteInfo(filepath.Dir(dbPath))
	}
	return nil
}

// ReadMetaValue reads one meta value from a gonow database file.
func ReadMetaValue(dbPath, key string) (string, error) {
	db, err := sql.Open(driverName, dsnRO(dbPath))
	if err != nil {
		return "", err
	}
	defer db.Close()
	var v string
	if err := db.QueryRow("SELECT value FROM meta WHERE key = ?", key).Scan(&v); err != nil {
		return "", err
	}
	return v, nil
}
