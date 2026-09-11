// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Package ftsq turns what the user typed into one query, lowered two ways.
//
// # Why this package exists
//
// A full-text search and its highlighting are the same question asked twice.
// When they are computed separately they drift, and the drift is visible: a
// query for `Физика в конспектах` matched on three independent prefixes marked
// every word in the article beginning with в, because the matcher's idea of the
// query and the highlighter's idea of the query were built by different code
// from different inputs. So there is exactly ONE parse here, and it is lowered
// into both an FTS5 MATCH expression and the hilite.Phrase set that paints the
// result. They cannot disagree, because they are the same tree.
//
// # What a bare query means (the relaxation ladder)
//
// `Физика в конспектах` is not a request for articles containing физика and в
// and конспектах somewhere, in any order - that is a bag of words, and it is
// what every general search box does because a web page is not a text. A
// dictionary article IS a text, and the lexicographic tradition reads adjacent
// words as a phrase: the OED treats quoted words as a phrase and says plainly
// that unquoted ones are "not necessarily together or in the order you want";
// DWDS indexes split into sentences so a phrase cannot even cross a sentence
// boundary. Meanwhile a strict phrase alone strands the user who typed a
// remembered fragment slightly wrong and gets nothing.
//
// The ladder resolves it without asking the user to choose a mode:
//
//	rung 1  phrase   "w1 w2 … wn"*      the words, adjacent, in order
//	rung 2  near     NEAR("w1"* … , 10) the words, close together, any order
//	rung 3  words    "w1"* "w2"* …      the words, anywhere in the article
//
// A rung is descended ONLY on zero results, so the precise reading always wins
// when it can answer, and the user is told which rung answered - a silent
// fallback would make the app lie about what it found.
//
// # The operator language
//
// Quotes, AND, OR, NOT, NEAR and parentheses are parsed when the input actually
// contains them. They are deliberately NOT advertised in the search bar (D102):
// a professional who types `"faux ami" NOT linguistics` gets exactly that, and
// everyone else is never shown a syntax they did not ask for. Operator mode
// answers with a single rung - an explicit query is not relaxed, because
// relaxing it would contradict what was asked. A parse error is never an error
// the user sees: the input falls back to the plain reading, so a stray quote in
// a query still searches.
package ftsq

import (
	"strconv"
	"strings"
	"unicode"

	"github.com/wuweidict/wudict/internal/hilite"
)

const (
	// nearDefault matches FTS5's own default for NEAR with the distance
	// omitted, and is the rung-2 window: close enough that the words belong to
	// one thought, wide enough to survive an inserted clause.
	nearDefault = 10

	// maxTokens bounds a hostile query. Every token costs a phrase slot in the
	// highlighter and a term in the FTS5 expression tree; a thousand-word paste
	// into the search box must not become a thousand-way query.
	maxTokens = 128

	// maxDepth bounds parenthesis nesting so a crafted query cannot recurse the
	// parser into the stack.
	maxDepth = 24
)

// Rung is one attempt of the ladder: an FTS5 MATCH expression and the spans to
// mark if it answers. Name is what the UI reports.
type Rung struct {
	Name  string
	Match string
	Marks []hilite.Phrase
}

// Query is one parsed input. The zero value is not useful; use Parse.
type Query struct {
	raw   string
	words []string // the plain reading: bare tokens, operators not present
	node  node     // the operator reading, nil unless operator mode was entered
}

// Raw returns the input as typed.
func (q *Query) Raw() string { return q.raw }

// Explicit reports whether the user wrote operators, in which case there is one
// rung and no relaxation.
func (q *Query) Explicit() bool { return q != nil && q.node != nil }

// Parse reads the input. It never fails: anything it cannot parse is read as a
// bare word sequence, which is what the user most likely meant anyway.
func Parse(input string) *Query {
	q := &Query{raw: input}
	for _, f := range strings.Fields(input) {
		if w := strings.TrimRight(f, "*"); hasToken(w) {
			q.words = append(q.words, w)
			if len(q.words) == maxTokens {
				break
			}
		}
	}
	if looksOperator(input) {
		if n, ok := parse(input); ok {
			q.node = n
		}
	}
	return q
}

// Rungs returns the ladder to run in order. It is nil when there is nothing to
// search for, and has exactly one entry for an explicit query or a single word.
func (q *Query) Rungs() []Rung {
	if q == nil {
		return nil
	}
	if q.node != nil {
		m := q.node.match()
		if m == "" {
			return nil
		}
		return []Rung{{Name: "query", Match: m, Marks: q.node.marks()}}
	}
	switch len(q.words) {
	case 0:
		return nil
	case 1:
		r := Rung{Name: "words"}
		r.Match, r.Marks = bag(q.words)
		return []Rung{r}
	}
	ph := make(hilite.Phrase, len(q.words))
	for i, w := range q.words {
		ph[i] = hilite.Term{Text: w, Prefix: i == len(q.words)-1}
	}
	bagMatch, bagMarks := bag(q.words)
	return []Rung{
		{Name: "phrase", Match: quote(q.words) + "*", Marks: []hilite.Phrase{ph}},
		{Name: "near", Match: "NEAR(" + bagMatch + ", " + strconv.Itoa(nearDefault) + ")", Marks: bagMarks},
		{Name: "words", Match: bagMatch, Marks: bagMarks},
	}
}

// bag lowers words into independent prefix terms - FTS5 reads juxtaposition as
// AND - and the matching one-token phrases.
func bag(words []string) (string, []hilite.Phrase) {
	parts := make([]string, len(words))
	marks := make([]hilite.Phrase, len(words))
	for i, w := range words {
		parts[i] = quote([]string{w}) + "*"
		marks[i] = hilite.Phrase{{Text: w, Prefix: true}}
	}
	return strings.Join(parts, " "), marks
}

// quote renders a phrase as an FTS5 string literal. FTS5 escapes an embedded
// double quote by doubling it; everything else inside the literal is handed to
// the tokenizer, so no other character needs escaping.
func quote(words []string) string {
	var b strings.Builder
	b.WriteByte('"')
	for i, w := range words {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(strings.ReplaceAll(w, `"`, `""`))
	}
	b.WriteByte('"')
	return b.String()
}

// hasToken reports whether the tokenizer would get anything out of s. A term
// that yields no token cannot be part of an FTS5 phrase - `MATCH '""'` is a
// syntax error, not an empty match - so such terms are dropped before lowering
// rather than allowed to fail the whole query.
func hasToken(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			return true
		}
	}
	return false
}

// looksOperator decides whether to attempt the operator grammar at all. It is
// deliberately conservative: a query is read plainly unless it carries a quote
// or a BARE UPPERCASE keyword. Lower-case "and" is a word in every language
// this app serves, and parentheses alone are ordinary lexicographic notation -
// "(coll.)" must not turn a query into a syntax puzzle.
func looksOperator(s string) bool {
	if strings.ContainsRune(s, '"') {
		return true
	}
	for _, f := range strings.Fields(s) {
		switch strings.TrimLeft(f, "(") {
		case "AND", "OR", "NOT":
			return true
		}
		if strings.HasPrefix(f, "NEAR(") {
			return true
		}
	}
	return false
}
