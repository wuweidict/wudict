// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Package stardict is the direct backend + ingest reader for StarDict
// dictionaries: NAME.ifo (metadata), NAME.idx[.gz] (sorted headword
// index), NAME.dict[.dz] (article data), optional NAME.syn (synonyms)
// and res/ dir or res.zip (resources). Layout ported from
// pyglossary/plugins/stardict/reader.py.
package stardict

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"fmt"
	"io"
	"mime"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/glowinthedark/gonow-dict/internal/dict"
)

func init() {
	dict.RegisterFormat(".ifo", func(path string) (dict.Dictionary, error) { return Open(path) })
	dict.RegisterReader(".ifo", func(path string) (dict.Reader, error) { return NewReader(path) })
	dict.RegisterProber(".ifo", probe)
}

type idxEntry struct {
	word   string
	offset uint64
	size   uint32
}

// articleSource serves (offset,size) ranges of the .dict data.
type articleSource interface {
	readRange(offset int64, size int) ([]byte, error)
}

type plainDict struct{ f *os.File }

func (p plainDict) readRange(offset int64, size int) ([]byte, error) {
	out := make([]byte, size)
	_, err := p.f.ReadAt(out, offset)
	return out, err
}

// Dict is one opened StarDict dictionary. Safe for concurrent readers.
type Dict struct {
	meta      dict.Meta
	ifo       map[string]string
	sameType  string
	entries   []idxEntry
	synonyms  map[int][]string // idx entry -> synonym words (from .syn)
	exactOnce sync.Once        // exactIdx is built on first exact lookup only
	exactIdx  map[string][]int // headword or synonym -> entry indexes
	foldOnce  sync.Once        // foldIdx is built on first accent-folded lookup only
	foldIdx   map[string][]int
	data      articleSource
	dictFile  *os.File

	basePath string // path without .ifo
	resOnce  sync.Once
	resZip   *zip.Reader
	resZipMu sync.Mutex
	resFiles map[string]*zip.File
}

// Open opens NAME.ifo and its companion files.
func Open(ifoPath string) (*Dict, error) {
	ifo, err := parseIfo(ifoPath)
	if err != nil {
		return nil, err
	}
	base := strings.TrimSuffix(ifoPath, filepath.Ext(ifoPath))
	d := &Dict{
		ifo:      ifo,
		sameType: ifo["sametypesequence"],
		basePath: base,
		synonyms: map[int][]string{},
	}

	offBits := 32
	if ifo["idxoffsetbits"] == "64" {
		offBits = 64
	}
	idxData, err := readMaybeGz(base+".idx", base+".idx.gz")
	if err != nil {
		return nil, fmt.Errorf("stardict %s: %w", ifoPath, err)
	}
	if d.entries, err = parseIdx(idxData, offBits); err != nil {
		return nil, fmt.Errorf("stardict %s: %w", ifoPath, err)
	}

	if synData, err := readMaybeGz(base+".syn", base+".syn.gz"); err == nil {
		parseSyn(synData, len(d.entries), d.synonyms)
	}

	// No headword index is built at open — both the raw (ensureExact) and the
	// accent-folded (ensureFold) indexes are built lazily on first use, so an
	// open only for resources or the ingest scan builds nothing. Runtime
	// readers (GoldenDict, aard2) keep no in-memory headword index at all; our
	// ingested SQLite path is that on-disk index.
	if err := d.openDictData(base); err != nil {
		return nil, fmt.Errorf("stardict %s: %w", ifoPath, err)
	}

	name := strings.TrimSpace(ifo["bookname"])
	if name == "" {
		name = filepath.Base(base)
	}
	d.meta = dict.Meta{
		Name:        name,
		Format:      "stardict",
		Path:        ifoPath,
		Description: strings.TrimSpace(ifo["description"]),
		EntryCount:  len(d.entries),
	}
	return d, nil
}

// probe reads bookname/wordcount from the tiny .ifo header only (no .idx
// load, no fold-maps) for the cheap dictionary-list path. Name mirrors
// Open's derivation so both report the same display name.
func probe(ifoPath string) (dict.Meta, error) {
	ifo, err := parseIfo(ifoPath)
	if err != nil {
		return dict.Meta{}, err
	}
	name := strings.TrimSpace(ifo["bookname"])
	if name == "" {
		name = filepath.Base(strings.TrimSuffix(ifoPath, filepath.Ext(ifoPath)))
	}
	n, _ := strconv.Atoi(strings.TrimSpace(ifo["wordcount"]))
	return dict.Meta{
		Name:        name,
		Format:      "stardict",
		Path:        ifoPath,
		Description: strings.TrimSpace(ifo["description"]),
		EntryCount:  n,
	}, nil
}

func (d *Dict) openDictData(base string) error {
	if f, err := os.Open(base + ".dict"); err == nil {
		d.dictFile = f
		d.data = plainDict{f: f}
		return nil
	}
	f, err := os.Open(base + ".dict.dz")
	if err != nil {
		return fmt.Errorf("neither .dict nor .dict.dz found")
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	dz, err := newDzReader(f, st.Size())
	if err != nil {
		f.Close()
		return fmt.Errorf(".dict.dz: %w", err)
	}
	d.dictFile = f
	d.data = dz
	return nil
}

func parseIfo(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	if len(lines) == 0 || !strings.Contains(lines[0], "StarDict's dict ifo file") {
		return nil, fmt.Errorf("%s: not a StarDict .ifo file", path)
	}
	m := map[string]string{}
	for _, ln := range lines[1:] {
		if k, v, ok := strings.Cut(ln, "="); ok {
			m[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return m, nil
}

func readMaybeGz(plain, gzPath string) ([]byte, error) {
	if data, err := os.ReadFile(plain); err == nil {
		return data, nil
	}
	f, err := os.Open(gzPath)
	if err != nil {
		return nil, fmt.Errorf("%s(.gz) not found", plain)
	}
	defer f.Close()
	gr, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	defer gr.Close()
	return io.ReadAll(gr)
}

// parseIdx decodes the sorted index: word\0 + offset (32/64-bit BE) +
// size (u32 BE) per entry.
func parseIdx(data []byte, offBits int) ([]idxEntry, error) {
	offSize := 4
	if offBits == 64 {
		offSize = 8
	}
	var out []idxEntry
	pos := 0
	for pos < len(data) {
		nul := bytes.IndexByte(data[pos:], 0)
		if nul < 0 {
			break
		}
		word := string(data[pos : pos+nul])
		pos += nul + 1
		if pos+offSize+4 > len(data) {
			return nil, fmt.Errorf("truncated idx at %q", word)
		}
		var off uint64
		if offBits == 64 {
			off = binary.BigEndian.Uint64(data[pos:])
		} else {
			off = uint64(binary.BigEndian.Uint32(data[pos:]))
		}
		size := binary.BigEndian.Uint32(data[pos+offSize:])
		pos += offSize + 4
		out = append(out, idxEntry{word: word, offset: off, size: size})
	}
	return out, nil
}

// parseSyn decodes NAME.syn: word\0 + u32 BE index into the idx entries.
func parseSyn(data []byte, entryCount int, into map[int][]string) {
	pos := 0
	for pos < len(data) {
		nul := bytes.IndexByte(data[pos:], 0)
		if nul < 0 || pos+nul+5 > len(data) {
			break
		}
		word := string(data[pos : pos+nul])
		idx := int(binary.BigEndian.Uint32(data[pos+nul+1:]))
		pos += nul + 5
		if idx >= 0 && idx < entryCount {
			into[idx] = append(into[idx], word)
		}
	}
}

func (d *Dict) Meta() dict.Meta { return d.meta }
func (d *Dict) Caps() dict.Caps { return dict.Caps{Exact: true, Prefix: true} }

func (d *Dict) Close() error {
	if d.dictFile != nil {
		return d.dictFile.Close()
	}
	return nil
}

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

// ensureExact builds the raw headword+synonym index on first lookup, so opens
// that only serve resources or feed the ingest scan never build it.
func (d *Dict) ensureExact() {
	d.exactOnce.Do(func() {
		idx := make(map[string][]int, len(d.entries))
		for i, e := range d.entries {
			idx[e.word] = append(idx[e.word], i)
		}
		for i, words := range d.synonyms {
			for _, w := range words {
				idx[w] = append(idx[w], i)
			}
		}
		d.exactIdx = idx
	})
}

// ensureFold builds the accent/case-folded headword index on first use. The
// per-headword NFD fold is the expensive part of opening, so deferring it keeps
// open fast and costs nothing for dictionaries that are only ever queried by
// exact (or ingested) headword.
func (d *Dict) ensureFold() {
	d.foldOnce.Do(func() {
		idx := make(map[string][]int, len(d.entries))
		for i, e := range d.entries {
			f := fold(e.word)
			idx[f] = append(idx[f], i)
		}
		for i, words := range d.synonyms {
			for _, w := range words {
				f := fold(w)
				idx[f] = append(idx[f], i)
			}
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
		for i, e := range d.entries {
			w := e.word
			if useFold {
				w = fold(w)
			}
			if strings.HasPrefix(w, key) {
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

func (d *Dict) results(idxs []int, limit int) ([]dict.Result, error) {
	var out []dict.Result
	seen := map[int]bool{}
	for _, i := range idxs {
		if seen[i] {
			continue
		}
		seen[i] = true
		body, err := d.article(i)
		if err != nil {
			return nil, err
		}
		out = append(out, dict.Result{Headword: d.entries[i].word, Body: body})
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

// article reads and renders one record to HTML.
func (d *Dict) article(i int) (string, error) {
	e := d.entries[i]
	raw, err := d.data.readRange(int64(e.offset), int(e.size))
	if err != nil {
		return "", fmt.Errorf("record %q: %w", e.word, err)
	}
	return recordToHTML(raw, d.sameType), nil
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
		out = append(out, e.word)
	}
	return out
}

// Resource serves files from res/ (dir) or res.zip beside the .ifo.
func (d *Dict) Resource(name string) (io.ReadCloser, string, error) {
	norm := strings.TrimLeft(path.Clean(name), "/")
	if norm == "" || norm == "." || strings.HasPrefix(norm, "..") {
		return nil, "", dict.ErrNotFound
	}
	dir := filepath.Join(filepath.Dir(d.basePath), "res")
	if st, err := os.Stat(dir); err == nil && st.IsDir() {
		f, err := os.Open(filepath.Join(dir, filepath.FromSlash(norm)))
		if err == nil {
			return f, mime.TypeByExtension(path.Ext(norm)), nil
		}
	}
	d.resOnce.Do(d.loadResZip)
	if d.resFiles != nil {
		if zf, ok := d.resFiles[strings.ToLower(norm)]; ok {
			d.resZipMu.Lock()
			rc, err := zf.Open()
			d.resZipMu.Unlock()
			if err != nil {
				return nil, "", err
			}
			return rc, mime.TypeByExtension(path.Ext(norm)), nil
		}
	}
	return nil, "", dict.ErrNotFound
}

// Resources lists res/ dir files (relative, forward-slash) and res.zip
// entries.
func (d *Dict) Resources() []string {
	seen := map[string]bool{}
	var out []string
	dir := filepath.Join(filepath.Dir(d.basePath), "res")
	filepath.WalkDir(dir, func(p string, de os.DirEntry, err error) error {
		if err != nil || de.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return nil
		}
		name := filepath.ToSlash(rel)
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
		return nil
	})
	d.resOnce.Do(d.loadResZip)
	for name := range d.resFiles {
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

func (d *Dict) loadResZip() {
	zr, err := zip.OpenReader(filepath.Join(filepath.Dir(d.basePath), "res.zip"))
	if err != nil {
		return
	}
	d.resFiles = make(map[string]*zip.File, len(zr.File))
	for _, f := range zr.File {
		d.resFiles[strings.ToLower(f.Name)] = f
	}
	d.resZip = &zr.Reader
}

func fold(s string) string { return dict.Fold(s) }

// ---- record rendering ----------------------------------------------------

// recordToHTML converts one .dict record to HTML. With sametypesequence
// the type of each part is fixed; otherwise each part is prefixed by its
// type char. Lowercase types are text (NUL-terminated unless last),
// uppercase types carry a u32 BE size prefix.
func recordToHTML(raw []byte, sameType string) string {
	var b strings.Builder
	if sameType != "" {
		rest := raw
		for ti := 0; ti < len(sameType) && len(rest) > 0; ti++ {
			t := sameType[ti]
			last := ti == len(sameType)-1
			part, tail := cutPart(rest, t, last)
			b.WriteString(partToHTML(t, part))
			rest = tail
		}
		return b.String()
	}
	rest := raw
	for len(rest) > 0 {
		t := rest[0]
		rest = rest[1:]
		part, tail := cutPart(rest, t, false)
		b.WriteString(partToHTML(t, part))
		rest = tail
	}
	return b.String()
}

// cutPart splits one typed part off the record.
func cutPart(data []byte, t byte, last bool) (part, rest []byte) {
	if t >= 'A' && t <= 'Z' { // uppercase: u32 size prefix
		if len(data) < 4 {
			return nil, nil
		}
		n := int(binary.BigEndian.Uint32(data))
		if 4+n > len(data) {
			n = len(data) - 4
		}
		return data[4 : 4+n], data[4+n:]
	}
	if last {
		return data, nil
	}
	if i := bytes.IndexByte(data, 0); i >= 0 {
		return data[:i], data[i+1:]
	}
	return data, nil
}

func partToHTML(t byte, data []byte) string {
	switch t {
	case 'h', 'g': // HTML / Pango markup (HTML-compatible subset)
		return strings.Trim(string(data), "\x00")
	case 'm', 'l', 't', 'y': // plain text / phonetics
		s := htmlEscape(strings.Trim(string(data), "\x00"))
		return "<p>" + strings.ReplaceAll(s, "\n", "<br/>") + "</p>"
	case 'x': // XDXF markup
		return xdxfToHTML(strings.Trim(string(data), "\x00"))
	case 'W', 'P', 'X': // media / unknown binary: not renderable inline
		return ""
	default:
		s := htmlEscape(strings.Trim(string(data), "\x00"))
		return "<p>" + s + "</p>"
	}
}

func htmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return r.Replace(s)
}

// wordCount reports the .ifo wordcount (used by tests).
func (d *Dict) wordCount() int {
	n, _ := strconv.Atoi(d.ifo["wordcount"])
	return n
}
