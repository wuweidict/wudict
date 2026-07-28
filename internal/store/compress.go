// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"bytes"
	"compress/flate"
	"io"
	"sync"
)

// Article bodies dominate a prepared dictionary: measured on a 40k-entry
// dictionary, the HTML in `entry.m` was 69 MB of a 72 MB database — the search
// indexes themselves were 2 MB. Storing that text verbatim is why a text.db
// could be several times the size of the file it came from.
//
// So bodies are DEFLATE-compressed per row. Per row, rather than in shared
// blocks like MDX/SLOB do, because a row must stay independently readable
// without a block cache; the cost is a worse ratio on very short articles,
// which is why a body is only stored compressed when compression actually
// wins.
//
// Discrimination is by a one-byte sentinel: article HTML never begins with
// NUL, so a body whose first byte is 0x00 is compressed and anything else is
// literal text. That keeps mixed rows legal — old databases stay readable with
// no migration and no schema version bump — and needs no per-database flag on
// the read path.
const compressedMark = 0x00

// compressBodies turns article compression on or off for subsequent ingests.
// Reading always understands both forms, so the setting only ever affects what
// is written next.
var compressBodies = true

// SetCompressBodies sets whether new ingests compress article bodies
// (config NO_COMPRESS / --no-compress turns it off).
func SetCompressBodies(on bool) { compressBodies = on }

// CompressBodies reports the current setting.
func CompressBodies() bool { return compressBodies }

var flateWriters = sync.Pool{New: func() any {
	w, _ := flate.NewWriter(io.Discard, 6) // 6: the usual size/speed balance
	return w
}}

// encodeBody returns what to store for one article body.
func encodeBody(s string) []byte {
	if !compressBodies || len(s) < 120 { // shorter than the deflate overhead pays back
		return []byte(s)
	}
	var buf bytes.Buffer
	buf.WriteByte(compressedMark)
	w := flateWriters.Get().(*flate.Writer)
	defer flateWriters.Put(w)
	w.Reset(&buf)
	if _, err := io.WriteString(w, s); err != nil {
		return []byte(s)
	}
	if err := w.Close(); err != nil {
		return []byte(s)
	}
	if buf.Len() >= len(s) { // incompressible: keep it readable
		return []byte(s)
	}
	return buf.Bytes()
}

// decodeBody reverses encodeBody, passing through anything stored literally
// (which includes every database written before compression existed).
func decodeBody(raw []byte) string {
	if len(raw) == 0 || raw[0] != compressedMark {
		return string(raw)
	}
	r := flate.NewReader(bytes.NewReader(raw[1:]))
	defer r.Close()
	var out bytes.Buffer
	if _, err := io.Copy(&out, r); err != nil {
		// a truncated or corrupt body must not take the whole query down;
		// return what inflated so the article degrades instead of vanishing
		return out.String()
	}
	return out.String()
}
