// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package server

import (
	"os/exec"
	"syscall"
)

// createNoWindow is CREATE_NO_WINDOW: the child gets no console of its own.
const createNoWindow = 0x08000000

// hideWindow keeps a console helper from flashing a window on screen.
//
// It matters because of D76: a double-clicked wudict.exe gives up its console,
// and a process with no console that starts a console program gets a NEW
// console window for it — so every .spx decode through an external speexdec
// would pop a black box in front of whatever the user is reading.
func hideWindow(c *exec.Cmd) {
	if c.SysProcAttr == nil {
		c.SysProcAttr = &syscall.SysProcAttr{}
	}
	c.SysProcAttr.CreationFlags |= createNoWindow
}
