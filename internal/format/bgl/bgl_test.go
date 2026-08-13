// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package bgl

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/wuweidict/wudict/internal/dict"
)

// block encodes one BGL block using the 4-byte explicit-length header form
// (high nibble 3 → the length is the next 4 big-endian bytes).
func block(typ byte, data []byte) []byte {
	out := []byte{0x30 | typ}
	var l [4]byte
	binary.BigEndian.PutUint32(l[:], uint32(len(data)))
	out = append(out, l[:]...)
	return append(out, data...)
}

func info3(code uint16, val []byte) []byte {
	b := make([]byte, 2)
	binary.BigEndian.PutUint16(b, code)
	return block(3, append(b, val...))
}

// stdEntry builds a type-1 entry: 1-byte word len, 2-byte defi len, alts.
func stdEntry(word, defi string, alts ...string) []byte {
	var b []byte
	b = append(b, byte(len(word)))
	b = append(b, word...)
	var dl [2]byte
	binary.BigEndian.PutUint16(dl[:], uint16(len(defi)))
	b = append(b, dl[:]...)
	b = append(b, defi...)
	for _, a := range alts {
		b = append(b, byte(len(a)))
		b = append(b, a...)
	}
	return block(1, b)
}

// type11Entry builds a type-11 entry (5-byte word len, 4-byte alt count, 4-byte defi len).
func type11Entry(word, defi string) []byte {
	var b []byte
	var wl [5]byte
	putUint40(wl[:], len(word))
	b = append(b, wl[:]...)
	b = append(b, word...)
	b = append(b, 0, 0, 0, 0) // altsCount = 0
	var dl [4]byte
	binary.BigEndian.PutUint32(dl[:], uint32(len(defi)))
	b = append(b, dl[:]...)
	b = append(b, defi...)
	return block(11, b)
}

func putUint40(b []byte, v int) {
	for i := 4; i >= 0; i-- {
		b[i] = byte(v & 0xFF)
		v >>= 8
	}
}

func resourceBlock(name, data string) []byte {
	b := []byte{byte(len(name))}
	b = append(b, name...)
	b = append(b, data...)
	return block(2, b)
}

// writeBGL assembles a valid .bgl file (English/English → cp1252).
func writeBGL(t *testing.T, dir string) string {
	t.Helper()
	var stream []byte
	stream = append(stream, info3(0x01, []byte("Test BGL"))...) // title
	stream = append(stream, info3(0x07, []byte{0, 0, 0, 0})...) // sourceLang English
	stream = append(stream, info3(0x08, []byte{0, 0, 0, 0})...) // targetLang English
	stream = append(stream, block(0, []byte{8, 0x42})...)       // default charset cp1252
	stream = append(stream, stdEntry("apple", "<b>a fruit</b>", "apples")...)
	stream = append(stream, type11Entry("banana", "<i>yellow</i>")...)
	stream = append(stream, resourceBlock("pic.png", "PNGDATA")...)

	var gzbuf bytes.Buffer
	zw := gzip.NewWriter(&gzbuf)
	zw.Write(stream)
	zw.Close()

	var file bytes.Buffer
	file.Write([]byte{0x12, 0x34, 0x00, 0x02, 0x00, 0x06}) // magic + gzipOffset=6
	file.Write(gzbuf.Bytes())

	p := filepath.Join(dir, "test.bgl")
	if err := os.WriteFile(p, file.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestReaderParsesEntriesAndResources(t *testing.T) {
	p := writeBGL(t, t.TempDir())
	r, err := NewReader(p)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	if m := r.Meta(); m.Name != "Test BGL" || m.Format != "bgl" {
		t.Fatalf("meta: %+v", m)
	}
	if r.sourceEncoding != "cp1252" || r.targetEncoding != "cp1252" {
		t.Errorf("encoding: src=%q tgt=%q", r.sourceEncoding, r.targetEncoding)
	}

	var got []dict.Entry
	for {
		e, err := r.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, e)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 entries, got %d: %+v", len(got), got)
	}
	if got[0].Headwords[0] != "apple" || got[0].Body != "<b>a fruit</b>" {
		t.Errorf("entry 0: %+v", got[0])
	}
	if len(got[0].Headwords) != 2 || got[0].Headwords[1] != "apples" {
		t.Errorf("entry 0 alts: %+v", got[0].Headwords)
	}
	if got[1].Headwords[0] != "banana" || got[1].Body != "<i>yellow</i>" {
		t.Errorf("entry 1: %+v", got[1])
	}

	// resources (embedded type-2 block) — streamed separately
	res, names, err := scanResources(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 || names[0] != "pic.png" {
		t.Fatalf("resources: %v", names)
	}
	if string(res["pic.png"]) != "PNGDATA" {
		t.Errorf("resource data: %q", res["pic.png"])
	}
}

// TestOpenAutoIngestAndLookup exercises the full direct-backend path: Open
// auto-ingests into a text.db, then exact/alias lookups and embedded-resource
// serving must work.
func TestOpenAutoIngestAndLookup(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WUDICT_DB_DIR", t.TempDir())
	p := writeBGL(t, dir)

	d, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	// BGL prepares itself so it can be searched at all, but only the cheap
	// headword index: full-text is the user's choice, not a toll for opening
	// the file (D24). Finding must work; full-text must not be assumed.
	if c := d.Caps(); !c.Exact || !c.Prefix {
		t.Errorf("bgl must be searchable after auto-preparation: %+v", c)
	}
	if c := d.Caps(); c.FTS || c.Contains {
		t.Errorf("bgl must not build the opt-in indexes by itself: %+v", c)
	}
	if res, err := d.Exact("apple", 5); err != nil || len(res) != 1 || res[0].Body != "<b>a fruit</b>" {
		t.Fatalf("exact apple: %v %v", res, err)
	}
	if res, _ := d.Exact("apples", 5); len(res) != 1 { // alias
		t.Errorf("alias apples: %v", res)
	}
	if res, _ := d.Exact("banana", 5); len(res) != 1 {
		t.Errorf("exact banana: %v", res)
	}
	rc, _, err := d.Resource("pic.png")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(rc)
	rc.Close()
	if string(b) != "PNGDATA" {
		t.Errorf("resource: %q", b)
	}
}
