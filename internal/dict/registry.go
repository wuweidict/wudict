// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package dict

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
)

// opener opens one dictionary given the path of its main file.
type opener func(path string) (Dictionary, error)

var openers = map[string]opener{} // key: lowercase extension incl. dot, e.g. ".mdx"

// RegisterFormat wires a file extension to a format package. Called from
// format package init(); the cmd package blank-imports each format.
func RegisterFormat(ext string, fn opener) {
	openers[strings.ToLower(ext)] = fn
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
	_, ok := probers[strings.ToLower(filepath.Ext(path))]
	return ok
}

// Probe returns lightweight metadata for the dictionary at path without
// the cost of a full Open (no key-block decompression, no fold-maps). It
// is used to populate the dictionary list and to locate a cached text.db
// (via its name) without opening the heavy direct backend. Formats with
// no registered prober fall back to a full Open.
func Probe(path string) (m Meta, err error) {
	defer recoverOpen(path, &err)
	if fn, ok := probers[strings.ToLower(filepath.Ext(path))]; ok {
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
	fn, ok := readerOpeners[strings.ToLower(filepath.Ext(path))]
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
	fn, ok := openers[strings.ToLower(filepath.Ext(path))]
	if !ok {
		return nil, fmt.Errorf("unsupported dictionary format: %s", filepath.Ext(path))
	}
	defer recoverOpen(path, &err)
	return fn(path)
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
			if _, ok := openers[strings.ToLower(filepath.Ext(p))]; ok {
				out = append(out, p)
			}
		}
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i]) < strings.ToLower(out[j]) })
	return out, err
}
