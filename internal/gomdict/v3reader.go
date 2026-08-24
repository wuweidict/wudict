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

// locateByKeywordEntryV3 looks up the record bytes for a single keyword entry
// by scanning the v3 record-data blocks.
//
// Record data block layout (same per-block structure as key data):
//
//	[4-byte BE] number of record blocks
//	[8-byte BE] total decompressed size (unused)
//	For each block:
//	  [4-byte BE] decompressed size
//	  [4-byte BE] compressed size
//	  [compressed_size bytes] block data (decoded via decodeBlockV3)
//
// We walk the blocks tracking the cumulative decompressed offset until we
// find the block containing the entry's RecordStartOffset, then slice the
// record bytes out of the decompressed block.
func (mdict *MdictBase) locateByKeywordEntryV3(entry *MDictKeywordEntry) ([]byte, error) {
	if mdict.meta.v3Offsets == nil {
		return nil, fmt.Errorf("v3 record: block offsets not scanned")
	}

	f, err := os.Open(mdict.filePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	if _, err := f.Seek(mdict.meta.v3Offsets.recordData, io.SeekStart); err != nil {
		return nil, fmt.Errorf("v3 record: seek: %w", err)
	}

	var numBlocks uint32
	if err := binary.Read(f, binary.BigEndian, &numBlocks); err != nil {
		return nil, fmt.Errorf("v3 record: read numBlocks: %w", err)
	}
	var totalSize uint64
	if err := binary.Read(f, binary.BigEndian, &totalSize); err != nil {
		return nil, fmt.Errorf("v3 record: read totalSize: %w", err)
	}

	var decompressedOffset int64
	var decompressed []byte
	var foundBlock bool

	for i := uint32(0); i < numBlocks; i++ {
		var decompSize uint32
		var compSize uint32
		if err := binary.Read(f, binary.BigEndian, &decompSize); err != nil {
			return nil, fmt.Errorf("v3 record: block %d: read decompSize: %w", i, err)
		}
		if err := binary.Read(f, binary.BigEndian, &compSize); err != nil {
			return nil, fmt.Errorf("v3 record: block %d: read compSize: %w", i, err)
		}
		blockData := make([]byte, compSize)
		if _, err := io.ReadFull(f, blockData); err != nil {
			return nil, fmt.Errorf("v3 record: block %d: read data: %w", i, err)
		}

		if decompressedOffset+int64(decompSize) > entry.RecordStartOffset {
			// This block contains the record.
			out, err := mdict.decodeBlockV3(blockData, int(decompSize))
			if err != nil {
				return nil, fmt.Errorf("v3 record: block %d: decode: %w", i, err)
			}
			decompressed = out
			foundBlock = true
			break
		}
		decompressedOffset += int64(decompSize)
	}

	if !foundBlock {
		return nil, fmt.Errorf("v3 record: no block contains offset %d", entry.RecordStartOffset)
	}

	start := entry.RecordStartOffset - decompressedOffset
	end := entry.RecordEndOffset - decompressedOffset
	if end < 0 || end > int64(len(decompressed)) {
		end = int64(len(decompressed))
	}
	if start < 0 || start > int64(len(decompressed)) {
		return nil, fmt.Errorf("v3 record: start offset %d out of range [0,%d]", start, len(decompressed))
	}

	return decompressed[start:end], nil
}
