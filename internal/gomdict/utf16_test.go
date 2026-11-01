// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package go_mdict

import (
	"math/rand"
	"testing"

	"golang.org/x/text/encoding/unicode"
)

// TestDecodeUTF16MatchesXText holds the hand-written decoder to the x/text
// decoder it replaced, on real-shaped and on hostile input alike.
func TestDecodeUTF16MatchesXText(t *testing.T) {
	ref := func(b []byte) string {
		out, err := unicode.UTF16(unicode.LittleEndian, unicode.IgnoreBOM).NewDecoder().Bytes(b)
		if err != nil {
			t.Fatalf("x/text: %v", err)
		}
		return string(out)
	}
	inputs := [][]byte{
		nil,
		utf16le(`\audio\uk\abandon__gb_1.mp3`),
		utf16le("año 日本 𝄞 😀"),
		{0xFF, 0xFE, 'a', 0},              // byte-order mark, kept
		{0x3D, 0xD8},                      // lone high surrogate at the end
		{0x3D, 0xD8, 'a', 0},              // high surrogate, then not a low one
		{0x00, 0xDC, 'a', 0},              // lone low surrogate
		{0x3D, 0xD8, 0x3D, 0xD8, 0, 0xDE}, // high, high, low
		{'a', 0, 'b'},                     // odd length
	}
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 2000; i++ {
		b := make([]byte, rng.Intn(24))
		for j := range b {
			b[j] = byte(rng.Intn(256))
			if j%2 == 1 && rng.Intn(3) == 0 {
				b[j] = 0xD8 + byte(rng.Intn(8)) // plenty of surrogates
			}
		}
		inputs = append(inputs, b)
	}
	for _, in := range inputs {
		got, _ := decodeLittleEndianUtf16(in)
		if want := ref(in); got != want {
			t.Fatalf("decode(% x) = %q, want %q", in, got, want)
		}
	}
}

func BenchmarkDecodeUTF16Key(b *testing.B) {
	key := utf16le(`\audio\uk\abandon__gb_1.mp3`)
	b.Run("hand", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			decodeLittleEndianUtf16(key)
		}
	})
	b.Run("x/text", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			unicode.UTF16(unicode.LittleEndian, unicode.IgnoreBOM).NewDecoder().Bytes(key)
		}
	})
}
