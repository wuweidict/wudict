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

	// defaults() must actually use them — the layering only holds if the
	// platform answer is what a config file overrides, not something applied
	// later behind its back.
	d := defaults()
	if d.PreviewMemory != previewMemoryDefault() || d.MemoryLimit != memoryLimitDefault() {
		t.Errorf("defaults() ignores the platform tuning: %d/%d", d.PreviewMemory, d.MemoryLimit)
	}
}

// memTotal reads a Linux file that does not exist elsewhere; it must answer 0
// rather than fail, since memoryLimitDefault falls back on the floor.
func TestMemTotalNeverPanics(t *testing.T) {
	if n := memTotal(); n < 0 {
		t.Errorf("memTotal() = %d", n)
	}
}
