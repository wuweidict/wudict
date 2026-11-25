// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package intake

import (
	"errors"
	"io"
	"sort"
	"strings"

	"github.com/wuweidict/wudict/internal/dict"
)

// ErrTooManyEntries is an archive whose directory is larger than any bundle
// has cause to be. Reported rather than truncated: a partial answer to "what
// is in here" would be a list the user is invited to trust.
var ErrTooManyEntries = errors.New("archive holds too many files")

// Sniff lists an archive and reports the dictionaries in it. No byte is
// written, and the answer comes from names and the directory's declared sizes
// - except for a name whose content decides (dict.KindCandidate, the markdown
// family): the head of that one entry is read, a few KiB, since a README.md
// beside a dictionary must not be offered as one.
//
// The grouping rule is dict's, not one of our own: a candidate is one main
// file plus every companion sharing its stem IN THE SAME ARCHIVE DIRECTORY.
// The directory qualifier is what keeps two dictionaries in one archive from
// claiming each other's resources - and two that share a stem inside one
// directory could not have been packed there in the first place without
// colliding on their companions' names.
//
// Entries that are unsafe to write (absolute, climbing, too deeply nested) are
// dropped here rather than at extraction time, so a hostile name cannot even
// be offered to the user, let alone reached with a file handle.
func Sniff(a Archive) ([]Candidate, error) {
	entries := a.Entries()
	if len(entries) > maxEntries {
		return nil, ErrTooManyEntries
	}

	// group key: directory and lowercased stem. Lowercased because a bundle
	// packed on Windows routinely spells "Oxford.MDX" beside "oxford.mdd",
	// and two files that a case-insensitive filesystem cannot tell apart must
	// not be filed under two dictionaries.
	type group struct {
		main       *Entry
		companions []Entry
	}
	groups := map[string]*group{}
	key := func(dir, stem string) string { return dir + "\x00" + strings.ToLower(stem) }

	// res/ and res.zip are StarDict's, shared by everything in the folder that
	// holds them, and named after neither stem. Collected per directory and
	// handed out below only where there is exactly one dictionary to hand them
	// to - the archive reading of companions.go's soleIfoInDir, and for the
	// same reason: resources that belong to two dictionaries belong to
	// neither, and guessing costs the user the media of whichever it guessed.
	shared := map[string][]Entry{}
	// custom.css and font.ttf are the folder's on purpose: every dictionary
	// there is shown with them (dict.AppliesToFolder). Each dictionary is
	// installed into a folder of its own, so each one gets a copy - the one
	// way they keep looking as they did in the bundle.
	folderWide := map[string][]Entry{}

	for _, e := range entries {
		if !safeEntryName(e.Name) {
			continue
		}
		name := e.Name
		switch kindOf(e.Base, func() (io.ReadCloser, error) { return a.Open(name) }) {
		case dict.KindMain:
			k := key(e.Dir, dict.Stem(e.Base))
			g := groups[k]
			if g == nil {
				g = &group{}
				groups[k] = g
			}
			if g.main == nil {
				// First wins, and the list is walked in archive order, so an
				// archive holding the same dictionary twice resolves the same
				// way on every machine.
				m := e
				g.main = &m
			}
		case dict.KindCompanion:
			stem := dict.CompanionStem(e.Base)
			if stem == "" {
				// named for its folder: custom.css and font.ttf are every
				// dictionary's there; res.zip is the one StarDict's
				if dict.AppliesToFolder(e.Base) {
					folderWide[e.Dir] = append(folderWide[e.Dir], e)
				} else {
					shared[e.Dir] = append(shared[e.Dir], e)
				}
				continue
			}
			k := key(e.Dir, stem)
			g := groups[k]
			if g == nil {
				g = &group{}
				groups[k] = g
			}
			g.companions = append(g.companions, e)
		default:
			// A StarDict res/ subtree is ordinary image and sound files, so
			// they classify as nothing and are collected by their location.
			if d, ok := resSubtree(e.Dir); ok {
				shared[d] = append(shared[d], e)
			}
		}
	}

	// how many dictionaries each directory holds, for the shared-resource rule
	perDir := map[string]int{}
	for _, g := range groups {
		if g.main != nil {
			perDir[g.main.Dir]++
		}
	}

	out := make([]Candidate, 0, len(groups))
	for _, g := range groups {
		if g.main == nil {
			// companions with no main file in their directory: an archive of
			// somebody's resources, or the half of a split upload that did
			// not finish. Nothing to install, and nothing worth reporting as
			// a dictionary that is merely incomplete.
			continue
		}
		c := buildCandidate(*g.main, g.companions)
		for _, e := range folderWide[g.main.Dir] {
			c.Files = append(c.Files, e.Name)
			c.sizes = append(c.sizes, e.Size)
			c.Size += e.Size
		}
		if perDir[g.main.Dir] == 1 {
			for _, e := range shared[g.main.Dir] {
				c.Files = append(c.Files, e.Name)
				c.sizes = append(c.sizes, e.Size)
				c.Size += e.Size
			}
		}
		if c.Size > maxCandidateBytes() {
			continue
		}
		out = append(out, c)
	}
	// Stable, case-insensitive, and by location before name, so an archive of
	// a hundred dictionaries reads as its own folder listing.
	sort.Slice(out, func(i, j int) bool {
		a, b := strings.ToLower(out[i].Main), strings.ToLower(out[j].Main)
		return a < b
	})
	return out, nil
}

// buildCandidate assembles one dictionary from its main file and the
// companions that share its stem, and decides whether it is whole.
func buildCandidate(main Entry, companions []Entry) Candidate {
	ext := dict.MainExt(main.Base)
	c := Candidate{
		Name:   dict.Stem(main.Base),
		Format: ext,
		Main:   main.Name,
		Files:  []string{main.Name},
		sizes:  []int64{main.Size},
		Size:   main.Size,
	}
	// Archive order, so the same archive extracts in the same order twice.
	sort.SliceStable(companions, func(i, j int) bool { return companions[i].Name < companions[j].Name })
	have := make([]string, 0, len(companions))
	for _, e := range companions {
		if e.Name == main.Name {
			continue
		}
		c.Files = append(c.Files, e.Name)
		c.sizes = append(c.sizes, e.Size)
		c.Size += e.Size
		have = append(have, strings.ToLower(e.Base))
	}
	for _, group := range dict.RequiredCompanions(ext) {
		if !satisfied(group, have) {
			// the first spelling is the canonical one, and the one worth
			// naming to a user who is about to go and look for it
			c.Missing = append(c.Missing, group[0])
		}
	}
	return c
}

// satisfied reports whether any spelling in group appears among the companion
// base names collected for a candidate.
func satisfied(group, have []string) bool {
	for _, suf := range group {
		for _, name := range have {
			if strings.HasSuffix(name, suf) {
				return true
			}
		}
	}
	return false
}

// resSubtree reports the dictionary directory a StarDict "res/" path belongs
// to: "sd/res" and "sd/res/audio" both answer "sd".
func resSubtree(dir string) (string, bool) {
	if dir == "" {
		return "", false
	}
	parts := strings.Split(dir, "/")
	for i, p := range parts {
		if strings.EqualFold(p, "res") {
			return strings.Join(parts[:i], "/"), true
		}
	}
	return "", false
}

// safeEntryName is the whole traversal defence, applied before a name is used
// for anything at all - grouping included, so a hostile entry is never even
// listed to the user.
//
// It is a rejection test and not a cleaning pass, for the reason userfiles.go
// gives about its own name grammar: a name that has been "made safe" by
// rewriting is a name whose safety depends on the rewriter being exhaustive,
// whereas a name that was never allowed through needs no further argument. The
// canonical containment check at write time is the second line, not the first.
func safeEntryName(name string) bool {
	if name == "" || len(name) > 4096 {
		return false
	}
	if strings.ContainsRune(name, 0) {
		return false
	}
	if strings.HasPrefix(name, "/") {
		return false
	}
	// a Windows drive letter or UNC path, which filepath.IsAbs does not call
	// absolute when the program is running on Linux
	if len(name) > 1 && name[1] == ':' {
		return false
	}
	parts := strings.Split(name, "/")
	if len(parts) > maxDepth {
		return false
	}
	for _, p := range parts {
		if p == "" || p == "." || p == ".." {
			return false
		}
	}
	return true
}
