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
	"strconv"
	"strings"
	"sync"

	"github.com/glowinthedark/gonow-dict/internal/dict"
)

func init() {
	dict.RegisterFormat(".ifo", func(path string) (dict.Dictionary, error) { return Open(path) })
	dict.RegisterReader(".ifo", func(path string) (dict.Reader, error) { return NewReader(path) })
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
	meta     dict.Meta
	ifo      map[string]string
	sameType string
	entries  []idxEntry
	synonyms map[int][]string // idx entry -> synonym words (from .syn)
	exactIdx map[string][]int // headword or synonym -> entry indexes
	foldIdx  map[string][]int
	data     articleSource
	dictFile *os.File

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

	d.exactIdx = make(map[string][]int, len(d.entries))
	d.foldIdx = make(map[string][]int, len(d.entries))
	add := func(w string, i int) {
		d.exactIdx[w] = append(d.exactIdx[w], i)
		f := fold(w)
		d.foldIdx[f] = append(d.foldIdx[f], i)
	}
	for i, e := range d.entries {
		add(e.word, i)
	}
	for i, words := range d.synonyms {
		for _, w := range words {
			add(w, i)
		}
	}

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
	idxs := d.exactIdx[word]
	if len(idxs) == 0 {
		idxs = d.foldIdx[fold(word)]
	}
	return d.results(idxs, limit)
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
