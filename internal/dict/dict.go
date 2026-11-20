// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Package dict defines the format-agnostic core of wudict: the
// Dictionary interface every backend (direct native readers, ingested
// SQLite) implements, plus the ingest-side Reader contract shared by all
// format packages. See docs/SPEC.md in the workspace root.
package dict

import (
	"context"
	"errors"
	"fmt"
	"html"
	"io"

	"github.com/wuweidict/wudict/internal/logx"
)

var (
	// ErrNotFound is returned for a missing headword or resource.
	ErrNotFound = errors.New("not found")
	// ErrUnsupported is returned when a backend lacks a capability
	// (e.g. full-text search on a direct backend).
	ErrUnsupported = errors.New("operation not supported by this backend")
	// ErrUnreadable marks a failure the file's own bytes decide: the same
	// file opened by the same build fails the same way, so it is worth
	// remembering until either changes.
	ErrUnreadable = errors.New("not readable")
	// ErrTooLarge marks a dictionary too large to open on this system: its
	// text unpacks past what its reader accepts (also ErrUnreadable), or past
	// the free space to hold it (*NoRoomError).
	ErrTooLarge = errors.New("too large to open")
)

// NoRoomError: opening the dictionary needs more free space than there is.
// Dir is the folder the space is missing in, "" for memory (Free is then
// what this process allows itself). Need is a lower
// bound - what was known to be needed when the open stopped - so an open
// retried once Free exceeds it can still stop again, further on.
type NoRoomError struct {
	What       string // what the space was for, e.g. "to unpack it"
	Dir        string
	Need, Free int64
}

func (e *NoRoomError) Error() string {
	where := "memory"
	if e.Dir != "" {
		where = "free disk space in " + e.Dir
	}
	return fmt.Sprintf("not enough %s %s: more than %s needed, %s available",
		where, e.What, logx.Size(e.Need), logx.Size(e.Free))
}

func (e *NoRoomError) Is(target error) bool { return target == ErrTooLarge }

// Caps advertises which search modes a backend supports. The UI/API must
// consult this instead of probing with queries.
type Caps struct {
	Exact    bool
	Prefix   bool // starts-with (accent-insensitive on ingested backends)
	FTS      bool // full text: FTS5 over headwords + article text (Searcher), or the file's own index (D194)
	Wildcard bool // `?` and `*` over headwords (D167) - Searcher only
}

// Meta describes one opened dictionary.
type Meta struct {
	Name        string // display name (dictionary title, else file stem)
	Format      string // "mdx" | "stardict" | "slob" | "dsl" | "bgl" | "zim" | "wmd" | "wudict"
	Path        string // source path (or .text.db path for ingested)
	Description string
	EntryCount  int

	// IndexLang is the ISO 639-1 code of the language the HEADWORDS are in,
	// as the dictionary itself declares it (Lingvo's #INDEX_LANGUAGE, Babylon's
	// source language), normalised by internal/lang. "" when the format
	// declares nothing, which is most of them - the search path then falls back
	// to the file and folder naming conventions, and never records what it
	// worked out that way. See internal/lang.
	IndexLang string

	// ContentsLang is the ISO 639-1 code of the language the ARTICLE BODIES are
	// in, where the format declares it (Lingvo's #CONTENTS_LANGUAGE, Babylon's
	// target language). DSL and BGL are currently the only ones that say; ""
	// everywhere else, and the About panel simply omits the row. Same rule as IndexLang: what the dictionary
	// declared, never what a file name suggested.
	ContentsLang string

	// Header is the rest of what the dictionary says about itself: every
	// non-empty field of its own header that none of the fields above already
	// carries (author, copyright, creation date, engine version…), in file
	// order and under the file's own key names. Informational only - nothing
	// decides behaviour from it; `wudict info` prints it. Cheap probes leave it
	// nil.
	Header []Field
}

// Field is one name/value pair from a dictionary header, as the file spells it.
type Field struct {
	Name, Value string
}

// DisplayText decodes the character references a dictionary's human-readable
// header fields still carry, and is the one place that leniency lives.
//
// A header value is XML, so its parser undoes the five predefined entities and
// stops - which is right, and not enough. Two dictionaries in a 93-title MDX
// corpus arrive still escaped: one titles itself "Webster&#x27;s" (a numeric
// character reference, outside the XML predefined set), the other
// "Learner&amp;#x27;s" - HTML-escaped by its builder and then XML-escaped over
// the top. Both are the dictionary's fault; neither is a reason to show a user
// "&#x27;" where an apostrophe belongs.
//
// Decoding therefore runs to a fixed point, bounded at two passes: enough for
// one layer of over-escaping, never a loop over hostile input. Apply it only
// to text shown as text - a name or a description. Header fields whose
// entities are OUTPUT rather than displayed (StyleSheet, article markup) must
// keep exactly the escaping they were written with.
func DisplayText(s string) string {
	for i := 0; i < 2; i++ {
		u := html.UnescapeString(s)
		if u == s {
			break
		}
		s = u
	}
	return s
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
	// Use KeywordRange to resolve the window, so every backend reads these
	// three the same way.
	Keywords(offset, n int) []string

	// Resource streams a binary resource (image/audio/css) by its
	// normalized name (forward slashes, no leading slash). The string is
	// the MIME type ("" = unknown). Returns ErrNotFound when absent.
	Resource(name string) (io.ReadCloser, string, error)

	Close() error
}

// ResourceOpener is Dictionary's Resource alone, for code that reads a
// dictionary's files without its index: `wudict dump` reaches a DSL's, BGL's
// or wudict markdown file's resources through one without preparing it.
type ResourceOpener interface {
	Resource(name string) (io.ReadCloser, string, error)
}

// ResourceLister is implemented by backends that can enumerate their
// binary resources (used by full ingest to pack a media.db).
type ResourceLister interface {
	Resources() []string
}

// Searcher is the search surface of a prepared database - the only backend
// that has one. Every query takes a context, and a cancelled context
// interrupts it inside SQLite: the web UI abandons a search on every
// keystroke, and an abandoned query that runs to completion is CPU spent on
// output nobody reads. A direct-format backend has nothing to interrupt, so
// it answers only Dictionary's Exact and Prefix.
//
// FullTextContext and FullTextMatch need Caps.FTS; without it they return
// ErrUnsupported.
// WildcardContext needs only the headword index, which every prepared
// database has.
type Searcher interface {
	ExactContext(ctx context.Context, word string, limit int) ([]Result, error)
	PrefixContext(ctx context.Context, word string, limit int) ([]Result, error)
	// FullTextContext reads query as one bag of prefix words: the fallback
	// when no composed reading could run.
	FullTextContext(ctx context.Context, query string, limit int) ([]Result, error)
	// FullTextMatch runs one reading of a full-text query. A query has more
	// than one - `Физика в конспектах` is first a phrase, then a proximity,
	// then a bag of words - and only the caller knows which to try in what
	// order. match is an FTS5 expression composed by internal/ftsq, never
	// assembled from user text anywhere else: raw input reaching MATCH is how
	// a quote in a search box becomes a syntax error (FTS-audit #2).
	FullTextMatch(ctx context.Context, match string, limit int) ([]Result, error)
	// WildcardContext matches whole headwords against a wildcard pattern
	// (internal/wildcard): `?` is one character, `*` any run of characters.
	WildcardContext(ctx context.Context, pattern string, limit int) ([]Result, error)
}

// Keyer lists the keys a prepared database would answer a query with, without
// reading an article: what the list of matching words under the search box is
// made of (D199). One key for each entry the Searcher method of the same name
// returns, in the same order - the headword, or for a prefix the alias that
// was typed. Both read the headword index only.
type Keyer interface {
	PrefixKeys(ctx context.Context, word string, limit int) ([]string, error)
	WildcardKeys(ctx context.Context, pattern string, limit int) ([]string, error)
}

// Letter is one chip of the browse strip: a run of headwords sharing an
// initial, and where that run begins in browse order.
//
// Offset is a position in the SAME sequence Browser.Page pages through, so a
// chip becomes a page by integer division and nothing has to agree about page
// size across the wire.
type Letter struct {
	Letter string `json:"l"` // what the chip shows: an uppercase initial, "0-9", "#"
	Offset int    `json:"o"` // browse offset of the first headword under it
	Count  int    `json:"n"` // how many headwords it holds
}

// Browser is implemented by backends that can present the whole dictionary as
// an ordered, addressable list - reading it page by page instead of querying
// it.
//
// Only the prepared backend implements it, and that is the contract rather
// than an omission: all three answers are reads of one index (idx_entry_w),
// which is what makes a jump to "M" in a 2.9 M-entry dictionary a seek instead
// of a scan. A direct format backend would have to sort its entire headword
// list in memory to answer any of them - precisely the cost preparation
// exists to remove - so browsing is offered where the index is, and the
// dictionary panel is where an index is asked for.
//
// Browse order is the index's order (case-insensitive by headword), NOT the
// source file's entry order: a paper dictionary's pages are alphabetical, and
// `wudict keys` - which reads the source sequentially - is the other tool.
type Browser interface {
	// Page returns up to n headwords starting at offset, in browse order.
	// An offset past the end returns nil, never an error.
	Page(offset, n int) ([]string, error)

	// Locate returns the browse offset of the first headword at or after
	// word, i.e. where the reader lands when they jump to it. A word past
	// the last headword resolves to the end.
	Locate(word string) (int, error)

	// Alphabet returns every initial present, in browse order. Cheap to call
	// repeatedly: the backend computes it once per open.
	Alphabet() ([]Letter, error)

	// Jump says where Browse puts the reader down for a typed word (D203):
	// on the headword it spells, accents and case aside; else on the first
	// headword it starts; else at the place it would have, before Word.
	Jump(ctx context.Context, word string) (Landing, error)
}

// How a typed word matched where Browse landed (Landing.Match).
const (
	JumpExact  = "exact"  // the headword, accents and case aside
	JumpPrefix = "prefix" // the first headword it starts
	JumpNone   = "none"   // no headword: Offset is where it would be
)

// Landing is a Jump's answer: the browse offset, the headword there ("" past
// the last one), and how the typed word matched it.
type Landing struct {
	Offset int
	Word   string
	Match  string
}

// Sampler draws entries at random: the random word under the empty search box
// (D202). Only the prepared backend implements it, for the reason Browser
// gives: a draw is a seek in the entry table, where a direct backend would
// have to count its headwords first.
type Sampler interface {
	// Sample returns the headword of an entry drawn from u, uniform over the
	// uint64 range: the randomness is the caller's, so a test can name the
	// entry it wants. Every entry is about equally likely; sub-entries (`@_…`)
	// are never returned. No article is read. ErrNotFound when the dictionary
	// holds no entry.
	Sample(ctx context.Context, u uint64) (string, error)
	// Has reports whether an exact search for word finds an entry, by its
	// headword or an alias, reading the headword index only.
	Has(ctx context.Context, word string) (bool, error)
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
// index - which is the same arithmetic that made `keys -n -1` panic in
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
