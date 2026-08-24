// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"database/sql"
	"runtime"
	"time"
)

// A library of a hundred prepared dictionaries is a hundred *sql.DB, and each
// one is a pool: every connection it keeps holds its own SQLite page cache
// (2 MiB by default) and its own descriptors, for the life of the process. The
// numbers here bound that (D64).
const (
	// idleConns is how many connections a dictionary keeps warm. One, because
	// concurrency across dictionaries is what this program does - a hundred at
	// once, bounded by the search fan-out - while concurrency WITHIN one
	// dictionary is a single user typing.
	idleConns = 1

	// idleConnTTL returns a connection's page cache to the allocator when the
	// dictionary goes quiet. database/sql's own cleaner does the work and
	// stops itself once the pool is empty, so this costs nothing while idle.
	idleConnTTL = 30 * time.Second
)

// pageCacheKiB is the per-connection page cache, 0 meaning the driver's own
// default (2 MiB). Android halves it: the cache buys throughput on a scan, and
// scans are what this app does least - a lookup is an index seek and a row.
var pageCacheKiB = func() int {
	if runtime.GOOS == "android" {
		return 1024
	}
	return 0
}()

// openRO opens a wudict database read-only with the pool bounded.
//
// Deliberately NOT SetMaxOpenConns: capping the pool would make one query wait
// for another to return its connection, and any path that holds a *sql.Rows
// open while issuing a second query on the same handle would then deadlock
// rather than merely queue. Idle bounds give the memory back without ever
// changing what a query is allowed to do.
func openRO(path string) (*sql.DB, error) {
	db, err := sql.Open(driverName, dsnRO(path))
	if err != nil {
		return nil, err
	}
	db.SetMaxIdleConns(idleConns)
	db.SetConnMaxIdleTime(idleConnTTL)
	return db, nil
}
