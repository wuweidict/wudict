// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build linux && !android

package tray

import (
	"os"
	"time"

	"github.com/godbus/dbus/v5"
)

// The freedesktop StatusNotifierItem host. If nobody owns this name there is
// no tray on this desktop - GNOME without the AppIndicator extension, a bare
// compositor, a session with no panel yet.
const watcherName = "org.kde.StatusNotifierWatcher"

// probeTimeout bounds the D-Bus round trip. A session bus that does not answer
// is indistinguishable from one that is not there, and either way the answer we
// need is "do not start the tray".
const probeTimeout = 3 * time.Second

// preflight is the whole reason this package exists. The library reports no
// error on Linux - registering with the watcher fails with a slog.Warn and
// Create() returns nil anyway - and Run() is a bare channel receive. Start a
// tray on a desktop that has no watcher and the process pumps a message loop
// forever for an icon nobody can see. Asking D-Bus directly is the difference
// between knowing and guessing.
func preflight(Config) string {
	if os.Getenv("DBUS_SESSION_BUS_ADDRESS") == "" && os.Getenv("XDG_RUNTIME_DIR") == "" {
		return "no session D-Bus"
	}
	res := make(chan string, 1) // buffered: a timed-out probe must not leak a blocked goroutine
	go func() { res <- probeWatcher() }()
	select {
	case why := <-res:
		return why
	case <-time.After(probeTimeout):
		return "session D-Bus did not respond"
	}
}

func probeWatcher() string {
	// SessionBus returns a shared connection the systray library will reuse,
	// so it is deliberately not closed here.
	conn, err := dbus.SessionBus()
	if err != nil {
		return "no session D-Bus: " + err.Error()
	}
	var owned bool
	call := conn.BusObject().Call("org.freedesktop.DBus.NameHasOwner", 0, watcherName)
	if err := call.Store(&owned); err != nil {
		return "session D-Bus query failed: " + err.Error()
	}
	if !owned {
		return "no StatusNotifierWatcher (GNOME needs the AppIndicator extension)"
	}
	return ""
}

// GUILaunched is always false on Linux. There is no bundle and no distinct GUI
// binary to key off, and every session heuristic is wrong for the case that
// matters: a systemd USER unit inherits DISPLAY and WAYLAND_DISPLAY, so
// sniffing them would put a tray icon on a background service. Linux desktop
// users ask for the tray explicitly with --tray or TRAY=1.
func GUILaunched() bool { return false }

// DetachConsole is a Windows concern: only there is "console or not" decided
// in the executable header rather than by how the process was started.
func DetachConsole() {}

// Alert has nothing to do here: GUILaunched is always false on Linux, so a
// degraded tray always has a terminal, a pipe or a journal behind it.
func Alert(string, string) {}

// stopHint completes "To stop it, ..." for this platform.
const stopHint = "press Ctrl-C in its terminal"
