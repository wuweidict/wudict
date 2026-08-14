// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build sqlite_fts5 && cgo

package store

// The fast driver: mattn/go-sqlite3 (cgo — decision D4), selected by
// -tags sqlite_fts5 as `make build` and `make install` pass.
//
// BOTH conditions are required, and neither is optional (D29). mattn
// compiles SQLite *without* FTS5 unless the sqlite_fts5 tag sets
// -DSQLITE_ENABLE_FTS5 in its own cgo CFLAGS, and store's base schema
// always creates an FTS5 table (see ingest.go) — so a mattn build
// missing that tag fails at runtime with "no such module: fts5". The
// && cgo half keeps CGO_ENABLED=0 away from mattn's !cgo stub, whose
// registered driver refuses every Open.
//
// Anything this constraint rejects lands on driver_purego.go, which is
// always correct if slower. The two constraints are exhaustive by
// construction, so no flag combination can produce a binary without FTS5.

import (
	"strconv"

	_ "github.com/mattn/go-sqlite3"
)

const driverName = "sqlite3"

// cacheClause sizes the per-connection page cache when this platform asks for
// something other than the driver default (see pageCacheKiB).
func cacheClause() string {
	if pageCacheKiB <= 0 {
		return ""
	}
	return "&_cache_size=-" + strconv.Itoa(pageCacheKiB)
}

// dsnRO is a read-only, query-only connection string.
func dsnRO(path string) string {
	return "file:" + path + "?mode=ro&_query_only=1&_busy_timeout=5000" + cacheClause()
}

// dsnIngest is a throwaway-safe bulk-write connection string (the ingest
// target is a temp file renamed on success, so durability is pointless).
func dsnIngest(path string) string {
	return "file:" + path + "?_journal_mode=OFF&_synchronous=OFF"
}
