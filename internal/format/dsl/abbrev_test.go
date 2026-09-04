// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package dsl

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wuweidict/wudict/internal/store"
)

// writePair writes a DSL and its abbreviation companion into ONE folder, which
// is what makes them a pair - writeDSL gives every file a temp dir of its own.
func writePair(t *testing.T, main, abrv string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	mainPath := filepath.Join(dir, "Mini.dsl")
	abrvPath := filepath.Join(dir, "Mini_abrv.dsl")
	if err := os.WriteFile(mainPath, []byte(main), 0o644); err != nil {
		t.Fatal(err)
	}
	if abrv != "" {
		if err := os.WriteFile(abrvPath, []byte(abrv), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return mainPath, abrvPath
}

const abbrevDSL = "#NAME \"Mini abbreviations\"\n" +
	"#INDEX_LANGUAGE \"English\"\n" +
	"\n" +
	"pl\n" +
	"\tplural\n" +
	"\n" +
	"Adj.\n" +
	"\t[b]adjective[/b]\n" +
	"\n" +
	"ok\n" +
	"\tsays \"fine\"\n"

// The label lookup, exercised through the real transformer. A miss must emit
// byte-for-byte what the transformer emitted before abbreviations existed -
// that invariant is what keeps the golden table in dsl_test.go valid.
func TestCloseLabelAbbrev(t *testing.T) {
	ab := &abbrevMap{
		exact: map[string]string{"pl": "plural", "Adj.": "adjective", "ok": `says "fine"`},
		fold:  map[string]string{"pl": "plural", "adj.": "adjective", "ok": `says "fine"`},
		count: 3,
	}
	cases := []struct{ in, want string }{
		{`[p]pl[/p]`, `<i class="p"><abbr class="wudict-abbr" title="plural"><font color="green">pl</font></abbr></i>`},
		// case-folded: the glossary is keyed "Adj.", the article says "adj."
		{`[p]adj.[/p]`, `<i class="p"><abbr class="wudict-abbr" title="adjective"><font color="green">adj.</font></abbr></i>`},
		// a quote in the expansion may never break out of the attribute
		{`[p]ok[/p]`, `<i class="p"><abbr class="wudict-abbr" title="says &quot;fine&quot;"><font color="green">ok</font></abbr></i>`},
		// miss: exactly the pre-abbreviation bytes
		{`[p]zzz[/p]`, `<i class="p"><font color="green">zzz</font></i>`},
		// nested markup inside the label still resolves on its plain text
		{`[p][i]pl[/i][/p]`, `<i class="p"><abbr class="wudict-abbr" title="plural"><font color="green"><i>pl</i></font></abbr></i>`},
	}
	for _, c := range cases {
		got, _, err := transformBodyAbbrev(c.in, "", ab)
		if err != nil {
			t.Fatalf("%q: %v", c.in, err)
		}
		if got != c.want {
			t.Errorf("transformBodyAbbrev(%q)\n got %q\nwant %q", c.in, got, c.want)
		}
	}
	// a nil map is how "no companion" is spelled, and must change nothing
	got, _, err := transformBodyAbbrev(`[p]pl[/p]`, "", nil)
	if err != nil || got != `<i class="p"><font color="green">pl</font></i>` {
		t.Errorf("nil map: %q, %v", got, err)
	}
}

// End to end through the Reader: the companion is found from the main file's
// path, its expansions are baked into the parent's articles, and ExtraMeta
// records what was absorbed so a later change can be detected.
func TestReaderAbsorbsAbbrev(t *testing.T) {
	main := "#NAME \"Mini\"\n#INDEX_LANGUAGE \"English\"\n\ndog\n\t[p]pl[/p] canine\n"
	mainPath, abrvPath := writePair(t, main, abbrevDSL)

	r, err := NewReader(mainPath)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	e, err := r.Next()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(e.Body, `title="plural"`) {
		t.Fatalf("expansion not baked in: %q", e.Body)
	}
	meta := r.ExtraMeta()
	if meta["abbrev_path"] != abrvPath {
		t.Errorf("abbrev_path = %q, want %q", meta["abbrev_path"], abrvPath)
	}
	if meta["abbrev_count"] != "3" {
		t.Errorf("abbrev_count = %q, want 3", meta["abbrev_count"])
	}
	if meta["abbrev_size"] == "" || meta["abbrev_mtime"] == "" {
		t.Errorf("staleness keys missing: %v", meta)
	}

	// no companion: no meta, no change to the articles
	solo, _ := writePair(t, main, "")
	r2, err := NewReader(solo)
	if err != nil {
		t.Fatal(err)
	}
	defer r2.Close()
	e2, err := r2.Next()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(e2.Body, "<abbr") {
		t.Errorf("no companion, yet an abbr appeared: %q", e2.Body)
	}
	if m := r2.ExtraMeta(); m != nil {
		t.Errorf("ExtraMeta without a companion = %v, want nil", m)
	}
}

// All inputs are hostile: an oversized companion is not read at all, a huge one
// is truncated by key count, and a single monstrous expansion is cut by runes.
func TestLoadAbbrevBounds(t *testing.T) {
	mainPath, abrvPath := writePair(t, "#NAME \"Mini\"\n\ndog\n\tcanine\n", abbrevDSL)
	if err := os.Truncate(abrvPath, maxAbbrevFile+1); err != nil {
		t.Fatal(err)
	}
	if a := loadAbbrev(mainPath); a != nil {
		t.Errorf("an oversized companion must be ignored, got %d keys", a.count)
	}

	var b strings.Builder
	b.WriteString("#NAME \"Big\"\n\n")
	for i := 0; i < maxAbbrevKeys+5; i++ {
		fmt.Fprintf(&b, "k%d\n\tv%d\n\n", i, i)
	}
	b.WriteString("long\n\t" + strings.Repeat("é", maxAbbrevRunes+50) + "\n")
	mainPath2, _ := writePair(t, "#NAME \"Mini\"\n\ndog\n\tcanine\n", b.String())
	a := loadAbbrev(mainPath2)
	if a == nil || a.count != maxAbbrevKeys {
		t.Fatalf("key cap not applied: %+v", a)
	}

	mainPath3, _ := writePair(t, "#NAME \"Mini\"\n\ndog\n\tcanine\n",
		"#NAME \"L\"\n\nlong\n\t"+strings.Repeat("é", maxAbbrevRunes+50)+"\n\nself\n\tself\n")
	a3 := loadAbbrev(mainPath3)
	if a3 == nil {
		t.Fatal("companion not loaded")
	}
	exp := a3.exact["long"]
	if r := []rune(exp); len(r) != maxAbbrevRunes+1 || !strings.HasSuffix(exp, "…") {
		t.Errorf("rune cap: %d runes, %q", len(r), exp[len(exp)-8:])
	}
	// an entry expanding to its own headword says nothing and is dropped
	if _, ok := a3.exact["self"]; ok {
		t.Error("self-referential entry must be dropped")
	}
}

// The round trip that matters in production: the pair goes through the real
// ingester, the expansion is in the stored article, and the staleness keys
// survive into meta so store.AbbrevChanged can see a later edit.
func TestIngestAbsorbsAbbrev(t *testing.T) {
	main := "#NAME \"Mini\"\n#INDEX_LANGUAGE \"English\"\n\ndog\n\t[p]pl[/p] canine\n"
	mainPath, abrvPath := writePair(t, main, abbrevDSL)

	r, err := NewReader(mainPath)
	if err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(t.TempDir(), "text.db")
	if _, err := store.IngestPlan(r, dbPath, store.Plan{}, nil); err != nil {
		r.Close()
		t.Fatal(err)
	}
	r.Close()

	meta, err := store.ReadMeta(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if meta["abbrev_count"] != "3" || meta["abbrev_path"] != abrvPath {
		t.Fatalf("abbrev meta not recorded: %v", meta)
	}
	if store.AbbrevChanged(dbPath, abrvPath) {
		t.Error("a freshly ingested pair must not read as stale")
	}
	// touching the companion is what forces the parent to be re-ingested
	future := time.Now().Add(2 * time.Hour)
	if err := os.Chtimes(abrvPath, future, future); err != nil {
		t.Fatal(err)
	}
	if !store.AbbrevChanged(dbPath, abrvPath) {
		t.Error("a changed companion must read as stale")
	}

	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	res, err := s.Exact("dog", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || !strings.Contains(res[0].Body, `title="plural"`) {
		t.Fatalf("stored article lost the expansion: %+v", res)
	}
}
