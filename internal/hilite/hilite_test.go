// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package hilite

import (
	"strings"
	"testing"
)

const (
	o = `<mark class="wu-hl">`
	c = `</mark>`
)

func TestMark(t *testing.T) {
	for _, tc := range []struct {
		name, query, in, want string
	}{
		{"whole token from a prefix term", "run", "he was running fast", "he was " + o + "running" + c + " fast"},
		{"case insensitive", "RUN", "Running", o + "Running" + c},
		{"diacritics, precomposed text", "cafe", "a café here", "a " + o + "café" + c + " here"},
		{"diacritics, precomposed query", "café", "a cafe here", "a " + o + "cafe" + c + " here"},
		{"diacritics, decomposed text", "cafe", "a café here", "a " + o + "café" + c + " here"},
		{"anchored at token start, not mid-word", "run", "overrun", "overrun"},
		{"multiple terms", "red fox", "the red fox ran", "the " + o + "red" + c + " " + o + "fox" + c + " ran"},
		{"term longer than the token", "running", "run", "run"},
		{"digits are token characters", "h2", "h2o and h3", o + "h2o" + c + " and h3"},
		{"no match allocates nothing to show", "zebra", "the red fox", "the red fox"},
		{"empty text", "run", "", ""},

		// The entity guard. Without it the first case emits
		// "&<mark>nbsp</mark>;" and the article renders a literal "&nbsp;".
		{"entity is never a token", "nb", "a&nbsp;b", "a&nbsp;b"},
		{"entity is never part of one", "nbs", "a&nbsp;b", "a&nbsp;b"},
		{"entity ends a token", "caf", "caf&eacute;", o + "caf" + c + "&eacute;"},
		{"numeric entity", "x", "&#120;y", "&#120;y"},
		{"a bare ampersand is prose", "bo", "a & bo", "a & " + o + "bo" + c},
		{"an unterminated ampersand is prose", "amp", "a &amp b", "a &" + o + "amp" + c + " b"},

		// Markup is not this function's input - it sees one text node - but a
		// stray angle bracket in prose must still come back untouched.
		{"angle brackets in prose survive", "bo", "a < bo", "a < " + o + "bo" + c},

		{"punctuation splits tokens", "bo", "ax-bo-cx", "ax-" + o + "bo" + c + "-cx"},
		// A term the tokenizer cuts is the tokens either side, adjacent - what
		// the index matched - and not its first half wherever that occurs.
		{"a term with a separator is its tokens together", "don't", "don't don", o + "don't" + c + " don"},
		{"a hyphenated term", "medium-to-large", "a medium-to-large dog, medium", "a " + o + "medium-to-large" + c + " dog, medium"},
		{"leading punctuation is skipped, not fatal", "-run", "a run", "a " + o + "run" + c},
		{"repeated term marks every token", "ax", "ax ax ax", o + "ax" + c + " " + o + "ax" + c + " " + o + "ax" + c},
		// One rune in a spaced script is a wildcard, not a word (worthMarking).
		{"a one-letter latin term marks nothing", "a", "a apple and", "a apple and"},
		{"a one-letter cyrillic term marks nothing", "в", "в вода время", "в вода время"},
		{"a one-character han term still marks", "\u4e2d", "\u4e2d\u6587", o + "\u4e2d\u6587" + c},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Compile(tc.query).Mark(tc.in)
			if got != tc.want {
				t.Errorf("Mark(%q) with %q\n got %q\nwant %q", tc.in, tc.query, got, tc.want)
			}
		})
	}
}

// ph builds a Phrase from words, a trailing star meaning the token was left
// open. It is the shape internal/ftsq hands over after lowering one query into
// both the FTS5 MATCH expression and these phrases.
func ph(words ...string) Phrase {
	p := make(Phrase, len(words))
	for i, w := range words {
		p[i] = Term{Text: strings.TrimSuffix(w, "*"), Prefix: strings.HasSuffix(w, "*")}
	}
	return p
}

// A phrase is one span, not its words marked separately: `Физика в конспектах`
// asks about three adjacent tokens, so three adjacent tokens are what gets
// painted - and the в, which would drown the article if every word were marked
// on its own, is pinned in place by its neighbours.
func TestMarkPhrase(t *testing.T) {
	for _, tc := range []struct {
		name     string
		phrases  []Phrase
		in, want string
	}{
		{"adjacent tokens are one span", []Phrase{ph("физика", "в", "конспектах")},
			"Физика в конспектах — курс", o + "Физика в конспектах" + c + " — курс"},
		{"the same words apart are not the phrase", []Phrase{ph("физика", "в", "конспектах")},
			"Физика и вода в конспектах", "Физика и вода в конспектах"},
		{"an intervening token breaks it", []Phrase{ph("red", "fox")},
			"red the fox", "red the fox"},
		{"separators between tokens are skipped", []Phrase{ph("физика", "в")},
			"физика, в конспектах", o + "физика, в" + c + " конспектах"},
		{"an entity between tokens is skipped", []Phrase{ph("a1", "b1")},
			"a1&nbsp;b1", o + "a1&nbsp;b1" + c},
		{"the last token may be open", []Phrase{ph("red", "fo*")},
			"the red fox ran", "the " + o + "red fox" + c + " ran"},
		{"a closed last token must match whole", []Phrase{ph("red", "fo")},
			"the red fox ran", "the red fox ran"},
		{"an interior token must match whole", []Phrase{ph("re*", "fox")},
			"red fox", "red fox"},
		{"an interior token that is whole still matches", []Phrase{ph("re*", "fox")},
			"re fox", o + "re fox" + c},
		{"the longest phrase wins over its own first word", []Phrase{ph("red"), ph("red", "fox")},
			"red fox", o + "red fox" + c},
		{"a one-rune token inside a phrase is kept", []Phrase{ph("в", "конспектах")},
			"в конспектах и в вода", o + "в конспектах" + c + " и в вода"},
		// An open last token whose text runs past the pattern by MORE than one
		// rune: the scan keeps counting past the pattern's length, so it must
		// stay out of the pattern on every one of those runes, not just the
		// first. Reading that boundary as `==` indexed the pattern out of range
		// and panicked on the search worker's goroutine, which took the whole
		// process down (`de miedo` -> *miedoso*, `de mala leche` -> *lecheria*).
		{"an open last token may overrun the pattern by many runes", []Phrase{ph("de", "miedo*")},
			"un cuento de miedoso", "un cuento " + o + "de miedoso" + c},
		{"an open last token overrunning a short pattern", []Phrase{ph("de", "mala", "leche*")},
			"de mala lecheria", o + "de mala lecheria" + c},
		{"a closed last token is still rejected on a long overrun", []Phrase{ph("de", "miedo")},
			"un cuento de miedoso", "un cuento de miedoso"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := NewPhrases(tc.phrases).Mark(tc.in); got != tc.want {
				t.Errorf("Mark(%q)\n got %q\nwant %q", tc.in, got, tc.want)
			}
		})
	}
}

// A phrase made only of terms that fold to nothing has nothing to mark; one
// that keeps at least one token is not voided by the others, because the
// tokenizer that built the index dropped them too.
func TestNewPhrasesDropsEmpty(t *testing.T) {
	if got := NewPhrases([]Phrase{ph("!!!", "???")}); got != nil {
		t.Errorf("NewPhrases(punctuation only) = %v, want nil", got)
	}
	if got := NewPhrases([]Phrase{ph("red", "!!!", "fox*")}).Mark("red fox"); got != o+"red fox"+c {
		t.Errorf("a dropped middle term voided the phrase: %q", got)
	}
}

// A nil *Terms is what Compile returns for a query with nothing to mark, and
// every caller is allowed to pass it straight through.
func TestNilTermsIsIdentity(t *testing.T) {
	// "a" and "в" are there because worthMarking rejects them: a query made
	// only of one-rune spaced-script terms compiles to nothing at all, and the
	// article must come back untouched rather than reach a zero-term scan.
	for _, q := range []string{"", "   ", "-", "!!! ???", "a", "в", "a в"} {
		if got := Compile(q); got != nil {
			t.Errorf("Compile(%q) = %v, want nil", q, got)
		}
	}
	if got := (*Terms)(nil).Mark("untouched"); got != "untouched" {
		t.Errorf("nil.Mark = %q", got)
	}
}

// The output must never be shorter than the input in text: marking adds, it
// never replaces. This is the property that says an article cannot be
// corrupted by the scan, independent of what it contains.
func TestMarkPreservesText(t *testing.T) {
	in := "Der Fuchs läuft; café & co. 中文 한국어 &amp; <not a tag> 42"
	for _, q := range []string{"der", "fuchs", "lauft", "cafe", "co", "42", "中", "한"} {
		got := Compile(q).Mark(in)
		stripped := strings.ReplaceAll(strings.ReplaceAll(got, o, ""), c, "")
		if stripped != in {
			t.Errorf("query %q: stripping the marks did not restore the text\n got %q\nwant %q", q, stripped, in)
		}
	}
}

func TestMaxMarks(t *testing.T) {
	in := strings.TrimSpace(strings.Repeat("ax ", MaxMarks+50))
	got := Compile("ax").Mark(in)
	if n := strings.Count(got, o); n != MaxMarks {
		t.Errorf("marks = %d, want %d", n, MaxMarks)
	}
	// Past the cap the article is still whole, which is the point of capping
	// rather than truncating.
	if stripped := strings.ReplaceAll(strings.ReplaceAll(got, o, ""), c, ""); stripped != in {
		t.Error("text past the mark cap was lost")
	}
}

func TestMaxPhrases(t *testing.T) {
	var q []string
	for i := 0; i < maxPhrases+10; i++ {
		q = append(q, string(rune('a'+i%26))+string(rune('a'+i/26)))
	}
	if n := len(Compile(strings.Join(q, " ")).phrases); n != maxPhrases {
		t.Errorf("terms = %d, want %d", n, maxPhrases)
	}
}

// A find field's text is one phrase with its last word open (D198): the words
// adjacent and in order, never each word on its own.
func TestCompilePhrase(t *testing.T) {
	for _, tc := range []struct {
		name, find, in, want string
	}{
		{"one word is a prefix", "viv", "una vivienda", "una " + o + "vivienda" + c},
		{"it does not match inside a word", "enda", "una vivienda", "una vivienda"},
		{"words are one adjacent span", "take of", "to take off now", "to " + o + "take off" + c + " now"},
		{"an earlier word must match whole", "tak off", "to take off", "to take off"},
		{"the words apart are not marked", "take off", "take it off", "take it off"},
		{"case and diacritics fold", "RUN", "to rún", "to " + o + "rún" + c},
		{"trailing punctuation keeps the last word open", "take of !", "take off", o + "take off" + c},
		{"a lone letter marks nothing", "a", "a cat and a dog", "a cat and a dog"},
		{"punctuation alone marks nothing", "?!", "what?!", "what?!"},
		{"an empty field marks nothing", "  ", "nothing", "nothing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := CompilePhrase(tc.find).Mark(tc.in); got != tc.want {
				t.Errorf("CompilePhrase(%q).Mark(%q)\n got %q\nwant %q", tc.find, tc.in, got, tc.want)
			}
		})
	}
}

func TestCompileDeduplicates(t *testing.T) {
	if n := len(Compile("run RUN rún run").phrases); n != 1 {
		t.Errorf("terms = %d, want 1", n)
	}
}

func BenchmarkMark(b *testing.B) {
	text := strings.Repeat("The quick brown fox jumps over the lazy dog. ", 200) // ~9 kB
	t := Compile("quick dog")
	b.SetBytes(int64(len(text)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = t.Mark(text)
	}
}

func BenchmarkMarkNoMatch(b *testing.B) {
	text := strings.Repeat("The quick brown fox jumps over the lazy dog. ", 200)
	t := Compile("zebra")
	b.SetBytes(int64(len(text)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = t.Mark(text)
	}
}

// markDoc marks a document the way the server does: its text nodes in order,
// each with what follows it. Tags are found the simple way, which the
// documents below allow.
func markDoc(t *Terms, doc string) string {
	m := t.Marker()
	var b strings.Builder
	for i := 0; i < len(doc); {
		if doc[i] == '<' {
			j := tagEnd(doc, i)
			if j < 0 {
				j = len(doc)
			}
			b.WriteString(doc[i:j])
			i = j
			continue
		}
		j := strings.IndexByte(doc[i:], '<')
		if j < 0 {
			j = len(doc) - i
		}
		b.WriteString(m.Mark(doc[i:i+j], doc[i+j:]))
		i += j
	}
	return b.String()
}

// The index reads an article with its tags taken out, so a phrase matches
// across them; it is marked there too, a stretch in each text node.
func TestMarkAcrossMarkup(t *testing.T) {
	const o, c = openTag, closeTag
	phrase := func(words ...string) *Terms {
		p := make(Phrase, len(words))
		for i, w := range words {
			p[i] = Term{Text: w}
		}
		return NewPhrases([]Phrase{p})
	}
	for _, tc := range []struct {
		name string
		t    *Terms
		in   string
		want string
	}{
		{"split by one tag", phrase("физика", "в", "конспектах"),
			"<b>Физика</b> в конспектах.",
			"<b>" + o + "Физика" + c + "</b> " + o + "в конспектах" + c + "."},
		{"a word in each element", phrase("one", "two", "three"),
			"<i>one</i> <b>two</b> <u>three</u> four",
			"<i>" + o + "one" + c + "</i> <b>" + o + "two" + c + "</b> <u>" + o + "three" + c + "</u> four"},
		{"across nested markup, a comment and space", phrase("jazz", "clarinet"),
			`<a href="x>y">jazz</a><!-- <b> --><span> <a title='>'>clarinet</a></span>`,
			`<a href="x>y">` + o + "jazz" + c + `</a><!-- <b> --><span> <a title='>'>` + o + "clarinet" + c + "</a></span>"},
		{"a word the markup cuts is the tokens it is cut into", phrase("abbrevi", "ā", "re"),
			"<i>abbrevi<font>ā</font>re</i>",
			"<i>" + o + "abbrevi" + c + "<font>" + o + "ā" + c + "</font>" + o + "re" + c + "</i>"},
		{"the open last word", NewPhrases([]Phrase{{{Text: "old"}, {Text: "hou", Prefix: true}}}),
			"old <b>houses</b>", o + "old" + c + " <b>" + o + "houses" + c + "</b>"},
		{"not completed after the markup: nothing is marked", phrase("one", "two"),
			"<b>one</b> three two", "<b>one</b> three two"},
		{"the text of a script is not text", phrase("one", "two"),
			"one <script>var x = '</b>';</script> two", o + "one" + c + " <script>var x = '</b>';</script> " + o + "two" + c},
		{"nor completed inside one", phrase("one", "two"),
			"one <script>two</script>", "one <script>two</script>"},
		{"a phrase whole in one node is one mark, as before", phrase("one", "two"),
			"<p>one two</p> one", "<p>" + o + "one two" + c + "</p> one"},
		{"the tail does not hide a later match", phrase("one", "two"),
			"<b>one</b> two one two", "<b>" + o + "one" + c + "</b> " + o + "two" + c + " " + o + "one two" + c},
		{"an entity between the words", phrase("one", "two"),
			"<b>one</b>&nbsp;<i>two</i>", "<b>" + o + "one" + c + "</b>&nbsp;<i>" + o + "two" + c + "</i>"},
		{"markup the document ends inside", phrase("one", "two"),
			"one <b title=\"two", "one <b title=\"two"},
	} {
		if got := markDoc(tc.t, tc.in); got != tc.want {
			t.Errorf("%s:\n got %q\nwant %q", tc.name, got, tc.want)
		}
	}
	// The rest of a phrase is looked for close by, not through the document.
	far := "<b>one</b>" + strings.Repeat("<i></i>", maxLook/7+1) + "two"
	if got := markDoc(phrase("one", "two"), far); got != far {
		t.Errorf("a phrase was completed %d bytes of markup away", len(far))
	}
	// The cap is the article's, whichever nodes the marks fall in.
	many := strings.Repeat("<p>ax</p>", MaxMarks+50)
	if n := strings.Count(markDoc(Compile("ax"), many), openTag); n != MaxMarks {
		t.Errorf("%d marks in one article, want the cap of %d", n, MaxMarks)
	}
}

func BenchmarkMarkArticle(b *testing.B) {
	doc := strings.Repeat("<p>The <b>quick</b> brown <i>fox</i> jumps over the lazy dog.</p>\n", 150)
	for _, bc := range []struct {
		name string
		t    *Terms
	}{
		{"words", Compile("quick dog")},
		{"phrase across markup", NewPhrases([]Phrase{{{Text: "the"}, {Text: "quick"}, {Text: "brown"}}})},
		{"no match", Compile("zebra")},
	} {
		b.Run(bc.name, func(b *testing.B) {
			b.SetBytes(int64(len(doc)))
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_ = markDoc(bc.t, doc)
			}
		})
	}
}
