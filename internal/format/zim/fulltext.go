// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package zim

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/wuweidict/wudict/internal/dict"
	"github.com/wuweidict/wudict/internal/format/zim/glass"
	"github.com/wuweidict/wudict/internal/ftsq"
	"github.com/wuweidict/wudict/internal/hilite"
	"github.com/wuweidict/wudict/internal/logx"
	"github.com/wuweidict/wudict/internal/store"
)

// A Kiwix archive carries a full-text index of its own - a Xapian database in
// the X/fulltext/xapian entry, stored uncompressed so it can be read where it
// lies - and full-text search is answered from it (D194): no index of ours
// is built, and nothing is copied out of the archive.
//
// That index knows which articles hold a word and how often, not where: it
// has no positions (internal/format/zim/glass). So a reading that is about
// positions - a phrase, a NEAR - is answered in two steps: the index names
// the articles holding all the words, best first, and the text of each is
// read to see whether the words stand as asked.

const (
	// A positional reading reads its candidates' text, best-ranked first,
	// until it has its results or has read maxVerifyBytes of articles: the
	// cost is the text - a cluster decompressed, stripped and split - so the
	// bound is in bytes, and reaches thousands of dictionary entries or a
	// hundred encyclopedia articles. maxCandidates is how many the index is
	// asked to rank. An answer cut short by either says so (ftsq.Answer).
	maxVerifyBytes = 8 << 20
	maxCandidates  = 4096

	// maxExpand bounds the index terms an open word stands for, in the
	// index's own order. An answer from a word with more says so too.
	maxExpand = 256

	// maxForms bounds the matched word forms marked beyond the query's own
	// spelling. hilite marks 64 phrases at most, and the query's come first.
	maxForms = 32
)

type textIndex struct {
	db *glass.DB
	an *glass.Analyzer
}

type textIndexOnce struct {
	once sync.Once
	ix   *textIndex
}

// textIndex opens the archive's own full-text index, nil when it carries none
// this reader can search: no index, one stored in a compressed cluster, or
// one stemmed for a language there is no stemmer for.
func (d *Dict) textIndex() *textIndex {
	d.text.once.Do(func() {
		ix, err := d.openTextIndex()
		if err != nil {
			if !errors.Is(err, dict.ErrNotFound) {
				logx.V("%s: full-text index not used: %v", d.meta.Name, err)
			}
			return
		}
		d.text.ix = ix
	})
	return d.text.ix
}

func (d *Dict) openTextIndex() (*textIndex, error) {
	// libzim looks in the same two places (src/fileimpl.cpp): the second is
	// where archives older than 2017 keep it.
	i, de, ok := d.c.find('X', "fulltext/xapian")
	if !ok {
		if i, de, ok = d.c.find('Z', "/fulltextIndex/xapian"); !ok {
			return nil, dict.ErrNotFound
		}
	}
	if de.isRedirect() {
		var err error
		if _, de, err = d.c.resolve(i); err != nil {
			return nil, err
		}
	}
	if !de.isContent() {
		return nil, dict.ErrNotFound
	}
	cd, lo, hi, err := d.c.blobSpan(de.cluster, de.blob)
	if err != nil {
		return nil, err
	}
	if !cd.direct || hi < lo {
		return nil, errors.New("the index is stored compressed")
	}
	size := int64(hi - lo)
	db, err := glass.Open(sectionAt{d.c.f, cd.body + int64(lo), size}, size)
	if err != nil {
		return nil, err
	}
	an, err := db.Analyzer()
	if err != nil {
		return nil, err
	}
	return &textIndex{db: db, an: an}, nil
}

// sectionAt is a window onto the archive: io.SectionReader without its
// 64-bit seek state, which a ReaderAt shared between searches has no use for.
type sectionAt struct {
	r    source
	off  int64
	size int64
}

func (s sectionAt) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 || off+int64(len(p)) > s.size {
		return 0, errors.New("read outside the index")
	}
	return s.r.ReadAt(p, s.off+off)
}

// FullTextPlan answers a full-text ladder from the archive's own index
// (ftsq.Planner).
func (d *Dict) FullTextPlan(ctx context.Context, plan []ftsq.Rung, limit int) (ftsq.Answer, error) {
	ix := d.textIndex()
	if ix == nil {
		return ftsq.Answer{}, dict.ErrUnsupported
	}
	if limit <= 0 {
		limit = 100
	}
	run := &textRun{d: d, ix: ix, ctx: ctx, texts: map[uint32]*docText{}}
	partial := false
	for _, r := range plan {
		if r.Expr == nil {
			continue
		}
		res, cut, err := run.rung(r.Expr, limit)
		if err != nil {
			return ftsq.Answer{}, err
		}
		if len(res) > 0 {
			return ftsq.Answer{Results: res, Rung: r.Name, Marks: run.marks(r, res), Partial: partial, More: cut}, nil
		}
		partial = partial || cut
	}
	return ftsq.Answer{Partial: partial}, nil
}

// textRun is one ladder against one archive. The rungs of a ladder ask about
// the same articles, so an article's text is read once for all of them.
type textRun struct {
	d     *Dict
	ix    *textIndex
	ctx   context.Context
	texts map[uint32]*docText
	read  int64 // bytes of article read for their words
	wide  bool  // an open word stood for more index terms than maxExpand
}

// docText is an article of the index: where it is, and - once a positional
// reading has asked - its words, folded, in order.
type docText struct {
	entry int
	de    dirent
	words []string
}

// rung answers one reading. cut reports that candidates were left unread.
func (r *textRun) rung(e *ftsq.Expr, limit int) (res []dict.Result, cut bool, err error) {
	r.wide = false
	q, exact, err := r.candidates(e)
	if err != nil || q == nil {
		return nil, false, err
	}
	want := limit
	if !exact {
		want = maxCandidates
	}
	hits, matched, err := r.ix.db.Search(r.ctx, q, want)
	if err != nil {
		return nil, false, err
	}
	cut = r.wide || (!exact && matched > len(hits))
	var keep []int
	for _, h := range hits {
		if err := r.ctx.Err(); err != nil {
			return nil, false, err
		}
		if t := r.texts[h.DocID]; !exact && (t == nil || t.words == nil) && r.read >= maxVerifyBytes {
			cut = true
			break
		}
		t := r.text(h.DocID, !exact)
		if t == nil || (!exact && !r.holds(e, t)) {
			continue
		}
		keep = append(keep, t.entry)
		if len(keep) == limit {
			break
		}
	}
	if res, err = r.d.results(keep, limit); err != nil {
		return nil, false, err
	}
	return res, cut && len(keep) < limit, nil
}

// candidates lowers a reading to what the index can be asked: the articles
// that hold its words. exact reports that this is the reading itself - it has
// nothing to say about where the words stand.
func (r *textRun) candidates(e *ftsq.Expr) (q *glass.Query, exact bool, err error) {
	switch e.Op {
	case ftsq.OpPhrase:
		var args []*glass.Query
		tokens := 0
		for i, t := range e.Terms {
			tokens += len(splitWords(t.Text))
			var terms []string
			r.ix.an.Terms(t.Text, func(term string) { terms = append(terms, term) })
			for k, term := range terms {
				tq := glass.Term(term)
				if t.Prefix && i == len(e.Terms)-1 && k == len(terms)-1 {
					if tq, err = r.open(t.Text, term); err != nil {
						return nil, false, err
					}
				}
				args = append(args, tq)
			}
		}
		if len(args) == 0 {
			return nil, false, nil // stopwords and punctuation: the index holds none of it
		}
		return glass.And(args...), tokens <= 1, nil
	case ftsq.OpNot:
		keep, exact, err := r.candidates(e.Args[0])
		if err != nil || keep == nil {
			return nil, false, err
		}
		// Only what the index can say for certain may be taken away: the
		// candidates of a phrase are more articles than hold the phrase.
		drop, dropExact, err := r.candidates(e.Args[1])
		if err != nil {
			return nil, false, err
		}
		if drop == nil {
			return keep, exact, nil
		}
		if !dropExact {
			return keep, false, nil
		}
		return glass.AndNot(keep, drop), exact, nil
	}
	exact = e.Op != ftsq.OpNear
	var args []*glass.Query
	for _, a := range e.Args {
		aq, aExact, err := r.candidates(a)
		if err != nil {
			return nil, false, err
		}
		if aq == nil {
			if e.Op == ftsq.OpOr {
				continue
			}
			// A word the index never holds cannot be required of an article;
			// what is left of the reading is checked against the text.
			exact = false
			continue
		}
		exact = exact && aExact
		args = append(args, aq)
	}
	if len(args) == 0 {
		return nil, false, nil
	}
	if e.Op == ftsq.OpOr {
		return glass.Or(args...), exact, nil
	}
	return glass.And(args...), exact, nil
}

// open is the query for a word left open at its end: the word's own term, and
// every index term that begins with what was typed.
func (r *textRun) open(typed, term string) (*glass.Query, error) {
	prefix := ""
	if ws := splitWords(typed); len(ws) > 0 {
		prefix = ws[len(ws)-1]
	}
	terms, more, err := r.ix.db.Expand(prefix, maxExpand)
	if err != nil {
		return nil, err
	}
	r.wide = r.wide || more
	args := []*glass.Query{glass.Term(term)}
	for _, t := range terms {
		if t != term {
			args = append(args, glass.Term(t))
		}
	}
	return glass.Or(args...), nil
}

// text resolves a document of the index to its article, nil when the archive
// no longer has it as one, and reads its words when words is set.
func (r *textRun) text(docid uint32, words bool) *docText {
	t, ok := r.texts[docid]
	if !ok {
		t = r.d.indexed(r.ix, docid)
		r.texts[docid] = t
	}
	if t != nil && words && t.words == nil {
		raw, err := r.d.c.content(t.de)
		if err != nil {
			r.texts[docid] = nil
			return nil
		}
		r.read += int64(len(raw))
		t.words = textWords(store.StripHTML(string(raw)))
		if t.words == nil {
			t.words = []string{}
		}
	}
	return t
}

// indexed finds the article a document of the index stands for.
func (d *Dict) indexed(ix *textIndex, docid uint32) *docText {
	// libzim stores the path with its namespace, "C/path" (xapianWorker.cpp).
	data, err := ix.db.DocData(docid)
	if err != nil || len(data) < 3 || data[1] != '/' {
		return nil
	}
	i, de, ok := d.c.find(data[0], data[2:])
	if !ok || !d.c.isArticle(i, de) {
		return nil
	}
	if de.isRedirect() {
		if _, de, err = d.c.resolve(i); err != nil {
			return nil
		}
	}
	if !de.isContent() {
		return nil
	}
	return &docText{entry: i, de: de}
}

func wordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.Is(unicode.M, r)
}

// textWords cuts text into the words a positional reading counts in: runs of
// letters and digits, folded as the index folds (glass.Fold). A word is folded
// on its own, and an ASCII one without the Unicode machinery.
func textWords(s string) []string {
	words := strings.FieldsFunc(s, func(r rune) bool { return !wordRune(r) })
	n := 0
	for _, w := range words {
		if w = foldWord(w); w != "" {
			words[n] = w
			n++
		}
	}
	return words[:n]
}

func foldWord(w string) string {
	for i := 0; i < len(w); i++ {
		if c := w[i]; c >= 0x80 {
			return glass.Fold(w)
		} else if c >= 'A' && c <= 'Z' {
			return strings.ToLower(w)
		}
	}
	return w
}

// splitWords is textWords for a query's term.
func splitWords(s string) []string { return textWords(s) }

// holds reports whether the article's text satisfies a reading.
func (r *textRun) holds(e *ftsq.Expr, t *docText) bool {
	switch e.Op {
	case ftsq.OpPhrase:
		return len(r.occurrences(e, t, 1)) > 0
	case ftsq.OpNear:
		return r.near(e, t)
	case ftsq.OpAnd:
		for _, a := range e.Args {
			if !r.holds(a, t) {
				return false
			}
		}
		return true
	case ftsq.OpOr:
		for _, a := range e.Args {
			if r.holds(a, t) {
				return true
			}
		}
		return false
	case ftsq.OpNot:
		return r.holds(e.Args[0], t) && !r.holds(e.Args[1], t)
	}
	return false
}

// span is one place a phrase stands in an article: its first word and how
// many words it takes.
type span struct{ at, n, phrase int }

// occurrences lists where a phrase stands in an article, up to limit places
// (0 is all of them). One word matches as the index matches it - by its stem,
// or as the beginning of a word when it was left open; several match as they
// are spelled, adjacent and in order, with only the last one open.
func (r *textRun) occurrences(e *ftsq.Expr, t *docText, limit int) (out []span) {
	var want []string
	for _, term := range e.Terms {
		want = append(want, splitWords(term.Text)...)
	}
	if len(want) == 0 {
		return nil
	}
	open := e.Terms[len(e.Terms)-1].Prefix
	stem := ""
	if len(want) == 1 {
		stem = r.ix.an.Stem(want[0])
	}
	last := want[len(want)-1]
	for i := 0; i+len(want) <= len(t.words); i++ {
		ok := true
		for k, w := range want[:len(want)-1] {
			if t.words[i+k] != w {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		got := t.words[i+len(want)-1]
		if got == last || (open && strings.HasPrefix(got, last)) || (stem != "" && sameStem(r.ix.an, got, last, stem)) {
			out = append(out, span{at: i, n: len(want)})
			if len(out) == limit {
				return out
			}
		}
	}
	return out
}

// sameStem reports whether a word of an article is a form of the query's.
// Forms of one word begin alike in every language stemmed here but Arabic,
// so a word that does not share its first letter is not stemmed to find out.
func sameStem(an *glass.Analyzer, word, typed, stem string) bool {
	first, _ := utf8.DecodeRuneInString(word)
	a, _ := utf8.DecodeRuneInString(typed)
	b, _ := utf8.DecodeRuneInString(stem)
	if first != a && first != b {
		return false
	}
	return an.Stem(word) == stem
}

// near is FTS5's NEAR: every phrase stands in the article, in some stretch
// where no more than e.Near words lie between the end of the first of them
// and the beginning of the last.
func (r *textRun) near(e *ftsq.Expr, t *docText) bool {
	var all []span
	for k, a := range e.Args {
		occ := r.occurrences(a, t, 0)
		if len(occ) == 0 {
			return false
		}
		for i := range occ {
			occ[i].phrase = k
		}
		all = append(all, occ...)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].at < all[j].at })
	// The smallest stretch that ends at each occurrence and holds every
	// phrase starts at the latest "last seen" of the others.
	seen := make([]int, len(e.Args)) // index into all, +1
	have := 0
	for i, s := range all {
		if seen[s.phrase] == 0 {
			have++
		}
		seen[s.phrase] = i + 1
		if have < len(e.Args) {
			continue
		}
		first := s
		for _, k := range seen {
			if all[k-1].at < first.at {
				first = all[k-1]
			}
		}
		if s.at-(first.at+first.n) <= e.Near {
			return true
		}
	}
	return false
}

// marks are the spans to mark in an answer: what the query spells, and the
// forms the index matched by stem that the query's own spelling does not
// cover - "running" in an article found for "run" is not a word that begins
// with one typed, yet it is why the article is there.
func (r *textRun) marks(rung ftsq.Rung, res []dict.Result) []hilite.Phrase {
	marks := rung.Marks
	stems := map[string]string{} // stem -> the folded word typed
	var collect func(e *ftsq.Expr)
	collect = func(e *ftsq.Expr) {
		switch e.Op {
		case ftsq.OpPhrase:
			var want []string
			for _, term := range e.Terms {
				want = append(want, splitWords(term.Text)...)
			}
			if len(want) == 1 {
				if s := r.ix.an.Stem(want[0]); s != "" {
					stems[s] = want[0]
				}
			}
		case ftsq.OpNot:
			collect(e.Args[0])
		default:
			for _, a := range e.Args {
				collect(a)
			}
		}
	}
	collect(rung.Expr)
	if len(stems) == 0 {
		return marks
	}
	forms := map[string]bool{}
	tried := map[string]bool{}
	for _, res := range res {
		for _, w := range textWords(store.StripHTML(res.Body)) {
			if tried[w] {
				continue
			}
			tried[w] = true
			for stem, typed := range stems {
				if !strings.HasPrefix(w, typed) && sameStem(r.ix.an, w, typed, stem) {
					forms[w] = true
				}
			}
		}
	}
	if len(forms) == 0 {
		return marks
	}
	list := make([]string, 0, len(forms))
	for w := range forms {
		list = append(list, w)
	}
	sort.Strings(list)
	marks = append([]hilite.Phrase(nil), marks...)
	for _, w := range list[:min(len(list), maxForms)] {
		marks = append(marks, hilite.Phrase{{Text: w}})
	}
	return marks
}
