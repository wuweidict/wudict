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
// after New() (readKeyBlockMeta) without the full BuildIndex — used for
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
