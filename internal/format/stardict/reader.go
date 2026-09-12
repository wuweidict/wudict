// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package stardict

import (
	"io"

	"github.com/wuweidict/wudict/internal/dict"
)

// Reader is the sequential ingest scan: idx entries in order, with .syn
// synonyms attached as extra headwords (= aliases in the text.db).
type Reader struct {
	d   *Dict
	pos int
}

func NewReader(path string) (*Reader, error) {
	d, err := Open(path)
	if err != nil {
		return nil, err
	}
	return &Reader{d: d}, nil
}

func (r *Reader) Meta() dict.Meta { return r.d.Meta() }

// Next yields the next readable record.
//
// A record whose (offset,size) cannot be served is SKIPPED, not returned as an
// error: the ingest driver aborts the whole preparation on any error from here,
// which turned one unreadable article into the loss of every other entry in the
// dictionary. Damage is local - a truncated .dict.dz tail costs the chunks it
// truncates - so the scan steps over it and prepares the rest, matching what a
// preview search already does, where the same record fails alone.
func (r *Reader) Next() (dict.Entry, error) {
	for r.pos < len(r.d.entries) {
		i := r.pos
		r.pos++
		body, err := r.d.article(i)
		if err != nil {
			continue // unreadable record: step over it, keep the dictionary
		}
		headwords := append([]string{r.d.entries[i].word}, r.d.synonyms[i]...)
		return dict.Entry{Headwords: headwords, Body: body, Kind: dict.BodyHTML}, nil
	}
	return dict.Entry{}, io.EOF
}

func (r *Reader) Close() error { return r.d.Close() }
