// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package server

import "syscall"

// Windows refuses to let a background process pull a window to the front:
// SetForegroundWindow succeeds only for a process that is already foreground,
// was launched by the foreground process, or received the last input event.
// Everything else gets its taskbar button flashed instead. AllowSetForegroundWindow
// is the sanctioned way to hand that right to someone else — and it is subject
// to the same rule, so it only works when WE hold the right to give away.
//
// That makes this exactly half a fix, deliberately:
//
//   - from the tray menu (D74) the user's last input event went to our own
//     icon, so we hold the right, pass it on, and Explorer comes up focused;
//   - from the web panel the browser is foreground and we have received no
//     input at all, so the call returns FALSE and Explorer flashes as before.
//
// The alternative that works from both is AttachThreadInput + a forced
// SetForegroundWindow, which is the classic focus-stealing hack: it must poll
// for the new window, it is racy, it is the behaviour the restriction exists to
// prevent, and unlike this call it is a recognised signature in AV and EDR
// heuristics. Not worth a focused Explorer window.
//
// stdlib syscall, not golang.org/x/sys/windows.NewLazySystemDLL, on purpose:
// the point of NewLazySystemDLL is to force a System32-only search so a planted
// DLL beside the .exe cannot win. user32.dll is a KnownDLL — LoadLibraryW takes
// it from the \KnownDlls section object and never consults the search order —
// so that hardening buys nothing here, and this way x/sys stays out of go.mod
// as a direct requirement. Do not "upgrade" this without a DLL that needs it.
var (
	user32                       = syscall.NewLazyDLL("user32.dll")
	procAllowSetForegroundWindow = user32.NewProc("AllowSetForegroundWindow")
)

// ASFW_ANY: (DWORD)-1, "the next process to ask may take the foreground".
// Not the child's PID, which would be the obvious choice and would be wrong —
// explorer.exe almost always hands the request to the ALREADY RUNNING shell
// process and exits, so the window is created by a process whose id we never
// see. Granting it to whoever asks next is the only form that covers that.
const asfwAny = uintptr(0xFFFFFFFF)

// allowForeground gives up our claim on the foreground, if we have one. Best
// effort by construction: the result is documented to be FALSE whenever we do
// not hold the right, which is a normal outcome here rather than an error, and
// there is nothing a caller could do about it either way.
func allowForeground() {
	// Find() rather than Call() straight away: LazyProc.Call PANICS when the
	// export cannot be resolved, and a missing user32 export must not be able
	// to take down a running server over a file-manager click.
	if err := procAllowSetForegroundWindow.Find(); err != nil {
		return
	}
	_, _, _ = procAllowSetForegroundWindow.Call(asfwAny)
}
