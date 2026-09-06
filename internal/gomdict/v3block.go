// Copyright (C) 2026 glowinthedark
//
// extends the medict-derived MDX parser in this package.
//
// SPDX-License-Identifier: GPL-3.0-or-later

package go_mdict

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"io/ioutil"

	lzo "github.com/anchore/go-lzo"
)

// decodeBlockV3 decodes a single v3 key/record block.
//
// v3 block layout (after the [4-byte type][8-byte size] directory header):
//
//	[4 bytes LE]  info word:
//	                bits [0:4]   = compression method (0=none, 1=lzo, 2=zlib)
//	                bits [4:8]   = encryption method (0=none, 1=fast, 2=salsa20/8)
//	                bits [8:16]  = encryption size (bytes encrypted from the front)
//	[4 bytes BE]  adler32 checksum (over decrypted data for v3)
//	[N bytes]     block payload
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

// lzoDecompress1X decompresses raw LZO1X data (no MDict \xf0 prefix) into a
// buffer of exactly the size the block header claims. LZO carries no length of
// its own, so that claimed size is both the allocation and the check: a stream
// that stops short or runs long is a corrupt block, not a short read.
func lzoDecompress1X(data []byte, decompressedSize int) ([]byte, error) {
	if decompressedSize < 0 || decompressedSize > maxLZOBlock {
		return nil, fmt.Errorf("lzo: implausible decompressed size %d", decompressedSize)
	}
	out := make([]byte, decompressedSize)
	n, err := lzo.Decompress(data, out)
	if err != nil {
		return nil, fmt.Errorf("lzo: %w", err)
	}
	if n != decompressedSize {
		return nil, fmt.Errorf("lzo: produced %d bytes, block claims %d", n, decompressedSize)
	}
	return out, nil
}

// maxLZOBlock caps what a header may ask us to allocate. MDX record and key
// blocks are tens of KB; this is three orders of magnitude of headroom, and it
// is the only thing between a corrupt size field and a 4 GB allocation.
const maxLZOBlock = 256 << 20

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
