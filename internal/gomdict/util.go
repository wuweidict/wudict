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
// Modified in 2026 by glowinthedark for wudict: vendored unmodified.
//
// SPDX-License-Identifier: GPL-3.0-or-later

package go_mdict

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"unicode/utf16"

	"github.com/c0mm4nd/go-ripemd"
	"golang.org/x/text/encoding/unicode"
)

func decodeLittleEndianUtf16(raw []byte) (string, error) {
	decoder := unicode.UTF16(unicode.LittleEndian, unicode.IgnoreBOM).NewDecoder()
	bs2, err := decoder.Bytes(raw[:])
	if err != nil {
		return "", err
	}
	return string(bs2), nil
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

func min(a, b int) int {
	if a > b {
		return b
	}
	return a
}

func bigEndianBinToUTF8(bytes []byte, offset int, length int) string {
	cbytes := make([]byte, length)
	rawLen := len(bytes)
	length = min(rawLen, length)
	copy(cbytes, bytes[offset:offset+length])
	return string(cbytes)
}

func binSlice(srcByte []byte, offset int, length int, distByte []byte) int {
	srcByteLen := len(srcByte)
	if offset < 0 || offset > srcByteLen-1 {
		return -1
	}
	if offset+length > srcByteLen {
		return -2
	}
	for i := 0; i < length; i++ {
		distByte[i] = srcByte[i+offset]
	}

	return 0
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

func beBinToU16(bin []byte) uint16 {
	var n uint16 = 0
	for i := 0; i < 1; i++ {
		n = n | uint16(bin[i])
		n = n << 8
	}
	n = n | uint16(bin[1])
	return n
}

func beBinToU8(bin []byte) uint8 {
	return uint8(bin[0] & 255)
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
// error, so a short read left the tail of data zero-filled and the parser then
// decompressed article bytes the file never contained.
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

func zlibDecompress(data []byte, from, len int64) ([]byte, error) {
	b := bytes.NewReader(data[from : from+len])
	z, err := zlib.NewReader(b)
	if err != nil {
		return nil, err
	}
	defer z.Close()
	p, err := ioutil.ReadAll(z)
	if err != nil {
		return nil, err
	}
	return p, nil
}

/**
 *
 * decrypt the data, this is a helper function to invoke the fast_decrypt
 * note: don't forget free comp_block !!
 *
 * @param comp_block compressed block buffer
 * @param comp_block_len compressed block buffer size
 * @return the decrypted compressed block
 */
func mdxDecrypt(compBlock []byte, compBlockLen int64) []byte {
	keyBuffer := make([]byte, 8)
	copy(keyBuffer, compBlock[4:8])
	keyBuffer[4] = 0x95
	keyBuffer[5] = 0x36
	key := ripemd128bytes(keyBuffer)
	fastDecrypt(compBlock[8:], key, compBlockLen-8, 16)

	return compBlock
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
