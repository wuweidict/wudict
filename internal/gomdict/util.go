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
//
// Derived from medict (https://github.com/terasum/medict),
// internal/libs/go-mdict.
//
// Modified in 2026 by glowinthedark for wudict: bounded reads, DecodeText,
// and the decryptor narrowed to the key-block info.
//
// SPDX-License-Identifier: GPL-3.0-or-later

package go_mdict

import (
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/c0mm4nd/go-ripemd"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"
	"golang.org/x/text/transform"
)

// DecodeText converts text in one of the Encoding* constants to UTF-8. Bytes a
// decoder rejects are kept as they are rather than failing the whole string: a
// headword or an article with one bad byte is still worth showing.
func DecodeText(raw []byte, enc int) string {
	switch enc {
	case EncodingUtf16:
		if s, err := decodeLittleEndianUtf16(raw); err == nil {
			return s
		}
	case EncodingGb18030, ENCODING_GBK, ENCODING_GB2312:
		if out, _, err := transform.Bytes(simplifiedchinese.GB18030.NewDecoder(), raw); err == nil {
			return string(out)
		}
	case EncodingBig5:
		if out, _, err := transform.Bytes(traditionalchinese.Big5.NewDecoder(), raw); err == nil {
			return string(out)
		}
	case EncodingLatin1:
		// ISO 8859-1 is the first 256 code points, byte for byte.
		var b strings.Builder
		b.Grow(len(raw) + len(raw)/4)
		for _, c := range raw {
			b.WriteRune(rune(c))
		}
		return b.String()
	}
	return string(raw)
}

// decodeLittleEndianUtf16 decodes UTF-16LE to UTF-8 without a transformer:
// every .mdd key and every UTF-16 record goes through here, and building an
// x/text decoder per call cost more than the decoding. Same output as that
// decoder (unicode.UTF16(LittleEndian, IgnoreBOM)): a byte-order mark is kept
// as U+FEFF, an unpaired surrogate and a trailing odd byte become U+FFFD.
// It never fails; the error is kept for the callers' shape.
func decodeLittleEndianUtf16(raw []byte) (string, error) {
	var b strings.Builder
	b.Grow(len(raw)/2 + len(raw)/8 + 4)
	for i := 0; i+1 < len(raw); i += 2 {
		u := rune(raw[i]) | rune(raw[i+1])<<8
		switch {
		case u < 0x80:
			b.WriteByte(byte(u))
			continue
		case u >= 0xD800 && u < 0xE000:
			// Any surrogate takes a following low surrogate with it, as
			// x/text does: a valid pair decodes, any other pairing is one
			// U+FFFD for both units.
			if i+3 < len(raw) {
				if lo := rune(raw[i+2]) | rune(raw[i+3])<<8; lo >= 0xDC00 && lo < 0xE000 {
					u = utf16.DecodeRune(u, lo)
					i += 2
					break
				}
			}
			u = utf8.RuneError
		}
		b.WriteRune(u)
	}
	if len(raw)%2 == 1 {
		b.WriteRune(utf8.RuneError)
	}
	return b.String(), nil
}

func littleEndianBinUTF16ToUTF8(bytes []byte, offset int, length int) string {
	cbytes := make([]byte, length)
	copy(cbytes, bytes[offset:])
	wcbytes := make([]uint16, len(cbytes)/2)
	for i := 0; i < len(wcbytes); i++ {
		wcbytes[i] = uint16(cbytes[i*2]) | uint16(cbytes[i*2+1])<<8
	}
	u8 := utf16.Decode(wcbytes)
	return string(u8)
}

func beBinToU64(bin []byte) uint64 {
	var n uint64 = 0
	for i := 0; i < 7; i++ {
		n = n | uint64(bin[i])
		n = n << 8
	}
	n = n | uint64(bin[7])
	return n
}

func beBinToU32(bin []byte) uint32 {
	var n uint32 = 0
	for i := 0; i < 3; i++ {
		n = n | uint32(bin[i])
		n = n << 8
	}
	n = n | uint32(bin[3])
	return n
}

// readFileFromPos reads exactly n bytes at start.
//
// Every caller passes an n that came out of the file being parsed (a block's
// declared compressed size, an index region's length), so n is attacker-chosen
// for a hostile or merely corrupt dictionary: unbounded, it is an allocation
// of up to the declared value before the read that would have failed.
//
// io.ReadFull, not one Read, for a second reason that is a correctness bug
// rather than a robustness one: a single Read returns what it got with a nil
// error, so a short read would leave the tail of data zero-filled and the
// parser would decompress article bytes the file never contained.
func readFileFromPos(file *os.File, start, n int64) ([]byte, error) {
	if start < 0 || n < 0 {
		return nil, fmt.Errorf("invalid read (offset %d, %d bytes)", start, n)
	}
	st, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if size := st.Size(); start > size || n > size-start {
		return nil, fmt.Errorf("read out of bounds (offset %d, %d bytes, file %d)", start, n, size)
	}
	if _, err := file.Seek(start, io.SeekStart); err != nil {
		return nil, err
	}
	data := make([]byte, n)
	if _, err := io.ReadFull(file, data); err != nil {
		return nil, err
	}
	return data, nil
}

// decryptKeyInfo undoes the key-block info encryption (Encrypted bit 1) in
// place: the payload after the 8-byte block header is fast-encrypted with
// ripemd128(the header's adler32 bytes ‖ 95 36 00 00) (docs/mdict/FORMAT.md
// §3.2). The block header itself is left in the clear.
func decryptKeyInfo(block []byte) {
	if len(block) <= 8 {
		return
	}
	keyBuffer := make([]byte, 8)
	copy(keyBuffer, block[4:8])
	keyBuffer[4] = 0x95
	keyBuffer[5] = 0x36
	key := ripemd128bytes(keyBuffer)
	fastDecrypt(block[8:], key, int64(len(block)-8), int64(len(key)))
}

func fastDecrypt(data []byte, k []byte, dataLen int64, keyLen int64) {
	key := k

	b := data
	previous := byte(0x36)
	for i := int64(0); i < dataLen; i++ {
		t := byte((b[i]>>4 | b[i]<<4) & 0xff)
		t = t ^ previous ^ byte(i&0xff) ^ key[i%keyLen]
		previous = b[i]
		b[i] = t
	}
}

func ripemd128bytes(data []byte) []byte {
	md := ripemd.New128()
	md.Write(data)
	out := md.Sum(nil)
	md.Reset()
	return out
}
