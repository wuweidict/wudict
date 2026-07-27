// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"time"

	"github.com/glowinthedark/gonow-dict/internal/logx"
)

// ReportPrepared prints the one line that closes a "preparing search index…"
// status: how many entries were indexed, how long it took, and — only when
// there were any — how many redirects pointed at headwords that do not exist
// in the source. Shared by the formats that prepare themselves on first open
// (DSL, BGL) so both read identically.
func ReportPrepared(name string, rep Report, took time.Duration) {
	logx.ClearLine()
	logx.Status("%s%d entries indexed in %.1fs", logx.Dict(name), rep.Entries, took.Seconds())
	if rep.UnresolvedLinks > 0 {
		logx.V("%s%d link targets not found in the source (skipped)", logx.Dict(name), rep.UnresolvedLinks)
	}
}
