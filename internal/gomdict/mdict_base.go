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
// Modified in 2026 by glowinthedark: MDX v3 block scanning, indexed record
// access, a logging shim replacing the go-logging, and the header, key and
// record tables read as MDict reads them (docs/mdict/).
//
// SPDX-License-Identifier: GPL-3.0-or-later

package go_mdict

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/adler32"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/wuweidict/wudict/internal/logx"
)

// readDictHeader reads the header and the attributes the parser acts on, read
// the way MDict reads them (mdx::ZDB_T::parseSpec; docs/mdict/FORMAT.md §2).
func (mdict *MdictBase) readDictHeader() error {
	dictHeader, err := readMDictFileHeader(mdict.filePath)
	if err != nil {
		return err
	}
	mdict.header = dictHeader

	headerInfo, err := parseXMLHeader(dictHeader.headerInfo)
	if err != nil {
		return err
	}

	meta := &mdictMeta{}

	// The format version is RequiredEngineVersion, which MDict requires;
	// GeneratedByEngineVersion is the writer's and the engine never reads it.
	// Every known file gives both the same value, so the fallback only spares
	// a header that names just one.
	versionStr := headerInfo.RequiredEngineVersion
	if strings.TrimSpace(versionStr) == "" {
		versionStr = headerInfo.GeneratedByEngineVersion
	}
	version, ok := leadingFloat(versionStr)
	if !ok {
		return fmt.Errorf("the header names no format version (RequiredEngineVersion %q)", versionStr)
	}
	if int(version*100+1e-6) > 300 {
		return fmt.Errorf("format version %s is newer than the 3.0 this reader knows", strings.TrimSpace(versionStr))
	}
	meta.version = float32(version)

	if meta.version >= 2.0 {
		meta.numberWidth = 8
		meta.numberFormat = NumfmtBe8bytesq
	} else {
		meta.numberWidth = 4
		meta.numberFormat = NumfmtBe4bytesi
	}

	// atoi, as MDict reads it: "No", "Yes" and "" are all 0.
	meta.encryptType = leadingInt(headerInfo.Encrypted)

	mdd := mdict.fileType == MdictTypeMdd
	enc, named := encodingOf(headerInfo.Encoding)
	switch {
	case named:
		meta.encoding = enc
	case meta.version >= 3.0:
		meta.encoding = EncodingUtf8 // the section trailers refine it (scanV3Blocks)
	case mdd:
		meta.encoding = EncodingUtf16
	default:
		// MDict's default for a v1/v2 .mdx whose Encoding is empty or unknown
		// is code page 936; GB18030 is its superset.
		meta.encoding = EncodingGb18030
	}
	meta.keyEncoding = meta.encoding

	contentType := headerInfo.Format
	if meta.version >= 3.0 {
		contentType = headerInfo.ContentType
	}
	meta.contentType = contentTypeOf(contentType, mdd)

	compact := headerInfo.Compact
	if strings.TrimSpace(compact) == "" {
		compact = headerInfo.Compat
	}
	meta.compact = strings.EqualFold(strings.TrimSpace(compact), "Yes")

	// 4 bytes header size + header_bytes_size + 4 bytes adler32
	meta.keyBlockMetaStartOffset = int64(4 + dictHeader.headerBytesSize + 4)

	meta.description = headerInfo.Description
	meta.title = headerInfo.Title
	meta.creationDate = headerInfo.CreationDate
	meta.generatedByEngineVersion = headerInfo.GeneratedByEngineVersion
	meta.stylesheet = headerInfo.StyleSheet
	meta.headerAttrs = headerInfo.Attrs

	// v3: stash the UUID so we can derive the encryption key later if needed.
	if meta.version >= 3.0 {
		meta.uuid = headerInfo.UUID
	}

	mdict.meta = meta
	return nil
}

// encodingOf maps the header's Encoding attribute to an Encoding* constant and
// reports whether the name was recognised. MDict recognises GBK, BIG5, UTF-16,
// UTF-8 and ISO8859-1, case-insensitively; the aliases here are other
// spellings of the same encodings. GBK and GB2312 are read as their superset
// GB18030.
func encodingOf(attr string) (int, bool) {
	switch strings.ToLower(strings.TrimSpace(attr)) {
	case "gbk", "gb2312", "gb18030":
		return EncodingGb18030, true
	case "big5":
		return EncodingBig5, true
	case "utf-16", "utf16", "utf-16le":
		return EncodingUtf16, true
	case "utf-8", "utf8":
		return EncodingUtf8, true
	case "iso8859-1", "iso-8859-1", "latin1":
		return EncodingLatin1, true
	}
	return EncodingUtf8, false
}

// contentTypeOf maps Format (v1/v2) or ContentType (v3). An absent or unknown
// value is HTML for an .mdx, where MDict would show plain text: the
// dictionaries that omit it are written for readers that render every record
// as HTML, and escaping their markup would break them. Only an explicit
// "Text" - MdxBuilder writes it for SugarDict and KDict sources - is text. An
// .mdd defaults to binary.
func contentTypeOf(attr string, mdd bool) int {
	switch strings.ToLower(strings.TrimSpace(attr)) {
	case "text":
		return ContentText
	case "html":
		return ContentHTML
	case "xml":
		return ContentXML
	case "binary":
		return ContentBinary
	}
	if mdd {
		return ContentBinary
	}
	return ContentHTML
}

// leadingInt is C's atoi: optional leading space and sign, then the digits
// that follow; anything else ends the number, and no digits is 0.
func leadingInt(s string) int {
	s = strings.TrimLeft(s, " \t\r\n")
	neg := false
	if s != "" && (s[0] == '+' || s[0] == '-') {
		neg = s[0] == '-'
		s = s[1:]
	}
	n := 0
	for i := 0; i < len(s) && s[i] >= '0' && s[i] <= '9' && n < 1<<30; i++ {
		n = n*10 + int(s[i]-'0')
	}
	if neg {
		return -n
	}
	return n
}

// leadingFloat is C's atof over a plain decimal: the longest prefix that is a
// number, so "2.0" and "2.0 beta" both read 2. ok is false when there is none.
func leadingFloat(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	end, dot := 0, false
	for end < len(s) && (s[end] >= '0' && s[end] <= '9' || s[end] == '.' && !dot) {
		dot = dot || s[end] == '.'
		end++
	}
	f, err := strconv.ParseFloat(strings.TrimSuffix(s[:end], "."), 64)
	if err != nil {
		return 0, false
	}
	return f, true
}

func readMDictFileHeader(filename string) (*mdictHeader, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var headerBytesSize uint32
	if err := binary.Read(file, binary.BigEndian, &headerBytesSize); err != nil {
		return nil, err
	}

	// Via readFileFromPos, so a hostile length cannot allocate before the file
	// is known to hold that many bytes - the header is an XML string,
	// kilobytes in every real dictionary.
	headerInfoBytes, err := readFileFromPos(file, 4, int64(headerBytesSize))
	if err != nil {
		return nil, err
	}

	// The checksum is little-endian, unlike every other number in the format.
	// MDict refuses a mismatch; it is noted here instead, because the XML
	// either parses or fails on its own.
	var adler32Checksum uint32
	if err := binary.Read(file, binary.LittleEndian, &adler32Checksum); err != nil {
		return nil, err
	}
	if adler32.Checksum(headerInfoBytes) != adler32Checksum {
		logx.V("%s: header checksum does not match its text", filepath.Base(filename))
	}

	// UTF-16LE when the text starts "<\0" (v1/v2), which is MDict's test; a
	// header ending in a two-byte NUL is UTF-16 too, which also covers one that
	// opens with a byte-order mark. Otherwise UTF-8 (v3).
	var utfHeaderInfo string
	n := len(headerInfoBytes)
	if n >= 2 && (headerInfoBytes[0] == '<' && headerInfoBytes[1] == 0 ||
		n%2 == 0 && headerInfoBytes[n-1] == 0 && headerInfoBytes[n-2] == 0) {
		utfHeaderInfo = littleEndianBinUTF16ToUTF8(headerInfoBytes, 0, n)
	} else {
		utfHeaderInfo = string(headerInfoBytes)
	}
	utfHeaderInfo = strings.TrimRight(utfHeaderInfo, "\x00")
	utfHeaderInfo = strings.Replace(utfHeaderInfo, "Library_Data", "Dictionary", 1)

	return &mdictHeader{
		headerBytesSize:          headerBytesSize,
		headerInfoBytes:          headerInfoBytes,
		headerInfo:               utfHeaderInfo,
		adler32Checksum:          adler32Checksum,
		dictionaryHeaderByteSize: int64(4 + n + 4),
	}, nil
}

// byteCursor reads big-endian fields off a decoded buffer, reporting a short
// buffer instead of panicking: every count and length in it came out of the
// file.
type byteCursor struct {
	b   []byte
	off int
	bad bool
}

func (c *byteCursor) take(n int) []byte {
	if c.bad || n < 0 || n > len(c.b)-c.off {
		c.bad = true
		return nil
	}
	out := c.b[c.off : c.off+n]
	c.off += n
	return out
}

// uint reads an unsigned big-endian integer of width 1, 2, 4 or 8 bytes.
func (c *byteCursor) uint(width int) int64 {
	b := c.take(width)
	var n uint64
	for _, x := range b {
		n = n<<8 | uint64(x)
	}
	return int64(n)
}

// readKeyBlockMeta reads the v1/v2 key-block meta: the counts and sizes of the
// key-block info and the key blocks (docs/mdict/FORMAT.md §3.1).
func (mdict *MdictBase) readKeyBlockMeta() error {
	// A registered dictionary encrypts this very header with a key derived
	// from the purchaser's registration code, which a reader without that
	// code cannot have.
	if mdict.meta.encryptType&EncryptRecordEnc != 0 {
		return errors.New("registered dictionary: it can only be read with the purchaser's registration code")
	}

	file, err := os.Open(mdict.filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	v2 := mdict.meta.version >= 2.0
	n := 4 * 4
	if v2 {
		n = 8*5 + 4 // five numbers and their adler32
	}
	buf, err := readFileFromPos(file, mdict.meta.keyBlockMetaStartOffset, int64(n))
	if err != nil {
		return err
	}
	// MDict refuses a mismatch here; it is noted instead. Registered
	// dictionaries, whose encrypted counts are what the check exists to catch,
	// were refused above, and corrupt counts fail on the reads they drive.
	if v2 && adler32.Checksum(buf[:40]) != binary.BigEndian.Uint32(buf[40:44]) {
		logx.V("%s: key-block header checksum does not match", filepath.Base(mdict.filePath))
	}

	w := mdict.meta.numberWidth
	c := byteCursor{b: buf}
	m := &mdictKeyBlockMeta{}
	m.keyBlockNum = c.uint(w)
	m.entriesNum = c.uint(w)
	if v2 {
		m.keyBlockInfoDecompressSize = c.uint(w)
	}
	m.keyBlockInfoCompressedSize = c.uint(w)
	m.keyBlockDataTotalSize = c.uint(w)
	if c.bad || m.keyBlockNum < 0 || m.entriesNum < 0 || m.keyBlockInfoCompressedSize < 0 || m.keyBlockDataTotalSize < 0 {
		return errors.New("the key-block header is corrupt")
	}
	m.keyBlockInfoStartOffset = mdict.meta.keyBlockMetaStartOffset + int64(n)

	mdict.keyBlockMeta = m
	return nil
}

func (mdict *MdictBase) readKeyBlockInfo() error {
	file, err := os.Open(mdict.filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	buffer, err := readFileFromPos(file, mdict.keyBlockMeta.keyBlockInfoStartOffset, mdict.keyBlockMeta.keyBlockInfoCompressedSize)
	if err != nil {
		return err
	}
	return mdict.decodeKeyBlockInfo(buffer)
}

// decodeKeyBlockInfo decodes the key-block info: per key block, its entry
// count, first and last keys, and compressed and decompressed sizes
// (docs/mdict/FORMAT.md §3.2). v2 stores it as an ordinary block, which MDict
// decodes with the general block codec, so any compression method is
// accepted; v1 stores it uncompressed.
func (mdict *MdictBase) decodeKeyBlockInfo(data []byte) error {
	v2 := mdict.meta.version >= 2.0

	// Encrypted bit 1: the payload after the 8-byte block header is
	// fast-encrypted. MDict undoes it whatever the version.
	if mdict.meta.encryptType&EncryptKeyInfoEnc != 0 {
		decryptKeyInfo(data)
	}
	info := data
	if v2 {
		out, err := mdict.decodeBlock(data, int(mdict.keyBlockMeta.keyBlockInfoDecompressSize))
		if err != nil {
			return fmt.Errorf("key-block info: %w", err)
		}
		info = out
	}

	sizeWidth, term := 1, 0
	if v2 {
		sizeWidth, term = 2, 1
	}
	unit := 1
	if mdict.meta.keyEncoding == EncodingUtf16 {
		unit = 2
	}
	w := mdict.meta.numberWidth

	c := byteCursor{b: info}
	list := make([]*mdictKeyBlockInfoItem, 0)
	var compAccu, decompAccu int64
	for i := int64(0); i < mdict.keyBlockMeta.keyBlockNum; i++ {
		// The entry count, then the first and last keys, which only a binary
		// search over the key blocks would need: skipped.
		entries := c.uint(w)
		c.take((int(c.uint(sizeWidth)) + term) * unit)
		c.take((int(c.uint(sizeWidth)) + term) * unit)
		compSize := c.uint(w)
		decompSize := c.uint(w)
		if c.bad || compSize < 0 || decompSize < 0 {
			return fmt.Errorf("the key-block info ends after %d of %d key blocks", i, mdict.keyBlockMeta.keyBlockNum)
		}
		list = append(list, &mdictKeyBlockInfoItem{
			entries:                       entries,
			keyBlockCompressSize:          compSize,
			keyBlockDeCompressSize:        decompSize,
			keyBlockDeCompressAccumulator: decompAccu,
		})
		compAccu += compSize
		decompAccu += decompSize
	}

	mdict.keyBlockInfo = &mdictKeyBlockInfo{
		keyBlockEntriesStartOffset: mdict.keyBlockMeta.keyBlockInfoCompressedSize + mdict.keyBlockMeta.keyBlockInfoStartOffset,
		keyBlockInfoList:           list,
	}

	// The meta's key-blocks size locates the record section and bounds the
	// key-block read; MDict never compares it with the blocks' own sizes, so a
	// difference is a note, and a block that falls outside is dropped by
	// decodeKeyEntries.
	if compAccu != mdict.keyBlockMeta.keyBlockDataTotalSize {
		logx.V("%s: key blocks add up to %d bytes, the header says %d",
			filepath.Base(mdict.filePath), compAccu, mdict.keyBlockMeta.keyBlockDataTotalSize)
	}
	return nil
}

func (mdict *MdictBase) readKeyEntries() error {
	file, err := os.Open(mdict.filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	buffer, err := readFileFromPos(file,
		mdict.keyBlockInfo.keyBlockEntriesStartOffset,
		mdict.keyBlockMeta.keyBlockDataTotalSize)
	if err != nil {
		return err
	}
	return mdict.decodeKeyEntries(buffer)
}

// decodeKeyEntries decodes every key block. A block that cannot be decoded
// costs its own keys and nothing else: MDict loads key blocks lazily, so a
// corrupt one leaves the rest of the dictionary readable, and refusing the
// whole file would refuse a dictionary MDict opens.
func (mdict *MdictBase) decodeKeyEntries(buf []byte) error {
	list := mdict.keyBlockInfo.keyBlockInfoList
	var keyBytes int64
	if n := len(list); n > 0 {
		keyBytes = list[n-1].keyBlockDeCompressAccumulator + list[n-1].keyBlockDeCompressSize
	}
	entries := make([]*MDictKeywordEntry, 0, keyCountHint(mdict.keyBlockMeta.entriesNum, keyBytes, mdict.meta.numberWidth))
	var gaps []int // index of the last entry before an unreadable block
	failed := 0
	var firstErr error

	start := int64(0)
	for idx, item := range list {
		end := start + item.keyBlockCompressSize
		if item.keyBlockCompressSize < 8 || end > int64(len(buf)) {
			// This block and every later one lie outside the key section.
			failed += len(list) - idx
			if firstErr == nil {
				firstErr = fmt.Errorf("key block %d lies outside the key section", idx)
			}
			break
		}
		blk, err := mdict.decodeBlock(buf[start:end], int(item.keyBlockDeCompressSize))
		start = end
		if err != nil {
			failed++
			if firstErr == nil {
				firstErr = fmt.Errorf("key block %d: %w", idx, err)
			}
			gaps = append(gaps, len(entries)-1)
			continue
		}
		entries = mdict.appendKeyBlock(entries, blk, item.entries)
	}
	if len(entries) == 0 && failed > 0 {
		return firstErr
	}

	// Each record ends where the next key's record starts, across key-block
	// seams too. Across an unreadable block that successor is unknown, so the
	// entry before the gap gets the open-ended sentinel and is read to the end
	// of its record block.
	chainRecordEnds(entries, 0)
	for _, g := range gaps {
		if g >= 0 {
			entries[g].RecordEndOffset = 0
		}
	}

	if failed > 0 {
		logx.Warn("%s: %d of %d key blocks cannot be read (%v); the headwords in them are missing",
			filepath.Base(mdict.filePath), failed, len(list), firstErr)
	}
	if int64(len(entries)) != mdict.keyBlockMeta.entriesNum && failed == 0 {
		logx.V("%s: %d headwords read, the header says %d",
			filepath.Base(mdict.filePath), len(entries), mdict.keyBlockMeta.entriesNum)
	}

	mdict.keyBlockData = &mdictKeyBlockData{
		keyEntries:                 entries,
		keyEntriesSize:             int64(len(entries)),
		recordBlockMetaStartOffset: mdict.keyBlockInfo.keyBlockEntriesStartOffset + mdict.keyBlockMeta.keyBlockDataTotalSize,
	}
	return nil
}

// chainRecordEnds gives every entry the end offset of its successor's start:
// nothing in either layout stores a record's length, so an entry's extent is
// only knowable from the next one. lastEnd is the "unknown, read to the end of
// the containing block" sentinel, which the two layouts spell differently (0
// for v1/v2, negative for v3 - see LocateAt).
func chainRecordEnds(entries []*MDictKeywordEntry, lastEnd int64) {
	for i := 0; i+1 < len(entries); i++ {
		entries[i].RecordEndOffset = entries[i+1].RecordStartOffset
	}
	if n := len(entries); n > 0 {
		entries[n-1].RecordEndOffset = lastEnd
	}
}

func (mdict *MdictBase) splitKeyBlock(keyBlock []byte) []*MDictKeywordEntry {
	return mdict.appendKeyBlock(nil, keyBlock, 0)
}

// appendKeyBlock splits one decompressed key block,
//
//	[record offset: Number][key text][NUL unit]   … to the end of the block
//
// and appends its entries to dst. Keys come back decoded to UTF-8 and
// trimmed. The entries of a block share one backing array, sized by hint (the
// block's declared key count; 0 when unknown): a dictionary has up to
// millions of entries, and one heap object each was most of the open's
// allocations. Record end offsets are left to chainRecordEnds.
//
// The terminator width follows the key *encoding*, never the file type. A
// v1/v2 MDD defaults to UTF-16 keys - readDictHeader's default for an MDD
// whose header names no encoding - but a v3 MDD is UTF-8 unless its KeyData
// trailer says otherwise, with single-byte keys and a one-byte NUL. Deriving
// the width from fileType==MDD scanned a v3 MDD key block at a two-byte
// stride, walked past every terminator and then off the block.
func (mdict *MdictBase) appendKeyBlock(dst []*MDictKeywordEntry, keyBlock []byte, hint int64) []*MDictKeywordEntry {
	nw := mdict.meta.numberWidth
	enc := mdict.meta.keyEncoding
	// Every key takes at least its number and a terminator, which bounds a
	// declared count that came out of the file.
	if most := int64(len(keyBlock) / (nw + 1)); hint <= 0 || hint > most {
		hint = min(most, 64)
	}
	slab := make([]MDictKeywordEntry, 0, hint)

	for start := 0; start+nw <= len(keyBlock); {
		var off int64
		if nw == 8 {
			off = int64(beBinToU64(keyBlock[start : start+8]))
		} else {
			off = int64(beBinToU32(keyBlock[start : start+4]))
		}
		text := start + nw

		// The key ends at a NUL unit. A truncated or mis-framed block may hold
		// no terminator at all; the remainder is then the key.
		end := len(keyBlock)
		if enc == EncodingUtf16 {
			for i := text; i+1 < len(keyBlock); i += 2 {
				if keyBlock[i] == 0 && keyBlock[i+1] == 0 {
					end = i
					break
				}
			}
		} else if i := bytes.IndexByte(keyBlock[text:], 0); i >= 0 {
			end = text + i
		}
		width := 1
		if enc == EncodingUtf16 {
			width = 2
		}

		// v1.x (and some UTF-16 v2.x) dicts pad keys with leading/trailing
		// spaces; the reference reader strips them so lookups match.
		slab = append(slab, MDictKeywordEntry{
			RecordStartOffset: off,
			KeyWord:           strings.TrimSpace(DecodeText(keyBlock[text:end], enc)),
		})
		start = end + width
	}
	for i := range slab {
		dst = append(dst, &slab[i])
	}
	return dst
}

// keyCountHint is how many entries to preallocate for: the declared count,
// bounded by what the decoded key bytes could hold and by a fixed ceiling, as
// the count is the file's word.
func keyCountHint(declared, keyBytes int64, numberWidth int) int {
	n := declared
	if limit := keyBytes / int64(numberWidth+1); n > limit {
		n = limit
	}
	if n > 1<<22 {
		n = 1 << 22
	}
	if n < 0 {
		n = 0
	}
	return int(n)
}

func (mdict *MdictBase) readRecordBlockMeta() error {
	file, err := os.Open(mdict.filePath)
	if err != nil {
		return err
	}
	defer file.Close()
	/*
	 * [0:8/4]    - record block number
	 * [8:16/4:8] - num entries the key-value entries number
	 * [16:24/8:12] - record block info size
	 * [24:32/12:16] - record block size
	 *
	 */
	recordBlockMetaBufferLen := int64(16)
	if mdict.meta.version >= 2.0 {
		recordBlockMetaBufferLen = 32
	}

	recordBlockStartOffset := mdict.keyBlockInfo.keyBlockEntriesStartOffset + mdict.keyBlockMeta.keyBlockDataTotalSize

	buffer, err := readFileFromPos(file, recordBlockStartOffset, recordBlockMetaBufferLen)
	if err != nil {
		return err
	}

	err = mdict.decodeRecordBlockMeta(buffer, recordBlockStartOffset, recordBlockStartOffset+recordBlockMetaBufferLen)
	if err != nil {
		return err
	}
	return nil
}

// decodeRecordBlockMeta decodes the record section's header (docs/mdict/
// FORMAT.md §3.4):
//
//	[record blocks][entries][record-info size][record-blocks size]   Numbers
//
// The entry count is read and not used. MDict never compares it with the
// number of keys, and dictionaries in circulation disagree
// (cedict_2026-8-7.mdx: 465 621 against 560 500 keys) while reading
// perfectly well.
func (mdict *MdictBase) decodeRecordBlockMeta(data []byte, startOffset, endOffset int64) error {
	c := byteCursor{b: data}
	w := mdict.meta.numberWidth
	m := &mdictRecordBlockMeta{
		keyRecordMetaStartOffset: startOffset,
		keyRecordMetaEndOffset:   endOffset,
	}
	m.recordBlockNum = c.uint(w)
	m.entriesNum = c.uint(w)
	m.recordBlockInfoCompSize = c.uint(w)
	m.recordBlockCompSize = c.uint(w)
	if c.bad || m.recordBlockNum < 0 || m.recordBlockInfoCompSize < 0 || m.recordBlockCompSize < 0 {
		return errors.New("the record section header is corrupt")
	}
	mdict.recordBlockMeta = m
	return nil
}

func (mdict *MdictBase) readRecordBlockInfo() error {
	file, err := os.Open(mdict.filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	start := mdict.recordBlockMeta.keyRecordMetaEndOffset
	n := mdict.recordBlockMeta.recordBlockInfoCompSize
	buffer, err := readFileFromPos(file, start, n)
	if err != nil {
		return err
	}
	return mdict.decodeRecordBlockInfo(buffer, start, start+n)
}

// decodeRecordBlockInfo decodes the record-block table: a compressed and a
// decompressed size per block. Like MDict, it checks one thing: the
// compressed sizes add up to the section's declared size.
func (mdict *MdictBase) decodeRecordBlockInfo(data []byte, startOffset, endOffset int64) error {
	w := mdict.meta.numberWidth
	n := mdict.recordBlockMeta.recordBlockNum
	if n > int64(len(data))/int64(2*w) {
		return fmt.Errorf("the record table holds %d bytes, too few for %d blocks", len(data), n)
	}

	c := byteCursor{b: data}
	list := make([]*MdictRecordBlockInfoListItem, 0, n)
	var compAccu, decompAccu int64
	for i := int64(0); i < n; i++ {
		compSize := c.uint(w)
		decompSize := c.uint(w)
		if compSize < 0 || decompSize < 0 {
			return fmt.Errorf("record block %d declares a negative size", i)
		}
		list = append(list, &MdictRecordBlockInfoListItem{
			compressSize:                compSize,
			deCompressSize:              decompSize,
			compressAccumulatorOffset:   compAccu,
			deCompressAccumulatorOffset: decompAccu,
		})
		compAccu += compSize
		decompAccu += decompSize
	}
	if compAccu != mdict.recordBlockMeta.recordBlockCompSize {
		return fmt.Errorf("record blocks add up to %d bytes, the header says %d", compAccu, mdict.recordBlockMeta.recordBlockCompSize)
	}

	mdict.recordBlockInfo = &mdictRecordBlockInfo{
		recordInfoList:             list,
		recordBlockInfoStartOffset: startOffset,
		recordBlockInfoEndOffset:   endOffset,
		recordBlockDataStartOffset: endOffset,
	}
	return nil
}

// recordBlockAt returns the record block holding a decompressed offset.
//
// recordInfoList is built by readRecordBlockInfo in file order, so
// deCompressAccumulatorOffset is sorted by construction and the offset can be
// found by binary search: a linear scan per lookup costs nothing on a
// dozen-block dictionary and O(N*B) over the ingest of a large one.
func (mdict *MdictBase) recordBlockAt(offset int64) *MdictRecordBlockInfoListItem {
	list := mdict.recordBlockInfo.recordInfoList
	i := sort.Search(len(list), func(i int) bool {
		return list[i].deCompressAccumulatorOffset+list[i].deCompressSize > offset
	})
	if i == len(list) || offset < list[i].deCompressAccumulatorOffset {
		return nil
	}
	return list[i]
}

func (mdict *MdictBase) locateByKeywordEntry(item *MDictKeywordEntry) ([]byte, error) {
	recordBlockInfo := mdict.recordBlockAt(item.RecordStartOffset)
	if recordBlockInfo == nil {
		return nil, fmt.Errorf("key-item record info not found (recordStartOffset=%d)", item.RecordStartOffset)
	}

	recordBlockStartOffset := recordBlockInfo.compressAccumulatorOffset + mdict.recordBlockInfo.recordBlockDataStartOffset
	recordBlockLen := recordBlockInfo.compressSize

	recordBlock, err := mdict.decompressedRecordBlock(recordBlockStartOffset, recordBlockLen, recordBlockInfo)
	if err != nil {
		return nil, err
	}

	if int64(len(recordBlock)) != recordBlockInfo.deCompressSize {
		return nil, errors.New("recordBlock length not equals decompress Size")
	}

	start := item.RecordStartOffset - recordBlockInfo.deCompressAccumulatorOffset
	// End offsets are chained from the NEXT entry, which may be filed in a
	// later record block, and a corrupt file can name anything at all: clamp
	// to this block instead of slicing past it. 0 still means "to the end".
	end := int64(len(recordBlock))
	if item.RecordEndOffset != 0 {
		if e := item.RecordEndOffset - recordBlockInfo.deCompressAccumulatorOffset; e < end {
			end = e
		}
	}
	if start < 0 || start > end {
		return nil, fmt.Errorf("record offsets out of range (start=%d end=%d block=%d)", start, end, len(recordBlock))
	}

	data := recordBlock[start:end]

	if mdict.fileType == MdictTypeMdd {
		return data, nil
	}

	if mdict.meta.encoding == EncodingUtf16 {
		datastr, err1 := decodeLittleEndianUtf16(data)
		if err1 != nil {
			return nil, err1
		}
		return []byte(datastr), nil
	}
	return data, nil
}

// recordBlockCacheCap bounds the per-file decompressed record-block cache.
const recordBlockCacheCap = 8

// decompressedRecordBlock returns the decompressed record block at startOffset,
// caching it (bounded FIFO) so adjacent lookups and inline resources that share
// a block skip the re-open + re-decompress - the dominant per-lookup cost. The
// returned slice is shared read-only; callers only sub-slice it, never mutate.
func (mdict *MdictBase) decompressedRecordBlock(startOffset, compLen int64, info *MdictRecordBlockInfoListItem) ([]byte, error) {
	return mdict.cachedBlock(startOffset, func() ([]byte, error) {
		return mdict.decodeRecordBlock(startOffset, compLen, info)
	})
}

// cachedBlock returns the decompressed block identified by key, calling decode
// on a miss. One implementation for both layouts: the v1/v2 and v3 record
// sections decode differently but cache identically, and two copies of a
// bounded FIFO is one copy too many.
//
// decode runs OUTSIDE the lock. Holding a mutex across a zlib inflate would
// serialise every concurrent lookup in the process behind the slowest one; the
// cost of letting two callers decode the same block in a rare race is one
// wasted inflate, and the first result to arrive is kept.
func (mdict *MdictBase) cachedBlock(key int64, decode func() ([]byte, error)) ([]byte, error) {
	mdict.blkMu.Lock()
	if blk, ok := mdict.blkCache[key]; ok {
		mdict.blkMu.Unlock()
		return blk, nil
	}
	mdict.blkMu.Unlock()

	blk, err := decode()
	if err != nil {
		return nil, err
	}

	mdict.blkMu.Lock()
	defer mdict.blkMu.Unlock()
	if existing, ok := mdict.blkCache[key]; ok {
		return existing, nil // lost the race; one block is one block
	}
	if mdict.blkCache == nil {
		mdict.blkCache = make(map[int64][]byte, recordBlockCacheCap)
	}
	mdict.blkCache[key] = blk
	mdict.blkOrder = append(mdict.blkOrder, key)
	if len(mdict.blkOrder) > recordBlockCacheCap {
		delete(mdict.blkCache, mdict.blkOrder[0])
		mdict.blkOrder = mdict.blkOrder[1:]
	}
	return blk, nil
}

// decodeRecordBlock reads and decodes one v1/v2 record block.
func (mdict *MdictBase) decodeRecordBlock(startOffset, compLen int64, info *MdictRecordBlockInfoListItem) ([]byte, error) {
	file, err := os.Open(mdict.filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	comp, err := readFileFromPos(file, startOffset, compLen)
	if err != nil {
		return nil, err
	}

	// The block header is 8 bytes (4 info word + 4 checksum) and
	// info.compressSize is a PER-BLOCK value read out of the file.
	// decodeRecordBlockInfo validates only the aggregate - the total
	// compressed size - which stays satisfied while one block declares 0 or
	// 1-7 bytes, too few to hold its own header.
	if int64(info.compressSize) < 8 || int64(info.compressSize) > int64(len(comp)) {
		return nil, fmt.Errorf("record block at %d declares %d bytes, block holds %d",
			startOffset, info.compressSize, len(comp))
	}

	return mdict.decodeBlock(comp[:info.compressSize], int(info.deCompressSize))
}

func (mdict *MdictBase) getKeyWordEntries() ([]*MDictKeywordEntry, error) {
	return mdict.keyBlockData.keyEntries, nil
}
