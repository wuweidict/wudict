// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package intake

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/wuweidict/wudict/internal/dict"
)

// A loose dictionary file, presented as a one-directory archive.
//
// The rest of this package is built around archives because that is how these
// formats are distributed - but it is not how they are always HANDED OVER. A
// link points at "oxford.mdx"; a share sheet carries one file; a download
// folder holds the .mdx and the .mdd side by side because the site offered
// them as two links. None of those is an archive, and all of them are a
// dictionary somebody is trying to install.
//
// Rather than a second pipeline for loose files, they enter the existing one
// through the Archive interface: a plainArchive lists the file the user named
// plus the siblings that belong to it, and everything downstream - the
// grouping rule, the completeness check, the "already installed" marking, the
// staging and the atomic rename - is the archive code, unchanged.
//
// The siblings are found by dict's own rule, the same one a folder scan uses:
// same directory, same stem. That is what makes an .mdx arrive with its .mdd
// instead of arriving as a dictionary with no images, and a StarDict .ifo
// arrive with the index without which it is a header describing nothing.

// maxPlainRes bounds the walk of a StarDict "res/" subtree. A resource folder
// is images and sounds and can honestly hold thousands; this is far above any
// of them and far below the point where walking is the cost.
const maxPlainRes = 20_000

// plainArchive is a real directory, filtered to one dictionary. Entry names
// are relative to that directory, so the candidate it produces is rooted
// exactly as one from a zip whose files sit at the archive root.
type plainArchive struct {
	dir     string
	entries []Entry
	// byName indexes entries for Open and realPaths: a StarDict resource
	// folder reaches maxPlainRes files, and a linear scan per requested file
	// turned a candidate's extraction into an O(files²) string comparison run.
	byName map[string]int // entry name -> position in entries
}

// OpenPlain presents the dictionary file at path, together with the files
// beside it that belong to the same dictionary, as an Archive.
//
// path may be the main file or one of its companions: a user who downloads
// "oxford.mdd" after "oxford.mdx" is handing over the SECOND half, and the
// dictionary that results is the same one either way. What it may not be is a
// file that belongs to no dictionary, which is refused here rather than
// reported as an archive holding nothing.
func OpenPlain(path string) (Archive, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	fi, err := os.Stat(abs)
	if err != nil || !fi.Mode().IsRegular() {
		return nil, ErrUnsupported
	}
	base := filepath.Base(abs)
	dir := filepath.Dir(abs)
	stem := plainStem(dir, base)
	if stem == "" {
		return nil, ErrUnsupported
	}

	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	a := &plainArchive{dir: dir, byName: map[string]int{}}
	// The index is rebuilt after the walk rather than kept in add(): the same
	// name can arrive from several passes, and entries only ever grows before
	// the archive is handed out.
	stardict := false
	for _, e := range ents {
		name := e.Name()
		// The dictionary's own files, and the folder's custom.css and font.ttf,
		// which it is shown with where it lies now (dict.AppliesToFolder).
		if e.IsDir() || !belongsTo(dir, name, stem) && !dict.AppliesToFolder(name) {
			continue
		}
		info, err := e.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		if dict.ClassifyName(name) == dict.KindMain && dict.MainExt(name) == ".ifo" {
			stardict = true
		}
		a.add(name, info.Size())
	}
	if len(a.entries) == 0 {
		// The file is gone, or it is a directory now. Either way there is
		// nothing here to describe.
		return nil, ErrUnsupported
	}
	if stardict {
		a.addStarDictResources()
	}
	if len(a.entries) > maxEntries {
		return nil, ErrTooManyEntries
	}
	// Indexed once, after every add: the passes above can hand the archive a
	// name more than once, and the map should answer with the last one.
	for i, e := range a.entries {
		a.byName[e.Name] = i
	}
	return a, nil
}

// plainStem is the dictionary a loose file in dir belongs to, by name - and,
// for a name whose content decides, by its head: its own stem when it is a
// main file, its parent's when it is a companion. Empty for anything else, and
// for StarDict's res.zip, which is named after a folder and so names no
// dictionary on its own.
func plainStem(dir, name string) string {
	switch kindOf(name, onDisk(filepath.Join(dir, name))) {
	case dict.KindMain:
		return dict.Stem(name)
	case dict.KindCompanion:
		return dict.CompanionStem(name)
	}
	return ""
}

// belongsTo reports whether a file in the same directory is part of the
// dictionary named by stem. Case-insensitive, for the reason Sniff gives about
// its own grouping: a bundle written on Windows spells "Oxford.MDX" beside
// "oxford.mdd", and a case-insensitive filesystem cannot tell the two stems
// apart in the first place.
func belongsTo(dir, name, stem string) bool {
	s := plainStem(dir, name)
	return s != "" && strings.EqualFold(s, stem)
}

// addStarDictResources adds the shared resources a StarDict folder keeps
// beside its dictionary - "res.zip", or a "res/" subtree - which are named
// after the folder rather than after any stem and so are invisible to the rule
// above.
//
// They are added only when the group HAS a StarDict main file, so a res.zip
// sitting in a download folder is never attached to an unrelated .mdx. Sniff
// applies its own rule on top: shared resources go to the directory's
// dictionary only when there is exactly one, which here there always is.
func (a *plainArchive) addStarDictResources() {
	if fi, err := os.Stat(filepath.Join(a.dir, "res.zip")); err == nil && fi.Mode().IsRegular() {
		a.add("res.zip", fi.Size())
	}
	root := filepath.Join(a.dir, "res")
	if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
		return
	}
	n := 0
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // an unreadable corner of a resource folder is not fatal
		}
		if d.IsDir() {
			return nil
		}
		rel, rerr := filepath.Rel(a.dir, p)
		if rerr != nil {
			return nil
		}
		name := filepath.ToSlash(rel)
		if !safeEntryName(name) {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil || !info.Mode().IsRegular() {
			return nil
		}
		a.add(name, info.Size())
		if n++; n >= maxPlainRes {
			return fs.SkipAll
		}
		return nil
	})
}

// add records one file. Compressed equals Size because nothing here is
// compressed: the ratio check downstream is then trivially satisfied, which is
// the truth about a file being copied rather than a hole in the check.
func (a *plainArchive) add(name string, size int64) {
	slash := filepath.ToSlash(name)
	a.entries = append(a.entries, Entry{
		Name:       slash,
		Dir:        path2dir(slash),
		Base:       filepath.Base(name),
		Size:       size,
		Compressed: size,
	})
}

// path2dir is the directory part of a slash-separated entry name, "" at the
// root - the shape Sniff groups on.
func path2dir(name string) string {
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		return name[:i]
	}
	return ""
}

func (a *plainArchive) Entries() []Entry { return a.entries }

// Open streams one of the listed files. The name is checked against the list
// rather than merely cleaned, so this cannot be steered at a file that was
// never offered - the same discipline the extractor applies to a name coming
// out of a zip directory.
func (a *plainArchive) Open(name string) (io.ReadCloser, error) {
	if _, ok := a.byName[name]; ok {
		return os.Open(filepath.Join(a.dir, filepath.FromSlash(name)))
	}
	return nil, errors.New("no such file: " + name)
}

// Close is nothing: this archive holds no handle between reads, which is the
// same one-open-entry discipline the compressed readers are held to.
func (a *plainArchive) Close() error { return nil }

// realPaths maps entry names back onto the files on disk. It is what lets the
// job dispose of the files it actually consumed - a loose import takes the
// .mdd as well as the .mdx, and "delete the source afterwards" that left the
// .mdd behind would be leaving half of what it took.
func (a *plainArchive) realPaths(names []string) []string {
	out := make([]string, 0, len(names))
	for _, n := range names {
		if _, ok := a.byName[n]; ok {
			out = append(out, filepath.Join(a.dir, filepath.FromSlash(n)))
		}
	}
	return out
}
