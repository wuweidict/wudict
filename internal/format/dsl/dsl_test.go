// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package dsl

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/glowinthedark/gonow-dict/internal/dict"
)

func TestTransformBody(t *testing.T) {
	cases := []struct{ in, want string }{
		{`[b]bold[/b]`, `<b>bold</b>`},
		{`[i]it[/i] [u]un[/u] [sup]s[/sup]`, `<i>it</i> <u>un</u> <sup>s</sup>`},
		{`[c]green[/c]`, `<font color="green">green</font>`},
		{`[c darkred]x[/c]`, `<font color="darkred">x</font>`},
		{`[m2]indent[/m]`, `<p style="padding-left:2em;margin:0">indent</p>`},
		{`[ex]sample[/ex]`, `<span class="ex"><font color="steelblue">sample</font></span>`},
		{`a [ref]target[/ref]`, `a <a href="bword://target">target</a>`},
		{`<<other>>`, `<a href="bword://other">other</a>`},
		{`[url]example.com[/url]`, `<a href="http://example.com">example.com</a>`},
		{`[p]adj.[/p]`, `<i class="p"><font color="green">adj.</font></i>`},
		{`x {{comment}} y`, `x  y`},
		{`[trn]kept[/trn]`, `kept`},
		{`5 &lt; 6? a<b`, `5 &amp;lt; 6? a&lt;b`},
		{`literal ([ ]) brackets`, `literal ([ ]) brackets`}, // corchete case
		{`[unknowntag]inner[/unknowntag]`, `inner`},
	}
	for _, c := range cases {
		got, _, err := transformBody(c.in, "KEY")
		if err != nil {
			t.Errorf("transformBody(%q) error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("transformBody(%q)\n got %q\nwant %q", c.in, got, c.want)
		}
	}
}

func TestTransformBodyUTF8(t *testing.T) {
	got, _, err := transformBody(`[b]corazón[/b] órgano`, "corazón")
	if err != nil || got != `<b>corazón</b> órgano` {
		t.Errorf("utf8 mangled: %q err=%v", got, err)
	}
	got, _, _ = transformBody(`véase ~`, "corazón")
	if got != `véase corazón` {
		t.Errorf("tilde subst: %q", got)
	}
}

func TestTransformMedia(t *testing.T) {
	got, res, _ := transformBody(`[s]audio/x.mp3[/s][s]img.png[/s]`, "")
	if !strings.Contains(got, `data="audio/x.mp3"`) || !strings.Contains(got, `<img align="top" src="img.png"`) {
		t.Errorf("media html: %q", got)
	}
	if len(res) != 2 || res[0] != "audio/x.mp3" || res[1] != "img.png" {
		t.Errorf("resFiles: %v", res)
	}
}

func TestTransformTitle(t *testing.T) {
	tr := transformTitle(`abandonar(se)`)
	if tr.Full != "abandonar(se)"[:9]+"se" || tr.Alt != "abandonar" {
		t.Errorf("parens: %+v", tr)
	}
	tr = transformTitle(`word {[i]extra[/i]}`)
	if tr.Full != "word" || !strings.Contains(tr.Display, "<i>extra</i>") {
		t.Errorf("curly: %+v", tr)
	}
	tr = transformTitle(`corazón`)
	if tr.Full != "corazón" || tr.Alt != "corazón" {
		t.Errorf("utf8 title: %+v", tr)
	}
	tr = transformTitle(`a\(b`)
	if tr.Full != "a(b" {
		t.Errorf("escaped paren: %+v", tr)
	}
}

const sampleDSL = "#NAME \"Mini Diccionario\"\n" +
	"#INDEX_LANGUAGE \"Spanish\"\n" +
	"#CONTENTS_LANGUAGE \"Spanish\"\n" +
	"\n" +
	"corazón\n" +
	"\t[b]1.[/b] órgano muscular\n" +
	"\t[b]2.[/b] centro de algo\n" +
	"\n" +
	"amar(se)\n" +
	"\tsentir amor: [i]véase ~[/i]\n" +
	"\n" +
	"casa\n" +
	"\tvivienda\n" +
	"\t@ casa rural\n" +
	"\tvivienda en el campo\n" +
	"\t@\n"

func writeDSL(t *testing.T, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func utf16le(s string) []byte {
	var b bytes.Buffer
	b.Write([]byte{0xFF, 0xFE})
	for _, r := range s {
		if r < 0x10000 {
			binary.Write(&b, binary.LittleEndian, uint16(r))
		} else {
			r -= 0x10000
			binary.Write(&b, binary.LittleEndian, uint16(0xD800+(r>>10)))
			binary.Write(&b, binary.LittleEndian, uint16(0xDC00+(r&0x3FF)))
		}
	}
	return b.Bytes()
}

func readAllEntries(t *testing.T, path string) []dict.Entry {
	t.Helper()
	r, err := NewReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if r.Meta().Name != "Mini Diccionario" {
		t.Fatalf("meta name: %+v", r.Meta())
	}
	var out []dict.Entry
	for {
		e, err := r.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, e)
	}
	return out
}

func checkEntries(t *testing.T, entries []dict.Entry) {
	t.Helper()
	if len(entries) != 4 { // corazón, amar(se), casa, casa rural
		t.Fatalf("want 4 entries, got %d: %+v", len(entries), entries)
	}
	if entries[0].Headwords[0] != "corazón" || !strings.Contains(entries[0].Body, "órgano muscular") {
		t.Errorf("entry0: %+v", entries[0])
	}
	if !strings.Contains(entries[0].Body, "<br/>") {
		t.Errorf("multi-line body needs <br/>: %q", entries[0].Body)
	}
	// amar(se): Full + Alt variants, ~ replaced by first term
	if len(entries[1].Headwords) != 2 || entries[1].Headwords[0] != "amarse" || entries[1].Headwords[1] != "amar" {
		t.Errorf("entry1 headwords: %v", entries[1].Headwords)
	}
	if !strings.Contains(entries[1].Body, "véase amarse") {
		t.Errorf("tilde: %q", entries[1].Body)
	}
	// casa: sub-entry split off and linked
	if entries[2].Headwords[0] != "casa" || !strings.Contains(entries[2].Body, `bword://casa rural`) {
		t.Errorf("entry2: %+v", entries[2])
	}
	if entries[3].Headwords[0] != "casa rural" || !strings.Contains(entries[3].Body, "vivienda en el campo") {
		t.Errorf("entry3 (sub): %+v", entries[3])
	}
}

func TestReaderUTF8(t *testing.T) {
	checkEntries(t, readAllEntries(t, writeDSL(t, "mini.dsl", []byte(sampleDSL))))
}

func TestReaderUTF8BOM(t *testing.T) {
	data := append([]byte{0xEF, 0xBB, 0xBF}, []byte(sampleDSL)...)
	checkEntries(t, readAllEntries(t, writeDSL(t, "mini.dsl", data)))
}

func TestReaderUTF16LE(t *testing.T) {
	checkEntries(t, readAllEntries(t, writeDSL(t, "mini.dsl", utf16le(sampleDSL))))
}

func TestReaderUTF16LENoBOM(t *testing.T) {
	checkEntries(t, readAllEntries(t, writeDSL(t, "mini.dsl", utf16le(sampleDSL)[2:])))
}

func TestReaderDz(t *testing.T) {
	var b bytes.Buffer
	gw := gzip.NewWriter(&b)
	gw.Write(utf16le(sampleDSL))
	gw.Close()
	checkEntries(t, readAllEntries(t, writeDSL(t, "mini.dsl.dz", b.Bytes())))
}

func TestResourceZip(t *testing.T) {
	dir := t.TempDir()
	dslPath := filepath.Join(dir, "mini.dsl")
	os.WriteFile(dslPath, []byte(sampleDSL), 0o644)

	// build mini.dsl.files.zip with one resource
	zf, err := os.Create(dslPath + ".files.zip")
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(zf)
	w, _ := zw.Create("audio/es.mp3")
	w.Write([]byte{1, 2, 3})
	zw.Close()
	zf.Close()

	t.Setenv("GONOW_DB_DIR", t.TempDir()) // keep auto-ingest out of the user cache
	d, err := Open(dslPath)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	rc, mimeType, err := d.Resource("audio/es.mp3")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(rc)
	rc.Close()
	if mimeType != "audio/mpeg" || len(data) != 3 {
		t.Errorf("resource: %q %v", mimeType, data)
	}
	if _, _, err := d.Resource("../evil"); err == nil {
		t.Error("traversal must be rejected")
	}
	// caps: DSL auto-ingests, so contains/fts must be live
	if c := d.Caps(); !c.Contains || !c.FTS {
		t.Errorf("caps: %+v", c)
	}
	if res, err := d.Exact("corazon", 5); err != nil || len(res) != 1 {
		t.Errorf("folded exact via store: %v %v", res, err)
	}
}

// Integration against the real DSL; skips unless GONOW_TEST_DSL is set.
func TestIntegrationRealDSL(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	p := os.Getenv("GONOW_TEST_DSL")
	if p == "" {
		t.Skip("GONOW_TEST_DSL not set")
	}
	if _, err := os.Stat(p); err != nil {
		t.Skipf("%s not readable", p)
	}
	r, err := NewReader(p)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	n := 0
	for n < 200 {
		e, err := r.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("entry %d: %v", n, err)
		}
		if len(e.Headwords) == 0 || e.Headwords[0] == "" {
			t.Fatalf("entry %d: empty headword", n)
		}
		n++
	}
	if n == 0 {
		t.Fatal("no entries parsed")
	}
}
