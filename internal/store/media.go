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

	"github.com/glowinthedark/gonow-dict/internal/dict"
)

// Media is one opened `<slug>.media.db` (SPEC §3): binary resources
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
		return nil, "", dict.ErrNotFound
	}
	if err != nil {
		return nil, "", err
	}
	return io.NopCloser(bytes.NewReader(data)), mime, nil
}

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
	return os.Rename(tmp, dbPath)
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
