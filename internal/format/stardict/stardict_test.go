// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package stardict

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"encoding/binary"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/wuweidict/wudict/internal/resource"
)

// buildStarDict writes a synthetic dictionary: 3 idx entries (one
// article per entry, sametypesequence=h), a .syn synonym, and a res/ dir.
func buildStarDict(t *testing.T, dictzip bool) string {
	t.Helper()
	dir := t.TempDir()
	base := filepath.Join(dir, "test")

	articles := []struct{ word, body string }{
		{"corazón", "<b>corazón</b> órgano muscular"},
		{"pregunta", "<i>petición</i> de información"},
		{"zapato", "calzado"},
	}
	var dictBuf bytes.Buffer
	var idxBuf bytes.Buffer
	for _, a := range articles {
		off := dictBuf.Len()
		dictBuf.WriteString(a.body)
		idxBuf.WriteString(a.word)
		idxBuf.WriteByte(0)
		binary.Write(&idxBuf, binary.BigEndian, uint32(off))
		binary.Write(&idxBuf, binary.BigEndian, uint32(len(a.body)))
	}

	ifo := "StarDict's dict ifo file\nversion=3.0.0\nbookname=Test StarDict\nwordcount=3\nidxfilesize=" +
		strconv.Itoa(idxBuf.Len()) + "\nsametypesequence=h\n"
	mustWrite(t, base+".ifo", []byte(ifo))
	mustWrite(t, base+".idx", idxBuf.Bytes())

	if dictzip {
		mustWrite(t, base+".dict.dz", makeDictzip(t, dictBuf.Bytes(), 16))
	} else {
		mustWrite(t, base+".dict", dictBuf.Bytes())
	}

	// .syn: "cuore" -> entry 0
	var syn bytes.Buffer
	syn.WriteString("cuore")
	syn.WriteByte(0)
	binary.Write(&syn, binary.BigEndian, uint32(0))
	mustWrite(t, base+".syn", syn.Bytes())

	os.MkdirAll(filepath.Join(dir, "res"), 0o755)
	mustWrite(t, filepath.Join(dir, "res", "logo.png"), []byte{0x89, 'P', 'N', 'G'})

	return base + ".ifo"
}

func mustWrite(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// makeDictzip builds a dictzip file whose chunks are independent complete
// deflate streams (valid for our reader, which stops at chunkLen).
func makeDictzip(t *testing.T, data []byte, chunkLen int) []byte {
	t.Helper()
	var chunks [][]byte
	for off := 0; off < len(data); off += chunkLen {
		end := min(off+chunkLen, len(data))
		var cb bytes.Buffer
		fw, _ := flate.NewWriter(&cb, flate.BestCompression)
		fw.Write(data[off:end])
		fw.Close()
		chunks = append(chunks, cb.Bytes())
	}
	// RA subfield: VER, CHLEN, CHCNT, sizes... (little-endian)
	var ra bytes.Buffer
	binary.Write(&ra, binary.LittleEndian, uint16(1))
	binary.Write(&ra, binary.LittleEndian, uint16(chunkLen))
	binary.Write(&ra, binary.LittleEndian, uint16(len(chunks)))
	for _, c := range chunks {
		binary.Write(&ra, binary.LittleEndian, uint16(len(c)))
	}
	var extra bytes.Buffer
	extra.WriteByte('R')
	extra.WriteByte('A')
	binary.Write(&extra, binary.LittleEndian, uint16(ra.Len()))
	extra.Write(ra.Bytes())

	var out bytes.Buffer
	out.Write([]byte{0x1f, 0x8b, 8, 0x04, 0, 0, 0, 0, 0, 0xff}) // hdr, FEXTRA
	binary.Write(&out, binary.LittleEndian, uint16(extra.Len()))
	out.Write(extra.Bytes())
	for _, c := range chunks {
		out.Write(c)
	}
	// A real trailer, not eight zeroes: our own dzReader ignores it, but a
	// dictzipped .idx/.syn is read by compress/gzip, which verifies it.
	binary.Write(&out, binary.LittleEndian, crc32.ChecksumIEEE(data))
	binary.Write(&out, binary.LittleEndian, uint32(len(data)))
	return out.Bytes()
}

// makeDictzipStream builds a dictzip whose chunks are one CONTINUOUS deflate
// stream separated by sync-flush points - what a whole-stream reader
// (compress/gzip, for a dictzipped .idx or .syn) must be able to read end to
// end. It is a separate helper from makeDictzip because Go's flate has no
// Z_FULL_FLUSH: a stream this shape cannot be inflated chunk-independently, so
// the .dict fixture needs the other one and the companions need this one.
func makeDictzipStream(t *testing.T, data []byte, chunkLen int) []byte {
	t.Helper()
	var body bytes.Buffer
	fw, _ := flate.NewWriter(&body, flate.BestCompression)
	var sizes []uint16
	prev := 0
	for off := 0; off < len(data); off += chunkLen {
		end := min(off+chunkLen, len(data))
		if _, err := fw.Write(data[off:end]); err != nil {
			t.Fatal(err)
		}
		if err := fw.Flush(); err != nil {
			t.Fatal(err)
		}
		sizes = append(sizes, uint16(body.Len()-prev))
		prev = body.Len()
	}
	if err := fw.Close(); err != nil {
		t.Fatal(err)
	}

	var ra bytes.Buffer
	binary.Write(&ra, binary.LittleEndian, uint16(1))
	binary.Write(&ra, binary.LittleEndian, uint16(chunkLen))
	binary.Write(&ra, binary.LittleEndian, uint16(len(sizes)))
	for _, n := range sizes {
		binary.Write(&ra, binary.LittleEndian, n)
	}
	var out bytes.Buffer
	out.Write([]byte{0x1f, 0x8b, 8, 0x04, 0, 0, 0, 0, 0, 0xff})
	binary.Write(&out, binary.LittleEndian, uint16(4+ra.Len()))
	out.WriteByte('R')
	out.WriteByte('A')
	binary.Write(&out, binary.LittleEndian, uint16(ra.Len()))
	out.Write(ra.Bytes())
	out.Write(body.Bytes())
	binary.Write(&out, binary.LittleEndian, crc32.ChecksumIEEE(data))
	binary.Write(&out, binary.LittleEndian, uint32(len(data)))
	return out.Bytes()
}

func TestSyntheticStarDict(t *testing.T) {
	for _, dz := range []bool{false, true} {
		name := "plain"
		if dz {
			name = "dictzip"
		}
		t.Run(name, func(t *testing.T) {
			d, err := Open(buildStarDict(t, dz))
			if err != nil {
				t.Fatal(err)
			}
			defer d.Close()

			if d.Meta().Name != "Test StarDict" || d.Meta().EntryCount != 3 {
				t.Fatalf("meta: %+v", d.Meta())
			}
			res, err := d.Exact("corazón", 10)
			if err != nil || len(res) != 1 || !strings.Contains(res[0].Body, "órgano") {
				t.Fatalf("Exact: %v %v", res, err)
			}
			// fold fallback
			res, err = d.Exact("CORAZON", 10)
			if err != nil || len(res) != 1 {
				t.Fatalf("folded: %v %v", res, err)
			}
			// synonym via .syn resolves to entry 0
			res, err = d.Exact("cuore", 10)
			if err != nil || len(res) != 1 || res[0].Headword != "corazón" {
				t.Fatalf("syn: %v %v", res, err)
			}
			// prefix
			res, err = d.Prefix("pre", 10)
			if err != nil || len(res) != 1 || res[0].Headword != "pregunta" {
				t.Fatalf("prefix: %v %v", res, err)
			}
			// res/ dir resource
			rc, ctype, err := d.Resource("logo.png")
			if err != nil {
				t.Fatalf("Resource: %v", err)
			}
			data, _ := io.ReadAll(rc)
			rc.Close()
			if ctype != "image/png" || !bytes.HasPrefix(data, []byte{0x89}) {
				t.Errorf("resource: %q %v", ctype, data)
			}
			if _, _, err := d.Resource("../escape.png"); err == nil {
				t.Error("traversal must be rejected")
			}
		})
	}
}

func TestSyntheticIngestReader(t *testing.T) {
	r, err := NewReader(buildStarDict(t, false))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	e, err := r.Next()
	if err != nil {
		t.Fatal(err)
	}
	// first entry carries its synonym as an extra headword
	if len(e.Headwords) != 2 || e.Headwords[0] != "corazón" || e.Headwords[1] != "cuore" {
		t.Fatalf("headwords: %v", e.Headwords)
	}
	n := 1
	for {
		if _, err := r.Next(); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		n++
	}
	if n != 3 {
		t.Errorf("entries: %d", n)
	}
}

func TestRecordToHTML(t *testing.T) {
	// multi-type record without sametypesequence: m part then h part
	raw := append([]byte("m"), append([]byte("plain <text>\x00"), []byte("h<b>html</b>")...)...)
	got := recordToHTML(raw, "")
	if !strings.Contains(got, "&lt;text&gt;") || !strings.Contains(got, "<b>html</b>") {
		t.Errorf("recordToHTML: %q", got)
	}
	// uppercase type: size-prefixed binary is skipped
	var rec bytes.Buffer
	rec.WriteByte('W')
	binary.Write(&rec, binary.BigEndian, uint32(3))
	rec.Write([]byte{1, 2, 3})
	rec.WriteString("mafter")
	if got := recordToHTML(rec.Bytes(), ""); !strings.Contains(got, "after") || strings.Contains(got, "\x01") {
		t.Errorf("uppercase skip: %q", got)
	}
}

func TestXdxfToHTML(t *testing.T) {
	src := `<k>word</k><c c="red">colored</c> <kref>other</kref> <tr>wɜːd</tr> <ex>an example</ex>`
	got := xdxfToHTML(src)
	for _, want := range []string{
		`<div class="wu-k">word</div>`,
		`<span class="wu-c" style="--wd-c:red">colored</span>`,
		`<a href="">other</a>`,
		`[wɜːd]`,
		`class="wu-ex"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("xdxf missing %q in %q", want, got)
		}
	}
	// malformed input falls back to escaped text, never errors
	if got := xdxfToHTML("<broken <<x"); !strings.Contains(got, "&lt;") {
		t.Errorf("malformed fallback: %q", got)
	}
}

// Integration against a real StarDict; skips unless WUDICT_TEST_STARDICT set.
func TestIntegrationRealStarDict(t *testing.T) {
	p := os.Getenv("WUDICT_TEST_STARDICT")
	if p == "" {
		t.Skip("WUDICT_TEST_STARDICT not set")
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
	if m.EntryCount == 0 {
		t.Fatal("no entries")
	}
	if wc := d.wordCount(); wc != 0 && wc != m.EntryCount {
		t.Logf("note: ifo wordcount=%d, idx entries=%d", wc, m.EntryCount)
	}
	keys := d.Keywords(m.EntryCount/2, 3)
	res, err := d.Exact(keys[0], 5)
	if err != nil || len(res) == 0 || res[0].Body == "" {
		t.Fatalf("Exact(%q): %d results, err=%v", keys[0], len(res), err)
	}
}

// StarDict's resources are a `res/` folder beside the .ifo, so the provider
// finds them from the path alone (O8) - the .dict is never opened.
func TestMediaSourcesFromPathAlone(t *testing.T) {
	dir := t.TempDir()
	res := filepath.Join(dir, "res")
	if err := os.MkdirAll(res, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(res, "bell.wav"), []byte("riff"), 0o644); err != nil {
		t.Fatal(err)
	}
	srcs := MediaSources(filepath.Join(dir, "x.ifo"))
	if len(srcs) != 1 {
		t.Fatalf("want the res folder, got %d sources", len(srcs))
	}
	rc, err := srcs[0].Open("bell.wav")
	if err != nil {
		t.Fatalf("res/bell.wav: %v", err)
	}
	rc.Close()
	if p, ok := resource.Get("stardict"); !ok || p.Sources == nil {
		t.Fatal("stardict registered no media provider")
	}
}

// skewDictzipSubfield rewrites a dictzip header so its RA subfield declares two
// bytes (one chunk-table slot) more than XLEN leaves room for, without moving
// any data. This is a real writer bug, seen on a 10 MB Polish dictionary: XLEN
// sized the field for the chunks actually emitted, SLEN and CHCNT for one more.
//
// It is worth a fixture because of HOW it failed. `sub := extra[4 : 4+slen]`
// did not panic - extra is a window into a 64 KiB header buffer, so slicing
// past its length is still inside its capacity - and the panic landed two
// statements later on `extra = extra[4+slen:]`, where the implied high bound is
// len(extra): "slice bounds out of range [372:370]", once per query, for every
// word in the dictionary.
func skewDictzipSubfield(t *testing.T, dz []byte) []byte {
	t.Helper()
	out := append([]byte(nil), dz...)
	if len(out) < 22 || out[12] != 'R' || out[13] != 'A' {
		t.Fatalf("fixture is not a dictzip with a leading RA subfield")
	}
	slen := binary.LittleEndian.Uint16(out[14:])
	chcnt := binary.LittleEndian.Uint16(out[20:])
	binary.LittleEndian.PutUint16(out[14:], slen+2)  // SLEN: one slot too many
	binary.LittleEndian.PutUint16(out[20:], chcnt+1) // CHCNT agrees with SLEN
	return out
}

// TestDictzipSubfieldOverrunsXLEN: the dictionary opens and answers, rather
// than panicking or being rejected, when SLEN overruns the FEXTRA field.
func TestDictzipSubfieldOverrunsXLEN(t *testing.T) {
	ifoPath := buildStarDict(t, true)
	base := strings.TrimSuffix(ifoPath, ".ifo")
	raw, err := os.ReadFile(base + ".dict.dz")
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, base+".dict.dz", skewDictzipSubfield(t, raw))

	d, err := Open(ifoPath)
	if err != nil {
		t.Fatalf("open with an over-declared RA subfield: %v", err)
	}
	defer d.Close()
	res, err := d.Exact("zapato", 5)
	if err != nil {
		t.Fatalf("exact: %v", err)
	}
	if len(res) != 1 || !strings.Contains(res[0].Body, "calzado") {
		t.Fatalf("article not served: %+v", res)
	}
}

// TestCompressedCompanions: .idx and .syn are read through their dictzip
// spelling. Whole dictionaries ship as `dictzip *`, which leaves .idx.dz and
// .syn.dz beside the .dict.dz; probing only ".gz" reported those as having no
// index at all while the index was sitting right there.
func TestCompressedCompanions(t *testing.T) {
	for _, suffix := range []string{".gz", ".dz"} {
		t.Run(suffix, func(t *testing.T) {
			ifoPath := buildStarDict(t, true)
			base := strings.TrimSuffix(ifoPath, ".ifo")
			for _, ext := range []string{".idx", ".syn"} {
				raw, err := os.ReadFile(base + ext)
				if err != nil {
					t.Fatal(err)
				}
				if suffix == ".dz" {
					mustWrite(t, base+ext+suffix, makeDictzipStream(t, raw, 16))
				} else {
					mustWrite(t, base+ext+suffix, gzipBytes(t, raw))
				}
				if err := os.Remove(base + ext); err != nil {
					t.Fatal(err)
				}
			}
			d, err := Open(ifoPath)
			if err != nil {
				t.Fatalf("open with %s companions: %v", suffix, err)
			}
			defer d.Close()
			if got := d.Meta().EntryCount; got != 3 {
				t.Fatalf("entries = %d, want 3 (index not read)", got)
			}
			// the synonym proves the .syn came back too
			res, err := d.Exact("cuore", 5)
			if err != nil || len(res) != 1 {
				t.Fatalf("synonym lookup: %v %+v", err, res)
			}
		})
	}
}

func gzipBytes(t *testing.T, data []byte) []byte {
	t.Helper()
	var b bytes.Buffer
	gw := gzip.NewWriter(&b)
	if _, err := gw.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// TestMissingIndexNamesEverySpelling: the "not found" message must name what
// was actually looked for, since the fix for a missing index is to supply one
// of these three files.
func TestMissingIndexNamesEverySpelling(t *testing.T) {
	ifoPath := buildStarDict(t, true)
	if err := os.Remove(strings.TrimSuffix(ifoPath, ".ifo") + ".idx"); err != nil {
		t.Fatal(err)
	}
	_, err := Open(ifoPath)
	if err == nil {
		t.Fatal("opened without an index")
	}
	for _, want := range []string{"test.idx", "test.idx.gz", "test.idx.dz"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not name %s", err, want)
		}
	}
}

// TestReadRangeRejectsBadRanges: a record range out of the .dict must be an
// error on that article, never a panic - both backends, since the .idx pair
// they are handed is u32 data from the file.
func TestReadRangeRejectsBadRanges(t *testing.T) {
	ifoPath := buildStarDict(t, true)
	d, err := Open(ifoPath)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	dz, ok := d.data.(*dzReader)
	if !ok {
		t.Fatalf("expected a dictzip backend, got %T", d.data)
	}
	plain := plainDict{f: nil, size: 64}
	for _, tc := range []struct{ offset, size int }{
		{0, -2},      // u32 size widened to a negative int (32-bit build)
		{1 << 20, 4}, // offset past the end
		{0, 1 << 20}, // size past the end
		{-1, 4},      // negative offset
	} {
		if _, err := dz.readRange(int64(tc.offset), tc.size); err == nil {
			t.Fatalf("dictzip readRange(%d,%d) returned no error", tc.offset, tc.size)
		}
		if _, err := plain.readRange(int64(tc.offset), tc.size); err == nil {
			t.Fatalf("plain readRange(%d,%d) returned no error", tc.offset, tc.size)
		}
	}
}

// TestReaderSkipsUnreadableRecords: ingest must not lose a whole dictionary to
// one bad record. The driver aborts the scan on any error from Next, so a
// record pointing outside the .dict has to be stepped over here.
func TestReaderSkipsUnreadableRecords(t *testing.T) {
	ifoPath := buildStarDict(t, false)
	r, err := NewReader(ifoPath)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	r.d.entries[1].offset = 1 << 30 // middle entry now points past the file

	var words []string
	for {
		e, err := r.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("scan aborted on a bad record: %v", err)
		}
		words = append(words, e.Headwords[0])
	}
	if len(words) != 2 || words[0] != "corazón" || words[1] != "zapato" {
		t.Fatalf("got %v, want the two readable entries", words)
	}
}

// TestDamagedCompanionTailKeepsEntries: the last bytes of a compressed .idx
// being wrong must cost the last record, not the dictionary. Both halves are
// exercised at once - gzip's trailer no longer matches, and the final idx
// record is cut in half.
func TestDamagedCompanionTailKeepsEntries(t *testing.T) {
	ifoPath := buildStarDict(t, true)
	base := strings.TrimSuffix(ifoPath, ".ifo")
	raw, err := os.ReadFile(base + ".idx")
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, base+".idx.gz", gzipBytes(t, raw[:len(raw)-3]))
	if err := os.Remove(base + ".idx"); err != nil {
		t.Fatal(err)
	}
	// rewrite the trailer so the checksum describes bytes that are no longer there
	gzBad, err := os.ReadFile(base + ".idx.gz")
	if err != nil {
		t.Fatal(err)
	}
	binary.LittleEndian.PutUint32(gzBad[len(gzBad)-8:], 0xdeadbeef)
	mustWrite(t, base+".idx.gz", gzBad)

	d, err := Open(ifoPath)
	if err != nil {
		t.Fatalf("open with a damaged index tail: %v", err)
	}
	defer d.Close()
	if got := d.Meta().EntryCount; got != 2 {
		t.Fatalf("entries = %d, want the 2 intact records", got)
	}
	if res, err := d.Exact("corazón", 5); err != nil || len(res) != 1 {
		t.Fatalf("first entry lost: %v %+v", err, res)
	}
}
