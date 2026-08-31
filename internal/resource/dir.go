// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package resource

import (
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"unicode/utf8"

	"github.com/wuweidict/wudict/internal/dict"
)

// maxScan caps the folded index of a folder. A dictionary's own media folder
// holds thousands of files; nothing legitimate holds a million, and the cap
// is what stops a symlink loop or a wrong path from eating the process.
const maxScan = 200000

// Dir is a folder of resource files: a DSL "<name>.dsl.files", a StarDict
// "res", or the folder the dictionary itself sits in.
//
// The exact relative path is tried first and costs one open. Only on a miss,
// and only when the folder belongs to this dictionary, is the tree walked and
// indexed so that case and Unicode normalization stop mattering - a folder
// the dictionary merely sits in is not swept, because it holds other people's
// dictionaries and their assets.
type Dir struct {
	root    string
	indexed bool

	once sync.Once
	ix   Index
	rel  []string // slot -> path relative to root, forward slashes
}

// NewDir is a folder owned by the dictionary: indexed on a miss.
func NewDir(root string) *Dir { return &Dir{root: root, indexed: true} }

// NewDirExact is a folder the dictionary shares with others: exact paths only.
func NewDirExact(root string) *Dir { return &Dir{root: root} }

// IsDir reports whether path is an existing directory.
func IsDir(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}

func (d *Dir) open(rel string) (io.ReadCloser, bool) {
	f, err := os.Open(filepath.Join(d.root, filepath.FromSlash(rel)))
	if err != nil {
		return nil, false
	}
	// A directory opens successfully and reads as garbage: reject it here
	// rather than serve it as a file.
	if st, err := f.Stat(); err != nil || st.IsDir() {
		f.Close()
		return nil, false
	}
	return f, true
}

func (d *Dir) Open(name string) (io.ReadCloser, error) {
	rel := clean(name)
	if rel == "" {
		return nil, dict.ErrNotFound
	}
	// The original spelling, not the lower-cased key: on a case-sensitive
	// filesystem "Kubok.jpg" is not "kubok.jpg".
	if f, ok := d.open(rel); ok {
		return f, nil
	}
	if !d.indexed {
		return nil, dict.ErrNotFound
	}
	d.once.Do(d.scan)
	if slot, ok := d.ix.Lookup(name); ok {
		if f, ok := d.open(d.rel[slot]); ok {
			return f, nil
		}
	}
	return nil, dict.ErrNotFound
}

func (d *Dir) List() []string {
	if !d.indexed {
		return nil
	}
	d.once.Do(d.scan)
	return d.ix.Names()
}

func (d *Dir) Close() error { return nil }

func (d *Dir) scan() {
	filepath.WalkDir(d.root, func(p string, de fs.DirEntry, err error) error {
		if err != nil {
			return nil // an unreadable subfolder skips itself, not the walk
		}
		if de.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(d.root, p)
		if err != nil {
			return nil
		}
		name := filepath.ToSlash(rel)
		// A filesystem name is bytes too: one extracted on Linux from a
		// code-page zip is not valid UTF-8, and gets the same readings a zip
		// entry would.
		d.ix.Add(name, utf8.ValidString(name))
		d.rel = append(d.rel, name)
		if d.ix.Len() >= maxScan {
			return filepath.SkipAll
		}
		return nil
	})
}
