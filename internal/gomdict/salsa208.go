//
// Copyright (C) 2023 Quan Chen <chenquan_act@163.com>
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with this program.  If not, see <http://www.gnu.org/licenses/>.

package go_mdict

import (
	"encoding/binary"
	"math/bits"
)

// salsa208 implements the Salsa20/8 stream cipher with a 16-byte key and a
// zero 8-byte nonce, matching MDict v3's encryption (the Python reference
// uses pureSalsa20.Salsa20(key, IV=b"\x00"*8, rounds=8) with a 16-byte key
// derived from xxhash of the dictionary UUID).
//
// golang.org/x/crypto/salsa20 only supports 32-byte keys and 20 rounds, so
// we vendor a minimal 8-round / 16-byte-key implementation here.
//
// The 16-byte-key variant uses the constant "expand 16-byte k" and places
// the same 16 key bytes into both halves of the 4x4 state matrix.

// salsa208XORKeyStream encrypts/decrypts src into dst (in-place allowed when
// dst == src) using Salsa20/8 with the given 16-byte key and a zero nonce.
// The counter starts at zero. dst must be at least as long as src.
func salsa208XORKeyStream(dst, src []byte, key []byte) {
	if len(key) != 16 {
		panic("salsa208: key must be 16 bytes")
	}
	if len(dst) < len(src) {
		panic("salsa208: dst shorter than src")
	}

	// Build the initial 16-word (32-bit each) state.
	// Layout (little-endian):
	//   [0]     = "expa"      [5]  = "d 16"     [10] = "-byt"     [15] = "e k"
	//   [1..4]  = key[0:16]
	//   [6,7]   = nonce (zero)
	//   [8,9]   = counter (starts at zero, increments per 64-byte block)
	//   [11..14]= key[0:16] (same as [1..4] for 16-byte key)
	var state [16]uint32
	state[0] = binary.LittleEndian.Uint32([]byte("expa"))
	state[5] = binary.LittleEndian.Uint32([]byte("nd 1"))
	state[10] = binary.LittleEndian.Uint32([]byte("6-by"))
	state[15] = binary.LittleEndian.Uint32([]byte("te k"))
	for i := 0; i < 4; i++ {
		state[1+i] = binary.LittleEndian.Uint32(key[i*4 : (i+1)*4])
		state[11+i] = state[1+i]
	}
	// nonce (ctx[6], ctx[7]) and counter (ctx[8], ctx[9]) are already zero.

	var block [64]byte
	for offset := 0; offset < len(src); offset += 64 {
		salsa208Core(&block, &state)
		// XOR keystream block with plaintext.
		end := offset + 64
		if end > len(src) {
			end = len(src)
		}
		for i := offset; i < end; i++ {
			dst[i] = src[i] ^ block[i-offset]
		}
		// Increment the 64-bit little-endian counter (ctx[8], ctx[9]).
		state[8]++
		if state[8] == 0 {
			state[9]++
		}
	}
}

// salsa208Core runs 8 rounds of the Salsa20 core function on the given state
// and writes the 64-byte keystream block.
func salsa208Core(out *[64]byte, state *[16]uint32) {
	var x [16]uint32
	copy(x[:], state[:])

	// 8 rounds = 4 iterations of the double-round (each does column then row).
	for i := 0; i < 4; i++ {
		// Column round.
		x[4] ^= bits.RotateLeft32(x[0]+x[12], 7)
		x[8] ^= bits.RotateLeft32(x[4]+x[0], 9)
		x[12] ^= bits.RotateLeft32(x[8]+x[4], 13)
		x[0] ^= bits.RotateLeft32(x[12]+x[8], 18)
		x[9] ^= bits.RotateLeft32(x[5]+x[1], 7)
		x[13] ^= bits.RotateLeft32(x[9]+x[5], 9)
		x[1] ^= bits.RotateLeft32(x[13]+x[9], 13)
		x[5] ^= bits.RotateLeft32(x[1]+x[13], 18)
		x[14] ^= bits.RotateLeft32(x[10]+x[6], 7)
		x[2] ^= bits.RotateLeft32(x[14]+x[10], 9)
		x[6] ^= bits.RotateLeft32(x[2]+x[14], 13)
		x[10] ^= bits.RotateLeft32(x[6]+x[2], 18)
		x[3] ^= bits.RotateLeft32(x[15]+x[11], 7)
		x[7] ^= bits.RotateLeft32(x[3]+x[15], 9)
		x[11] ^= bits.RotateLeft32(x[7]+x[3], 13)
		x[15] ^= bits.RotateLeft32(x[11]+x[7], 18)

		// Row round.
		x[1] ^= bits.RotateLeft32(x[0]+x[3], 7)
		x[2] ^= bits.RotateLeft32(x[1]+x[0], 9)
		x[3] ^= bits.RotateLeft32(x[2]+x[1], 13)
		x[0] ^= bits.RotateLeft32(x[3]+x[2], 18)
		x[6] ^= bits.RotateLeft32(x[5]+x[4], 7)
		x[7] ^= bits.RotateLeft32(x[6]+x[5], 9)
		x[4] ^= bits.RotateLeft32(x[7]+x[6], 13)
		x[5] ^= bits.RotateLeft32(x[4]+x[7], 18)
		x[11] ^= bits.RotateLeft32(x[10]+x[9], 7)
		x[8] ^= bits.RotateLeft32(x[11]+x[10], 9)
		x[9] ^= bits.RotateLeft32(x[8]+x[11], 13)
		x[10] ^= bits.RotateLeft32(x[9]+x[8], 18)
		x[12] ^= bits.RotateLeft32(x[15]+x[14], 7)
		x[13] ^= bits.RotateLeft32(x[12]+x[15], 9)
		x[14] ^= bits.RotateLeft32(x[13]+x[12], 13)
		x[15] ^= bits.RotateLeft32(x[14]+x[13], 18)
	}

	// Add the original state and serialize as little-endian.
	for i := 0; i < 16; i++ {
		binary.LittleEndian.PutUint32(out[i*4:(i+1)*4], x[i]+state[i])
	}
}
