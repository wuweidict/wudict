// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package slob

import (
	"bytes"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/legbehindneck/wudict/internal/dict"
)

func init() {
	dict.RegisterFormat(".slob", func(path string) (dict.Dictionary, error) { return Open(path) })
	dict.RegisterReader(".slob", func(path string) (dict.Reader, error) { return NewReader(path) })
}

// Dict is one opened .slob dictionary (direct backend). Lookup runs over
// an in-memory copy of the ref list with exact/fold maps — no ICU
// collation needed (see docs/FORMATS.md). Safe for concurrent readers.
type Dict struct {
	c         *container
	meta      dict.Meta
	exactOnce sync.Once // exactIdx is built on first exact lookup only
	exactIdx  map[string][]int
	foldOnce  sync.Once // foldIdx is built on first accent-folded lookup only
	foldIdx   map[string][]int
}

func Open(path string) (*Dict, error) {
	c, err := openContainer(path)
	if err != nil {
		return nil, err
	}
	// No headword index is built at open — neither exact nor folded. Both are
	// built lazily on first use (see ensureExact/ensureFold), so opening a slob
	// only for its resources (native path) or for the ingest scan never pays
	// for a lookup index. The official aard2/GoldenDict readers keep no
	// in-memory headword index at all; our ingested SQLite path is that index.
	d := &Dict{c: c}
	name := strings.TrimSpace(c.tags["label"])
	if name == "" {
		base := filepath.Base(path)
		name = strings.TrimSuffix(base, filepath.Ext(base))
	}
	d.meta = dict.Meta{
		Name:        name,
		Format:      "slob",
		Path:        path,
		Description: strings.TrimSpace(c.tags["copyright"]),
		EntryCount:  len(c.refs),
	}
	return d, nil
}

func (d *Dict) Meta() dict.Meta { return d.meta }
func (d *Dict) Caps() dict.Caps { return dict.Caps{Exact: true, Prefix: true} }
func (d *Dict) Close() error    { return d.c.Close() }

// Tags exposes the raw slob tag map (label, uri, license…).
func (d *Dict) Tags() map[string]string { return d.c.tags }

func (d *Dict) Exact(word string, limit int) ([]dict.Result, error) {
	word = strings.TrimSpace(word)
	d.ensureExact()
	idxs := d.exactIdx[word]
	if len(idxs) == 0 {
		d.ensureFold()
		idxs = d.foldIdx[fold(word)]
	}
	return d.results(idxs, limit)
}

// ensureExact builds the raw headword index on first lookup, so opens that only
// serve resources or feed the ingest scan never build it.
func (d *Dict) ensureExact() {
	d.exactOnce.Do(func() {
		idx := make(map[string][]int, len(d.c.refs))
		for i, r := range d.c.refs {
			idx[r.key] = append(idx[r.key], i)
		}
		d.exactIdx = idx
	})
}

// ensureFold builds the accent/case-folded ref index on first use, deferring
// the per-headword NFD fold that dominates open time and RAM.
func (d *Dict) ensureFold() {
	d.foldOnce.Do(func() {
		idx := make(map[string][]int, len(d.c.refs))
		for i, r := range d.c.refs {
			f := fold(r.key)
			idx[f] = append(idx[f], i)
		}
		d.foldIdx = idx
	})
}

func (d *Dict) Prefix(word string, limit int) ([]dict.Result, error) {
	word = strings.TrimSpace(word)
	if res, err := d.Exact(word, limit); err != nil || len(res) > 0 {
		return res, err
	}
	scan := func(useFold bool) []int {
		key := word
		if useFold {
			key = fold(word)
		}
		var idxs []int
		for i, r := range d.c.refs {
			k := r.key
			if useFold {
				k = fold(k)
			}
			if strings.HasPrefix(k, key) {
				idxs = append(idxs, i)
				if len(idxs) >= limit {
					break
				}
			}
		}
		return idxs
	}
	idxs := scan(false)
	if len(idxs) == 0 {
		idxs = scan(true)
	}
	return d.results(idxs, limit)
}

// results renders the referenced blobs, keeping only article content
// types (resources are reachable via Resource, not search).
func (d *Dict) results(idxs []int, limit int) ([]dict.Result, error) {
	var out []dict.Result
	seen := map[[2]uint32]bool{} // dedupe alias refs to the same blob
	for _, i := range idxs {
		r := d.c.refs[i]
		blobKey := [2]uint32{r.bin, uint32(r.item)}
		if seen[blobKey] {
			continue
		}
		ctype, data, err := d.c.getItem(r.bin, r.item)
		if err != nil {
			return nil, err
		}
		body, ok := articleHTML(ctype, data)
		if !ok {
			continue
		}
		seen[blobKey] = true
		out = append(out, dict.Result{Headword: r.key, Body: body})
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

// articleHTML converts blob content to HTML when it is article-like.
func articleHTML(ctype string, data []byte) (string, bool) {
	switch {
	case strings.HasPrefix(ctype, "text/html"):
		return string(data), true
	case strings.HasPrefix(ctype, "text/plain"):
		return "<p>" + strings.ReplaceAll(htmlEscape(string(data)), "\n", "<br/>") + "</p>", true
	default:
		return "", false
	}
}

func htmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return r.Replace(s)
}

func fold(s string) string { return dict.Fold(s) }

func (d *Dict) Keywords(offset, n int) []string {
	lo, hi, ok := dict.KeywordRange(len(d.c.refs), offset, n)
	if !ok {
		return nil
	}
	out := make([]string, 0, hi-lo)
	for _, r := range d.c.refs[lo:hi] {
		out = append(out, r.key)
	}
	return out
}

// Resource serves any blob by its ref key (slob stores resources as
// ordinary refs with non-article content types).
func (d *Dict) Resource(name string) (io.ReadCloser, string, error) {
	// ensureExact/ensureFold, not a bare map read. Both indexes are built
	// lazily on first use, and this read used to assume some earlier lookup
	// had already triggered that — so on a Dict opened only to serve files it
	// consulted two nil maps and reported every resource missing. That is the
	// normal case for a PREPARED dictionary: searches are answered from
	// text.db, and the slob is reopened solely as the resource fallback
	// (registry.go's upgraded.src), where no lookup ever runs.
	d.ensureExact()
	idxs := d.exactIdx[name]
	if len(idxs) == 0 {
		d.ensureFold()
		idxs = d.foldIdx[fold(name)]
	}
	if len(idxs) == 0 {
		return nil, "", dict.ErrNotFound
	}
	r := d.c.refs[idxs[0]]
	ctype, data, err := d.c.getItem(r.bin, r.item)
	if err != nil {
		return nil, "", err
	}
	return io.NopCloser(bytes.NewReader(data)), ctype, nil
}

// Resources lists ref keys whose content type is not article-like
// (images, audio, css, …), reading only bin headers.
func (d *Dict) Resources() []string {
	var out []string
	for _, r := range d.c.refs {
		ctype, err := d.c.itemContentType(r.bin, r.item)
		if err != nil {
			continue
		}
		if strings.HasPrefix(ctype, "text/html") || strings.HasPrefix(ctype, "text/plain") {
			continue
		}
		out = append(out, r.key)
	}
	sort.Strings(out)
	return out
}

// ---- ingest --------------------------------------------------------------

// Reader scans blobs bin-by-bin (sequential decompression — refs order
// would thrash bins) and attaches every ref key of a blob as headwords:
// first key = display headword, the rest become aliases.
type Reader struct {
	d          *Dict
	keysByBlob map[[2]uint32][]string
	blobs      [][2]uint32 // bin/item in ascending bin order
	pos        int
}

func NewReader(path string) (*Reader, error) {
	d, err := Open(path)
	if err != nil {
		return nil, err
	}
	r := &Reader{d: d, keysByBlob: map[[2]uint32][]string{}}
	for _, rf := range d.c.refs {
		k := [2]uint32{rf.bin, uint32(rf.item)}
		if _, ok := r.keysByBlob[k]; !ok {
			r.blobs = append(r.blobs, k)
		}
		keys := r.keysByBlob[k]
		if len(keys) == 0 || keys[0] != rf.key { // skip fragment-only duplicates
			if !contains(keys, rf.key) {
				keys = append(keys, rf.key)
			}
		}
		r.keysByBlob[k] = keys
	}
	sortBlobs(r.blobs)
	return r, nil
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

// sortBlobs orders by bin then item so bins decompress once each.
func sortBlobs(b [][2]uint32) {
	sort.Slice(b, func(i, j int) bool {
		if b[i][0] != b[j][0] {
			return b[i][0] < b[j][0]
		}
		return b[i][1] < b[j][1]
	})
}

func (r *Reader) Meta() dict.Meta { return r.d.Meta() }

func (r *Reader) Next() (dict.Entry, error) {
	for r.pos < len(r.blobs) {
		bk := r.blobs[r.pos]
		r.pos++
		ctype, data, err := r.d.c.getItem(bk[0], uint16(bk[1]))
		if err != nil {
			return dict.Entry{}, fmt.Errorf("slob ingest blob %v: %w", bk, err)
		}
		body, ok := articleHTML(ctype, data)
		if !ok {
			continue // resource blob: stays lazy in the source file
		}
		return dict.Entry{Headwords: r.keysByBlob[bk], Body: body, Kind: dict.BodyHTML}, nil
	}
	return dict.Entry{}, io.EOF
}

func (r *Reader) Close() error { return r.d.Close() }
