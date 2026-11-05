// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package zim

import (
	"bytes"
	"cmp"
	"io"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/wuweidict/wudict/internal/dict"
	"github.com/wuweidict/wudict/internal/lang"
)

// ReaderVersion is the behaviour version of this format's Reader (see
// dict.RegisterReaderVersion). Bump it in the same commit as any change to
// what the Reader yields, and update the golden in reader_golden_test.go.
const ReaderVersion = 4

func init() {
	// ".zimaa" is the first part of a split archive; the parts after it are
	// its companions (dict.ZIMParts) and are read as one file with it.
	for _, ext := range []string{".zim", ".zimaa"} {
		dict.RegisterFormat(ext, func(path string) (dict.Dictionary, error) { return Open(path) })
		dict.RegisterReader(ext, func(path string) (dict.Reader, error) { return NewReader(path) })
	}
	dict.RegisterReaderVersion("zim", ReaderVersion)
}

// Dict is one opened .zim (direct backend).
//
// Unlike every other direct backend in the tree it builds NO headword index,
// in memory or otherwise: the file's own path pointer list is sorted, so
// lookup is a binary search over the file (see search.go). Resident cost is
// the cluster pointer list plus a bounded cluster cache, which is why PreviewBytes
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

	titles *titleIndex // nil when the file has no usable title order
}

func Open(path string) (*Dict, error) {
	c, err := openContainer(path)
	if err != nil {
		return nil, err
	}
	d := &Dict{c: c, titles: c.titles()}
	d.lo, d.hi = c.nsRange(c.contentNS())

	name := strings.TrimSpace(c.metadata("Title"))
	nameFromName := name == ""
	if nameFromName {
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
	for _, k := range headerKeys {
		if k == "Name" && nameFromName {
			continue
		}
		if v := strings.TrimSpace(dict.DisplayText(c.metadata(k))); v != "" {
			d.meta.Header = append(d.meta.Header, dict.Field{Name: k, Value: v})
		}
	}
	return d, nil
}

// headerKeys are the openZIM metadata keys (wiki.openzim.org/wiki/Metadata)
// that hold text and are not already in Meta: Title, Description and Language
// are, Counter is bookkeeping and Illustration_* are images. Name is the
// machine name, shown unless it stood in for a missing Title.
var headerKeys = []string{
	"Name", "Creator", "Publisher", "Date", "LongDescription",
	"License", "Tags", "Relation", "Flavour", "Source", "Scraper",
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
	// each keyCache: its slots, and a key of a few dozen bytes in each
	const keyCacheBytes = int64(len(keyCache{})) * (8 + 64)
	return int64(len(d.c.clusterPtr)+len(d.c.nextStart))*8 + ptrPageLen*8 + clusterCacheBytes + 2*keyCacheBytes
}

// SelfIndexed marks a backend whose own file answers exact and prefix lookup
// at no resident cost, so the server has no reason to prepare it behind the
// user's back. For ZIM that matters twice over: preparation would also be a
// disk EXPANSION, because the source packs whole clusters with zstd (~14x)
// while a prepared text.db compresses one article at a time (D24, D191;
// measured with DEFLATE, ~3.5x: a 123 MB wiktionary became a ~431 MB
// text.db), and an 8 GB wikipedia would become something nobody asked for. Preparation stays available on
// request (the per-dictionary switches, `wudict ingest`); only the automatic
// path declines.
func (d *Dict) SelfIndexed() bool { return true }

// loadMeta reads the M/ namespace once, by name: Open asks for a dozen keys,
// and each was a binary search of its own. Two binary searches bound the
// namespace and one walk reads it, typically 15-30 entries. A namespace past
// maxMetaEntries, or one that cannot be read whole, is left to metadata's
// search.
func (c *container) loadMeta() {
	lo, hi := c.nsRange('M')
	if hi-lo > maxMetaEntries {
		return
	}
	meta := make(map[string]int, hi-lo)
	w := c.walk()
	for i := lo; i < hi; i++ {
		d, err := w.entry(i)
		if err != nil {
			return
		}
		meta[d.path] = i
	}
	c.meta = meta
}

// metadata reads one M/ namespace value, following a redirect as libzim
// does (FileImpl::findxMetadata). Metadata blobs are small; only a handful
// are read, all at open.
func (c *container) metadata(name string) string {
	i, ok := c.meta[name]
	if c.meta == nil {
		i, _, ok = c.find('M', name)
	}
	if !ok {
		return ""
	}
	_, d, err := c.resolve(i)
	if err != nil {
		return ""
	}
	b, err := c.content(d)
	if err != nil || len(b) > 1<<16 {
		return ""
	}
	return string(b)
}

// isArticle reports whether entry i is something a search may return: a
// stored HTML page, or a redirect that resolves to one. A redirect to an image
// or a stylesheet ("favicon" -> favicon.png in every zimwriterfs file) is not:
// listed by Keywords, it was a headword that Exact could not show.
func (c *container) isArticle(i int, d dirent) bool {
	if d.namespace != c.contentNS() {
		return false
	}
	if d.isRedirect() {
		_, t, err := c.resolve(i)
		return err == nil && c.htmlMIME[t.mimetype]
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
		if ok && d.c.isArticle(i, de) {
			idxs = append(idxs, i)
			break
		}
	}
	// A path that misses may carry a namespace - "A/Foo" from a pre-6.1 link
	// carried into a later file - and is looked up once more without it, as
	// libzim's Archive::getEntryByPath does.
	if len(idxs) == 0 && len(word) > 2 && word[1] == '/' {
		if i, de, ok := d.c.find(d.c.contentNS(), word[2:]); ok && d.c.isArticle(i, de) {
			idxs = append(idxs, i)
		}
	}
	if len(idxs) == 0 && d.titles != nil {
		ns := d.c.contentNS()
		for _, v := range titleVariants(word) {
			// Titles are not unique (an article and a redirect may share
			// one), so the run of equal titles is walked to its first article.
			d.titles.scan(ns, v, 16, func(i int, de dirent) bool {
				if de.headword() != v {
					return false
				}
				if de.namespace == ns && d.c.isArticle(i, de) {
					idxs = append(idxs, i)
					return false
				}
				return true
			})
			if len(idxs) > 0 {
				break
			}
		}
	}
	return d.results(idxs, limit)
}

// Prefix returns up to limit articles whose path starts with word, the exact
// one first.
//
// No exact short-circuit: an exact hit returned alone would make "starts with"
// hide the siblings of any headword typed in full. Nothing else is needed to
// put it on top - the content
// namespace is sorted, prefixScan starts at the key itself, and a string is a
// prefix of itself, so the exact entry is simply the scan's first hit.
func (d *Dict) Prefix(word string, limit int) ([]dict.Result, error) {
	word = strings.TrimSpace(word)
	if word == "" {
		return nil, nil
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
			if d.c.isArticle(i, de) {
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
	if len(idxs) == 0 && d.titles != nil {
		ns := d.c.contentNS()
		for _, v := range titleVariants(word) {
			d.titles.scan(ns, v, limit*4+64, func(i int, de dirent) bool {
				if de.namespace == ns && d.c.isArticle(i, de) {
					idxs = append(idxs, i)
				}
				return len(idxs) < limit
			})
			if len(idxs) > 0 {
				break
			}
		}
	}
	return d.results(idxs, limit)
}

// results renders entries, following redirects and dropping duplicates that
// resolve to the same article.
//
// Bodies are read in cluster order and returned in the order given. Hits come
// in path order, which scatters them over the file's clusters, and in that
// order the cluster cache misses on nearly every one: a 20-hit prefix search
// on wikipedia_en_climate_change_mini_2024-06 (2 MB clusters) decompressed a
// cluster per hit. In cluster order each cluster is decompressed once.
func (d *Dict) results(idxs []int, limit int) ([]dict.Result, error) {
	type hit struct {
		headword string
		de       dirent
		body     string
	}
	// A broken entry, redirect or cluster is one lost result, not a failed
	// search; the first error is reported only when it leaves nothing.
	var hits []hit
	var failed error
	seen := map[int]bool{}
	for _, i := range idxs {
		src, err := d.c.entry(i)
		if err != nil {
			failed = cmp.Or(failed, err)
			continue
		}
		tgt, de := i, src
		if src.isRedirect() {
			if tgt, de, err = d.c.resolve(i); err != nil {
				continue
			}
		}
		if seen[tgt] || !de.isContent() || !d.c.htmlMIME[de.mimetype] {
			continue
		}
		seen[tgt] = true
		hits = append(hits, hit{headword: src.headword(), de: de})
	}
	order := make([]int, len(hits))
	for k := range order {
		order[k] = k
	}
	sort.Slice(order, func(a, b int) bool {
		x, y := hits[order[a]].de, hits[order[b]].de
		if x.cluster != y.cluster {
			return x.cluster < y.cluster
		}
		return x.blob < y.blob
	})
	for _, k := range order {
		body, err := d.c.content(hits[k].de)
		if err != nil {
			failed = cmp.Or(failed, err)
			continue
		}
		if len(body) > 0 {
			hits[k].body = d.articleHTML(hits[k].de, body)
		}
	}
	var out []dict.Result
	for _, h := range hits {
		if h.body == "" {
			continue
		}
		out = append(out, dict.Result{Headword: h.headword, Body: h.body})
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	if len(out) == 0 && failed != nil {
		return nil, failed
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
	w := d.c.walk()
	for i := d.lo + lo; i < d.lo+hi; i++ {
		de, err := w.entry(i)
		if err != nil {
			break
		}
		if !d.c.isArticle(i, de) {
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
		if de.isRedirect() {
			var err error
			if _, de, err = d.c.resolve(i); err != nil {
				return nil, "", err
			}
		}
		if !de.isContent() {
			return nil, "", dict.ErrNotFound
		}
		rc, err := d.c.contentReader(de)
		if err != nil {
			return nil, "", err
		}
		return rc, d.c.mimeOf(de), nil
	}
	return nil, "", dict.ErrNotFound
}

// Resources lists the non-article blobs, for media packing. This walks the
// content namespace's dirents - one pread each, no decompression - which is
// the same shape of scan the other formats' Resources does.
//
// A name is the entry's path as an article references it once resolved
// (resolveRef): in a pre-6.1 file the namespace is part of it, "I/pic.png",
// so the packed name is the one the article asks for. Resource also takes
// the bare path, which an older library may hold.
func (d *Dict) Resources() []string {
	var out []string
	w := d.c.walk()
	for i := d.lo; i < d.hi; i++ {
		de, err := w.entry(i)
		if err != nil {
			break
		}
		if !de.isContent() || d.c.htmlMIME[de.mimetype] {
			continue
		}
		if strings.HasPrefix(mimeBase(d.c.mimeOf(de)), "text/plain") {
			continue
		}
		out = append(out, d.c.longPath(de))
	}
	if !d.c.newNamespaces() {
		for _, ns := range []byte{'I', '-', 'J'} {
			lo, hi := d.c.nsRange(ns)
			for i := lo; i < hi; i++ {
				de, err := w.entry(i)
				if err != nil {
					break
				}
				if de.isContent() {
					out = append(out, d.c.longPath(de))
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
