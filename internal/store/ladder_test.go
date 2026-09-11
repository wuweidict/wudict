// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"strings"
	"testing"

	"github.com/wuweidict/wudict/internal/ftsq"
)

// run walks the relaxation ladder the way internal/search does and reports the
// rung that answered plus its headwords. It is duplicated here rather than
// imported because the point of this test is the SQL side: that every shape
// internal/ftsq can emit is a legal FTS5 expression against the real schema.
func run(t *testing.T, s *Store, q string) (string, []string) {
	t.Helper()
	for _, r := range ftsq.Parse(q).Rungs() {
		res, err := s.FullTextMatch(r.Match, 10)
		if err != nil {
			t.Fatalf("query %q rung %s (%s): %v", q, r.Name, r.Match, err)
		}
		if len(res) == 0 {
			continue
		}
		var hw []string
		for _, e := range res {
			hw = append(hw, e.Headword)
		}
		return r.Name, hw
	}
	return "", nil
}

// The point of the ladder: the precise reading answers when it can, and only
// then does the query relax. Before this, "órgano muscular" and "muscular
// órgano" and "órgano ... anything ... muscular" were all the same query.
func TestFullTextLadder(t *testing.T) {
	s := testStore(t)
	for _, tc := range []struct {
		name, q, rung, want string
	}{
		{"adjacent words are a phrase", "órgano muscular", "phrase", "corazón"},
		{"a prefix on the last word only", "órgano muscul", "phrase", "corazón"},
		{"reversed words fall to NEAR", "muscular órgano", "near", "corazón"},
		{"a single word does not relax", "muscular", "words", "corazón"},
		{"nothing at all", "zzzz yyyy", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rung, hw := run(t, s, tc.q)
			if rung != tc.rung {
				t.Errorf("query %q answered on rung %q, want %q (hits %v)", tc.q, rung, tc.rung, hw)
			}
			if tc.want != "" && (len(hw) != 1 || hw[0] != tc.want) {
				t.Errorf("query %q hits = %v, want [%s]", tc.q, hw, tc.want)
			}
		})
	}
}

// Every expression the operator grammar can produce must be legal FTS5. A
// syntax error here is not a caught exception - it is a search that fails for a
// user who typed something perfectly reasonable.
func TestOperatorQueriesAreLegalFTS5(t *testing.T) {
	for _, q := range []string{
		`"órgano muscular"`, `"órgano muscular"*`, `"órgano" OR pregunta`,
		`"órgano" pregunta`, `corazón NOT muscular`, `a AND b NOT c`,
		`(corazón OR pregunta) muscular`, `NEAR(órgano muscular, 3)`,
		`NEAR("órgano" "muscular")`, `"say ""no"""`, `w:corazón`,
	} {
		s := testStore(t)
		rs := ftsq.Parse(q).Rungs()
		if len(rs) == 0 {
			t.Errorf("Parse(%q) produced no rungs", q)
			continue
		}
		for _, r := range rs {
			if _, err := s.FullTextMatch(r.Match, 5); err != nil {
				t.Errorf("query %q rung %s (%s): %v", q, r.Name, r.Match, err)
			}
		}
	}
}

// FTS-audit #2, restated for the ladder: nothing a user can type may reach the
// MATCH parser as syntax. Hostile input either parses into a legal expression
// or falls back to the plain reading, and either way it must not error.
func TestLadderSurvivesHostileInput(t *testing.T) {
	s := testStore(t)
	for _, q := range []string{
		`-corazón`, `cora*zon`, `^inicio`, `a:b`, `(paren`, `NEAR`, `OR`, `NOT x`,
		`"quoted"`, `""`, `co"ra`, "back\\slash", `emoji💡word`, `-`, `*`, `   `,
		`""""`, `NEAR(`, `NEAR()`, `NEAR(a,)`, `a NOT`, `((((((((((((((((((((((((((((a`,
		strings.Repeat("word ", 300),
	} {
		for _, r := range ftsq.Parse(q).Rungs() {
			if _, err := s.FullTextMatch(r.Match, 5); err != nil {
				t.Errorf("query %q rung %s (%s): %v", q, r.Name, r.Match, err)
			}
		}
	}
}
