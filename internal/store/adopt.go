// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// hashSuffix matches the content hash the pre-folder layout appended to a
// database name: `<slug>-<hash8>.text.db`.
var hashSuffix = regexp.MustCompile(`-[0-9a-f]{8}$`)

// Adopted is one database moved into the folder layout.
type Adopted struct {
	From string // the loose file
	Dir  string // the folder it now lives in
}

// AdoptLoose brings databases written by the pre-folder flat layout into the
// current one — a `<slug>-<hash8>.text.db` (with its `.media.db`, if any)
// becomes `<name>/{text.db, media.db, info.txt}`.
//
// It is a **rename**, never a re-index: the data is already prepared, and
// refusing to use it would force the user to prepare the same dictionary
// twice. Nothing is deleted, and nothing is overwritten — if the dictionary
// has meanwhile been prepared into a folder of its own, the loose file is left
// exactly where it is (FindOrphans then reports it as superseded, which is the
// one case where deleting it loses nothing).
//
// Called once at server startup; safe to call again (already-adopted
// libraries have no loose files left, so it is a single directory read).
func AdoptLoose() ([]Adopted, error) {
	root := DefaultDBDir()
	des, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Adopted
	var firstErr error
	for _, de := range des {
		name := de.Name()
		if de.IsDir() || !strings.HasSuffix(strings.ToLower(name), ".text.db") {
			continue
		}
		textDB := filepath.Join(root, name)
		dir, err := adoptOne(textDB)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if dir != "" {
			out = append(out, Adopted{From: textDB, Dir: dir})
		}
	}
	return out, firstErr
}

// adoptOne moves one loose text.db (and its media sibling) into a folder,
// returning the folder ("" when the file was deliberately left in place).
func adoptOne(textDB string) (string, error) {
	// the folder a fresh ingest of this dictionary would use, so adopting and
	// re-preparing converge on the same place
	src, _ := ReadMetaValue(textDB, "source_path")
	var dir string
	var err error
	if src != "" {
		dir, err = ClaimDir(src)
	} else {
		// no recorded source: name the folder after the file, minus the old
		// content-hash suffix (spa-cat-index-7d9963cd → "spa-cat-index").
		base := hashSuffix.ReplaceAllString(strings.TrimSuffix(filepath.Base(textDB), ".text.db"), "")
		if base == "" {
			base = "dictionary"
		}
		root := DefaultDBDir()
		cands := []string{filepath.Join(root, base)}
		for i := 2; i < maxCandidates; i++ {
			cands = append(cands, filepath.Join(root, fmt.Sprintf("%s (%d)", base, i)))
		}
		dir, err = claimFrom(cands, "")
	}
	if err != nil {
		return "", err
	}
	if fileExists(TextDBPath(dir)) {
		// this dictionary already has a prepared folder: leave the loose copy
		// alone rather than overwrite either of them.
		return "", nil
	}
	if err := os.Rename(textDB, TextDBPath(dir)); err != nil {
		return "", err
	}
	if media := strings.TrimSuffix(textDB, ".text.db") + ".media.db"; fileExists(media) {
		if err := os.Rename(media, MediaDBPath(dir)); err != nil {
			return dir, err
		}
	}
	_ = WriteInfo(dir) // receipt is derived; a failure must not undo the move
	return dir, nil
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}
