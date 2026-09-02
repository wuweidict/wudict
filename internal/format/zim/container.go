// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Package zim implements the openZIM container (direct backend + ingest
// reader) - the format behind offline Wiktionary, Wikipedia and devdocs
// dumps. Little-endian throughout:
//
//	header (80 B): magic u32 = 0x044D495A, major u16, minor u16, uuid[16],
//	               entryCount u32, clusterCount u32, pathPtrPos u64,
//	               titlePtrPos u64, clusterPtrPos u64, mimeListPos u64,
//	               mainPage u32, layoutPage u32, checksumPos u64
//	mime list:     NUL-terminated strings; an EMPTY string terminates the list
//	path ptrs:     u64 file offset x entryCount, sorted by (namespace, path)
//	cluster ptrs:  u64 file offset x clusterCount
//	dirent:        mimetype u16, parameterLen u8, namespace u8, revision u32,
//	               then u32 redirectIndex           (mimetype == 0xFFFF)
//	               or   u32 cluster, u32 blob       (otherwise),
//	               then NUL path, NUL title, then parameterLen extra bytes
//	cluster:       info u8 (codec = b&0x0F, extended = b&0x10), then a
//	               compressed stream whose first bytes are the blob offset
//	               table: W-byte offsets (W = 8 when extended, else 4),
//	               count = offset[0]/W - 1, blobs following contiguously
//
// # Why this is written from scratch
//
// Of the four reference implementations available, pyglossary's `zimfile`,
// `zim-cgo` and GoldenDict-ng's `zim.cc` all delegate to libzim and carry no
// binary knowledge; cgo is disqualified here regardless (D4: a tag-less build
// and `-tags purego` must both work). unidict's czim is the only from-scratch
// reader, and its layout - cross-checked against real files - is what this
// follows. Two of its choices are deliberately NOT reproduced: a fixed 256-byte
// buffer per MIME string, which silently truncates and desynchronises the list
// walk, and `lzma_alone_decoder` for codec 4, where real ZIMs write an xz
// stream (magic FD 37 7A 58 5A 00) - hence ulikunitz/xz here, not xz/lzma.
//
// # The property that shapes the design
//
// The path pointer list is sorted in plain BYTE order over (namespace, path) -
// verified across all 181,966 entries of a real wiktionary. So exact and prefix
// lookup are an on-disk binary search of ~21 preads holding NO resident
// headword index, which makes ZIM the only wudict format whose direct
// ("preview", D15) backend is the right permanent answer rather than a
// compromise. See docs/FORMATS.md.
package zim

import (
	"bytes"
	"compress/bzip2"
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/klauspost/compress/zstd"
	"github.com/ulikunitz/xz"
)

const (
	headerSize = 80
	zimMagic   = 0x044D495A

	// mimeRedirect marks a dirent that points at another entry instead of at
	// a blob. 0xFFFE/0xFFFD are the deprecated linktarget/deleted markers -
	// they carry no content and are skipped everywhere.
	mimeRedirect   = 0xFFFF
	mimeLinkTarget = 0xFFFE
	mimeDeleted    = 0xFFFD

	// maxDirentSpan bounds one dirent read. Paths are normally short, but a
	// zimit web capture keys entries by full captured URL and reaches a few
	// kilobytes, so the window grows rather than truncating (czim's bug, in
	// the other list).
	maxDirentSpan = 1 << 16

	// maxMimeListSpan bounds the MIME string region.
	maxMimeListSpan = 1 << 20

	// maxClusterBytes bounds one DECOMPRESSED cluster. Every codec here can
	// be made to expand a bounded input without limit, and a cluster's
	// compressed extent is attacker-controlled in a downloaded file.
	maxClusterBytes = 256 << 20

	// clusterCacheBytes is the decompressed-cluster budget. A cluster larger
	// than the whole budget is still cached (alone), because evicting the
	// entry just inserted would make every blob read decompress again.
	clusterCacheBytes = 16 << 20
)

// dirent is one directory entry: an article, a resource, or a redirect.
type dirent struct {
	mimetype  uint16
	namespace byte
	revision  uint32
	redirect  uint32 // valid when isRedirect
	cluster   uint32
	blob      uint32
	path      string
	title     string
}

func (d dirent) isRedirect() bool { return d.mimetype == mimeRedirect }

// headword is the title when the file records one, else the path. An empty
// title means "same as the path" and is how ZIM avoids storing it twice - so
// the path is used VERBATIM, never with '_' turned back into a space, which
// would invent a headword the file does not contain.
func (d dirent) headword() string {
	if d.title != "" {
		return d.title
	}
	return d.path
}

// key is the sort key of the path pointer list: namespace byte then path
// bytes, compared as raw bytes.
func (d dirent) key() string { return string([]byte{d.namespace}) + d.path }

func searchKey(ns byte, path string) string { return string([]byte{ns}) + path }

type clusterData struct {
	data     []byte
	extended bool
}

type container struct {
	f    *os.File
	path string
	size int64

	major, minor uint16
	entryCount   uint32
	clusterCount uint32
	mainPage     uint32

	mimes    []string
	htmlMIME map[uint16]bool

	urlPtr     []uint64 // entryCount file offsets, sorted by (namespace, path)
	clusterPtr []uint64 // clusterCount+1: the tail is synthesised (see parse)

	zr *zstd.Decoder

	mu         sync.Mutex
	cache      map[uint32]*clusterData
	order      []uint32
	cacheBytes int64
}

func openContainer(path string) (*container, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	c := &container{f: f, path: path, size: st.Size(), cache: map[uint32]*clusterData{}}
	if err := c.parse(); err != nil {
		f.Close()
		return nil, fmt.Errorf("zim %s: %w", path, err)
	}
	return c, nil
}

func (c *container) Close() error {
	if c.zr != nil {
		c.zr.Close()
		c.zr = nil
	}
	return c.f.Close()
}

func (c *container) parse() error {
	if c.size < headerSize {
		return fmt.Errorf("file shorter than a ZIM header")
	}
	var h [headerSize]byte
	if _, err := c.f.ReadAt(h[:], 0); err != nil {
		return err
	}
	if binary.LittleEndian.Uint32(h[0:]) != zimMagic {
		return fmt.Errorf("bad magic (not a ZIM file)")
	}
	c.major = binary.LittleEndian.Uint16(h[4:])
	c.minor = binary.LittleEndian.Uint16(h[6:])
	if c.major < 5 || c.major > 6 {
		return fmt.Errorf("unsupported ZIM version %d.%d", c.major, c.minor)
	}
	c.entryCount = binary.LittleEndian.Uint32(h[24:])
	c.clusterCount = binary.LittleEndian.Uint32(h[28:])
	pathPtrPos := binary.LittleEndian.Uint64(h[32:])
	clusterPtrPos := binary.LittleEndian.Uint64(h[48:])
	mimeListPos := binary.LittleEndian.Uint64(h[56:])
	c.mainPage = binary.LittleEndian.Uint32(h[64:])
	checksumPos := binary.LittleEndian.Uint64(h[72:])

	if c.entryCount == 0 {
		return fmt.Errorf("empty ZIM (no entries)")
	}
	if err := c.checkSpan(pathPtrPos, uint64(c.entryCount)*8, "path pointer list"); err != nil {
		return err
	}
	if err := c.checkSpan(clusterPtrPos, uint64(c.clusterCount)*8, "cluster pointer list"); err != nil {
		return err
	}
	if err := c.checkSpan(mimeListPos, 1, "MIME list"); err != nil {
		return err
	}

	if err := c.parseMIMEList(mimeListPos, pathPtrPos); err != nil {
		return err
	}

	c.urlPtr = make([]uint64, c.entryCount)
	if err := c.readU64s(c.urlPtr, int64(pathPtrPos)); err != nil {
		return fmt.Errorf("reading path pointer list: %w", err)
	}
	for i, off := range c.urlPtr {
		if off < headerSize || off >= uint64(c.size) {
			return fmt.Errorf("path pointer %d out of range (%d)", i, off)
		}
	}

	// The format records no cluster SIZE, only a start offset; a cluster ends
	// where the next one begins. The last one therefore needs a synthetic
	// bound, and the checksum block is what follows it. A file whose header
	// checksum offset is missing or nonsense falls back to the file end,
	// which over-reads by 16 bytes at worst and never truncates a blob.
	end := checksumPos
	if end == 0 || end > uint64(c.size) {
		end = uint64(c.size)
	}
	c.clusterPtr = make([]uint64, c.clusterCount+1)
	if c.clusterCount > 0 {
		if err := c.readU64s(c.clusterPtr[:c.clusterCount], int64(clusterPtrPos)); err != nil {
			return fmt.Errorf("reading cluster pointer list: %w", err)
		}
	}
	c.clusterPtr[c.clusterCount] = end
	for i := 0; i < int(c.clusterCount); i++ {
		if c.clusterPtr[i] < headerSize || c.clusterPtr[i] >= uint64(c.size) {
			return fmt.Errorf("cluster pointer %d out of range (%d)", i, c.clusterPtr[i])
		}
	}
	return nil
}

func (c *container) checkSpan(off, n uint64, what string) error {
	if off < headerSize || off > uint64(c.size) || n > uint64(c.size)-off {
		return fmt.Errorf("%s out of range (offset %d, %d bytes, file %d)", what, off, n, c.size)
	}
	return nil
}

func (c *container) readU64s(dst []uint64, off int64) error {
	buf := make([]byte, len(dst)*8)
	if _, err := c.f.ReadAt(buf, off); err != nil {
		return err
	}
	for i := range dst {
		dst[i] = binary.LittleEndian.Uint64(buf[i*8:])
	}
	return nil
}

// parseMIMEList reads the whole string region at once and splits it, rather
// than walking it one fixed-size buffer at a time: a string longer than the
// buffer would truncate, and every following offset would then be wrong.
//
// The index of "text/html" is per-file (6, 7, 12 and 15 across one real
// corpus) and is resolved by STRING, never hardcoded. A parameterised type
// ("text/html; charset=iso-8859-1", which real wiktionaries contain) is
// matched on its type/subtype alone.
func (c *container) parseMIMEList(start, next uint64) error {
	end := next
	if end <= start || end > uint64(c.size) {
		end = uint64(c.size)
	}
	if end-start > maxMimeListSpan {
		end = start + maxMimeListSpan
	}
	buf := make([]byte, end-start)
	n, err := c.f.ReadAt(buf, int64(start))
	if n == 0 && err != nil {
		return fmt.Errorf("reading MIME list: %w", err)
	}
	buf = buf[:n]

	c.htmlMIME = map[uint16]bool{}
	for pos := 0; pos < len(buf); {
		i := bytes.IndexByte(buf[pos:], 0)
		if i < 0 {
			return fmt.Errorf("unterminated MIME list")
		}
		if i == 0 {
			break // the empty string terminates the list
		}
		s := string(buf[pos : pos+i])
		if isHTMLMIME(s) {
			c.htmlMIME[uint16(len(c.mimes))] = true
		}
		c.mimes = append(c.mimes, s)
		pos += i + 1
	}
	if len(c.mimes) == 0 {
		return fmt.Errorf("empty MIME list")
	}
	return nil
}

func mimeBase(s string) string {
	if i := strings.IndexByte(s, ';'); i >= 0 {
		s = s[:i]
	}
	return strings.ToLower(strings.TrimSpace(s))
}

func isHTMLMIME(s string) bool { return mimeBase(s) == "text/html" }

func (c *container) mimeOf(d dirent) string {
	if d.isRedirect() || int(d.mimetype) >= len(c.mimes) {
		return ""
	}
	return c.mimes[d.mimetype]
}

// contentNS is the namespace articles live in. ZIM 6.1 replaced the
// per-kind namespaces (A articles, I images, - layout) with a single content
// namespace C; the metadata namespace M is unchanged across both. This one
// function is the whole compatibility story, and every content lookup routes
// through it.
func (c *container) contentNS() byte {
	if c.major > 6 || (c.major == 6 && c.minor >= 1) {
		return 'C'
	}
	return 'A'
}

func (c *container) newNamespaces() bool { return c.contentNS() == 'C' }

// direntAt decodes the entry at a file offset, growing its read window until
// both NUL-terminated strings are inside it.
func (c *container) direntAt(off int64) (dirent, error) {
	if off < headerSize || off >= c.size {
		return dirent{}, fmt.Errorf("dirent offset %d out of range", off)
	}
	var stack [512]byte
	buf := stack[:]
	for {
		if int64(len(buf)) > c.size-off {
			buf = buf[:c.size-off]
		}
		n, err := c.f.ReadAt(buf, off)
		if n < 12 {
			if err == nil {
				err = io.ErrUnexpectedEOF
			}
			return dirent{}, err
		}
		d, ok := parseDirent(buf[:n])
		if ok {
			return d, nil
		}
		if int64(n) >= c.size-off {
			return dirent{}, fmt.Errorf("truncated dirent at %d", off)
		}
		if len(buf) >= maxDirentSpan {
			return dirent{}, fmt.Errorf("dirent at %d exceeds %d bytes", off, maxDirentSpan)
		}
		buf = make([]byte, min(len(buf)*8, maxDirentSpan))
	}
}

// parseDirent decodes one entry from a window. ok=false means the window ends
// before both strings do and must be grown - it is never an error by itself.
func parseDirent(b []byte) (dirent, bool) {
	if len(b) < 12 {
		return dirent{}, false
	}
	var d dirent
	d.mimetype = binary.LittleEndian.Uint16(b[0:])
	paramLen := int(b[2])
	d.namespace = b[3]
	d.revision = binary.LittleEndian.Uint32(b[4:])
	off := 16
	if d.isRedirect() {
		d.redirect = binary.LittleEndian.Uint32(b[8:])
		off = 12
	} else {
		if len(b) < 16 {
			return dirent{}, false
		}
		d.cluster = binary.LittleEndian.Uint32(b[8:])
		d.blob = binary.LittleEndian.Uint32(b[12:])
	}
	if off > len(b) {
		return dirent{}, false
	}
	i := bytes.IndexByte(b[off:], 0)
	if i < 0 {
		return dirent{}, false
	}
	d.path = string(b[off : off+i])
	rest := b[off+i+1:]
	j := bytes.IndexByte(rest, 0)
	if j < 0 {
		return dirent{}, false
	}
	d.title = string(rest[:j])
	// The extra parameter bytes are unused by every writer in the wild, but
	// they must be inside the window for the entry to be complete.
	if len(rest)-(j+1) < paramLen {
		return dirent{}, false
	}
	return d, true
}

// entry decodes the i-th entry of the path pointer list.
func (c *container) entry(i int) (dirent, error) {
	if i < 0 || i >= len(c.urlPtr) {
		return dirent{}, fmt.Errorf("entry %d out of range (%d entries)", i, len(c.urlPtr))
	}
	return c.direntAt(int64(c.urlPtr[i]))
}

// resolve follows a redirect chain to the entry that actually holds content.
// Real files contain cycles, so the walk is depth-bounded rather than trusting
// termination.
func (c *container) resolve(i int) (int, dirent, error) {
	for hop := 0; hop < 8; hop++ {
		d, err := c.entry(i)
		if err != nil {
			return 0, dirent{}, err
		}
		if !d.isRedirect() {
			return i, d, nil
		}
		next := int(d.redirect)
		if next == i || next < 0 || next >= len(c.urlPtr) {
			return 0, dirent{}, fmt.Errorf("entry %d: bad redirect to %d", i, next)
		}
		i = next
	}
	return 0, dirent{}, fmt.Errorf("redirect chain too long")
}

// cluster returns one decompressed cluster body (everything after the info
// byte), through a byte-bounded LRU.
func (c *container) cluster(i uint32) (*clusterData, error) {
	c.mu.Lock()
	if cd, ok := c.cache[i]; ok {
		c.touch(i)
		c.mu.Unlock()
		return cd, nil
	}
	c.mu.Unlock()

	if i >= c.clusterCount {
		return nil, fmt.Errorf("cluster %d out of range (%d clusters)", i, c.clusterCount)
	}
	start, end := c.clusterPtr[i], c.clusterPtr[i+1]
	if end <= start || end > uint64(c.size) {
		return nil, fmt.Errorf("cluster %d: bad extent %d..%d", i, start, end)
	}
	raw := make([]byte, end-start)
	if _, err := c.f.ReadAt(raw, int64(start)); err != nil {
		return nil, fmt.Errorf("cluster %d: %w", i, err)
	}
	info := raw[0]
	cd := &clusterData{extended: info&0x10 != 0}
	data, err := c.decompress(info&0x0F, raw[1:])
	if err != nil {
		return nil, fmt.Errorf("cluster %d: %w", i, err)
	}
	cd.data = data

	c.mu.Lock()
	if _, ok := c.cache[i]; !ok {
		c.cache[i] = cd
		c.order = append(c.order, i)
		c.cacheBytes += int64(len(cd.data))
		// Evict oldest first, but never the entry just inserted: one cluster
		// can exceed the whole budget, and dropping it would make every blob
		// read of that cluster decompress it again.
		for c.cacheBytes > clusterCacheBytes && len(c.order) > 1 {
			old := c.order[0]
			c.order = c.order[1:]
			if prev, ok := c.cache[old]; ok {
				c.cacheBytes -= int64(len(prev.data))
				delete(c.cache, old)
			}
		}
	}
	c.mu.Unlock()
	return cd, nil
}

// touch moves i to the back of the LRU order. Called under c.mu.
func (c *container) touch(i uint32) {
	for k, v := range c.order {
		if v == i {
			c.order = append(c.order[:k], c.order[k+1:]...)
			c.order = append(c.order, i)
			return
		}
	}
}

func (c *container) decompress(codec byte, data []byte) ([]byte, error) {
	switch codec {
	case 0, 1:
		return data, nil
	case 2:
		r, err := zlib.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		defer r.Close()
		return readAllBounded(r)
	case 3:
		return readAllBounded(bzip2.NewReader(bytes.NewReader(data)))
	case 4:
		// xz, NOT LZMA_Alone: real clusters carry the xz stream magic
		// FD 37 7A 58 5A 00. czim decodes this one with lzma_alone_decoder.
		r, err := xz.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		return readAllBounded(r)
	case 5:
		zr, err := c.zstd()
		if err != nil {
			return nil, err
		}
		return zr.DecodeAll(data, nil)
	default:
		return nil, fmt.Errorf("unsupported compression codec %d", codec)
	}
}

// zstd builds the shared decoder on first use. DecodeAll is safe for
// concurrent callers, so one decoder serves the whole container; it is built
// lazily because a file with no zstd cluster should not pay for it.
func (c *container) zstd() (*zstd.Decoder, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.zr != nil {
		return c.zr, nil
	}
	zr, err := zstd.NewReader(nil,
		zstd.WithDecoderConcurrency(1),
		zstd.WithDecoderMaxMemory(maxClusterBytes))
	if err != nil {
		return nil, err
	}
	c.zr = zr
	return zr, nil
}

func readAllBounded(r io.Reader) ([]byte, error) {
	out, err := io.ReadAll(io.LimitReader(r, maxClusterBytes+1))
	if err != nil {
		return nil, err
	}
	if len(out) > maxClusterBytes {
		return nil, fmt.Errorf("decompressed cluster exceeds %d bytes", maxClusterBytes)
	}
	return out, nil
}

// blob returns the bytes of one blob inside a cluster. The offset table lives
// INSIDE the compressed stream: W-byte offsets, and offset[0] is both the
// first blob's position and the table's own length, so it is what says how
// many blobs there are.
func (c *container) blob(cl, bl uint32) ([]byte, error) {
	cd, err := c.cluster(cl)
	if err != nil {
		return nil, err
	}
	w := 4
	if cd.extended {
		w = 8
	}
	data := cd.data
	if len(data) < w {
		return nil, fmt.Errorf("cluster %d: no offset table", cl)
	}
	first := readUint(data, 0, w)
	if first < uint64(w) || first%uint64(w) != 0 || first > uint64(len(data)) {
		return nil, fmt.Errorf("cluster %d: bad offset table header %d", cl, first)
	}
	count := first/uint64(w) - 1
	if uint64(bl) >= count {
		return nil, fmt.Errorf("cluster %d: blob %d out of range (%d blobs)", cl, bl, count)
	}
	lo := readUint(data, int(bl)*w, w)
	hi := readUint(data, (int(bl)+1)*w, w)
	if lo > hi || hi > uint64(len(data)) {
		return nil, fmt.Errorf("cluster %d blob %d: bad extent %d..%d", cl, bl, lo, hi)
	}
	return data[lo:hi], nil
}

func readUint(b []byte, off, w int) uint64 {
	if w == 8 {
		return binary.LittleEndian.Uint64(b[off:])
	}
	return uint64(binary.LittleEndian.Uint32(b[off:]))
}

// content returns the bytes of a non-redirect entry.
func (c *container) content(d dirent) ([]byte, error) {
	if d.isRedirect() {
		return nil, fmt.Errorf("entry is a redirect")
	}
	return c.blob(d.cluster, d.blob)
}
