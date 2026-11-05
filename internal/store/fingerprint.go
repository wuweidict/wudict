// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

// Fingerprint hashes the LOGICAL content of a prepared text.db: what a query
// can observe, independent of how the file happens to be laid out. Two ingests
// of the same source by the same code produce the same fingerprint; a change
// to a Reader, to IngestPlan or to the markup changes it.
// That is what the golden tests in the format packages pin, so a behaviour
// change cannot ship without the version bump that tells an existing library
// it is outdated (stale.go).
//
// Hashed, in order: the meta table minus what varies between two identical
// builds (identity, timestamps, the source's location on disk, the version
// stamps themselves, the body encoding); every entry with its aliases and its
// DECODED article; and the full-text index as (term, row, column, offset)
// instances, read through fts5vocab since it is contentless. Page layout,
// rowid gaps inside SQLite, compression and statistics are not content and
// are not hashed.
func Fingerprint(textDB string) (string, error) {
	m, schema, err := ReadMetaSchema(textDB)
	if err != nil {
		return "", err
	}
	if !textSchemaOK(schema) {
		return "", fmt.Errorf("%s: schema %d, want %d to %d", textDB, schema, textSchemaMin, textSchemaMax)
	}
	h := sha256.New()
	field := func(s string) {
		var n [8]byte
		binary.LittleEndian.PutUint64(n[:], uint64(len(s)))
		h.Write(n[:])
		h.Write([]byte(s))
	}

	keys := make([]string, 0, len(m))
	for k := range m {
		if !volatileMeta(k) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	field("meta")
	for _, k := range keys {
		field(k)
		field(m[k])
	}

	s, err := Open(textDB)
	if err != nil {
		return "", err
	}
	defer s.Close()
	field("entries")
	if err := s.EachEntry(func(hw string, alts []string, body string) error {
		field(hw)
		alts = append([]string(nil), alts...)
		sort.Strings(alts) // alias rowids follow redirect order, which is not content
		field(fmt.Sprint(len(alts)))
		for _, a := range alts {
			field(a)
		}
		field(body)
		return nil
	}); err != nil {
		return "", err
	}

	field("entry_fts")
	if err := hashVocab(s, "entry_fts", field); err != nil {
		return "", fmt.Errorf("%s: entry_fts: %w", textDB, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// volatileMeta names the meta keys that differ between two builds of the same
// content: identity, time, where the source sat on disk, and the version
// stamps (which the golden records separately, so a bump is a deliberate edit
// of the golden rather than an accident of the hash).
func volatileMeta(k string) bool {
	switch k {
	case "dict_uuid", "created", "source_path", "source_size", "source_mtime",
		"source_sha256_1M", "body_encoding", "body_dict_bytes",
		"ingest_version", "reader_version", "markup_version":
		return true
	}
	return strings.HasSuffix(k, "_path") || strings.HasSuffix(k, "_mtime")
}

// hashVocab feeds every token instance of a contentless FTS5 table into the
// hash, in a total order. The fts5vocab table lives in the connection's TEMP
// schema, so it is created on one pinned connection; the main database stays
// read-only (mode=ro) - only the query_only guard is lifted, and only on this
// connection of a handle Fingerprint closes when it returns.
func hashVocab(s *Store, table string, field func(string)) error {
	ctx := context.Background()
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "PRAGMA query_only = 0"); err != nil {
		return err
	}
	vocab := "temp.wudict_vocab_" + table
	if _, err := conn.ExecContext(ctx, fmt.Sprintf(
		"CREATE VIRTUAL TABLE IF NOT EXISTS %s USING fts5vocab(main, %s, instance)", vocab, table)); err != nil {
		return err
	}
	rows, err := conn.QueryContext(ctx, fmt.Sprintf(
		"SELECT term, doc, col, offset FROM %s ORDER BY term, doc, col, offset", vocab))
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var term, col string
		var doc, off int64
		if err := rows.Scan(&term, &doc, &col, &off); err != nil {
			return err
		}
		field(term)
		field(fmt.Sprint(doc))
		field(col)
		field(fmt.Sprint(off))
	}
	return rows.Err()
}
