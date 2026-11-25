// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package dict

import "strings"

// Name-only classification: "what is this file, judging by its name alone".
//
// companions.go answers the same family of questions with os.Stat, which is
// the right answer for files that are already in place and no answer at all
// for the caller this exists for. An archive's central directory is a list of
// NAMES; there is no directory to stat until something has been extracted, and
// extracting first to find out whether it was worth extracting is exactly the
// cost intake exists to avoid. So this is the pure-name layer, added beside
// the stat-based one rather than replacing it - the two agree because they
// read the same tables (CompanionSuffixes, MediaSuffixes, abbrevSuffixes).
//
// What it is NOT is a probe. A file named ".mdx" that holds a photograph
// classifies as a main file here and fails when it is opened, which is the
// correct division of labour: a name is cheap and a format header is not.

// Kind is what a name looks like it is.
type Kind int

const (
	// KindOther is anything a dictionary folder may legitimately contain and
	// intake has no use for: a readme, a licence, an archive.
	KindOther Kind = iota
	// KindMain is a file this build can open as a dictionary in its own right.
	KindMain
	// KindCompanion is a file that belongs to a main file - resources, an
	// index, an abbreviation glossary. Carried along with its dictionary,
	// never offered as one.
	KindCompanion
	// KindCandidate is a main file by name whose CONTENT decides (a
	// RegisterSniffed suffix: the markdown family, where most files are not
	// dictionaries). A caller holding the bytes settles it with Claims; one
	// that has only the name - a download not yet taken - may begin with it
	// and must settle it once the file is there.
	KindCandidate
)

// stardictResZip is StarDict's shared resource archive: a fixed NAME rather
// than a suffix of the dictionary's own stem, so the stem rule cannot
// recognise it (folderNamed). Its sibling "res/" folder is a
// directory and is matched as a path prefix by the caller, not here.
const stardictResZip = "res.zip"

// folderNamed reports the companions named for their folder rather than for a
// dictionary: StarDict's res.zip, the folder font ("font.ttf", CompanionFont)
// and the folder stylesheet ("custom.css", CompanionCSS). They belong to
// whatever dictionary is in the folder.
func folderNamed(lower string) bool {
	return lower == stardictResZip || folderWide(lower)
}

// AppliesToFolder reports whether name is a companion every dictionary in its
// folder uses: the folder's stylesheet and font ("custom.css", "font.ttf" …;
// CompanionCSS, CompanionFont). Intake gives each dictionary it installs from
// that folder its own copy, since each one lands in a folder of its own (D205).
// Not StarDict's res.zip, which is one dictionary's media, not everyone's.
func AppliesToFolder(name string) bool { return folderWide(strings.ToLower(entryBase(name))) }

func folderWide(lower string) bool {
	if lower == folderCSSStem+".css" {
		return true
	}
	n := fontExtLen(lower)
	return n > 0 && lower[:len(lower)-n] == folderFontStem
}

// ClassifyName reports what name looks like, reading no bytes and touching no
// filesystem. Accepts a bare name or a path in either separator; only the last
// element is considered.
//
// "Main" is decided by the format REGISTRY and not by the list in MainExt, so
// a build that did not link the zim package does not offer to install a .zim
// it cannot then open. Companions come from this package's own tables, which
// is why an .mdd is still recognised as baggage in a build without MDX: the
// mistake worth preventing is installing half a dictionary, not declining to
// carry a file.
//
// Bundle main files (the RegisterFileName registrations - a prepared library
// folder's "text.db") are deliberately NOT consulted. Such a folder is a main
// file plus two siblings named by neither stem nor suffix, so the stem rule
// below would group the wrong files and install a library folder stripped of
// its media. An archive of one yields no candidates and says so, which is the
// honest answer until intake learns the shape.
func ClassifyName(name string) Kind {
	base := strings.ToLower(entryBase(name))
	switch {
	case base == "" || base == "." || base == "..":
		return KindOther
	case strings.HasPrefix(base, "."):
		// Dotfiles, and with them the "._Oxford.mdx" AppleDouble twin that a
		// zip built on macOS carries beside every real entry - a 4 KiB
		// resource fork that classifies as a perfectly good MDX on its name.
		return KindOther
	case folderNamed(base):
		return KindCompanion
	}

	main := suffixLen(openers, base)
	cand := suffixLen(sniffedFormats, base)
	comp := suffixLen(inspectOpeners, base)
	if n := suffixLen(companionSuffixes, base); n > comp {
		comp = n
	}
	if n := zimPartLen(base); n > comp {
		comp = n
	}
	// "_abrv.dsl" with nothing in front of it names no parent, so it is
	// somebody's own abbreviation dictionary rather than a companion - the
	// name-only half of the check IsAbbrevCompanion makes with a stat.
	if comp > main && abbrevSuffixLen(base) > 0 && abbrevParentStem(base) == "" {
		comp = 0
	}

	switch {
	case comp > max(main, cand):
		// Longest suffix wins, so "x_abrv.dsl" is a companion although ".dsl"
		// also matches it, and "x.dict.dz" is one although nothing else does.
		return KindCompanion
	case main > 0:
		return KindMain
	case cand > 0:
		return KindCandidate
	}
	return KindOther
}

// companionSuffixes is every stem suffix any format's companion tables can
// produce, as one vocabulary for the longest-match test above. Built from the
// tables rather than restated, so it cannot fall behind them.
var companionSuffixes = func() map[string]bool {
	m := make(map[string]bool)
	for _, ext := range mainExts {
		for _, s := range CompanionSuffixes(ext) {
			m[s] = true
		}
		for _, s := range MediaSuffixes(ext) {
			m[s] = true
		}
	}
	// A dictionary's own font, whatever its format (CompanionFont).
	for _, s := range fontExts {
		m[s] = true
	}
	return m
}()

// suffixLen is matchKey's measurement without its lookup: the length of the
// longest key that lower ends with, 0 for none. Length rather than the key
// itself because the only thing three tables need to agree on is which of them
// matched MORE.
func suffixLen[T any](m map[string]T, lower string) int {
	best := 0
	for k := range m {
		if len(k) > best && strings.HasSuffix(lower, k) {
			best = len(k)
		}
	}
	return best
}

// abbrevSuffixLen reports whether lower is spelled like a DSL abbreviation
// glossary, and how long that spelling was.
func abbrevSuffixLen(lower string) int {
	for _, suf := range abbrevSuffixes {
		if strings.HasSuffix(lower, suf) {
			return len(suf)
		}
	}
	return 0
}

// entryBase is filepath.Base for a name that did not come from this operating
// system. An archive written on Windows holds "Dicts\\Oxford\\Oxford.mdx" and
// one written anywhere else holds it with slashes; a Go program reading either
// on Linux gets no help from filepath, which knows only its own separator, and
// would hand back the whole path as the "name". Both separators are treated as
// separators here, because on every platform that matters neither is a legal
// character in a file name.
func entryBase(name string) string {
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		return name[i+1:]
	}
	return name
}

// CompanionStem returns the stem of the main file a companion belongs to,
// from its name alone - the inverse of the "companions are named after the
// main file" rule that companions.go resolves with a stat. Empty when name is
// not spelled like a companion at all, and empty for StarDict's res.zip, which
// is named after the FOLDER and belongs to whatever dictionary is in it.
//
// The suffix is not always carried by the stem. Lingvo's media zip is written
// three ways - "x.dsl.files.zip", "x.dsl.dz.files.zip" and "x.files.zip" - and
// MDict numbers its resource parts "x.1.mdd", so what is left after the
// companion suffix comes off may still be the main file's name rather than its
// stem. Only a KNOWN main extension is taken off at that point: a dictionary
// honestly called "Collins.v2" has an .mdd called "Collins.v2.mdd", and
// trimming ".v2" as though it were an extension would file it under a
// dictionary that does not exist.
func CompanionStem(name string) string {
	base := entryBase(name)
	lower := strings.ToLower(base)
	if folderNamed(lower) {
		return ""
	}
	if n := abbrevSuffixLen(lower); n > 0 && len(base) > n {
		return base[:len(base)-n]
	}
	if n := zimPartLen(lower); n > 0 && len(base) > n {
		return base[:len(base)-n]
	}
	n := suffixLen(companionSuffixes, lower)
	if n == 0 || n >= len(base) {
		return ""
	}
	rest, suf := base[:len(base)-n], lower[len(lower)-n:]
	if suf == ".mdd" {
		rest = trimPartNumber(rest)
	}
	if e := knownMainExt(rest); e != "" {
		rest = rest[:len(rest)-len(e)]
	}
	return rest
}

// knownMainExt is MainExt without its filepath.Ext fallback: the empty string
// rather than a guess, for callers that must not treat a dot in a version
// number as a format.
func knownMainExt(name string) string {
	lower := strings.ToLower(name)
	for _, e := range mainExts {
		if len(name) > len(e) && strings.HasSuffix(lower, e) {
			return e
		}
	}
	return ""
}

// trimPartNumber removes the ".2" of "Oxford.2.mdd" - MDict's numbering of
// resource parts, which sits between the stem and the suffix. Digits only, so
// an "Oxford.v2.mdd" keeps the name its .mdx also has.
func trimPartNumber(name string) string {
	i := strings.LastIndexByte(name, '.')
	if i <= 0 || i == len(name)-1 {
		return name
	}
	for _, r := range name[i+1:] {
		if r < '0' || r > '9' {
			return name
		}
	}
	return name[:i]
}
