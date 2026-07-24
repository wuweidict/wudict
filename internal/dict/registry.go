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

// readerOpener opens the sequential ingest scan for one source file.
type readerOpener func(path string) (Reader, error)

var readerOpeners = map[string]readerOpener{}

// RegisterReader wires a file extension to a format's ingest Reader.
func RegisterReader(ext string, fn readerOpener) {
	readerOpeners[strings.ToLower(ext)] = fn
}

// OpenReader opens the ingest scan for path, dispatching on extension.
func OpenReader(path string) (Reader, error) {
	fn, ok := readerOpeners[strings.ToLower(filepath.Ext(path))]
	if !ok {
		return nil, fmt.Errorf("no ingest reader for format: %s", filepath.Ext(path))
	}
	return fn(path)
}

// Open opens the dictionary whose main file is path, dispatching on
// extension.
func Open(path string) (Dictionary, error) {
	fn, ok := openers[strings.ToLower(filepath.Ext(path))]
	if !ok {
		return nil, fmt.Errorf("unsupported dictionary format: %s", filepath.Ext(path))
	}
	return fn(path)
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
