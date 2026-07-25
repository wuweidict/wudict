// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build purego

package store

// Pure-Go driver: modernc.org/sqlite (FTS5 included, CGO_ENABLED=0).
// Used by release cross-builds so a single ubuntu runner can build every
// platform; local/native builds default to mattn (driver_cgo.go, D4).

import _ "modernc.org/sqlite"

const driverName = "sqlite"

// dsnRO is a read-only, query-only connection string (modernc pragma syntax).
func dsnRO(path string) string {
	return "file:" + path + "?mode=ro&_pragma=query_only(1)&_pragma=busy_timeout(5000)"
}

// dsnIngest is a throwaway-safe bulk-write connection string.
func dsnIngest(path string) string {
	return "file:" + path + "?_pragma=journal_mode(OFF)&_pragma=synchronous(OFF)"
}
