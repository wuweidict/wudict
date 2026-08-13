// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package mdx

import (
	"bytes"
	"fmt"
	"io"
	"mime"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/wuweidict/wudict/internal/dict"
	go_mdict "github.com/wuweidict/wudict/internal/gomdict"
)

// MDD is ONE .mdd file opened on its own.
//
// An .mdd is MDict's resource container, and it is the same key-block format
// as an .mdx with file bytes where the articles would be. That makes it a
// key/value store in its own right, so the commands that enumerate and fetch
// keys mean the same thing on it:
//
//	wudict keys x.mdd          list what is inside
//	wudict res  x.mdd <name>   pull one item out
//
// No .mdx is involved, deliberately. Reaching an .mdd through its dictionary
// would assume a source file that may not be present, and an .mdx may have
// several companions (NAME.mdd, NAME.1.mdd, …) whose union is not what you
// asked for when you named one file. This is also how mdict-utils behaves,
// which is the tool people already know.
//
// Registered as INSPECTABLE, not as a format: a folder scan must keep treating
// .mdd files as a dictionary's companions, never as dictionaries.
type MDD struct {
	meta  dict.Meta
	md    *go_mdict.Mdict
	items map[string]*go_mdict.MDictKeywordEntry // lower-case name -> entry
	names []string                               // original case, sorted, forward slashes
}

// OpenMDD opens one .mdd resource container.
func OpenMDD(filename string) (*MDD, error) {
	md, entries, err := openIndexed(filename)
	if err != nil {
		return nil, err
	}
	d := &MDD{
		md:    md,
		items: make(map[string]*go_mdict.MDictKeywordEntry, len(entries)),
		names: make([]string, 0, len(entries)),
	}
	for _, e := range entries {
		// MDD keys are stored as `\path\name`; normalise to the forward-slash,
		// no-leading-separator form the rest of wudict uses for resources — so
		// a name printed by `keys` is a name `res` accepts, unchanged.
		name := strings.ReplaceAll(strings.TrimLeft(e.KeyWord, "\\/"), "\\", "/")
		if name == "" {
			continue
		}
		k := strings.ToLower(name)
		if _, dup := d.items[k]; dup {
			continue // first wins, as resourceIndex does across companions
		}
		d.items[k] = e
		d.names = append(d.names, name)
	}
	sort.Strings(d.names)
	d.meta = dict.Meta{
		Name:       strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename)),
		Format:     "mdd",
		Path:       filename,
		EntryCount: len(d.names),
	}
	return d, nil
}

func (d *MDD) Meta() dict.Meta { return d.meta }

// Caps reports nothing searchable, which is the truth: an .mdd holds files,
// not headwords. It is listed and fetched, never looked up.
func (d *MDD) Caps() dict.Caps { return dict.Caps{} }

func (d *MDD) Close() error { return nil }

// Exact and Prefix find nothing: there are no articles here. They exist
// because Dictionary requires them, and answering "no results" is more honest
// than pretending a file name is a headword.
func (d *MDD) Exact(word string, limit int) ([]dict.Result, error)  { return nil, nil }
func (d *MDD) Prefix(word string, limit int) ([]dict.Result, error) { return nil, nil }

// Keywords lists the resource names, sorted, with the same (offset, n)
// contract as every other backend (dict.KeywordRange).
func (d *MDD) Keywords(offset, n int) []string {
	lo, hi, ok := dict.KeywordRange(len(d.names), offset, n)
	if !ok {
		return nil
	}
	return append([]string(nil), d.names[lo:hi]...)
}

// Resources lists every name, for the media packer.
func (d *MDD) Resources() []string { return append([]string(nil), d.names...) }

// Resource streams one item. Lookup is case-insensitive, matching how MDD
// keys are stored (case-preserving) against how articles reference them
// (loosely).
func (d *MDD) Resource(name string) (io.ReadCloser, string, error) {
	norm := strings.ToLower(strings.TrimLeft(path.Clean(name), "/"))
	if norm == "" || norm == "." || strings.HasPrefix(norm, "..") {
		return nil, "", dict.ErrNotFound
	}
	e, ok := d.items[norm]
	if !ok {
		return nil, "", dict.ErrNotFound
	}
	data, err := d.md.LocateByKeywordEntry(e)
	if err != nil {
		return nil, "", fmt.Errorf("mdd: locate %q: %w", name, err)
	}
	return io.NopCloser(bytes.NewReader(data)), mime.TypeByExtension(path.Ext(norm)), nil
}
