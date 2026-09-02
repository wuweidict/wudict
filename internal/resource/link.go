// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package resource

import "sync"

// Serving a dictionary's media WITHOUT opening the dictionary (O8).
//
// A prepared dictionary answers searches from its text.db, but its images and
// audio used to come from `upgraded.source()` - a full direct backend, opened
// solely so that one .ogg could be read out of it. That open decompresses every
// key block and decodes every headword of the .mdx AND of each companion .mdd,
// at a few hundred bytes per entry: hundreds of megabytes and seconds of CPU
// to serve eight kilobytes, on a handle the janitor then evicts, so the next
// article pays it again.
//
// Two things replace it, both keyed on the format name:
//
//   - Sources: containers that need no dictionary at all. A StarDict `res/`
//     folder, a DSL `.files.zip`, a file lying loose beside the .mdx. These are
//     derived from the SOURCE PATH, which the prepared folder already records,
//     so the answer costs a stat.
//   - Open: a Fetcher over locations recorded earlier (Link/Part below), for
//     containers whose bytes are packed inside the dictionary file - an .mdd.
//     The container is reopened for its record table only, which is per BLOCK
//     rather than per file and therefore small.
//
// The recorded locations are a CACHE. They are derivable from the source at any
// time, their absence is never an error, and deleting them is always safe -
// which is what keeps them out of the prepared folder's portable contract
// (D20): a folder that must work with the source gone still needs media.db.

// Part is one container file that Links point into. Size and MTime are what it
// measured when the locations were recorded: offsets are only meaningful for
// the exact bytes they were taken from, so a container that has been
// re-downloaded or recompressed must invalidate them rather than serve whatever
// now sits at that offset.
type Part struct {
	Path  string
	Size  int64
	MTime int64
}

// Link is where one resource's bytes are: which Part, and where inside it.
// Off/Size are in whatever coordinate the format's Fetcher understands - for
// MDict, offsets into the decompressed record stream.
type Link struct {
	Name string // resource.Key form: cleaned, NFC, lower case
	MIME string
	Part int
	Off  int64
	Size int64
}

// Linker is implemented by an opened dictionary that can say where its media
// lives instead of handing over the bytes. Enumerating costs the same walk the
// media packer does, and it is done once, from a backend that is already open.
type Linker interface {
	MediaLinks() ([]Part, []Link, error)
}

// Fetcher reads bytes back out of the recorded containers.
type Fetcher interface {
	Fetch(part int, off, size int64) ([]byte, error)
	Close() error
}

// Provider is what one format can do for its media without being opened.
// Either field may be nil: StarDict and DSL keep their resources in plain
// folders and archives and so need no Fetcher at all, which is why they are the
// cheapest case rather than the hardest.
type Provider struct {
	Sources func(srcPath string) []Source
	Open    func(parts []Part) (Fetcher, error)
}

var (
	provMu    sync.RWMutex
	providers = map[string]Provider{}
)

// Register records a format's media provider. Called from a format package's
// init, like the format registry itself.
func Register(format string, p Provider) {
	provMu.Lock()
	defer provMu.Unlock()
	providers[format] = p
}

// Get returns the provider for a format name ("mdx", "dsl", …), and whether
// one is registered. A format with none simply keeps the old fallback.
func Get(format string) (Provider, bool) {
	provMu.RLock()
	defer provMu.RUnlock()
	p, ok := providers[format]
	return p, ok
}
