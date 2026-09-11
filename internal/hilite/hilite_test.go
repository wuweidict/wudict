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
		{"a term is cut at its first separator", "don't", "don't", o + "don" + c + "'t"},
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

// A phrase is one span, not its words marked separately. This is the whole
// point of the rewrite: `Физика в конспектах` asked about three adjacent
// tokens, so three adjacent tokens are what gets painted - and the в that
// drowned the article when every word was marked on its own is now pinned in
// place by its neighbours.
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
