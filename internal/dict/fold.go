// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package dict

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// FoldVersion identifies Fold's BEHAVIOUR, and must be bumped in the same
// commit as any change to what Fold returns.
//
// Fold is called in two places with different lifetimes: at query time on what
// the user typed, and at ingest time on every headword - where the result is
// PERSISTED into the trigram index that powers the "contains" mode
// (store/ingest.go). Change Fold without bumping this, and every prepared
// dictionary keeps a trigram index built by the old rules while queries fold
// by the new ones: a search that quietly stops finding words it used to find,
// with no error, and invisible to any test that ingests fresh data.
//
// Nothing else Fold touches is stored. The direct backends rebuild their fold
// indexes on every open, entry_fts stores raw text and folds with SQLite's own
// tokenizer, and idx_entry_w is a raw NOCASE index - so a bump makes exactly
// one thing stale, and only for dictionaries that opted into "contains".
const FoldVersion = 1

// Fold lowercases and strips combining marks for accent/case-insensitive
// matching. Shared by every direct backend's fold index.
func Fold(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	b.Grow(len(s))
	for _, ru := range norm.NFD.String(s) {
		if unicode.Is(unicode.Mn, ru) {
			continue
		}
		b.WriteRune(ru)
	}
	return b.String()
}
