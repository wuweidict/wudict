// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package go_mdict

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"hash/adler32"
	"os"
	"path/filepath"
	"testing"
	"unicode/utf16"
)

// Builders for whole MDict files, laid out as docs/mdict/FORMAT.md describes,
// so the parser is tested against complete containers rather than against its
// own pieces.

// testBlock frames payload as one block (FORMAT.md §5): info word, adler32,
// payload. method 0 stores, 2 zlib-compresses; enc 1 fast-encrypts the first
// encLen bytes of the compressed payload with key. The adler32 follows MDict's
// rule: over the decrypted payload when encrypted, else the decompressed data.
func testBlock(t *testing.T, payload []byte, method, enc uint32, encLen int, key []byte) []byte {
	t.Helper()
	data := payload
	if method == 2 {
		var zb bytes.Buffer
		zw := zlib.NewWriter(&zb)
		zw.Write(payload)
		zw.Close()
		data = zb.Bytes()
	}
	sum := adler32.Checksum(payload)
	if enc != 0 {
		sum = adler32.Checksum(data)
		if encLen > len(data) {
			encLen = len(data)
		}
		data = append([]byte(nil), data...)
		fastEncrypt(data[:encLen], key)
	}
	out := make([]byte, 8, 8+len(data))
	binary.LittleEndian.PutUint32(out, method|enc<<4|uint32(encLen)<<8)
	binary.BigEndian.PutUint32(out[4:], sum)
	return append(out, data...)
}

// fastEncrypt is the inverse of fastDecrypt.
func fastEncrypt(b, key []byte) {
	prev := byte(0x36)
	for i := range b {
		x := b[i] ^ prev ^ byte(i) ^ key[i%len(key)]
		b[i] = x>>4 | x<<4
		prev = b[i]
	}
}

func be32(v uint32) []byte { b := make([]byte, 4); binary.BigEndian.PutUint32(b, v); return b }
func be64(v uint64) []byte { b := make([]byte, 8); binary.BigEndian.PutUint64(b, v); return b }

func utf16le(s string) []byte {
	var out []byte
	for _, u := range utf16.Encode([]rune(s)) {
		out = append(out, byte(u), byte(u>>8))
	}
	return out
}

// v2Spec describes a v2 file. keys[i] owns records[i]; keysPerBlock and
// recsPerBlock group them into key and record blocks.
type v2Spec struct {
	attrs           string // header attributes after the version pair
	keys            []string
	records         []string
	encode          func(string) []byte // text encoding of keys and records
	utf16           bool                // key-info lengths count UTF-16 units
	keysPerBlock    int
	recsPerBlock    int
	corruptKeyBlock int // 1-based key block whose payload is garbled; 0 for none
	recordEntries   int // record-section entry count; 0 writes the key count
	encryptKeyInfo  bool
}

func writeV2(t *testing.T, s v2Spec) string {
	t.Helper()
	if s.encode == nil {
		s.encode = func(x string) []byte { return []byte(x) }
	}
	if s.keysPerBlock == 0 {
		s.keysPerBlock = 2
	}
	if s.recsPerBlock == 0 {
		s.recsPerBlock = 2
	}
	var out bytes.Buffer

	// header
	hdr := utf16le(`<Dictionary GeneratedByEngineVersion="2.0" RequiredEngineVersion="2.0" ` + s.attrs + "/>\r\n\x00")
	out.Write(be32(uint32(len(hdr))))
	out.Write(hdr)
	_ = binary.Write(&out, binary.LittleEndian, adler32.Checksum(hdr))

	// record offsets and record blocks
	offsets := make([]uint64, len(s.records))
	var recBlocks [][2]int // compressed block, decompressed size
	var recData bytes.Buffer
	var off uint64
	var recTable bytes.Buffer
	for i := 0; i < len(s.records); i += s.recsPerBlock {
		var raw []byte
		for j := i; j < i+s.recsPerBlock && j < len(s.records); j++ {
			offsets[j] = off
			r := s.encode(s.records[j])
			raw = append(raw, r...)
			off += uint64(len(r))
		}
		blk := testBlock(t, raw, 2, 0, 0, nil)
		recData.Write(blk)
		recTable.Write(be64(uint64(len(blk))))
		recTable.Write(be64(uint64(len(raw))))
		recBlocks = append(recBlocks, [2]int{len(blk), len(raw)})
	}

	// key blocks and their info
	unitLen := func(k string) int {
		if s.utf16 {
			return len(utf16.Encode([]rune(k)))
		}
		return len(s.encode(k))
	}
	term := []byte{0}
	if s.utf16 {
		term = []byte{0, 0}
	}
	var keyData, info bytes.Buffer
	nKB := 0
	for i := 0; i < len(s.keys); i += s.keysPerBlock {
		nKB++
		var raw []byte
		end := i + s.keysPerBlock
		if end > len(s.keys) {
			end = len(s.keys)
		}
		for j := i; j < end; j++ {
			raw = append(raw, be64(offsets[j])...)
			raw = append(raw, s.encode(s.keys[j])...)
			raw = append(raw, term...)
		}
		blk := testBlock(t, raw, 2, 0, 0, nil)
		if nKB == s.corruptKeyBlock {
			for k := 8; k < len(blk); k++ {
				blk[k] ^= 0x5A
			}
		}
		keyData.Write(blk)
		first, last := s.keys[i], s.keys[end-1]
		info.Write(be64(uint64(end - i)))
		_ = binary.Write(&info, binary.BigEndian, uint16(unitLen(first)))
		info.Write(s.encode(first))
		info.Write(term)
		_ = binary.Write(&info, binary.BigEndian, uint16(unitLen(last)))
		info.Write(s.encode(last))
		info.Write(term)
		info.Write(be64(uint64(len(blk))))
		info.Write(be64(uint64(len(raw))))
	}
	infoBlk := testBlock(t, info.Bytes(), 2, 0, 0, nil)
	if s.encryptKeyInfo {
		key := ripemd128bytes(append(append([]byte(nil), infoBlk[4:8]...), 0x95, 0x36, 0, 0))
		fastEncrypt(infoBlk[8:], key)
	}

	var meta bytes.Buffer
	meta.Write(be64(uint64(nKB)))
	meta.Write(be64(uint64(len(s.keys))))
	meta.Write(be64(uint64(info.Len())))
	meta.Write(be64(uint64(len(infoBlk))))
	meta.Write(be64(uint64(keyData.Len())))
	out.Write(meta.Bytes())
	out.Write(be32(adler32.Checksum(meta.Bytes())))
	out.Write(infoBlk)
	out.Write(keyData.Bytes())

	entries := s.recordEntries
	if entries == 0 {
		entries = len(s.keys)
	}
	out.Write(be64(uint64(len(recBlocks))))
	out.Write(be64(uint64(entries)))
	out.Write(be64(uint64(recTable.Len())))
	out.Write(be64(uint64(recData.Len())))
	out.Write(recTable.Bytes())
	out.Write(recData.Bytes())

	path := filepath.Join(t.TempDir(), "fixture.mdx")
	if err := os.WriteFile(path, out.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// v3Spec describes a v3 file, every block zlib-compressed and, with a UUID,
// fast-encrypted as MdxBuilder 4 writes them (16 bytes, the UUID key).
type v3Spec struct {
	uuid      string
	keys      []string
	records   []string
	withIndex bool // write the RecordIndex section
	badIndex  bool // write one that disagrees with the data
}

func writeV3(t *testing.T, s v3Spec) string {
	t.Helper()
	var key []byte
	enc := uint32(0)
	if s.uuid != "" {
		key = deriveV3EncryptedKey(s.uuid)
		enc = 1
	}
	block := func(p []byte) []byte { return testBlock(t, p, 2, enc, 16, key) }
	framed := func(p []byte) []byte {
		b := block(p)
		return append(append(be32(uint32(len(p))), be32(uint32(len(b)))...), b...)
	}
	section := func(typ uint32, blocks [][]byte, trailer string) []byte {
		var data bytes.Buffer
		for _, b := range blocks {
			data.Write(framed(b))
		}
		var body bytes.Buffer
		body.Write(be32(uint32(len(blocks))))
		body.Write(be64(uint64(data.Len())))
		body.Write(data.Bytes())
		body.Write(framed([]byte(trailer)))
		return append(append(be32(typ), be64(uint64(body.Len()))...), body.Bytes()...)
	}

	var out bytes.Buffer
	hdr := []byte(fmt.Sprintf(`<ZDB GeneratedByEngineVersion="3.0" RequiredEngineVersion="3.0" ContentType="Html" UUID="%s"/>`+"\r\n\x00", s.uuid))
	out.Write(be32(uint32(len(hdr))))
	out.Write(hdr)
	_ = binary.Write(&out, binary.LittleEndian, adler32.Checksum(hdr))

	// two records per block
	var recBlocks [][]byte
	offsets := make([]uint64, len(s.records))
	var off uint64
	var index bytes.Buffer
	for i := 0; i < len(s.records); i += 2 {
		var raw []byte
		for j := i; j < i+2 && j < len(s.records); j++ {
			offsets[j] = off
			raw = append(raw, s.records[j]...)
			off += uint64(len(s.records[j]))
		}
		recBlocks = append(recBlocks, raw)
		size := uint64(len(framed(raw)))
		if s.badIndex {
			size += 3
		}
		index.Write(be64(size))
		index.Write(be64(uint64(len(raw))))
	}
	var keyRaw []byte
	for i, k := range s.keys {
		keyRaw = append(keyRaw, be64(offsets[i])...)
		keyRaw = append(keyRaw, k...)
		keyRaw = append(keyRaw, 0)
	}

	n := len(s.records)
	out.Write(section(v3BlockTypeRecordData, recBlocks, fmt.Sprintf(`<RecordData encoding="UTF-8" recordCount="%d"/>`+"\r\n", n)))
	if s.withIndex {
		out.Write(section(v3BlockTypeRecordIndex, [][]byte{index.Bytes()}, fmt.Sprintf(`<RecordIndex encoding="UTF-8" recordCount="%d"/>`+"\r\n", n)))
	}
	out.Write(section(v3BlockTypeKeyData, [][]byte{keyRaw}, fmt.Sprintf(`<KeyData encoding="UTF-8" locale="en" keyCount="%d"/>`+"\r\n", len(s.keys))))

	path := filepath.Join(t.TempDir(), "fixture3.mdx")
	if err := os.WriteFile(path, out.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// readAll opens path and returns every headword with its record text.
func readAll(t *testing.T, path string) (*Mdict, map[string]string, []string) {
	t.Helper()
	md, err := New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := md.BuildIndex(); err != nil {
		t.Fatalf("BuildIndex: %v", err)
	}
	entries, _ := md.GetKeyWordEntries()
	got := make(map[string]string, len(entries))
	var order []string
	for _, e := range entries {
		b, err := md.LocateByKeywordEntry(e)
		if err != nil {
			t.Fatalf("locate %q: %v", e.KeyWord, err)
		}
		got[e.KeyWord] = string(b)
		order = append(order, e.KeyWord)
	}
	return md, got, order
}
