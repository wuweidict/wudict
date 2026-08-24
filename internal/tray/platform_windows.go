// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package tray

import (
	"syscall"
	"unsafe"
)

// Windows decides "console or no console" in the PE header, at link time, so
// the obvious way to ship a GUI launch is a second `-H windowsgui` binary.
// **One wudict.exe, and the launch is read at runtime instead (D76).**
//
// GetConsoleProcessList reports how many processes are attached to this
// process's console. Typed into cmd, PowerShell or Windows Terminal the shell
// is attached too, so the count is >= 2. Double-clicked from Explorer, started
// from a Start-menu or Startup shortcut, or opened through a file
// association, Windows creates a console for this process ALONE and the count
// is exactly 1. No console at all (a service, a detached parent) returns 0.
//
// That is the only signal on Windows that separates the two launches, and
// unlike DISPLAY it cannot be inherited by a background process that has no
// business showing an icon.
var (
	kernel32                  = syscall.NewLazyDLL("kernel32.dll")
	user32                    = syscall.NewLazyDLL("user32.dll")
	procGetConsoleProcessList = kernel32.NewProc("GetConsoleProcessList")
	procFreeConsole           = kernel32.NewProc("FreeConsole")
	procProcessIdToSessionId  = kernel32.NewProc("ProcessIdToSessionId")
	procGetCurrentProcessId   = kernel32.NewProc("GetCurrentProcessId")
	procMessageBoxW           = user32.NewProc("MessageBoxW")
)

// preflight refuses only where there is no desktop to put an icon on.
// Shell_NotifyIconW is part of the OS on every supported Windows, so there is
// nothing to probe for - the notification area exists even when the user has
// collapsed it behind the chevron. Session 0 is the exception: it has been
// isolated from every interactive desktop since Vista, so a service or a "run
// whether the user is logged on or not" scheduled task would otherwise pump a
// message loop forever for an icon that cannot be drawn.
func preflight(Config) string {
	if sessionZero() {
		return "session 0 has no interactive desktop"
	}
	return ""
}

func sessionZero() bool {
	pid, _, _ := procGetCurrentProcessId.Call()
	var session uint32
	ok, _, _ := procProcessIdToSessionId.Call(pid, uintptr(unsafe.Pointer(&session)))
	return ok != 0 && session == 0
}

// GUILaunched reports whether this process owns its console - see above.
//
// It must be read BEFORE DetachConsole, and its answer carried in Config.GUI:
// once the console is gone the same call reports false.
func GUILaunched() bool {
	// The buffer only has to separate 1 from more. When it is too small the
	// call returns the required element count, which is the number we want
	// either way, so a console with a crowd on it is never mistaken for ours.
	var pids [8]uint32
	n, _, _ := procGetConsoleProcessList.Call(uintptr(unsafe.Pointer(&pids[0])), uintptr(len(pids)))
	return n == 1
}

// DetachConsole closes the console Windows created for this process alone, so
// a double-click does not leave a black window on the desktop for as long as
// the server runs. It is a no-op unless this process owns its console: a
// console shared with the shell that launched us is the user's, not ours.
//
// After this, writes to stderr fail silently - which is why the caller moves
// logx to a file FIRST and only detaches if that succeeded (D76).
func DetachConsole() {
	if !GUILaunched() {
		return
	}
	_, _, _ = procFreeConsole.Call()
}

// Alert shows a modal message box. It is the last channel a GUI launch has:
// the console was closed by DetachConsole, stderr goes nowhere, and the log
// file is not something anyone is watching. Modal, so callers who must keep
// running start it detached.
func Alert(title, body string) {
	// A modal box on the session 0 desktop is one nobody can see and nobody
	// can dismiss, and MessageBoxW does not return until it is - so a service
	// or a "run whether the user is logged on or not" task would hang here
	// forever instead of exiting with its error. Say nothing rather than that.
	if sessionZero() {
		return
	}
	text, err := syscall.UTF16PtrFromString(body)
	if err != nil {
		return
	}
	caption, err := syscall.UTF16PtrFromString(title)
	if err != nil {
		return
	}
	const mbIconWarning, mbSetForeground, mbTopMost = 0x30, 0x10000, 0x40000
	_, _, _ = procMessageBoxW.Call(0,
		uintptr(unsafe.Pointer(text)), uintptr(unsafe.Pointer(caption)),
		uintptr(mbIconWarning|mbSetForeground|mbTopMost))
}

// stopHint completes "To stop it, ..." for this platform.
const stopHint = "end wudict.exe in Task Manager"
