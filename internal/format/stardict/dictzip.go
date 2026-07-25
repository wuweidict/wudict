// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package stardict

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"fmt"
	"io"
	"sync"
)

// dzReader provides random access into a dictzip (.dict.dz) file: a gzip
// member whose FEXTRA 'RA' subfield lists the compressed size of each
// fixed-length chunk, letting any (offset,size) range be served by
// inflating only the chunks that cover it.
type dzReader struct {
	ra        io.ReaderAt
	chunkLen  int     // uncompressed bytes per chunk
	offsets   []int64 // absolute offset of each compressed chunk
	sizes     []int   // compressed size of each chunk
	mu        sync.Mutex
	cache     map[int][]byte // chunk index -> decompressed bytes
	cacheKeys []int
}

const dzCacheSize = 8

// newDzReader parses the gzip/dictzip header of ra.
func newDzReader(ra io.ReaderAt, fileSize int64) (*dzReader, error) {
	head := make([]byte, min(int(fileSize), 1<<16))
	if _, err := ra.ReadAt(head, 0); err != nil && err != io.EOF {
		return nil, err
	}
	if len(head) < 12 || head[0] != 0x1f || head[1] != 0x8b {
		return nil, fmt.Errorf("not a gzip file")
	}
	if head[2] != 8 {
		return nil, fmt.Errorf("unsupported gzip method %d", head[2])
	}
	flg := head[3]
	if flg&0x04 == 0 { // FEXTRA
		return nil, fmt.Errorf("gzip file lacks dictzip extra field")
	}
	pos := 10
	xlen := int(binary.LittleEndian.Uint16(head[pos:]))
	pos += 2
	extra := head[pos : pos+xlen]
	pos += xlen

	d := &dzReader{ra: ra, cache: map[int][]byte{}}
	found := false
	for len(extra) >= 4 {
		si1, si2 := extra[0], extra[1]
		slen := int(binary.LittleEndian.Uint16(extra[2:]))
		sub := extra[4 : 4+slen]
		if si1 == 'R' && si2 == 'A' {
			ver := binary.LittleEndian.Uint16(sub[0:])
			if ver != 1 {
				return nil, fmt.Errorf("unsupported dictzip version %d", ver)
			}
			d.chunkLen = int(binary.LittleEndian.Uint16(sub[2:]))
			chcnt := int(binary.LittleEndian.Uint16(sub[4:]))
			d.sizes = make([]int, chcnt)
			for i := 0; i < chcnt; i++ {
				d.sizes[i] = int(binary.LittleEndian.Uint16(sub[6+2*i:]))
			}
			found = true
		}
		extra = extra[4+slen:]
	}
	if !found || d.chunkLen == 0 {
		return nil, fmt.Errorf("dictzip RA field missing or empty")
	}
	if flg&0x08 != 0 { // FNAME: NUL-terminated
		for pos < len(head) && head[pos] != 0 {
			pos++
		}
		pos++
	}
	if flg&0x10 != 0 { // FCOMMENT
		for pos < len(head) && head[pos] != 0 {
			pos++
		}
		pos++
	}
	if flg&0x02 != 0 { // FHCRC
		pos += 2
	}
	d.offsets = make([]int64, len(d.sizes))
	off := int64(pos)
	for i, sz := range d.sizes {
		d.offsets[i] = off
		off += int64(sz)
	}
	return d, nil
}

// readRange returns size uncompressed bytes starting at offset.
func (d *dzReader) readRange(offset int64, size int) ([]byte, error) {
	if size == 0 {
		return nil, nil
	}
	first := int(offset) / d.chunkLen
	last := int(offset+int64(size)-1) / d.chunkLen
	if first >= len(d.offsets) {
		return nil, io.ErrUnexpectedEOF
	}
	var buf bytes.Buffer
	for i := first; i <= last && i < len(d.offsets); i++ {
		chunk, err := d.chunk(i)
		if err != nil {
			return nil, err
		}
		buf.Write(chunk)
	}
	start := int(offset) - first*d.chunkLen
	if start+size > buf.Len() {
		return nil, io.ErrUnexpectedEOF
	}
	return buf.Bytes()[start : start+size], nil
}

// chunk inflates one chunk (dictzip chunks start at deflate full-flush
// boundaries, so each is independently decompressible).
func (d *dzReader) chunk(i int) ([]byte, error) {
	d.mu.Lock()
	if c, ok := d.cache[i]; ok {
		d.mu.Unlock()
		return c, nil
	}
	d.mu.Unlock()

	comp := make([]byte, d.sizes[i])
	if _, err := d.ra.ReadAt(comp, d.offsets[i]); err != nil && err != io.EOF {
		return nil, err
	}
	fr := flate.NewReader(bytes.NewReader(comp))
	out := make([]byte, d.chunkLen)
	n, err := io.ReadFull(fr, out)
	// the final chunk is shorter; a chunk stream may also end without a
	// terminating block — both surface as EOF errors after valid data
	if err != nil && n == 0 {
		return nil, fmt.Errorf("dictzip chunk %d: %w", i, err)
	}
	out = out[:n]

	d.mu.Lock()
	d.cache[i] = out
	d.cacheKeys = append(d.cacheKeys, i)
	if len(d.cacheKeys) > dzCacheSize {
		delete(d.cache, d.cacheKeys[0])
		d.cacheKeys = d.cacheKeys[1:]
	}
	d.mu.Unlock()
	return out, nil
}
