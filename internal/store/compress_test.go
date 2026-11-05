// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/wuweidict/wudict/internal/dict"
	"github.com/wuweidict/wudict/internal/sysmem"
)

// withCodec sets the codec for the test and restores the default after it.
func withCodec(t *testing.T, name string, level int) {
	t.Helper()
	SetCodec(name, level)
	t.Cleanup(func() { SetCodec(codecZstd, 1) })
}

// codecEntries is laneEntries plus articles long and alike enough that every
// codec compresses them: the shared markup is what a dictionary is for.
func codecEntries() []dict.Entry {
	es := laneEntries(3000)
	for i := range 600 {
		w := fmt.Sprintf("long%04d", i)
		body := fmt.Sprintf(`<div class="entry"><span class="hw">%s</span><span class="pos">noun</span>`+
			`<ol class="senses"><li class="sense">%s, the %d-th meaning, with an example sentence.</li>`+
			`<li class="sense">another sense of %s</li></ol></div>`, w, w, i, w)
		es = append(es, dict.Entry{Headwords: []string{w}, Body: body, Kind: dict.BodyHTML})
	}
	return es
}

// rawStats reads what an ingest stored: the schema, the meta, the dictionary
// size, how many rows start with each marker and the stored bytes in all.
type rawStats struct {
	schema          int
	meta            map[string]string
	dict            int64
	zstdRows, total int64
	stored          int64
}

func statsOf(t *testing.T, path string) rawStats {
	t.Helper()
	db, err := openRO(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var st rawStats
	if err := db.QueryRow("PRAGMA user_version").Scan(&st.schema); err != nil {
		t.Fatal(err)
	}
	if st.meta, err = readMeta(db); err != nil {
		t.Fatal(err)
	}
	if st.schema >= 2 {
		if err := db.QueryRow("SELECT coalesce(sum(length(data)), 0) FROM body_dict").Scan(&st.dict); err != nil {
			t.Fatal(err)
		}
	} else if err := db.QueryRow("SELECT count(*) FROM sqlite_master WHERE name = 'body_dict'").Scan(&st.dict); err != nil || st.dict != 0 {
		t.Fatalf("a schema-1 database has a body_dict table (%v)", err)
	}
	if err := db.QueryRow(`SELECT count(*), coalesce(sum(substr(m, 1, 1) = x'01'), 0), coalesce(sum(length(CAST(m AS BLOB))), 0) FROM entry`).
		Scan(&st.total, &st.zstdRows, &st.stored); err != nil {
		t.Fatal(err)
	}
	return st
}

func ingestTo(t *testing.T, entries []dict.Entry, plan Plan) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), TextDBName)
	r := &fakeReader{meta: dict.Meta{Name: "codec", Format: "test", Path: filepath.Join(t.TempDir(), "codec.src")}, entries: entries}
	if _, err := IngestPlan(r, p, plan, nil); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestCodecsSameContent: every codec and level prepares the same content
// (Fingerprint hashes decoded bodies), writes its own schema and stamps, and
// zstd actually compresses with its dictionary - a fingerprint alone would
// also pass if every row had fallen back to literal text.
func TestCodecsSameContent(t *testing.T) {
	entries := codecEntries()
	type run struct {
		name  string
		level int
		label string
	}
	runs := []run{
		{codecNone, 0, "plain"}, {codecDeflate, 1, "deflate-1"}, {codecDeflate, 6, "deflate-6"},
		{codecZstd, 1, "zstd-1"}, {codecZstd, 3, "zstd-3"},
	}
	var want string
	stored := map[string]int64{}
	for _, plan := range []Plan{{}, {FullText: true}} {
		for i, r := range runs {
			withCodec(t, r.name, r.level)
			p := ingestTo(t, entries, plan)
			fp, err := Fingerprint(p)
			if err != nil {
				t.Fatal(err)
			}
			if i == 0 {
				want = fp
			} else if fp != want {
				t.Errorf("%s, full text %v: fingerprint %s, want %s", r.label, plan.FullText, fp, want)
			}
			st := statsOf(t, p)
			if st.meta["body_encoding"] != r.label {
				t.Errorf("%s: body_encoding %q", r.label, st.meta["body_encoding"])
			}
			stored[r.label] = st.stored
			if r.name == codecZstd {
				if st.schema != 2 || st.dict == 0 || st.zstdRows == 0 {
					t.Errorf("%s: schema %d, dictionary %d bytes, %d zstd rows", r.label, st.schema, st.dict, st.zstdRows)
				}
				if st.meta["body_dict_bytes"] != fmt.Sprint(st.dict) {
					t.Errorf("%s: body_dict_bytes %q, table holds %d", r.label, st.meta["body_dict_bytes"], st.dict)
				}
			} else if st.schema != 1 || st.zstdRows != 0 {
				// 0x01 is literal text in schema 1; laneEntries has none
				t.Errorf("%s: schema %d, %d rows starting 0x01", r.label, st.schema, st.zstdRows)
			}
		}
	}
	if !(stored["zstd-1"] < stored["deflate-6"] && stored["deflate-6"] < stored["plain"]) {
		t.Errorf("stored bytes: zstd-1 %d, deflate-6 %d, plain %d", stored["zstd-1"], stored["deflate-6"], stored["plain"])
	}
}

// TestCodecWindow: a window cut while the source is still being read - on
// either lane, switching between them, and with no budget at all - prepares
// the same content as DEFLATE, and stores a dictionary.
func TestCodecWindow(t *testing.T) {
	saved, savedWindow := urgentAt, dictWindow
	defer func() { urgentAt, dictWindow = saved, savedWindow }()
	entries := codecEntries()
	urgentAt = func(string) bool { return false }
	withCodec(t, codecDeflate, 6)
	want, err := Fingerprint(ingestTo(t, entries, Plan{FullText: true}))
	if err != nil {
		t.Fatal(err)
	}
	withCodec(t, codecZstd, 1)
	dictWindow = 64 << 10 // the source holds several times this
	check := func(name string) {
		t.Helper()
		p := ingestTo(t, entries, Plan{FullText: true})
		if got, err := Fingerprint(p); err != nil || got != want {
			t.Errorf("%s: fingerprint %s (%v), want %s", name, got, err, want)
		}
		if st := statsOf(t, p); st.dict == 0 || st.zstdRows == 0 {
			t.Errorf("%s: dictionary %d bytes, %d zstd rows", name, st.dict, st.zstdRows)
		}
	}
	for name, lane := range map[string]func(string) bool{
		"one core": func(string) bool { return false },
		"urgent":   func(string) bool { return true },
		"every boundary": func() func(string) bool {
			var n atomic.Int64
			return func(string) bool { return n.Add(1)%2 == 0 }
		}(),
	} {
		urgentAt = lane
		check(name)
	}
	// No budget: the window is cut after its first record, and the pipeline
	// runs at its floor; neither may wait on the other.
	urgentAt = func(string) bool { return true }
	release := fillPool()
	check("no room")
	release()
	// The source ends inside the window: the dictionary is cut at the end.
	dictWindow = savedWindow
	check("end inside the window")
}

// TestCodecRedirectsOnly: a source with no article writes no dictionary and
// opens.
func TestCodecRedirectsOnly(t *testing.T) {
	withCodec(t, codecZstd, 1)
	var es []dict.Entry
	for i := range 50 {
		es = append(es, dict.Entry{Headwords: []string{fmt.Sprintf("to%d", i)}, LinkTo: "nowhere"})
	}
	p := ingestTo(t, es, Plan{})
	if st := statsOf(t, p); st.schema != 2 || st.dict != 0 {
		t.Errorf("schema %d, dictionary %d bytes", st.schema, st.dict)
	}
	s, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
}

// TestReservedFirstBytes: a body that would read as a marker is never stored
// literally, in any codec; one that would not stays literal where that is
// smaller. Each reads back unchanged.
func TestReservedFirstBytes(t *testing.T) {
	var acct sysmem.Account
	defer acct.Close()
	zc := &bodyCodec{name: codecZstd, level: 1}
	z, err := zc.start(nil, &acct)
	if err != nil {
		t.Fatal(err)
	}
	defer zc.close()
	encs := map[string]*bodyEncoder{
		"zstd":    z,
		"deflate": newBodyEncoder(codecDeflate, 6, 1, nil),
		"none":    newBodyEncoder(codecNone, 0, 1, nil),
	}
	decs := map[string]*bodyDecoder{"zstd": newBodyDecoder(2, nil), "deflate": schema1, "none": schema1}
	defer decs["zstd"].close()
	bodies := []string{"", "\x00", "\x00short", "\x01", "\x01short", "\x05x", "\x08x", "\x09x", "short", "<p>ñ</p>",
		"\x00" + strings.Repeat("<p>long</p>", 40), "\x01" + strings.Repeat("<p>long</p>", 40), strings.Repeat("<p>long</p>", 40)}
	for name, e := range encs {
		for _, b := range bodies {
			out := e.encode(b)
			if got := decs[name].decode(out); got != b {
				t.Errorf("%s %q: reads back %q", name, b, got)
			}
			if len(out) > 0 && e.reserved(string(out)) && string(out) == b {
				t.Errorf("%s %q: stored literally, but reads as a marker", name, b)
			}
		}
	}
	// In schema 1, 0x01-0x08 are text: an older database stays as it was.
	if got := schema1.decode([]byte("\x01legacy")); got != "\x01legacy" {
		t.Errorf("schema 1 misread %q", got)
	}
	// In schema 2 an unknown reserved marker reads as nothing, never as bytes.
	if got := newBodyDecoder(2, nil).decode([]byte("\x05abc")); got != "" {
		t.Errorf("reserved marker read as %q", got)
	}
}

// TestCorruptZstdRow: a damaged zstd row degrades to an empty article; the
// query still answers.
func TestCorruptZstdRow(t *testing.T) {
	withCodec(t, codecZstd, 1)
	p := ingestTo(t, codecEntries(), Plan{})
	db, err := sql.Open(driverName, dsnIngest(p))
	if err != nil {
		t.Fatal(err)
	}
	var w string
	if err := db.QueryRow(`SELECT w FROM entry WHERE substr(m, 1, 1) = x'01' ORDER BY id LIMIT 1`).Scan(&w); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE entry SET m = x'01deadbeefdeadbeef' WHERE w = ?`, w); err != nil {
		t.Fatal(err)
	}
	db.Close()
	s, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	res, err := s.Exact(w, 5)
	if err != nil || len(res) == 0 || res[0].Body != "" {
		t.Fatalf("Exact(%q) = %+v, %v; want one empty article", w, res, err)
	}
}

// TestReferencedAssetsZstd: packing media reads the articles of a zstd
// database through its dictionary.
func TestReferencedAssetsZstd(t *testing.T) {
	withCodec(t, codecZstd, 1)
	es := codecEntries()
	es = append(es, h("styled", `<link rel="stylesheet" href="look.css"><p>`+strings.Repeat("text ", 40)+`</p>`))
	got, err := ReferencedAssets(ingestTo(t, es, Plan{}))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "look.css" {
		t.Errorf("ReferencedAssets = %q", got)
	}
}

func TestDictSize(t *testing.T) {
	for _, tc := range []struct {
		level             int
		text              int64
		articles, entries int
		eof               bool
		want              int
	}{
		{1, 1 << 20, 100, 0, true, (1 << 20) / 50},                      // 2% of the text
		{1, 1000, 1, 0, true, 8 << 10},                                  // the floor
		{1, 100 << 20, 1, 0, true, 256 << 10},                           // the cap at the fast levels
		{3, 100 << 20, 1, 0, true, 1 << 20},                             // and at the others
		{3, 16 << 20, 1000, 0, false, 1 << 20},                          // no entry count: the cap
		{3, 16 << 20, 1000, 2000, false, (16 << 20) / 1000 * 2000 / 50}, // estimated from the count
		{1, 16 << 20, 1000, 2000, false, 256 << 10},
	} {
		if got := dictSize(tc.level, tc.text, tc.articles, tc.entries, tc.eof); got != tc.want {
			t.Errorf("dictSize(%+v) = %d, want %d", tc, got, tc.want)
		}
	}
}

// TestDecoderRelease: a released decoder is made again by the next article,
// which reads the same; releasing while others decode is safe (run with
// -race); a closed Store makes none.
func TestDecoderRelease(t *testing.T) {
	withCodec(t, codecZstd, 1)
	es := codecEntries()
	s, err := Open(ingestTo(t, es, Plan{}))
	if err != nil {
		t.Fatal(err)
	}
	const w = "long0007"
	read := func() string {
		res, err := s.Exact(w, 1)
		if err != nil || len(res) == 0 {
			t.Errorf("Exact(%q): %v %v", w, res, err)
			return ""
		}
		return res[0].Body
	}
	if b, _ := s.DecoderHeld(); b != 0 {
		t.Fatalf("a decoder exists before any article was read (%d bytes)", b)
	}
	want := read()
	if want == "" || !strings.Contains(want, w) {
		t.Fatalf("article %q", want)
	}
	b, used := s.DecoderHeld()
	if b == 0 || used == 0 {
		t.Fatalf("held %d bytes, last use %d", b, used)
	}
	if freed := s.ReleaseDecoder(); freed != b {
		t.Errorf("released %d, held %d", freed, b)
	}
	if b, _ := s.DecoderHeld(); b != 0 {
		t.Errorf("still holds %d after release", b)
	}
	if got := read(); got != want {
		t.Errorf("after release: %q", got)
	}
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if got := read(); got != want {
					t.Errorf("concurrent read: %q", got)
					return
				}
			}
		}()
	}
	for range 200 {
		s.ReleaseDecoder()
	}
	close(stop)
	wg.Wait()
	s.Close()
	if b, _ := s.DecoderHeld(); b != 0 {
		t.Errorf("a closed Store holds %d", b)
	}
}

// TestCodecOutdated: a database stored with another codec than COMPRESSION is
// outdated for that reason alone, and a rebuild the user asks for converts it;
// a level is not a reason, and neither is a database that recorded no codec.
func TestCodecOutdated(t *testing.T) {
	t.Setenv("WUDICT_DB_DIR", t.TempDir())
	src := writeSrc(t, filepath.Join(t.TempDir(), "s.mdx"), "source bytes")
	dir := t.TempDir()
	db := TextDBPath(dir)
	reader := Hooks{Reader: func() (dict.Reader, error) {
		return &fakeReader{meta: dict.Meta{Name: "S", Format: "mdx", Path: src}, entries: codecEntries()}, nil
	}}
	withCodec(t, codecDeflate, 6)
	if _, err := Reconcile(src, Target{Dir: dir}, reader); err != nil {
		t.Fatal(err)
	}
	if got := Stale(db, src); len(got) != 0 {
		t.Fatalf("DEFLATE under deflate: Stale = %v", got)
	}
	withCodec(t, codecZstd, 1)
	if got := Stale(db, src); len(got) != 1 || got[0] != ReasonCodec {
		t.Fatalf("DEFLATE under zstd: Stale = %v, want [codec]", got)
	}
	// preparation nobody asked for leaves it alone (D151)
	if out, err := Reconcile(src, Target{Dir: dir}, reader); err != nil || out.Rebuilt {
		t.Fatalf("an unasked Reconcile rebuilt it: %+v, %v", out, err)
	}
	out, err := Reconcile(src, Target{Dir: dir, Rebuild: IfOutdated}, reader)
	if err != nil || !out.Rebuilt || len(out.Why) != 1 || out.Why[0] != ReasonCodec {
		t.Fatalf("the rebuild: %+v, %v", out, err)
	}
	if st := statsOf(t, db); st.schema != 2 || st.meta["body_encoding"] != "zstd-1" || st.zstdRows == 0 {
		t.Errorf("after the rebuild: schema %d, %q, %d zstd rows", st.schema, st.meta["body_encoding"], st.zstdRows)
	}
	if got := Stale(db, src); len(got) != 0 {
		t.Errorf("after the rebuild: Stale = %v", got)
	}
	withCodec(t, codecZstd, 3)
	if got := Stale(db, src); len(got) != 0 {
		t.Errorf("a level change made it outdated: %v", got)
	}
	for _, tc := range []struct {
		enc, setting string
		stale        bool
	}{
		{"deflate", codecZstd, true}, // the label before D191
		{"plain", codecZstd, true},
		{"plain", codecNone, false},
		{"deflate-1", codecDeflate, false},
		{"zstd-10", codecZstd, false},
		{"zstd-1", codecDeflate, true},
		{"", codecZstd, false},
	} {
		SetCodec(tc.setting, 1)
		if got := codecStale(map[string]string{"body_encoding": tc.enc}); got != tc.stale {
			t.Errorf("body_encoding %q under %s: stale %v, want %v", tc.enc, tc.setting, got, tc.stale)
		}
	}
}

// TestCodecRedirectHeavy: a source that is mostly redirects - written at once
// while the window is open, never held - prepares the same content with zstd
// as with DEFLATE, and resolves the same redirects.
func TestCodecRedirectHeavy(t *testing.T) {
	saved := dictWindow
	defer func() { dictWindow = saved }()
	var es []dict.Entry
	for i := range 5000 {
		es = append(es, dict.Entry{Headwords: []string{fmt.Sprintf("form%05d", i)}, LinkTo: fmt.Sprintf("lemma%02d", i%60)})
		if i%100 == 0 {
			w := fmt.Sprintf("lemma%02d", i/100)
			es = append(es, h(w, "<p>"+strings.Repeat(w+" a sense with an example. ", 10)+"</p>"))
		}
	}
	prep := func() (string, Report) {
		p := filepath.Join(t.TempDir(), TextDBName)
		r := &fakeReader{meta: dict.Meta{Name: "R", Format: "test", Path: filepath.Join(t.TempDir(), "r.src")}, entries: es}
		rep, err := IngestPlan(r, p, Plan{}, nil)
		if err != nil {
			t.Fatal(err)
		}
		fp, err := Fingerprint(p)
		if err != nil {
			t.Fatal(err)
		}
		return fp, rep
	}
	withCodec(t, codecDeflate, 6)
	want, wantRep := prep()
	withCodec(t, codecZstd, 1)
	for _, window := range []int64{saved, 1 << 10} {
		dictWindow = window
		if got, rep := prep(); got != want || rep != wantRep {
			t.Errorf("window %d: fingerprint %s, report %+v; want %s, %+v", window, got, rep, want, wantRep)
		}
	}
}

// TestZstdOversizeBody: a body larger than the decoder may produce is stored
// as DEFLATE, never as a zstd frame nothing could read back.
func TestZstdOversizeBody(t *testing.T) {
	var acct sysmem.Account
	defer acct.Close()
	c := &bodyCodec{name: codecZstd, level: 1}
	e, err := c.start(nil, &acct)
	if err != nil {
		t.Fatal(err)
	}
	defer c.close()
	big := strings.Repeat("<p>a long article</p>", maxBody/20+1)
	out := e.encode(big)
	if out[0] != markDeflate {
		t.Fatalf("a %d-byte body stored with marker %#x", len(big), out[0])
	}
	if got := newBodyDecoder(2, nil).decode(out); got != big {
		t.Errorf("read back %d bytes, want %d", len(got), len(big))
	}
}

// TestDecoderRetriesAndCloses: a decoder that could not be made is tried
// again by the next row, and a closed decoder makes none.
func TestDecoderRetriesAndCloses(t *testing.T) {
	withCodec(t, codecZstd, 1)
	p := ingestTo(t, codecEntries(), Plan{})
	db, err := openRO(p)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var raw []byte
	if err := db.QueryRow(`SELECT m FROM entry WHERE substr(m, 1, 1) = x'01' LIMIT 1`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	gone, _ := openRO(p)
	gone.Close() // every query on it fails
	d := newBodyDecoder(2, gone)
	if got := d.decode(raw); got != "" {
		t.Fatalf("no dictionary readable, yet decoded %q", got)
	}
	d.db = db // the read succeeds now
	want := d.decode(raw)
	if want == "" {
		t.Fatal("the decoder was not tried again")
	}
	d.close()
	if got := d.decode(raw); got != "" {
		t.Errorf("a closed decoder decoded %q", got)
	}
	if b, _ := d.held(); b != 0 {
		t.Errorf("a closed decoder holds %d", b)
	}
}

func TestSetCodecClamps(t *testing.T) {
	defer SetCodec(codecZstd, 1)
	for _, tc := range []struct {
		name      string
		level     int
		wantName  string
		wantLevel int
	}{
		{codecDeflate, 0, codecDeflate, 6}, {codecDeflate, 12, codecDeflate, 6}, {codecDeflate, 9, codecDeflate, 9},
		{codecZstd, 0, codecZstd, 1}, {codecZstd, 10, codecZstd, 10}, {codecNone, 5, codecNone, 0}, {"lz4", 3, codecZstd, 1},
	} {
		SetCodec(tc.name, tc.level)
		if codecSetting.name != tc.wantName || codecSetting.level != tc.wantLevel {
			t.Errorf("SetCodec(%q, %d) = %s-%d", tc.name, tc.level, codecSetting.name, codecSetting.level)
		}
		newBodyCodec().close() // must not panic for any input
	}
}
