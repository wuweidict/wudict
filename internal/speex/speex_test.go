// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build cgo

package speex

import (
	"encoding/binary"
	"os"
	"testing"
)

// TestDecodeRealSpx decodes a real Ogg-Speex file (WUDICT_TEST_SPX) and reports
// the WAV rate/sample-count so it can be compared against the speexdec CLI.
func TestDecodeRealSpx(t *testing.T) {
	p := os.Getenv("WUDICT_TEST_SPX")
	if p == "" {
		t.Skip("WUDICT_TEST_SPX not set")
	}
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	wav, err := DecodeToWAV(f)
	if err != nil {
		t.Fatal(err)
	}
	if out := os.Getenv("WUDICT_TEST_SPX_OUT"); out != "" {
		_ = os.WriteFile(out, wav, 0o644)
	}
	if string(wav[0:4]) != "RIFF" || string(wav[8:12]) != "WAVE" {
		t.Fatal("not a WAV")
	}
	le := binary.LittleEndian
	rate := le.Uint32(wav[24:28])
	channels := le.Uint16(wav[22:24])
	bits := le.Uint16(wav[34:36])
	samples := le.Uint32(wav[40:44]) / 2
	t.Logf("wav: rate=%d channels=%d bits=%d samples=%d bytes=%d", rate, channels, bits, samples, len(wav))
}
