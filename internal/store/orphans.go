// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/wuweidict/wudict/internal/basedir"
)

// Orphaned prepared dictionaries (D156).
//
// A prepared folder whose source has vanished is, for most users, not a copy
// they want: they keep their original dictionaries (other apps read those
// too), treat the library as a cache, and a folder they deleted by hand would
// leave gigabytes behind that nothing offers to remove. So an orphan is simply
// this: a healthy prepared folder whose
// recorded source is not on disk. No guessing about why - an unplugged drive
// and a deleted folder look the same, and the user, not the app, decides.
//
// Nothing here deletes on its own. The orphans are OFFERED (Rescan folders in
// the panel, `wudict clean -orphans`), and the user's two answers are recorded
// in the library itself:
//
//   - delete: RemoveOrphan, through RemovePrepared's single guard;
//   - keep:   `keep = standalone` in the folder's info.txt, after which the
//     folder is never offered again. Removing a dictionary's files from the
//     app while keeping its prepared data (D63 "dictionary files only") writes
//     the same line, because that act IS the decision to keep it standalone.
//
// A source that was only MOVED is not an orphan at all: Relink re-points the
// folder at it before anyone is asked.

// keepKey is the info.txt line that marks a folder as deliberately standalone.
const (
	keepKey   = "keep"
	keepValue = "standalone"
)

// Orphan is one prepared dictionary whose source file is no longer on disk.
// Folder - the folder's name inside the library - is the handle a caller sends
// back; it is never a path, so a request cannot name anything outside the
// library.
type Orphan struct {
	Dir    string `json:"-"`
	Folder string `json:"folder"`
	Name   string `json:"name"`
	Source string `json:"source"`
	Size   int64  `json:"size"`
}

// sourceGone reports whether a recorded source is known to be absent. Only a
// definite "does not exist" counts: an empty or relative claim cannot be
// judged, and a permission error means the file is there.
func sourceGone(src string) bool {
	if src == "" || !filepath.IsAbs(src) {
		return false
	}
	_, err := os.Stat(src)
	return errors.Is(err, fs.ErrNotExist)
}

// isKept reports whether a folder carries the keep marker.
func isKept(dir string) bool {
	info, err := readInfo(InfoPath(dir))
	return err == nil && info[keepKey] != ""
}

// FindOrphans lists every orphan in the library, in folder-name order. skip
// excludes the app's own dictionaries (the built-in guide), whose source the
// app writes and restores itself; nil skips nothing.
func FindOrphans(skip func(src string) bool) ([]Orphan, error) {
	folders, err := Folders()
	if err != nil {
		return nil, err
	}
	var out []Orphan
	for _, f := range folders {
		if o, ok := orphanOf(f, skip); ok {
			out = append(out, o)
		}
	}
	return out, nil
}

// orphanOf judges one library folder.
func orphanOf(f Folder, skip func(string) bool) (Orphan, bool) {
	if !sourceGone(f.Source) || (skip != nil && skip(f.Source)) || isKept(f.Dir) {
		return Orphan{}, false
	}
	o := Orphan{Dir: f.Dir, Folder: filepath.Base(f.Dir), Source: f.Source, Size: TreeSize(f.Dir)}
	if meta, ok := receiptMeta(f.Dir, TextDBPath(f.Dir)); ok {
		o.Name = meta["name"]
	} else if meta, err := ReadMeta(TextDBPath(f.Dir)); err == nil {
		o.Name = meta["name"]
	}
	if o.Name == "" {
		o.Name = o.Folder
	}
	return o, true
}

// OrphanNamed resolves a folder name received from a caller and re-judges it
// now: the list the user answered was computed earlier, and in between the
// source may have come back, been relinked, or the folder been kept.
func OrphanNamed(folder string, skip func(string) bool) (Orphan, error) {
	if folder == "" || folder == "." || folder == ".." ||
		strings.ContainsAny(folder, `/\`+"\x00") || filepath.Base(folder) != folder {
		return Orphan{}, fmt.Errorf("%q is not a library folder name", folder)
	}
	dir := filepath.Join(DefaultDBDir(), folder)
	owner, hasDB, exists := dirOwner(dir)
	if !exists || !hasDB {
		return Orphan{}, fmt.Errorf("%q is not a prepared dictionary", folder)
	}
	o, ok := orphanOf(Folder{Dir: dir, Source: owner}, skip)
	if !ok {
		return Orphan{}, fmt.Errorf("%q is no longer an orphan - its source is back, or it is kept", folder)
	}
	return o, nil
}

// RemoveOrphan deletes one orphan's prepared folder, re-validated at the
// moment of deletion, and returns the bytes freed.
func RemoveOrphan(folder string, skip func(string) bool) (int64, error) {
	o, err := OrphanNamed(folder, skip)
	if err != nil {
		return 0, err
	}
	return RemovePrepared(o.Dir)
}

// KeepOrphan marks one orphan as deliberately standalone.
func KeepOrphan(folder string, skip func(string) bool) error {
	o, err := OrphanNamed(folder, skip)
	if err != nil {
		return err
	}
	return MarkKept(o.Dir)
}

// MarkKept writes the keep marker into a prepared folder's info.txt. The
// line is appended rather than regenerated, so a receipt this build cannot
// rewrite (an unreadable text.db) still takes it; WriteInfo carries it across
// every later regeneration. Idempotent.
func MarkKept(dir string) error {
	path := InfoPath(dir)
	info, err := readInfo(path)
	if err == nil && info[keepKey] != "" {
		return nil
	}
	if err != nil {
		// No receipt at all: write the full one first, so the ownership claim
		// (taken from the meta) is not lost to a file holding only this line.
		if werr := WriteInfo(dir); werr != nil {
			return werr
		}
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, werr := fmt.Fprintf(f, "\n%s%s = %s\n", keepComment, keepKey, keepValue)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	return werr
}

// keepComment explains the marker to a person reading the receipt.
const keepComment = "# kept on purpose: never offered for deletion when its source is gone\n" +
	"# (delete the next line to be asked again)\n"

// Relinked is one library folder re-pointed at its source's new location.
type Relinked struct {
	Dir  string
	From string
	To   string
}

// Relink re-points library folders whose source has disappeared at a
// discovered file that is demonstrably the same one, moved: the same file
// name, the same size, and the same first MiB (or, for a folder that recorded
// no hash, the same modification time). Only the ownership claim in info.txt
// changes; the databases are not touched, so nothing is re-indexed.
//
// A discovered file that already has a prepared folder of its own is left
// alone - then the old folder is a genuine orphan, and is offered as one. One
// file relinks at most one folder, and one folder at most one file, first in
// discovery order.
//
// The file name must match because a folder's name derives from it
// (candidateDirs): a renamed file would never find the folder again, however
// its claim read.
func Relink(discovered []string) []Relinked {
	folders, err := Folders()
	if err != nil || len(folders) == 0 {
		return nil
	}
	byBase := map[string][]Folder{}
	for _, f := range folders {
		if sourceGone(f.Source) {
			b := filepath.Base(f.Source)
			byBase[b] = append(byBase[b], f)
		}
	}
	if len(byBase) == 0 {
		return nil
	}
	var out []Relinked
	for _, p := range discovered {
		base := filepath.Base(p)
		cands := byBase[base]
		if len(cands) == 0 || IsTextDB(p) || !filepath.IsAbs(p) {
			continue
		}
		if _, ok := LookupDir(p); ok {
			continue
		}
		st, err := os.Stat(p)
		if err != nil || st.IsDir() {
			continue
		}
		hash := "" // computed at most once per file, and only if a size matches
		for i, f := range cands {
			meta, err := ReadMeta(TextDBPath(f.Dir))
			if err != nil || !sameContent(meta, st, p, &hash) {
				continue
			}
			if err := writeInfo(f.Dir, p); err != nil {
				continue
			}
			out = append(out, Relinked{Dir: f.Dir, From: f.Source, To: p})
			byBase[base] = append(cands[:i:i], cands[i+1:]...)
			break
		}
	}
	return out
}

// Repoint re-points the library folders prepared from files under the folder
// from at the same files under to, where that folder has just moved as a
// whole: wudict's own files moving into the XDG layout (internal/basedir) take
// the howto with them, and it has been prepared like any dictionary. Unlike
// Relink nothing needs proving - the move is known - and the content may have
// changed since (a new build rewrites the howto): the folder then re-indexes
// in place, as for any edited source. Only the claim in info.txt changes.
func Repoint(from, to string) []Relinked {
	folders, err := Folders()
	if err != nil {
		return nil
	}
	var out []Relinked
	for _, f := range folders {
		if f.Source == "" || !basedir.Within(f.Source, from) {
			continue
		}
		rel, err := filepath.Rel(from, f.Source)
		if err != nil {
			continue
		}
		p := filepath.Join(to, rel)
		if err := writeInfo(f.Dir, p); err != nil {
			continue
		}
		out = append(out, Relinked{Dir: f.Dir, From: f.Source, To: p})
	}
	return out
}

// sameContent reports whether the file at p is the one a text.db was
// prepared from, by what the meta recorded about it. A missing size proves
// nothing, so it never matches.
func sameContent(meta map[string]string, st os.FileInfo, p string, hash *string) bool {
	n, err := strconv.ParseInt(meta["source_size"], 10, 64)
	if err != nil || n != st.Size() {
		return false
	}
	if want := meta["source_sha256_1M"]; want != "" {
		if *hash == "" {
			*hash = sourceHash(p)
		}
		return *hash == want
	}
	t, err := time.Parse(time.RFC3339, meta["source_mtime"])
	return err == nil && t.Equal(st.ModTime().UTC().Truncate(time.Second))
}
