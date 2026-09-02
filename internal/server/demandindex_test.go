// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// demandEntry is one unprepared dictionary in an isolated library, with the
// registry's own view of it. AutoIndex is left off: every test here drives
// demandIndex directly, so nothing else may be preparing the same file.
func demandEntry(t *testing.T) (*Server, *entry) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "d.fake"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	isolatedDBDir(t)
	reg, err := NewRegistry([]string{dir}, false)
	if err != nil {
		t.Fatal(err)
	}
	return New(reg), reg.all()[0]
}

// waitUntil polls a condition the ingest goroutine will make true. Polling
// rather than a channel because the thing being observed is the entry's own
// state, which is what the deferred slot reads.
func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for i := 0; i < 400; i++ {
		if cond() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func prepared(e *entry) bool {
	p, ok := preparedFor(e.Path)
	return ok && fileExists(p)
}

// A demand is a finger on the screen, so it does NOT consult the power state -
// the one exception in this file, and the reason it exists: a demand declined
// for a stale background/thermal flag is a dictionary that searches but never
// prepares, and says nothing about why.
func TestDemandIndexIgnoresPowerState(t *testing.T) {
	restorePower(t)
	_, e := demandEntry(t)

	powerState.Store(int32(PowerRestricted))
	e.demandIndex()
	waitUntil(t, "the demanded index", func() bool { return prepared(e) })
	if !e.indexing() {
		t.Error("a demand that ran must report itself as indexing")
	}
}

// A failed demand is retryable. The once-per-process flag is there to stop a
// per-keystroke re-queue, not to make one unwritable library permanent - so the
// flag is released, the cooldown holds the keystrokes off, and the next demand
// after it does the work.
func TestFailedDemandIsRetried(t *testing.T) {
	restorePower(t)
	_, e := demandEntry(t)

	// A regular file where the library folder should be: MkdirAll fails, so
	// ClaimDir fails, so the ingest never starts - the shape of a read-only or
	// full disk, without needing either.
	blocked := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocked, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WUDICT_DB_DIR", blocked)

	e.demandIndex()
	waitUntil(t, "the failure to be recorded", func() bool { return e.demandFail.Load() != 0 })
	if e.demanded.Load() {
		t.Fatal("a failed demand must release the attempt, not keep it")
	}
	if prepared(e) {
		t.Fatal("setup: the ingest was supposed to fail")
	}

	// Within the cooldown nothing is re-queued, whatever asks: this is the
	// keystroke case, where a search runs per character typed.
	for i := 0; i < 5; i++ {
		e.demandIndex()
	}
	if e.demanded.Load() {
		t.Fatal("a demand inside the cooldown must be a no-op")
	}

	// Past it - and with the library writable again - the same tap works.
	e.demandFail.Store(time.Now().Add(-2 * demandRetryAfter).UnixNano())
	t.Setenv("WUDICT_DB_DIR", t.TempDir())
	e.demandIndex()
	waitUntil(t, "the retried index", func() bool { return prepared(e) })
}

// What the client is told. A deferred slot for a dictionary already being
// prepared carries `indexing`, so the page can stop offering the same tap at
// something that is already doing the work.
func TestDeferredSlotReportsIndexing(t *testing.T) {
	restorePower(t)
	dir := t.TempDir()
	// Two, because a search naming ONE dictionary is a demand and is never
	// capped (handleSearch): a deferral needs something to fan out across.
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
	s := New(reg)

	// Learn the price, then evict and leave room for exactly one: a fan-out
	// cannot cap what it has never opened (fanout_test), so both steps matter.
	if hits := searchStream(t, s, "/api/search?q=beta&mode=prefix&dict=all"); len(hits) != 2 {
		t.Fatalf("setup: %d slots, want 2", len(hits))
	}
	price := reg.all()[0].lastWeight.Load()
	if price <= 0 {
		t.Fatalf("setup: no price learned, got %d", price)
	}
	for _, e := range reg.all() {
		e.evict()
	}
	reg.SetSearchBudget(price)

	deferred := func() streamMsg {
		t.Helper()
		for _, e := range reg.all() {
			e.evict()
		}
		for _, h := range searchStream(t, s, "/api/search?q=beta&mode=prefix&dict=all") {
			if h.Deferred {
				return h
			}
		}
		t.Fatal("a one-dictionary budget must defer the other one")
		return streamMsg{}
	}

	if h := deferred(); h.Indexing {
		t.Error("nothing has been demanded yet: the slot must not claim to be preparing")
	}

	// The flag is set directly rather than by demanding, and on both entries
	// because which one loses the budget race is not fixed: a real demand here
	// would PREPARE the dictionary within milliseconds, and a prepared
	// dictionary weighs nothing and is never deferred again - so the state
	// being asserted could not be observed through the door it arrives by.
	for _, e := range reg.all() {
		e.demanded.Store(true)
	}
	h := deferred()
	if !h.Indexing {
		t.Error("a deferred slot whose dictionary is being prepared must say so")
	}
	if h.Error != "" {
		t.Errorf("indexing is not an error: %q", h.Error)
	}
}
