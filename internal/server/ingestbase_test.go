// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import "testing"

// The panel's "🚀 index" chip: level=headwords at a dictionary that has no
// database at all prepares it. The chip exists because this path does NOT open
// the direct backend first (handleIngest), which is what setFeatures does and
// what the heavy, still-unprepared dictionaries cannot afford - but the door
// taken is not visible from outside, so what is asserted here is the outcome:
// a prepared dictionary, at the cheapest level, with nothing else built.
func TestIngestBaseIndexesAnUnpreparedDictionary(t *testing.T) {
	s, e := demandEntry(t)
	if prepared(e) {
		t.Fatal("setup: the fixture was supposed to be unprepared")
	}

	sse(t, s, "/api/ingest?dict="+e.ID+"&level=headwords")

	if !prepared(e) {
		t.Fatal("level=headwords left the dictionary unprepared")
	}
	if f := s.currentFeatures(e); f.FullText || f.Contains || f.Media {
		t.Errorf("the cheap index built more than headwords: %+v", f)
	}
}

// The same parameter keeps its old meaning on a dictionary that already HAS a
// database: turn full text off. That is a rebuild, so it must go through
// setFeatures - and this is the assertion that tells the two apart, because
// ensureBaseIndex returns early at an already-prepared dictionary and would
// leave full text exactly where it was. Contains survives: the panel sends
// desired state, and a parameter left out changes nothing.
func TestIngestHeadwordsStillStripsFullText(t *testing.T) {
	s := newTestServer(t)
	id := getDicts(t, s, "/api/dicts")[0].ID
	e, err := s.reg.get(id)
	if err != nil {
		t.Fatal(err)
	}
	sse(t, s, "/api/ingest?dict="+id+"&fts=1&contains=1")
	if f := s.currentFeatures(e); !f.FullText || !f.Contains {
		t.Fatalf("setup: %+v, want full text and contains", f)
	}

	sse(t, s, "/api/ingest?dict="+id+"&level=headwords")

	f := s.currentFeatures(e)
	if f.FullText {
		t.Error("legacy level=headwords must remove full text")
	}
	if !f.Contains {
		t.Error("a parameter that was not sent must keep its value")
	}
}
