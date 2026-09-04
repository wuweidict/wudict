// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package dict

import (
	"sort"
	"strings"
	"sync"
)

// What a dictionary says about ITSELF - the editorial blurb, the copyright, the
// edition. Six formats already parse one into Meta.Description; some ship it in
// a sidecar beside the file instead (Lingvo's "<stem>.ann"), which no opened
// dictionary can hand back because it was never part of the dictionary.
//
// A provider is therefore keyed by format and takes the SOURCE PATH, exactly
// like resource.Register (internal/resource/link.go) and RegisterProber: it
// runs without opening anything. That is what makes an About cheap enough to
// resolve while a panel is being drawn, and it is why the sidecar is read live
// on every request rather than baked into a library at ingest - an annotation
// is display text, it is not part of any article, and it must never make a
// prepared library look stale (the deliberate inverse of D97's _abrv rule).

// Section is one labelled block of an About. Lang is the language NAME as the
// source spelled it ("Russian"), empty for an unlabelled block; it is a
// heading, not a code, and is never matched against a locale.
type Section struct {
	Lang string
	Text string
}

// About is the raw, unescaped annotation of one dictionary. Nothing here is
// sanitised: package dict stays free of internal/htmlref, and the single
// normalisation point lives in the server (internal/server/about.go).
type About struct {
	Sections []Section
	// HTML says Text is dictionary markup rather than plain text, and so
	// selects the sanitiser instead of the escaper.
	HTML bool
	// Source is the sidecar the text came from, "" when the format's own
	// header supplied it.
	Source string
}

// Empty reports whether an About carries no text at all. A provider that
// returns true with nothing in it is treated as a miss.
func (a About) Empty() bool {
	for _, s := range a.Sections {
		if strings.TrimSpace(s.Text) != "" {
			return false
		}
	}
	return true
}

// AboutProvider resolves the annotation of one dictionary from its source path.
// It reports false - never an error - for a missing, unreadable or empty
// annotation: a panel being drawn is not a place to fail.
type AboutProvider func(srcPath string) (About, bool)

var (
	aboutMu  sync.RWMutex
	abouters = map[string]AboutProvider{}
)

// RegisterAbout wires a format name (Meta.Format: "dsl", "mdx", ...) to its
// annotation provider. Optional: a format without one falls back to whatever
// its header put in Meta.Description.
func RegisterAbout(format string, fn AboutProvider) {
	if fn == nil {
		return
	}
	aboutMu.Lock()
	defer aboutMu.Unlock()
	abouters[strings.ToLower(format)] = fn
}

// AboutFor asks format's provider for the annotation beside srcPath. It
// reports false when the format has no provider, when srcPath is empty, or
// when the provider found nothing usable.
func AboutFor(format, srcPath string) (About, bool) {
	if srcPath == "" {
		return About{}, false
	}
	aboutMu.RLock()
	fn := abouters[strings.ToLower(format)]
	aboutMu.RUnlock()
	if fn == nil {
		return About{}, false
	}
	a, ok := fn(srcPath)
	if !ok || a.Empty() {
		return About{}, false
	}
	return a, true
}

// AboutForPath asks EVERY registered provider about srcPath and returns the
// first usable answer, for the caller that has a path and no reliable format
// name for it.
//
// That caller is the server's About endpoint. Naming the format there used to
// mean opening the dictionary to ask it, and opening is not free in either
// direction: a DSL that has never been opened INGESTS on its first open, so a
// GET that only wanted a blurb paid for a whole library. A provider is already
// required to decide from the path alone and to report false rather than fail
// (AboutProvider), and the one that exists rejects anything that is not its own
// suffix - so asking all of them costs a handful of stats and no open at all.
//
// Deterministic by format name rather than map order, so a path two providers
// would both answer resolves the same way twice.
func AboutForPath(srcPath string) (About, bool) {
	if srcPath == "" {
		return About{}, false
	}
	aboutMu.RLock()
	names := make([]string, 0, len(abouters))
	for name := range abouters {
		names = append(names, name)
	}
	fns := make([]AboutProvider, 0, len(abouters))
	sort.Strings(names)
	for _, name := range names {
		fns = append(fns, abouters[name])
	}
	aboutMu.RUnlock()
	for _, fn := range fns {
		if a, ok := fn(srcPath); ok && !a.Empty() {
			return a, true
		}
	}
	return About{}, false
}
