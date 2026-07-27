// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package mdx

import (
	"io"
	"strings"

	"github.com/glowinthedark/gonow-dict/internal/dict"
	"github.com/glowinthedark/gonow-dict/internal/logx"
)

func init() {
	dict.RegisterReader(".mdx", func(path string) (dict.Reader, error) { return NewReader(path) })
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
		body := strings.TrimSpace(strings.Trim(decodeEnc(raw, r.d.enc), "\x00"))
		if target, ok := strings.CutPrefix(body, linkPrefix); ok {
			target = strings.TrimSpace(strings.Trim(target, "\x00"))
			if target == "" || target == e.KeyWord {
				continue
			}
			return dict.Entry{Headwords: []string{e.KeyWord}, LinkTo: target}, nil
		}
		if len(r.d.stylesheet) > 0 {
			body = substituteStylesheet(body, r.d.stylesheet)
		}
		return dict.Entry{Headwords: []string{e.KeyWord}, Body: body, Kind: dict.BodyHTML}, nil
	}
	return dict.Entry{}, io.EOF
}

func (r *Reader) Close() error { return r.d.Close() }
