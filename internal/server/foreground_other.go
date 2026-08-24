// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !windows

package server

// Everywhere else the question does not arise: macOS `open` and xdg-open ask
// the window server to activate the target, and no equivalent of Windows'
// foreground lock stands in the way. See foreground_windows.go for why it does.
func allowForeground() {}
