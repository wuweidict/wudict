// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build cgo

package speex

import "github.com/wuweidict/wudict/internal/speex/clib"

func init() {
	Available = true
	newDecoder = func(mode, channels int) (frameDecoder, error) {
		d, err := clib.New(mode, channels)
		if err != nil {
			return nil, err
		}
		return d, nil
	}
}
