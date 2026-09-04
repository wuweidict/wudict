// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package dsl

import (
	"errors"
	"io"
	"os"
	"strings"
	"time"

	"github.com/wuweidict/wudict/internal/dict"
	"github.com/wuweidict/wudict/internal/logx"
	"github.com/wuweidict/wudict/internal/store"
)

// A DSL dictionary is often shipped with a "<name>_abrv.dsl" beside it. That
// file is not a dictionary: it is the expansion map ABBYY Lingvo shows on hover
// over a [p] label - "pl" over which the reader sees "plural". The parent
// absorbs it here, at ingest, and closeLabel bakes the expansion into the
// article as a title= attribute; discovery hides the companion itself
// (dict.IsAbbrevCompanion), which is what Lingvo does with it too.
//
// Baking rather than looking up at read time is what makes this free
// everywhere: the tooltip works the same in the built-in UI, in a sandboxed
// iframe, in the Android WebView, under `-format clean` and in `wudict dump`,
// with no client code at all.

const (
	// The companion is a glossary of a few hundred labels. Anything of this
	// size is not one, whatever it is named, and is left alone rather than
	// pulled into memory during someone else's ingest.
	maxAbbrevFile = 8 << 20
	maxAbbrevKeys = 20000
	// A tooltip is a label's expansion, not an article. Long values are cut so
	// one hostile entry cannot push a megabyte into every article that happens
	// to use its abbreviation.
	maxAbbrevRunes = 200
)

// abbrevMap resolves a [p] label to its expansion. The exact spelling wins; the
// case-folded map is the fallback, because a glossary keyed "Adj." is routinely
// used from a "[p]adj.[/p]" label. A nil *abbrevMap resolves nothing, which is
// how "no companion" is spelled - the transformer then emits exactly the bytes
// it emitted before this existed.
type abbrevMap struct {
	exact map[string]string
	fold  map[string]string

	path  string
	size  int64
	mtime time.Time
	count int
}

func (a *abbrevMap) lookup(label string) (string, bool) {
	if a == nil || label == "" {
		return "", false
	}
	if v, ok := a.exact[label]; ok {
		return v, true
	}
	v, ok := a.fold[strings.ToLower(label)]
	return v, ok
}

// loadAbbrev reads the abbreviation companion of a DSL main file. It never
// fails the caller: a companion that is missing, oversized, unreadable or
// malformed yields nil and a note at -v. The parent's own ingest is not the
// place to die over an auxiliary file.
func loadAbbrev(mainPath string) *abbrevMap {
	path, ok := dict.AbbrevCompanion(mainPath)
	if !ok {
		return nil
	}
	st, err := os.Stat(path)
	if err != nil {
		return nil
	}
	if st.Size() > maxAbbrevFile {
		logx.V("dsl abbreviations: %s is %d bytes - too large for a glossary, ignored", path, st.Size())
		return nil
	}
	r, err := newAbbrevReader(path)
	if err != nil {
		logx.V("dsl abbreviations: %s: %v", path, err)
		return nil
	}
	defer r.Close()

	a := &abbrevMap{
		exact: map[string]string{},
		fold:  map[string]string{},
		path:  path,
		size:  st.Size(),
		mtime: st.ModTime().UTC().Truncate(time.Second),
	}
	for len(a.exact) < maxAbbrevKeys {
		e, err := r.Next()
		if err != nil {
			if !errors.Is(err, io.EOF) {
				logx.V("dsl abbreviations: %s: %v", path, err)
			}
			break
		}
		exp := abbrevText(e.Body)
		if exp == "" {
			continue
		}
		for _, hw := range e.Headwords {
			k := collapseSpace(hw)
			// An entry that expands to its own headword says nothing, and a
			// tooltip repeating the word under the cursor is noise.
			if k == "" || strings.EqualFold(k, exp) {
				continue
			}
			if _, seen := a.exact[k]; !seen {
				a.exact[k] = exp
				a.count++
			}
			// Presence, not emptiness: empty expansions are filtered three
			// functions away, and the first-wins rule should not depend on
			// that filter still being there.
			f := strings.ToLower(k)
			if _, seen := a.fold[f]; !seen {
				a.fold[f] = exp
			}
		}
	}
	if a.count == 0 {
		logx.V("dsl abbreviations: %s held no usable entries", path)
		return nil
	}
	logx.V("dsl abbreviations: %d labels from %s", a.count, path)
	return a
}

// abbrevText reduces one glossary article to the plain text a title= attribute
// can carry. StripHTML already unescapes entities and collapses whitespace; the
// cut is on runes, so a multi-byte expansion is never sliced mid-character.
func abbrevText(body string) string {
	s := strings.TrimSpace(store.StripHTML(body))
	if r := []rune(s); len(r) > maxAbbrevRunes {
		s = strings.TrimSpace(string(r[:maxAbbrevRunes])) + "…"
	}
	return s
}
