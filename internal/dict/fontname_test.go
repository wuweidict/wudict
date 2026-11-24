// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package dict

import (
	"bytes"
	"errors"
	"testing"
)

func TestCheckFont(t *testing.T) {
	pad := func(s string) []byte { return append([]byte(s), make([]byte, 40)...) }
	ok := map[string][]byte{
		"truetype":       pad("\x00\x01\x00\x00"),
		"apple truetype": pad("true"),
		"opentype":       pad("OTTO"),
		"woff":           pad("wOFF\x00\x01\x00\x00"),
		"woff2":          pad("wOF2OTTO"),
	}
	for name, b := range ok {
		if err := CheckFont(bytes.NewReader(b)); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	bad := map[string][]byte{
		"empty":            nil,
		"shorter than 8":   []byte("OTTO"),
		"collection":       pad("ttcf"),
		"woff2 collection": pad("wOF2ttcf"),
		"html":             []byte("<html><body>not a font</body></html>"),
		"zip":              pad("PK\x03\x04"),
	}
	for name, b := range bad {
		if err := CheckFont(bytes.NewReader(b)); !errors.Is(err, ErrNotFont) {
			t.Errorf("%s: err = %v, want ErrNotFont", name, err)
		}
	}
}
