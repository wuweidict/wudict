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

type wordsResp struct {
	Favourites []string `json:"favourites"`
	History   []string `json:"history"`
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
	if r = put("favourites", "c", true); !slices.Equal(r.Favourites, []string{"c", "a", "b"}) {
		t.Fatalf("favourites after hand edit = %q", r.Favourites)
	}

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
