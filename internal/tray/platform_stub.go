// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build android || (!darwin && !linux && !windows)

package tray

// Android and anything else the library does not implement. Wrap never reaches
// these — preflight refuses first — but they exist so the package compiles
// everywhere the app does, with no tray dependency linked in.

func newPlatform() platform { return stubPlatform{} }

func preflight(Config) string { return "no system tray on this platform" }

// GUILaunched reports whether this process was started from a desktop shell
// rather than a terminal. Never true here.
func GUILaunched() bool { return false }

type stubPlatform struct{}

func (stubPlatform) Start(Config, []Item) error { return errNoTray }
func (stubPlatform) Run() error                 { return errNoTray }
func (stubPlatform) Stop()                      {}

// DetachConsole and notify exist so every platform file answers the same four
// questions. On a platform with no tray at all there is nothing to answer.
func DetachConsole()        {}
func notify(Config, string) {}
