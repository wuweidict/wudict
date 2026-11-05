// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"bytes"
	"compress/flate"
	"database/sql"
	"errors"
	"io"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	kflate "github.com/klauspost/compress/flate"
	"github.com/klauspost/compress/zstd"

	"github.com/wuweidict/wudict/internal/sysmem"
)

// Article bodies dominate a prepared dictionary: measured on a 40k-entry
// dictionary, the HTML in `entry.m` was 69 MB of a 72 MB database - the search
// indexes themselves were 2 MB. So bodies are compressed, one row at a time:
// a row must stay readable on its own, without a block cache.
//
// The codec is a setting (D191). zstd is the default: each database carries
// one raw dictionary, evenly spaced 1 KB slices of its own first bodies (the
// Relative Lempel-Ziv sample, Hoobin, Puglisi & Zobel, PVLDB 2012), which
// gives every row the shared markup a lone row cannot. Measured on 19 whole
// databases: 0.72 of DEFLATE level 6, encoded with 0.78 of its CPU. DEFLATE
// and no compression stay selectable.
//
// The first byte of a stored body says what it is:
//
//	0x00         raw DEFLATE (RFC 1951)
//	0x01         a zstd frame without its 4-byte magic, decoded with the
//	             database's raw dictionary (table body_dict, id 0) - or with
//	             none when the table holds no row
//	0x02-0x08    reserved
//	anything else  literal text
//
// 0x01-0x08 are markers only in a schema-2 database, the one written with
// zstd; in schema 1 they are literal text, so no older database is misread. A
// body whose first byte is a marker byte of its database's schema is never
// stored literally, whatever its length or the setting: it is compressed, so
// a literal never looks like a marker.
const (
	markDeflate  = 0x00
	markZstd     = 0x01
	markReserved = 0x08 // the last reserved byte
)

// The schemas a text.db may have (PRAGMA user_version). 1 is DEFLATE and
// literal rows; 2 adds zstd rows and body_dict. media.db and the link cache
// stay at sidecarSchema whatever the text.db beside them is.
const (
	textSchemaMin = 1
	textSchemaMax = 2
	sidecarSchema = 1
)

// textSchemaOK reports whether this build reads a text.db of schema v.
func textSchemaOK(v int) bool { return v >= textSchemaMin && v <= textSchemaMax }

// Codec names, as COMPRESSION spells them (config.CodecZstd and its
// siblings; store does not import config).
const (
	codecZstd    = "zstd"
	codecDeflate = "deflate"
	codecNone    = "none"
)

// The smallest bodies worth compressing. A zstd row with a dictionary pays
// one marker byte and a frame header of a few bytes; a DEFLATE row has no
// dictionary to lean on, so a short body seldom shrinks.
const (
	zstdMinLen    = 16
	deflateMinLen = 120
)

// dictWindow is how much body text the writer holds before it cuts the
// dictionary (D191: a dictionary cut from the first 16 MiB was within 1.3
// points of one cut uniformly from the whole database, OED included). A
// variable so the tests can cut early.
var dictWindow int64 = 16 << 20

// dictSliceLen is the length of one dictionary slice.
const dictSliceLen = 1024

// codecSetting is what new ingests write. Set at start-up, before anything is
// prepared, like the memory limit.
var codecSetting = struct {
	name  string
	level int
}{codecZstd, 1}

// SetCodec sets how ingests started from now on store article text: name is
// "zstd", "deflate" or "none", level the codec's own (ignored for none).
// Reading understands every codec and level whatever the setting.
func SetCodec(name string, level int) {
	switch name {
	case codecZstd:
		level = max(level, 1)
	case codecDeflate:
		if level < 1 || level > 9 {
			level = 6
		}
	case codecNone:
		level = 0
	default:
		name, level = codecZstd, 1
	}
	codecSetting.name, codecSetting.level = name, level
}

// bodyCodec is one ingest's codec. For zstd the encoder exists only once the
// writer has cut the dictionary; until then encodeEntry keeps the text and the
// writer holds the rows (writer.hold).
type bodyCodec struct {
	name  string
	level int
	enc   atomic.Pointer[bodyEncoder]
}

func newBodyCodec() *bodyCodec {
	c := &bodyCodec{name: codecSetting.name, level: codecSetting.level}
	if c.name != codecZstd {
		c.enc.Store(newBodyEncoder(c.name, c.level, c.schema(), nil))
	}
	return c
}

// schema is the user_version of the database this codec writes.
func (c *bodyCodec) schema() int {
	if c.name == codecZstd {
		return 2
	}
	return 1
}

// label is the meta body_encoding: how the rows were written, for people.
func (c *bodyCodec) label() string {
	switch c.name {
	case codecZstd:
		return "zstd-" + strconv.Itoa(c.level)
	case codecDeflate:
		return "deflate-" + strconv.Itoa(c.level)
	}
	return "plain"
}

// dictCap is the largest dictionary for a zstd level: the fast tiers gain
// nothing past 256 KB, the others up to 1 MB (D191).
func dictCap(level int) int {
	if level >= 3 {
		return 1 << 20
	}
	return 256 << 10
}

// dictSize is 2% of the text, at least 8 KB, at most dictCap. text is the body
// text held; when the source did not end inside the window (eof false), the
// total is estimated from the source's entry count and the mean article, and
// a source that declares no count gets the cap.
func dictSize(level int, text int64, articles, entries int, eof bool) int {
	total := text
	if !eof {
		if entries <= 0 || articles <= 0 {
			return dictCap(level)
		}
		if est := text / int64(articles) * int64(entries); est > total {
			total = est
		}
	}
	return int(min(max(total/50, 8<<10), int64(dictCap(level))))
}

// sampleDict is the raw dictionary: n = size/chunk slices of chunk bytes, the
// i-th taken from body i*len/n at a spread offset. A short body contributes
// itself whole, so a dictionary of short articles comes out smaller than size.
func sampleDict(bodies []string, size, chunk int) []byte {
	n := size / chunk
	out := make([]byte, 0, size)
	for i := 0; i < n && len(bodies) > 0; i++ {
		b := bodies[i*len(bodies)/n]
		off := 0
		if len(b) > chunk {
			off = (i * 7919) % (len(b) - chunk)
		}
		out = append(out, b[off:min(len(b), off+chunk)]...)
	}
	return out[:min(len(out), size)]
}

// encoders is how many goroutines encode bodies at once in an urgent
// preparation (pipe), and so how many zstd encoders an ingest may need.
func encoders() int { return min(max(runtime.GOMAXPROCS(0)-1, 1), 8) }

// start creates the zstd encoder for dict, charging its memory to acct, and
// publishes it: from here on encodeEntry compresses.
func (c *bodyCodec) start(dict []byte, acct *sysmem.Account) (*bodyEncoder, error) {
	n := encoders()
	// Per encoder: the history (dictionary plus a block) and the match
	// tables, which klauspost copies from the dictionary's on every frame.
	per := int64(2*len(dict)) + 1<<20
	if c.level >= 3 {
		per += 3 << 20
	}
	if !acct.TryAcquire(int64(n) * per) {
		n = 1
		acct.TryAcquire(per)
	}
	opts := []zstd.EOption{
		zstd.WithEncoderLevel(zstd.EncoderLevelFromZstd(c.level)),
		zstd.WithEncoderConcurrency(n),
		zstd.WithEncoderCRC(false),
		zstd.WithSingleSegment(true),
	}
	if len(dict) > 0 {
		opts = append(opts, zstd.WithEncoderDictRaw(0, dict))
	}
	z, err := zstd.NewWriter(nil, opts...)
	if err != nil {
		return nil, err
	}
	e := newBodyEncoder(c.name, c.level, c.schema(), z)
	c.enc.Store(e)
	return e, nil
}

// close releases the zstd encoder, if one was made.
func (c *bodyCodec) close() {
	if e := c.enc.Load(); e != nil && e.zstd != nil {
		e.zstd.Close()
	}
}

// bodyEncoder turns one article's HTML into what is stored. Safe for
// concurrent use.
type bodyEncoder struct {
	name   string
	schema int
	zstd   *zstd.Encoder
	flate  *sync.Pool // *kflate.Writer at the DEFLATE level
}

func newBodyEncoder(name string, level, schema int, z *zstd.Encoder) *bodyEncoder {
	// none still needs DEFLATE for a body that must not be stored literally.
	fl := level
	if name != codecDeflate {
		fl = 1
	}
	return &bodyEncoder{name: name, schema: schema, zstd: z, flate: &sync.Pool{New: func() any {
		w, _ := kflate.NewWriter(io.Discard, fl)
		return w
	}}}
}

// reserved reports whether s would read as a marker in this schema.
func (e *bodyEncoder) reserved(s string) bool {
	return len(s) > 0 && (s[0] == markDeflate || e.schema >= 2 && s[0] <= markReserved)
}

// encode returns what to store for s: compressed when that is smaller, or
// when s must not be stored literally; s itself otherwise.
func (e *bodyEncoder) encode(s string) []byte {
	must := e.reserved(s)
	// A body the decoder could not read back (maxBody) is stored as DEFLATE,
	// which has no such cap.
	if e.name == codecZstd && (must || len(s) >= zstdMinLen) && len(s) <= maxBody {
		// Two allocations a row, as DEFLATE has: the text as a slice, and one
		// buffer the frame is written into straight after the marker, its
		// magic then dropped in place. A row that stays literal returns the
		// text slice it already has.
		src := []byte(s)
		out := e.zstd.EncodeAll(src, append(make([]byte, 0, len(s)/2+64), markZstd))
		if len(out) > 1+len(zstdMagic) && bytes.Equal(out[1:1+len(zstdMagic)], zstdMagic) {
			out = out[:1+copy(out[1:], out[1+len(zstdMagic):])]
			if must || len(out) < len(s) {
				return out
			}
			return src
		}
		// not a standard frame: never stored, and DEFLATE covers a must
	}
	if must || (e.name == codecDeflate || e.name == codecZstd && len(s) > maxBody) && len(s) >= deflateMinLen {
		if out, ok := e.deflate(s); ok && (must || len(out) < len(s)) {
			return out
		}
	}
	return []byte(s)
}

func (e *bodyEncoder) deflate(s string) ([]byte, bool) {
	var buf bytes.Buffer
	buf.WriteByte(markDeflate)
	w := e.flate.Get().(*kflate.Writer)
	defer e.flate.Put(w)
	w.Reset(&buf)
	if _, err := io.WriteString(w, s); err != nil {
		return nil, false
	}
	if err := w.Close(); err != nil {
		return nil, false
	}
	return buf.Bytes(), true
}

// zstdMagic opens every zstd frame; rows store the frame without it.
var zstdMagic = []byte{0x28, 0xb5, 0x2f, 0xfd}

// maxBody bounds one decoded zstd body, like the other decompression caps.
const maxBody = 64 << 20

// bodyDecoder reads the stored bodies of one database. The zstd decoder, and
// with it the dictionary, is made on the first zstd row: most opened
// dictionaries are searched for headwords and never read an article. It costs
// about the dictionary's size (measured: 270 KB a database read, 46 MB for 160),
// so it can be released under memory pressure (Store.ReleaseDecoder) and is
// made again by the next zstd row. Safe for concurrent use: a decode holds the
// read lock, so a release never closes a decoder that is in use.
type bodyDecoder struct {
	schema int
	db     *sql.DB // where body_dict is read from

	mu      sync.RWMutex
	zstd    *zstd.Decoder // nil until the first zstd row, and after a release
	weight  int64         // what the decoder holds, while it exists
	closed  bool
	lastUse atomic.Int64 // UnixNano of the last zstd row
}

func newBodyDecoder(schema int, db *sql.DB) *bodyDecoder {
	return &bodyDecoder{schema: schema, db: db}
}

// schema1 decodes bodies of a schema-1 database, which holds no zstd rows.
var schema1 = &bodyDecoder{schema: 1}

// decodeBody is schema1.decode.
func decodeBody(raw []byte) string { return schema1.decode(raw) }

// decode returns the article stored as raw. A row that does not decode
// degrades to what decoded, or to nothing; it never fails the query.
func (d *bodyDecoder) decode(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	switch b := raw[0]; {
	case b == markDeflate:
		return inflate(raw[1:])
	case d.schema >= 2 && b == markZstd:
		return d.unzstd(raw[1:])
	case d.schema >= 2 && b <= markReserved:
		return ""
	}
	return string(raw)
}

// unzstd decodes one frame. The decoder is used under the read lock; the row
// that has to make it decodes under the write lock it made it with, so a
// release in between can never leave that row without one. A decoder that
// could not be made is tried again by the next row: the failure may have been
// transient (a busy or interrupted read of body_dict).
func (d *bodyDecoder) unzstd(frame []byte) string {
	d.mu.RLock()
	if d.zstd != nil {
		defer d.mu.RUnlock()
		return d.decodeFrame(frame)
	}
	d.mu.RUnlock()
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.zstd == nil && !d.closed {
		d.init()
	}
	if d.zstd == nil {
		return ""
	}
	return d.decodeFrame(frame)
}

// maxPooledFrame bounds the buffers framePool keeps: one huge article must
// not stay resident in the pool.
const maxPooledFrame = 1 << 20

// decodeFrame decodes with d.zstd, which the caller holds a lock on.
func (d *bodyDecoder) decodeFrame(frame []byte) string {
	d.lastUse.Store(time.Now().UnixNano())
	bp := framePool.Get().(*[]byte)
	buf := append(append((*bp)[:0], zstdMagic...), frame...)
	out, err := d.zstd.DecodeAll(buf, nil)
	if cap(buf) <= maxPooledFrame {
		*bp = buf[:0]
		framePool.Put(bp)
	}
	if err != nil {
		return ""
	}
	return string(out)
}

// decoderOverhead is what a zstd decoder holds besides its dictionary
// (measured: about 270 KB a database against dictionaries of 200-260 KB).
const decoderOverhead = 64 << 10

// init makes the zstd decoder. Called with mu held for writing.
func (d *bodyDecoder) init() {
	var dict []byte
	if d.db != nil {
		err := d.db.QueryRow("SELECT data FROM body_dict WHERE id = 0").Scan(&dict)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return
		}
	}
	opts := []zstd.DOption{
		zstd.WithDecoderConcurrency(1),
		zstd.WithDecoderMaxMemory(maxBody),
		zstd.WithDecoderMaxWindow(maxBody),
	}
	if len(dict) > 0 {
		opts = append(opts, zstd.WithDecoderDictRaw(0, dict))
	}
	z, err := zstd.NewReader(nil, opts...)
	if err != nil {
		return
	}
	d.zstd, d.weight = z, int64(len(dict))+decoderOverhead
}

// held reports what the decoder holds now (0 when there is none) and when it
// last decoded a row.
func (d *bodyDecoder) held() (bytes, lastUse int64) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.zstd == nil {
		return 0, 0
	}
	return d.weight, d.lastUse.Load()
}

// release drops the zstd decoder and reports what it held; the next zstd row
// makes it again.
func (d *bodyDecoder) release() int64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	n := int64(0)
	if d.zstd != nil {
		d.zstd.Close()
		n = d.weight
	}
	d.zstd, d.weight = nil, 0
	return n
}

// close releases the decoder for good: no later row makes another.
func (d *bodyDecoder) close() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.zstd != nil {
		d.zstd.Close()
	}
	d.zstd, d.weight, d.closed = nil, 0, true
}

var framePool = sync.Pool{New: func() any { b := make([]byte, 0, 4096); return &b }}

// flateReaders hold DEFLATE readers for reuse: a new reader per row costs
// about a tenth of the decode time.
var flateReaders sync.Pool

func inflate(b []byte) string {
	br := bytes.NewReader(b)
	r, _ := flateReaders.Get().(io.ReadCloser)
	if r == nil {
		r = flate.NewReader(br)
	} else if err := r.(flate.Resetter).Reset(br, nil); err != nil {
		r = flate.NewReader(br)
	}
	var out bytes.Buffer
	// a truncated or corrupt body must not take the whole query down; return
	// what inflated so the article degrades instead of vanishing
	_, _ = io.Copy(&out, r)
	flateReaders.Put(r)
	return out.String()
}
