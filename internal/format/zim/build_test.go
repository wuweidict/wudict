// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package zim

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/ulikunitz/xz"
)

// A synthetic ZIM writer, so the reader is tested against bytes this file
// lays out explicitly rather than against a fixture nobody can read. It
// asserts its own offset arithmetic (see buildZIM), because a builder that
// silently writes a malformed file would make the reader's tests pass by
// agreeing with the same mistake.

type bEntry struct {
	ns      byte
	path    string
	title   string
	mime    string // "" marks a redirect
	body    []byte
	cluster int    // which cluster body goes in (ignored for redirects)
	target  string // redirect target, as "<ns>/<path>"
}

type bCluster struct {
	codec    byte
	extended bool
}

type bFile struct {
	major, minor uint16
	entries      []bEntry
	clusters     []bCluster
}

func compressWith(t *testing.T, codec byte, in []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	switch codec {
	case 0, 1:
		return in
	case 2:
		w := zlib.NewWriter(&buf)
		w.Write(in)
		w.Close()
	case 4:
		w, err := xz.NewWriter(&buf)
		if err != nil {
			t.Fatal(err)
		}
		w.Write(in)
		w.Close()
	case 5:
		w, err := zstd.NewWriter(&buf)
		if err != nil {
			t.Fatal(err)
		}
		w.Write(in)
		w.Close()
	default:
		t.Fatalf("builder cannot write codec %d", codec)
	}
	return buf.Bytes()
}

// buildZIM serialises a file and returns its bytes. Order on disk: header,
// MIME list, dirents, clusters, path pointers, cluster pointers, checksum.
func buildZIM(t *testing.T, f bFile) []byte {
	t.Helper()

	// Entries must be sorted by (namespace, path): the whole lookup path is a
	// binary search over that order, so a builder that emitted them unsorted
	// would be testing nothing.
	ents := append([]bEntry(nil), f.entries...)
	sort.Slice(ents, func(i, j int) bool {
		if ents[i].ns != ents[j].ns {
			return ents[i].ns < ents[j].ns
		}
		return ents[i].path < ents[j].path
	})
	index := map[string]int{}
	for i, e := range ents {
		index[string(e.ns)+"/"+e.path] = i
	}

	// MIME table, in first-seen order.
	var mimes []string
	mimeID := map[string]uint16{}
	for _, e := range ents {
		if e.mime == "" {
			continue
		}
		if _, ok := mimeID[e.mime]; !ok {
			mimeID[e.mime] = uint16(len(mimes))
			mimes = append(mimes, e.mime)
		}
	}
	if len(mimes) == 0 {
		t.Fatal("builder: no content entries")
	}

	// Blob slots per cluster, in entry order.
	blobOf := make([]uint32, len(ents))
	perCluster := make([][][]byte, len(f.clusters))
	for i, e := range ents {
		if e.mime == "" {
			continue
		}
		if e.cluster < 0 || e.cluster >= len(f.clusters) {
			t.Fatalf("builder: entry %q names cluster %d of %d", e.path, e.cluster, len(f.clusters))
		}
		blobOf[i] = uint32(len(perCluster[e.cluster]))
		perCluster[e.cluster] = append(perCluster[e.cluster], e.body)
	}

	var out bytes.Buffer
	out.Write(make([]byte, headerSize))

	mimeListPos := uint64(out.Len())
	for _, m := range mimes {
		out.WriteString(m)
		out.WriteByte(0)
	}
	out.WriteByte(0) // the empty string terminates the list

	// Dirents.
	direntPos := make([]uint64, len(ents))
	for i, e := range ents {
		direntPos[i] = uint64(out.Len())
		var hdr [16]byte
		if e.mime == "" {
			binary.LittleEndian.PutUint16(hdr[0:], mimeRedirect)
			hdr[3] = e.ns
			tgt, ok := index[e.target]
			if !ok {
				t.Fatalf("builder: entry %q redirects to unknown %q", e.path, e.target)
			}
			binary.LittleEndian.PutUint32(hdr[8:], uint32(tgt))
			out.Write(hdr[:12])
		} else {
			binary.LittleEndian.PutUint16(hdr[0:], mimeID[e.mime])
			hdr[3] = e.ns
			binary.LittleEndian.PutUint32(hdr[8:], uint32(e.cluster))
			binary.LittleEndian.PutUint32(hdr[12:], blobOf[i])
			out.Write(hdr[:16])
		}
		out.WriteString(e.path)
		out.WriteByte(0)
		out.WriteString(e.title)
		out.WriteByte(0)
	}

	// Clusters: info byte, then compress(offset table || blobs).
	clusterPos := make([]uint64, len(f.clusters))
	for ci, c := range f.clusters {
		clusterPos[ci] = uint64(out.Len())
		w := 4
		if c.extended {
			w = 8
		}
		blobs := perCluster[ci]
		var body bytes.Buffer
		off := uint64((len(blobs) + 1) * w)
		put := func(v uint64) {
			var b [8]byte
			binary.LittleEndian.PutUint64(b[:], v)
			body.Write(b[:w])
		}
		put(off)
		for _, b := range blobs {
			off += uint64(len(b))
			put(off)
		}
		if body.Len() != (len(blobs)+1)*w {
			t.Fatalf("builder: cluster %d offset table is %d bytes, want %d", ci, body.Len(), (len(blobs)+1)*w)
		}
		for _, b := range blobs {
			body.Write(b)
		}
		info := c.codec & 0x0F
		if c.extended {
			info |= 0x10
		}
		out.WriteByte(info)
		out.Write(compressWith(t, c.codec, body.Bytes()))
	}

	pathPtrPos := uint64(out.Len())
	for _, p := range direntPos {
		var b [8]byte
		binary.LittleEndian.PutUint64(b[:], p)
		out.Write(b[:])
	}
	clusterPtrPos := uint64(out.Len())
	for _, p := range clusterPos {
		var b [8]byte
		binary.LittleEndian.PutUint64(b[:], p)
		out.Write(b[:])
	}
	checksumPos := uint64(out.Len())
	out.Write(make([]byte, 16))

	b := out.Bytes()
	binary.LittleEndian.PutUint32(b[0:], zimMagic)
	binary.LittleEndian.PutUint16(b[4:], f.major)
	binary.LittleEndian.PutUint16(b[6:], f.minor)
	binary.LittleEndian.PutUint32(b[24:], uint32(len(ents)))
	binary.LittleEndian.PutUint32(b[28:], uint32(len(f.clusters)))
	binary.LittleEndian.PutUint64(b[32:], pathPtrPos)
	binary.LittleEndian.PutUint64(b[40:], ^uint64(0)) // no title index (>= 6.3)
	binary.LittleEndian.PutUint64(b[48:], clusterPtrPos)
	binary.LittleEndian.PutUint64(b[56:], mimeListPos)
	binary.LittleEndian.PutUint32(b[64:], 0)
	binary.LittleEndian.PutUint32(b[68:], ^uint32(0))
	binary.LittleEndian.PutUint64(b[72:], checksumPos)
	return b
}

func writeZIM(t *testing.T, f bFile) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "test.zim")
	if err := os.WriteFile(p, buildZIM(t, f), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// sample is the fixture most tests open: a 6.3 new-namespace file whose three
// clusters cover an uncompressed table, a zstd one, and 8-byte (extended)
// offsets.
func sample() bFile {
	page := func(title, bodyHTML string) []byte {
		return []byte(`<!DOCTYPE html><html><head><title>` + title +
			`</title><link rel="stylesheet" href="./_res_/a.css"><link rel="icon" href="./f.png"></head>` +
			`<body class="x">` + bodyHTML + `</body></html>`)
	}
	return bFile{
		major: 6, minor: 3,
		clusters: []bCluster{{codec: 1}, {codec: 5}, {codec: 1, extended: true}},
		entries: []bEntry{
			{ns: 'C', path: "_res_/a.css", mime: "text/css", body: []byte("b{color:red}"), cluster: 2},
			{ns: 'C', path: "cat", title: "cat", mime: "text/html",
				body: page("cat", `<a rel="mw:WikiLink" href="odrasl%C4%83">x</a><img src="f.png">`), cluster: 0},
			{ns: 'C', path: "New_York", mime: "text/html", body: page("New York", "big"), cluster: 1},
			{ns: 'C', path: "catalog", mime: "text/html", body: page("catalog", "list"), cluster: 1},
			{ns: 'C', path: "f.png", mime: "image/png", body: []byte("\x89PNG\r\n\x1a\n"), cluster: 2},
			{ns: 'C', path: "kitty", mime: "", target: "C/cat"},
			{ns: 'M', path: "Counter", mime: "text/plain", body: []byte("text/html=3;image/png=1"), cluster: 0},
			{ns: 'M', path: "Description", mime: "text/plain", body: []byte("A test file"), cluster: 0},
			{ns: 'M', path: "Language", mime: "text/plain", body: []byte("ron"), cluster: 0},
			{ns: 'M', path: "Title", mime: "text/plain", body: []byte("Test ZIM"), cluster: 0},
		},
	}
}
