// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package go_mdict

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"hash/adler32"
	"strings"
	"testing"

	"github.com/pierrec/lz4/v4"

	"github.com/wuweidict/wudict/internal/limits"
)

func rawBlock(info uint32, sum uint32, data []byte) []byte {
	out := make([]byte, 8, 8+len(data))
	binary.LittleEndian.PutUint32(out, info)
	binary.BigEndian.PutUint32(out[4:], sum)
	return append(out, data...)
}

// TestDecodeBlock covers the one codec every version shares
// (docs/mdict/FORMAT.md §5): each compression method MDict reads, both
// ciphers, both key sources, and the checksum rule.
func TestDecodeBlock(t *testing.T) {
	plain := []byte(strings.Repeat("the quick brown fox jumps over the lazy dog. ", 20))

	var zb bytes.Buffer
	zw := zlib.NewWriter(&zb)
	zw.Write(plain)
	zw.Close()
	zdata := zb.Bytes()

	lz := make([]byte, lz4.CompressBlockBound(len(plain)))
	n, err := lz4.CompressBlock(plain, lz, nil)
	if err != nil || n == 0 {
		t.Fatalf("lz4 compress: %d %v", n, err)
	}
	lz = lz[:n]

	// A literal-only LZO1X stream: a run of 5 literals, then the end marker.
	lzoHello := append(append([]byte{17 + 5}, "hello"...), 0x11, 0, 0)

	uuidKey := deriveV3EncryptedKey("117363fc-ce66-40dd-bcc8-24f52554630f")
	fast := func(data, key []byte, n int) []byte {
		out := append([]byte(nil), data...)
		fastEncrypt(out[:n], key)
		return out
	}
	salsa := func(data, key []byte, n int) []byte {
		out := append([]byte(nil), data...)
		salsa208XORKeyStream(out[:n], data[:n], key)
		return out
	}
	// With no dictionary key, a block's key is ripemd128 of its stored adler32.
	adlerKey := func(sum uint32) []byte {
		var b [4]byte
		binary.BigEndian.PutUint32(b[:], sum)
		return ripemd128bytes(b[:])
	}
	zsum := adler32.Checksum(zdata)

	cases := []struct {
		name    string
		key     []byte // the dictionary key; nil for none
		block   []byte
		size    int
		want    []byte
		wantErr string
	}{
		{name: "stored", block: rawBlock(0, adler32.Checksum(plain), plain), size: len(plain), want: plain},
		{name: "zlib", block: rawBlock(2, adler32.Checksum(plain), zdata), size: len(plain), want: plain},
		{name: "lzo", block: rawBlock(1, adler32.Checksum([]byte("hello")), lzoHello), size: 5, want: []byte("hello")},
		{name: "lz4", block: rawBlock(5, adler32.Checksum(plain), lz), size: len(plain), want: plain},
		{name: "zlib, fast-encrypted with the UUID key", key: uuidKey,
			block: rawBlock(2|1<<4|16<<8, zsum, fast(zdata, uuidKey, 16)), size: len(plain), want: plain},
		{name: "zlib, Salsa20/8 with the UUID key", key: uuidKey,
			block: rawBlock(2|2<<4|16<<8, zsum, salsa(zdata, uuidKey, 16)), size: len(plain), want: plain},
		{name: "zlib, fast-encrypted with the adler32 key",
			block: rawBlock(2|1<<4|32<<8, zsum, fast(zdata, adlerKey(zsum), 32)), size: len(plain), want: plain},
		{name: "the block checksum is not checked", block: rawBlock(2, 12345, zdata), size: len(plain), want: plain},
		{name: "zlib longer than declared", block: rawBlock(2, 0, zdata), size: len(plain) - 10, want: plain},
		{name: "zlib shorter than declared", block: rawBlock(2, 0, zdata), size: len(plain) + 10, want: plain},
		{name: "zlib with an implausible declared size", block: rawBlock(2, 0, zdata), size: 1 << 30, want: plain},
		{name: "zlib with no declared size", block: rawBlock(2, 0, zdata), size: 0, want: plain},
		{name: "lzo stream shorter than declared", block: rawBlock(1, 0, lzoHello), size: 6, wantErr: "lzo"},
		{name: "unknown compression method", block: rawBlock(3, 0, plain), wantErr: "compression method 3"},
		{name: "unknown encryption method", block: rawBlock(0|3<<4|4<<8, 0, plain), wantErr: "encryption method 3"},
		{name: "encrypts more than the payload", block: rawBlock(0|1<<4|200<<8, 0, []byte("short")), wantErr: "encrypts 200"},
		{name: "shorter than its header", block: []byte{1, 2, 3}, wantErr: "too short"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := &MdictBase{filePath: "fixture.mdx", meta: &mdictMeta{encryptedKey: tc.key}}
			in := append([]byte(nil), tc.block...)
			got, err := m.decodeBlock(in, tc.size)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want one mentioning %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			if !bytes.Equal(got, tc.want) {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// TestInflateBounded: the decompressed size a caller checks against is read
// out of the file, so the inflate itself has to be the bounded step.
func TestInflateBounded(t *testing.T) {
	limits.Set(limits.Limits{MDictBlock: 1 << 20})
	t.Cleanup(func() { limits.Set(limits.Default) })

	var bomb bytes.Buffer
	zw := zlib.NewWriter(&bomb)
	zw.Write(make([]byte, 4<<20)) // 4 MiB of zeros against a 1 MiB cap
	zw.Close()
	for _, declared := range []int{0, 1 << 20, 4 << 20} {
		if _, err := inflate(bomb.Bytes(), declared); err == nil || !strings.Contains(err.Error(), "exceeds") {
			t.Fatalf("declared %d: expected the inflate-size refusal, got %v", declared, err)
		}
	}
	// A corrupt stream is an error, also once the declared size is reached.
	var good bytes.Buffer
	zw = zlib.NewWriter(&good)
	zw.Write([]byte("hello, world"))
	zw.Close()
	bad := append([]byte(nil), good.Bytes()...)
	bad[len(bad)-1] ^= 0xFF // the stream's own adler32
	if _, err := inflate(bad, 12); err == nil {
		t.Fatal("a zlib stream with a wrong checksum was accepted")
	}
	if _, err := lzoDecompress1X(nil, 2<<20); err == nil {
		t.Fatal("an LZO block may not declare more than the cap")
	}
	if _, err := lz4Decompress(nil, 2<<20); err == nil {
		t.Fatal("an LZ4 block may not declare more than the cap")
	}
}
