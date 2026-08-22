// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Package tray puts an optional system-tray icon around a running server.
//
// It exists for one reason: WuWeiDict ships an always-on local HTTP server to
// people who do not use a terminal, and without a tray they can neither see
// that it is running nor stop it. Everything else on the menu is convenience;
// those two are the feature.
//
// # The governing invariant
//
// The server's lifetime is never downstream of the tray. Wrap starts serve()
// BEFORE it touches any platform API, and serve() runs to completion no matter
// what the tray does. The tray may *request* a shutdown; it can never cause one
// by failing.
//
// That is not caution for its own sake. github.com/gogpu/systray is a thin,
// error-suppressing wrapper: New() discards the platform's Create error, Show()
// discards its error, and Run() — the only call that returns one — blocks. On
// Linux, Run() is literally a receive on a quit channel, and registering with
// the StatusNotifierWatcher is non-fatal even when it fails. So on GNOME
// without the AppIndicator extension every call "succeeds" and the process
// hangs forever on an icon nobody can see. Failure cannot be detected after the
// fact, which is why preflight (below) is ours rather than the library's.
package tray

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wuweidict/wudict/internal/logx"
)

// errNoTray is what a platform returns when it has no tray to give. It is a
// Start/Run failure, not a preflight verdict: preflight is the path that is
// supposed to catch this, and reaching here means it did not.
var errNoTray = errors.New("no system tray available")

// How long Stopping waits for the server to finish. http.Server.Shutdown
// already runs on a 5s grace in cmdServe; past that the server is wedged and
// waiting longer helps nobody.
const shutdownGrace = 8 * time.Second

// How long to wait for Run() to return after the icon is removed, before
// exiting anyway. The library documents a Quit() that does not exist, so
// Run() returning is a hope, not a contract.
const exitGrace = 3 * time.Second

// State is machine T. Transitions are forward-only; Degraded and Gone absorb.
type State int

const (
	Off       State = iota // not enabled — terminal, zero cost
	Preflight              // asking the OS whether a tray is possible
	Starting               // building the icon and menu
	Running                // Run() is pumping the platform message loop
	Stopping               // Quit chosen; draining the server
	Degraded               // any failure or panic — never retried
	Gone                   // stopped cleanly
)

func (s State) String() string {
	switch s {
	case Off:
		return "off"
	case Preflight:
		return "preflight"
	case Starting:
		return "starting"
	case Running:
		return "running"
	case Stopping:
		return "stopping"
	case Degraded:
		return "degraded"
	case Gone:
		return "gone"
	}
	return "invalid"
}

// allowed is the whole transition table. Degraded and Gone have no outgoing
// edges, which is what makes a second failure arriving after the first unable
// to resurrect the machine — and what makes "never retried" structural rather
// than a rule somebody has to remember.
var allowed = map[State][]State{
	Off:       {Preflight},
	Preflight: {Starting, Degraded},
	Starting:  {Running, Degraded},
	Running:   {Stopping, Degraded},
	Stopping:  {Gone},
	Degraded:  nil,
	Gone:      nil,
}

type machine struct {
	mu sync.Mutex
	s  State
}

func (m *machine) state() State {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.s
}

// to advances the machine and reports whether the transition was legal. An
// illegal transition is refused, never applied — callers use the false return
// to decide whether they are the one who gets to report the failure.
func (m *machine) to(next State) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, ok := range allowed[m.s] {
		if ok == next {
			m.s = next
			return true
		}
	}
	return false
}

// Item is one menu entry. A Sep item is a separator and ignores every other
// field; a Disabled item is drawn greyed and is how the version header is
// rendered.
type Item struct {
	Label    string
	Disabled bool
	Sep      bool
	Do       func()
}

// Config is everything the tray knows about the server. The callbacks are
// supplied by the caller, so this package depends on neither the registry nor
// the HTTP layer — and Quit runs in-process, which keeps a remotely reachable
// kill switch out of the mux entirely.
type Config struct {
	Enabled  bool   // false → Wrap is a pass-through
	Explicit bool   // --tray was passed, as opposed to inferred from a GUI launch
	GUI      bool   // launched from a desktop, so there is no console to fall back on
	Name     string // product name, user-visible (default "WuWeiDict")
	Version  string
	URL      string

	Open     func() // open the UI in a browser; omitted from the menu when nil
	Rescan   func() // rescan the dictionary folders; omitted when nil
	OpenDir  func() // reveal the dictionary folder; omitted when nil
	Shutdown func() // ask the server to stop. Required; must be safe to call twice
}

func (cfg Config) name() string {
	if cfg.Name == "" {
		return "wuDict"
	}
	return cfg.Name
}

func (cfg Config) header() string {
	if cfg.Version == "" {
		return cfg.name()
	}
	return cfg.name() + " " + cfg.Version
}

func (cfg Config) tooltip() string {
	if cfg.URL == "" {
		return cfg.name()
	}
	return cfg.name() + " — serving on " + cfg.URL
}

// items builds the menu. Optional entries are dropped rather than greyed when
// their callback is absent, and the separator around them is dropped with them
// so an empty middle section cannot produce two adjacent rules.
func (cfg Config) items(quit func()) []Item {
	items := []Item{{Label: cfg.header(), Disabled: true}}
	if cfg.Open != nil {
		items = append(items, Item{Label: "Open " + cfg.name(), Do: cfg.Open})
	}
	var mid []Item
	if cfg.Rescan != nil {
		mid = append(mid, Item{Label: "Rescan dictionaries", Do: cfg.Rescan})
	}
	if cfg.OpenDir != nil {
		mid = append(mid, Item{Label: "Open dictionary folder", Do: cfg.OpenDir})
	}
	if len(mid) > 0 {
		items = append(items, Item{Sep: true})
		items = append(items, mid...)
	}
	items = append(items, Item{Sep: true}, Item{Label: "Quit " + cfg.name(), Do: quit})
	return items
}

// platform is the OS-facing half, behind an interface so machine T is testable
// without a desktop.
type platform interface {
	Start(cfg Config, items []Item) error
	Run() error // blocks, pumping the platform message loop
	Stop()      // destroys the icon; must unblock Run
}

// Seams for tests. Production values come from the platform_*.go files.
var (
	platformFor = newPlatform
	checkHost   = preflight
	warnUser    = notify
)

// Wrap runs serve, optionally with a tray icon around it, and returns serve's
// error. It must be called from the main goroutine: Run() pumps NSApplication
// on macOS, and the library's own init() pins the main OS thread for exactly
// this reason.
func Wrap(cfg Config, serve func() error) error {
	// A tray with no way to stop the server is not a tray, it is a decoration
	// with a misleading Quit item. Refuse it rather than ship one.
	if !cfg.Enabled || cfg.Shutdown == nil {
		return serve()
	}

	// The server first, and unconditionally. Nothing below can prevent it.
	serveErr := make(chan error, 1)
	go func() { serveErr <- serve() }()

	m := &machine{}
	m.to(Preflight)
	if why := checkHost(cfg); why != "" {
		m.to(Degraded)
		degrade(cfg, why)
		return <-serveErr
	}

	m.to(Starting)
	quit := make(chan struct{})
	var quitOnce sync.Once
	p := platformFor()
	if err := startSafely(p, cfg, cfg.items(func() {
		quitOnce.Do(func() { close(quit) })
	})); err != nil {
		m.to(Degraded)
		degrade(cfg, err.Error())
		return <-serveErr
	}
	m.to(Running)
	logx.V("tray: icon active, serving on %s", cfg.URL)

	// intentional distinguishes "Run returned because we asked it to" from
	// "Run returned on its own", which is the difference between a clean exit
	// and a degradation. A channel cannot do this job: Stop() must happen
	// before the supervisor finishes, so any done-channel it closes afterwards
	// is still open at the moment Run() unblocks.
	var intentional atomic.Bool
	done := make(chan struct{})
	runReturned := make(chan struct{})
	var result error

	go func() {
		defer close(done)
		select {
		case e := <-serveErr:
			// The server ended on its own — Ctrl-C, SIGTERM, a listener
			// error. The tray follows it, never the reverse.
			result = e
		case <-quit:
			if m.to(Stopping) {
				cfg.Shutdown()
			}
			select {
			case e := <-serveErr:
				result = e
			case <-time.After(shutdownGrace):
				logx.Warn("shutdown timed out after %s — exiting anyway", shutdownGrace)
			}
			m.to(Gone)
		}
		intentional.Store(true)
		p.Stop()
		// Run() may never return: the Quit() the library documents does not
		// exist, and Remove() unblocking Run() is an implementation detail of
		// this version of it. Bound the wait rather than trust it — but arm the
		// exit only against a Run() that genuinely hangs, so a well-behaved
		// library reaches the ordinary return path and its error survives.
		go func() {
			select {
			case <-runReturned:
			case <-time.After(exitGrace):
				logx.Warn("tray message loop did not stop — exiting")
				if result != nil {
					os.Exit(1)
				}
				os.Exit(0)
			}
		}()
	}()

	err := runSafely(p)
	close(runReturned)
	if !intentional.Load() {
		// Run() gave up while the server is still serving. Say so once and
		// keep serving; Degraded is absorbing, so a later failure stays quiet.
		if m.to(Degraded) {
			degrade(cfg, runReason(err))
		}
	}
	<-done
	return result
}

func runReason(err error) string {
	if err != nil {
		return "message loop failed: " + err.Error()
	}
	return "message loop ended unexpectedly"
}

// startSafely and runSafely contain the library rather than trust it: a v0.2
// FFI layer panicking is a live possibility, and a panic on the main goroutine
// would take a working server down with it.
func startSafely(p platform, cfg Config, items []Item) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("tray library panicked: %v", r)
		}
	}()
	return p.Start(cfg, items)
}

func runSafely(p platform) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("tray library panicked: %v", r)
		}
	}()
	return p.Run()
}

// degrade reports the one line a user gets when the tray cannot be had, and
// tells them how to stop the server without it. In a GUI launch this lands in
// the log file rather than on a console (machine C, Headless) — the browser tab
// that opened at startup is then the only liveness indicator left, which is why
// preflight refuses early rather than letting Starting fail silently.
func degrade(cfg Config, why string) {
	logx.Warn("tray unavailable (%s) — serving anyway at %s; press Ctrl-C or run 'kill %d' to stop",
		why, cfg.URL, os.Getpid())
	// A GUI launch has nowhere else to look: no console (Windows closed its
	// own before this could be called), no Dock icon (LSUIElement), and a log
	// file nobody is watching. The server is up and the user cannot tell — so
	// on that path only, say it in the one channel the OS always has. Detached,
	// because every implementation is modal.
	if cfg.GUI {
		go warnUser(cfg, why)
	}
}
