// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/wuweidict/wudict/internal/dict"
	"github.com/wuweidict/wudict/internal/fsx"
)

func fullTextEntries() []dict.Entry {
	es := []dict.Entry{
		h("alpha", "<p>one <b>two</b> three</p>"),
		h("beta", "<p>three four</p>"),
		h("@sub", "<p>three hidden</p>"),
		h("gamma", "<p>five three</p><script>three</script>"),
	}
	for i := 0; i < 600; i++ { // more than one batch of the builder
		es = append(es, h(fmt.Sprintf("n%03d", i), fmt.Sprintf("<p>filler %d</p>", i)))
	}
	return es
}

func matchWords(t *testing.T, textDB, match string, limit int) []string {
	t.Helper()
	s, err := Open(textDB)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	res, err := s.FullTextMatch(context.Background(), match, limit)
	if err != nil {
		t.Fatalf("%s: %v", match, err)
	}
	var out []string
	for _, r := range res {
		out = append(out, r.Headword)
	}
	slices.Sort(out)
	return out
}

// Full text is a file beside the text: the text.db has no index of it, the
// pair answers as one database did, and a text.db alone has no full text.
func TestFullTextBesideText(t *testing.T) {
	textDB := ingestTo(t, fullTextEntries(), Plan{FullText: true})
	ftDB := FullTextSibling(textDB)
	if !fsx.FileExists(ftDB) || hasTable(t, textDB, "entry_fts") || !hasTable(t, ftDB, "entry_fts") {
		t.Fatal("the index is not in fulltext.db alone")
	}
	if p := Inspect(textDB); !p.Plan.FullText || PlanFromMeta(p.Meta).FullText {
		t.Errorf("plan %+v, recorded level %q", p.Plan, p.Meta["ingest_level"])
	}
	for match, want := range map[string][]string{
		`"three"*`:       {"alpha", "beta", "gamma"}, // not @sub, not the script
		`"one two"`:      {"alpha"},
		`"alpha"`:        {"alpha"}, // the headword column
		`"filler" "599"`: {"n599"},
		`"nosuch"`:       nil,
	} {
		if got := matchWords(t, textDB, match, 10); !slices.Equal(got, want) {
			t.Errorf("%s: %v, want %v", match, got, want)
		}
	}
	// The sub-entry the index holds does not cost a result its place.
	if got := matchWords(t, textDB, `"three"*`, 3); len(got) != 3 {
		t.Errorf("limit 3 returned %v", got)
	}
	if got := matchWords(t, textDB, `"filler"`, 0); len(got) != maxLimit {
		t.Errorf("no limit returned %d", len(got))
	}

	// Without its index the text has none: nothing fails, full text is off.
	if err := os.Remove(ftDB); err != nil {
		t.Fatal(err)
	}
	s, err := Open(textDB)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.Caps().FTS || Inspect(textDB).Plan.FullText {
		t.Error("full text reported with no index")
	}
	if got, err := s.ExactContext(context.Background(), "beta", 5); err != nil || len(got) != 1 {
		t.Errorf("the text itself: %v, %v", got, err)
	}
}

// The index built later, from the text, is the index an ingest writes.
func TestBuildFullTextFromText(t *testing.T) {
	entries := fullTextEntries()
	want, err := Fingerprint(ingestTo(t, entries, Plan{FullText: true}))
	if err != nil {
		t.Fatal(err)
	}
	for _, codec := range []string{codecZstd, codecDeflate, codecNone} {
		withCodec(t, codec, 0)
		textDB := ingestTo(t, entries, Plan{})
		if Inspect(textDB).Plan.FullText {
			t.Fatal("a headwords-only build has full text")
		}
		done, total := 0, 0
		if err := buildFullText(context.Background(), textDB, func(d, n int) { done, total = d, n }); err != nil {
			t.Fatal(err)
		}
		if done != len(entries) || total != len(entries) {
			t.Errorf("%s: progress ended at %d of %d, want %d", codec, done, total, len(entries))
		}
		if got, err := Fingerprint(textDB); err != nil || got != want {
			t.Errorf("%s: built from the text %s, %v; ingested %s", codec, got, err, want)
		}
		left, _ := filepath.Glob(filepath.Join(filepath.Dir(textDB), "*.ingest.*"))
		if len(left) != 0 {
			t.Errorf("working files left: %v", left)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	textDB := ingestTo(t, entries, Plan{})
	if err := buildFullText(ctx, textDB, nil); err == nil || fsx.FileExists(FullTextSibling(textDB)) {
		t.Errorf("a cancelled build: %v, index present %v", err, fsx.FileExists(FullTextSibling(textDB)))
	}
}

// An index serves the build of the text it was made from and no other.
func TestFullTextPairing(t *testing.T) {
	a := ingestTo(t, fullTextEntries(), Plan{FullText: true})
	b := ingestTo(t, fullTextEntries(), Plan{})
	copyFile(t, FullTextSibling(a), FullTextSibling(b))
	if Inspect(b).Plan.FullText {
		t.Error("another dictionary's index was taken")
	}
	// The same dictionary, built again: its entries may be numbered anew.
	setMeta(t, a, "created", "2001-01-01T00:00:00Z")
	if Inspect(a).Plan.FullText {
		t.Error("the index of an earlier build of the text was taken")
	}
	c := ingestTo(t, fullTextEntries(), Plan{FullText: true})
	setMeta(t, FullTextSibling(c), "fulltext_version", "0")
	if Inspect(c).Plan.FullText {
		t.Error("an index of another version was taken")
	}
	// An ingest without full text leaves no index of the text it replaced.
	r := &fakeReader{meta: dict.Meta{Name: "codec", Format: "test", Path: filepath.Join(t.TempDir(), "x.src")}, entries: fullTextEntries()}
	if _, err := IngestPlan(r, c, Plan{}, nil); err != nil {
		t.Fatal(err)
	}
	if fsx.FileExists(FullTextSibling(c)) {
		t.Error("a headwords-only ingest left the old index beside the text")
	}
	// A loose database pairs by name, and one that is not a text.db cannot.
	if got := FullTextSibling(filepath.Join("d", "Name.text.db")); got != filepath.Join("d", "Name.fulltext.db") {
		t.Errorf("loose sibling %q", got)
	}
	out := filepath.Join(t.TempDir(), "plain.db")
	r = &fakeReader{meta: dict.Meta{Name: "codec", Format: "test", Path: filepath.Join(t.TempDir(), "y.src")}, entries: fullTextEntries()}
	if _, err := IngestPlan(r, out, Plan{FullText: true}, nil); err == nil || fsx.FileExists(out) {
		t.Errorf("full text into %s: %v", out, err)
	}
}

// A text.db written before fulltext.db carries its index inside it: it is
// read as it is, and loses the index only by being written again.
func TestFullTextInsideText(t *testing.T) {
	textDB := ingestTo(t, fullTextEntries(), Plan{})
	s, err := Open(textDB)
	if err != nil {
		t.Fatal(err)
	}
	type row struct{ w, txt string }
	var rows []row
	if err := s.EachEntry(func(hw string, _ []string, body string) error {
		rows = append(rows, row{hw, StripHTML(body)})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	s.Close()
	db, err := sql.Open(driverName, dsnIngest(textDB))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE VIRTUAL TABLE entry_fts USING fts5(w, txt, content='', columnsize=0,
		tokenize='unicode61 remove_diacritics 2')`); err != nil {
		t.Fatal(err)
	}
	for i, r := range rows {
		if _, err := db.Exec("INSERT INTO entry_fts(rowid, w, txt) VALUES(?, ?, ?)", i+1, r.w, r.txt); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	setMeta(t, textDB, "ingest_level", levelText)

	if p := Inspect(textDB); !p.Plan.FullText {
		t.Fatal("the index inside the text is not seen")
	}
	if got := matchWords(t, textDB, `"three"*`, 10); !slices.Equal(got, []string{"alpha", "beta", "gamma"}) {
		t.Errorf("matched %v", got)
	}
	// A stray fulltext.db beside it changes nothing.
	other := ingestTo(t, fullTextEntries(), Plan{FullText: true})
	copyFile(t, FullTextSibling(other), FullTextSibling(textDB))
	if got := matchWords(t, textDB, `"one two"`, 10); !slices.Equal(got, []string{"alpha"}) {
		t.Errorf("matched %v with a stray index beside", got)
	}
}

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	b, err := os.ReadFile(from)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(to, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// A dictionary that is its text.db alone - the files it was prepared from are
// gone - still gains and loses its full-text index, which is made of the text;
// nothing else about it can change.
func TestReconcileWithoutSource(t *testing.T) {
	yes, no := true, false
	for _, textDB := range []string{
		ingestTo(t, fullTextEntries(), Plan{}), // a library folder
		func() string { // a loose <name>.text.db
			p := filepath.Join(t.TempDir(), "Loose.text.db")
			copyFile(t, ingestTo(t, fullTextEntries(), Plan{}), p)
			return p
		}(),
	} {
		released := 0
		hooks := Hooks{
			Reader:  func() (dict.Reader, error) { t.Fatal("the source was asked for"); return nil, nil },
			Release: func(string) { released++ },
		}
		out, err := Reconcile(textDB, Target{FullText: &yes}, hooks)
		if err != nil || !out.FullTextBuilt || out.Rebuilt || !out.Plan.FullText || released == 0 {
			t.Fatalf("%s: adding full text: %+v, %v", filepath.Base(textDB), out, err)
		}
		if got := matchWords(t, textDB, `"one two"`, 5); !slices.Equal(got, []string{"alpha"}) {
			t.Errorf("matched %v", got)
		}
		if out, err = Reconcile(textDB, Target{FullText: &yes}, hooks); err != nil || out.Changed() {
			t.Errorf("adding it again: %+v, %v", out, err)
		}
		if out, err = Reconcile(textDB, Target{}, hooks); err != nil || out.Changed() || !out.Plan.FullText {
			t.Errorf("nothing asked: %+v, %v", out, err)
		}
		if _, err = Reconcile(textDB, Target{Media: MediaOn}, hooks); !errors.Is(err, ErrNoSource) {
			t.Errorf("packing media: %v", err)
		}
		if _, err = Reconcile(textDB, Target{Media: MediaOff}, hooks); !errors.Is(err, ErrNoSource) {
			t.Errorf("removing media: %v", err)
		}
		out, err = Reconcile(textDB, Target{FullText: &no, Rebuild: Always}, hooks)
		if err != nil || !out.FullTextRemoved || out.Rebuilt || out.Plan.FullText || fsx.FileExists(FullTextSibling(textDB)) {
			t.Errorf("removing full text: %+v, %v", out, err)
		}
		s, err := Open(textDB)
		if err != nil {
			t.Errorf("the text afterwards: %v", err)
		} else {
			s.Close() // the TempDir cleanup cannot unlink an open file (Windows)
		}
	}
	// An index inside the text can only go by writing the text again.
	inside := ingestTo(t, fullTextEntries(), Plan{})
	setMeta(t, inside, "ingest_level", levelText)
	if _, err := Reconcile(inside, Target{FullText: &no}, Hooks{}); !errors.Is(err, ErrNoSource) {
		t.Errorf("removing an index that is inside the text: %v", err)
	}
}

// Results come in the index's rank order, across batches, past more
// sub-entries than one batch holds, and stop at the limit.
func TestFullTextRankOrder(t *testing.T) {
	var es []dict.Entry
	for i := 0; i < 300; i++ {
		// the repeats vary the rank; every third entry is a sub-entry
		body := "<p>" + strings.Repeat("kappa ", 1+i%7) + fmt.Sprint(i) + "</p>"
		w := fmt.Sprintf("k%03d", i)
		if i%3 == 0 {
			w = "@" + w
		}
		es = append(es, h(w, body))
	}
	textDB := ingestTo(t, es, Plan{FullText: true})

	ft, err := sql.Open(driverName, dsnRO(FullTextSibling(textDB)))
	if err != nil {
		t.Fatal(err)
	}
	defer ft.Close()
	text, err := sql.Open(driverName, dsnRO(textDB))
	if err != nil {
		t.Fatal(err)
	}
	defer text.Close()
	rows, err := ft.Query(`SELECT rowid FROM entry_fts WHERE entry_fts MATCH '"kappa"' ORDER BY rank`)
	if err != nil {
		t.Fatal(err)
	}
	var ranked []string
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		var w string
		if err := text.QueryRow("SELECT w FROM entry WHERE id = ?", id).Scan(&w); err != nil {
			t.Fatal(err)
		}
		if !isSubEntry(w) {
			ranked = append(ranked, w)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	if len(ranked) != 200 {
		t.Fatalf("%d entries ranked, want 200", len(ranked))
	}

	s, err := Open(textDB)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, limit := range []int{1, 63, 64, 65, 150, 200, 500} {
		res, err := s.FullTextMatch(context.Background(), `"kappa"`, limit)
		if err != nil {
			t.Fatal(err)
		}
		got := make([]string, len(res))
		for i, r := range res {
			got[i] = r.Headword
			if r.Body == "" {
				t.Errorf("limit %d: %s has no article", limit, r.Headword)
			}
		}
		if want := ranked[:min(limit, len(ranked))]; !slices.Equal(got, want) {
			t.Errorf("limit %d: %d results, first %v, want %d, first %v", limit, len(got), got[:min(3, len(got))], len(want), want[:min(3, len(want))])
		}
	}
}
