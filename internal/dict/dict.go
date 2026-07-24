// Package dict defines the format-agnostic core of gonow-dict: the
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
	Exact  bool
	Prefix bool
	Fuzzy  bool // FTS5 over headwords — ingested backend only
	FTS    bool // FTS5 over headwords + article text — ingested backend only
}

// Meta describes one opened dictionary.
type Meta struct {
	Name        string // display name (dictionary title, else file stem)
	Format      string // "mdx" | "stardict" | "slob" | "dsl" | "gonow"
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

	// Keywords returns up to n headwords starting at offset, for browsing.
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

// FuzzySearcher is implemented by backends with Caps.Fuzzy.
type FuzzySearcher interface {
	Fuzzy(word string, limit int) ([]Result, error)
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
