// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package dict

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
)

// opener opens one dictionary given the path of its main file.
type opener func(path string) (Dictionary, error)

var openers = map[string]opener{} // key: lowercase extension incl. dot, e.g. ".mdx"

// RegisterFormat wires a file extension to a format package. Called from
// format package init(); the cmd package blank-imports each format. The key
// may be a multi-part suffix (e.g. ".dsl.dz") — matchKey prefers the longest
// match, so a StarDict companion ".dict.dz" is not mistaken for a ".dz" dict.
func RegisterFormat(ext string, fn opener) {
	openers[strings.ToLower(ext)] = fn
}

// matchKey returns the registered map key that path ends with, preferring the
// LONGEST so ".dsl.dz" beats ".dz" and an unregistered suffix (".dict.dz",
// a StarDict companion found via its own .ifo) matches nothing.
func matchKey[T any](m map[string]T, path string) (T, bool) {
	lower := strings.ToLower(path)
	var best string
	var val T
	for k, v := range m {
		if len(k) > len(best) && strings.HasSuffix(lower, k) {
			best, val = k, v
		}
	}
	if best == "" {
		var zero T
		return zero, false
	}
	return val, true
}

// prober reads lightweight metadata (name, format, entry count) without
// building a format's full in-memory index.
type prober func(path string) (Meta, error)

var probers = map[string]prober{}

// RegisterProber wires a file extension to a cheap metadata reader.
// Optional: formats without one fall back to a full Open in Probe.
func RegisterProber(ext string, fn prober) {
	probers[strings.ToLower(ext)] = fn
}

// HasProber reports whether path's format has a cheap metadata prober
// (so a Probe miss on a cache means "not ingested" rather than "unknown").
func HasProber(path string) bool {
	_, ok := matchKey(probers, path)
	return ok
}

// Probe returns lightweight metadata for the dictionary at path without
// the cost of a full Open (no key-block decompression, no fold-maps). It
// is used to populate the dictionary list and to locate a cached text.db
// (via its name) without opening the heavy direct backend. Formats with
// no registered prober fall back to a full Open.
func Probe(path string) (m Meta, err error) {
	defer recoverOpen(path, &err)
	if fn, ok := matchKey(probers, path); ok {
		return fn(path)
	}
	d, err := Open(path)
	if err != nil {
		return Meta{}, err
	}
	defer d.Close()
	return d.Meta(), nil
}

// readerOpener opens the sequential ingest scan for one source file.
type readerOpener func(path string) (Reader, error)

var readerOpeners = map[string]readerOpener{}

// RegisterReader wires a file extension to a format's ingest Reader.
func RegisterReader(ext string, fn readerOpener) {
	readerOpeners[strings.ToLower(ext)] = fn
}

// OpenReader opens the ingest scan for path, dispatching on extension.
func OpenReader(path string) (r Reader, err error) {
	fn, ok := matchKey(readerOpeners, path)
	if !ok {
		return nil, fmt.Errorf("no ingest reader for format: %s", filepath.Ext(path))
	}
	defer recoverOpen(path, &err)
	return fn(path)
}

// Open opens the dictionary whose main file is path, dispatching on
// extension. Corrupt or truncated files must surface as errors, never
// panics: a slice-bounds panic deep in a format parser is converted here
// so one bad file cannot take down the server or a batch ingest.
func Open(path string) (d Dictionary, err error) {
	fn, ok := matchKey(openers, path)
	if !ok {
		return nil, fmt.Errorf("unsupported dictionary format: %s", filepath.Ext(path))
	}
	defer recoverOpen(path, &err)
	d, err = fn(path)
	if err != nil && (errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)) {
		// a bare "EOF" from a header/parse read is an unreadable file; name it
		// so a broken dictionary is identifiable instead of a cryptic "EOF".
		err = fmt.Errorf("%s: corrupt or truncated file", filepath.Base(path))
	}
	return d, err
}

func recoverOpen(path string, err *error) {
	if r := recover(); r != nil {
		*err = fmt.Errorf("%s: corrupt or unsupported file (parser panic: %v)", filepath.Base(path), r)
	}
}

// Discover walks root recursively and returns the main files of all
// recognizable dictionaries, sorted case-insensitively.
func Discover(root string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable subtree: skip, keep walking
		}
		if !d.IsDir() {
			if _, ok := matchKey(openers, p); ok {
				out = append(out, p)
			}
		}
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i]) < strings.ToLower(out[j]) })
	return out, err
}
