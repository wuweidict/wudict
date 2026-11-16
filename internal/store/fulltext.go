// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/wuweidict/wudict/internal/fsx"
)

// The full-text index is a file of its own beside the text (D195):
// `fulltext.db` in a library folder, `<name>.fulltext.db` beside a loose
// `<name>.text.db`. It holds the FTS5 table and nothing else, so adding full
// text to a prepared dictionary reads the articles the text.db already holds
// and removing it deletes a file; neither rebuilds the text.
//
// The file's presence, paired, is what says a dictionary has full text - the
// rule media.db follows. A text.db written before this layout carries the
// index inside it and says so in its `ingest_level`; both are read (planOf).

// FullTextDBName is the file name of the full-text index inside a library
// folder.
const FullTextDBName = "fulltext.db"

// FullTextVersion stamps what buildFullText and the ingest write into the
// index: its schema, its tokenizer, and StripHTML. An index stamped lower is
// not used.
const FullTextVersion = 1

// FullTextDBPath names the full-text index inside a library folder.
func FullTextDBPath(dir string) string { return filepath.Join(dir, FullTextDBName) }

// FullTextSibling returns the full-text index that pairs with a text.db path,
// in the bundle (`<dir>/fulltext.db`) or the loose (`<name>.fulltext.db`)
// form; "" for a path that is neither.
func FullTextSibling(textDB string) string {
	if strings.EqualFold(filepath.Base(textDB), TextDBName) {
		return FullTextDBPath(filepath.Dir(textDB))
	}
	if base, ok := strings.CutSuffix(textDB, ".text.db"); ok {
		return base + ".fulltext.db"
	}
	return ""
}

// fullTextSchema creates the index in the database named by prefix ("" for
// the connection's main one, "ft." for an attached one) and stamps it with
// the text it indexes. The rowid of a row is its entry's id.
//
// uuid alone does not pair them: a text.db rebuilt from an unchanged source
// keeps its dict_uuid (keptUUID) and may number its entries anew. `created`
// is written by every ingest, so the two together name one build of the text.
func fullTextSchema(db execer, prefix, uuid, created string) error {
	if _, err := db.Exec(fmt.Sprintf(`
		PRAGMA %[1]suser_version = %[2]d;
		CREATE TABLE %[1]smeta(key TEXT PRIMARY KEY, value TEXT);
		CREATE VIRTUAL TABLE %[1]sentry_fts USING fts5(
			w, txt, content='', columnsize=0,
			tokenize='unicode61 remove_diacritics 2');
	`, prefix, sidecarSchema)); err != nil {
		return err
	}
	for k, v := range map[string]string{
		"dict_uuid":        uuid,
		"text_created":     created,
		"fulltext_version": fmt.Sprint(FullTextVersion),
	} {
		if _, err := db.Exec("INSERT INTO "+prefix+"meta(key, value) VALUES(?, ?)", k, v); err != nil {
			return err
		}
	}
	return nil
}

type execer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

// fullTextPaired reports that the index file serves the text.db whose meta is
// m: readable, written by this FullTextVersion, and stamped with that build
// of the text.
func fullTextPaired(ftDB string, m map[string]string) bool {
	if ftDB == "" || m == nil || !fsx.FileExists(ftDB) {
		return false
	}
	fm, schema, err := ReadMetaSchema(ftDB)
	if err != nil || schema != sidecarSchema {
		return false
	}
	return m["dict_uuid"] != "" && fm["dict_uuid"] == m["dict_uuid"] &&
		fm["text_created"] == m["created"] &&
		stampOf(fm, "fulltext_version", 0) == FullTextVersion
}

// planOf is the Plan a prepared database has: full text inside it (a text.db
// from before fulltext.db, which records it as its `ingest_level`), or in the
// paired index beside it.
func planOf(textDB string, m map[string]string) Plan {
	if p := PlanFromMeta(m); p.FullText {
		return p
	}
	return Plan{FullText: fullTextPaired(FullTextSibling(textDB), m)}
}

// fullTextWorkers is how many goroutines turn stored articles into index
// text: decoding and StripHTML are the work, the writer is one.
func fullTextWorkers() int { return encoders() }

// buildFullText writes the full-text index of textDB from the articles it
// already holds, to a temp file renamed into place: the source dictionary is
// not read, so a dictionary whose source is gone can gain full text too. What
// lands in the index is what an ingest with full text writes - the headword
// and StripHTML of the stored article, per entry.
func buildFullText(ctx context.Context, textDB string, progress Progress) (err error) {
	ftDB := FullTextSibling(textDB)
	if ftDB == "" {
		return fmt.Errorf("a full-text index is built beside a text.db, and %s is not one", textDB)
	}
	src, err := openRO(textDB)
	if err != nil {
		return err
	}
	defer src.Close()
	var ver int
	if err = src.QueryRow("PRAGMA user_version").Scan(&ver); err != nil {
		return err
	}
	if !textSchemaOK(ver) {
		return fmt.Errorf("%s: not a wudict database (user_version=%d)", textDB, ver)
	}
	m, err := readMeta(src)
	if err != nil {
		return err
	}
	dec := newBodyDecoder(ver, src)
	defer dec.close()
	var total int
	if err = src.QueryRow("SELECT count(*) FROM entry").Scan(&total); err != nil {
		return err
	}

	tmp := tempDBName(ftDB)
	defer func() {
		if err != nil {
			_ = os.Remove(tmp)
		}
	}()
	out, err := sql.Open(driverName, dsnIngest(tmp))
	if err != nil {
		return err
	}
	defer out.Close()
	out.SetMaxOpenConns(1)
	if err = fullTextSchema(out, "", m["dict_uuid"], m["created"]); err != nil {
		return err
	}
	tx, err := out.Begin()
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	ins := newBulk(tx, "INSERT INTO entry_fts(rowid, w, txt) VALUES", 3)
	defer ins.close()

	// Rows are read in id order, stripped by the workers a batch at a time,
	// and written in the order read: FTS5 builds fastest from ascending rowids.
	type row struct {
		id   int64
		w    string
		body []byte
		txt  string
	}
	rows, err := src.QueryContext(ctx, "SELECT id, w, m FROM entry ORDER BY id")
	if err != nil {
		return err
	}
	defer rows.Close()
	workers := fullTextWorkers()
	batch := make([]row, 0, bulkRows*workers)
	done := 0
	flush := func() error {
		var wg sync.WaitGroup
		for k := 0; k < workers; k++ {
			wg.Add(1)
			go func(k int) {
				defer wg.Done()
				for i := k; i < len(batch); i += workers {
					batch[i].txt = StripHTML(dec.decode(batch[i].body))
					batch[i].body = nil
				}
			}(k)
		}
		wg.Wait()
		for i := range batch {
			if err := ins.add(batch[i].id, batch[i].w, batch[i].txt); err != nil {
				return err
			}
		}
		done += len(batch)
		batch = batch[:0]
		if progress != nil {
			progress(clampDone(done, total), total)
		}
		return ctx.Err()
	}
	for rows.Next() {
		var r row
		var body sql.RawBytes
		if err = rows.Scan(&r.id, &r.w, &body); err != nil {
			return err
		}
		r.body = append([]byte(nil), body...)
		batch = append(batch, r)
		if len(batch) == cap(batch) {
			if err = flush(); err != nil {
				return err
			}
		}
	}
	if err = rows.Err(); err != nil {
		return err
	}
	if err = flush(); err != nil {
		return err
	}
	if err = ins.flush(); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if _, err = out.Exec("INSERT INTO entry_fts(entry_fts) VALUES('optimize')"); err != nil {
		return err
	}
	if err = out.Close(); err != nil {
		return err
	}
	syncFile(tmp)
	return os.Rename(tmp, ftDB)
}

// removeFullText deletes the index beside textDB, and reports whether there
// was one.
func removeFullText(textDB string) (bool, error) {
	ftDB := FullTextSibling(textDB)
	if ftDB == "" || !fsx.FileExists(ftDB) {
		return false, nil
	}
	if err := os.Remove(ftDB); err != nil {
		return false, fmt.Errorf("removing the full-text index: %w", err)
	}
	return true, nil
}
