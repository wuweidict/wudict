// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/wuweidict/wudict/internal/store"
)

// entryState reads the parts of an entry a rescan is supposed to re-derive.
func entryState(e *entry) (d interface{}, err error, backing string) {
	e.dMu.RLock()
	defer e.dMu.RUnlock()
	return e.d, e.err, e.backing
}

// A library folder deleted from outside the app - a file manager, a cleanup
// script - must survive "Rescan folders": the dictionary falls back to its
// source in preview mode and prepares itself again. Before the rescan
// revalidated what it kept, the entry went on holding a handle to the deleted
// database and answered every search with SQLite's "unable to open database
// file", with neither preparation lane willing to rebuild it.
func TestRescanRecoversFromDeletedPreparedFolder(t *testing.T) {
	restorePower(t)
	s, e := demandEntry(t)
	s.AutoIndex = true

	e.demandIndex()
	waitUntil(t, "the demanded index", func() bool { return prepared(e) })
	if _, err := e.open(); err != nil {
		t.Fatalf("open after preparing: %v", err)
	}
	if d, _, backing := entryState(e); backing == "" {
		t.Fatalf("prepared entry recorded no backing database (backend %T)", d)
	} else if _, ok := d.(storeBacked); !ok {
		t.Fatalf("prepared entry is not store-backed: %T", d)
	}

	dir, ok := store.LookupDir(e.Path)
	if !ok {
		t.Fatal("prepared folder not found")
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := s.reg.Rescan(); err != nil {
		t.Fatal(err)
	}

	d, err, backing := entryState(e)
	if d != nil || err != nil || backing != "" {
		t.Fatalf("rescan kept stale state: backend=%T err=%v backing=%q", d, err, backing)
	}
	// The conclusions drawn from the deleted data go with it, or nothing would
	// ever prepare this dictionary again.
	if e.autoTried.Load() || e.demanded.Load() || e.demandFail.Load() != 0 {
		t.Fatalf("preparation flags survived the deletion: auto=%v demanded=%v fail=%v",
			e.autoTried.Load(), e.demanded.Load(), e.demandFail.Load())
	}

	// It opens again - directly, from the source - and searches.
	fresh, err := e.open()
	if err != nil {
		t.Fatalf("reopen after deletion: %v", err)
	}
	if _, ok := fresh.(storeBacked); ok {
		t.Fatalf("expected the direct source backend, got %T", fresh)
	}
	hits, err := fresh.Exact("beta", 10)
	if err != nil || len(hits) != 1 {
		t.Fatalf("search through the recovered backend: %d hits, %v", len(hits), err)
	}

	// and prepares itself again, unasked
	e.maybeAutoIndex()
	waitUntil(t, "the rebuilt index", func() bool { return prepared(e) })
}

// entry.open memoizes failures on purpose (a fan-out must not retry a broken
// file per keystroke), but the memo must not outlive a rescan: that button is
// the user saying "look again", and before this it was the one thing that could
// not be looked at again short of a restart. Set directly rather than provoked,
// because what is under test is the lifetime of the memo, not any one failure.
func TestRescanClearsMemoizedOpenError(t *testing.T) {
	s, e := demandEntry(t)

	e.dMu.Lock()
	e.err = errors.New("boom")
	e.dMu.Unlock()
	if _, err := e.open(); err == nil {
		t.Fatal("open should have returned the memoized failure")
	}

	if err := s.reg.Rescan(); err != nil {
		t.Fatal(err)
	}
	if _, err, _ := entryState(e); err != nil {
		t.Fatalf("rescan kept the memoized error: %v", err)
	}
	if _, err := e.open(); err != nil {
		t.Fatalf("open after rescan: %v", err)
	}
}

// A dictionary that vanishes from the scan stops resolving - and must also stop
// costing. Its backend is unreachable once the entry leaves the registry, so
// nothing else can ever close it: descriptors and, in preview mode, a headword
// map of hundreds of bytes per entry.
func TestRescanClosesVanishedDictionary(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"a.fake", "b.fake"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	isolatedDBDir(t)
	reg, err := NewRegistry([]string{dir}, false)
	if err != nil {
		t.Fatal(err)
	}
	var vanishing *entry
	for _, e := range reg.all() {
		if _, err := e.open(); err != nil {
			t.Fatal(err)
		}
		if filepath.Base(e.Path) == "a.fake" {
			vanishing = e
		}
	}
	if vanishing == nil {
		t.Fatal("a.fake was not discovered")
	}

	if err := os.Remove(filepath.Join(dir, "a.fake")); err != nil {
		t.Fatal(err)
	}
	if err := reg.Rescan(); err != nil {
		t.Fatal(err)
	}
	if n := reg.Count(); n != 1 {
		t.Fatalf("registry still lists %d dictionaries", n)
	}
	if _, err := reg.get(vanishing.ID); err == nil {
		t.Fatal("a deleted dictionary is still addressable by id")
	}
	if d, _, _ := entryState(vanishing); d != nil {
		t.Fatalf("the backend of a vanished dictionary was left open: %T", d)
	}
}
