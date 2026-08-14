// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// restorePower puts the process-global power knobs back the way the rest of
// the package expects to find them. Everything here is deliberately global —
// it governs a process, not a request — so every test that moves one must put
// it back or it will be testing the previous test's leftovers.
func restorePower(t *testing.T) {
	t.Helper()
	procs := runtime.GOMAXPROCS(0)
	t.Cleanup(func() {
		powerState.Store(int32(PowerActive))
		activeProcs.Store(0)
		memLimit.Store(0)
		runtime.GOMAXPROCS(procs)
	})
}

// previewRegistry builds a registry over one direct-only dictionary and opens
// it, so there is exactly one reclaimable backend with a nonzero weight.
func previewRegistry(t *testing.T) (*Server, *Registry) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "p.fake"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	isolatedDBDir(t)
	reg, err := NewRegistry([]string{dir}, false)
	if err != nil {
		t.Fatal(err)
	}
	s := New(reg) // AutoIndex false: the dictionary stays in preview mode
	searchStream(t, s, "/api/search?q=beta&mode=prefix&dict=all")
	if len(reg.reclaimables()) != 1 {
		t.Fatalf("expected one open preview backend, got %d", len(reg.reclaimables()))
	}
	return s, reg
}

func TestPowerNames(t *testing.T) {
	for _, c := range []struct {
		in   string
		want Power
		ok   bool
	}{
		{"active", PowerActive, true},
		{"foreground", PowerActive, true},
		{" Background ", PowerBackground, true},
		{"RESTRICTED", PowerRestricted, true},
		{"critical", PowerRestricted, true},
		{"", PowerActive, false},
		{"asleep", PowerActive, false},
	} {
		got, ok := ParsePower(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("ParsePower(%q) = %v, %v; want %v, %v", c.in, got, ok, c.want, c.ok)
		}
	}
	// the wire form must round-trip, since the shell sends what String prints
	for _, p := range []Power{PowerActive, PowerBackground, PowerRestricted} {
		if got, ok := ParsePower(p.String()); !ok || got != p {
			t.Errorf("round-trip %v → %q → %v, %v", p, p.String(), got, ok)
		}
	}
}

// A background process must not look like a busy one: dropping to a single
// thread is what keeps the scheduler off the big cores while the user is
// somewhere else. On a platform that never called SetActiveProcs (every
// desktop) the runtime's own default must be left strictly alone.
func TestPowerParallelism(t *testing.T) {
	restorePower(t)
	_, reg := previewRegistry(t)

	before := runtime.GOMAXPROCS(0)
	reg.SetPower(PowerBackground)
	if got := runtime.GOMAXPROCS(0); got != before {
		t.Fatalf("unmanaged parallelism changed: %d → %d", before, got)
	}
	reg.SetPower(PowerActive)

	SetActiveProcs(2)
	if got := runtime.GOMAXPROCS(0); got != 2 {
		t.Fatalf("SetActiveProcs(2): GOMAXPROCS = %d", got)
	}
	reg.SetPower(PowerBackground)
	if got := runtime.GOMAXPROCS(0); got != 1 {
		t.Errorf("background: GOMAXPROCS = %d, want 1", got)
	}
	reg.SetPower(PowerRestricted)
	if got := runtime.GOMAXPROCS(0); got != 1 {
		t.Errorf("restricted: GOMAXPROCS = %d, want 1", got)
	}
	reg.SetPower(PowerActive)
	if got := runtime.GOMAXPROCS(0); got != 2 {
		t.Errorf("back to active: GOMAXPROCS = %d, want 2", got)
	}
}

// Going away drops every handle that can be rebuilt, without waiting for the
// janitor's idle grace: "recently used" says nothing about what is about to be
// used once the user has left.
func TestPowerShedsOnBackground(t *testing.T) {
	restorePower(t)
	s, reg := previewRegistry(t)

	reg.SetPower(PowerBackground)
	if n := len(reg.reclaimables()); n != 0 {
		t.Fatalf("background left %d reclaimable handles open", n)
	}
	if b := reg.previewBytes(); b != 0 {
		t.Errorf("background left %d bytes of preview memory", b)
	}
	// and the dictionary still answers: eviction is a memory decision, never
	// a functional one — the next query reopens the file.
	reg.SetPower(PowerActive)
	hits := searchStream(t, s, "/api/search?q=beta&mode=prefix&dict=all")
	if len(hits) != 1 || len(hits[0].Results) != 1 {
		t.Fatalf("dictionary stopped answering after a shed: %+v", hits)
	}
}

// Indexing is the most expensive thing this program does. Starting one because
// a search happened to land as the screen went off is exactly how an app gets
// flagged as a battery hog — and the refusal must be a deferral, so the next
// search once the user is back still builds it.
func TestAutoIndexDeferredWhenNotActive(t *testing.T) {
	restorePower(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "d.fake"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	isolatedDBDir(t)
	reg, err := NewRegistry([]string{dir}, false)
	if err != nil {
		t.Fatal(err)
	}
	s := New(reg)
	s.AutoIndex = true

	reg.SetPower(PowerBackground)
	e := reg.all()[0]
	e.maybeAutoIndex()
	if e.autoTried.Load() {
		t.Fatal("a deferred auto-index must not consume the one attempt")
	}

	reg.SetPower(PowerActive)
	e.maybeAutoIndex()
	if !e.autoTried.Load() {
		t.Fatal("auto-index did not start once active")
	}
	// it runs in the background; wait for the folded query only the index can
	// answer, which is what proves the deferral was not a cancellation
	var hits []streamMsg
	for i := 0; i < 200; i++ {
		hits = searchStream(t, s, "/api/search?q=corazon&mode=prefix&dict=all")
		if len(hits) == 1 && !hits[0].Skipped && len(hits[0].Results) == 1 {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("index never became available after returning to active: %+v", hits)
}

// The janitor exists to enforce a budget. With nothing open there is no budget
// to enforce, and a periodic wakeup to discover that — every twenty seconds,
// forever, in a process that outlives its window — is a battery cost for
// nothing. needsSweep is what lets it block instead.
func TestJanitorIdlesWithNothingToReclaim(t *testing.T) {
	restorePower(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "j.fake"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	isolatedDBDir(t)
	reg, err := NewRegistry([]string{dir}, false)
	if err != nil {
		t.Fatal(err)
	}
	reg.SetPreviewBudget(1) // absurdly small: anything open exceeds it
	if reg.needsSweep() {
		t.Fatal("nothing is open yet — there is nothing to sweep")
	}
	s := New(reg)
	searchStream(t, s, "/api/search?q=beta&mode=prefix&dict=all")
	if !reg.needsSweep() {
		t.Fatal("an open backend over budget must wake the janitor")
	}
	reg.SetPower(PowerBackground)
	if reg.needsSweep() {
		t.Fatal("after shedding there is nothing left to sweep")
	}
}

// Pressure is measured against the limit the process was given, so a limit
// below what is already mapped must read as pressure — that is the signal that
// turns "collect harder forever" into "shed something and hand the collector
// real garbage".
func TestMemoryPressureFollowsTheLimit(t *testing.T) {
	restorePower(t)
	memLimit.Store(0)
	if memoryPressure() {
		t.Error("no limit set must never report pressure")
	}
	memLimit.Store(1 << 20) // 1 MB: the runtime has certainly mapped more
	if !memoryPressure() {
		t.Error("a limit far below the mapped heap must report pressure")
	}
	memLimit.Store(1 << 60)
	if memoryPressure() {
		t.Error("a limit far above the mapped heap must not report pressure")
	}
}

// The endpoint is the whole interface between the Android shell and this
// mechanism (D64): the states it accepts, and the fact that it is loopback-only
// like every other control that acts on the machine rather than the library.
func TestPowerEndpoint(t *testing.T) {
	restorePower(t)
	s, reg := previewRegistry(t)

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("POST", "/api/power?state=background", nil))
	if rec.Code != 200 {
		t.Fatalf("POST background: %d: %s", rec.Code, rec.Body.String())
	}
	if CurrentPower() != PowerBackground {
		t.Fatalf("state is %v after a background POST", CurrentPower())
	}
	if n := len(reg.reclaimables()); n != 0 {
		t.Errorf("the POST did not shed: %d handles still open", n)
	}

	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("POST", "/api/power?state=asleep", nil))
	if rec.Code != 400 {
		t.Errorf("unknown state: %d, want 400", rec.Code)
	}
	if CurrentPower() != PowerBackground {
		t.Errorf("a rejected state changed the power state to %v", CurrentPower())
	}

	req := httptest.NewRequest("POST", "/api/power?state=active", nil)
	req.RemoteAddr = "192.0.2.7:5000" // TEST-NET-1: not this machine
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != 403 {
		t.Errorf("remote caller: %d, want 403", rec.Code)
	}
	if CurrentPower() != PowerBackground {
		t.Errorf("a remote caller changed the power state to %v", CurrentPower())
	}
}
