// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package dict

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// opener opens one dictionary given the path of its main file.
type opener func(path string) (Dictionary, error)

var openers = map[string]opener{} // key: lowercase extension incl. dot, e.g. ".mdx"

// fileOpeners keys on an exact lowercase base name rather than a suffix, for
// formats whose main file has a fixed name inside a bundle directory (a
// prepared-dictionary folder holds "text.db"). Suffix matching cannot express
// this safely: a "text.db" suffix key would also swallow an unrelated
// "context.db", and matching bare ".db" is exactly the greedy registration
// that made a "media.db" sidecar open as a phantom dictionary.
var fileOpeners = map[string]opener{}

// RegisterFormat wires a file extension to a format package. Called from
// format package init(); the cmd package blank-imports each format. The key
// may be a multi-part suffix (e.g. ".dsl.dz") — matchKey prefers the longest
// match, so a StarDict companion ".dict.dz" is not mistaken for a ".dz" dict.
func RegisterFormat(ext string, fn opener) {
	openers[strings.ToLower(ext)] = fn
}

// RegisterFileName wires an exact file name (not a suffix) to a format.
func RegisterFileName(name string, fn opener) {
	fileOpeners[strings.ToLower(name)] = fn
}

// inspectOpeners are files wudict can OPEN by explicit path but must never
// DISCOVER as dictionaries.
//
// Not everything readable is a dictionary. An .mdd is MDict's resource
// container — the same key-block file format as an .mdx, holding file bytes
// instead of articles — so `wudict keys x.mdd` and `wudict res x.mdd name`
// are exactly as meaningful there as on an .mdx, and a user who has only the
// .mdd must not be told to go and find an .mdx that may not exist. But a
// folder scan must not turn every companion .mdd into a dictionary in the
// panel: that is the same greedy registration the store package warns about,
// where a bare ".db" key made every media.db sidecar open as a phantom
// dictionary.
var inspectOpeners = map[string]opener{}

// RegisterInspectable wires an extension that Open accepts and Discover
// ignores. See inspectOpeners.
func RegisterInspectable(ext string, fn opener) {
	inspectOpeners[strings.ToLower(ext)] = fn
}

// openerFor resolves the opener for an explicitly named path: an exact
// base-name registration first (bundle main files), then the longest matching
// dictionary suffix, and only then an inspect-only container — so a real
// format always wins.
func openerFor(path string) (opener, bool) {
	if fn, ok := fileOpeners[strings.ToLower(filepath.Base(path))]; ok {
		return fn, true
	}
	if fn, ok := matchKey(openers, path); ok {
		return fn, true
	}
	return matchKey(inspectOpeners, path)
}

// discoverableFor is openerFor without the inspect-only containers: what a
// folder scan is allowed to consider a dictionary.
func discoverableFor(path string) (opener, bool) {
	if fn, ok := fileOpeners[strings.ToLower(filepath.Base(path))]; ok {
		return fn, true
	}
	return matchKey(openers, path)
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
	fn, ok := openerFor(path)
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

// excludedDirs are subtrees Discover never walks — canonical absolute paths.
// The generated-database directory is registered here at startup: it is the
// app's private library, never a discovery root, so a dict dir that happens to
// contain (or equal an ancestor of) it cannot list prepared dictionaries twice.
var excludedDirs []string

// ExcludeDir marks dir as never-walked by Discover.
func ExcludeDir(dir string) {
	if c := CanonPath(dir); c != "" {
		excludedDirs = append(excludedDirs, c)
	}
}

// CanonPath resolves dir to an absolute, symlink-free, cleaned path so that
// "~/.wudict/db" and "/Users/x/.wudict/db" compare equal.
func CanonPath(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return filepath.Clean(resolved)
	}
	return filepath.Clean(abs)
}

// DedupeDirs drops folders that are the SAME folder reached by a different
// spelling, keeping the first spelling and the original order.
//
// Discovery already guarantees a dictionary is never listed twice, so this is
// not about correctness of results — it is about not showing the user four
// rows for one folder, not walking that folder four times, and not writing
// duplicates into wudict.toml.
//
// Identity is os.SameFile where both paths exist: that catches a symlink, a
// case-variant spelling on a case-insensitive filesystem, a hard link and a
// bind mount, none of which string comparison sees. Paths that do not exist
// (an unmounted drive, a folder yet to be created) fall back to comparing
// canonical strings — they must still be deduped, and must still be kept.
func DedupeDirs(dirs []string) []string {
	var out []string
	var infos []os.FileInfo
	var keys []string
	for _, d := range dirs {
		if strings.TrimSpace(d) == "" {
			continue
		}
		key := CanonPath(d)
		fi, _ := os.Stat(d)
		dup := false
		for i := range out {
			if fi != nil && infos[i] != nil {
				if os.SameFile(fi, infos[i]) {
					dup = true
					break
				}
				continue // both exist and differ: a string match cannot override
			}
			if key != "" && key == keys[i] {
				dup = true
				break
			}
		}
		if dup {
			continue
		}
		out = append(out, d)
		infos = append(infos, fi)
		keys = append(keys, key)
	}
	return out
}

// SameDir reports whether two paths denote the same directory, comparing
// canonical (absolute, symlink-resolved) forms.
func SameDir(a, b string) bool {
	ca, cb := CanonPath(a), CanonPath(b)
	return ca != "" && ca == cb
}

func isExcluded(dir string) bool {
	if len(excludedDirs) == 0 {
		return false
	}
	c := CanonPath(dir)
	if c == "" {
		return false
	}
	for _, e := range excludedDirs {
		if e == c {
			return true
		}
	}
	return false
}

// RootScan is what one root contributed: New counts the dictionaries it was
// the first to offer, Total everything it holds. They differ when roots
// overlap, which lets the UI say "already listed from another folder" instead
// of the misleading "no dictionaries found".
type RootScan struct{ New, Total int }

// DiscoverAll walks several roots and returns their dictionaries as one list,
// with each dictionary appearing exactly once.
//
// Deduplication is by CANONICAL path (symlinks resolved), because overlapping
// roots are normal once more than one is allowed — "~/Dicts" alongside
// "~/Dicts/Spanish", or a symlinked shortcut to a folder already listed.
// Without it the same dictionary would get two registry ids: two rows in the
// panel and two copies of every hit in an all-dictionaries search.
//
// A root that cannot be walked (an unmounted drive, a deleted folder) is
// skipped rather than failing the scan — the other roots must keep working —
// and reported through the returned per-root counts, which say how many
// dictionaries each root contributed *first* (earlier roots win a tie).
func DiscoverAll(roots []string) (paths []string, perRoot []RootScan, err error) {
	perRoot = make([]RootScan, len(roots))
	seen := map[string]bool{}
	for i, root := range roots {
		found, ferr := Discover(root)
		if ferr != nil && err == nil {
			err = ferr
		}
		perRoot[i].Total = len(found)
		for _, p := range found {
			key := CanonPath(p)
			if key == "" {
				key = p
			}
			if seen[key] {
				continue
			}
			seen[key] = true
			paths = append(paths, p)
			perRoot[i].New++
		}
	}
	sort.Slice(paths, func(i, j int) bool { return strings.ToLower(paths[i]) < strings.ToLower(paths[j]) })
	return paths, perRoot, err
}

// Discover walks root recursively and returns the main files of all
// recognizable dictionaries, sorted case-insensitively. Excluded subtrees
// (see ExcludeDir) are skipped whole.
func Discover(root string) ([]string, error) {
	// Resolve the root first: filepath.WalkDir lstats what it is given, so a
	// SYMLINKED dictionary folder ("~/Dicts" → /Volumes/Ext/Dicts) yielded
	// nothing at all. Following the link the user explicitly configured is
	// intended; links *inside* the tree are still not followed, which is what
	// keeps cycles and surprise duplicates out.
	if c := CanonPath(root); c != "" {
		root = c
	}
	var out []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable subtree: skip, keep walking
		}
		if d.IsDir() {
			if isExcluded(p) {
				return fs.SkipDir
			}
			return nil
		}
		if _, ok := discoverableFor(p); ok {
			out = append(out, p)
		}
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i]) < strings.ToLower(out[j]) })
	return out, err
}

// IsDictionaryFile reports whether path names a file this build can serve as a
// dictionary: a registered main file or suffix, and not an inspect-only
// container (an .mdd is readable but is not a dictionary — see inspectOpeners).
// It is a name test, not a probe: no bytes are read.
//
// Used by the "open this file with wudict" entry point the desktop file
// associations rely on, so that `wudict nonsense` still reports an unknown
// command instead of trying to serve a typo.
func IsDictionaryFile(path string) bool {
	_, ok := discoverableFor(path)
	return ok
}
