// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Package logx is the app-wide message channel.
//
// House style for everything a user can read on the terminal:
//
//   - **Name the dictionary.** A message about one dictionary starts with its
//     name in quotes — `"Espasa Calpe": …`. Bare lines like
//     `ingest: 6931 unresolved link targets` are useless when 55 dictionaries
//     are being prepared at once; use Dict() to build the prefix.
//   - **Levels.** V() is verbose-only detail (timings, per-entry problems,
//     background work). Warn() is a real degradation the user should know
//     about but that does not stop anything. Status() is progress on a slow
//     foreground operation. Errors are returned, never printed, by library
//     packages — the CLI and the server decide what reaches the terminal.
//   - **Lowercase, no trailing period**, and prefer naming the thing over
//     naming the function that failed.
//   - **Destination.** Everything here goes to stderr, because stdout carries
//     results. SetOutput moves the whole channel to a file for the one case
//     that has no stderr worth writing to — a GUI launch (D74).
package logx

import (
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"sync"
)

// Enabled turns V output on. Set from the CLI flag or environment.
var Enabled = os.Getenv("WUDICT_VERBOSE") != "" || os.Getenv("VERBOSE") != ""

// The destination is a variable rather than a constant os.Stderr because a
// GUI launch has no console to write to: a macOS .app inherits a stderr
// pointing at nothing useful, and a double-clicked wudict.exe is about to
// close the console Windows made for it (D76). SetOutput is the only way to
// move it, it is called at most once per
// process, and a terminal session never calls it — a console is never taken
// away from a user who has one (D74, machine C).
var (
	mu         sync.RWMutex
	out        io.Writer = os.Stderr
	redirected bool
	logger     = log.New(os.Stderr, "", log.Ltime|log.Lmicroseconds)
)

// SetOutput sends every logx line to w. A nil w restores stderr.
//
// Redirecting also makes Interactive report false, which silences Progress and
// ClearLine: their carriage returns are for a human watching a wait, and in a
// log file they are corruption.
func SetOutput(w io.Writer) {
	mu.Lock()
	defer mu.Unlock()
	if w == nil {
		out, redirected = os.Stderr, false
	} else {
		out, redirected = w, true
	}
	logger.SetOutput(out) // log.Logger guards its own writer
}

func dest() io.Writer {
	mu.RLock()
	defer mu.RUnlock()
	return out
}

// Output returns the current destination, for the few places that format their
// own multi-line output (the startup banner) rather than going through Status.
// They must not capture it: SetOutput may move it.
func Output() io.Writer { return dest() }

// V logs one verbose line when enabled.
func V(format string, args ...any) {
	if Enabled {
		logger.Printf(format, args...)
	}
}

// Warn reports a degradation the user should see (a media file that cannot be
// read, a dictionary that failed to open) without stopping anything.
func Warn(format string, args ...any) {
	fmt.Fprintf(dest(), "warning: "+format+"\n", args...)
}

// Status reports progress on a slow foreground operation (preparing a search
// index on first open). One line, no timestamp — it is for humans watching a
// wait, not for logs.
func Status(format string, args ...any) {
	fmt.Fprintf(dest(), format+"\n", args...)
}

// Interactive reports whether stderr is a terminal. In-place progress
// counters ("\r1234 entries") are for a human watching a wait; written to a
// pipe or a log file they are just noise, and their carriage returns collide
// with result lines on stdout.
func Interactive() bool {
	mu.RLock()
	red := redirected
	mu.RUnlock()
	if red {
		return false
	}
	fi, err := os.Stderr.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// Progress writes an in-place counter, or nothing when stderr is not a
// terminal. Follow it with ClearLine before printing anything else.
func Progress(format string, args ...any) {
	if Interactive() {
		fmt.Fprintf(dest(), "\r"+format, args...)
	}
}

// ClearLine erases an in-place progress counter ("\r1234 entries") so the
// line that follows starts clean. Blanks rather than an ANSI escape, to keep
// working on consoles that do not interpret them.
func ClearLine() {
	if Interactive() {
		fmt.Fprint(dest(), "\r"+strings.Repeat(" ", 48)+"\r")
	}
}

// Dict formats the standard "which dictionary is this about" prefix.
func Dict(name string) string { return fmt.Sprintf("%q: ", name) }
