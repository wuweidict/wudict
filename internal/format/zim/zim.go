// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package zim

import (
	"bytes"
	"io"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/wuweidict/wudict/internal/dict"
	"github.com/wuweidict/wudict/internal/lang"
)

func init() {
	dict.RegisterFormat(".zim", func(path string) (dict.Dictionary, error) { return Open(path) })
	dict.RegisterReader(".zim", func(path string) (dict.Reader, error) { return NewReader(path) })
}

// Dict is one opened .zim (direct backend).
//
// Unlike every other direct backend in the tree it builds NO headword index,
// in memory or otherwise: the file's own path pointer list is sorted, so
// lookup is a binary search over the file (see search.go). Resident cost is
// the pointer list plus a bounded cluster cache, which is why PreviewBytes
// below reports a real figure instead of letting the server's per-headword
// estimate charge half a gigabyte for an index that does not exist.
//
// Safe for concurrent readers.
type Dict struct {
	c    *container
	meta dict.Meta

	// The content namespace's entry range, resolved once at open by two
	// binary searches. It is both what Keywords browses and what EntryCount
	// counts, so the two can never disagree.
	lo, hi int
}

func Open(path string) (*Dict, error) {
	c, err := openContainer(path)
	if err != nil {
		return nil, err
	}
	d := &Dict{c: c}
	d.lo, d.hi = c.nsRange(c.contentNS())

	name := strings.TrimSpace(c.metadata("Title"))
	if name == "" {
		name = strings.TrimSpace(c.metadata("Name"))
	}
	if name == "" {
		base := filepath.Base(path)
		name = strings.TrimSuffix(base, filepath.Ext(base))
	}
	d.meta = dict.Meta{
		Name:        dict.DisplayText(name),
		Format:      "zim",
		Path:        path,
		Description: dict.DisplayText(strings.TrimSpace(c.metadata("Description"))),
		EntryCount:  d.hi - d.lo,
		IndexLang:   lang.FromDeclared(c.metadata("Language")),
	}
	return d, nil
}

func (d *Dict) Meta() dict.Meta { return d.meta }
func (d *Dict) Close() error    { return d.c.Close() }

// Caps is exact and prefix only, and stays that way in preview on purpose.
// Accent-folded, substring and full-text matching all need an index keyed on
// something other than raw byte order - which is exactly the resident map this
// backend exists to avoid building. They arrive with preparation, as for every
// other format.
func (d *Dict) Caps() dict.Caps { return dict.Caps{Exact: true, Prefix: true} }

// PreviewBytes reports what this backend actually keeps resident, so the
// server's headword-count estimate (previewWeight) does not charge it for an
// in-memory index it never builds.
func (d *Dict) PreviewBytes() int64 {
	return int64(len(d.c.urlPtr))*8 + clusterCacheBytes
}

// SelfIndexed marks a backend whose own file answers exact and prefix lookup
// at no resident cost, so the server has no reason to prepare it behind the
// user's back. For ZIM that matters twice over: preparation would also be a
// disk EXPANSION, because the source packs whole clusters with zstd (~14x)
// while a prepared text.db compresses one article at a time with DEFLATE
// (~3.5x, D24) - a 123 MB wiktionary becomes a ~431 MB text.db, and an 8 GB
// wikipedia something nobody asked for. Preparation stays available on
// request (the per-dictionary switches, `wudict ingest`); only the automatic
// path declines.
func (d *Dict) SelfIndexed() bool { return true }

// metadata reads one M/ namespace value. Metadata blobs are small and each
// costs one binary search plus a cluster read; only a handful are read, all at
// open.
func (c *container) metadata(name string) string {
	_, d, ok := c.find('M', name)
	if !ok || d.isRedirect() {
		return ""
	}
	b, err := c.content(d)
	if err != nil || len(b) > 1<<16 {
		return ""
	}
	return string(b)
}

// isArticle reports whether an entry is something a search may return: a
// stored HTML page, or a redirect (whose target is resolved before rendering).
func (c *container) isArticle(d dirent) bool {
	if d.namespace != c.contentNS() {
		return false
	}
	if d.isRedirect() {
		return true
	}
	return c.htmlMIME[d.mimetype]
}

func (d *Dict) Exact(word string, limit int) ([]dict.Result, error) {
	word = strings.TrimSpace(word)
	if word == "" {
		return nil, nil
	}
	var idxs []int
	for _, v := range pathVariants(word) {
		i, de, ok := d.c.find(d.c.contentNS(), v)
		if ok && d.c.isArticle(de) {
			idxs = append(idxs, i)
			break
		}
	}
	return d.results(idxs, limit)
}

func (d *Dict) Prefix(word string, limit int) ([]dict.Result, error) {
	word = strings.TrimSpace(word)
	if word == "" {
		return nil, nil
	}
	if res, err := d.Exact(word, limit); err != nil || len(res) > 0 {
		return res, err
	}
	if limit <= 0 {
		limit = 100
	}
	var idxs []int
	for _, v := range pathVariants(word) {
		// The budget is deliberately larger than the caller's limit: a prefix
		// can sit in front of entries this rejects (a stylesheet, an image),
		// and it must be able to walk past them without walking the namespace.
		budget := limit*4 + 64
		err := d.c.prefixScan(d.c.contentNS(), v, budget, func(i int, de dirent) bool {
			if d.c.isArticle(de) {
				idxs = append(idxs, i)
			}
			return len(idxs) < limit
		})
		if err != nil {
			return nil, err
		}
		if len(idxs) > 0 {
			break
		}
	}
	return d.results(idxs, limit)
}

// results renders entries, following redirects and dropping duplicates that
// resolve to the same article.
func (d *Dict) results(idxs []int, limit int) ([]dict.Result, error) {
	var out []dict.Result
	seen := map[int]bool{}
	for _, i := range idxs {
		src, err := d.c.entry(i)
		if err != nil {
			return nil, err
		}
		tgt, de, err := d.c.resolve(i)
		if err != nil {
			continue // a broken redirect is one bad entry, not a failed search
		}
		if seen[tgt] || !d.c.htmlMIME[de.mimetype] {
			continue
		}
		seen[tgt] = true
		body, err := d.c.content(de)
		if err != nil {
			return nil, err
		}
		if len(body) == 0 {
			continue
		}
		out = append(out, dict.Result{Headword: src.headword(), Body: d.articleHTML(body)})
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

// Keywords browses the content namespace in path order.
//
// The file's title-ordered listing (X/listing/titleOrdered/v1) is deliberately
// NOT used: it lives inside a compressed cluster, so consulting it would turn
// a browse into a multi-megabyte decompression, and it covers a different set
// of entries than EntryCount reports. Path order is byte order over the same
// range the count comes from, and since a path is its title with spaces
// written as '_', the two orders barely differ.
func (d *Dict) Keywords(offset, n int) []string {
	lo, hi, ok := dict.KeywordRange(d.hi-d.lo, offset, n)
	if !ok {
		return nil
	}
	out := make([]string, 0, hi-lo)
	for i := d.lo + lo; i < d.lo+hi; i++ {
		de, err := d.c.entry(i)
		if err != nil {
			break
		}
		if !d.c.isArticle(de) {
			continue
		}
		out = append(out, de.headword())
	}
	return out
}

// Resource serves any stored blob by its path. New-scheme files keep every
// resource in the content namespace alongside the articles; pre-6.1 files
// split them across I (images), - (layout) and J, so those are tried too.
func (d *Dict) Resource(name string) (io.ReadCloser, string, error) {
	name = strings.TrimPrefix(trimRelative(name), "/")
	if name == "" {
		return nil, "", dict.ErrNotFound
	}
	nss := []byte{d.c.contentNS()}
	if !d.c.newNamespaces() {
		nss = append(nss, 'I', '-', 'J')
		// A pre-6.1 article points at its picture as "../I/pic.png", and that
		// spelling is what reaches us: the namespace is a segment of the
		// reference but NOT part of the stored path, so searching for
		// "I/pic.png" finds nothing and every image in every old ZIM 404s.
		// A leading single-letter segment naming a namespace we would search
		// anyway is that prefix, and it says which namespace to search.
		if len(name) > 2 && name[1] == '/' && bytes.IndexByte(nss, name[0]) >= 0 {
			nss, name = []byte{name[0]}, name[2:]
		}
	}
	for _, ns := range nss {
		i, de, ok := d.c.find(ns, name)
		if !ok {
			continue
		}
		_, de, err := d.c.resolve(i)
		if err != nil {
			return nil, "", err
		}
		b, err := d.c.content(de)
		if err != nil {
			return nil, "", err
		}
		return io.NopCloser(bytes.NewReader(b)), d.c.mimeOf(de), nil
	}
	return nil, "", dict.ErrNotFound
}

// Resources lists the non-article blobs, for media packing. This walks the
// content namespace's dirents - one pread each, no decompression - which is
// the same shape of scan the other formats' Resources does.
func (d *Dict) Resources() []string {
	var out []string
	for i := d.lo; i < d.hi; i++ {
		de, err := d.c.entry(i)
		if err != nil {
			break
		}
		if de.isRedirect() || d.c.htmlMIME[de.mimetype] {
			continue
		}
		if strings.HasPrefix(mimeBase(d.c.mimeOf(de)), "text/plain") {
			continue
		}
		out = append(out, de.path)
	}
	if !d.c.newNamespaces() {
		for _, ns := range []byte{'I', '-', 'J'} {
			lo, hi := d.c.nsRange(ns)
			for i := lo; i < hi; i++ {
				de, err := d.c.entry(i)
				if err != nil {
					break
				}
				if !de.isRedirect() {
					out = append(out, de.path)
				}
			}
		}
	}
	return out
}

// articleCount is what the file itself claims, used only to sanity-check the
// namespace walk in tests and diagnostics. M/Counter is a
// "mime=count;mime=count" list; a missing or malformed entry reports 0.
func (c *container) articleCount() int {
	for _, part := range strings.Split(c.metadata("Counter"), ";") {
		i := strings.LastIndexByte(part, '=')
		if i < 0 || !isHTMLMIME(part[:i]) {
			continue
		}
		if n, err := strconv.Atoi(strings.TrimSpace(part[i+1:])); err == nil {
			return n
		}
	}
	return 0
}
