// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/wuweidict/wudict/internal/dict"
)

// Removing a prepared dictionary (D63). Until now the library could only grow
// from inside the app: `clean` deletes orphans, the panel's switches drop an
// index or a media.db, and everything else was left to the file manager. That
// delegation has no counterparty on Android, where the library lives in
// app-private external storage that no file manager may open — so the app is
// the only process on the device that can free those bytes.
//
// This is the whole file-deleting surface of that feature, kept in one place
// and behind one guard: **the folder's parent must be the library root**.
// Everything directly under the db dir is ours by construction (D20: one
// dictionary is one folder), and nothing else may be passed here — the caller
// resolves a dictionary to a folder, this refuses anything that is not one.

// RemovePrepared deletes one prepared dictionary's folder and reports the
// bytes it freed.
//
// Guards, in order: the path must resolve to a direct child of the library
// root; it must be a real directory, not a symlink (which would let a link
// planted in the library delete a tree outside it); and it must not be the
// library root itself. The folder's contents are not inspected — a folder with
// no readable text.db is exactly the incomplete/interrupted case `clean`
// reports, and refusing to remove it would leave the one thing a user most
// wants gone.
func RemovePrepared(dir string) (int64, error) {
	if dir == "" {
		return 0, fmt.Errorf("no folder given")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return 0, err
	}
	abs = filepath.Clean(abs)

	root := DefaultDBDir()
	if dict.SameDir(abs, root) {
		return 0, fmt.Errorf("%s is the library itself, not a dictionary in it", abs)
	}
	// Compared canonically on the PARENT: the folder is about to be deleted,
	// so resolving symlinks through it is both pointless and racy, while its
	// parent must genuinely be the library however it was spelled (~, a
	// symlinked home, a case-variant volume).
	if !dict.SameDir(filepath.Dir(abs), root) {
		return 0, fmt.Errorf("%s is not in the library (%s)", abs, root)
	}
	// Lstat, not Stat: a symlink named like a dictionary folder must be
	// rejected rather than followed, because os.RemoveAll on the link would
	// delete the link but a caller could not tell that from success — and the
	// library never contains one.
	fi, err := os.Lstat(abs)
	if err != nil {
		return 0, err
	}
	if !fi.IsDir() {
		return 0, fmt.Errorf("%s is not a prepared dictionary folder", abs)
	}

	n := TreeSize(abs)
	if err := os.RemoveAll(abs); err != nil {
		return 0, fmt.Errorf("removing %s: %w", abs, err)
	}
	return n, nil
}

// TreeSize sums the regular files under path (path itself, if it is a file).
// Unreadable subtrees are skipped: this figure is reported to a user as "space
// freed", so an incomplete answer is better than no answer.
func TreeSize(path string) int64 {
	fi, err := os.Lstat(path)
	if err != nil {
		return 0
	}
	if !fi.IsDir() {
		return fi.Size()
	}
	var n int64
	_ = filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, err := d.Info(); err == nil && info.Mode().IsRegular() {
			n += info.Size()
		}
		return nil
	})
	return n
}
