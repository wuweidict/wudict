// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package ftsq

import (
	"reflect"
	"testing"

	"github.com/wuweidict/wudict/internal/hilite"
)

func matches(rs []Rung) []string {
	var out []string
	for _, r := range rs {
		out = append(out, r.Name+" "+r.Match)
	}
	return out
}

// The ladder is the whole answer to "what does a bare query mean": the precise
// reading first, and every relaxation named so the UI can say which one spoke.
func TestPlainLadder(t *testing.T) {
	for _, tc := range []struct {
		name, in string
		want     []string
	}{
		{"three words", "Физика в конспектах", []string{
			`phrase "Физика в конспектах"*`,
			`near NEAR("Физика"* "в"* "конспектах"*, 10)`,
			`words "Физика"* "в"* "конспектах"*`,
		}},
		{"one word does not relax", "физика", []string{`words "физика"*`}},
		{"a trailing star is the default anyway", "рус*", []string{`words "рус"*`}},
		{"lower-case and is a word, not an operator", "salt and pepper", []string{
			`phrase "salt and pepper"*`,
			`near NEAR("salt"* "and"* "pepper"*, 10)`,
			`words "salt"* "and"* "pepper"*`,
		}},
		{"parenthetical notation is prose", "(coll.) foo", []string{
			`phrase "(coll.) foo"*`,
			`near NEAR("(coll.)"* "foo"*, 10)`,
			`words "(coll.)"* "foo"*`,
		}},
		{"nothing to search for", "  ", nil},
		{"no token survives", "!!! ???", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := matches(Parse(tc.in).Rungs())
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Parse(%q).Rungs()\n got %q\nwant %q", tc.in, got, tc.want)
			}
		})
	}
}

// An explicit query is answered once. Relaxing it would contradict what was
// asked, which is the one thing a professional's query language must not do.
func TestOperatorMode(t *testing.T) {
	for _, tc := range []struct {
		name, in, want string
	}{
		{"quoting means exact", `"faux ami"`, `"faux ami"`},
		{"a star reopens the last token", `"faux ami"*`, `"faux ami"*`},
		{"a bare word stays a prefix", `"faux ami" OR calque`, `("faux ami" OR "calque"*)`},
		{"juxtaposition is AND", `"faux ami" calque`, `("faux ami" AND "calque"*)`},
		{"explicit AND", `alpha AND beta`, `("alpha"* AND "beta"*)`},
		{"NOT is binary", `"faux ami" NOT linguistics`, `("faux ami" NOT "linguistics"*)`},
		{"NOT binds tighter than AND", `a AND b NOT c`, `("a"* AND ("b"* NOT "c"*))`},
		{"AND binds tighter than OR", `a OR b c`, `("a"* OR ("b"* AND "c"*))`},
		{"parentheses regroup", `("a" OR b) c`, `(("a" OR "b"*) AND "c"*)`},
		{"NEAR with a distance", `NEAR(alpha beta, 3)`, `NEAR("alpha"* "beta"*, 3)`},
		{"NEAR defaults to ten", `NEAR("a b" c)`, `NEAR("a b" "c"*, 10)`},
		{"an embedded quote is doubled", `"say ""no"""`, `"say ""no"""`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := Parse(tc.in)
			if !q.Explicit() {
				t.Fatalf("Parse(%q) was not read as an operator query", tc.in)
			}
			rs := q.Rungs()
			if len(rs) != 1 {
				t.Fatalf("rungs = %d, want 1", len(rs))
			}
			if rs[0].Match != tc.want {
				t.Errorf("Parse(%q)\n got %s\nwant %s", tc.in, rs[0].Match, tc.want)
			}
		})
	}
}

// Nothing the user can type may make the search fail. Everything unparseable
// falls back to the plain reading, which still searches.
func TestMalformedFallsBack(t *testing.T) {
	for _, in := range []string{`foo "bar`, `(a`, `a)`, `AND`, `NEAR(`, `NEAR(a, x)`, `"" a`} {
		q := Parse(in)
		if q.Explicit() {
			t.Errorf("Parse(%q) parsed as an operator query, want the plain fallback", in)
		}
		if len(q.Rungs()) == 0 && in != `"" a` {
			t.Errorf("Parse(%q) produced no rungs at all", in)
		}
	}
}

// The marks are the query's own spans, not its words. This is the invariant the
// whole package exists for.
func TestMarksFollowTheQuery(t *testing.T) {
	rs := Parse("Физика в конспектах").Rungs()
	want := []hilite.Phrase{{{Text: "Физика"}, {Text: "в"}, {Text: "конспектах", Prefix: true}}}
	if !reflect.DeepEqual(rs[0].Marks, want) {
		t.Errorf("phrase rung marks = %#v", rs[0].Marks)
	}
	if n := len(rs[2].Marks); n != 3 {
		t.Errorf("words rung marks = %d phrases, want 3", n)
	}

	// A NOT branch is a condition on the hit, never a span in it.
	rs = Parse(`"faux ami" NOT linguistics`).Rungs()
	want = []hilite.Phrase{{{Text: "faux"}, {Text: "ami"}}}
	if !reflect.DeepEqual(rs[0].Marks, want) {
		t.Errorf("NOT branch leaked into the marks: %#v", rs[0].Marks)
	}
}

// A term the tokenizer would discard cannot be lowered into a phrase: FTS5
// rejects an empty string literal outright, so such a query is refused by the
// parser and answered by the plain reading instead.
func TestEmptyPhraseNeverReachesFTS5(t *testing.T) {
	for _, in := range []string{`"!!!"`, `"" a`, `NEAR("!!!" b)`} {
		if Parse(in).Explicit() {
			t.Errorf("Parse(%q) was lowered as an operator query", in)
		}
	}
}
