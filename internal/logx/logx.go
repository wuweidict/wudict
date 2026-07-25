// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Package logx is the app-wide verbose logger: silent by default,
// enabled with --verbose / VERBOSE=1 / GONOW_VERBOSE=1.
package logx

import (
	"log"
	"os"
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
