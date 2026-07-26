// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build cgo

package speex

import "github.com/glowinthedark/gonow-dict/internal/speex/clib"

func init() {
	Available = true
	newDecoder = func(mode int) (frameDecoder, error) {
		d, err := clib.New(mode)
		if err != nil {
			return nil, err
		}
		return d, nil
	}
}
