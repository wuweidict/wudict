// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package stardict

import (
	"io"

	"github.com/glowinthedark/gonow-dict/internal/dict"
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

func (r *Reader) Next() (dict.Entry, error) {
	if r.pos >= len(r.d.entries) {
		return dict.Entry{}, io.EOF
	}
	i := r.pos
	r.pos++
	body, err := r.d.article(i)
	if err != nil {
		return dict.Entry{}, err
	}
	headwords := append([]string{r.d.entries[i].word}, r.d.synonyms[i]...)
	return dict.Entry{Headwords: headwords, Body: body, Kind: dict.BodyHTML}, nil
}

func (r *Reader) Close() error { return r.d.Close() }
