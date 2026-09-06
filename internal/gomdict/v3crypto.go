// Copyright (C) 2026 glowinthedark
//
// extends the medict-derived MDX parser in this package.
//
// SPDX-License-Identifier: GPL-3.0-or-later

package go_mdict

import (
	"encoding/binary"

	"github.com/cespare/xxhash/v2"
)

// deriveV3EncryptedKey derives the 16-byte Salsa20/8 encryption key used by
// MDict v3 encrypted blocks from the dictionary's UUID attribute.
//
// Matches the Python reference:
//
//	mid = (len(uuid) + 1) // 2
//	encrypted_key = xxh64_digest(uuid[:mid]) + xxh64_digest(uuid[mid:])
//
// xxh64_digest returns the 8-byte big-endian digest of xxhash64 (standard
// hash-digest convention). The resulting 16-byte key is used directly as a
// Salsa20 16-byte key or as the fast_decrypt key.
func deriveV3EncryptedKey(uuid string) []byte {
	mid := (len(uuid) + 1) / 2
	key := make([]byte, 16)
	binary.BigEndian.PutUint64(key[0:8], xxhash.Sum64String(uuid[:mid]))
	binary.BigEndian.PutUint64(key[8:16], xxhash.Sum64String(uuid[mid:]))
	return key
}
