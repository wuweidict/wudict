// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package ftsq

import (
	"strconv"
	"strings"

	"github.com/wuweidict/wudict/internal/hilite"
)

// node is one operator-mode expression. Every node knows how to render itself
// as FTS5 and which of its own spans deserve marking - the second is not a
// projection of the first but a sibling of it, which is why a NOT branch can
// contribute to the match and contribute nothing to the highlighting.
type node interface {
	match() string
	marks() []hilite.Phrase
}

// phraseNode is a quoted phrase or a bare word (a one-term phrase).
type phraseNode struct{ terms []hilite.Term }

func (p phraseNode) match() string {
	w := make([]string, len(p.terms))
	for i, t := range p.terms {
		w[i] = t.Text
	}
	s := quote(w)
	if p.terms[len(p.terms)-1].Prefix {
		s += "*"
	}
	return s
}

func (p phraseNode) marks() []hilite.Phrase { return []hilite.Phrase{hilite.Phrase(p.terms)} }

// nearNode is NEAR(a b c, n): the phrases within n tokens of one another, in
// any order. Each is marked on its own - proximity is a condition on the hit,
// not a span in it.
type nearNode struct {
	ps []phraseNode
	n  int
}

func (o nearNode) match() string {
	parts := make([]string, len(o.ps))
	for i, p := range o.ps {
		parts[i] = p.match()
	}
	return "NEAR(" + strings.Join(parts, " ") + ", " + strconv.Itoa(o.n) + ")"
}

func (o nearNode) marks() []hilite.Phrase {
	var m []hilite.Phrase
	for _, p := range o.ps {
		m = append(m, p.marks()...)
	}
	return m
}

// binNode is AND, OR or NOT. The rendering is fully parenthesised because this
// tree's shape, not FTS5's precedence table, is what was parsed; re-deriving
// the grouping from precedence on the way out is how the two drift apart.
type binNode struct {
	op   string
	l, r node
}

func (b binNode) match() string { return "(" + b.l.match() + " " + b.op + " " + b.r.match() + ")" }

func (b binNode) marks() []hilite.Phrase {
	m := b.l.marks()
	if b.op == "NOT" {
		// The right branch says what the article must NOT contain. Nothing
		// there can be present in a hit, so marking it would be marking
		// something that is not on the page.
		return m
	}
	return append(m, b.r.marks()...)
}

// --- lexer ---

type tokKind int

const (
	tkWord tokKind = iota
	tkPhrase
	tkLParen
	tkRParen
	tkComma
)

type token struct {
	kind   tokKind
	text   string   // tkWord: the word; tkPhrase: unused
	words  []string // tkPhrase: the phrase's words
	prefix bool     // a trailing star was present
}

// lex splits the input. An unterminated quote is not repaired - it fails, and
// Parse falls back to the plain reading, which is the only behaviour that lets
// a user type a lone quote mark without the search breaking.
func lex(s string) ([]token, bool) {
	var out []token
	for i := 0; i < len(s); {
		switch c := s[i]; {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c == '(':
			out, i = append(out, token{kind: tkLParen}), i+1
		case c == ')':
			out, i = append(out, token{kind: tkRParen}), i+1
		case c == ',':
			out, i = append(out, token{kind: tkComma}), i+1
		case c == '"':
			// A doubled quote inside a literal is one quote, the same escape
			// FTS5 itself uses - so a query can be pasted back and forth
			// between the search bar and SQLite without changing meaning.
			var body strings.Builder
			j, closed := i+1, false
			for j < len(s) {
				if s[j] != '"' {
					body.WriteByte(s[j])
					j++
					continue
				}
				if j+1 < len(s) && s[j+1] == '"' {
					body.WriteByte('"')
					j += 2
					continue
				}
				j, closed = j+1, true
				break
			}
			if !closed {
				return nil, false
			}
			i = j
			t := token{kind: tkPhrase, words: strings.Fields(body.String())}
			if i < len(s) && s[i] == '*' {
				t.prefix = true
				i++
			}
			out = append(out, t)
		default:
			j := i
			for j < len(s) && !strings.ContainsRune(" \t\n\r(),\"", rune(s[j])) {
				j++
			}
			w := s[i:j]
			i = j
			t := token{kind: tkWord, text: strings.TrimRight(w, "*")}
			t.prefix = t.text != w
			out = append(out, t)
		}
		if len(out) > maxTokens {
			return nil, false
		}
	}
	return out, len(out) > 0
}

// --- parser ---

// FTS5 binds NOT tightest, then AND, then OR; juxtaposition is AND. This
// mirrors that exactly so a query means here what it would mean typed straight
// into SQLite.
type parser struct {
	tok   []token
	i     int
	depth int
	terms int
}

func parse(s string) (node, bool) {
	tk, ok := lex(s)
	if !ok {
		return nil, false
	}
	p := &parser{tok: tk}
	n, ok := p.or()
	if !ok || p.i != len(p.tok) {
		return nil, false
	}
	return n, true
}

func (p *parser) peek() (token, bool) {
	if p.i < len(p.tok) {
		return p.tok[p.i], true
	}
	return token{}, false
}

// keyword reports whether the next token is the given bare uppercase operator.
func (p *parser) keyword(k string) bool {
	t, ok := p.peek()
	return ok && t.kind == tkWord && !t.prefix && t.text == k
}

func (p *parser) or() (node, bool) {
	l, ok := p.and()
	if !ok {
		return nil, false
	}
	for p.keyword("OR") {
		p.i++
		r, ok := p.and()
		if !ok {
			return nil, false
		}
		l = binNode{op: "OR", l: l, r: r}
	}
	return l, true
}

func (p *parser) and() (node, bool) {
	l, ok := p.not()
	if !ok {
		return nil, false
	}
	for {
		t, ok := p.peek()
		if !ok || t.kind == tkRParen || t.kind == tkComma || p.keyword("OR") {
			return l, true
		}
		if p.keyword("AND") {
			p.i++
		}
		r, ok := p.not()
		if !ok {
			return nil, false
		}
		l = binNode{op: "AND", l: l, r: r}
	}
}

func (p *parser) not() (node, bool) {
	l, ok := p.unary()
	if !ok {
		return nil, false
	}
	for p.keyword("NOT") {
		p.i++
		r, ok := p.unary()
		if !ok {
			return nil, false
		}
		l = binNode{op: "NOT", l: l, r: r}
	}
	return l, true
}

func (p *parser) unary() (node, bool) {
	t, ok := p.peek()
	if !ok {
		return nil, false
	}
	switch {
	case t.kind == tkWord && !t.prefix && t.text == "NEAR":
		return p.near()
	case t.kind == tkLParen:
		if p.depth++; p.depth > maxDepth {
			return nil, false
		}
		p.i++
		n, ok := p.or()
		p.depth--
		if !ok || !p.take(tkRParen) {
			return nil, false
		}
		return n, true
	case t.kind == tkWord && !t.prefix && (t.text == "AND" || t.text == "OR" || t.text == "NOT"):
		// An operator where an operand belongs. Rather than silently searching
		// for the literal word "AND", fail and let the plain reading answer.
		return nil, false
	case t.kind == tkWord, t.kind == tkPhrase:
		return p.phrase()
	}
	return nil, false
}

func (p *parser) near() (node, bool) {
	p.i++ // NEAR
	if !p.take(tkLParen) {
		return nil, false
	}
	var ps []phraseNode
	for {
		t, ok := p.peek()
		if !ok || (t.kind != tkWord && t.kind != tkPhrase) {
			break
		}
		n, ok := p.phrase()
		if !ok {
			return nil, false
		}
		ps = append(ps, n.(phraseNode))
	}
	if len(ps) == 0 {
		return nil, false
	}
	n := nearDefault
	if p.take(tkComma) {
		t, ok := p.peek()
		if !ok || t.kind != tkWord {
			return nil, false
		}
		v, err := strconv.Atoi(t.text)
		if err != nil || v < 0 {
			return nil, false
		}
		n, p.i = v, p.i+1
	}
	if !p.take(tkRParen) {
		return nil, false
	}
	if len(ps) == 1 {
		return ps[0], true // NEAR of one phrase is that phrase
	}
	return nearNode{ps: ps, n: n}, true
}

// phrase builds one operand. A bare word is a PREFIX term - that is what this
// app's search bar has always meant and what the plain reading does - while a
// quoted phrase is EXACT unless the user writes the star themselves. Quoting is
// how a lexicographer says "this word, not words beginning with it".
func (p *parser) phrase() (node, bool) {
	t := p.tok[p.i]
	p.i++
	var terms []hilite.Term
	switch t.kind {
	case tkWord:
		if !hasToken(t.text) {
			return nil, false
		}
		terms = []hilite.Term{{Text: t.text, Prefix: true}}
	case tkPhrase:
		for _, w := range t.words {
			if hasToken(w) {
				terms = append(terms, hilite.Term{Text: w})
			}
		}
		if len(terms) == 0 {
			return nil, false
		}
		terms[len(terms)-1].Prefix = t.prefix
	default:
		return nil, false
	}
	if p.terms += len(terms); p.terms > maxTokens {
		return nil, false
	}
	return phraseNode{terms: terms}, true
}

func (p *parser) take(k tokKind) bool {
	if t, ok := p.peek(); ok && t.kind == k {
		p.i++
		return true
	}
	return false
}
