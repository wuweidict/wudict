// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Package slob implements the Aard2 .slob container (direct backend +
// ingest reader). Binary layout ported from pyglossary/pyglossary/slob
// (big-endian throughout):
//
//	header:  magic "!-1SLOB\x1f", uuid[16], enc(u8-str), compression(tiny),
//	         tags u8×{tiny,tiny}, content-types u8×{u16-str},
//	         blob_count u32, store_offset u64, file_size u64
//	refs:    count u32, pos u64×count, items {key u16-str, bin u32,
//	         item u16, fragment tiny}
//	store:   count u32, pos u64×count, items {n u32, ctype_id u8×n,
//	         zlen u32, compressed bin}
//	bin:     pos u32×n, items {len u32, bytes}   (after decompression)
//
// "tiny" strings written as editable (length byte 255) are NUL-truncated.
package slob

import (
	"bytes"
	"compress/bzip2"
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/ulikunitz/xz/lzma"
)

var magic = []byte("!-1SLOB\x1f")

type ref struct {
	key      string
	bin      uint32
	item     uint16
	fragment string
}

type container struct {
	f            *os.File
	uuid         [16]byte
	encoding     string
	compression  string
	tags         map[string]string
	contentTypes []string
	blobCount    uint32
	refs         []ref

	storePos     []uint64 // store item positions
	storeDataOff int64

	mu        sync.Mutex
	binCache  map[uint32][]byte
	binOrder  []uint32
	ctidCache map[uint32][]byte // bin -> content-type ids (headers only)
}

const binCacheSize = 4

func openContainer(path string) (*container, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	c := &container{f: f, binCache: map[uint32][]byte{}}
	if err := c.parse(); err != nil {
		f.Close()
		return nil, fmt.Errorf("slob %s: %w", path, err)
	}
	return c, nil
}

func (c *container) Close() error { return c.f.Close() }

// bufReader tracks absolute position over the file for header parsing.
type bufReader struct {
	data []byte
	pos  int
}

func (b *bufReader) read(n int) ([]byte, error) {
	if b.pos+n > len(b.data) {
		return nil, io.ErrUnexpectedEOF
	}
	out := b.data[b.pos : b.pos+n]
	b.pos += n
	return out, nil
}

func (b *bufReader) u8() (byte, error) {
	d, err := b.read(1)
	if err != nil {
		return 0, err
	}
	return d[0], nil
}

func (b *bufReader) u16() (uint16, error) {
	d, err := b.read(2)
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint16(d), nil
}

func (b *bufReader) u32() (uint32, error) {
	d, err := b.read(4)
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint32(d), nil
}

func (b *bufReader) u64() (uint64, error) {
	d, err := b.read(8)
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint64(d), nil
}

// tinyText: u8 length; a length of 255 marks an editable (padded) string
// that is NUL-truncated.
func (b *bufReader) tinyText() (string, error) {
	n, err := b.u8()
	if err != nil {
		return "", err
	}
	d, err := b.read(int(n))
	if err != nil {
		return "", err
	}
	if n == 255 {
		if i := bytes.IndexByte(d, 0); i >= 0 {
			d = d[:i]
		}
	}
	return string(d), nil
}

// text: u16 length, NUL-truncated at max length.
func (b *bufReader) text() (string, error) {
	n, err := b.u16()
	if err != nil {
		return "", err
	}
	d, err := b.read(int(n))
	if err != nil {
		return "", err
	}
	if n == 0xFFFF {
		if i := bytes.IndexByte(d, 0); i >= 0 {
			d = d[:i]
		}
	}
	return string(d), nil
}

func (c *container) parse() error {
	st, err := c.f.Stat()
	if err != nil {
		return err
	}
	fileSize := st.Size()

	// Header is small; refs region follows it and ends at store_offset.
	// Read a generous fixed prefix first to locate the offsets.
	head := make([]byte, min64(int64(1<<16), fileSize))
	if _, err := io.ReadFull(c.f, head); err != nil {
		return err
	}
	br := &bufReader{data: head}

	m, err := br.read(len(magic))
	if err != nil || !bytes.Equal(m, magic) {
		return fmt.Errorf("bad magic (not a slob file)")
	}
	uuid, err := br.read(16)
	if err != nil {
		return err
	}
	copy(c.uuid[:], uuid)
	if c.encoding, err = br.tinyText(); err != nil {
		return err
	}
	if c.encoding != "utf-8" && c.encoding != "utf8" && c.encoding != "UTF-8" {
		return fmt.Errorf("unsupported encoding %q", c.encoding)
	}
	if c.compression, err = br.tinyText(); err != nil {
		return err
	}
	tagCount, err := br.u8()
	if err != nil {
		return err
	}
	c.tags = make(map[string]string, tagCount)
	for i := 0; i < int(tagCount); i++ {
		k, err := br.tinyText()
		if err != nil {
			return err
		}
		v, err := br.tinyText()
		if err != nil {
			return err
		}
		c.tags[k] = v
	}
	ctCount, err := br.u8()
	if err != nil {
		return err
	}
	for i := 0; i < int(ctCount); i++ {
		ct, err := br.text()
		if err != nil {
			return err
		}
		c.contentTypes = append(c.contentTypes, ct)
	}
	if c.blobCount, err = br.u32(); err != nil {
		return err
	}
	storeOffset, err := br.u64()
	if err != nil {
		return err
	}
	size, err := br.u64()
	if err != nil {
		return err
	}
	if int64(size) != fileSize {
		return fmt.Errorf("file size mismatch: header says %d, file is %d", size, fileSize)
	}
	refsOffset := int64(br.pos)

	if err := c.parseRefs(refsOffset, int64(storeOffset)); err != nil {
		return err
	}
	return c.parseStoreDir(int64(storeOffset), fileSize)
}

// parseRefs loads the whole refs region (refsOffset..storeOffset) into
// memory and decodes every ref.
func (c *container) parseRefs(refsOffset, storeOffset int64) error {
	if storeOffset <= refsOffset {
		return fmt.Errorf("invalid store offset")
	}
	region := make([]byte, storeOffset-refsOffset)
	if _, err := c.f.ReadAt(region, refsOffset); err != nil {
		return fmt.Errorf("reading refs: %w", err)
	}
	br := &bufReader{data: region}
	count, err := br.u32()
	if err != nil {
		return err
	}
	positions := make([]uint64, count)
	for i := range positions {
		if positions[i], err = br.u64(); err != nil {
			return err
		}
	}
	dataOff := br.pos
	c.refs = make([]ref, count)
	for i, pos := range positions {
		br.pos = dataOff + int(pos)
		var r ref
		if r.key, err = br.text(); err != nil {
			return fmt.Errorf("ref %d: %w", i, err)
		}
		if r.bin, err = br.u32(); err != nil {
			return err
		}
		if r.item, err = br.u16(); err != nil {
			return err
		}
		if r.fragment, err = br.tinyText(); err != nil {
			return err
		}
		c.refs[i] = r
	}
	return nil
}

func (c *container) parseStoreDir(storeOffset, fileSize int64) error {
	var cnt [4]byte
	if _, err := c.f.ReadAt(cnt[:], storeOffset); err != nil {
		return fmt.Errorf("reading store dir: %w", err)
	}
	count := binary.BigEndian.Uint32(cnt[:])
	posBytes := make([]byte, int64(count)*8)
	if _, err := c.f.ReadAt(posBytes, storeOffset+4); err != nil {
		return fmt.Errorf("reading store positions: %w", err)
	}
	c.storePos = make([]uint64, count)
	for i := range c.storePos {
		c.storePos[i] = binary.BigEndian.Uint64(posBytes[i*8:])
	}
	c.storeDataOff = storeOffset + 4 + int64(count)*8
	_ = fileSize
	return nil
}

// getItem returns the content type and bytes of one blob.
func (c *container) getItem(bin uint32, item uint16) (string, []byte, error) {
	if int(bin) >= len(c.storePos) {
		return "", nil, fmt.Errorf("bin %d out of range", bin)
	}
	base := c.storeDataOff + int64(c.storePos[bin])

	var hdr [4]byte
	if _, err := c.f.ReadAt(hdr[:], base); err != nil {
		return "", nil, err
	}
	itemCount := binary.BigEndian.Uint32(hdr[:])
	if uint32(item) >= itemCount {
		return "", nil, fmt.Errorf("item %d out of range (bin has %d)", item, itemCount)
	}
	ctids := make([]byte, itemCount)
	if _, err := c.f.ReadAt(ctids, base+4); err != nil {
		return "", nil, err
	}
	ctype := ""
	if int(ctids[item]) < len(c.contentTypes) {
		ctype = c.contentTypes[ctids[item]]
	}

	content, err := c.binContent(bin, base+4+int64(itemCount))
	if err != nil {
		return "", nil, err
	}
	// bin layout: u32 positions ×itemCount, then {u32 len, bytes} items
	posOff := int64(item) * 4
	if posOff+4 > int64(len(content)) {
		return "", nil, io.ErrUnexpectedEOF
	}
	itemPos := binary.BigEndian.Uint32(content[posOff:])
	dataOff := int64(itemCount)*4 + int64(itemPos)
	if dataOff+4 > int64(len(content)) {
		return "", nil, io.ErrUnexpectedEOF
	}
	n := binary.BigEndian.Uint32(content[dataOff:])
	if dataOff+4+int64(n) > int64(len(content)) {
		return "", nil, io.ErrUnexpectedEOF
	}
	return ctype, content[dataOff+4 : dataOff+4+int64(n)], nil
}

// itemContentType returns just the content type of one blob, reading
// only the bin header (no decompression); headers are cached per bin.
func (c *container) itemContentType(bin uint32, item uint16) (string, error) {
	c.mu.Lock()
	ctids, ok := c.ctidCache[bin]
	c.mu.Unlock()
	if !ok {
		if int(bin) >= len(c.storePos) {
			return "", fmt.Errorf("bin %d out of range", bin)
		}
		base := c.storeDataOff + int64(c.storePos[bin])
		var hdr [4]byte
		if _, err := c.f.ReadAt(hdr[:], base); err != nil {
			return "", err
		}
		n := binary.BigEndian.Uint32(hdr[:])
		ctids = make([]byte, n)
		if _, err := c.f.ReadAt(ctids, base+4); err != nil {
			return "", err
		}
		c.mu.Lock()
		if c.ctidCache == nil {
			c.ctidCache = map[uint32][]byte{}
		}
		c.ctidCache[bin] = ctids
		c.mu.Unlock()
	}
	if int(item) >= len(ctids) || int(ctids[item]) >= len(c.contentTypes) {
		return "", nil
	}
	return c.contentTypes[ctids[item]], nil
}

// binContent returns the decompressed bin, via a small LRU cache.
func (c *container) binContent(bin uint32, zlenOff int64) ([]byte, error) {
	c.mu.Lock()
	if data, ok := c.binCache[bin]; ok {
		c.mu.Unlock()
		return data, nil
	}
	c.mu.Unlock()

	var zl [4]byte
	if _, err := c.f.ReadAt(zl[:], zlenOff); err != nil {
		return nil, err
	}
	zlen := binary.BigEndian.Uint32(zl[:])
	compressed := make([]byte, zlen)
	if _, err := c.f.ReadAt(compressed, zlenOff+4); err != nil {
		return nil, err
	}
	data, err := decompress(c.compression, compressed)
	if err != nil {
		return nil, fmt.Errorf("bin %d: %w", bin, err)
	}

	c.mu.Lock()
	c.binCache[bin] = data
	c.binOrder = append(c.binOrder, bin)
	if len(c.binOrder) > binCacheSize {
		delete(c.binCache, c.binOrder[0])
		c.binOrder = c.binOrder[1:]
	}
	c.mu.Unlock()
	return data, nil
}

func decompress(name string, data []byte) ([]byte, error) {
	switch name {
	case "":
		return data, nil
	case "zlib":
		r, err := zlib.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		defer r.Close()
		return io.ReadAll(r)
	case "bz2":
		return io.ReadAll(bzip2.NewReader(bytes.NewReader(data)))
	case "lzma2":
		r, err := lzma.Reader2Config{DictCap: 1 << 25}.NewReader2(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		return io.ReadAll(r)
	default:
		return nil, fmt.Errorf("unsupported compression %q", name)
	}
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
