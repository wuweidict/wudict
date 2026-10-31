// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"encoding/csv"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/wuweidict/wudict/internal/dict"
	"github.com/wuweidict/wudict/internal/store"
)

// dumpBase names the file the user gets. A prepared dictionary's file is
// always called text.db, so the name has to come from the folder or every
// library dump would be called text.csv.
func TestDumpBase(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{"plain source", "/d/Oxford.mdx", "Oxford"},
		{"double extension", "/d/big.dsl.dz", "big"},
		{"library folder", "/db/Oxford ALD/text.db", "Oxford ALD"},
		{"library folder, cased file", "/db/Oxford ALD/TEXT.DB", "Oxford ALD"},
		{"detached text.db", "/tmp/Collins.text.db", "Collins"},
		{"illegal characters", "/d/a:b?c.mdx", "a-b-c"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := dumpBase(filepath.FromSlash(tt.src)); got != tt.want {
				t.Errorf("dumpBase(%q) = %q, want %q", tt.src, got, tt.want)
			}
		})
	}
}

// safeName and resFilePath are the whole of the zip-slip defence: the names
// come from the dictionary, and a container is free to hold "..", an absolute
// path or a NUL. Nothing they return may leave resDir.
func TestResFilePath(t *testing.T) {
	root := filepath.FromSlash("/out/d.csv_res")
	tests := []struct {
		name string
		in   string
		want string // "" = refused
	}{
		{"plain", "a.mp3", "/out/d.csv_res/a.mp3"},
		{"subtree kept", "images/i/x.png", "/out/d.csv_res/images/i/x.png"},
		{"backslashes", `images\x.png`, "/out/d.csv_res/images/x.png"},
		{"leading slash", "/a.mp3", "/out/d.csv_res/a.mp3"},
		{"climbing", "../../etc/passwd", "/out/d.csv_res/etc/passwd"},
		{"climbing mid-path", "a/../../b.png", "/out/d.csv_res/b.png"},
		{"dot components", "./a/./b.png", "/out/d.csv_res/a/b.png"},
		{"control characters", "a\x00b\x1f.mp3", "/out/d.csv_res/ab.mp3"},
		{"trailing dot", "a.mp3.", "/out/d.csv_res/a.mp3"},
		{"query-looking name", "menu.svg@version=3.0", "/out/d.csv_res/menu.svg@version=3.0"},
		{"empty", "", ""},
		{"nothing usable", "...", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resFilePath(root, tt.in)
			want := ""
			if tt.want != "" {
				want = filepath.FromSlash(tt.want)
			}
			if got != want {
				t.Fatalf("resFilePath(%q) = %q, want %q", tt.in, got, want)
			}
			if got != "" && !strings.HasPrefix(got, root+string(filepath.Separator)) {
				t.Fatalf("resFilePath(%q) escaped the resource dir: %q", tt.in, got)
			}
		})
	}
}

// fakeReader is a format Reader with no format behind it: the ingest contract
// is all dump uses, so the test does not need a real container to exercise the
// paths that matter (aliases, redirects, body normalization).
type fakeReader struct {
	meta    dict.Meta
	entries []dict.Entry
	i       int
}

func (r *fakeReader) Meta() dict.Meta { return r.meta }
func (r *fakeReader) Close() error    { return nil }
func (r *fakeReader) Next() (dict.Entry, error) {
	if r.i >= len(r.entries) {
		return dict.Entry{}, io.EOF
	}
	e := r.entries[r.i]
	r.i++
	return e, nil
}

func testReader() *fakeReader {
	return &fakeReader{
		meta: dict.Meta{Name: "Test Dict", Description: "a fixture", IndexLang: "en"},
		entries: []dict.Entry{
			{Headwords: []string{"aardvark", "ant bear"}, Body: "<b>burrowing</b>, \"quoted\"\nnext", Kind: dict.BodyHTML},
			{Headwords: []string{"know"}, Body: "to be aware", Kind: dict.BodyText},
			{Headwords: []string{"knew"}, LinkTo: "know"},
		},
	}
}

// readAll is the source-file path: a redirect has no body of its own and is
// written as an entry:// anchor.
func TestReadAllRedirect(t *testing.T) {
	var got [][]string
	err := readAll(testReader(), func(words []string, body string) error {
		got = append(got, append(append([]string{}, words...), body))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d rows, want 3: %q", len(got), got)
	}
	if want := []string{"aardvark", "ant bear", "<b>burrowing</b>, \"quoted\"\nnext"}; !reflect.DeepEqual(got[0], want) {
		t.Errorf("row 0 = %q, want %q", got[0], want)
	}
	if want := `<a href="entry://know">know</a>`; got[2][1] != want {
		t.Errorf("redirect body = %q, want %q", got[2][1], want)
	}
	// BodyText is escaped and wrapped by the ingest normalizer, not passed
	// through: the CSV must hold the same HTML a prepared dictionary would.
	if got[1][1] == "to be aware" {
		t.Errorf("BodyText body was not normalized: %q", got[1][1])
	}
}

// The prepared path: ingest resolves the redirect into an alias, so `knew`
// comes back as an alternate of `know` rather than as a row of its own. Both
// spellings are legitimate; this pins which one the library folder produces.
func TestDumpEntriesPrepared(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "Test Dict", store.TextDBName)
	if _, err := store.IngestPlan(testReader(), dbPath, store.Plan{}, func(done, total int) {}); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "dump")
	csvPath := filepath.Join(out, dumpBase(dbPath)+".csv")
	res, err := dumpEntries(dbPath, out, csvPath)
	if err != nil {
		t.Fatal(err)
	}
	if res.rows != 2 {
		t.Fatalf("wrote %d entries, want 2 (the redirect is an alias)", res.rows)
	}
	if filepath.Base(csvPath) != "Test Dict.csv" {
		t.Fatalf("csv name = %q", filepath.Base(csvPath))
	}

	f, err := os.Open(csvPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	r := csv.NewReader(f)
	r.FieldsPerRecord = -1 // info rows are 2 fields, an aliased entry 3
	rows, err := r.ReadAll()
	if err != nil {
		t.Fatalf("the file is not valid CSV: %v", err)
	}
	if len(rows) != 5 {
		t.Fatalf("got %d rows, want 5 (3 info + 2 entries): %q", len(rows), rows)
	}
	want := [][]string{
		{"#name", "Test Dict"},
		{"#sourceLang", "en"},
		{"#description", "a fixture"},
	}
	for i, w := range want {
		if !reflect.DeepEqual(rows[i], w) {
			t.Errorf("info row %d = %q, want %q", i, rows[i], w)
		}
	}
	if rows[3][0] != "aardvark" || len(rows[3]) != 3 || rows[3][2] != "ant bear" {
		t.Errorf("entry row = %q, want aardvark with alternate \"ant bear\"", rows[3])
	}
	if !strings.Contains(rows[3][1], "<b>burrowing</b>") {
		t.Errorf("body lost its markup: %q", rows[3][1])
	}
	if rows[4][0] != "know" || len(rows[4]) != 3 || rows[4][2] != "knew" {
		t.Errorf("redirect target row = %q, want know with alternate \"knew\"", rows[4])
	}
}

// An entry without a headword is left out of the CSV and counted, so the
// command can say so; an empty first headword drops the entry with its
// alternates, as pyglossary's reader would.
func TestDumpEntriesNameless(t *testing.T) {
	dict.RegisterReader(".namelesstest", func(string) (dict.Reader, error) {
		return &fakeReader{meta: dict.Meta{Name: "N"}, entries: []dict.Entry{
			{Headwords: []string{"a"}, Body: "x", Kind: dict.BodyText},
			{Headwords: nil, Body: "y", Kind: dict.BodyText},
			{Headwords: []string{"", "alt"}, Body: "z", Kind: dict.BodyText},
			{Headwords: []string{"b"}, Body: "w", Kind: dict.BodyText},
		}}, nil
	})
	dir := t.TempDir()
	src := filepath.Join(dir, "n.namelesstest")
	if err := os.WriteFile(src, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out")
	r, err := dumpEntries(src, out, filepath.Join(out, "n.csv"))
	if err != nil {
		t.Fatal(err)
	}
	if r.rows != 2 || r.nameless != 2 {
		t.Errorf("wrote %d, left out %d; want 2 and 2", r.rows, r.nameless)
	}
}

// A source that cannot be read must leave no output folder behind: the folder
// is created only after the dictionary has opened.
func TestDumpEntriesNoOutputOnFailure(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "broken.mdx")
	if err := os.WriteFile(src, []byte("not an mdx"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out")
	if _, err := dumpEntries(src, out, filepath.Join(out, "broken.csv")); err == nil {
		t.Fatal("dumping an unreadable file succeeded")
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatalf("output folder was created for a failed dump: %v", err)
	}
}

// TestCSVNeverBword: the CSV dump writes cross-references as entry://, both
// its own redirect anchors and the source's bword: links it passes through.
func TestCSVNeverBword(t *testing.T) {
	for _, tc := range []struct {
		name string
		e    dict.Entry
		want string
	}{
		{"redirect", dict.Entry{Headwords: []string{"a"}, LinkTo: "b c"}, `<a href="entry://b c">b c</a>`},
		// a redirect names a headword: a leading "@" is encoded so the host
		// looks it up instead of inlining an MDict sub-entry (R5.16)
		{"redirect to an @ headword", dict.Entry{Headwords: []string{"a"}, LinkTo: "@sub"}, `<a href="entry://%40sub">@sub</a>`},
		{"redirect to C#", dict.Entry{Headwords: []string{"a"}, LinkTo: "C#"}, `<a href="entry://C%23">C#</a>`},
		{"redirect escaped", dict.Entry{Headwords: []string{"a"}, LinkTo: `x"<y`}, `<a href="entry://x&#34;&lt;y">x&#34;&lt;y</a>`},
		{"source bword://", dict.Entry{Headwords: []string{"a"}, Body: `<a href="bword://run#s2">run</a>`, Kind: dict.BodyHTML},
			`<a href="entry://run#s2">run</a>`},
		{"source bword: no slashes", dict.Entry{Headwords: []string{"a"}, Body: `<a href=bword:run>run</a>`, Kind: dict.BodyHTML},
			`<a href="entry://run">run</a>`},
		{"source BWORD sub-entry", dict.Entry{Headwords: []string{"a"}, Body: `<a href="BWORD://@ex">e</a>`, Kind: dict.BodyHTML},
			`<a href="entry:@ex">e</a>`},
		{"text body untouched", dict.Entry{Headwords: []string{"a"}, Body: "see bword://x", Kind: dict.BodyText},
			"<p>see bword://x</p>"},
		{"entry:// untouched", dict.Entry{Headwords: []string{"a"}, Body: `<a href='entry://run'>run</a>`, Kind: dict.BodyHTML},
			`<a href='entry://run'>run</a>`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got string
			err := readAll(&fakeReader{entries: []dict.Entry{tc.e}}, func(_ []string, body string) error {
				got = body
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Errorf("body = %q, want %q", got, tc.want)
			}
		})
	}
}

// A prepared dictionary stores its source's links as found; the dump of its
// text.db respells them like the Reader path does.
func TestDumpEntriesPreparedNeverBword(t *testing.T) {
	src := &fakeReader{
		meta: dict.Meta{Name: "B"},
		entries: []dict.Entry{
			{Headwords: []string{"a"}, Body: `<a href="bword://run">run</a> <a href="bword:@sub">s</a>`, Kind: dict.BodyHTML},
		},
	}
	dbPath := filepath.Join(t.TempDir(), "B", store.TextDBName)
	if _, err := store.IngestPlan(src, dbPath, store.Plan{}, func(done, total int) {}); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "dump")
	csvPath := filepath.Join(out, "B.csv")
	if _, err := dumpEntries(dbPath, out, csvPath); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(csvPath)
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	if strings.Contains(strings.ToLower(got), "bword:") {
		t.Errorf("dump still carries bword:\n%s", got)
	}
	for _, want := range []string{"entry://run", "entry:@sub"} {
		if !strings.Contains(got, want) {
			t.Errorf("dump lacks %q:\n%s", want, got)
		}
	}
}

func TestNewResSelect(t *testing.T) {
	tests := []struct {
		name             string
		scope            resScope
		include, exclude []string
		articles         bool
		articleFlags     bool
		wantErr          bool
	}{
		{name: "defaults", scope: resAll, articles: true},
		{name: "filters", scope: resText, include: []string{"css/"}, exclude: []string{"*.map"}, articles: true},
		{name: "resources only", scope: resAll, include: []string{"*.ogg"}},
		{name: "none with include", scope: resNone, include: []string{"*.ogg"}, articles: true, wantErr: true},
		{name: "none with exclude", scope: resNone, exclude: []string{"*.ogg"}, articles: true, wantErr: true},
		{name: "nothing at all", scope: resNone, wantErr: true},
		{name: "article flags without articles", scope: resAll, articleFlags: true, wantErr: true},
		{name: "empty include", scope: resAll, include: []string{""}, articles: true, wantErr: true},
		{name: "empty exclude", scope: resAll, exclude: []string{".."}, articles: true, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := newResSelect(tt.scope, tt.include, tt.exclude, tt.articles, tt.articleFlags)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, want an error: %v", err, tt.wantErr)
			}
		})
	}
}

func TestResSelectPick(t *testing.T) {
	names := []string{"style.css", "css/print.css", "audio/US/1.ogg", "audio/US/2.wav", "audio/UK/1.ogg", "img/a.png", "audio/.DS_Store"}
	tests := []struct {
		name                       string
		scope                      resScope
		include, exclude           []string
		want, noInclude, noExclude []string
	}{
		{name: "all, junk dropped", scope: resAll, want: names[:6]},
		{name: "text", scope: resText, want: []string{"style.css", "css/print.css"}},
		{name: "text and include narrow together", scope: resText, include: []string{"css/"}, want: []string{"css/print.css"}},
		{name: "includes add up", scope: resAll, include: []string{"*.png", "audio/UK/"}, want: []string{"audio/UK/1.ogg", "img/a.png"}},
		{name: "exclude beats include", scope: resAll, include: []string{"audio/"}, exclude: []string{"*.wav"}, want: []string{"audio/US/1.ogg", "audio/UK/1.ogg"}},
		{name: "no slash: any depth", scope: resAll, exclude: []string{"*.ogg", "*.wav"}, want: []string{"style.css", "css/print.css", "img/a.png"}},
		{name: "case ignored", scope: resAll, include: []string{"AUDIO/us/*"}, want: []string{"audio/US/1.ogg", "audio/US/2.wav"}},
		{name: "include outside -resources matches nothing", scope: resText, include: []string{"*.png", "*.css"}, want: []string{"style.css", "css/print.css"}, noInclude: []string{"*.png"}},
		{name: "exclude of nothing", scope: resAll, include: []string{"img/"}, exclude: []string{"*.ogg"}, want: []string{"img/a.png"}, noExclude: []string{"*.ogg"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sel, err := newResSelect(tt.scope, tt.include, tt.exclude, true, false)
			if err != nil {
				t.Fatal(err)
			}
			got, noInc, noExc := sel.pick(names)
			if !slices.Equal(got, tt.want) || !slices.Equal(noInc, tt.noInclude) || !slices.Equal(noExc, tt.noExclude) {
				t.Fatalf("pick = %q (no include %q, no exclude %q), want %q (%q, %q)",
					got, noInc, noExc, tt.want, tt.noInclude, tt.noExclude)
			}
		})
	}
}
