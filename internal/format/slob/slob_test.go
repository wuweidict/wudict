// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package slob

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/glowinthedark/gonow-dict/internal/dict"
)

// buildSlob writes a minimal valid .slob file: 2 content types, refs with
// aliases, one zlib bin. Mirrors pyglossary's writer layout, including an
// editable (255-padded) tag value.
func buildSlob(t *testing.T) string {
	t.Helper()
	var buf bytes.Buffer
	w := func(v any) {
		if err := binary.Write(&buf, binary.BigEndian, v); err != nil {
			t.Fatal(err)
		}
	}
	tiny := func(s string) {
		w(uint8(len(s)))
		buf.WriteString(s)
	}
	editableTiny := func(s string) { // length byte 255, NUL-padded to 255
		w(uint8(255))
		buf.WriteString(s)
		buf.Write(make([]byte, 255-len(s)))
	}
	text := func(s string) {
		w(uint16(len(s)))
		buf.WriteString(s)
	}

	buf.Write(magic)
	buf.Write(bytes.Repeat([]byte{0xAB}, 16)) // uuid
	tiny("utf-8")
	tiny("zlib")
	w(uint8(2)) // tags
	tiny("label")
	editableTiny("Test Slob Diccionario")
	tiny("created.by")
	editableTiny("gonow-test")
	w(uint8(2)) // content types
	text("text/html; charset=utf-8")
	text("image/png")

	// blobs: bin0: item0 = corazón html, item1 = png resource
	items := [][]byte{
		[]byte("<p><b>corazón</b> órgano muscular</p>"),
		{0x89, 'P', 'N', 'G'},
	}
	ctypeIDs := []byte{0, 1}
	var binContent bytes.Buffer
	pos := 0
	for _, it := range items {
		binary.Write(&binContent, binary.BigEndian, uint32(pos))
		pos += 4 + len(it)
	}
	for _, it := range items {
		binary.Write(&binContent, binary.BigEndian, uint32(len(it)))
		binContent.Write(it)
	}
	var compressed bytes.Buffer
	zw := zlib.NewWriter(&compressed)
	zw.Write(binContent.Bytes())
	zw.Close()

	// refs: corazón (blob 0/0), Corazón alias (same blob), img.png (0/1)
	type refDef struct {
		key      string
		bin      uint32
		item     uint16
		fragment string
	}
	refDefs := []refDef{
		{"corazón", 0, 0, ""},
		{"Corazón", 0, 0, ""},
		{"img.png", 0, 1, ""},
	}
	var refData bytes.Buffer
	var refPos []uint64
	for _, r := range refDefs {
		refPos = append(refPos, uint64(refData.Len()))
		binary.Write(&refData, binary.BigEndian, uint16(len(r.key)))
		refData.WriteString(r.key)
		binary.Write(&refData, binary.BigEndian, r.bin)
		binary.Write(&refData, binary.BigEndian, r.item)
		refData.WriteByte(uint8(len(r.fragment)))
		refData.WriteString(r.fragment)
	}

	w(uint32(len(items))) // blob_count

	// store: one item {count u32, ctids, zlen u32, data}
	var storeItem bytes.Buffer
	binary.Write(&storeItem, binary.BigEndian, uint32(len(items)))
	storeItem.Write(ctypeIDs)
	binary.Write(&storeItem, binary.BigEndian, uint32(compressed.Len()))
	storeItem.Write(compressed.Bytes())

	refsSection := 4 + 8*len(refDefs) + refData.Len()
	storeOffset := buf.Len() + 8 + 8 + refsSection
	storeSection := 4 + 8 + storeItem.Len()
	fileSize := storeOffset + storeSection

	w(uint64(storeOffset))
	w(uint64(fileSize))
	// refs
	w(uint32(len(refDefs)))
	for _, p := range refPos {
		w(p)
	}
	buf.Write(refData.Bytes())
	// store
	w(uint32(1))
	w(uint64(0))
	buf.Write(storeItem.Bytes())

	if buf.Len() != fileSize {
		t.Fatalf("builder math: wrote %d, declared %d", buf.Len(), fileSize)
	}
	p := filepath.Join(t.TempDir(), "test.slob")
	if err := os.WriteFile(p, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSyntheticSlob(t *testing.T) {
	d, err := Open(buildSlob(t))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	if d.Meta().Name != "Test Slob Diccionario" {
		t.Errorf("editable tag mis-read: %q", d.Meta().Name)
	}
	if d.Meta().EntryCount != 3 {
		t.Errorf("EntryCount = %d", d.Meta().EntryCount)
	}

	res, err := d.Exact("corazón", 10)
	if err != nil || len(res) != 1 || !bytes.Contains([]byte(res[0].Body), []byte("órgano")) {
		t.Fatalf("Exact: %v %v", res, err)
	}
	// alias ref to the same blob must dedupe, fold must resolve "corazon"
	res, err = d.Exact("corazon", 10)
	if err != nil || len(res) != 1 {
		t.Fatalf("folded exact: %v %v", res, err)
	}
	// resource blob is excluded from search results
	if res, _ := d.Exact("img.png", 10); len(res) != 0 {
		t.Errorf("resource leaked into search: %v", res)
	}
	// …but served via Resource with its content type
	rc, ctype, err := d.Resource("img.png")
	if err != nil || ctype != "image/png" {
		t.Fatalf("Resource: %v %v", ctype, err)
	}
	data, _ := io.ReadAll(rc)
	rc.Close()
	if !bytes.HasPrefix(data, []byte{0x89, 'P'}) {
		t.Errorf("resource bytes wrong: %v", data)
	}
	if _, _, err := d.Resource("missing.png"); err == nil {
		t.Error("missing resource must error")
	}

	// prefix
	res, err = d.Prefix("cora", 10)
	if err != nil || len(res) != 1 {
		t.Fatalf("Prefix: %v %v", res, err)
	}
}

func TestSyntheticSlobIngestReader(t *testing.T) {
	r, err := NewReader(buildSlob(t))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	e, err := r.Next()
	if err != nil {
		t.Fatal(err)
	}
	// blob 0/0 has two keys: display headword + alias
	if len(e.Headwords) != 2 || e.Headwords[0] != "corazón" || e.Headwords[1] != "Corazón" {
		t.Errorf("headwords: %v", e.Headwords)
	}
	if e.Kind != dict.BodyHTML {
		t.Errorf("kind: %v", e.Kind)
	}
	// resource blob skipped -> EOF
	if _, err := r.Next(); err != io.EOF {
		t.Errorf("want EOF after skipping resource, got %v", err)
	}
}

func TestOpenRejectsGarbage(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.slob")
	os.WriteFile(p, []byte("this is not a slob file at all"), 0o644)
	if _, err := Open(p); err == nil {
		t.Error("garbage must be rejected")
	}
}

// Integration against a real slob; skips unless GONOW_TEST_SLOB is set.
func TestIntegrationRealSlob(t *testing.T) {
	p := os.Getenv("GONOW_TEST_SLOB")
	if p == "" {
		t.Skip("GONOW_TEST_SLOB not set")
	}
	if _, err := os.Stat(p); err != nil {
		t.Skipf("%s not readable", p)
	}
	d, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	m := d.Meta()
	if m.EntryCount == 0 || m.Name == "" {
		t.Fatalf("bad meta: %+v", m)
	}
	keys := d.Keywords(m.EntryCount/3, 3)
	if len(keys) == 0 {
		t.Fatal("no keywords")
	}
	res, err := d.Exact(keys[0], 5)
	if err != nil {
		t.Fatalf("Exact(%q): %v", keys[0], err)
	}
	if len(res) == 0 {
		// mid-list key may be a resource; article keys must still resolve
		t.Logf("key %q yielded no article (resource?), acceptable", keys[0])
	}
}
