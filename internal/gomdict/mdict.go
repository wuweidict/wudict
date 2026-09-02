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
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

type Mdict struct {
	*MdictBase
	rangeTreeRoot *RecordBlockRangeTreeNode
}

func New(filename string) (*Mdict, error) {
	dictType := MdictTypeMdx
	if strings.ToLower(filepath.Ext(filename)) == ".mdd" {
		dictType = MdictTypeMdd
	}

	mdict := &Mdict{
		MdictBase: &MdictBase{
			filePath:      filename,
			fileType:      dictType,
			rangeTreeRoot: new(RecordBlockRangeTreeNode),
		},
	}
	return mdict, mdict.init()
}

func (mdict *Mdict) init() error {
	// 读取词典头
	err := mdict.readDictHeader()
	if err != nil {
		return err
	}

	if mdict.meta.version >= 3.0 {
		// v3: scan the self-describing block directory instead of the
		// fixed-layout v1/v2 key-block meta.
		return mdict.scanV3Blocks()
	}

	// 读取 key block 元信息
	err = mdict.readKeyBlockMeta()
	if err != nil {
		return err
	}

	return nil
}

// BuildIndex 构建索引
func (mdict *Mdict) BuildIndex() error {
	if mdict.meta.version >= 3.0 {
		return mdict.readKeyEntriesV3()
	}

	err := mdict.readKeyBlockInfo()
	if err != nil {
		return err
	}

	err = mdict.readKeyEntries()
	if err != nil {
		return err
	}

	err = mdict.readRecordBlockMeta()
	if err != nil {
		return err
	}

	err = mdict.readRecordBlockInfo()
	if err != nil {
		return err
	}

	mdict.buildRecordRangeTree()

	return nil
}

// BuildRecordIndex reads ONLY what is needed to fetch a record by its offsets:
// the record-block meta, the record-block table, and the range tree over it.
// It is the cheap half of BuildIndex - no key-block info, no keyword entries.
//
// Those two halves look coupled and are not. readRecordBlockMeta needs exactly
// one value from the key side,
//
//	keyBlockInfo.keyBlockEntriesStartOffset + keyBlockMeta.keyBlockDataTotalSize
//
// and keyBlockEntriesStartOffset is pure arithmetic on keyBlockMeta (see
// decodeKeyBlockInfo, which assigns it as keyBlockInfoCompressedSize +
// keyBlockInfoStartOffset). readDictHeader/readKeyBlockMeta have already run in
// init(), so that arithmetic is available here and no key block is ever
// decompressed.
//
// The saving is the whole point: the key side costs a struct and a decoded
// string PER ENTRY - hundreds of megabytes for an .mdd holding a few hundred
// thousand files - while the record side costs one small read and ~24 bytes per
// record BLOCK, of which even a 5 GB container has only tens of thousands. It
// is what makes serving a resource from a recorded (offset, size) cheap enough
// to replace opening the dictionary.
func (mdict *Mdict) BuildRecordIndex() error {
	if mdict.meta.version >= 3.0 {
		// v3 records are reached through meta.v3Offsets, which scanV3Blocks
		// already filled in during init(), and locateByKeywordEntryV3 walks the
		// block table itself on every call. Nothing to precompute.
		return nil
	}
	if mdict.keyBlockMeta == nil {
		return errors.New("mdict: record index needs the key-block meta from init()")
	}
	if mdict.keyBlockInfo == nil {
		mdict.keyBlockInfo = &mdictKeyBlockInfo{
			keyBlockEntriesStartOffset: mdict.keyBlockMeta.keyBlockInfoCompressedSize +
				mdict.keyBlockMeta.keyBlockInfoStartOffset,
		}
	}
	if err := mdict.readRecordBlockMeta(); err != nil {
		return err
	}
	if err := mdict.readRecordBlockInfo(); err != nil {
		return err
	}
	mdict.buildRecordRangeTree()
	return nil
}

// FilePath is the container this Mdict was opened from.
func (mdict *Mdict) FilePath() string { return mdict.filePath }

func (mdict *Mdict) Name() string {
	_, rawpath := filepath.Split(mdict.filePath)
	rawpath = strings.TrimRight(rawpath, ".mdx")
	if len(rawpath) > 0 {
		return rawpath
	}
	return rawpath
}

func (mdict *Mdict) Title() string {
	return mdict.meta.title

}

func (mdict *Mdict) Description() string {
	return mdict.meta.description
}
func (mdict *Mdict) GeneratedByEngineVersion() string {
	return mdict.meta.generatedByEngineVersion
}
func (mdict *Mdict) CreationDate() string {
	return mdict.meta.creationDate
}
func (mdict *Mdict) Version() string {
	return fmt.Sprintf("%f", mdict.meta.version)
}

func (mdict *Mdict) IsMDD() bool {
	return mdict.fileType == MdictTypeMdd
}

func (mdict *Mdict) IsRecordEncrypted() bool {
	return mdict.meta.encryptType == EncryptRecordEnc
}

func (mdict *Mdict) IsUTF16() bool {
	return mdict.meta.encoding == EncodingUtf16
}

// Encoding returns the dictionary's text encoding as one of the Encoding* /
// ENCODING_* constants. For non-UTF16 MDX dictionaries LocateByKeywordEntry
// returns record bytes still in this native encoding (UTF16 is decoded to
// UTF-8 internally), so callers must transcode accordingly.
func (mdict *Mdict) Encoding() int {
	return mdict.meta.encoding
}

// StyleSheet returns the raw StyleSheet header attribute (groups of three
// lines: number, begin, end), empty when the dictionary defines none.
func (mdict *Mdict) StyleSheet() string {
	return mdict.meta.stylesheet
}

func (mdict *Mdict) Lookup(word string) ([]byte, error) {
	word = strings.TrimSpace(word)
	for _, keyBlockEntry := range mdict.keyBlockData.keyEntries {
		if keyBlockEntry.KeyWord == word {
			return mdict.LocateByKeywordEntry(keyBlockEntry)
		}
	}
	return nil, fmt.Errorf("word:(%s) not found", word)
}

func (mdict *Mdict) LocateByKeywordEntry(entry *MDictKeywordEntry) ([]byte, error) {
	if entry == nil {
		return nil, errors.New("invalid mdict keyword entry")
	}
	if mdict.meta.version >= 3.0 {
		return mdict.locateByKeywordEntryV3(entry)
	}
	return mdict.MdictBase.locateByKeywordEntry(entry)
}

// LocateAt fetches a record from its recorded position, with no keyword entry
// and therefore no key index: the two numbers below are everything the record
// side consumes (see locateByKeywordEntry, which reads nothing else off the
// entry). Pair it with BuildRecordIndex to serve a resource out of an .mdd that
// was never fully opened.
//
// size < 0 means "to the end of the containing block", which is how the LAST
// entry of a container is stored - readKeyEntries only ever fills in the end
// offset of an entry's predecessor, so the final one has none. The sentinel for
// that differs between layouts (0 for v1/v2, negative for v3), so it is
// translated here rather than by every caller.
func (mdict *Mdict) LocateAt(off, size int64) ([]byte, error) {
	if off < 0 {
		return nil, errors.New("mdict: negative record offset")
	}
	entry := &MDictKeywordEntry{RecordStartOffset: off}
	switch {
	case size > 0:
		entry.RecordEndOffset = off + size
	case size == 0:
		// A genuinely empty record. Returning early matters: leaving the end
		// offset at 0 would be read as the sentinel and hand back the whole
		// block instead of nothing.
		return nil, nil
	case mdict.meta.version >= 3.0:
		entry.RecordEndOffset = -1 // locateByKeywordEntryV3 clamps a negative end
	}
	return mdict.LocateByKeywordEntry(entry)
}

func (mdict *Mdict) LocateByKeywordIndex(index *MDictKeywordIndex) ([]byte, error) {
	if index == nil {
		return nil, errors.New("invalid mdict keyword index")
	}
	return mdict.MdictBase.locateByKeywordIndex(index)

}

func (mdict *Mdict) GetKeyWordEntries() ([]*MDictKeywordEntry, error) {
	return mdict.getKeyWordEntries()
}

func (mdict *Mdict) GetKeyWordEntriesSize() int64 {
	return mdict.keyBlockData.keyEntriesSize
}

// EntryCount returns the headword count from the key-block meta, available
// after New() (readKeyBlockMeta) without the full BuildIndex - used for
// the cheap metadata probe. Zero when the layout does not expose it (v3).
func (mdict *Mdict) EntryCount() int64 {
	if mdict.keyBlockMeta != nil {
		return mdict.keyBlockMeta.entriesNum
	}
	if mdict.keyBlockData != nil {
		return int64(len(mdict.keyBlockData.keyEntries))
	}
	return 0
}

func (mdict *Mdict) KeywordEntryToIndex(item *MDictKeywordEntry) (*MDictKeywordIndex, error) {
	return mdict.MdictBase.keywordEntryToIndex(item)
}
