// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package glass

import "bytes"

// A posting list is a term's documents in ascending id order, each with the
// number of times the term occurs in it (wdf). Xapian cuts a list into chunks
// of about 2 KB, one table entry each: the first under the term itself, the
// rest under term + "\x00" + the id of the chunk's first document
// (glass_postlist.cc, "The format of a postlist"):
//
//	first chunk only   document frequency, collection frequency, first id - 1
//	every chunk        '0' or '1' (the last chunk), last id - first id,
//	                   the first wdf, then (id gap - 1, wdf) per further document
//
// The document lengths are a posting list too, of a term with no name: keyed
// "\x00\xe0", its later chunks "\x00\xe0" + id with no separator, and its
// "wdf" a document's length (pack_glass_postlist_key).

var docLenKey = []byte{0x00, 0xe0}

// postings walks one posting list. The zero value is not usable; see
// DB.postings.
type postings struct {
	cu     *cursor
	key    []byte // the first chunk's key
	prefix []byte // what precedes the document id in a later chunk's key
	tag    []byte
	p      int

	docFreq uint32 // documents the term occurs in
	did     uint32 // the current document; 0 before the first and at the end
	wdf     uint32
	last    uint32 // the last document of the current chunk
	final   bool   // the current chunk is the list's last
}

// postings opens the list of term, or of the document lengths when term is
// empty. found is false when the index does not hold the term.
func (db *DB) postings(term string) (pl *postings, found bool, err error) {
	key, prefix := docLenKey, docLenKey
	if term != "" {
		if bytes.IndexByte([]byte(term), 0) >= 0 {
			return nil, false, nil // no term libzim writes holds a NUL
		}
		key = []byte(term)
		prefix = append(append(make([]byte, 0, len(key)+1), key...), 0)
	}
	pl = &postings{cu: db.cursor(tablePostlist), key: key, prefix: prefix}
	exact, ok, err := pl.cu.seek(key)
	if err != nil || !ok || !exact {
		return nil, false, err
	}
	if pl.tag, err = pl.cu.tag(pl.tag[:0]); err != nil {
		return nil, false, err
	}
	if err := pl.first(); err != nil {
		return nil, false, err
	}
	return pl, true, nil
}

// first reads the head of the list's first chunk and stands on its first
// document.
func (pl *postings) first() (err error) {
	pl.p = 0
	if pl.docFreq, pl.p, err = uvarint32(pl.tag, pl.p); err != nil {
		return err
	}
	if _, pl.p, err = uvarint(pl.tag, pl.p); err != nil { // collection frequency
		return err
	}
	var did uint32
	if did, pl.p, err = uvarint32(pl.tag, pl.p); err != nil {
		return err
	}
	if did == 1<<32-1 {
		return corrupt("a posting list starts past the last document id")
	}
	return pl.chunk(did + 1)
}

// chunk reads a chunk's header at pl.p and stands on its first document, did.
func (pl *postings) chunk(did uint32) (err error) {
	if pl.p >= len(pl.tag) || pl.tag[pl.p]&^1 != '0' {
		return corrupt("a posting chunk has no last-chunk flag")
	}
	pl.final = pl.tag[pl.p] == '1'
	pl.p++
	var span uint32
	if span, pl.p, err = uvarint32(pl.tag, pl.p); err != nil {
		return err
	}
	if span > 1<<32-1-did {
		return corrupt("a posting chunk ends past the last document id")
	}
	pl.did, pl.last = did, did+span
	pl.wdf, pl.p, err = uvarint32(pl.tag, pl.p)
	return err
}

// next moves to the following document. At the list's end it reports false
// and pl.did is 0.
func (pl *postings) next() (bool, error) {
	if pl.did == 0 {
		return false, nil
	}
	if pl.did < pl.last {
		gap, p, err := uvarint32(pl.tag, pl.p)
		if err != nil {
			return false, err
		}
		if gap >= pl.last-pl.did {
			return false, corrupt("a posting runs past its chunk's last document")
		}
		pl.did += gap + 1
		pl.wdf, pl.p, err = uvarint32(pl.tag, p)
		return err == nil, err
	}
	if pl.final {
		pl.did = 0
		return false, nil
	}
	ok, err := pl.cu.next()
	if err != nil {
		return false, err
	}
	if !ok {
		return false, corrupt("the table ends inside a posting list")
	}
	return pl.enter(pl.did)
}

// enter loads the later chunk the cursor stands on, which must start after
// the document after.
func (pl *postings) enter(after uint32) (bool, error) {
	it, err := pl.cu.item()
	if err != nil {
		return false, err
	}
	if !bytes.HasPrefix(it.key, pl.prefix) {
		return false, corrupt("a posting list ends before its last chunk")
	}
	did, err := unpackSortable(it.key[len(pl.prefix):])
	if err != nil {
		return false, err
	}
	if did <= after {
		return false, corrupt("a posting chunk starts at document %d, not after %d", did, after)
	}
	if pl.tag, err = pl.cu.tag(pl.tag[:0]); err != nil {
		return false, err
	}
	pl.p = 0
	return true, pl.chunk(did)
}

// skipTo moves to the first document whose id is at least target, which must
// be greater than the current one. A target beyond the current chunk is
// reached through the tree, not by reading the chunks between.
func (pl *postings) skipTo(target uint32) (bool, error) {
	if pl.did == 0 {
		return false, nil
	}
	if target > pl.last && !pl.final {
		if err := pl.jump(target); err != nil {
			return false, err
		}
	}
	for pl.did < target {
		ok, err := pl.next()
		if err != nil || !ok {
			return false, err
		}
	}
	return true, nil
}

// jump loads the chunk that would hold target, a document past the current
// chunk. When no later chunk starts at or before target the walk is left at
// the end of the current chunk, with the cursor where next expects it.
func (pl *postings) jump(target uint32) error {
	key := packSortable(append(make([]byte, 0, len(pl.prefix)+5), pl.prefix...), target)
	exact, _, err := pl.cu.seek(key)
	if err != nil {
		return err
	}
	it, err := pl.cu.item()
	if err != nil {
		return err
	}
	// The cursor is on the last item not after (key, component 1): the first
	// component of key itself, or the LAST component of the entry before it.
	// Either way that entry is a chunk of this list - the first one sorts
	// before every later one and nothing else sorts between them.
	if !exact && it.component != 1 {
		found := append([]byte(nil), it.key...)
		if exact, _, err = pl.cu.seek(found); err != nil {
			return err
		}
		if !exact {
			return corrupt("a posting chunk lost its first component")
		}
		if it, err = pl.cu.item(); err != nil {
			return err
		}
	}
	if !bytes.Equal(it.key, pl.key) {
		if !bytes.HasPrefix(it.key, pl.prefix) || len(it.key) == len(pl.prefix) {
			return corrupt("a posting list has no chunk for document %d", target)
		}
		start, err := unpackSortable(it.key[len(pl.prefix):])
		if err != nil {
			return err
		}
		if start > pl.last {
			_, err := pl.enter(pl.last)
			return err
		}
	}
	// The chunk found is the current one: target lies between it and the
	// next. Finish it, and stand on the last item of its tag.
	pl.did = pl.last
	for !it.last {
		ok, err := pl.cu.next()
		if err != nil {
			return err
		}
		if !ok {
			return corrupt("the table ends inside a tag")
		}
		if it, err = pl.cu.item(); err != nil {
			return err
		}
	}
	return nil
}
