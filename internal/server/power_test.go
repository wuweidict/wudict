// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"testing"
	"time"
)

// restorePower puts the process-global power knobs back the way the rest of
// the package expects to find them. Everything here is deliberately global -
// it governs a process, not a request - so every test that moves one must put
// it back or it will be testing the previous test's leftovers.
func restorePower(t *testing.T) {
	t.Helper()
	procs := runtime.GOMAXPROCS(0)
	t.Cleanup(func() {
		powerState.Store(int32(PowerActive))
		activeProcs.Store(0)
		memLimit.Store(0)
		memLimitConfigured.Store(0)
		pressurePasses.Store(0)
		// and the runtime's own ceiling, which is NOT the atomic above: a test
		// that left a 1 MB limit installed would make every test after it run
		// in continuous GC, which is the failure being tested rather than a
		// condition to inflict on the rest of the package.
		debug.SetMemoryLimit(math.MaxInt64)
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

// loopbackReq is a POST that came from this machine, which every control that
// acts on the process rather than the library requires.
func loopbackReq(target string) *http.Request {
	req := httptest.NewRequest("POST", target, nil)
	req.RemoteAddr = "127.0.0.1:5555"
	return req
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
	// a functional one - the next query reopens the file.
	reg.SetPower(PowerActive)
	hits := searchStream(t, s, "/api/search?q=beta&mode=prefix&dict=all")
	if len(hits) != 1 || len(hits[0].Results) != 1 {
		t.Fatalf("dictionary stopped answering after a shed: %+v", hits)
	}
}

// Indexing is the most expensive thing this program does. Starting one because
// a search happened to land as the screen went off is exactly how an app gets
// flagged as a battery hog - and the refusal must be a deferral, so the next
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
// to enforce, and a periodic wakeup to discover that - every twenty seconds,
// forever, in a process that outlives its window - is a battery cost for
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
		t.Fatal("nothing is open yet - there is nothing to sweep")
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
	// One exception, and it is the whole reason needsSweep does not simply
	// return false here: a ceiling this process cannot fit under is not
	// "nothing to do", and the janitor is the only thing that corrects it.
	memLimit.Store(1 << 20)
	if !reg.needsSweep() {
		t.Fatal("an impossible ceiling with nothing left to shed must still wake the janitor")
	}
}

// A ceiling below what the program genuinely holds cannot be obeyed, and trying
// costs ~45% of CPU indefinitely (D64, measured). It must therefore yield -
// but only after shedding has been given several passes to prove it cannot fix
// this, so a transient spike never permanently raises the ceiling.
func TestRelaxLiftsAnImpossibleCeiling(t *testing.T) {
	restorePower(t)
	const impossible = 1 << 20 // 1 MB: the runtime mapped more than this before main
	SetMemoryLimit(impossible)
	if !memoryPressure() {
		t.Fatal("a 1 MB ceiling must read as pressure")
	}
	for i := 1; i < relaxAfterPasses; i++ {
		adjustLimit()
		if got := memLimit.Load(); got != impossible {
			t.Fatalf("ceiling moved to %d after only %d passes", got, i)
		}
	}
	adjustLimit()
	raised := memLimit.Load()
	if raised <= impossible {
		t.Fatalf("ceiling did not yield after %d passes: %d", relaxAfterPasses, raised)
	}
	if memoryPressure() {
		t.Error("the raised ceiling is still under pressure - the headroom is too small to end the thrash")
	}
	if got := memLimitConfigured.Load(); got != impossible {
		t.Errorf("relaxing forgot what was configured: %d", got)
	}
	// and it must not creep upwards on every later pass
	adjustLimit()
	if got := memLimit.Load(); got != raised {
		t.Errorf("ceiling moved again with no pressure: %d → %d", raised, got)
	}
}

// Restoring must not re-create the state it is recovering from, so it waits for
// the footprint to fall clear of the configured value rather than merely below
// it.
func TestRestoreWaitsForRoom(t *testing.T) {
	restorePower(t)
	const impossible = 1 << 20
	SetMemoryLimit(impossible)
	for i := 0; i < relaxAfterPasses; i++ {
		adjustLimit()
	}
	raised := memLimit.Load()
	if raised <= impossible {
		t.Fatalf("setup: ceiling never yielded (%d)", raised)
	}

	restoreMemoryLimit()
	if got := memLimit.Load(); got != raised {
		t.Errorf("restored a ceiling the process still does not fit under: %d", got)
	}

	// The other half cannot be reached by shrinking a live heap on demand, so
	// it is constructed: a configured ceiling the footprint now fits under
	// with room to spare, and an in-force ceiling still raised above it.
	inUse := heapInUse()
	roomy := inUse * 2 // 0.85 × this is comfortably above what is in use
	memLimitConfigured.Store(roomy)
	memLimit.Store(roomy * 4)
	restoreMemoryLimit()
	if got := memLimit.Load(); got != roomy {
		t.Errorf("a ceiling with room to spare was not restored: %d, want %d", got, roomy)
	}

	// and the boundary the other way: configured exactly at the footprint is
	// pressure by definition, so restoring it would re-create what the relax
	// was for.
	memLimitConfigured.Store(inUse)
	memLimit.Store(inUse * 4)
	restoreMemoryLimit()
	if got := memLimit.Load(); got != inUse*4 {
		t.Errorf("restored a ceiling that is itself under pressure: %d", got)
	}
}

// A raised ceiling is unfinished business: the janitor must keep taking passes
// until it can hand it back, even when there is nothing left to reclaim and no
// pressure to report. Measured on a phone (PERF §8.5) that is precisely the
// state the passes stopped in - one heavy search raised the ceiling to 6 GB,
// the heap then drained, and nothing ever asked for the configured 384 MB back.
func TestJanitorKeepsGoingWhileTheCeilingIsRaised(t *testing.T) {
	restorePower(t)
	r := &Registry{}
	if r.needsSweep() {
		t.Fatal("setup: an empty registry under no limit wants a sweep")
	}

	inUse := heapInUse()
	memLimitConfigured.Store(inUse * 2)
	memLimit.Store(inUse * 8) // relaxed, and roomy enough that there is no pressure
	if memoryPressure() {
		t.Fatal("setup: constructed state is under pressure, which would pass for the wrong reason")
	}
	if !r.needsSweep() {
		t.Error("janitor went to sleep under a ceiling it had raised")
	}

	// and it stops again once the ceiling is its own
	memLimit.Store(memLimitConfigured.Load())
	if r.needsSweep() {
		t.Error("janitor kept waking with nothing to do and its own ceiling in force")
	}
}

// Pressure is measured against the limit the process was given, so a limit
// below what is already mapped must read as pressure - that is the signal that
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

	// httptest.NewRequest's default RemoteAddr is 192.0.2.1, i.e. NOT this
	// machine, so every accepted call here has to say where it came from.
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, loopbackReq("/api/power?state=background"))
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
	s.ServeHTTP(rec, loopbackReq("/api/power?state=asleep"))
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
