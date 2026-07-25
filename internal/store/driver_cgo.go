// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !purego

package store

// Default driver: mattn/go-sqlite3 (cgo, fastest — decision D4). Build
// with -tags sqlite_fts5. Release cross-builds use -tags purego instead
// (see driver_purego.go) so the GitHub workflow needs no C toolchains.

import _ "github.com/mattn/go-sqlite3"

const driverName = "sqlite3"

// dsnRO is a read-only, query-only connection string.
func dsnRO(path string) string {
	return "file:" + path + "?mode=ro&_query_only=1"
}

// dsnIngest is a throwaway-safe bulk-write connection string (the ingest
// target is a temp file renamed on success, so durability is pointless).
func dsnIngest(path string) string {
	return "file:" + path + "?_journal_mode=OFF&_synchronous=OFF"
}
