// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Package dict defines the format-agnostic core of wudict: the
// Dictionary interface every backend (direct native readers, ingested
// SQLite) implements, plus the ingest-side Reader contract shared by all
// format packages. See docs/SPEC.md in the workspace root.
package dict

import (
	"errors"
	"io"
)

var (
	// ErrNotFound is returned for a missing headword or resource.
	ErrNotFound = errors.New("not found")
	// ErrUnsupported is returned when a backend lacks a capability
	// (e.g. fuzzy search on a direct backend).
	ErrUnsupported = errors.New("operation not supported by this backend")
)

// Caps advertises which search modes a backend supports. The UI/API must
// consult this instead of probing with queries.
type Caps struct {
	Exact    bool
	Prefix   bool // starts-with (accent-insensitive on ingested backends)
	Contains bool // substring/typo-tolerant headword match (FTS5 trigram) — ingested backend only
	FTS      bool // FTS5 over headwords + article text — ingested backend only
}

// Meta describes one opened dictionary.
type Meta struct {
	Name        string // display name (dictionary title, else file stem)
	Format      string // "mdx" | "stardict" | "slob" | "dsl" | "wudict"
	Path        string // source path (or .text.db path for ingested)
	Description string
	EntryCount  int
}

// Result is one matched entry, rendered to HTML.
type Result struct {
	Headword string
	Body     string // article HTML (native markup already converted)
}

// Dictionary is the unified runtime view over one dictionary, regardless
// of backend. Implementations must be safe for concurrent readers.
type Dictionary interface {
	Meta() Meta
	Caps() Caps

	// Exact returns entries whose headword matches word exactly
	// (implementations may fall back to case/accent-folded equality
	// when there is no raw exact hit).
	Exact(word string, limit int) ([]Result, error)
	// Prefix returns exact matches if any, else up to limit prefix matches.
	Prefix(word string, limit int) ([]Result, error)

	// Keywords returns headwords starting at offset, for browsing.
	//
	//	n <= 0     no limit: everything from offset onwards
	//	offset < 0 treated as 0
	//	offset past the last headword: nil, never an error
	//
	// The three of those used to be unstated, and the implementations had
	// drifted into disagreeing about all of them — two panicked on a negative
	// n, a third silently capped every answer at 500. Use KeywordRange to
	// resolve the window so a fourth backend cannot invent a fourth reading.
	Keywords(offset, n int) []string

	// Resource streams a binary resource (image/audio/css) by its
	// normalized name (forward slashes, no leading slash). The string is
	// the MIME type ("" = unknown). Returns ErrNotFound when absent.
	Resource(name string) (io.ReadCloser, string, error)

	Close() error
}

// ResourceLister is implemented by backends that can enumerate their
// binary resources (used by full ingest to pack a media.db).
type ResourceLister interface {
	Resources() []string
}

// ContainsSearcher is implemented by backends with Caps.Contains: a
// substring match over headwords (FTS5 trigram), accent/case-insensitive.
type ContainsSearcher interface {
	Contains(word string, limit int) ([]Result, error)
}

// FullTextSearcher is implemented by backends with Caps.FTS.
type FullTextSearcher interface {
	FullText(query string, limit int) ([]Result, error)
}

// Entry is one dictionary article as produced by a format Reader during
// an ingest scan. When LinkTo is non-empty the entry is a pure redirect
// (e.g. MDX @@@LINK): Body is ignored and Headwords become aliases of the
// entry whose headword is LinkTo.
type Entry struct {
	Headwords []string // first = display headword, rest = aliases
	Body      string
	Kind      BodyKind
	LinkTo    string
}

// BodyKind tells the ingester how to normalize Entry.Body to HTML.
type BodyKind int

const (
	BodyHTML BodyKind = iota // pass through
	BodyText                 // escape + wrap
	BodyXDXF                 // convert XDXF markup
	BodyDSL                  // convert DSL markup
)

// Reader is the sequential scan a format package provides for ingestion.
// Next returns io.EOF after the last entry.
type Reader interface {
	Meta() Meta
	Next() (Entry, error)
	Close() error
}

// KeywordRange resolves a browse window against a backend holding `total`
// headwords, per the Keywords contract above: offset<0 counts as 0, n<=0 means
// "to the end", and an offset past the end reports ok=false so the caller
// returns nil rather than slicing.
//
// The bound is computed against the REMAINING count rather than as offset+n,
// so a caller passing a huge n cannot overflow the sum into a negative slice
// index — which is the same arithmetic that made `keys -n -1` panic in
// makeslice.
func KeywordRange(total, offset, n int) (lo, hi int, ok bool) {
	if offset < 0 {
		offset = 0
	}
	if total <= 0 || offset >= total {
		return 0, 0, false
	}
	hi = total
	if n > 0 && n < total-offset {
		hi = offset + n
	}
	return offset, hi, true
}
