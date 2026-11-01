// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package mdx

import (
	"io"

	"github.com/wuweidict/wudict/internal/dict"
	"github.com/wuweidict/wudict/internal/logx"
)

// ReaderVersion is the behaviour version of this format's Reader (see
// dict.RegisterReaderVersion). Bump it in the same commit as any change to
// what the Reader yields. Unlike the other formats there is no golden test to
// catch a forgotten bump: MDX has no in-code writer to build a fixture with,
// so the tests that exercise this Reader need a real file (WUDICT_TEST_MDX).
const ReaderVersion = 3

func init() {
	dict.RegisterReader(".mdx", func(path string) (dict.Reader, error) { return NewReader(path) })
	dict.RegisterReaderVersion("mdx", ReaderVersion)
}

// Reader is the sequential ingest scan over an .mdx file. It reuses the
// opened Dict (headwords already decoded) and walks entries in dictionary
// order, emitting @@@LINK records as alias redirects instead of bodies.
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
	for r.pos < len(r.d.entries) {
		e := r.d.entries[r.pos]
		r.pos++
		raw, err := r.d.mdx.LocateByKeywordEntry(e)
		if err != nil {
			logx.V("%sentry %q could not be read: %v (skipped)", logx.Dict(r.d.meta.Name), e.KeyWord, err)
			continue
		}
		body, target, isLink := r.d.article(raw)
		if isLink {
			if target == "" || target == e.KeyWord {
				continue
			}
			return dict.Entry{Headwords: []string{e.KeyWord}, LinkTo: target}, nil
		}
		return dict.Entry{Headwords: []string{e.KeyWord}, Body: body, Kind: dict.BodyHTML}, nil
	}
	return dict.Entry{}, io.EOF
}

func (r *Reader) Close() error { return r.d.Close() }
