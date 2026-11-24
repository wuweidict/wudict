// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package dict

import (
	"errors"
	"fmt"
	"io"
)

// ErrNotFont is wrapped by every CheckFont error.
var ErrNotFont = errors.New("not a usable font")

// CheckFont reports whether r holds a font a browser can be given with
// @font-face (CompanionFont, D204): TrueType, OpenType, WOFF or WOFF2, told
// apart by the first four bytes, which every one of them is required to start
// with. A collection (TrueType's "ttcf", or a WOFF2 whose flavour is one) is
// refused: @font-face has no way to say which of its faces it means.
//
// Eight bytes, never more: the question is "will the browser take this file",
// and the browser itself validates the rest.
func CheckFont(r io.ReaderAt) error {
	var h [8]byte
	if _, err := r.ReadAt(h[:], 0); err != nil {
		return fmt.Errorf("%w: too short or unreadable", ErrNotFont)
	}
	switch string(h[:4]) {
	case "\x00\x01\x00\x00", "true", "OTTO", "wOFF":
		return nil
	case "wOF2":
		if string(h[4:]) != "ttcf" {
			return nil
		}
	case "ttcf":
	default:
		return fmt.Errorf("%w: no font signature", ErrNotFont)
	}
	return fmt.Errorf("%w: a font collection", ErrNotFont)
}
