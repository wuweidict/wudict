package stardict

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
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
	out.Write(make([]byte, 8)) // CRC32+ISIZE (unchecked by our reader)
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
		`<div class="xdxf-k"><b>word</b></div>`,
		`<span style="color:red">colored</span>`,
		`<a href="">other</a>`,
		`[wɜːd]`,
		`class="xdxf-ex"`,
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

// Integration against a real StarDict; skips unless GONOW_TEST_STARDICT set.
func TestIntegrationRealStarDict(t *testing.T) {
	p := os.Getenv("GONOW_TEST_STARDICT")
	if p == "" {
		t.Skip("GONOW_TEST_STARDICT not set")
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
