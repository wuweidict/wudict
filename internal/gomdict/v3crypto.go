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

	"github.com/cespare/xxhash/v2"
)

// deriveV3EncryptedKey derives the 16-byte Salsa20/8 encryption key used by
// MDict v3 encrypted blocks from the dictionary's UUID attribute.
//
// Matches the Python reference:
//   mid = (len(uuid) + 1) // 2
//   encrypted_key = xxh64_digest(uuid[:mid]) + xxh64_digest(uuid[mid:])
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
