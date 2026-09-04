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

package go_mdict

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/wuweidict/wudict/internal/logx"
)

// v3 block type tags (big-endian uint32 in the block directory).
const (
	v3BlockTypeRecordData  = 0x01000000
	v3BlockTypeRecordIndex = 0x02000000
	v3BlockTypeKeyData     = 0x03000000
	v3BlockTypeKeyIndex    = 0x04000000
)

// v3BlockOffsets records the file offsets of each v3 block section, discovered
// by scanning the self-describing block directory that starts immediately
// after the header.
type v3BlockOffsets struct {
	recordData  int64
	keyData     int64
	recordIndex int64
	keyIndex    int64
}

// scanV3Blocks reads the block directory that follows the header and records
// the file offset of each block section. The directory is a sequence of
//
//	[4-byte BE type] [8-byte BE size] [size bytes of data]
//
// terminated by EOF.
func (mdict *MdictBase) scanV3Blocks() error {
	f, err := os.Open(mdict.filePath)
	if err != nil {
		return err
	}
	defer f.Close()

	off := mdict.meta.keyBlockMetaStartOffset
	if _, err := f.Seek(off, io.SeekStart); err != nil {
		return fmt.Errorf("v3 scan: seek: %w", err)
	}

	var offsets v3BlockOffsets

	for {
		var blockType uint32
		var blockSize uint64
		if err := binary.Read(f, binary.BigEndian, &blockType); err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("v3 scan: read type: %w", err)
		}
		if err := binary.Read(f, binary.BigEndian, &blockSize); err != nil {
			return fmt.Errorf("v3 scan: read size: %w", err)
		}
		dataOffset, err := f.Seek(0, io.SeekCurrent)
		if err != nil {
			return fmt.Errorf("v3 scan: tell: %w", err)
		}

		switch blockType {
		case v3BlockTypeRecordData:
			offsets.recordData = dataOffset
		case v3BlockTypeRecordIndex:
			offsets.recordIndex = dataOffset
		case v3BlockTypeKeyData:
			offsets.keyData = dataOffset
		case v3BlockTypeKeyIndex:
			offsets.keyIndex = dataOffset
		default:
			return fmt.Errorf("v3 scan: unknown block type 0x%08x at offset %d", blockType, dataOffset)
		}

		// Seek past this block's data to the next directory entry.
		if _, err := f.Seek(int64(blockSize), io.SeekCurrent); err != nil {
			return fmt.Errorf("v3 scan: seek past block: %w", err)
		}
	}

	// Derive the encrypted key from UUID if the dict is encrypted and we
	// haven't already computed it.
	if mdict.meta.version >= 3.0 && mdict.meta.uuid != "" && mdict.meta.encryptedKey == nil {
		mdict.meta.encryptedKey = deriveV3EncryptedKey(mdict.meta.uuid)
	}

	mdict.meta.v3Offsets = &offsets
	return nil
}

// readKeyEntriesV3 reads all key entries from the v3 key-data block.
//
// Key data block layout:
//
//	[4-byte BE] number of key blocks
//	[8-byte BE] total decompressed size (unused - we read block-by-block)
//	For each block:
//	  [4-byte BE] decompressed size
//	  [4-byte BE] compressed size
//	  [compressed_size bytes] block data (decoded via decodeBlockV3)
//
// Each decompressed key block is split into entries using splitKeyBlock (the
// same function used for v1/v2), which handles both UTF-8 and UTF-16
// encodings.
func (mdict *MdictBase) readKeyEntriesV3() error {
	if mdict.meta.v3Offsets == nil {
		return fmt.Errorf("v3 key entries: block offsets not scanned")
	}

	f, err := os.Open(mdict.filePath)
	if err != nil {
		return err
	}
	defer f.Close()

	if _, err := f.Seek(mdict.meta.v3Offsets.keyData, io.SeekStart); err != nil {
		return fmt.Errorf("v3 keys: seek: %w", err)
	}

	var numBlocks uint32
	if err := binary.Read(f, binary.BigEndian, &numBlocks); err != nil {
		return fmt.Errorf("v3 keys: read numBlocks: %w", err)
	}
	var totalSize uint64 // total decompressed size; we don't need it
	if err := binary.Read(f, binary.BigEndian, &totalSize); err != nil {
		return fmt.Errorf("v3 keys: read totalSize: %w", err)
	}

	keyBlockData := &mdictKeyBlockData{
		keyEntries: make([]*MDictKeywordEntry, 0),
	}

	for i := uint32(0); i < numBlocks; i++ {
		var decompSize uint32
		var compSize uint32
		if err := binary.Read(f, binary.BigEndian, &decompSize); err != nil {
			return fmt.Errorf("v3 keys: block %d: read decompSize: %w", i, err)
		}
		if err := binary.Read(f, binary.BigEndian, &compSize); err != nil {
			return fmt.Errorf("v3 keys: block %d: read compSize: %w", i, err)
		}
		blockData := make([]byte, compSize)
		if _, err := io.ReadFull(f, blockData); err != nil {
			return fmt.Errorf("v3 keys: block %d: read data: %w", i, err)
		}

		decompressed, err := mdict.decodeBlockV3(blockData, int(decompSize))
		if err != nil {
			return fmt.Errorf("v3 keys: block %d: decode: %w", i, err)
		}

		splitKeys := mdict.splitKeyBlock(decompressed)
		keyBlockData.keyEntries = append(keyBlockData.keyEntries, splitKeys...)
		keyBlockData.keyEntriesSize += int64(len(splitKeys))
	}

	// Set record end offsets: for v3, each key's record extends from its
	// RecordStartOffset to the next key's RecordStartOffset (or end of the
	// record data). We can't know the total record size here without reading
	// the record blocks, so we compute end offsets lazily during lookup.
	n := len(keyBlockData.keyEntries)
	for i := 0; i < n-1; i++ {
		keyBlockData.keyEntries[i].RecordEndOffset = keyBlockData.keyEntries[i+1].RecordStartOffset
	}
	if n > 0 {
		// The last entry's end offset is unknown until we read the record
		// blocks; set it to -1 as a sentinel meaning "to end of block".
		keyBlockData.keyEntries[n-1].RecordEndOffset = -1
	}

	mdict.keyBlockData = keyBlockData
	return nil
}

// ── the v3 record-block table ────────────────────────────────────────────
//
// A v3 record section is a chain, not an index:
//
//	[4-byte BE] number of record blocks
//	[8-byte BE] total decompressed size
//	For each block:
//	  [4-byte BE] decompressed size
//	  [4-byte BE] compressed size
//	  [compressed_size bytes] block data (decoded via decodeBlockV3)
//
// Nothing in that layout says where block k begins - the only way to know is to
// add up the k-1 headers before it. locateByKeywordEntryV3 used to do exactly
// that on EVERY lookup, reading each preceding block's payload in full merely to
// get past it: O(N*B) reads and N whole-block decompressions for an N-entry
// ingest. On a 2.9M-entry dictionary that is 66.9 GB of read() against a 26.6 MB
// file, and it does not finish.
//
// The chain is walked ONCE instead, seeking over payloads rather than reading
// them (one 8-byte header read per block, no decompression), and the result is
// this table. Every later lookup is a binary search over decompAcc plus one
// block decompression, which the shared FIFO cache then usually serves for
// free.
type v3RecordBlock struct {
	fileOff    int64 // where this block's compressed payload starts in the file
	compSize   int64 // bytes on disk
	decompSize int64 // bytes once decoded
	decompAcc  int64 // decompressed offset of this block's first byte
}

// recordBlockTableV3 returns the table, building it on first use. Safe for
// concurrent callers: the whole build is under v3RecMu, so a second caller
// waits rather than walking the chain again.
func (mdict *MdictBase) recordBlockTableV3() ([]v3RecordBlock, error) {
	mdict.v3RecMu.Lock()
	defer mdict.v3RecMu.Unlock()
	if mdict.v3RecBlocks != nil {
		return mdict.v3RecBlocks, nil
	}
	if mdict.meta.v3Offsets == nil {
		return nil, fmt.Errorf("v3 record: block offsets not scanned")
	}

	f, err := os.Open(mdict.filePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	fileSize := st.Size()

	pos := mdict.meta.v3Offsets.recordData
	var head [12]byte
	if _, err := f.ReadAt(head[:], pos); err != nil {
		return nil, fmt.Errorf("v3 record: read section header: %w", err)
	}
	numBlocks := binary.BigEndian.Uint32(head[0:4])
	// The 8-byte total decompressed size is redundant with the table for
	// LOCATION, but it is the only cross-check the format offers against a
	// truncated or mis-declared chain, so it is kept and compared after the
	// walk. Compared, not enforced: a writer that got this field wrong would
	// otherwise turn a dictionary that reads perfectly well into a hard
	// failure, and every block header the walk touches is already validated
	// against the file's own size. A mismatch is a note in the log for the
	// person diagnosing records that resolve to nothing near the end.
	totalSize := int64(binary.BigEndian.Uint64(head[4:12]))
	pos += 12

	// The count comes from the file, so it is hostile until proven otherwise:
	// preallocating for it unchecked would let a corrupt uint32 ask for 128 GB.
	// Every block costs at least its 8-byte header, so the file size is a hard
	// upper bound on how many there can be.
	if maxBlocks := (fileSize - pos) / 8; int64(numBlocks) > maxBlocks {
		return nil, fmt.Errorf("v3 record: block count %d exceeds what a %d-byte file can hold", numBlocks, fileSize)
	}
	tbl := make([]v3RecordBlock, 0, numBlocks)

	var acc int64
	var hdr [8]byte
	for i := uint32(0); i < numBlocks; i++ {
		if _, err := f.ReadAt(hdr[:], pos); err != nil {
			return nil, fmt.Errorf("v3 record: block %d: read header: %w", i, err)
		}
		decompSize := int64(binary.BigEndian.Uint32(hdr[0:4]))
		compSize := int64(binary.BigEndian.Uint32(hdr[4:8]))
		pos += 8
		if compSize > fileSize-pos {
			return nil, fmt.Errorf("v3 record: block %d: compressed size %d runs past end of file", i, compSize)
		}
		tbl = append(tbl, v3RecordBlock{
			fileOff: pos, compSize: compSize, decompSize: decompSize, decompAcc: acc,
		})
		pos += compSize
		acc += decompSize
	}

	if totalSize != 0 && acc != totalSize {
		logx.V("v3 record: %s declares %d decompressed bytes, %d blocks account for %d",
			mdict.filePath, totalSize, len(tbl), acc)
	}

	mdict.v3RecBlocks = tbl
	return tbl, nil
}

// blockV3 decodes one record block, through the same bounded FIFO cache the
// v1/v2 path uses. A sequential ingest scan asks for the same block for
// thousands of consecutive entries, so the cache is what turns N decompressions
// into one per block.
func (mdict *MdictBase) blockV3(b *v3RecordBlock) ([]byte, error) {
	return mdict.cachedBlock(b.fileOff, func() ([]byte, error) {
		f, err := os.Open(mdict.filePath)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		raw := make([]byte, b.compSize)
		if _, err := f.ReadAt(raw, b.fileOff); err != nil {
			return nil, fmt.Errorf("v3 record: read block at %d: %w", b.fileOff, err)
		}
		return mdict.decodeBlockV3(raw, int(b.decompSize))
	})
}

// locateByKeywordEntryV3 returns the record bytes for one keyword entry: a
// binary search for the block holding RecordStartOffset, then that block.
//
// RecordEndOffset carries two spellings of "unknown" - 0 from an entry that was
// never given an end, negative from readKeyEntriesV3's explicit sentinel for the
// last entry - and both mean "to the end of the containing block".
func (mdict *MdictBase) locateByKeywordEntryV3(entry *MDictKeywordEntry) ([]byte, error) {
	tbl, err := mdict.recordBlockTableV3()
	if err != nil {
		return nil, err
	}
	if len(tbl) == 0 {
		return nil, fmt.Errorf("v3 record: dictionary has no record blocks")
	}

	start := entry.RecordStartOffset
	if start < 0 {
		return nil, fmt.Errorf("v3 record: negative start offset %d", start)
	}
	// The same predicate the old linear walk used - first block whose
	// decompressed range ends past the offset - decided in O(log B).
	i := sort.Search(len(tbl), func(i int) bool {
		return tbl[i].decompAcc+tbl[i].decompSize > start
	})
	if i == len(tbl) {
		return nil, fmt.Errorf("v3 record: no block contains offset %d", start)
	}

	last := tbl[len(tbl)-1]
	total := last.decompAcc + last.decompSize
	end := entry.RecordEndOffset
	if end <= 0 || end > total || end < start {
		end = tbl[i].decompAcc + tbl[i].decompSize
	}

	// The common case by far: the record lives inside one block, and the answer
	// is a sub-slice of the cached block with no copy.
	if end <= tbl[i].decompAcc+tbl[i].decompSize {
		blk, err := mdict.blockV3(&tbl[i])
		if err != nil {
			return nil, err
		}
		lo, hi := start-tbl[i].decompAcc, end-tbl[i].decompAcc
		if hi > int64(len(blk)) {
			hi = int64(len(blk))
		}
		if lo < 0 || lo > hi {
			return nil, fmt.Errorf("v3 record: offset %d out of range in block %d", start, i)
		}
		return blk[lo:hi], nil
	}

	// A record straddling a block boundary. The old code silently truncated it
	// at the block edge; the table makes stitching it back together trivial, so
	// it is stitched. Bounded by `end`, which is already clamped to the total.
	out := make([]byte, 0, end-start)
	for j := i; j < len(tbl) && tbl[j].decompAcc < end; j++ {
		blk, err := mdict.blockV3(&tbl[j])
		if err != nil {
			return nil, err
		}
		lo := int64(0)
		if j == i {
			lo = start - tbl[j].decompAcc
		}
		hi := end - tbl[j].decompAcc
		if hi > int64(len(blk)) {
			hi = int64(len(blk))
		}
		if lo < 0 || lo > hi {
			return nil, fmt.Errorf("v3 record: offset %d out of range in block %d", start, j)
		}
		out = append(out, blk[lo:hi]...)
	}
	return out, nil
}
