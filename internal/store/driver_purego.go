// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !sqlite_fts5 || !cgo

package store

// The default driver: modernc.org/sqlite (pure Go, FTS5 always compiled
// in, no C toolchain). This is what a plain `go build` / `go install`
// with no tags gets, and what release cross-builds use so a single ubuntu
// runner can build every platform.
//
// It is the default because it cannot be wrong (D29): FTS5 is mandatory
// for store's schema, and this driver always has it. Opt into the faster
// cgo driver with -tags sqlite_fts5 (driver_cgo.go, D4) - passing that tag
// on a machine without a C compiler falls back here rather than breaking.
// The legacy -tags purego also lands here, which is what it always meant.

import (
	"strconv"

	_ "modernc.org/sqlite"
)

const driverName = "sqlite"

// cacheClause sizes the per-connection page cache when this platform asks for
// something other than the driver default (see pageCacheKiB).
func cacheClause() string {
	if pageCacheKiB <= 0 {
		return ""
	}
	return "&_pragma=cache_size(-" + strconv.Itoa(pageCacheKiB) + ")"
}

// dsnRO is a read-only, query-only connection string (modernc pragma syntax).
func dsnRO(path string) string {
	return "file:" + path + "?mode=ro&_pragma=query_only(1)&_pragma=busy_timeout(5000)" + cacheClause()
}

// dsnIngest is a throwaway-safe bulk-write connection string.
func dsnIngest(path string) string {
	return "file:" + path + "?_pragma=journal_mode(OFF)&_pragma=synchronous(OFF)"
}
