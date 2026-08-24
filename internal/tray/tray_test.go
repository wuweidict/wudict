// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package tray

import (
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakePlatform stands in for the OS. Every test drives machine T through it,
// so none of them needs a desktop, a D-Bus session or a window server.
type fakePlatform struct {
	startErr   error
	startPanic bool
	runErr     error
	runNow     bool // Run returns immediately instead of blocking

	started  atomic.Bool
	stopped  atomic.Bool
	items    []Item
	unblock  chan struct{}
	stopOnce sync.Once
}

func newFake() *fakePlatform { return &fakePlatform{unblock: make(chan struct{})} }

func (f *fakePlatform) Start(_ Config, items []Item) error {
	if f.startPanic {
		panic("boom")
	}
	f.items = items
	if f.startErr != nil {
		return f.startErr
	}
	f.started.Store(true)
	return nil
}

func (f *fakePlatform) Run() error {
	if f.runNow {
		return f.runErr
	}
	<-f.unblock
	return f.runErr
}

func (f *fakePlatform) Stop() {
	f.stopped.Store(true)
	f.stopOnce.Do(func() { close(f.unblock) })
}

// withPlatform installs the seams and restores them, so tests never leak a
// fake into the package's production wiring.
func withPlatform(t *testing.T, f *fakePlatform, why string) {
	t.Helper()
	oldP, oldC := platformFor, checkHost
	platformFor = func() platform { return f }
	checkHost = func(Config) string { return why }
	t.Cleanup(func() { platformFor, checkHost = oldP, oldC })
}

func baseConfig(shutdown func()) Config {
	return Config{
		Enabled: true, Name: "wuDict", Version: "1.2.3",
		URL:      "http://127.0.0.1:8080/",
		Open:     func() {},
		Rescan:   func() {},
		OpenDir:  func() {},
		Shutdown: shutdown,
	}
}

func TestMachineTransitions(t *testing.T) {
	tests := []struct {
		name string
		path []State
		want []bool
	}{
		{"happy path", []State{Preflight, Starting, Running, Stopping, Gone}, []bool{true, true, true, true, true}},
		{"preflight fails", []State{Preflight, Degraded}, []bool{true, true}},
		{"starting fails", []State{Preflight, Starting, Degraded}, []bool{true, true, true}},
		{"running degrades", []State{Preflight, Starting, Running, Degraded}, []bool{true, true, true, true}},
		{"degraded absorbs", []State{Preflight, Degraded, Preflight, Starting, Running}, []bool{true, true, false, false, false}},
		{"gone absorbs", []State{Preflight, Starting, Running, Stopping, Gone, Degraded}, []bool{true, true, true, true, true, false}},
		{"no skipping preflight", []State{Starting}, []bool{false}},
		{"no restart from stopping", []State{Preflight, Starting, Running, Stopping, Running}, []bool{true, true, true, true, false}},
		{"no going back", []State{Preflight, Starting, Running, Starting}, []bool{true, true, true, false}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := &machine{}
			for i, next := range tc.path {
				if got := m.to(next); got != tc.want[i] {
					t.Fatalf("step %d to %v: got %v, want %v (state %v)", i, next, got, tc.want[i], m.state())
				}
			}
		})
	}
}

func TestMachineRefusalLeavesStateUntouched(t *testing.T) {
	m := &machine{}
	m.to(Preflight)
	m.to(Degraded)
	if m.to(Running) {
		t.Fatal("Degraded accepted a transition")
	}
	if got := m.state(); got != Degraded {
		t.Fatalf("state after refused transition = %v, want degraded", got)
	}
}

func TestStateStrings(t *testing.T) {
	for s, want := range map[State]string{
		Off: "off", Preflight: "preflight", Starting: "starting", Running: "running",
		Stopping: "stopping", Degraded: "degraded", Gone: "gone", State(99): "invalid",
	} {
		if got := s.String(); got != want {
			t.Errorf("State(%d).String() = %q, want %q", int(s), got, want)
		}
	}
}

func TestWrapDisabledIsPassThrough(t *testing.T) {
	f := newFake()
	withPlatform(t, f, "")
	want := errors.New("serve failed")
	if got := Wrap(Config{Enabled: false}, func() error { return want }); !errors.Is(got, want) {
		t.Fatalf("Wrap returned %v, want %v", got, want)
	}
	if f.started.Load() {
		t.Fatal("platform started for a disabled tray")
	}
}

// A tray whose Quit cannot stop anything is a decoration with a lying menu
// item; Wrap must refuse it rather than draw it.
func TestWrapWithoutShutdownIsPassThrough(t *testing.T) {
	f := newFake()
	withPlatform(t, f, "")
	cfg := baseConfig(nil)
	if err := Wrap(cfg, func() error { return nil }); err != nil {
		t.Fatalf("Wrap: %v", err)
	}
	if f.started.Load() {
		t.Fatal("platform started without a Shutdown callback")
	}
}

// The invariant: the server's lifetime is never downstream of the tray.
func TestPreflightFailureStillServes(t *testing.T) {
	f := newFake()
	withPlatform(t, f, "no StatusNotifierWatcher")
	served := make(chan struct{})
	want := errors.New("listener closed")
	got := Wrap(baseConfig(func() {}), func() error { close(served); return want })
	<-served
	if !errors.Is(got, want) {
		t.Fatalf("Wrap returned %v, want the serve error %v", got, want)
	}
	if f.started.Load() {
		t.Fatal("platform started despite a failed preflight")
	}
}

func TestStartPanicDegradesWithoutCrashing(t *testing.T) {
	f := newFake()
	f.startPanic = true
	withPlatform(t, f, "")
	want := errors.New("serve done")
	if got := Wrap(baseConfig(func() {}), func() error { return want }); !errors.Is(got, want) {
		t.Fatalf("Wrap returned %v, want %v", got, want)
	}
}

func TestStartErrorDegradesWithoutCrashing(t *testing.T) {
	f := newFake()
	f.startErr = errors.New("create failed")
	withPlatform(t, f, "")
	want := errors.New("serve done")
	if got := Wrap(baseConfig(func() {}), func() error { return want }); !errors.Is(got, want) {
		t.Fatalf("Wrap returned %v, want %v", got, want)
	}
}

// Run() giving up on its own must not take the server with it: Wrap keeps
// waiting for serve and returns serve's verdict.
func TestRunReturningEarlyKeepsServing(t *testing.T) {
	f := newFake()
	f.runNow = true
	f.runErr = errors.New("loop died")
	withPlatform(t, f, "")
	release := make(chan struct{})
	want := errors.New("serve stopped later")
	done := make(chan error, 1)
	go func() {
		done <- Wrap(baseConfig(func() {}), func() error { <-release; return want })
	}()
	// Wrap must still be blocked: Run has already returned, serve has not.
	select {
	case got := <-done:
		t.Fatalf("Wrap returned %v before serve finished", got)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	if got := <-done; !errors.Is(got, want) {
		t.Fatalf("Wrap returned %v, want %v", got, want)
	}
}

func TestQuitShutsDownExactlyOnce(t *testing.T) {
	f := newFake()
	withPlatform(t, f, "")
	var calls atomic.Int32
	serveDone := make(chan struct{})
	cfg := baseConfig(func() { calls.Add(1); close(serveDone) })

	// Capture the Quit callback the moment the menu is built.
	quitItem := make(chan func(), 1)
	done := make(chan error, 1)
	go func() {
		done <- Wrap(cfg, func() error { <-serveDone; return nil })
	}()
	// Wait for the menu, then fire Quit twice - a double click on the item
	// must not shut the server down twice.
	deadline := time.After(2 * time.Second)
	for {
		if f.started.Load() {
			for _, it := range f.items {
				if strings.HasPrefix(it.Label, "Quit ") {
					quitItem <- it.Do
				}
			}
			break
		}
		select {
		case <-deadline:
			t.Fatal("platform never started")
		case <-time.After(time.Millisecond):
		}
	}
	q := <-quitItem
	q()
	q()
	if err := <-done; err != nil {
		t.Fatalf("Wrap: %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("Shutdown called %d times, want 1", got)
	}
	if !f.stopped.Load() {
		t.Fatal("platform was never stopped")
	}
}

// Ctrl-C: the server ends on its own, and the tray must follow it.
func TestServerEndingStopsTheTray(t *testing.T) {
	f := newFake()
	withPlatform(t, f, "")
	if err := Wrap(baseConfig(func() { t.Error("Shutdown called for a self-terminating server") }),
		func() error { return nil }); err != nil {
		t.Fatalf("Wrap: %v", err)
	}
	if !f.stopped.Load() {
		t.Fatal("platform was never stopped after the server ended")
	}
}

func TestMenuShape(t *testing.T) {
	labels := func(cfg Config) []string {
		var out []string
		for _, it := range cfg.items(func() {}) {
			switch {
			case it.Sep:
				out = append(out, "---")
			case it.Disabled:
				out = append(out, "["+it.Label+"]")
			default:
				out = append(out, it.Label)
			}
		}
		return out
	}
	full := baseConfig(func() {})
	want := []string{
		"[wuDict 1.2.3]", "Open wuDict", "---",
		"Rescan dictionaries", "Open dictionary folder", "---", "Quit wuDict",
	}
	if got := labels(full); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("full menu = %v, want %v", got, want)
	}

	// Optional entries drop out with their separator, so an empty middle
	// section cannot produce two adjacent rules.
	bare := Config{Enabled: true, Shutdown: func() {}}
	wantBare := []string{"[wuDict]", "---", "Quit wuDict"}
	if got := labels(bare); strings.Join(got, "|") != strings.Join(wantBare, "|") {
		t.Errorf("bare menu = %v, want %v", got, wantBare)
	}
}

func TestTooltip(t *testing.T) {
	if got := baseConfig(nil).tooltip(); got != "wuDict - serving on http://127.0.0.1:8080/" {
		t.Errorf("tooltip = %q", got)
	}
	if got := (Config{}).tooltip(); got != "wuDict" {
		t.Errorf("tooltip without a URL = %q", got)
	}
}
