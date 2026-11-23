// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestCleanWord(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"casa", "casa"},
		{"  casa \n", "casa"},
		{"a\nb\r\tc", "a b  c"},
		{"", ""},
		{" \t\r\n", ""},
		{"\xff", ""},
		{strings.Repeat("я", maxWordRunes), strings.Repeat("я", maxWordRunes)},
		{strings.Repeat("я", maxWordRunes+1), ""},
	} {
		if got := cleanWord(c.in); got != c.want {
			t.Errorf("cleanWord(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestParseWords(t *testing.T) {
	for _, c := range []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"a\nb\n", []string{"a", "b"}},
		{"a\r\nb", []string{"a", "b"}},
		{"a\n\n  \nb\na\n", []string{"a", "b"}},
		{"Casa\ncasa\nCASA\n", []string{"Casa"}},
		{"\xff\nok\n", []string{"ok"}},
	} {
		got, probs := parseWords(c.in)
		if !slices.Equal(got, c.want) || probs != nil {
			t.Errorf("parseWords(%q) = %q %v, want %q", c.in, got, probs, c.want)
		}
	}
}

func TestPutWord(t *testing.T) {
	for _, c := range []struct {
		list    []string
		w       string
		on      bool
		limit   int
		want    []string
		changed bool
	}{
		{nil, "a", true, 3, []string{"a"}, true},
		{[]string{"a", "b"}, "a", true, 3, []string{"a", "b"}, false},
		{[]string{"a", "b"}, "b", true, 3, []string{"b", "a"}, true},
		{[]string{"a", "b", "c"}, "d", true, 3, []string{"d", "a", "b"}, true},
		{[]string{"a", "b"}, "a", false, 3, []string{"b"}, true},
		{[]string{"a", "b"}, "x", false, 3, []string{"a", "b"}, false},
		{[]string{"casa", "b"}, "CASA", true, 3, []string{"CASA", "b"}, true},
		{[]string{"b", "Casa"}, "casa", false, 3, []string{"b"}, true},
	} {
		got, changed := putWord(c.list, c.w, c.on, c.limit)
		if !slices.Equal(got, c.want) || changed != c.changed {
			t.Errorf("putWord(%q, %q, %v) = %q %v, want %q %v", c.list, c.w, c.on, got, changed, c.want, c.changed)
		}
	}
}

func TestParseFavourites(t *testing.T) {
	type F = favourite
	for _, c := range []struct {
		in   string
		want []F
	}{
		{"", nil},
		{"a\nb\n", []F{{"a", ""}, {"b", ""}}},
		{"a\tx1\r\nb\n", []F{{"a", "x1"}, {"b", ""}}},
		// the same word in two dictionaries is two favourites; the word alone a third
		{"a\tx1\nA\tx2\na\n", []F{{"a", "x1"}, {"A", "x2"}, {"a", ""}}},
		{"Casa\tx1\ncasa\tx1\n", []F{{"Casa", "x1"}}},
		// an id that is not one keeps the word; no word, no line
		{"a\tbad id\nb\t" + strings.Repeat("x", maxDictBytes+1) + "\n\tx1\n", []F{{"a", ""}, {"b", ""}}},
		{"a\t x1 \n", []F{{"a", "x1"}}},
		{"\xff\tx1\nok\n", []F{{"ok", ""}}},
	} {
		got, probs := parseFavourites(c.in)
		if !slices.Equal(got, c.want) || probs != nil {
			t.Errorf("parseFavourites(%q) = %q %v, want %q", c.in, got, probs, c.want)
		}
	}
}

func TestPutFavourite(t *testing.T) {
	type F = favourite
	for _, c := range []struct {
		list    []F
		f       F
		on      bool
		want    []F
		changed bool
	}{
		{nil, F{"a", "x"}, true, []F{{"a", "x"}}, true},
		{[]F{{"a", "x"}}, F{"a", "x"}, true, []F{{"a", "x"}}, false},
		{[]F{{"a", "x"}}, F{"a", "y"}, true, []F{{"a", "y"}, {"a", "x"}}, true},
		// starring in a dictionary replaces the word alone…
		{[]F{{"b", ""}, {"a", ""}}, F{"A", "x"}, true, []F{{"A", "x"}, {"b", ""}}, true},
		// …but the word alone leaves the precise ones be
		{[]F{{"a", "x"}}, F{"a", ""}, true, []F{{"a", ""}, {"a", "x"}}, true},
		// unstarring in one dictionary takes that one only
		{[]F{{"a", "x"}, {"a", "y"}, {"a", ""}}, F{"a", "x"}, false, []F{{"a", "y"}, {"a", ""}}, true},
		{[]F{{"a", "x"}}, F{"a", "y"}, false, []F{{"a", "x"}}, false},
		// taking out the word alone takes the word out of the list
		{[]F{{"a", "x"}, {"b", "x"}, {"A", ""}, {"a", "y"}}, F{"a", ""}, false, []F{{"b", "x"}}, true},
		{[]F{{"a", "x"}, {"b", ""}, {"c", ""}}, F{"d", "x"}, true, []F{{"d", "x"}, {"a", "x"}, {"b", ""}}, true},
	} {
		got, changed := putFavourite(c.list, c.f, c.on, 3)
		if !slices.Equal(got, c.want) || changed != c.changed {
			t.Errorf("putFavourite(%q, %q, %v) = %q %v, want %q %v", c.list, c.f, c.on, got, changed, c.want, c.changed)
		}
	}
}

func TestCleanDict(t *testing.T) {
	for _, c := range []struct {
		in   string
		ok   bool
	}{
		{"", true}, {"0123456789ab", true}, {"wudict-howto", true},
		{"a b", false}, {"a\tb", false}, {"a\x7f", false}, {"\xff", false},
		{strings.Repeat("x", maxDictBytes), true}, {strings.Repeat("x", maxDictBytes+1), false},
	} {
		if got, ok := cleanDict(c.in); ok != c.ok || (ok && got != c.in) {
			t.Errorf("cleanDict(%q) = %q %v, want ok %v", c.in, got, ok, c.ok)
		}
	}
}

type wordsResp struct {
	Favourites []favourite `json:"favourites"`
	History    []string    `json:"history"`
}

func callWords(t *testing.T, s *Server, method, target, body string) (wordsResp, int) {
	t.Helper()
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, newRequest(method, target, strings.NewReader(body)))
	var r wordsResp
	if rec.Code == 200 {
		if err := json.Unmarshal(rec.Body.Bytes(), &r); err != nil {
			t.Fatalf("%s %s: bad JSON (%v): %s", method, target, err, rec.Body.String())
		}
		if r.Favourites == nil || r.History == nil {
			t.Fatalf("%s %s: a list is null: %s", method, target, rec.Body.String())
		}
	}
	return r, rec.Code
}

func TestWordsAPI(t *testing.T) {
	s := newTestServer(t)
	s.User = FolderUserDir(t.TempDir())
	put := func(list, word string, on bool) wordsResp {
		t.Helper()
		b, _ := json.Marshal(map[string]any{"list": list, "word": word, "on": on})
		r, code := callWords(t, s, "POST", "/api/words", string(b))
		if code != 200 {
			t.Fatalf("POST %s %q: %d", list, word, code)
		}
		return r
	}

	if r, _ := callWords(t, s, "GET", "/api/words", ""); len(r.Favourites)+len(r.History) != 0 {
		t.Fatalf("fresh lists not empty: %+v", r)
	}
	put("history", "casa", true)
	put("history", "lucid", true)
	r := put("history", " casa\n", true)
	if !slices.Equal(r.History, []string{"casa", "lucid"}) {
		t.Fatalf("history = %q", r.History)
	}
	if b, _ := os.ReadFile(s.User.History()); string(b) != "casa\nlucid\n" {
		t.Fatalf("history.txt = %q", b)
	}
	r = put("favourites", "ennui", true)
	r = put("favourites", "ennui", false)
	if len(r.Favourites) != 0 {
		t.Fatalf("favourites after removal = %q", r.Favourites)
	}

	// a hand edit is adopted by the next change
	if err := os.WriteFile(s.User.Favourites(), []byte("a\nb\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if r = put("favourites", "c", true); !slices.Equal(r.Favourites, []favourite{{"c", ""}, {"a", ""}, {"b", ""}}) {
		t.Fatalf("favourites after hand edit = %q", r.Favourites)
	}

	// a favourite in a dictionary: on the wire with its id, in the file after a tab
	fav := func(word, dict string, on bool) wordsResp {
		t.Helper()
		b, _ := json.Marshal(map[string]any{"list": "favourites", "word": word, "dict": dict, "on": on})
		r, code := callWords(t, s, "POST", "/api/words", string(b))
		if code != 200 {
			t.Fatalf("POST favourites %q %q: %d", word, dict, code)
		}
		return r
	}
	if r = fav("A", "0123456789ab", true); !slices.Equal(r.Favourites, []favourite{{"A", "0123456789ab"}, {"c", ""}, {"b", ""}}) {
		t.Fatalf("favourites after starring in a dictionary = %q", r.Favourites)
	}
	if b, _ := os.ReadFile(s.User.Favourites()); string(b) != "A\t0123456789ab\nc\nb\n" {
		t.Fatalf("favourites.txt = %q", b)
	}
	rec0 := httptest.NewRecorder()
	s.ServeHTTP(rec0, newRequest("GET", "/api/words", nil))
	if !strings.Contains(rec0.Body.String(), `{"word":"c"}`) || !strings.Contains(rec0.Body.String(), `{"word":"A","dict":"0123456789ab"}`) {
		t.Fatalf("GET /api/words = %s", rec0.Body)
	}
	if r = fav("a", "0123456789ab", false); !slices.Equal(r.Favourites, []favourite{{"c", ""}, {"b", ""}}) {
		t.Fatalf("favourites after unstarring = %q", r.Favourites)
	}
	fav("a", "0123456789ab", true)

	// "Keep history" off: adding records nothing, removing still works
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, newRequest("PUT", "/api/prefs", strings.NewReader(`{"ui":{"historyOff":true}}`)))
	if rec.Code != 200 {
		t.Fatalf("PUT /api/prefs: %d %s", rec.Code, rec.Body)
	}
	if r = put("history", "secret", true); slices.Contains(r.History, "secret") {
		t.Fatalf("recorded while off: %q", r.History)
	}
	if r = put("history", "lucid", false); !slices.Equal(r.History, []string{"casa"}) {
		t.Fatalf("history after removal = %q", r.History)
	}

	if r, code := callWords(t, s, "DELETE", "/api/words?list=history", ""); code != 200 || len(r.History) != 0 || len(r.Favourites) != 3 {
		t.Fatalf("DELETE history: %d %+v", code, r)
	}
	if _, err := os.Stat(s.User.History()); !os.IsNotExist(err) {
		t.Fatalf("history.txt still there: %v", err)
	}

	for _, c := range []struct{ method, target, body string }{
		{"POST", "/api/words", `{"list":"other","word":"a","on":true}`},
		{"POST", "/api/words", `{"list":"history","word":"  ","on":true}`},
		{"POST", "/api/words", `{"list":"history","word":"` + strings.Repeat("x", maxWordRunes+1) + `","on":true}`},
		{"POST", "/api/words", `not json`},
		{"POST", "/api/words", `{"list":"history","word":"a","dict":"0123456789ab","on":true}`},
		{"POST", "/api/words", `{"list":"favourites","word":"a","dict":"two words","on":true}`},
		{"DELETE", "/api/words?list=other", ""},
	} {
		if _, code := callWords(t, s, c.method, c.target, c.body); code != 400 {
			t.Errorf("%s %s %.40s: %d, want 400", c.method, c.target, c.body, code)
		}
	}
}

func TestWordsHistoryCap(t *testing.T) {
	s := newTestServer(t)
	s.User = FolderUserDir(t.TempDir())
	var list []string
	for i := range maxHistory + 5 {
		list = append(list, "w"+strconv.Itoa(i))
	}
	if err := os.WriteFile(s.User.History(), []byte(joinWords(list)), 0o600); err != nil {
		t.Fatal(err)
	}
	r, code := callWords(t, s, "POST", "/api/words", `{"list":"history","word":"new","on":true}`)
	if code != 200 || len(r.History) != maxHistory || r.History[0] != "new" || r.History[1] != "w0" {
		t.Fatalf("capped history: %d, %d words, first %q", code, len(r.History), r.History[:2])
	}
}
