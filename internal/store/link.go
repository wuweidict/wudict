// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/wuweidict/wudict/internal/resource"
)

// media.link.db - where a prepared dictionary's media IS, when it is not
// packed (O8).
//
// This file is a CACHE and is treated as one everywhere: it is derived from
// the source in one pass, its absence is never an error, deleting it is always
// safe, and it is NOT part of the folder's portable contract (D20) - a folder
// carried to another machine without its source still needs media.db. That is
// why it is a separate file rather than a table in text.db: dropping a cache
// must not mean rewriting the database that holds the articles.
//
// The pairing is checked twice, because a location is only meaningful for the
// exact bytes it was taken from:
//
//   - dict_uuid, against the text.db it sits beside - the same check media.db
//     gets, for the same reason.
//   - size and mtime of EVERY container, against the files on disk. A
//     re-downloaded or recompressed .mdd is the dangerous case: the offsets
//     still resolve, and would serve the wrong bytes rather than a miss.
//
// Either mismatch is reported as an error so the caller can delete the file
// and rebuild it, which costs one enumeration.

// LinkDBName is the cache's file name inside a library folder.
const LinkDBName = "media.link.db"

// LinkDBPath names the locator cache inside a library folder.
func LinkDBPath(dir string) string { return filepath.Join(dir, LinkDBName) }

// LinkSibling returns the locator cache that pairs with a text.db path, in
// either the bundle (`<dir>/media.link.db`) or the loose
// (`<name>.media.link.db`) form - mirroring MediaSibling, so a text.db copied
// out of its folder keeps whichever siblings came with it.
func LinkSibling(textDB string) string {
	if strings.EqualFold(filepath.Base(textDB), TextDBName) {
		return LinkDBPath(filepath.Dir(textDB))
	}
	if base, ok := strings.CutSuffix(textDB, ".text.db"); ok {
		return base + ".media.link.db"
	}
	return ""
}

// WriteLinks records the containers and locations of one dictionary's media,
// atomically: a temporary file, fsynced, then renamed, so an interrupted build
// leaves no half-written cache that the next open would have to distrust.
func WriteLinks(dbPath string, parts []resource.Part, links []resource.Link, dictUUID string) (err error) {
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
		CREATE TABLE part(id INTEGER PRIMARY KEY, path TEXT, size INTEGER, mtime INTEGER);
		CREATE TABLE link(name TEXT PRIMARY KEY, mime TEXT, part INTEGER, off INTEGER, size INTEGER);
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
	if _, err = tx.Exec("INSERT INTO meta(key, value) VALUES('dict_uuid', ?)", dictUUID); err != nil {
		return err
	}
	for i, p := range parts {
		if _, err = tx.Exec("INSERT INTO part(id, path, size, mtime) VALUES(?, ?, ?, ?)",
			i, p.Path, p.Size, p.MTime); err != nil {
			return err
		}
	}
	ins, err := tx.Prepare("INSERT OR IGNORE INTO link(name, mime, part, off, size) VALUES(?, ?, ?, ?, ?)")
	if err != nil {
		return err
	}
	for _, l := range links {
		if _, err = ins.Exec(l.Name, l.MIME, l.Part, l.Off, l.Size); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if err = db.Close(); err != nil {
		return err
	}
	syncFile(tmp)
	return os.Rename(tmp, dbPath)
}

// Links is an opened locator cache, validated against both its text.db and
// the containers it points into.
type Links struct {
	db    *sql.DB
	parts []resource.Part
}

// OpenLinks opens the cache and proves it still describes the files on disk.
// Every error means the same thing to a caller - "this cache is unusable" -
// and the answer is always to delete it and rebuild, never to serve from it.
func OpenLinks(dbPath, dictUUID string) (*Links, error) {
	db, err := openRO(dbPath)
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
		return nil, fmt.Errorf("%s: not a wudict media locator", dbPath)
	}
	m, err := readMeta(db)
	if err != nil {
		db.Close()
		return nil, err
	}
	if m["dict_uuid"] != dictUUID {
		db.Close()
		return nil, fmt.Errorf("%s: belongs to another dictionary", dbPath)
	}
	rows, err := db.Query("SELECT id, path, size, mtime FROM part ORDER BY id")
	if err != nil {
		db.Close()
		return nil, err
	}
	var parts []resource.Part
	for rows.Next() {
		var id int
		var p resource.Part
		if err := rows.Scan(&id, &p.Path, &p.Size, &p.MTime); err != nil {
			rows.Close()
			db.Close()
			return nil, err
		}
		if id != len(parts) {
			rows.Close()
			db.Close()
			return nil, fmt.Errorf("%s: container ids are not contiguous", dbPath)
		}
		parts = append(parts, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		db.Close()
		return nil, err
	}
	if len(parts) == 0 {
		db.Close()
		return nil, fmt.Errorf("%s: no containers recorded", dbPath)
	}
	for _, p := range parts {
		st, err := os.Stat(p.Path)
		if err != nil {
			db.Close()
			return nil, fmt.Errorf("%s: %w", dbPath, err)
		}
		if st.Size() != p.Size || st.ModTime().Unix() != p.MTime {
			db.Close()
			return nil, fmt.Errorf("%s: %s changed since the locations were recorded", dbPath, p.Path)
		}
	}
	return &Links{db: db, parts: parts}, nil
}

// Parts returns the recorded containers, in id order - what a format's
// Fetcher is opened over.
func (l *Links) Parts() []resource.Part { return append([]resource.Part(nil), l.parts...) }

// Lookup resolves an article's resource name to a location.
func (l *Links) Lookup(name string) (resource.Link, bool) {
	key := resource.Key(name)
	if key == "" {
		return resource.Link{}, false
	}
	out := resource.Link{Name: key}
	err := l.db.QueryRow("SELECT mime, part, off, size FROM link WHERE name = ?", key).
		Scan(&out.MIME, &out.Part, &out.Off, &out.Size)
	if err != nil {
		return resource.Link{}, false
	}
	return out, true
}

func (l *Links) Close() error { return l.db.Close() }
