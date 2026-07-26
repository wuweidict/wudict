// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !cgo

package speex

// Without cgo the in-process libspeex decoder is not compiled in; callers fall
// back to the external `speexdec` binary. Available stays false and newDecoder
// is never invoked (DecodeToWAV returns ErrNoBackend first).
func init() {
	Available = false
}
