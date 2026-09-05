// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package dict

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// A dictionary is rarely one file. MDX carries its resources in a sibling
// .mdd (possibly several, numbered); StarDict is an .ifo naming an .idx and a
// .dict.dz beside it, with an optional res/ folder; DSL keeps abbreviations in
// a _abrv file and media in a .files.zip. Two callers need to know which files
// belong to one dictionary - the panel, to show what a dictionary is made of,
// and removal (D63), to delete a dictionary without leaving its resources
// behind or taking a neighbour's with it - so the knowledge lives here once,
// beside the format registry, rather than in either caller.
//
// The rule that keeps this safe is the SHARED STEM: companions are named after
// the main file, and two dictionaries cannot share a stem in one folder
// without colliding on that name themselves. The two exceptions are named
// below and are why SourceFiles is presented to the user before anything is
// deleted rather than trusted silently.

// stem returns the main file's path with its format suffix removed, which is
// what every companion is named after. ".dz" is stripped first, so "x.dsl.dz"
// and "x.dsl" both yield "x" - a compressed DSL's companions are named for the
// dictionary, not for the compression.
func stem(src string) string {
	s := src
	if strings.EqualFold(filepath.Ext(s), ".dz") {
		s = s[:len(s)-len(".dz")]
	}
	return strings.TrimSuffix(s, filepath.Ext(s))
}

// mainExt classifies a main file for the tables below: the longest registered
// spelling, so a compressed DSL is ".dsl.dz" and not ".dz".
func mainExt(src string) string {
	lower := strings.ToLower(src)
	for _, e := range []string{".dsl.dz", ".dsl", ".mdx", ".ifo", ".slob", ".bgl", ".zim"} {
		if strings.HasSuffix(lower, e) {
			return e
		}
	}
	return strings.ToLower(filepath.Ext(src))
}

// CompanionMedia lists the files a dictionary keeps its images and audio in,
// alongside its main file. Used to decide whether "pack media" is worth
// offering and, once packed, what the packing came from.
func CompanionMedia(src string) []string {
	dir := filepath.Dir(src)
	base := stem(src)
	var out []string
	switch mainExt(src) {
	case ".mdx":
		for _, f := range []string{base + ".mdd", base + ".1.mdd"} {
			if fileExists(f) {
				out = append(out, f)
			}
		}
		// numbered parts run from .2.mdd upwards and stop at the first gap
		for n := 2; ; n++ {
			f := fmt.Sprintf("%s.%d.mdd", base, n)
			if !fileExists(f) {
				break
			}
			out = append(out, f)
		}
	case ".dsl", ".dsl.dz":
		// "x.dsl.files.zip" beside "x.dsl" - and beside "x.dsl.dz" too, where
		// the zip is named for the dictionary and not for the compression -
		// or the shorter "x.files.zip".
		uncompressed := strings.TrimSuffix(src, filepath.Ext(src)) // "x.dsl.dz" → "x.dsl"
		for _, f := range []string{src + ".files.zip", uncompressed + ".files.zip", base + ".files.zip"} {
			if fileExists(f) {
				out = append(out, f)
				break
			}
		}
	case ".ifo":
		// StarDict's media is a FOLDER, not a file named for the dictionary,
		// and a folder holding several .ifo files shares one res/ between all
		// of them. The list is consumed by removal (AllSources), so listing a
		// shared res/ would delete every sibling's images along with this
		// dictionary. Claim it only when this is the sole dictionary there;
		// otherwise it is not ours to hand over.
		if soleIfoInDir(dir) {
			if d := filepath.Join(dir, "res"); dirExists(d) {
				out = append(out, d)
			}
			if z := filepath.Join(dir, "res.zip"); fileExists(z) {
				out = append(out, z)
			}
		}
	}
	return out
}

// indexCompanions are the non-media files a format needs beside its main file:
// StarDict's index and article blob, DSL's abbreviations. Suffixes are
// appended to the STEM, and a candidate equal to the main file is skipped.
func indexCompanions(src string) []string {
	switch mainExt(src) {
	case ".ifo":
		return []string{".idx", ".idx.gz", ".idx.oft", ".dict", ".dict.dz", ".syn", ".syn.dz", ".ann"}
	case ".dsl", ".dsl.dz":
		return []string{"_abrv.dsl", "_abrv.dsl.dz", ".ann", ".dsl.ann"}
	}
	// MDX resources are media (.mdd, handled above); slob and bgl are single
	// files that carry everything inside them.
	return nil
}

// abbrevSuffixes are the spellings of DSL's abbreviation glossary, longest
// first so a ".dsl.dz" companion is recognised before the ".dsl" inside its
// name. Appended to the STEM, exactly like every other companion here.
var abbrevSuffixes = []string{"_abrv.dsl.dz", "_abrv.dsl"}

// AbbrevCompanion returns the abbreviation glossary belonging to the DSL main
// file src, if one exists beside it. A `_abrv.dsl` is not a dictionary: Lingvo
// writes it as the expansion map for the [p] labels of its parent, which is why
// the parent absorbs it at ingest and it is not discovered on its own.
func AbbrevCompanion(src string) (string, bool) {
	switch mainExt(src) {
	case ".dsl", ".dsl.dz":
	default:
		return "", false
	}
	if abbrevParentStem(src) != "" {
		return "", false // a companion has no companion of its own
	}
	base := stem(src)
	for _, suf := range abbrevSuffixes {
		if p := base + suf; !strings.EqualFold(p, src) && fileExists(p) {
			return p, true
		}
	}
	return "", false
}

// IsAbbrevCompanion reports whether path is an abbreviation glossary that has a
// parent to belong to. The parent check is what keeps this safe: a lone
// "foo_abrv.dsl" in a folder of its own is somebody's real abbreviation
// dictionary and stays an ordinary one. Name and stat only - no bytes are read.
func IsAbbrevCompanion(path string) bool {
	base := abbrevParentStem(path)
	if base == "" {
		return false
	}
	return fileExists(base+".dsl") || fileExists(base+".dsl.dz")
}

// abbrevParentStem strips the companion suffix, yielding the path its parent
// would be named after. Empty when path is not spelled like a companion, or
// when nothing would be left in front of the suffix ("_abrv.dsl" alone).
func abbrevParentStem(path string) string {
	lower := strings.ToLower(path)
	for _, suf := range abbrevSuffixes {
		if !strings.HasSuffix(lower, suf) {
			continue
		}
		if len(filepath.Base(path)) == len(suf) {
			return "" // "_abrv.dsl" with nothing in front of it names no parent
		}
		return path[:len(path)-len(suf)]
	}
	return ""
}

// SourceFiles lists every file that makes up the dictionary whose main file is
// src - the main file first, then its index companions, then its media - and
// only those that exist right now. It is the answer to "what would be deleted
// if this dictionary's originals were removed" (D63), which is why the caller
// SHOWS this list before acting on it: two entries can be wider than one
// dictionary.
//
//   - StarDict's res/ folder is shared by convention with any other .ifo in
//     the same folder. Listed, because that is where this dictionary's
//     resources are, and never assumed to be exclusively ours.
//   - A hand-made folder where two dictionaries were given the same stem
//     under different formats would overlap. Nothing in any format's own
//     conventions produces that.
//
// The main file is included even if it has since disappeared, so a caller
// always knows what it asked about; every other entry is stat'd.
func SourceFiles(src string) []string {
	if src == "" {
		return nil
	}
	out := []string{src}
	seen := map[string]bool{strings.ToLower(src): true}
	add := func(p string) {
		if k := strings.ToLower(p); !seen[k] {
			seen[k] = true
			out = append(out, p)
		}
	}
	base := stem(src)
	for _, suf := range indexCompanions(src) {
		p := base + suf
		if !strings.EqualFold(p, src) && fileExists(p) {
			add(p)
		}
	}
	for _, p := range CompanionMedia(src) {
		add(p)
	}
	return out
}

// soleIfoInDir reports whether dir holds exactly one StarDict .ifo. An
// unreadable directory answers false: the safe side of this question is the
// one that leaves the user's files alone.
func soleIfoInDir(dir string) bool {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	n := 0
	for _, e := range ents {
		if !e.IsDir() && strings.EqualFold(filepath.Ext(e.Name()), ".ifo") {
			if n++; n > 1 {
				return false
			}
		}
	}
	return n == 1
}

func fileExists(p string) bool {
	if p == "" {
		return false
	}
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

func dirExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}
