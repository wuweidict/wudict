// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Package glass reads a Xapian "glass" database stored as one file: the
// full-text index a Kiwix archive carries as its X/fulltext/xapian entry
// (D194). It answers what a search needs - a term's posting list, a
// document's length and data, the index's metadata - and writes nothing.
//
// The layout is Xapian's own (xapian-core/backends/glass: glass_version.cc,
// glass_table.h, glass_table.cc, glass_postlist.cc; common/pack.h). A
// single-file database is a version block followed by blocks of one size that
// its six B-trees share: block n is at n*blockSize, and block 0 holds the
// version file.
//
// Every length and offset here comes out of the file, so every one is checked
// before it indexes anything: a damaged or hostile index is an error, never a
// panic or an allocation the file chose.
package glass

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// The tables, in the order the version file lists their roots (glass_defs.h).
const (
	tablePostlist = iota
	tableDocdata
	tableTermlist
	tablePosition
	tableSpelling
	tableSynonym
	numTables
)

const (
	magicLen = 14 // "\x0f\x0dXapian Glass", then a 2-byte format date

	// A block starts with: revision u32, level u8, max free u16, total free
	// u16, directory end u16. The directory of u16 item offsets follows.
	dirStart = 11

	// maxLevels is GLASS_BTREE_CURSOR_LEVELS: no tree is deeper.
	maxLevels = 10

	minBlock = 2048
	maxBlock = 65536

	// The top three bits of a leaf item's 2-byte length are flags.
	itemCompressed = 0x80
	itemLast       = 0x40
	itemFirst      = 0x20
	itemSizeMask   = 0x1fff

	// versionRead is how much of the file's start is read for the version
	// file: six root records, each with a free list that a compacted
	// database leaves empty, and eight counters.
	versionRead = 4096

	// maxTag bounds one assembled tag. A posting-list chunk is about 2 KB
	// and a document's data is an article path; the stopword list, the
	// largest tag a Kiwix index holds, is a few kilobytes.
	maxTag = 4 << 20
)

var magic = []byte("\x0f\x0dXapian Glass")

// ErrCorrupt is wrapped by every error that reports a database whose bytes
// contradict the format.
var ErrCorrupt = errors.New("glass: the index is damaged")

func corrupt(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrCorrupt, fmt.Sprintf(format, args...))
}

type root struct {
	block     uint32
	level     int
	blockSize int
	fake      bool // a table with no entries has a root that is not on disk
}

// DB is an open database. It is safe for concurrent use: it holds no state
// that a read changes, and every walk owns its cursor.
type DB struct {
	src  io.ReaderAt
	size int64

	roots     [numTables]root
	blockSize int

	// The database-wide counters of the version file.
	DocCount    uint32
	LastDocID   uint32
	TotalDocLen uint64
}

// Open reads the version file of the database that occupies the first size
// bytes of src. Offsets are relative to the database, so a caller reading it
// out of a larger file passes an io.SectionReader.
func Open(src io.ReaderAt, size int64) (*DB, error) {
	n := min(size, versionRead)
	if n < magicLen+2+16 {
		return nil, corrupt("%d bytes is too short for a database", size)
	}
	head := make([]byte, n)
	if _, err := src.ReadAt(head, 0); err != nil {
		return nil, fmt.Errorf("glass: reading the version file: %w", err)
	}
	if !bytes.Equal(head[:magicLen], magic) {
		return nil, corrupt("not a glass database")
	}
	db := &DB{src: src, size: size}
	p := magicLen + 2 + 16 // the format date and the UUID
	var err error
	if _, p, err = uvarint(head, p); err != nil { // revision
		return nil, err
	}
	for i := range db.roots {
		var blk, flags, shift, freeLen uint64
		if blk, p, err = uvarint(head, p); err != nil {
			return nil, err
		}
		if flags, p, err = uvarint(head, p); err != nil {
			return nil, err
		}
		if _, p, err = uvarint(head, p); err != nil { // entry count
			return nil, err
		}
		if shift, p, err = uvarint(head, p); err != nil {
			return nil, err
		}
		if _, p, err = uvarint(head, p); err != nil { // compress_min
			return nil, err
		}
		if freeLen, p, err = uvarint(head, p); err != nil {
			return nil, err
		}
		if freeLen > uint64(len(head)-p) {
			return nil, corrupt("table %d: the free list runs past the version file", i)
		}
		p += int(freeLen)
		size := shift << 11
		if size < minBlock || size > maxBlock || size&(size-1) != 0 {
			return nil, corrupt("table %d: block size %d", i, size)
		}
		level := flags >> 2
		if level >= maxLevels || blk > 1<<32-1 {
			return nil, corrupt("table %d: root %d at level %d", i, blk, level)
		}
		db.roots[i] = root{block: uint32(blk), level: int(level), blockSize: int(size), fake: flags&1 != 0}
	}
	db.blockSize = db.roots[tablePostlist].blockSize
	for i, r := range db.roots {
		if r.blockSize != db.blockSize {
			return nil, corrupt("table %d: block size %d differs from %d", i, r.blockSize, db.blockSize)
		}
	}
	// doccount, last_docid - doccount, doclen lower bound, wdf upper bound,
	// doclen upper bound - wdf upper bound, oldest changeset, total doclen,
	// spelling frequency bound (GlassVersion::unserialise_stats).
	var stats [8]uint64
	for i := range stats {
		if stats[i], p, err = uvarint(head, p); err != nil {
			return nil, err
		}
	}
	if stats[0] > 1<<32-1 || stats[1] > 1<<32-1-stats[0] {
		return nil, corrupt("document count %d, last id +%d", stats[0], stats[1])
	}
	db.DocCount = uint32(stats[0])
	db.LastDocID = uint32(stats[0] + stats[1])
	db.TotalDocLen = stats[6]
	return db, nil
}

// uvarint reads Xapian's packed unsigned integer (common/pack.h, unpack_uint):
// seven bits a byte, low bits first, the high bit set on every byte but the
// last - the encoding binary.Uvarint reads.
func uvarint(b []byte, p int) (uint64, int, error) {
	if p > len(b) {
		return 0, p, corrupt("a packed integer starts past the end of its data")
	}
	v, n := binary.Uvarint(b[p:])
	if n <= 0 {
		return 0, p, corrupt("a packed integer is cut short or too long")
	}
	return v, p + n, nil
}

// uvarint32 is uvarint for a field Xapian holds in 32 bits.
func uvarint32(b []byte, p int) (uint32, int, error) {
	v, q, err := uvarint(b, p)
	if err == nil && v > 1<<32-1 {
		return 0, p, corrupt("a packed integer exceeds 32 bits")
	}
	return uint32(v), q, err
}

// packSortable appends v in the form whose byte order is numeric order
// (pack_uint_preserving_sort): two bytes below 0x8000, else a first byte
// whose leading one bits count the bytes that follow.
func packSortable(dst []byte, v uint32) []byte {
	if v < 0x8000 {
		return append(dst, byte(v>>8), byte(v))
	}
	n := 3
	for x := v >> 22; x != 0; x >>= 7 {
		n++
	}
	var buf [6]byte
	x := uint64(v)
	for i := n - 1; i > 0; i-- {
		buf[i] = byte(x)
		x >>= 8
	}
	buf[0] = byte(x) | byte(0xff<<(10-n))
	return append(dst, buf[:n]...)
}

// unpackSortable reads what packSortable wrote from the start of b.
func unpackSortable(b []byte) (uint32, error) {
	if len(b) < 2 {
		return 0, corrupt("a document id in a key is cut short")
	}
	first := b[0]
	if first < 0x80 {
		return uint32(first)<<8 | uint32(b[1]), nil
	}
	if first == 0xff {
		return 0, corrupt("a document id in a key is malformed")
	}
	n := 2
	for m := byte(0x40); first&m != 0; m >>= 1 {
		n++
	}
	if len(b)-1 < n {
		return 0, corrupt("a document id in a key is cut short")
	}
	v := uint64(first &^ byte(0xff<<(9-n)))
	for _, c := range b[1 : 1+n] {
		v = v<<8 | uint64(c)
	}
	if n > 5 || v > 1<<32-1 {
		return 0, corrupt("a document id in a key exceeds 32 bits")
	}
	return uint32(v), nil
}

// cursor is a position in one table: the block held at each level from the
// leaf (0) to the root, and the directory offset of the item it stands on.
// A cursor owns its block buffers and reuses them, so a walk allocates once.
type cursor struct {
	db    *DB
	root  root
	block [maxLevels][]byte
	held  [maxLevels]uint32 // the block number in block[j]; 0 is none
	c     [maxLevels]int
}

func (db *DB) cursor(table int) *cursor {
	return &cursor{db: db, root: db.roots[table]}
}

// load reads block n into level j and checks what every later access relies
// on: its level, and a directory that lies inside the block.
func (cu *cursor) load(j int, n uint32) error {
	if cu.held[j] == n && n != 0 {
		return nil
	}
	size := int64(cu.db.blockSize)
	off := int64(n) * size
	if n == 0 || off+size > cu.db.size {
		return corrupt("block %d is outside the database", n)
	}
	if cu.block[j] == nil {
		cu.block[j] = make([]byte, size)
	}
	cu.held[j] = 0
	if _, err := cu.db.src.ReadAt(cu.block[j], off); err != nil {
		return fmt.Errorf("glass: reading block %d: %w", n, err)
	}
	b := cu.block[j]
	end := dirEnd(b)
	if int(b[4]) != j || end < dirStart || end > len(b) || (end-dirStart)%2 != 0 {
		return corrupt("block %d: level %d where %d was expected, directory end %d", n, b[4], j, end)
	}
	if j > 0 && end == dirStart {
		return corrupt("block %d: a branch with no items", n)
	}
	cu.held[j] = n
	return nil
}

func dirEnd(b []byte) int { return int(binary.BigEndian.Uint16(b[9:])) }

// leafItem is one item of a leaf block: a key, the component number of the
// tag piece it carries, and that piece.
type leafItem struct {
	key        []byte
	component  int
	compressed bool
	last       bool
	data       []byte
}

// leaf decodes the item at directory offset c of leaf block b.
func leaf(b []byte, c int) (leafItem, error) {
	o := int(binary.BigEndian.Uint16(b[c:]))
	if o+3 > len(b) {
		return leafItem{}, corrupt("a leaf item starts at %d", o)
	}
	size := int(binary.BigEndian.Uint16(b[o:]))&itemSizeMask + 3
	kl := int(b[o+2])
	first := b[o]&itemFirst != 0
	head := 3 + kl
	if !first {
		head += 2
	}
	if head > size || o+size > len(b) {
		return leafItem{}, corrupt("a leaf item of %d bytes at %d", size, o)
	}
	it := leafItem{
		key:        b[o+3 : o+3+kl],
		component:  1,
		compressed: b[o]&itemCompressed != 0,
		last:       b[o]&itemLast != 0,
		data:       b[o+head : o+size],
	}
	if !first {
		it.component = int(binary.BigEndian.Uint16(b[o+3+kl:]))
	}
	return it, nil
}

// branch decodes the item at directory offset c of a branch block: the child
// block, and the key and component of the first item below it.
func branch(b []byte, c int) (child uint32, key []byte, component int, err error) {
	o := int(binary.BigEndian.Uint16(b[c:]))
	if o+5 > len(b) {
		return 0, nil, 0, corrupt("a branch item starts at %d", o)
	}
	kl := int(b[o+4])
	if o+5+kl+2 > len(b) {
		return 0, nil, 0, corrupt("a branch item of key length %d at %d", kl, o)
	}
	return binary.BigEndian.Uint32(b[o:]), b[o+5 : o+5+kl], int(binary.BigEndian.Uint16(b[o+5+kl:])), nil
}

// compareKey orders (key, component) pairs the way the tree does: by key
// bytes, a shorter key first, then by component (glass_table.h, compare).
func compareKey(a []byte, ac int, b []byte, bc int) int {
	if d := bytes.Compare(a, b); d != 0 {
		return d
	}
	return ac - bc
}

// seek places the cursor on the last item whose key is not greater than key
// (the first component of it), and reports whether that item's key is key.
// When every item is greater the cursor stands before the first one, and
// next moves onto it. ok is false for a table with no entries.
func (cu *cursor) seek(key []byte) (exact, ok bool, err error) {
	if cu.root.fake || cu.root.block == 0 {
		return false, false, nil
	}
	n := cu.root.block
	for j := cu.root.level; j > 0; j-- {
		if err := cu.load(j, n); err != nil {
			return false, false, err
		}
		b := cu.block[j]
		// The first item of a branch stands for everything below it, whatever
		// its key says (find_in_branch_ starts at DIR_START).
		lo, hi := dirStart, dirEnd(b)
		for hi-lo > 2 {
			mid := lo + (hi-lo)/4*2
			_, k, comp, err := branch(b, mid)
			if err != nil {
				return false, false, err
			}
			if compareKey(key, 1, k, comp) < 0 {
				hi = mid
			} else {
				lo = mid
			}
		}
		cu.c[j] = lo
		if n, _, _, err = branch(b, lo); err != nil {
			return false, false, err
		}
	}
	if err := cu.load(0, n); err != nil {
		return false, false, err
	}
	b := cu.block[0]
	lo, hi := dirStart-2, dirEnd(b)
	for hi-lo > 2 {
		mid := lo + (hi-lo)/4*2
		it, err := leaf(b, mid)
		if err != nil {
			return false, false, err
		}
		d := compareKey(key, 1, it.key, it.component)
		if d < 0 {
			hi = mid
		} else {
			lo = mid
			exact = d == 0
			if exact {
				break
			}
		}
	}
	cu.c[0] = lo
	if lo < dirStart {
		// Every item of this leaf is greater: a branch's key is a lower bound
		// of its subtree, not its first key, so the item wanted is the last of
		// the leaf before.
		if err := cu.prev(); err != nil {
			return false, false, err
		}
	}
	return exact, true, nil
}

// prev moves from before a leaf's first item onto the last item of the leaf
// before it. In the table's first leaf it stays where it is: nothing precedes.
func (cu *cursor) prev() error {
	j := 1
	for ; j <= cu.root.level && cu.c[j] == dirStart; j++ {
	}
	if j > cu.root.level {
		return nil
	}
	cu.c[j] -= 2
	for ; j > 0; j-- {
		child, _, _, err := branch(cu.block[j], cu.c[j])
		if err != nil {
			return err
		}
		if err := cu.load(j-1, child); err != nil {
			return err
		}
		cu.c[j-1] = dirEnd(cu.block[j-1]) - 2
	}
	if cu.c[0] < dirStart {
		return corrupt("block %d: a leaf with no items below a branch", cu.held[0])
	}
	return nil
}

// next moves to the following item of the table, across blocks. It reports
// false at the table's end.
func (cu *cursor) next() (bool, error) {
	cu.c[0] += 2
	if cu.c[0] < dirEnd(cu.block[0]) {
		return true, nil
	}
	j := 1
	for ; j <= cu.root.level; j++ {
		cu.c[j] += 2
		if cu.c[j] < dirEnd(cu.block[j]) {
			break
		}
	}
	if j > cu.root.level {
		return false, nil
	}
	for ; j > 0; j-- {
		child, _, _, err := branch(cu.block[j], cu.c[j])
		if err != nil {
			return false, err
		}
		if err := cu.load(j-1, child); err != nil {
			return false, err
		}
		cu.c[j-1] = dirStart
	}
	if dirEnd(cu.block[0]) == dirStart {
		return false, corrupt("block %d: a leaf with no items below a branch", cu.held[0])
	}
	return true, nil
}

// item is the leaf item the cursor stands on.
func (cu *cursor) item() (leafItem, error) {
	c := cu.c[0]
	if c < dirStart || c >= dirEnd(cu.block[0]) {
		return leafItem{}, corrupt("a cursor stands outside its block")
	}
	return leaf(cu.block[0], c)
}

// tag appends to dst the whole tag of the item the cursor stands on, which
// must be a first component, and leaves the cursor on its last component. A
// tag too long for one item continues in the items that follow under the same
// key (GlassTable::read_tag); a compressed one is a single raw DEFLATE stream
// cut across them (common/compression_stream.cc).
func (cu *cursor) tag(dst []byte) ([]byte, error) {
	it, err := cu.item()
	if err != nil {
		return nil, err
	}
	if it.component != 1 {
		return nil, corrupt("a tag is read from its component %d", it.component)
	}
	if it.last && !it.compressed {
		return append(dst, it.data...), nil
	}
	compressed := it.compressed
	raw := append([]byte(nil), it.data...)
	for want := 2; !it.last; want++ {
		ok, err := cu.next()
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, corrupt("the table ends inside a tag")
		}
		if it, err = cu.item(); err != nil {
			return nil, err
		}
		if it.component != want {
			return nil, corrupt("a tag continues with component %d where %d was expected", it.component, want)
		}
		if len(raw)+len(it.data) > maxTag {
			return nil, corrupt("a tag longer than %d bytes", maxTag)
		}
		raw = append(raw, it.data...)
	}
	if !compressed {
		return append(dst, raw...), nil
	}
	zr := flate.NewReader(bytes.NewReader(raw))
	defer zr.Close()
	plain, err := io.ReadAll(io.LimitReader(zr, maxTag+1))
	if err != nil {
		return nil, corrupt("a compressed tag does not inflate: %v", err)
	}
	if len(plain) > maxTag {
		return nil, corrupt("a tag longer than %d bytes", maxTag)
	}
	return append(dst, plain...), nil
}

// get returns the tag stored under key in one table.
func (db *DB) get(table int, key []byte) ([]byte, bool, error) {
	cu := db.cursor(table)
	exact, ok, err := cu.seek(key)
	if err != nil || !ok || !exact {
		return nil, false, err
	}
	tag, err := cu.tag(nil)
	return tag, err == nil, err
}

// Metadata returns a user metadata value of the index, "" when it has none.
// Metadata lives in the postlist table under "\x00\xc0" + key
// (GlassDatabase::get_metadata).
func (db *DB) Metadata(key string) (string, error) {
	tag, _, err := db.get(tablePostlist, append([]byte{0x00, 0xc0}, key...))
	return string(tag), err
}

// DocData returns what the index stores for a document: libzim writes the
// article's path with its namespace, "C/path" (xapianWorker.cpp).
func (db *DB) DocData(docid uint32) (string, error) {
	tag, found, err := db.get(tableDocdata, packSortable(nil, docid))
	if err != nil {
		return "", err
	}
	if !found {
		return "", corrupt("document %d has no data", docid)
	}
	return string(tag), nil
}

// HasPositions reports whether the index records where in a document each
// term occurs. libzim's full-text index does not (index_text_without_positions).
func (db *DB) HasPositions() bool {
	r := db.roots[tablePosition]
	return !r.fake && r.block != 0
}
