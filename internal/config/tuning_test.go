// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"runtime"
	"testing"
)

// These defaults differ per platform (D64), and the failure that matters is a
// silent one: a desktop that quietly halves its own parallelism or caps its
// heap because a platform check was inverted. On the platform the tests run on,
// the answer must be "leave the machine alone".
func TestTuningDefaults(t *testing.T) {
	android := runtime.GOOS == "android"

	if got := MaxProcs(); android {
		if got < 2 || got > 4 {
			t.Errorf("MaxProcs() = %d, want 2..4 on android", got)
		}
	} else if got != 0 {
		t.Errorf("MaxProcs() = %d, want 0 (the runtime's own default) off android", got)
	}

	if got := memoryLimitDefault(); android {
		if got < 192<<20 || got > 384<<20 {
			t.Errorf("memoryLimitDefault() = %d MB, outside the 192–384 MB range", got>>20)
		}
	} else if got != 0 {
		t.Errorf("memoryLimitDefault() = %d, want 0 (unlimited) off android", got)
	}

	if got := previewMemoryDefault(); got <= 0 {
		t.Errorf("previewMemoryDefault() = %d: an unlimited preview budget is never a default", got)
	} else if android && got > 128<<20 {
		t.Errorf("previewMemoryDefault() = %d MB: too large for a phone", got>>20)
	}

	// The per-search cap is the one default here that can change what a search
	// returns, so off Android it must be exactly nothing, and on Android it must
	// not exceed the ceiling it exists to keep the process under.
	if got := searchMemoryDefault(); android {
		if got <= 0 || got > memoryLimitDefault() {
			t.Errorf("searchMemoryDefault() = %d MB, want 0 < n <= the memory limit (%d MB)",
				got>>20, memoryLimitDefault()>>20)
		}
	} else if got != 0 {
		t.Errorf("searchMemoryDefault() = %d, want 0 (uncapped) off android - capping costs results", got)
	}

	// defaults() must actually use them - the layering only holds if the
	// platform answer is what a config file overrides, not something applied
	// later behind its back.
	d := defaults()
	if d.PreviewMemory != previewMemoryDefault() || d.MemoryLimit != memoryLimitDefault() || d.SearchMemory != searchMemoryDefault() {
		t.Errorf("defaults() ignores the platform tuning: %d/%d/%d", d.PreviewMemory, d.MemoryLimit, d.SearchMemory)
	}
}

// memTotal reads a Linux file that does not exist elsewhere; it must answer 0
// rather than fail, since memoryLimitDefault falls back on the floor.
func TestMemTotalNeverPanics(t *testing.T) {
	if n := memTotal(); n < 0 {
		t.Errorf("memTotal() = %d", n)
	}
}
