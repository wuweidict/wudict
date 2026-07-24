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
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"io/ioutil"

	"github.com/rasky/go-lzo"
)

// decodeBlockV3 decodes a single v3 key/record block.
//
// v3 block layout (after the [4-byte type][8-byte size] directory header):
//   [4 bytes LE]  info word:
//                   bits [0:4]   = compression method (0=none, 1=lzo, 2=zlib)
//                   bits [4:8]   = encryption method (0=none, 1=fast, 2=salsa20/8)
//                   bits [8:16]  = encryption size (bytes encrypted from the front)
//   [4 bytes BE]  adler32 checksum (over decrypted data for v3)
//   [N bytes]     block payload
//
// The adler32 is checked over the decrypted-but-not-decompressed data for v3
// (the opposite of v1/v2, which checks over decompressed data).
func (mdict *MdictBase) decodeBlockV3(block []byte, decompressedSize int) ([]byte, error) {
	if len(block) < 8 {
		return nil, fmt.Errorf("v3 block too short: %d bytes", len(block))
	}

	info := binary.LittleEndian.Uint32(block[0:4])
	compressionMethod := info & 0xF
	encryptionMethod := (info >> 4) & 0xF
	encryptionSize := int((info >> 8) & 0xFF)

	adler32 := binary.BigEndian.Uint32(block[4:8])
	data := block[8:]

	// Derive the encryption key. For v3, if the dictionary has a UUID-derived
	// encrypted key, use it; otherwise fall back to ripemd128 of the adler
	// checksum bytes (matching the Python reference).
	encryptedKey := mdict.meta.encryptedKey
	if encryptedKey == nil {
		encryptedKey = ripemd128bytes(block[4:8])
	}

	// Decrypt.
	var decrypted []byte
	switch encryptionMethod {
	case 0:
		decrypted = data
	case 1:
		if encryptionSize > len(data) {
			return nil, fmt.Errorf("v3 block: encryption size %d > data %d", encryptionSize, len(data))
		}
		decrypted = make([]byte, len(data))
		copy(decrypted, data)
		fastDecrypt(decrypted[:encryptionSize], encryptedKey, int64(encryptionSize), int64(len(encryptedKey)))
		// data[encryptionSize:] is copied as-is already.
	case 2:
		if encryptionSize > len(data) {
			return nil, fmt.Errorf("v3 block: encryption size %d > data %d", encryptionSize, len(data))
		}
		decrypted = make([]byte, len(data))
		copy(decrypted, data)
		salsa208XORKeyStream(decrypted[:encryptionSize], data[:encryptionSize], encryptedKey)
		// data[encryptionSize:] is copied as-is already.
	default:
		return nil, fmt.Errorf("v3 block: unsupported encryption method %d", encryptionMethod)
	}

	// v3: verify adler32 over the decrypted (pre-decompression) data.
	if got := adler32Of(decrypted); got != adler32 {
		return nil, fmt.Errorf("v3 block: adler32 mismatch (decrypted): got 0x%08x want 0x%08x", got, adler32)
	}

	// Decompress.
	var decompressed []byte
	switch compressionMethod {
	case 0:
		decompressed = decrypted
	case 1:
		// LZO1X: go-lzo expects raw LZO1X data with the decompressed size
		// passed as an outLen hint.
		out, err := lzoDecompress1X(decrypted, decompressedSize)
		if err != nil {
			return nil, fmt.Errorf("v3 block: lzo decompress: %w", err)
		}
		decompressed = out
	case 2:
		z, err := zlib.NewReader(bytes.NewReader(decrypted))
		if err != nil {
			return nil, fmt.Errorf("v3 block: zlib reader: %w", err)
		}
		defer z.Close()
		decompressed, err = ioutil.ReadAll(z)
		if err != nil {
			return nil, fmt.Errorf("v3 block: zlib read: %w", err)
		}
	default:
		return nil, fmt.Errorf("v3 block: unsupported compression method %d", compressionMethod)
	}

	return decompressed, nil
}

// lzoDecompress1X wraps go-lzo's Decompress1X with raw LZO1X data (no MDict
// \xf0 prefix) and the decompressed size as the outLen hint.
func lzoDecompress1X(data []byte, decompressedSize int) ([]byte, error) {
	return lzo.Decompress1X(bytes.NewReader(data), len(data), decompressedSize)
}

// adler32Of computes the Adler-32 checksum of data (matching zlib.adler32).
func adler32Of(data []byte) uint32 {
	const mod = 65521
	var a, b uint32 = 1, 0
	for _, c := range data {
		a = (a + uint32(c)) % mod
		b = (b + a) % mod
	}
	return (b << 16) | a
}
