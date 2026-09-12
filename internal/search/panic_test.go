// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package search

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/wuweidict/wudict/internal/dict"
)

type boomDict struct{ mode int }

func (b boomDict) Meta() dict.Meta { return dict.Meta{Name: "boom", Path: "/x/boom.mdx"} }
func (b boomDict) Caps() dict.Caps { return dict.Caps{Exact: true, Prefix: true} }
func (b boomDict) Exact(w string, n int) ([]dict.Result, error) {
	// the exact gomdict shape: comp[8:info.compressSize] with a per-block
	// compressSize that the file declared and nothing validated.
	comp := make([]byte, 4)
	compressSize := len(w) - len(w) + 2
	_ = comp[8:compressSize]
	return nil, nil
}
func (b boomDict) Prefix(w string, n int) ([]dict.Result, error) { return nil, nil }
func (b boomDict) Keywords(o, n int) []string                    { return nil }
func (b boomDict) Resource(n string) (io.ReadCloser, string, error) {
	return nil, "", dict.ErrNotFound
}
func (b boomDict) Close() error { return nil }

type okDict struct{ boomDict }

func (o okDict) Meta() dict.Meta { return dict.Meta{Name: "ok", Path: "/x/ok.mdx"} }
func (o okDict) Exact(w string, n int) ([]dict.Result, error) {
	return []dict.Result{{Headword: w, Body: "b"}}, nil
}

func TestFanOutSurvivesParserPanic(t *testing.T) {
	hits := All(context.Background(), []dict.Dictionary{boomDict{}, okDict{}}, Exact, "x", 5)
	if hits[0].Err == nil {
		t.Fatal("panicking dictionary produced no error")
	}
	if !strings.Contains(hits[0].Err.Error(), "parser panic") {
		t.Fatalf("unexpected error: %v", hits[0].Err)
	}
	if !strings.Contains(hits[0].Err.Error(), "boom.mdx") {
		t.Fatalf("error does not name the dictionary: %v", hits[0].Err)
	}
	if len(hits[1].Results) != 1 || hits[1].Err != nil {
		t.Fatalf("healthy dictionary broken: %+v", hits[1])
	}
}

func TestStreamOpenSurvivesOpenPanic(t *testing.T) {
	var got []Hit
	StreamOpen(context.Background(), []Opener{
		func() (dict.Dictionary, error) { panic("registry blew up") },
	}, Exact, "x", 5, func(i int, h Hit) { got = append(got, h) })
	if len(got) != 1 || got[0].Err == nil {
		t.Fatalf("panicking opener not converted: %+v", got)
	}
}
