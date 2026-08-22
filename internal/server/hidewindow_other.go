// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !windows

package server

import "os/exec"

// hideWindow is a Windows concern: no other platform invents a window for a
// child process that never asked for one.
func hideWindow(*exec.Cmd) {}
