// Copyright (C) 2026 glowinthedark
//
// extends the medict-derived MDX parser in this package.
//
// SPDX-License-Identifier: GPL-3.0-or-later

package go_mdict

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/wuweidict/wudict/internal/limits"
	"github.com/wuweidict/wudict/internal/logx"
)

// v3 section types (big-endian uint32 in the section directory).
const (
	v3BlockTypeRecordData  = 0x01000000
	v3BlockTypeRecordIndex = 0x02000000
	v3BlockTypeKeyData     = 0x03000000
	v3BlockTypeKeyIndex    = 0x04000000
)

// v3BlockOffsets locates the v3 sections. The four offsets are where each
// section's body starts, just after its 12-byte directory entry; sections
// holds what scanV3Blocks learned about each body, keyed by type.
type v3BlockOffsets struct {
	recordData  int64
	keyData     int64
	recordIndex int64
	keyIndex    int64

	sections map[uint32]*v3Section
}

// v3Section is one section body (docs/mdict/FORMAT.md §4.1):
//
//	[block count: u32][data size: u64]
//	data:    block count × [decompressed size: u32][compressed size: u32][block]
//	trailer: [decompressed size: u32][compressed size: u32][block holding XML]
//
// The data size is the byte length of the framed data blocks, not a
// decompressed total.
type v3Section struct {
	body    int64 // file offset of [block count]
	end     int64 // file offset just past the section
	count   uint32
	dataLen int64
	attrs   map[string]string // the trailer's XML attributes; nil when unreadable
}

// dataStart is the file offset of the section's first framed block.
func (s *v3Section) dataStart() int64 { return s.body + 12 }

// scanV3Blocks reads the section directory that follows the header,
//
//	[type: u32][section size: u64][section body]   … to the end of the file
//
// then each section's block count and data size, and its trailer, whose XML
// names the encodings (<KeyData encoding=… keyCount=…/>, <RecordData
// encoding=…/>).
func (mdict *MdictBase) scanV3Blocks() error {
	f, err := os.Open(mdict.filePath)
	if err != nil {
		return err
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return fmt.Errorf("v3 scan: stat: %w", err)
	}
	fileSize := fi.Size()

	// The trailers are encrypted blocks, so the key comes first.
	if mdict.meta.uuid != "" && mdict.meta.encryptedKey == nil {
		mdict.meta.encryptedKey = deriveV3EncryptedKey(mdict.meta.uuid)
	}

	offsets := v3BlockOffsets{sections: make(map[uint32]*v3Section, 4)}
	off := mdict.meta.keyBlockMetaStartOffset
	var dir [12]byte
	for off < fileSize {
		if _, err := f.ReadAt(dir[:], off); err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("v3 scan: read directory: %w", err)
		}
		blockType := binary.BigEndian.Uint32(dir[0:4])
		blockSize := binary.BigEndian.Uint64(dir[4:12])
		body := off + 12

		// A declared size is the file's word against itself: one that wraps
		// int64 negative would send the scan backwards onto this same entry,
		// reading it again forever.
		if blockSize > uint64(fileSize) || body+int64(blockSize) > fileSize {
			return fmt.Errorf("v3 scan: section size %d at offset %d exceeds file (%d bytes)", blockSize, body, fileSize)
		}

		switch blockType {
		case v3BlockTypeRecordData:
			offsets.recordData = body
		case v3BlockTypeRecordIndex:
			offsets.recordIndex = body
		case v3BlockTypeKeyData:
			offsets.keyData = body
		case v3BlockTypeKeyIndex:
			offsets.keyIndex = body
		default:
			return fmt.Errorf("v3 scan: unknown section type 0x%08x at offset %d", blockType, body)
		}
		offsets.sections[blockType] = mdict.readV3Section(f, body, body+int64(blockSize))
		off = body + int64(blockSize)
	}

	if s := offsets.sections[v3BlockTypeRecordData]; s != nil {
		if enc, ok := encodingOf(s.attrs["encoding"]); ok {
			mdict.meta.encoding = enc
		}
	}
	if s := offsets.sections[v3BlockTypeKeyData]; s != nil {
		if enc, ok := encodingOf(s.attrs["encoding"]); ok {
			mdict.meta.keyEncoding = enc
		}
	}

	mdict.meta.v3Offsets = &offsets
	return nil
}

// readV3Section reads a section body's counts and its trailer. Whatever it
// cannot read is left zero: the data blocks are still walked one by one, and
// the encodings keep their defaults.
func (mdict *MdictBase) readV3Section(f *os.File, body, end int64) *v3Section {
	s := &v3Section{body: body, end: end}
	var head [12]byte
	if end-body < 12 {
		return s
	}
	if _, err := f.ReadAt(head[:], body); err != nil {
		return s
	}
	s.count = binary.BigEndian.Uint32(head[0:4])
	s.dataLen = int64(binary.BigEndian.Uint64(head[4:12]))
	if s.dataLen < 0 || s.dataLen > end-s.dataStart() {
		s.dataLen = 0
		return s
	}

	xml, err := mdict.readV3Framed(f, s.dataStart()+s.dataLen, end)
	if err != nil {
		logx.V("%s: v3 section trailer at %d unreadable: %v", filepath.Base(mdict.filePath), s.dataStart()+s.dataLen, err)
		return s
	}
	s.attrs = make(map[string]string)
	for _, m := range headerAttrRe.FindAllStringSubmatch(string(xml), -1) {
		s.attrs[m[1]] = unescapeEntities(m[2] + m[3])
	}
	return s
}

// readV3Framed reads and decodes one framed block, [decompressed size: u32]
// [compressed size: u32][block], that must end by limit.
func (mdict *MdictBase) readV3Framed(f *os.File, at, limit int64) ([]byte, error) {
	var hdr [8]byte
	if _, err := f.ReadAt(hdr[:], at); err != nil {
		return nil, err
	}
	decompSize := int(binary.BigEndian.Uint32(hdr[0:4]))
	compSize := int64(binary.BigEndian.Uint32(hdr[4:8]))
	if compSize > limit-at-8 || compSize > maxLZOBlock() {
		return nil, fmt.Errorf("block of %d bytes overruns its section", compSize)
	}
	raw := make([]byte, compSize)
	if _, err := f.ReadAt(raw, at+8); err != nil {
		return nil, err
	}
	return mdict.decodeBlock(raw, decompSize)
}

// readKeyEntriesV3 reads every key from the KeyData section. Decompressed, a
// key block is [record offset: u64][key][NUL unit] × entries, split by the
// same splitKeyBlock as v1/v2. As there, a block that cannot be decoded costs
// its own keys only.
func (mdict *MdictBase) readKeyEntriesV3() error {
	if mdict.meta.v3Offsets == nil {
		return fmt.Errorf("v3 key entries: sections not scanned")
	}

	f, err := os.Open(mdict.filePath)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}

	pos := mdict.meta.v3Offsets.keyData
	var head [12]byte
	if _, err := f.ReadAt(head[:], pos); err != nil {
		return fmt.Errorf("v3 keys: read section header: %w", err)
	}
	numBlocks := binary.BigEndian.Uint32(head[0:4])
	pos += 12
	limit := st.Size()
	if s := mdict.meta.v3Offsets.sections[v3BlockTypeKeyData]; s != nil && s.dataLen > 0 {
		limit = s.dataStart() + s.dataLen
	}

	var declared int64
	if s := mdict.meta.v3Offsets.sections[v3BlockTypeKeyData]; s != nil {
		declared = int64(leadingInt(s.attrs["keyCount"]))
	}
	entries := make([]*MDictKeywordEntry, 0, keyCountHint(declared, maxLZOBlock(), mdict.meta.numberWidth))
	var gaps []int
	failed := 0
	var firstErr error
	var hdr [8]byte
	for i := uint32(0); i < numBlocks; i++ {
		if _, err := f.ReadAt(hdr[:], pos); err != nil {
			return fmt.Errorf("v3 keys: block %d: read header: %w", i, err)
		}
		decompSize := binary.BigEndian.Uint32(hdr[0:4])
		compSize := int64(binary.BigEndian.Uint32(hdr[4:8]))
		pos += 8
		// compSize is the file's word for how much to allocate and where the
		// next block starts: past the section there is no next block to find.
		if compSize > limit-pos || compSize > maxLZOBlock() {
			err := fmt.Errorf("block %d of %d bytes overruns the key section", i, compSize)
			if compSize > maxLZOBlock() {
				err = fmt.Errorf("block %d: %w", i, limits.Exceeded(fmt.Sprintf("a compressed size of %d bytes", compSize), "MAX_MDICT_BLOCK", maxLZOBlock()))
			}
			if len(entries) == 0 {
				return fmt.Errorf("v3 keys: %w", err)
			}
			failed += int(numBlocks - i)
			if firstErr == nil {
				firstErr = err
			}
			break
		}
		blockData := make([]byte, compSize)
		if _, err := f.ReadAt(blockData, pos); err != nil {
			return fmt.Errorf("v3 keys: block %d: read data: %w", i, err)
		}
		pos += compSize

		decompressed, err := mdict.decodeBlock(blockData, int(decompSize))
		if err != nil {
			failed++
			if firstErr == nil {
				firstErr = fmt.Errorf("key block %d: %w", i, err)
			}
			gaps = append(gaps, len(entries)-1)
			continue
		}
		entries = mdict.appendKeyBlock(entries, decompressed, 0)
	}
	if len(entries) == 0 && failed > 0 {
		return fmt.Errorf("v3 keys: %w", firstErr)
	}

	// Each key's record runs to the next key's start. The last one's end, and
	// the end of the last key before an unreadable block, are unknown: they
	// get v3's "to end of block" sentinel, which locateByKeywordEntryV3
	// resolves.
	chainRecordEnds(entries, -1)
	for _, g := range gaps {
		if g >= 0 {
			entries[g].RecordEndOffset = -1
		}
	}
	if failed > 0 {
		logx.Warn("%s: %d of %d key blocks cannot be read (%v); the headwords in them are missing",
			filepath.Base(mdict.filePath), failed, numBlocks, firstErr)
	}

	mdict.keyBlockData = &mdictKeyBlockData{
		keyEntries:     entries,
		keyEntriesSize: int64(len(entries)),
	}
	return nil
}

// ── the v3 record-block table ────────────────────────────────────────────
//
// Record lookups need, per block, where it starts in the file and which
// decompressed range it covers. The RecordIndex section holds exactly that,
// one [framed size: u64][decompressed size: u64] per RecordData block, and is
// how MDict opens the file: one small read. When it is missing or does not
// agree with the RecordData section, the data blocks are walked instead, once,
// seeking over payloads (one 8-byte header read per block, no decompression).
//
// Either way the table is built once. Recomputing a block's position per
// lookup cost O(N*B) reads for an N-entry ingest: on a 2.9M-entry dictionary,
// 66.9 GB of read() against a 26.6 MB file (docs.local/PERF.md §9). Every
// later lookup is a binary search over decompAcc plus one block
// decompression, which the shared FIFO cache then usually serves for free.

type v3RecordBlock struct {
	fileOff    int64 // where this block's compressed payload starts in the file
	compSize   int64 // bytes on disk
	decompSize int64 // bytes once decoded
	decompAcc  int64 // decompressed offset of this block's first byte
}

// recordBlockTableV3 returns the table, building it on first use. Safe for
// concurrent callers: the whole build is under v3RecMu, so a second caller
// waits rather than building it again.
func (mdict *MdictBase) recordBlockTableV3() ([]v3RecordBlock, error) {
	mdict.v3RecMu.Lock()
	defer mdict.v3RecMu.Unlock()
	if mdict.v3RecBlocks != nil {
		return mdict.v3RecBlocks, nil
	}
	if mdict.meta.v3Offsets == nil {
		return nil, fmt.Errorf("v3 record: sections not scanned")
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

	tbl, err := mdict.recordTableFromIndex(f)
	if err != nil {
		logx.V("%s: v3 record index not used (%v); walking the record blocks", filepath.Base(mdict.filePath), err)
		tbl = nil
	}
	if tbl == nil {
		if tbl, err = mdict.recordTableByWalk(f, st.Size()); err != nil {
			return nil, err
		}
	}
	mdict.v3RecBlocks = tbl
	return tbl, nil
}

// recordTableFromIndex builds the table from the RecordIndex section. A nil
// table with a nil error means there is no index to use.
func (mdict *MdictBase) recordTableFromIndex(f *os.File) ([]v3RecordBlock, error) {
	secs := mdict.meta.v3Offsets.sections
	data, index := secs[v3BlockTypeRecordData], secs[v3BlockTypeRecordIndex]
	if data == nil || index == nil || index.count != 1 || data.dataLen == 0 {
		return nil, nil
	}
	raw, err := mdict.readV3Framed(f, index.dataStart(), index.end)
	if err != nil {
		return nil, err
	}
	if uint64(len(raw)) != uint64(data.count)*16 {
		return nil, fmt.Errorf("index holds %d bytes for %d blocks", len(raw), data.count)
	}

	tbl := make([]v3RecordBlock, 0, data.count)
	pos, end := data.dataStart(), data.dataStart()+data.dataLen
	var acc int64
	for i := 0; i < int(data.count); i++ {
		framed := int64(binary.BigEndian.Uint64(raw[i*16:]))
		decompSize := int64(binary.BigEndian.Uint64(raw[i*16+8:]))
		if framed < 8 || framed > end-pos || decompSize < 0 {
			return nil, fmt.Errorf("index entry %d does not fit the record section", i)
		}
		tbl = append(tbl, v3RecordBlock{
			fileOff: pos + 8, compSize: framed - 8, decompSize: decompSize, decompAcc: acc,
		})
		pos += framed
		acc += decompSize
	}
	if pos != end {
		return nil, fmt.Errorf("index covers %d of the %d bytes of record blocks", pos-data.dataStart(), data.dataLen)
	}
	return tbl, nil
}

// recordTableByWalk builds the table by walking the RecordData blocks.
func (mdict *MdictBase) recordTableByWalk(f *os.File, fileSize int64) ([]v3RecordBlock, error) {
	pos := mdict.meta.v3Offsets.recordData
	var head [12]byte
	if _, err := f.ReadAt(head[:], pos); err != nil {
		return nil, fmt.Errorf("v3 record: read section header: %w", err)
	}
	numBlocks := binary.BigEndian.Uint32(head[0:4])
	dataLen := int64(binary.BigEndian.Uint64(head[4:12]))
	pos += 12
	start := pos

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

	// The data size is the only cross-check the section offers against a
	// truncated or mis-declared chain. Compared, not enforced: every block
	// header the walk touched was validated against the file's own size, and a
	// writer that got the field wrong must not turn a readable dictionary into
	// a hard failure.
	if dataLen != 0 && pos-start != dataLen {
		logx.V("v3 record: %s declares %d bytes of record blocks, %d blocks span %d",
			mdict.filePath, dataLen, len(tbl), pos-start)
	}
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
		return mdict.decodeBlock(raw, int(b.decompSize))
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
	// The first block whose decompressed range ends past the offset, decided
	// in O(log B).
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

	// A record straddling a block boundary is stitched back together, not
	// truncated at the block edge: the table makes that trivial. Bounded by
	// `end`, which is already clamped to the total.
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
