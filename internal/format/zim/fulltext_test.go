// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package zim

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/wuweidict/wudict/internal/dict"
	"github.com/wuweidict/wudict/internal/ftsq"
	"github.com/wuweidict/wudict/internal/search"
)

// indexedZIM is an archive that carries glass's fixture index (see
// glass/glass_test.go) and the 3000 articles it was written from: document i
// is C/doc{i}, whose text is
//
//	the house of common{x (i%5)+1} group{i%7} id{i}
//	  + "running quickly" (odd i) or "walked slowly" (even i)
//	  + "rare 日本語" (i a multiple of 100)
//
// edit, when set, rewrites the index before it is stored.
func indexedZIM(t *testing.T, edit func([]byte) []byte) string {
	t.Helper()
	f, err := os.Open("glass/testdata/small.glass.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	index, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	if edit != nil {
		index = edit(index)
	}
	b := bFile{
		major: 6, minor: 3,
		clusters: []bCluster{{codec: 5}, {codec: 1, extended: true}},
		entries: []bEntry{
			{ns: 'M', path: "Language", mime: "text/plain", body: []byte("eng"), cluster: 0},
			{ns: 'M', path: "Title", mime: "text/plain", body: []byte("Indexed"), cluster: 0},
		},
	}
	if index != nil {
		b.entries = append(b.entries, bEntry{ns: 'X', path: "fulltext/xapian", mime: "application/octet-stream+xapian", body: index, cluster: 1})
	}
	for i := 1; i <= 3000; i++ {
		text := "the house of " + strings.Repeat("common ", i%5+1) + fmt.Sprintf("group%d id%d", i%7, i)
		if i%2 == 1 {
			text += " <b>running</b> quickly"
		} else {
			text += " walked slowly"
		}
		if i%100 == 0 {
			text += " rare 日本語"
		}
		b.entries = append(b.entries, bEntry{ns: 'C', path: fmt.Sprintf("doc%d", i), mime: "text/html",
			body: []byte("<html><body><p>" + text + "</p></body></html>"), cluster: 0})
	}
	return writeZIM(t, b)
}

func docIDs(res []dict.Result) (ids []int) {
	for _, r := range res {
		var i int
		fmt.Sscanf(r.Headword, "doc%d", &i)
		ids = append(ids, i)
	}
	slices.Sort(ids)
	return ids
}

func idsWhere(keep func(i int) bool) (ids []int) {
	for i := 1; i <= 3000; i++ {
		if keep(i) {
			ids = append(ids, i)
		}
	}
	return ids
}

func TestOwnFullText(t *testing.T) {
	path := indexedZIM(t, nil)
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if !d.Caps().FTS || !dict.OwnText(path) {
		t.Fatalf("an archive with an index of its own: caps %+v, OwnText %v", d.Caps(), dict.OwnText(path))
	}
	for _, c := range []struct {
		query string
		rung  string
		keep  func(i int) bool
	}{
		// One word is matched by its stem, whatever form was typed.
		{"group3", "words", func(i int) bool { return i%7 == 3 }},
		{"run", "words", func(i int) bool { return i%2 == 1 }},
		{"Running", "words", func(i int) bool { return i%2 == 1 }},
		// An open last word stands for the index terms that begin with it.
		{"rar", "words", func(i int) bool { return i%100 == 0 }},
		{"id299", "words", func(i int) bool { return i == 299 || i/10 == 299 }},
		{"runn", "", func(int) bool { return false }}, // no index term begins so: the index holds "run"
		// A phrase is found in the text: across the <b> that splits it, and
		// not where the same words stand apart or the other way round.
		{"running quickly", "phrase", func(i int) bool { return i%2 == 1 }},
		{"group3 id17", "phrase", func(i int) bool { return i%7 == 3 && strings.HasPrefix(fmt.Sprint(i), "17") }},
		{"group3 id2999", "phrase", func(i int) bool { return i == 2999 }},
		{"house of common", "phrase", func(i int) bool { return true }},
		{"quickly running", "near", func(i int) bool { return i%2 == 1 }},
		{"id2999 house", "near", func(i int) bool { return i == 2999 }},
		{"rare group2", "near", func(i int) bool { return i%100 == 0 && i%7 == 2 }},
		// Operators, as written.
		{`"running quickly" group3`, "query", func(i int) bool { return i%2 == 1 && i%7 == 3 }},
		{`"quickly running"`, "", func(int) bool { return false }},
		{`group3 NOT running`, "query", func(i int) bool { return i%7 == 3 && i%2 == 0 }},
		{`group3 NOT "walked slowly"`, "query", func(i int) bool { return i%7 == 3 && i%2 == 1 }},
		{`rare OR "id5"`, "query", func(i int) bool { return i%100 == 0 || i == 5 }},
		{`NEAR(house "id17", 10)`, "query", func(i int) bool { return i == 17 }},
		{`NEAR(house "id19", 1)`, "", func(int) bool { return false }},
		// A character of a spaceless script, and a run of them.
		{"日本語", "words", func(i int) bool { return i%100 == 0 }},
		// Nothing the index holds.
		{"the of", "", func(int) bool { return false }},
		{"nosuchword", "", func(int) bool { return false }},
	} {
		h := search.Query(context.Background(), d, search.FullText, c.query, 5000)
		if h.Err != nil || h.Skipped {
			t.Errorf("%q: err %v, skipped %v", c.query, h.Err, h.Skipped)
			continue
		}
		if want := idsWhere(c.keep); h.Rung != c.rung || !slices.Equal(docIDs(h.Results), want) {
			t.Errorf("%q: rung %q with %d results (first %v), want %q with %d (first %v)",
				c.query, h.Rung, len(h.Results), firstInts(docIDs(h.Results)), c.rung, len(want), firstInts(want))
		}
		if h.Partial || h.More {
			t.Errorf("%q: partial %v, more %v on an archive read whole", c.query, h.Partial, h.More)
		}
	}
}

func firstInts(v []int) []int { return v[:min(len(v), 4)] }

// The forms the index matched by stem are marked, beside what was typed.
func TestOwnFullTextMarks(t *testing.T) {
	d, err := Open(indexedZIM(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	marked := func(query string) (texts []string) {
		h := search.Query(context.Background(), d, search.FullText, query, 3)
		if h.Err != nil || len(h.Results) == 0 {
			t.Fatalf("%q: %v, %d results", query, h.Err, len(h.Results))
		}
		for _, p := range h.Marks {
			var words []string
			for _, term := range p {
				words = append(words, term.Text)
			}
			texts = append(texts, strings.Join(words, " "))
		}
		return texts
	}
	// "walked" is why the article answers "walks", and no word typed begins it.
	if got := marked("walks"); !slices.Equal(got, []string{"walks", "walked"}) {
		t.Errorf("marks for walks: %q", got)
	}
	// What was typed already covers the form found: nothing is added.
	if got := marked("run"); !slices.Equal(got, []string{"run"}) {
		t.Errorf("marks for run: %q", got)
	}
	if got := marked("slowly walks"); !slices.Equal(got, []string{"slowly", "walks", "walked"}) {
		t.Errorf("marks for slowly walks: %q", got)
	}
}

// A word left open that more index terms begin with than are followed says
// its answer is not all of it.
func TestOwnFullTextWide(t *testing.T) {
	d, err := Open(indexedZIM(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	h := search.Query(context.Background(), d, search.FullText, "id", 5000)
	if h.Err != nil || len(h.Results) != maxExpand || !h.More {
		t.Errorf("id*: %d results, more %v, err %v", len(h.Results), h.More, h.Err)
	}
	if h = search.Query(context.Background(), d, search.FullText, "id29", 5000); len(h.Results) != 111 || h.More {
		t.Errorf("id29*: %d results, more %v", len(h.Results), h.More)
	}
}

// A limit below the answer keeps the best-ranked articles.
func TestOwnFullTextLimit(t *testing.T) {
	d, err := Open(indexedZIM(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	h := search.Query(context.Background(), d, search.FullText, "group3", 4)
	// Xapian's BM25 puts the shortest documents first, the lower id ahead.
	if got := docIDs(h.Results); !slices.Equal(got, []int{10, 45, 80, 115}) {
		t.Errorf("the four best of group3: %v", got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := d.FullTextPlan(ctx, ftsq.Parse("running quickly").Rungs(), 10); err == nil {
		t.Error("a cancelled plan answered")
	}
}

// Without an index it can read, an archive has no full text until one of
// ours is built: none at all, one stemmed for a language with no stemmer
// here, one that is not a database.
func TestNoOwnFullText(t *testing.T) {
	for name, edit := range map[string]func([]byte) []byte{
		"absent": func([]byte) []byte { return nil },
		"catalan": func(b []byte) []byte {
			return bytes.Replace(b, []byte("\x00\xc0languageeng"), []byte("\x00\xc0languagecat"), 1)
		},
		"damaged": func(b []byte) []byte { return append([]byte("not glass"), b[9:]...) },
		"cut":     func(b []byte) []byte { return b[:10] },
	} {
		path := indexedZIM(t, edit)
		d, err := Open(path)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if d.Caps().FTS || dict.OwnText(path) {
			t.Errorf("%s: full text offered", name)
		}
		if h := search.Query(context.Background(), d, search.FullText, "running", 5); !h.Skipped || h.Err != nil {
			t.Errorf("%s: skipped %v, err %v", name, h.Skipped, h.Err)
		}
		if res, err := d.Exact("doc7", 1); err != nil || len(res) != 1 {
			t.Errorf("%s: the archive itself: %v, %d results", name, err, len(res))
		}
		d.Close()
	}
}
