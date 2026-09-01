// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"bufio"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wuweidict/wudict/internal/morph"
)

// morphServer builds a registry over one temp folder holding the named DSL
// files, with lemmatization enabled. The file NAMES are the point: language
// detection reads them (prefix only), and the folder is a configured root so
// nothing above it is ever consulted.
func morphServer(t *testing.T, files map[string]string) *Server {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	isolatedDBDir(t)
	reg, err := NewRegistry([]string{dir}, false)
	if err != nil {
		t.Fatal(err)
	}
	s := New(reg)
	// Only English is built in (D87), so Russian arrives the way a user's
	// Russian arrives: as a file in LEMMA_DIR. Keyed on the ё spellings, as
	// golem's own ru data is, so the е→ё retry is still what resolves "идет".
	lemmas := t.TempDir()
	if err := os.WriteFile(filepath.Join(lemmas, "ru.txt"), []byte("идти\tидёт\tидёшь\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.Morph = morph.New(2, lemmas)
	return s
}

// stream returns EVERY message of an /api/search response, not just the hits:
// what is being asserted here is that a message type appears, or does not.
func stream(t *testing.T, s *Server, path string) []streamMsg {
	t.Helper()
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
	if rec.Code != 200 {
		t.Fatalf("GET %s: status %d: %s", path, rec.Code, rec.Body.String())
	}
	var out []streamMsg
	sc := bufio.NewScanner(strings.NewReader(rec.Body.String()))
	sc.Buffer(make([]byte, 0, 64*1024), 4<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var m streamMsg
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("GET %s: bad NDJSON line (%v): %s", path, err, line)
		}
		out = append(out, m)
	}
	return out
}

func morphMsgs(msgs []streamMsg) []streamMsg {
	var out []streamMsg
	for _, m := range msgs {
		if m.T == "morph" {
			out = append(out, m)
		}
	}
	return out
}

// dslFile is the smallest well-formed DSL holding one headword.
func dslFile(name string, words ...string) string {
	var b strings.Builder
	b.WriteString("#NAME \"" + name + "\"\n\n")
	for _, w := range words {
		b.WriteString(w + "\n\t" + w + " defined\n\n")
	}
	return b.String()
}

// The governing rule: a DICTIONARY that found something is never
// second-guessed. The only dictionary here holds "casa", so "casa" must
// produce no morphology message at all - not one that is ignored, none
// written.
func TestNoLemmaWhenTheSearchHit(t *testing.T) {
	s := morphServer(t, map[string]string{"es-es-x.dsl": dslFile("Spanish", "casa", "ser")})
	getDicts(t, s, "/api/dicts")

	msgs := stream(t, s, "/api/search?q=casa&mode=exact&dict=all")
	if len(morphMsgs(msgs)) != 0 {
		t.Fatalf("a search with results must not lemmatize: %+v", msgs)
	}
	var hits int
	for _, m := range msgs {
		if m.T == "hit" {
			hits += len(m.Results)
		}
	}
	if hits == 0 {
		t.Fatal("setup: the query was supposed to hit")
	}
}

// The wave is per dictionary, not per search. One dictionary lists the
// inflected form itself - as a Babylon glossary with hand-written aliases
// does - and the other indexes lemmas only. The first hits; the second must
// still be asked "know", instead of being silenced by its neighbour's hit.
func TestLemmaReachesDictionariesThatMissed(t *testing.T) {
	s := morphServer(t, map[string]string{
		"en-en-a.dsl": dslFile("Inflected", "knew"),
		"en-en-b.dsl": dslFile("Lemmas", "know"),
	})
	rows := getDicts(t, s, "/api/dicts")
	if len(rows) != 2 {
		t.Fatalf("setup: %d dictionaries, want 2", len(rows))
	}
	byName := map[string]string{}
	for _, d := range rows {
		byName[d.Name] = d.ID
	}

	msgs := stream(t, s, "/api/search?q=knew&mode=exact&dict=all")
	mm := morphMsgs(msgs)
	if len(mm) != 1 {
		t.Fatalf("want exactly one morph message, got %+v", msgs)
	}
	if mm[0].From != "knew" || mm[0].To != "know" {
		t.Fatalf("morph message = %+v, want knew -> know", mm[0])
	}
	// Before the notice: the dictionary that holds "knew" answered. After it:
	// the one that does not, and only that one - a dictionary that already hit
	// must never be sent a second set of results for the same query.
	before, after := map[string]int{}, map[string]int{}
	seen := false
	for _, m := range msgs {
		switch {
		case m.T == "morph":
			seen = true
		case m.T == "hit" && len(m.Results) > 0:
			if seen {
				after[m.Dict] += len(m.Results)
			} else {
				before[m.Dict] += len(m.Results)
			}
		}
	}
	if before[byName["Inflected"]] != 1 || len(before) != 1 {
		t.Fatalf("first wave = %v, want one result from the inflected dictionary", before)
	}
	if after[byName["Lemmas"]] != 1 || len(after) != 1 {
		t.Fatalf("lemma wave = %v, want one result from the lemma dictionary", after)
	}
}

// A dictionary whose name and folder say nothing is searched with the ENGLISH
// lemma - the one language assumed without evidence.
func TestLemmaFallsBackToEnglish(t *testing.T) {
	s := morphServer(t, map[string]string{"words.dsl": dslFile("Unlabelled", "know")})
	getDicts(t, s, "/api/dicts")

	msgs := stream(t, s, "/api/search?q=knew&mode=exact&dict=all")
	mm := morphMsgs(msgs)
	if len(mm) != 1 {
		t.Fatalf("want exactly one morph message, got %+v", msgs)
	}
	if mm[0].From != "knew" || mm[0].To != "know" || mm[0].Lang != "en" {
		t.Fatalf("morph message = %+v, want knew -> know (en)", mm[0])
	}
	// The notice may never stand over an empty list: results follow it.
	var after int
	seen := false
	for _, m := range msgs {
		switch {
		case m.T == "morph":
			seen = true
		case seen && m.T == "hit":
			after += len(m.Results)
		}
	}
	if after != 1 {
		t.Fatalf("want one result after the morph message, got %d", after)
	}
}

// Candidates are offered only to dictionaries of the matching language. Both
// dictionaries are searched in the first wave and both miss; the second wave
// asks each language its own question, and each answer reaches only the slots
// of that language.
func TestLemmaIsRestrictedToTheMatchingLanguage(t *testing.T) {
	s := morphServer(t, map[string]string{
		"en-en-a.dsl": dslFile("English", "know"),
		"ru-ru-b.dsl": dslFile("Russian", "идти"),
	})
	rows := getDicts(t, s, "/api/dicts")
	if len(rows) != 2 {
		t.Fatalf("setup: %d dictionaries, want 2", len(rows))
	}
	byName := map[string]string{}
	for _, d := range rows {
		byName[d.Name] = d.ID
	}

	for _, tc := range []struct {
		q, to, code, dict string
	}{
		{"knew", "know", "en", byName["English"]},
		{"идет", "идти", "ru", byName["Russian"]}, // via the ё retry
	} {
		msgs := stream(t, s, "/api/search?q="+tc.q+"&mode=exact&dict=all")
		mm := morphMsgs(msgs)
		if len(mm) != 1 {
			t.Fatalf("%s: want exactly one morph message, got %+v", tc.q, msgs)
		}
		if mm[0].To != tc.to || mm[0].Lang != tc.code {
			t.Fatalf("%s: morph = %+v, want %s (%s)", tc.q, mm[0], tc.to, tc.code)
		}
		seen := false
		for _, m := range msgs {
			if m.T == "morph" {
				seen = true
				continue
			}
			if seen && m.T == "hit" && len(m.Results) > 0 && m.Dict != tc.dict {
				t.Fatalf("%s: the other language's dictionary answered: %+v", tc.q, m)
			}
		}
	}
}

// MORPH_CACHE=0 is off, and off means no pack is ever linked in: the query
// that would have been lemmatized simply returns nothing.
func TestLemmaDisabled(t *testing.T) {
	s := morphServer(t, map[string]string{"en-en-a.dsl": dslFile("English", "know")})
	s.Morph = morph.New(0, "")
	getDicts(t, s, "/api/dicts")

	msgs := stream(t, s, "/api/search?q=knew&mode=exact&dict=all")
	if len(morphMsgs(msgs)) != 0 {
		t.Fatalf("MORPH_CACHE=0 must not lemmatize: %+v", msgs)
	}
}
