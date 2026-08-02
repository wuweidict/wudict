// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Package mdx is the direct backend for Octopus MDict dictionaries: one
// .mdx plus its companion .mdd resource files (NAME.mdd, NAME.1.mdd, …).
//
// It adapts the vendored go-mdict block parser (internal/gomdict) to the
// dict.Dictionary interface. Compared to mdict-go-web's reader it indexes
// headwords in maps at open (O(1) exact/folded lookup instead of full
// scans) and streams .mdd resources directly instead of extracting them
// to an asset directory.
package mdx

import (
	"bytes"
	"fmt"
	"io"
	"mime"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"unicode"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"
	"golang.org/x/text/transform"

	"github.com/legbehindneck/wudict/internal/dict"
	gomdict "github.com/legbehindneck/wudict/internal/gomdict"
	"github.com/legbehindneck/wudict/internal/logx"
)

func init() {
	dict.RegisterFormat(".mdx", func(path string) (dict.Dictionary, error) { return Open(path) })
	dict.RegisterProber(".mdx", probe)
}

// probe reads name/description/entry-count from the MDX header only (no
// BuildIndex, no fold-maps) for the cheap dictionary-list path. It shares
// dictName with Open so both report the same display name.
func probe(filename string) (dict.Meta, error) {
	md, err := gomdict.New(filename)
	if err != nil {
		return dict.Meta{}, err
	}
	return dict.Meta{
		Name:        dictName(md, filename),
		Format:      "mdx",
		Path:        filename,
		Description: strings.TrimSpace(md.Description()),
		EntryCount:  int(md.EntryCount()),
	}, nil
}

const linkPrefix = "@@@LINK="

// reStyleTag matches `N` stylesheet markers embedded in record text.
var reStyleTag = regexp.MustCompile("`\\d+`")

type mddFile struct {
	md      *gomdict.Mdict
	entries []*gomdict.MDictKeywordEntry
}

type resHit struct {
	md    *gomdict.Mdict
	entry *gomdict.MDictKeywordEntry
}

// Dict is one opened MDX dictionary. Safe for concurrent readers after Open.
type Dict struct {
	meta       dict.Meta
	mdx        *gomdict.Mdict
	entries    []*gomdict.MDictKeywordEntry // headwords decoded to UTF-8, dictionary order
	exactOnce  sync.Once                    // exactIdx built on first exact lookup only
	exactIdx   map[string][]int             // headword -> entry indexes
	foldOnce   sync.Once                    // foldIdx built on first accent-folded lookup only
	foldIdx    map[string][]int             // folded headword -> entry indexes
	enc        int
	stylesheet map[string][2]string
	mdds       []mddFile

	resOnce sync.Once
	resMap  map[string]resHit
}

// Open opens an .mdx file and any companion .mdd files.
func Open(filename string) (*Dict, error) {
	md, entries, err := openIndexed(filename)
	if err != nil {
		return nil, err
	}
	enc := md.Encoding()
	d := &Dict{
		mdx:        md,
		entries:    entries,
		enc:        enc,
		stylesheet: parseStylesheet(md.StyleSheet()),
	}
	// Decode every headword to UTF-8 at open (entries carry the decoded key
	// everywhere). No headword index is built here — both the raw
	// (ensureExact) and accent-folded (ensureFold) indexes are lazy, so an open
	// that only serves resources or feeds the ingest scan builds nothing.
	// Runtime readers keep no in-memory headword index; our ingested SQLite
	// path is that index.
	for _, e := range entries {
		e.KeyWord = strings.TrimSpace(decodeEnc([]byte(e.KeyWord), enc))
	}
	d.meta = dict.Meta{
		Name:        dictName(md, filename),
		Format:      "mdx",
		Path:        filename,
		Description: strings.TrimSpace(md.Description()),
		EntryCount:  len(entries),
	}

	for _, f := range companionMdds(filename) {
		rmd, rents, err := openIndexed(f)
		if err != nil {
			logx.Warn("%smedia file %s cannot be read: %v — its audio and images will be missing",
				logx.Dict(d.meta.Name), filepath.Base(f), err)
			continue
		}
		d.mdds = append(d.mdds, mddFile{md: rmd, entries: rents})
	}
	return d, nil
}

func openIndexed(filename string) (*gomdict.Mdict, []*gomdict.MDictKeywordEntry, error) {
	md, err := gomdict.New(filename)
	if err != nil {
		return nil, nil, err
	}
	if err := md.BuildIndex(); err != nil {
		return nil, nil, err
	}
	entries, err := md.GetKeyWordEntries()
	if err != nil {
		return nil, nil, err
	}
	return md, entries, nil
}

// companionMdds lists NAME.mdd, NAME.1.mdd … NAME.N.mdd files that exist.
func companionMdds(mdxPath string) []string {
	noExt := strings.TrimSuffix(mdxPath, filepath.Ext(mdxPath))
	var out []string
	for _, f := range []string{noExt + ".mdd", noExt + ".1.mdd"} {
		if isFile(f) {
			out = append(out, f)
		}
	}
	for n := 2; ; n++ {
		f := fmt.Sprintf("%s.%d.mdd", noExt, n)
		if !isFile(f) {
			break
		}
		out = append(out, f)
	}
	return out
}

func (d *Dict) Meta() dict.Meta { return d.meta }

func (d *Dict) Caps() dict.Caps { return dict.Caps{Exact: true, Prefix: true} }

func (d *Dict) Close() error { return nil } // gomdict opens files per read

// Exact returns all entries whose headword equals word; if none, all
// case/accent-folded matches.
func (d *Dict) Exact(word string, limit int) ([]dict.Result, error) {
	word = strings.TrimSpace(word)
	d.ensureExact()
	idxs := d.exactIdx[word]
	if len(idxs) == 0 {
		d.ensureFold()
		idxs = d.foldIdx[fold(word)]
	}
	return d.results(idxs, word, limit), nil
}

// ensureExact builds the raw headword index on first lookup, so opens that only
// serve resources or feed the ingest scan never build it.
func (d *Dict) ensureExact() {
	d.exactOnce.Do(func() {
		idx := make(map[string][]int, len(d.entries))
		for i, e := range d.entries {
			idx[e.KeyWord] = append(idx[e.KeyWord], i)
		}
		d.exactIdx = idx
	})
}

// ensureFold builds the accent/case-folded headword index on first use,
// deferring the per-headword NFD fold that dominates open time and RAM.
func (d *Dict) ensureFold() {
	d.foldOnce.Do(func() {
		idx := make(map[string][]int, len(d.entries))
		for i, e := range d.entries {
			f := fold(e.KeyWord)
			idx[f] = append(idx[f], i)
		}
		d.foldIdx = idx
	})
}

// Prefix returns exact matches if any, else up to limit prefix matches
// (raw pass first, folded pass only when the raw pass is empty).
func (d *Dict) Prefix(word string, limit int) ([]dict.Result, error) {
	word = strings.TrimSpace(word)
	if r, _ := d.Exact(word, limit); len(r) > 0 {
		return r, nil
	}
	scan := func(useFold bool) []int {
		key := word
		if useFold {
			key = fold(word)
		}
		var idxs []int
		for i, e := range d.entries {
			hw := e.KeyWord
			if useFold {
				hw = fold(hw)
			}
			if strings.HasPrefix(hw, key) {
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
	return d.results(idxs, word, limit), nil
}

func (d *Dict) results(idxs []int, word string, limit int) []dict.Result {
	var out []dict.Result
	for _, i := range idxs {
		e := d.entries[i]
		seen := map[string]bool{e.KeyWord: true, word: true}
		for _, body := range d.render(e, seen) {
			out = append(out, dict.Result{Headword: e.KeyWord, Body: body})
		}
		if limit > 0 && len(out) >= limit {
			out = out[:limit]
			break
		}
	}
	return out
}

// render locates one record and converts it to HTML, following @@@LINK
// redirects (cycle-guarded). One entry can yield several bodies when a
// link target has homographs.
func (d *Dict) render(e *gomdict.MDictKeywordEntry, seen map[string]bool) []string {
	raw, err := d.mdx.LocateByKeywordEntry(e)
	if err != nil {
		logx.V("%sentry %q could not be read: %v", logx.Dict(d.meta.Name), e.KeyWord, err)
		return nil
	}
	body := strings.TrimSpace(strings.Trim(decodeEnc(raw, d.enc), "\x00"))
	if target, ok := strings.CutPrefix(body, linkPrefix); ok {
		target = strings.TrimSpace(strings.Trim(target, "\x00"))
		if target == "" || seen[target] {
			return nil
		}
		seen[target] = true
		var out []string
		d.ensureExact()
		idxs := d.exactIdx[target]
		if len(idxs) == 0 {
			d.ensureFold()
			idxs = d.foldIdx[fold(target)]
		}
		for _, i := range idxs {
			out = append(out, d.render(d.entries[i], seen)...)
		}
		return out
	}
	if len(d.stylesheet) > 0 {
		body = substituteStylesheet(body, d.stylesheet)
	}
	return []string{body}
}

func (d *Dict) Keywords(offset, n int) []string {
	if offset < 0 {
		offset = 0
	}
	if offset >= len(d.entries) {
		return nil
	}
	end := min(offset+n, len(d.entries))
	out := make([]string, 0, end-offset)
	for _, e := range d.entries[offset:end] {
		out = append(out, e.KeyWord)
	}
	return out
}

// Resource streams one .mdd resource. name uses forward slashes without a
// leading slash (e.g. "audio/word.mp3"); lookup is case-insensitive, as
// MDD keys are conventionally case-preserving but referenced loosely.
func (d *Dict) Resource(name string) (io.ReadCloser, string, error) {
	norm := strings.ToLower(strings.TrimLeft(path.Clean(name), "/"))
	if norm == "" || norm == "." || strings.HasPrefix(norm, "..") {
		return nil, "", dict.ErrNotFound
	}
	hit, ok := d.resourceIndex()[norm]
	if !ok {
		// Not packed: MDict also serves files sitting next to the .mdx, which
		// is how repacks ship their stylesheet and scripts (LDOCE6 keeps
		// LDOCE6.css and entry.js loose). Only reached on a MISS — the packed
		// path never touches the disk — and a stat costs ~1 µs against a
		// ~100 µs HTTP round trip, so this is free where it matters.
		return d.looseFile(name)
	}
	data, err := hit.md.LocateByKeywordEntry(hit.entry)
	if err != nil {
		return nil, "", fmt.Errorf("mdx: locate resource %q: %w", name, err)
	}
	return io.NopCloser(bytes.NewReader(data)), mime.TypeByExtension(path.Ext(norm)), nil
}

// looseAssetExt is what may be served from beside the .mdx. An article is
// third-party HTML: the traversal guard keeps a reference inside the
// dictionary's own folder, and this keeps it to things a dictionary legitimately
// loads, so a stray `href="secrets.env"` cannot turn the server into a file
// browser for that directory.
var looseAssetExt = map[string]bool{
	".css": true, ".js": true, ".html": true, ".htm": true,
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true,
	".svg": true, ".bmp": true, ".ico": true,
	".mp3": true, ".ogg": true, ".wav": true, ".spx": true, ".m4a": true, ".opus": true,
	".woff": true, ".woff2": true, ".ttf": true, ".otf": true, ".eot": true,
}

// looseFile serves a resource from the .mdx's own directory. The ORIGINAL name
// is used, not the lower-cased index key: on a case-sensitive filesystem
// "LDOCE6.css" is not "ldoce6.css".
func (d *Dict) looseFile(name string) (io.ReadCloser, string, error) {
	rel := path.Clean("/" + strings.ReplaceAll(name, "\\", "/"))[1:]
	if rel == "" || rel == "." {
		return nil, "", dict.ErrNotFound
	}
	if !looseAssetExt[strings.ToLower(path.Ext(rel))] {
		return nil, "", dict.ErrNotFound
	}
	dir := filepath.Dir(d.meta.Path)
	p := filepath.Join(dir, filepath.FromSlash(rel))
	// belt and braces: Clean above removes "..", this proves the result stayed
	// inside the dictionary's folder even through symlinked spellings
	if !strings.HasPrefix(p, dir+string(filepath.Separator)) {
		return nil, "", dict.ErrNotFound
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, "", dict.ErrNotFound
	}
	if fi, err := f.Stat(); err != nil || fi.IsDir() {
		f.Close()
		return nil, "", dict.ErrNotFound
	}
	return f, mime.TypeByExtension(path.Ext(rel)), nil
}

// Resources lists all .mdd resource names (lowercased, forward-slash).
func (d *Dict) Resources() []string {
	idx := d.resourceIndex()
	out := make([]string, 0, len(idx))
	for name := range idx {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// resourceIndex maps lowercased forward-slash resource paths to their mdd
// entry. MDD keys look like `\path\name`; first mdd file wins on duplicates.
func (d *Dict) resourceIndex() map[string]resHit {
	d.resOnce.Do(func() {
		m := make(map[string]resHit)
		for i := range d.mdds {
			md := &d.mdds[i]
			for _, e := range md.entries {
				key := strings.ReplaceAll(strings.TrimLeft(e.KeyWord, "\\/"), "\\", "/")
				k := strings.ToLower(key)
				if _, ok := m[k]; !ok {
					m[k] = resHit{md: md.md, entry: e}
				}
			}
		}
		d.resMap = m
	})
	return d.resMap
}

// ---- shared helpers (ported from mdict-go-web/reader.go) ----------------

// decodeEnc converts record bytes in the dictionary's native encoding to
// UTF-8 (go-mdict already decodes UTF-16 internally).
func decodeEnc(raw []byte, enc int) string {
	switch enc {
	case gomdict.EncodingUtf16, gomdict.EncodingUtf8:
		return string(raw)
	case gomdict.EncodingGb18030, gomdict.ENCODING_GBK, gomdict.ENCODING_GB2312:
		out, _, err := transform.Bytes(simplifiedchinese.GB18030.NewDecoder(), raw)
		if err != nil {
			return string(raw)
		}
		return string(out)
	case gomdict.EncodingBig5:
		out, _, err := transform.Bytes(traditionalchinese.Big5.NewDecoder(), raw)
		if err != nil {
			return string(raw)
		}
		return string(out)
	default:
		return string(raw)
	}
}

// fold delegates to the shared accent/case folding.
func fold(s string) string { return dict.Fold(s) }

// parseStylesheet parses the MDX StyleSheet header attribute: groups of
// three lines (number, begin-tag, end-tag).
func parseStylesheet(s string) map[string][2]string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	lines := strings.Split(strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n"), "\n")
	m := make(map[string][2]string)
	for i := 0; i+2 < len(lines); i += 3 {
		m[lines[i]] = [2]string{lines[i+1], lines[i+2]}
	}
	return m
}

// substituteStylesheet replaces `N` style markers with their begin/end
// tag pairs from the parsed StyleSheet header.
func substituteStylesheet(txt string, stylesheet map[string][2]string) string {
	parts := reStyleTag.Split(txt, -1)
	tags := reStyleTag.FindAllString(txt, -1)
	var b strings.Builder
	b.WriteString(parts[0])
	for j := 1; j < len(parts); j++ {
		key := strings.Trim(tags[j-1], "`")
		style, ok := stylesheet[key]
		if !ok {
			continue
		}
		p := parts[j]
		if len(p) > 0 && p[len(p)-1] == '\n' {
			b.WriteString(style[0])
			b.WriteString(strings.TrimRightFunc(p, unicode.IsSpace))
			b.WriteString(style[1])
			b.WriteString("\r\n")
		} else {
			b.WriteString(style[0])
			b.WriteString(p)
			b.WriteString(style[1])
		}
	}
	return b.String()
}

func isFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

func dictName(md *gomdict.Mdict, filename string) string {
	title := strings.TrimSpace(md.Title())
	if title == "Title (No HTML code allowed)" { // MdxBuilder placeholder
		title = ""
	}
	if title != "" {
		return title
	}
	base := filepath.Base(filename)
	return strings.TrimSuffix(base, filepath.Ext(base))
}
