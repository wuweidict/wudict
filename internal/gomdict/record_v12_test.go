// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package go_mdict

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A v1/v2 record section, written the way the format writes it: leading junk so
// recordBlockDataStartOffset is not accidentally zero, then per block
//
//	[4 LE compression type = 0][4 BE adler32 (unread)][payload]
//
// Uncompressed blocks are enough here - the seam arithmetic under test is the
// same for every compression type, and decodeRecordBlock's inflate paths are
// already covered elsewhere.
func newV12Fixture(t *testing.T, blocks []string) *MdictBase {
	t.Helper()

	var body bytes.Buffer
	body.WriteString("MDX header stand-in\x00")
	dataStart := int64(body.Len())

	list := make([]*MdictRecordBlockInfoListItem, 0, len(blocks))
	var compAccu, decompAccu int64
	for _, blk := range blocks {
		var p bytes.Buffer
		_ = binary.Write(&p, binary.LittleEndian, uint32(0))
		_ = binary.Write(&p, binary.BigEndian, uint32(0))
		p.WriteString(blk)
		body.Write(p.Bytes())

		list = append(list, &MdictRecordBlockInfoListItem{
			compressSize:                int64(p.Len()),
			deCompressSize:              int64(len(blk)),
			compressAccumulatorOffset:   compAccu,
			deCompressAccumulatorOffset: decompAccu,
		})
		compAccu += int64(p.Len())
		decompAccu += int64(len(blk))
	}

	path := filepath.Join(t.TempDir(), "fixture.mdx")
	if err := os.WriteFile(path, body.Bytes(), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	m := &MdictBase{
		filePath: path,
		fileType: MdictTypeMdx,
		meta:     &mdictMeta{version: 2.0, encoding: EncodingUtf8},
		recordBlockInfo: &mdictRecordBlockInfo{
			recordInfoList:             list,
			recordBlockDataStartOffset: dataStart,
		},
		rangeTreeRoot: new(RecordBlockRangeTreeNode),
	}
	m.buildRecordRangeTree()
	return m
}

// The bug this guards: splitKeyBlock only ever backfilled end offsets inside
// one key block, so the last entry of every block kept the 0 sentinel and came
// back with its own record plus the rest of the record block. Chaining fixes
// that, and introduces the case the clamp exists for - a chained end that names
// an offset in the NEXT record block, which without clamping slices out of
// range.
func TestLocateByKeywordEntryV12Extents(t *testing.T) {
	m := newV12Fixture(t, []string{"0123456789", "ABCDEF"})

	cases := []struct {
		name       string
		start, end int64
		want       string
		wantErr    bool
	}{
		{name: "within a block", start: 2, end: 5, want: "234"},
		{name: "whole first block", start: 0, end: 10, want: "0123456789"},
		{name: "chained end at the block seam", start: 7, end: 10, want: "789"},
		{name: "chained end in the next block is clamped", start: 7, end: 13, want: "789"},
		{name: "zero sentinel reads to end of block", start: 7, end: 0, want: "789"},
		{name: "second block", start: 12, end: 14, want: "CD"},
		{name: "last entry of the dictionary", start: 12, end: 0, want: "CDEF"},
		{name: "end before start", start: 5, end: 2, wantErr: true},
		{name: "offset past the data", start: 16, end: 0, wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := m.locateByKeywordEntry(&MDictKeywordEntry{
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
}

func TestChainRecordEnds(t *testing.T) {
	starts := func(v ...int64) []*MDictKeywordEntry {
		out := make([]*MDictKeywordEntry, len(v))
		for i, s := range v {
			out[i] = &MDictKeywordEntry{RecordStartOffset: s}
		}
		return out
	}

	cases := []struct {
		name    string
		entries []*MDictKeywordEntry
		last    int64
		want    []int64
	}{
		{name: "empty", entries: nil, last: 0},
		{name: "single v1/v2", entries: starts(4), last: 0, want: []int64{0}},
		{name: "single v3", entries: starts(4), last: -1, want: []int64{-1}},
		{name: "across seams v1/v2", entries: starts(0, 10, 25), last: 0, want: []int64{10, 25, 0}},
		{name: "across seams v3", entries: starts(0, 10, 25), last: -1, want: []int64{10, 25, -1}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			chainRecordEnds(tc.entries, tc.last)
			for i, want := range tc.want {
				if got := tc.entries[i].RecordEndOffset; got != want {
					t.Errorf("entry %d: end = %d, want %d", i, got, want)
				}
			}
		})
	}
}

// A v3 directory entry declares its own block size. One that wraps int64
// negative seeks BACKWARDS onto the entry that was just read, so the scanner
// reads it forever: the only failure in this package that recoverOpen cannot
// turn into an error, because it never returns.
func TestScanV3BlocksRejectsHostileBlockSize(t *testing.T) {
	entry := func(typ uint32, size uint64) []byte {
		b := make([]byte, 12)
		binary.BigEndian.PutUint32(b, typ)
		binary.BigEndian.PutUint64(b[4:], size)
		return b
	}

	cases := []struct {
		name string
		file []byte
	}{
		{name: "size wraps int64 negative", file: entry(v3BlockTypeRecordData, 0xFFFFFFFFFFFFFFF4)},
		{name: "size past end of file", file: entry(v3BlockTypeKeyData, 1<<20)},
		{name: "size overflows the offset", file: entry(v3BlockTypeKeyData, uint64(1)<<62)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "hostile.mdx")
			if err := os.WriteFile(path, tc.file, 0o600); err != nil {
				t.Fatalf("write: %v", err)
			}
			m := &MdictBase{filePath: path, meta: &mdictMeta{version: 3.0}}

			done := make(chan error, 1)
			go func() { done <- m.scanV3Blocks() }()
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("scan accepted a block size the file cannot hold")
				}
			case <-time.After(5 * time.Second):
				t.Fatal("scan did not terminate")
			}
		})
	}
}
