// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build darwin

package tray

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// bundleMarker is the fixed layout every macOS application bundle has. Asking
// where the binary lives is decisive in a way no session heuristic is: it is
// the app's own identity, not a guess about the desktop around it.
const bundleMarker = ".app/Contents/MacOS/"

func preflight(cfg Config) string {
	// A menu-bar item on the machine you sshed FROM is not what anyone asked
	// for, and the window server may not even be reachable.
	if os.Getenv("SSH_CONNECTION") != "" || os.Getenv("SSH_TTY") != "" {
		return "remote session"
	}
	if !cfg.Explicit && !GUILaunched() {
		return "not launched from an .app bundle"
	}
	return ""
}

// GUILaunched reports whether this binary is running from inside an .app
// bundle, which is the only way a non-terminal user starts it on macOS.
func GUILaunched() bool {
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return strings.Contains(filepath.ToSlash(exe), bundleMarker)
}

// DetachConsole is a Windows concern: only there is "console or not" decided
// in the executable header rather than by how the process was started.
func DetachConsole() {}

// Alert shows a modal dialog. It is the last channel a GUI launch has:
// LSUIElement means there is no Dock icon either, so nothing else on screen
// would say anything at all. The dialog belongs to osascript, not to us, which
// is why it appears from a process macOS treats as an accessory.
func Alert(title, body string) {
	script := fmt.Sprintf("display alert %s message %s as warning",
		appleQuote(title), appleQuote(body))
	_ = exec.Command("osascript", "-e", script).Run()
}

// stopHint completes "To stop it, ..." for this platform.
const stopHint = `quit "wudict" from Activity Monitor`

// appleQuote renders s as an AppleScript string literal. Three characters can
// end or corrupt one - the escape, the quote, and a raw newline, which
// AppleScript does not accept inside a literal at all.
func appleQuote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(s) + `"`
}
