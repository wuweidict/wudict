// Copyright (C) 2026 glowinthedark
//
// extends the medict-derived MDX parser in this package.
//
// SPDX-License-Identifier: GPL-3.0-or-later

package go_mdict

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"sync"

	lzo "github.com/anchore/go-lzo"
	"github.com/klauspost/compress/zlib"
	"github.com/pierrec/lz4/v4"

	"github.com/wuweidict/wudict/internal/limits"
)

// decodeBlock decodes one compressed unit of any version: a v1/v2 key-block
// info, key or record block, or any v3 block. MDict has one codec for all of
// them (mdx::UncompressBlock; docs/mdict/FORMAT.md §5):
//
//	[4 bytes LE]  info word:
//	                bits 0-3   compression: 0 none, 1 LZO1X, 2 zlib, 5 LZ4 block
//	                bits 4-7   encryption: 0 none, 1 fast, 2 Salsa20/8
//	                bits 8-15  number of leading payload bytes encrypted
//	[4 bytes BE]  adler32
//	[N bytes]     payload
//
// The encrypted head of the payload is decrypted IN PLACE: every caller hands
// over a buffer it read for this call alone, and a copy of each compressed
// block would be pure overhead. A stored block's result aliases block.
//
// decompressedSize is the size the block's index declares. LZO and LZ4 carry
// no length of their own, so for them it is the allocation and the check; zlib
// and stored blocks are returned at their own length and the caller compares.
//
// The adler32 is not checked. MDict checks it only behind a debug switch
// (g_CRC_Check, off by default), a zlib stream carries its own checksum,
// which the inflater verifies, and computing it here would cost a pass over
// every block for a result that could not refuse the block anyway.
func (mdict *MdictBase) decodeBlock(block []byte, decompressedSize int) ([]byte, error) {
	if len(block) < 8 {
		return nil, fmt.Errorf("block too short: %d bytes", len(block))
	}

	info := binary.LittleEndian.Uint32(block[0:4])
	compression := info & 0xF
	encryption := (info >> 4) & 0xF
	encryptedLen := int((info >> 8) & 0xFF)
	data := block[8:]

	if encryption != 0 {
		if encryptedLen > len(data) {
			return nil, fmt.Errorf("block encrypts %d bytes of %d", encryptedLen, len(data))
		}
		key := mdict.meta.encryptedKey
		if key == nil {
			key = ripemd128bytes(block[4:8])
		}
		head := data[:encryptedLen]
		switch encryption {
		case 1:
			fastDecrypt(head, key, int64(encryptedLen), int64(len(key)))
		case 2:
			if len(key) != 16 {
				return nil, fmt.Errorf("Salsa20/8 needs a 16-byte key, have %d", len(key))
			}
			salsa208XORKeyStream(head, head, key)
		default:
			return nil, fmt.Errorf("unsupported encryption method %d", encryption)
		}
	}

	switch compression {
	case 0:
		return data, nil
	case 1:
		return lzoDecompress1X(data, decompressedSize)
	case 2:
		return inflate(data, decompressedSize)
	case 5:
		return lz4Decompress(data, decompressedSize)
	}
	return nil, fmt.Errorf("unsupported compression method %d", compression)
}

// inflater is a pooled zlib reader: a decompressor's state is tens of
// kilobytes, and a dictionary scan decodes one block per few dozen entries.
type inflater struct {
	src bytes.Reader
	z   io.Reader
}

var inflaters sync.Pool

// zlibRatio bounds what a zlib stream can expand to (deflate's maximum is
// 1032:1), so a declared size beyond it is not trusted with an allocation.
const zlibRatio = 1032

// inflate decompresses a zlib stream (klauspost's inflater: the same format,
// markedly faster than the standard library's). With a plausible declared
// size the output is allocated once at that size; the stream is still read to
// its end, which is where its checksum is verified, and a stream longer or
// shorter than declared is returned at its true length for the caller to
// judge. Every path is bounded by the MAX_MDICT_BLOCK cap: the sizes come out
// of the file, and an unbounded inflate turns a corrupt block into a zlib bomb.
func inflate(data []byte, decompressedSize int) ([]byte, error) {
	inf, _ := inflaters.Get().(*inflater)
	if inf == nil {
		inf = &inflater{}
	}
	inf.src.Reset(data)
	if inf.z == nil {
		z, err := zlib.NewReader(&inf.src)
		if err != nil {
			return nil, fmt.Errorf("zlib: %w", err)
		}
		inf.z = z
	} else if err := inf.z.(zlib.Resetter).Reset(&inf.src, nil); err != nil {
		return nil, fmt.Errorf("zlib: %w", err) // a reader whose Reset failed is not reused
	}
	defer func() {
		inf.src.Reset(nil) // the pool must not keep the block alive
		inflaters.Put(inf)
	}()

	max := maxLZOBlock()
	if decompressedSize > 0 && int64(decompressedSize) <= max && decompressedSize <= zlibRatio*len(data)+64 {
		out := make([]byte, decompressedSize)
		n, err := io.ReadFull(inf.z, out)
		switch err {
		case io.ErrUnexpectedEOF:
			return out[:n], nil // ended early and cleanly: shorter than declared
		case nil:
		default:
			return nil, fmt.Errorf("zlib: %w", err)
		}
		// Exactly full: one more read reaches the end of the stream and its
		// checksum, or finds the stream longer than declared.
		var one [1]byte
		k, err := inf.z.Read(one[:])
		if k == 0 {
			if err == io.EOF {
				return out, nil
			}
			if err != nil {
				return nil, fmt.Errorf("zlib: %w", err)
			}
		}
		rest, err := io.ReadAll(io.LimitReader(inf.z, max-int64(n)-int64(k)+1))
		if err != nil {
			return nil, fmt.Errorf("zlib: %w", err)
		}
		out = append(append(out, one[:k]...), rest...)
		if int64(len(out)) > max {
			return nil, limits.Exceeded("the unpacked block", "MAX_MDICT_BLOCK", max)
		}
		return out, nil
	}

	out, err := io.ReadAll(io.LimitReader(inf.z, max+1))
	if err != nil {
		return nil, fmt.Errorf("zlib: %w", err)
	}
	if int64(len(out)) > max {
		return nil, limits.Exceeded("the unpacked block", "MAX_MDICT_BLOCK", max)
	}
	return out, nil
}

// lzoDecompress1X decompresses raw LZO1X data (no MDict \xf0 prefix) into a
// buffer of exactly the size the block header claims. LZO carries no length of
// its own, so that claimed size is both the allocation and the check: a stream
// that stops short or runs long is a corrupt block, not a short read.
func lzoDecompress1X(data []byte, decompressedSize int) ([]byte, error) {
	out, err := sizedBuffer("lzo", decompressedSize)
	if err != nil {
		return nil, err
	}
	n, err := lzo.Decompress(data, out)
	if err != nil {
		return nil, err // the library's errors already read "lzo: …"
	}
	if n != decompressedSize {
		return nil, fmt.Errorf("lzo: produced %d bytes, block claims %d", n, decompressedSize)
	}
	return out, nil
}

// lz4Decompress decodes an LZ4 block (no frame), which MDict reads as
// compression method 5 with LZ4_decompress_safe: the output must be exactly
// the declared size.
func lz4Decompress(data []byte, decompressedSize int) ([]byte, error) {
	out, err := sizedBuffer("lz4", decompressedSize)
	if err != nil {
		return nil, err
	}
	n, err := lz4.UncompressBlock(data, out)
	if err != nil {
		return nil, err // the library's errors already read "lz4: …"
	}
	if n != decompressedSize {
		return nil, fmt.Errorf("lz4: produced %d bytes, block claims %d", n, decompressedSize)
	}
	return out, nil
}

// sizedBuffer allocates an output buffer of a size read out of the file,
// refusing one no real block has.
func sizedBuffer(codec string, size int) ([]byte, error) {
	if size < 0 {
		return nil, fmt.Errorf("%s: implausible decompressed size %d", codec, size)
	}
	if int64(size) > maxLZOBlock() {
		return nil, fmt.Errorf("%s: %w", codec, limits.Exceeded(fmt.Sprintf("an unpacked size of %d bytes", size), "MAX_MDICT_BLOCK", maxLZOBlock()))
	}
	return make([]byte, size), nil
}

// maxLZOBlock caps what a header may ask us to allocate. MDX record and key
// blocks are tens of KB; this is three orders of magnitude of headroom, and it
// is the only thing between a corrupt size field and a 4 GB allocation.
func maxLZOBlock() int64 { return limits.Get().MDictBlock }
