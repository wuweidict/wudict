// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package go_mdict

import (
	"encoding/binary"
	"testing"
	"unicode/utf16"
)

// num is the 8-byte big-endian record offset the fixtures give entry i.
func num(i int) []byte {
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], uint64(i*100))
	return n[:]
}

// keyBlockUTF8 lays out a v2/v3-style key block: an 8-byte big-endian record
// offset, the key bytes, one NUL.
func keyBlockUTF8(keys ...string) []byte {
	var out []byte
	for i, k := range keys {
		out = append(out, num(i)...)
		out = append(out, k...)
		out = append(out, 0)
	}
	return out
}

// keyBlockUTF16 is the same with little-endian UTF-16 keys and a two-byte NUL,
// which is how v1/v2 MDD key lists are written.
func keyBlockUTF16(keys ...string) []byte {
	var out []byte
	for i, k := range keys {
		out = append(out, num(i)...)
		for _, u := range utf16.Encode([]rune(k)) {
			out = append(out, byte(u), byte(u>>8))
		}
		out = append(out, 0, 0)
	}
	return out
}

// The terminator width follows the encoding, not the file type. Getting this
// from fileType==MDD was a real defect: v3 MDD key lists are UTF-8, and scanning
// them two bytes at a time steps over every single-byte NUL, then reads one past
// the end of the block (or slices backwards from a stale end index).
func TestSplitKeyBlock(t *testing.T) {
	mdd := []string{"/caldera.jpg", "/audio/es000023-11.mp3", "/.DS_Store"}
	tests := []struct {
		name     string
		fileType MdictType
		encoding int
		block    []byte
		want     []string
	}{
		{"v3 mdd is utf-8", MdictTypeMdd, EncodingUtf8, keyBlockUTF8(mdd...), mdd},
		{"v1/v2 mdd is utf-16", MdictTypeMdd, EncodingUtf16, keyBlockUTF16(mdd...), mdd},
		{"mdx utf-8", MdictTypeMdx, EncodingUtf8, keyBlockUTF8("caldero", "año", "日本"), []string{"caldero", "año", "日本"}},
		{"mdx utf-16", MdictTypeMdx, EncodingUtf16, keyBlockUTF16("caldero", "año", "日本"), []string{"caldero", "año", "日本"}},
		// A key that is all NULs in one encoding is data in the other: a UTF-16
		// "a" is 61 00, so a UTF-8 scan must not treat its high byte as a
		// terminator, and vice versa. Covered by the two mdx rows above; what is
		// left is malformed input, which must yield keys, not a panic.
		{"unterminated last key", MdictTypeMdx, EncodingUtf8,
			append(keyBlockUTF8("one"), append(num(1), "two"...)...),
			[]string{"one", "two"}},
		{"unterminated last utf-16 key", MdictTypeMdd, EncodingUtf16,
			append(keyBlockUTF16("/a.jpg"), append(num(1), 'b', 0)...),
			[]string{"/a.jpg", "b"}},
		{"trailing bytes shorter than a number", MdictTypeMdx, EncodingUtf8,
			append(keyBlockUTF8("one"), 0, 1, 2), []string{"one"}},
		{"empty block", MdictTypeMdx, EncodingUtf8, nil, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			md := &MdictBase{
				fileType: tt.fileType,
				meta:     &mdictMeta{encoding: tt.encoding, numberWidth: 8, version: 2.0},
			}
			got := md.splitKeyBlock(tt.block)
			if len(got) != len(tt.want) {
				t.Fatalf("got %d keys, want %d: %v", len(got), len(tt.want), keywords(got))
			}
			for i, w := range tt.want {
				if got[i].KeyWord != w {
					t.Errorf("key %d = %q, want %q", i, got[i].KeyWord, w)
				}
				if got[i].RecordStartOffset != int64(i*100) {
					t.Errorf("key %d offset = %d, want %d", i, got[i].RecordStartOffset, i*100)
				}
			}
		})
	}
}

func keywords(entries []*MDictKeywordEntry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.KeyWord
	}
	return out
}
