// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Package bgl is the direct backend for Babylon BGL dictionaries. A BGL file
// is a small header followed by a gzip stream of typed blocks (metadata,
// word/definition entries, embedded resources). It has no random-access index,
// so — like the DSL backend — Open transparently ingests into a cached
// text.db; embedded resources are served from a lazily-scanned map.
//
// The block/entry/definition parser is ported from pyglossary's babylon_bgl
// plugin; the streaming (one block resident at a time, no whole-file
// decompression) follows GoldenDict's bgl_babylon reader for memory
// efficiency. Both trace back to the reverse engineering by Raul Fernandes and
// Karl Grill.
package bgl

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/wuweidict/wudict/internal/dict"
)

// entryTypes are the block types carrying word/definition entries.
func isEntryType(t byte) bool {
	switch t {
	case 1, 7, 10, 11, 13:
		return true
	}
	return false
}

// Reader is the sequential ingest scan over one .bgl file. Metadata is read in
// a first streaming pass at NewReader; entries are streamed on demand in Next,
// so at most one block is resident at a time regardless of dictionary size.
type Reader struct {
	path string
	meta dict.Meta

	sourceEncoding  string
	targetEncoding  string
	defaultEncoding string

	// metadata gathered during the first pass
	title          []byte
	numEntries     int
	sourceLang     *language
	targetLang     *language
	defaultCharset string
	sourceCharset  string
	targetCharset  string
	utf8Encoding   bool

	// id-keyed verdict (decideIDKey), reached in the metadata pass
	probes  []idProbe
	idKeyed bool

	// second-pass streaming state (opened lazily on first Next)
	f       *os.File
	br      *bufio.Reader
	started bool
	done    bool
}

var bglMagics = [][]byte{
	{0x12, 0x34, 0x00, 0x01},
	{0x12, 0x34, 0x00, 0x02},
}

// openStream validates the BGL header and returns a reader positioned at the
// start of the (decompressed) block stream. The caller closes f.
func openStream(path string) (f *os.File, br *bufio.Reader, err error) {
	f, err = os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	head := make([]byte, 6)
	if _, err = io.ReadFull(f, head); err != nil {
		f.Close()
		return nil, nil, fmt.Errorf("bgl %s: reading header: %w", filepath.Base(path), err)
	}
	ok := false
	for _, m := range bglMagics {
		if bytes.Equal(head[:4], m) {
			ok = true
			break
		}
	}
	if !ok {
		f.Close()
		return nil, nil, fmt.Errorf("bgl %s: not a Babylon BGL file", filepath.Base(path))
	}
	gzipOffset := uintBE(head[4:6])
	if gzipOffset < 6 {
		f.Close()
		return nil, nil, fmt.Errorf("bgl %s: invalid gzip offset %d", filepath.Base(path), gzipOffset)
	}
	if _, err = f.Seek(int64(gzipOffset), io.SeekStart); err != nil {
		f.Close()
		return nil, nil, err
	}
	gz, err := gzip.NewReader(f)
	if err != nil {
		f.Close()
		return nil, nil, fmt.Errorf("bgl %s: gzip header: %w", filepath.Base(path), err)
	}
	gz.Multistream(false)
	return f, bufio.NewReaderSize(gz, 1<<16), nil
}

// isStreamEnd reports whether an error ends the block stream cleanly. Many BGL
// files carry a zero/absent gzip CRC (a Babylon quirk), so a checksum or
// short-trailer error at the end is expected, not fatal.
func isStreamEnd(err error) bool {
	return err == io.EOF || err == gzip.ErrChecksum || err == io.ErrUnexpectedEOF
}

// readBlockStream reads one block header + data. ok=false with err==nil marks
// a clean end of stream (EOF or the type-4 end marker).
func readBlockStream(br *bufio.Reader) (typ byte, data []byte, ok bool, err error) {
	b, err := br.ReadByte()
	if err != nil {
		if isStreamEnd(err) {
			return 0, nil, false, nil
		}
		return 0, nil, false, err
	}
	typ = b & 0x0F
	n := int(b >> 4)
	var length int
	if n < 4 {
		buf := make([]byte, n+1)
		if _, err = io.ReadFull(br, buf); err != nil {
			return 0, nil, false, ignoreEnd(err)
		}
		length = uintBE(buf)
	} else {
		length = n - 4
	}
	if typ == 4 { // end-of-file marker
		return 0, nil, false, nil
	}
	if length < 0 {
		return 0, nil, false, nil
	}
	if length > 0 {
		data = make([]byte, length)
		if _, err = io.ReadFull(br, data); err != nil {
			return 0, nil, false, ignoreEnd(err)
		}
	}
	return typ, data, true, nil
}

func ignoreEnd(err error) error {
	if isStreamEnd(err) {
		return nil
	}
	return err
}

// NewReader opens a .bgl and runs the first streaming pass to gather metadata,
// count entries, and resolve the source/target encodings.
func NewReader(path string) (*Reader, error) {
	f, br, err := openStream(path)
	if err != nil {
		return nil, err
	}
	r := &Reader{path: path}
	for {
		typ, data, ok, err := readBlockStream(br)
		if err != nil {
			f.Close()
			return nil, fmt.Errorf("bgl %s: %w", filepath.Base(path), err)
		}
		if !ok {
			break
		}
		switch {
		case typ == 0:
			r.readType0(data)
		case typ == 3:
			r.readType3(data)
		case isEntryType(typ):
			r.numEntries++
			r.probeIDKey(typ, data)
		}
	}
	f.Close()

	r.detectEncoding()
	r.idKeyed = r.decideIDKey()
	r.probes = nil

	name := strings.Trim(strings.TrimSpace(decodeBytes(r.targetEncoding, r.title)), "\x00")
	if name == "" {
		base := filepath.Base(path)
		name = strings.TrimSuffix(base, filepath.Ext(base))
	}
	desc := ""
	if r.sourceLang != nil && r.targetLang != nil {
		desc = r.sourceLang.name + " → " + r.targetLang.name
	}
	r.meta = dict.Meta{
		Name:        name,
		Format:      "bgl",
		Path:        path,
		Description: desc,
		EntryCount:  r.numEntries,
	}
	return r, nil
}

func (r *Reader) Meta() dict.Meta { return r.meta }

func (r *Reader) Close() error {
	if r.f != nil {
		err := r.f.Close()
		r.f = nil
		return err
	}
	return nil
}

// Next streams and decodes the next word entry, skipping malformed ones. The
// second-pass stream is opened lazily so a Meta-only caller never pays for it.
func (r *Reader) Next() (dict.Entry, error) {
	if r.done {
		return dict.Entry{}, io.EOF
	}
	if !r.started {
		f, br, err := openStream(r.path)
		if err != nil {
			return dict.Entry{}, err
		}
		r.f, r.br, r.started = f, br, true
	}
	for {
		typ, data, ok, err := readBlockStream(r.br)
		if err != nil {
			r.done = true
			return dict.Entry{}, err
		}
		if !ok {
			r.done = true
			r.Close()
			return dict.Entry{}, io.EOF
		}
		if !isEntryType(typ) || len(data) == 0 {
			continue
		}
		raw, good := splitEntry(typ, data)
		if !good {
			continue
		}
		word, alts, defi := r.decodeEntry(raw)
		if word == "" {
			continue
		}
		return dict.Entry{
			Headwords: append([]string{word}, alts...),
			Body:      defi,
			Kind:      dict.BodyHTML,
		}, nil
	}
}

// readType0: block type 0, code 8 carries the default charset.
func (r *Reader) readType0(blk []byte) {
	if len(blk) >= 2 && blk[0] == 8 {
		if cs, ok := charsetByCode[blk[1]]; ok {
			r.defaultCharset = cs
		}
	}
}

// readType3: block type 3 carries glossary info (code in first 2 bytes).
func (r *Reader) readType3(blk []byte) {
	if len(blk) < 2 {
		return
	}
	code := uintBE(blk[:2])
	val := blk[2:]
	if len(val) == 0 {
		return
	}
	switch code {
	case 0x01:
		r.title = val
	case 0x07:
		if i := uintBE(val); i >= 0 && i < len(languageByCode) {
			r.sourceLang = &languageByCode[i]
		}
	case 0x08:
		if i := uintBE(val); i >= 0 && i < len(languageByCode) {
			r.targetLang = &languageByCode[i]
		}
	case 0x0C:
		r.numEntries = uintBE(val)
	case 0x11:
		r.utf8Encoding = uintBE(val)&0x8000 != 0
	case 0x1A:
		if cs, ok := charsetByCode[val[0]]; ok {
			r.sourceCharset = cs
		}
	case 0x1B:
		if cs, ok := charsetByCode[val[0]]; ok {
			r.targetCharset = cs
		}
	}
}

// detectEncoding resolves source/target encodings. Port of
// reader_meta.detectEncoding.
func (r *Reader) detectEncoding() {
	if r.defaultCharset != "" {
		r.defaultEncoding = r.defaultCharset
	} else {
		r.defaultEncoding = "cp1252"
	}
	switch {
	case r.utf8Encoding:
		r.sourceEncoding = "utf-8"
	case r.sourceCharset != "":
		r.sourceEncoding = r.sourceCharset
	case r.sourceLang != nil:
		r.sourceEncoding = r.sourceLang.encoding
	default:
		r.sourceEncoding = r.defaultEncoding
	}
	switch {
	case r.utf8Encoding:
		r.targetEncoding = "utf-8"
	case r.targetCharset != "":
		r.targetEncoding = r.targetCharset
	case r.targetLang != nil:
		r.targetEncoding = r.targetLang.encoding
	default:
		r.targetEncoding = r.defaultEncoding
	}
}

// rawEntry is one entry's byte fields, framed but not decoded. Framing needs
// no encoding, which is what lets the metadata pass sample entries before the
// source/target encodings are resolved.
type rawEntry struct {
	word []byte
	defi []byte
	alts [][]byte
}

func splitEntry(typ byte, data []byte) (rawEntry, bool) {
	if typ == 11 {
		return splitEntryType11(data)
	}
	return splitEntryStandard(data)
}

// splitEntryStandard frames a type 1/7/10/13 entry: 1-byte-len word,
// 2-byte-len definition, then a run of 1-byte-len alternates.
func splitEntryStandard(data []byte) (e rawEntry, ok bool) {
	pos := 0
	if pos+1 > len(data) {
		return e, false
	}
	wl := int(data[pos])
	pos++
	if pos+wl > len(data) {
		return e, false
	}
	e.word = data[pos : pos+wl]
	pos += wl

	if pos+2 > len(data) {
		return e, false
	}
	dl := uintBE(data[pos : pos+2])
	pos += 2
	if pos+dl > len(data) {
		return e, false
	}
	e.defi = data[pos : pos+dl]
	pos += dl

	for pos < len(data) {
		al := int(data[pos])
		pos++
		if pos+al > len(data) {
			break // malformed alt: keep the entry with what we have
		}
		e.alts = append(e.alts, data[pos:pos+al])
		pos += al
	}
	return e, true
}

// splitEntryType11 frames a type 11 entry: 5-byte-len word, 4-byte alt count,
// 4-byte-len alternates (terminated by a zero length), then 4-byte-len defi.
func splitEntryType11(data []byte) (e rawEntry, ok bool) {
	pos := 0
	if pos+5 > len(data) {
		return e, false
	}
	wl := uintBE(data[pos : pos+5])
	pos += 5
	if pos+wl > len(data) {
		return e, false
	}
	e.word = data[pos : pos+wl]
	pos += wl

	if pos+4 > len(data) {
		return e, false
	}
	altsCount := uintBE(data[pos : pos+4])
	pos += 4
	for k := 0; k < altsCount; k++ {
		if pos+4 > len(data) {
			return e, false
		}
		al := uintBE(data[pos : pos+4])
		pos += 4
		if al == 0 {
			break
		}
		if pos+al > len(data) {
			return e, false
		}
		e.alts = append(e.alts, data[pos:pos+al])
		pos += al
	}

	if pos+4 > len(data) {
		return e, false
	}
	dl := uintBE(data[pos : pos+4])
	pos += 4
	if pos+dl > len(data) {
		return e, false
	}
	e.defi = data[pos : pos+dl]
	return e, true
}

// decodeEntry renders one framed entry: headword, alternates, HTML definition.
func (r *Reader) decodeEntry(e rawEntry) (word string, alts []string, defi string) {
	f := r.collectDefiFields(e.defi)
	word = r.processKey(e.word)

	altSet := map[string]bool{}
	for _, a := range e.alts {
		if ua := r.processAlternativeKey(a); ua != "" {
			altSet[ua] = true
		}
	}

	// In an id-keyed dictionary the key is an internal identifier and the
	// definition's title field holds the actual word (decideIDKey). Swap
	// them: the id stays searchable as an alternate because articles link
	// by it, but it must not be what the reader is indexed and shown under.
	if r.idKeyed {
		if t := r.plainTitle(f.bTitle); t != "" && !strings.EqualFold(t, word) {
			if word != "" {
				altSet[word] = true
			}
			word = t
		}
	}
	delete(altSet, word)

	return word, sortedKeys(altSet), r.renderDefi(f)
}

// idProbe is one sampled entry, kept as raw bytes: the metadata pass can frame
// an entry but not decode it, and the verdict needs the whole file.
type idProbe struct {
	word  []byte
	title []byte
	alts  [][]byte
}

const (
	idProbeSample = 1000 // entries sampled from the head of the file
	idProbeMin    = 20   // below this a dictionary is too small to judge
	idProbeRatio  = 0.90 // share of the sample that must show the signature
)

// probeIDKey records one sampled entry's key, title and alternates. Only the
// small fields are copied, so a 1000-entry sample does not pin 1000
// definitions in memory.
func (r *Reader) probeIDKey(typ byte, data []byte) {
	if len(r.probes) >= idProbeSample {
		return
	}
	e, ok := splitEntry(typ, data)
	if !ok {
		return
	}
	f := r.collectDefiFields(e.defi)
	p := idProbe{word: bytes.Clone(e.word), title: bytes.Clone(f.bTitle)}
	for _, a := range e.alts {
		p.alts = append(p.alts, bytes.Clone(a))
	}
	r.probes = append(r.probes, p)
}

// decideIDKey reports whether this dictionary keys its entries by internal
// identifier rather than by word.
//
// Larousse's "Gran Diccionario de la Lengua Española" does: every key is a
// string like "E310420", the real headword sits in the definition's title
// field, and the word is repeated among the alternates. Left alone, all 86,916
// of its entries index and display under their id — headwords the user cannot
// read and the contains/full-text indexes, which cover the headword column
// only, cannot search. Its articles cross-link by id (bword://E310420), which
// is why the id is demoted to an alternate rather than dropped.
//
// The verdict is per-file and deliberately narrow: for nearly every sampled
// entry the title must exist, differ from the key, be one of that entry's own
// alternates, and the key must not be. Measured across an 18-file BGL corpus
// that separates the one id-keyed dictionary (99.0% of its sample) from every
// other (<=1.1%). Two dictionaries contain entries that match the rule
// individually — Merriam-Webster Collegiate's "A"/"Å", Larousse Compact's
// "about time"/"time" — and a per-entry rule would wrongly rewrite them; a
// whole-file rule ignores them.
func (r *Reader) decideIDKey() bool {
	if len(r.probes) < idProbeMin {
		return false
	}
	hits := 0
	for _, p := range r.probes {
		title := r.plainTitle(p.title)
		if title == "" {
			continue
		}
		key := r.processKey(p.word)
		if strings.EqualFold(title, key) {
			continue
		}
		keyIsAlt, titleIsAlt := false, false
		for _, a := range p.alts {
			switch alt := r.processAlternativeKey(a); {
			case strings.EqualFold(alt, key):
				keyIsAlt = true
			case strings.EqualFold(alt, title):
				titleIsAlt = true
			}
		}
		if titleIsAlt && !keyIsAlt {
			hits++
		}
	}
	return float64(hits) >= idProbeRatio*float64(len(r.probes))
}

// scanResources streams the file once and returns its embedded resources
// (type-2 blocks) as a lowercased-name → bytes map. Names are decoded with the
// resolved source encoding (they are conventionally ASCII); bytes are copied so
// the stream buffers are released.
func scanResources(path string) (map[string][]byte, []string, error) {
	f, br, err := openStream(path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()

	meta := &Reader{}
	type rawRes struct{ name, data []byte }
	var raw []rawRes
	for {
		typ, data, ok, err := readBlockStream(br)
		if err != nil {
			return nil, nil, err
		}
		if !ok {
			break
		}
		switch {
		case typ == 0:
			meta.readType0(data)
		case typ == 3:
			meta.readType3(data)
		case typ == 2 && len(data) >= 1:
			n := int(data[0])
			if 1+n > len(data) {
				continue
			}
			nameCp := append([]byte(nil), data[1:1+n]...)
			body := append([]byte(nil), data[1+n:]...)
			raw = append(raw, rawRes{name: nameCp, data: body})
		}
	}
	meta.detectEncoding()

	res := make(map[string][]byte, len(raw))
	var names []string
	for _, rr := range raw {
		name := strings.TrimSpace(decodeBytes(meta.sourceEncoding, rr.name))
		if name == "" {
			continue
		}
		key := strings.ToLower(name)
		if _, exists := res[key]; exists {
			continue
		}
		res[key] = rr.data
		names = append(names, name)
	}
	sort.Strings(names)
	return res, names, nil
}

// uintBE reads a big-endian unsigned integer from b (any width up to 8 bytes).
func uintBE(b []byte) int {
	n := 0
	for _, x := range b {
		n = n<<8 | int(x)
	}
	return n
}

func sortedKeys(m map[string]bool) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
