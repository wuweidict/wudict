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
package logx

import (
	"fmt"
	"log"
	"os"
	"strings"
)

// Enabled turns V output on. Set from the CLI flag or environment.
var Enabled = os.Getenv("GONOW_VERBOSE") != "" || os.Getenv("VERBOSE") != ""

var logger = log.New(os.Stderr, "", log.Ltime|log.Lmicroseconds)

// V logs one verbose line when enabled.
func V(format string, args ...any) {
	if Enabled {
		logger.Printf(format, args...)
	}
}

// Warn reports a degradation the user should see (a media file that cannot be
// read, a dictionary that failed to open) without stopping anything.
func Warn(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "warning: "+format+"\n", args...)
}

// Status reports progress on a slow foreground operation (preparing a search
// index on first open). One line, no timestamp — it is for humans watching a
// wait, not for logs.
func Status(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
}

// Interactive reports whether stderr is a terminal. In-place progress
// counters ("\r1234 entries") are for a human watching a wait; written to a
// pipe or a log file they are just noise, and their carriage returns collide
// with result lines on stdout.
func Interactive() bool {
	fi, err := os.Stderr.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// Progress writes an in-place counter, or nothing when stderr is not a
// terminal. Follow it with ClearLine before printing anything else.
func Progress(format string, args ...any) {
	if Interactive() {
		fmt.Fprintf(os.Stderr, "\r"+format, args...)
	}
}

// ClearLine erases an in-place progress counter ("\r1234 entries") so the
// line that follows starts clean. Blanks rather than an ANSI escape, to keep
// working on consoles that do not interpret them.
func ClearLine() {
	if Interactive() {
		fmt.Fprint(os.Stderr, "\r"+strings.Repeat(" ", 48)+"\r")
	}
}

// Dict formats the standard "which dictionary is this about" prefix.
func Dict(name string) string { return fmt.Sprintf("%q: ", name) }
