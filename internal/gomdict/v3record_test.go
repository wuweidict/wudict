// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package go_mdict

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// v3 record-data section, written the way the format writes it, so the table
// walker and the locator are exercised against bytes rather than against a
// mock. The plan asked for the v3 locate to be checked "against the v1/v2
// path"; there is no v1/v2 fixture in the repo to diff against, and the two
// layouts do not share a record section anyway, so the stronger check is used
// instead: the payload is synthesised here, so the expected bytes for every
// offset range are known exactly.
//
//	[4 BE] block count
//	[8 BE] total decompressed size
//	per block: [4 BE decompSize][4 BE compSize][payload]
//	payload:   [4 LE info][4 BE adler32 over the compressed bytes][data]
//
// info 0 is "no compression, no encryption", info 2 is zlib; both are written
// so decodeBlockV3 is covered on the path the locator actually takes.
func writeV3Records(t *testing.T, blocks [][]byte, zlibBlock bool) (path string, total int64) {
	t.Helper()

	var body bytes.Buffer
	// Leading junk: recordData is an absolute file offset, and a table that
	// ignored it would still pass if the section started at zero.
	body.Write([]byte("MDX3 header stand-in\x00\x00"))
	recordData := int64(body.Len())

	var payloads [][]byte
	for _, raw := range blocks {
		data := raw
		info := uint32(0)
		if zlibBlock {
			var zb bytes.Buffer
			zw := zlib.NewWriter(&zb)
			if _, err := zw.Write(raw); err != nil {
				t.Fatalf("zlib write: %v", err)
			}
			if err := zw.Close(); err != nil {
				t.Fatalf("zlib close: %v", err)
			}
			data = zb.Bytes()
			info = 2
		}
		var p bytes.Buffer
		_ = binary.Write(&p, binary.LittleEndian, info)
		_ = binary.Write(&p, binary.BigEndian, adler32Of(data))
		p.Write(data)
		payloads = append(payloads, p.Bytes())
		total += int64(len(raw))
	}

	_ = binary.Write(&body, binary.BigEndian, uint32(len(blocks)))
	_ = binary.Write(&body, binary.BigEndian, uint64(total))
	for i, p := range payloads {
		_ = binary.Write(&body, binary.BigEndian, uint32(len(blocks[i])))
		_ = binary.Write(&body, binary.BigEndian, uint32(len(p)))
		body.Write(p)
	}
	// Trailing junk: the walker must stop after block count blocks.
	body.Write([]byte("\xff\xff\xff\xff"))

	path = filepath.Join(t.TempDir(), "fixture.mdx")
	if err := os.WriteFile(path, body.Bytes(), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	// recordData is what scanV3Blocks would have recorded.
	return path, recordData
}

func newV3Fixture(t *testing.T, blocks [][]byte, zlibBlock bool) *MdictBase {
	t.Helper()
	path, recordData := writeV3Records(t, blocks, zlibBlock)
	return &MdictBase{
		filePath: path,
		fileType: MdictTypeMdx,
		meta: &mdictMeta{
			version:   3.0,
			encoding:  EncodingUtf8,
			v3Offsets: &v3BlockOffsets{recordData: recordData},
		},
	}
}

// blocks whose decompressed sizes are 10, 6 and 8: cumulative starts 0, 10, 16,
// total 24.
var v3TestBlocks = [][]byte{
	[]byte("0123456789"),
	[]byte("ABCDEF"),
	[]byte("wxyz!?+-"),
}

const v3TestAll = "0123456789ABCDEFwxyz!?+-"

func TestRecordBlockTableV3(t *testing.T) {
	for _, zl := range []bool{false, true} {
		name := "raw"
		if zl {
			name = "zlib"
		}
		t.Run(name, func(t *testing.T) {
			m := newV3Fixture(t, v3TestBlocks, zl)
			tbl, err := m.recordBlockTableV3()
			if err != nil {
				t.Fatalf("recordBlockTableV3: %v", err)
			}
			if len(tbl) != 3 {
				t.Fatalf("blocks = %d, want 3", len(tbl))
			}
			wantAcc := []int64{0, 10, 16}
			wantDec := []int64{10, 6, 8}
			for i := range tbl {
				if tbl[i].decompAcc != wantAcc[i] {
					t.Errorf("block %d decompAcc = %d, want %d", i, tbl[i].decompAcc, wantAcc[i])
				}
				if tbl[i].decompSize != wantDec[i] {
					t.Errorf("block %d decompSize = %d, want %d", i, tbl[i].decompSize, wantDec[i])
				}
				if tbl[i].compSize <= 0 {
					t.Errorf("block %d compSize = %d, want > 0", i, tbl[i].compSize)
				}
			}
			// Every recorded position must decode to its own block.
			for i := range tbl {
				got, err := m.blockV3(&tbl[i])
				if err != nil {
					t.Fatalf("blockV3(%d): %v", i, err)
				}
				if !bytes.Equal(got, v3TestBlocks[i]) {
					t.Errorf("block %d = %q, want %q", i, got, v3TestBlocks[i])
				}
			}
		})
	}
}

func TestLocateByKeywordEntryV3(t *testing.T) {
	cases := []struct {
		name       string
		start, end int64
		want       string
		wantErr    bool
	}{
		{name: "within first block", start: 2, end: 5, want: "234"},
		{name: "whole first block", start: 0, end: 10, want: "0123456789"},
		{name: "starts at a block boundary", start: 10, end: 13, want: "ABC"},
		{name: "spans one boundary", start: 8, end: 12, want: "89AB"},
		{name: "spans two boundaries", start: 8, end: 18, want: "89ABCDEFwx"},
		{name: "spans every block", start: 0, end: 24, want: v3TestAll},
		{name: "zero end means to end of block", start: 4, end: 0, want: "456789"},
		{name: "negative end sentinel", start: 18, end: -1, want: "yz!?+-"},
		{name: "end past the data is clamped", start: 20, end: 999, want: "!?+-"},
		{name: "end before start falls back to block end", start: 12, end: 4, want: "CDEF"},
		{name: "last byte", start: 23, end: 24, want: "-"},
		{name: "negative start", start: -1, end: 4, wantErr: true},
		{name: "offset past the data", start: 24, end: 0, wantErr: true},
	}

	for _, zl := range []bool{false, true} {
		name := "raw"
		if zl {
			name = "zlib"
		}
		t.Run(name, func(t *testing.T) {
			m := newV3Fixture(t, v3TestBlocks, zl)
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					got, err := m.locateByKeywordEntryV3(&MDictKeywordEntry{
						RecordStartOffset: tc.start,
						RecordEndOffset:   tc.end,
					})
					if tc.wantErr {
						if err == nil {
							t.Fatalf("got %q, want an error", got)
						}
						return
					}
					if err != nil {
						t.Fatalf("locate: %v", err)
					}
					if string(got) != tc.want {
						t.Errorf("got %q, want %q", got, tc.want)
					}
				})
			}
		})
	}
}

// The table is what makes the locator O(log B); building it per lookup would
// restore the bug this fix removes. Concurrent callers must therefore share one
// table, which is asserted on the backing array rather than on its contents.
func TestRecordBlockTableV3BuiltOnce(t *testing.T) {
	m := newV3Fixture(t, v3TestBlocks, true)

	const n = 32
	firsts := make([]*v3RecordBlock, n)
	bodies := make([]string, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			tbl, err := m.recordBlockTableV3()
			if err != nil {
				t.Errorf("goroutine %d: table: %v", i, err)
				return
			}
			firsts[i] = &tbl[0]
			// Straddling read, so the block cache is hit concurrently too.
			b, err := m.locateByKeywordEntryV3(&MDictKeywordEntry{
				RecordStartOffset: 8, RecordEndOffset: 18,
			})
			if err != nil {
				t.Errorf("goroutine %d: locate: %v", i, err)
				return
			}
			bodies[i] = string(b)
		}(i)
	}
	close(start)
	wg.Wait()

	for i := 0; i < n; i++ {
		if firsts[i] != firsts[0] {
			t.Fatalf("goroutine %d got a different table (%p vs %p): built more than once",
				i, firsts[i], firsts[0])
		}
		if bodies[i] != "89ABCDEFwx" {
			t.Errorf("goroutine %d body = %q, want %q", i, bodies[i], "89ABCDEFwx")
		}
	}
}

// The block count is read straight out of the file, so a corrupt one must be
// refused rather than preallocated for.
func TestRecordBlockTableV3RejectsCorruptCount(t *testing.T) {
	m := newV3Fixture(t, v3TestBlocks, false)
	raw, err := os.ReadFile(m.filePath)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	off := m.meta.v3Offsets.recordData
	binary.BigEndian.PutUint32(raw[off:off+4], 1<<30)
	if err := os.WriteFile(m.filePath, raw, 0o600); err != nil {
		t.Fatalf("rewrite fixture: %v", err)
	}
	if _, err := m.recordBlockTableV3(); err == nil {
		t.Fatal("corrupt block count accepted")
	}
}
