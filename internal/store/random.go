// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"math/bits"
	"strings"

	"github.com/wuweidict/wudict/internal/dict"
	"github.com/wuweidict/wudict/internal/wildcard"
)

var _ dict.Sampler = (*Store)(nil)

// span is the id range of the entry table. `entry.id` is the rowid, so MIN and
// MAX are each one step down the table's b-tree, whatever its size.
func (s *Store) span(ctx context.Context) (lo, hi int64, err error) {
	s.spanMu.Lock()
	defer s.spanMu.Unlock()
	if s.spanOK {
		return s.spanLo, s.spanHi, nil
	}
	var l, h sql.NullInt64
	if err := s.db.QueryRowContext(ctx, "SELECT MIN(id), MAX(id) FROM entry").Scan(&l, &h); err != nil {
		return 0, 0, err
	}
	s.spanLo, s.spanHi = 1, 0 // empty: hi < lo
	if l.Valid && h.Valid {
		s.spanLo, s.spanHi = l.Int64, h.Int64
	}
	s.spanOK = true
	return s.spanLo, s.spanHi, nil
}

// sampleProbes is how many ids a draw tries before it walks. A dictionary
// that is 59 % sub-entries (LDOCE6) misses all 16 about once in 4,600 draws.
const sampleProbes = 16

// Sample implements dict.Sampler.
//
// u picks an id (the high word of u×n, uniform over [0, n) without the bias of
// a modulo). An id that is a sub-entry, or a gap, is not walked past - the
// entry after a run of 13,803 sub-entries would then take the run's whole
// share (LAAD3: one word in 5.5 of all draws) - but drawn again from the next
// value of a splitmix64 sequence seeded by u, so every entry stays equally
// likely. Only after sampleProbes misses does the draw take the first entry at
// or after its last id, wrapping to the first. Headwords only: the column the
// index covers, and no article decoded.
func (s *Store) Sample(ctx context.Context, u uint64) (string, error) {
	lo, hi, err := s.span(ctx)
	if err != nil {
		return "", err
	}
	if hi < lo {
		return "", dict.ErrNotFound
	}
	n := uint64(hi-lo) + 1
	var id int64
	for range sampleProbes {
		at, _ := bits.Mul64(u, n)
		id = lo + int64(at)
		var w string
		switch err := s.db.QueryRowContext(ctx, "SELECT w FROM entry WHERE id = ?", id).Scan(&w); {
		case errors.Is(err, sql.ErrNoRows): // a gap in the ids
		case err != nil:
			return "", err
		case !strings.HasPrefix(w, "@") || len(w) == 1: // browseWhere: `@_%` is a sub-entry
			return w, nil
		}
		u = splitmix64(u)
	}
	const walk = "SELECT w FROM entry WHERE id >= ? AND " + browseWhere + " ORDER BY id LIMIT 1"
	w, err := s.sampleAt(ctx, walk, id)
	if errors.Is(err, dict.ErrNotFound) {
		w, err = s.sampleAt(ctx, walk, lo)
	}
	return w, err
}

// splitmix64 is the next value of Steele, Lea and Flood's sequence: a full
// 64-bit permutation, so a draw's probes never repeat in a short run.
func splitmix64(x uint64) uint64 {
	x += 0x9e3779b97f4a7c15
	x = (x ^ (x >> 30)) * 0xbf58476d1ce4e5b9
	x = (x ^ (x >> 27)) * 0x94d049bb133111eb
	return x ^ (x >> 31)
}

func (s *Store) sampleAt(ctx context.Context, q string, id int64) (string, error) {
	var w string
	switch err := s.db.QueryRowContext(ctx, q, id).Scan(&w); {
	case errors.Is(err, sql.ErrNoRows):
		return "", dict.ErrNotFound
	case err != nil:
		return "", err
	}
	return w, nil
}

// Has implements dict.Sampler: ExactContext's match, by its headword or an
// alias, without reading an article. Sub-entries do not count: they are
// fetched by a link, not looked up.
func (s *Store) Has(ctx context.Context, word string) (bool, error) {
	word = strings.TrimSpace(word)
	if word == "" {
		return false, nil
	}
	keys, err := s.keys(ctx, wildcard.Literal(word, false), 1, scope{aliases: true})
	return len(keys) > 0, err
}
