// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package glass

import (
	"bytes"
	"container/heap"
	"context"
	"math"
)

// A query is a tree of index terms. libzim's own search is the AND of a
// query's terms (search.cpp, OP_AND); OR and AND NOT are here for the caller
// that composes more.

type op uint8

const (
	opTerm op = iota
	opAnd
	opOr
	opAndNot
)

// Query is one node of a query tree. Build it with Term, And, Or and AndNot.
type Query struct {
	op   op
	term string
	args []*Query
}

// Term matches the documents that hold one index term - a word as
// Analyzer.Terms returns it.
func Term(term string) *Query { return &Query{op: opTerm, term: term} }

// And matches the documents every argument matches.
func And(args ...*Query) *Query { return join(opAnd, args) }

// Or matches the documents any argument matches. Or of nothing matches
// nothing.
func Or(args ...*Query) *Query { return join(opOr, args) }

// AndNot matches the documents keep matches and drop does not.
func AndNot(keep, drop *Query) *Query { return &Query{op: opAndNot, args: []*Query{keep, drop}} }

func join(o op, args []*Query) *Query {
	if len(args) == 1 {
		return args[0]
	}
	return &Query{op: o, args: args}
}

// Hit is one matching document.
type Hit struct {
	DocID uint32
	// Score is Xapian's BM25 with its default parameters, which is what
	// libzim ranks by. Larger is better.
	Score float64
}

// BM25 with Xapian's defaults: k1 = 1, k2 = 0, k3 = 1, b = 0.5 and a
// normalised length of at least 0.5 (weight/bm25weight.cc).
const (
	bm25K1     = 1.0
	bm25B      = 0.5
	bm25MinLen = 0.5
)

// iter walks the documents a query node matches, in ascending id order.
type iter interface {
	// doc is the current document, 0 at the end.
	doc() uint32
	// advance moves to the first matching document that is at least target
	// and past the current one.
	advance(target uint32) error
	// weight adds up the term weights of the current document, whose
	// normalised length is normLen.
	weight(normLen float64) float64
}

type termIter struct {
	pl *postings // nil when the index does not hold the term
	tw float64   // the term's weight before the document's part
}

func (t *termIter) doc() uint32 {
	if t.pl == nil {
		return 0
	}
	return t.pl.did
}

func (t *termIter) advance(target uint32) error {
	if t.pl == nil || t.pl.did == 0 {
		return nil
	}
	var err error
	if target <= t.pl.did+1 {
		_, err = t.pl.next()
	} else {
		_, err = t.pl.skipTo(target)
	}
	return err
}

func (t *termIter) weight(normLen float64) float64 {
	wdf := float64(t.pl.wdf)
	return t.tw * wdf / (bm25K1*(normLen*bm25B+1-bm25B) + wdf)
}

type andIter struct {
	args []iter
	did  uint32
}

func (a *andIter) doc() uint32 { return a.did }

// align moves every argument to the first document all of them hold that is
// at least d.
func (a *andIter) align(d uint32) error {
	for i := 0; i < len(a.args); {
		it := a.args[i]
		if it.doc() < d && it.doc() != 0 {
			if err := it.advance(d); err != nil {
				return err
			}
		}
		switch cur := it.doc(); {
		case cur == 0:
			a.did = 0
			return nil
		case cur > d:
			d, i = cur, 0
		default:
			i++
		}
	}
	a.did = d
	return nil
}

func (a *andIter) advance(target uint32) error {
	if a.did == 0 {
		return nil
	}
	return a.align(max(target, a.did+1))
}

func (a *andIter) weight(normLen float64) (w float64) {
	for _, it := range a.args {
		w += it.weight(normLen)
	}
	return w
}

type orIter struct {
	args []iter
	did  uint32
}

func (o *orIter) doc() uint32 { return o.did }

func (o *orIter) settle() {
	o.did = 0
	for _, it := range o.args {
		if d := it.doc(); d != 0 && (o.did == 0 || d < o.did) {
			o.did = d
		}
	}
}

func (o *orIter) advance(target uint32) error {
	if o.did == 0 {
		return nil
	}
	target = max(target, o.did+1)
	for _, it := range o.args {
		if d := it.doc(); d != 0 && d < target {
			if err := it.advance(target); err != nil {
				return err
			}
		}
	}
	o.settle()
	return nil
}

func (o *orIter) weight(normLen float64) (w float64) {
	for _, it := range o.args {
		if it.doc() == o.did {
			w += it.weight(normLen)
		}
	}
	return w
}

type andNotIter struct {
	keep, drop iter
}

func (a *andNotIter) doc() uint32 { return a.keep.doc() }

// settle moves keep past every document drop holds too.
func (a *andNotIter) settle() error {
	for {
		d := a.keep.doc()
		if d == 0 {
			return nil
		}
		if x := a.drop.doc(); x != 0 && x < d {
			if err := a.drop.advance(d); err != nil {
				return err
			}
		}
		if a.drop.doc() != d {
			return nil
		}
		if err := a.keep.advance(d + 1); err != nil {
			return err
		}
	}
}

func (a *andNotIter) advance(target uint32) error {
	if err := a.keep.advance(target); err != nil {
		return err
	}
	return a.settle()
}

func (a *andNotIter) weight(normLen float64) float64 { return a.keep.weight(normLen) }

// compile opens the posting lists of q.
func (db *DB) compile(q *Query) (iter, error) {
	switch q.op {
	case opTerm:
		pl, found, err := db.postings(q.term)
		if err != nil || !found {
			return &termIter{}, err
		}
		// The weight of a term every document holds would be negative by the
		// textbook formula; Xapian lifts it (BM25Weight::init).
		tw := (float64(db.DocCount) - float64(pl.docFreq) + 0.5) / (float64(pl.docFreq) + 0.5)
		if tw < 2 {
			tw = tw*0.5 + 1
		}
		return &termIter{pl: pl, tw: math.Log(tw) * (bm25K1 + 1)}, nil
	case opAndNot:
		keep, err := db.compile(q.args[0])
		if err != nil {
			return nil, err
		}
		drop, err := db.compile(q.args[1])
		if err != nil {
			return nil, err
		}
		it := &andNotIter{keep: keep, drop: drop}
		return it, it.settle()
	}
	args := make([]iter, len(q.args))
	for i, a := range q.args {
		var err error
		if args[i], err = db.compile(a); err != nil {
			return nil, err
		}
	}
	if q.op == opOr {
		it := &orIter{args: args}
		it.settle()
		return it, nil
	}
	if len(args) == 0 {
		return &termIter{}, nil
	}
	it := &andIter{args: args}
	return it, it.align(1)
}

// hitHeap keeps the best limit hits, the worst of them on top.
type hitHeap []Hit

func (h hitHeap) Len() int { return len(h) }
func (h hitHeap) Less(i, j int) bool {
	return worse(h[i], h[j])
}
func (h hitHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *hitHeap) Push(x any)   { *h = append(*h, x.(Hit)) }
func (h *hitHeap) Pop() any {
	old := *h
	x := old[len(old)-1]
	*h = old[:len(old)-1]
	return x
}

// worse orders hits by score, and equal scores by document id, lower first -
// the order Xapian's MSet has.
func worse(a, b Hit) bool {
	if a.Score != b.Score {
		return a.Score < b.Score
	}
	return a.DocID > b.DocID
}

// Search returns the best limit documents q matches, best first, and how many
// it matches in all.
func (db *DB) Search(ctx context.Context, q *Query, limit int) (hits []Hit, matched int, err error) {
	if limit <= 0 || db.DocCount == 0 {
		return nil, 0, nil
	}
	it, err := db.compile(q)
	if err != nil {
		return nil, 0, err
	}
	lens, found, err := db.postings("")
	if err != nil {
		return nil, 0, err
	}
	if !found {
		return nil, 0, corrupt("the index has no document lengths")
	}
	avg := float64(db.TotalDocLen) / float64(db.DocCount)
	if avg == 0 {
		avg = 1
	}
	var best hitHeap
	for d := it.doc(); d != 0; d = it.doc() {
		if matched&0xfff == 0 {
			if err := ctx.Err(); err != nil {
				return nil, 0, err
			}
		}
		matched++
		if lens.did < d {
			if _, err := lens.skipTo(d); err != nil {
				return nil, 0, err
			}
		}
		var length float64
		if lens.did == d {
			length = float64(lens.wdf)
		}
		h := Hit{DocID: d, Score: it.weight(max(length/avg, bm25MinLen))}
		if len(best) < limit {
			heap.Push(&best, h)
		} else if worse(best[0], h) {
			best[0] = h
			heap.Fix(&best, 0)
		}
		if err := it.advance(d + 1); err != nil {
			return nil, 0, err
		}
	}
	hits = make([]Hit, len(best))
	for i := len(best) - 1; i >= 0; i-- {
		hits[i] = heap.Pop(&best).(Hit)
	}
	return hits, matched, nil
}

// Expand returns up to limit index terms that begin with prefix, in the
// index's own order, and whether there are more. Each distinct term costs one
// descent of the tree: the later chunks of a long posting list sort between a
// term and the next, and are stepped over rather than read.
func (db *DB) Expand(prefix string, limit int) (terms []string, more bool, err error) {
	if prefix == "" || bytes.IndexByte([]byte(prefix), 0) >= 0 {
		return nil, false, nil
	}
	cu := db.cursor(tablePostlist)
	key := []byte(prefix)
	for {
		exact, ok, err := cu.seek(key)
		if err != nil || !ok {
			return nil, false, err
		}
		if !exact {
			// On the last item before key: the term wanted is the next one.
			if ok, err = cu.next(); err != nil || !ok {
				return terms, false, err
			}
		}
		it, err := cu.item()
		if err != nil {
			return nil, false, err
		}
		for it.component != 1 || bytes.IndexByte(it.key, 0) >= 0 {
			// The rest of a tag, or a later chunk of the term before.
			if ok, err = cu.next(); err != nil || !ok {
				return terms, false, err
			}
			if it, err = cu.item(); err != nil {
				return nil, false, err
			}
		}
		if !bytes.HasPrefix(it.key, []byte(prefix)) {
			return terms, false, nil
		}
		if len(terms) == limit {
			return terms, true, nil
		}
		terms = append(terms, string(it.key))
		// The first key past this term's own chunks.
		key = append(append(key[:0], it.key...), 1)
	}
}

// Has reports whether a document holds an index term.
func (db *DB) Has(term string, docid uint32) (bool, error) {
	pl, found, err := db.postings(term)
	if err != nil || !found {
		return false, err
	}
	if pl.did < docid {
		if _, err := pl.skipTo(docid); err != nil {
			return false, err
		}
	}
	return pl.did == docid, nil
}
