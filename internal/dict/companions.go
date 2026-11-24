// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package dict

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/wuweidict/wudict/internal/fsx"
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

// Stem returns the main file's path with its format suffix removed, which is
// what every companion is named after. ".dz" is stripped first, so "x.dsl.dz"
// and "x.dsl" both yield "x" - a compressed DSL's companions are named for the
// dictionary, not for the compression.
//
// Exported because the format packages resolve the same companions at READ
// time that this package resolves at removal time, and the two answering
// differently is how a dictionary comes to list media it will never serve.
func Stem(src string) string {
	s := src
	switch ext := strings.ToLower(filepath.Ext(s)); {
	case ext == ".dz", ext == ".gz" && strings.HasSuffix(strings.ToLower(s), ".wudict.md.gz"):
		s = s[:len(s)-len(ext)]
	}
	return strings.TrimSuffix(s, filepath.Ext(s))
}

// Name is a dictionary's name as its file spells it: the base name without
// its format suffix - the longest one in mainExts, so "x.dsl.dz" and
// "x.wudict.md.gz" are both "x". A name with no such suffix is kept whole, so
// a library folder named "Pocket.Encyclopedia" keeps its last word. Unlike
// Stem, which is what companions are named after ("x.wudict" for
// "x.wudict.md"), this is the name a person reads: the library folder, the
// file-name hints for language and language pair.
func Name(path string) string {
	base := filepath.Base(path)
	if n, ok := trimMainExt(base); ok {
		return n
	}
	// a compressed spelling the list does not name ("notes.md.gz")
	for _, z := range []string{".gz", ".dz"} {
		if len(base) > len(z) && strings.EqualFold(base[len(base)-len(z):], z) {
			if n, ok := trimMainExt(base[:len(base)-len(z)]); ok {
				return n
			}
		}
	}
	return base
}

func trimMainExt(base string) (string, bool) {
	lower := strings.ToLower(base)
	for _, e := range mainExts {
		if strings.HasSuffix(lower, e) {
			return base[:len(base)-len(e)], true
		}
	}
	return base, false
}

// mainExts are those spellings, longest first so ".dsl.dz" is answered before
// the ".dsl" inside it. One list, read twice: MainExt matches a name against
// it, and classify.go walks it to build the companion vocabulary, so a format
// that grows a companion table cannot be forgotten by the other reader.
var mainExts = []string{".wudict.md.gz", ".wudict.md.dz", ".wudict.md", ".dsl.dz", ".dsl", ".mdx", ".ifo", ".slob", ".bgl", ".zimaa", ".zim", ".md"}

// MainExt names the format of a main file for the tables below: the longest
// spelling this file has companion knowledge about, so a compressed DSL is
// ".dsl.dz" and not ".dz".
//
// Deliberately its own list rather than the format registry's. The registry
// answers "what can this build open", which depends on which format packages
// were linked in; this answers "which formats does THIS FILE hold companion
// tables for", a closed set that must read the same in a test binary that has
// linked no format at all. ClassifyName is the one that asks the registry.
func MainExt(src string) string {
	lower := strings.ToLower(src)
	for _, e := range mainExts {
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
	base := Stem(src)
	var out []string
	switch MainExt(src) {
	case ".mdx":
		for _, f := range []string{base + ".mdd", base + ".1.mdd"} {
			if fsx.FileExists(f) {
				out = append(out, f)
			}
		}
		// numbered parts run from .2.mdd upwards and stop at the first gap
		for n := 2; ; n++ {
			f := fmt.Sprintf("%s.%d.mdd", base, n)
			if !fsx.FileExists(f) {
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
			if fsx.FileExists(f) {
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
			if d := filepath.Join(dir, "res"); fsx.DirExists(d) {
				out = append(out, d)
			}
			if z := filepath.Join(dir, "res.zip"); fsx.FileExists(z) {
				out = append(out, z)
			}
		}
	case ".wudict.md", ".wudict.md.gz", ".wudict.md.dz", ".md":
		// "x.wudict.files/" or "x.wudict.files.zip" beside "x.wudict.md"
		// (the Stem keeps ".wudict"); a plain "x.md" uses "x.files".
		if z := base + ".files.zip"; fsx.FileExists(z) {
			out = append(out, z)
		}
		if d := base + ".files"; fsx.DirExists(d) {
			out = append(out, d)
		}
	}
	return out
}

// CompanionSuffixes are the non-media files a format needs beside its main
// file: StarDict's index and article blob, DSL's abbreviations. Suffixes are
// appended to the STEM, and a candidate equal to the main file is skipped.
//
// Takes the extension rather than the path because the two callers hold
// different things: SourceFiles has a path on disk, the archive sniffer has an
// entry name and no disk at all. One table, both readings.
func CompanionSuffixes(mainExt string) []string {
	switch mainExt {
	case ".ifo":
		// Every spelling the format reader accepts, or removal leaves the
		// orphan behind and the panel under-reports what a dictionary is: the
		// index and the synonyms each exist plain, gzipped or dictzipped.
		return []string{
			".idx", ".idx.gz", ".idx.dz", ".idx.oft",
			".dict", ".dict.dz",
			".syn", ".syn.gz", ".syn.dz",
			".ann",
		}
	case ".dsl", ".dsl.dz":
		return []string{"_abrv.dsl", "_abrv.dsl.dz", ".ann", ".dsl.ann"}
	case ".mdx":
		// A repacked MDX ships its stylesheet and scripts LOOSE beside the
		// .mdx rather than packed in the .mdd - LDOCE6.css and entry.js are
		// the canonical pair - and mdx.looseFile serves them from there at
		// read time. So they are companions in the only sense this file
		// means: without them the articles render as unstyled text, and an
		// import or a removal that ignored them would be wrong about what the
		// dictionary is made of. The .mdd resources are media and live in
		// MediaSuffixes.
		return []string{".css", ".js"}
	}
	// slob, bgl and an unsplit zim are single files that carry everything
	// inside them. A split zim's parts are not suffixes of this table: there
	// are 675 of them, and a name is matched against them by pattern
	// (zimPartLen) rather than by a scan of every one.
	return nil
}

// zimPartSuffixes are the names of a split ZIM's parts after the first:
// ".zimab" to ".zimzz", the `split` naming Kiwix uses to fit an archive on
// FAT32 (4 GiB per file). The first part, ".zimaa", is the main file. Every
// part is required - they are one archive cut into pieces - and the run ends at
// the first missing name.
var zimPartSuffixes = func() []string {
	var out []string
	for a := 'a'; a <= 'z'; a++ {
		for b := 'a'; b <= 'z'; b++ {
			if a != 'a' || b != 'a' {
				out = append(out, ".zim"+string(a)+string(b))
			}
		}
	}
	return out
}()

// zimPartLen is the length of the split-ZIM part suffix lower ends with -
// ".zimab" to ".zimzz", 6 - or 0. The first part, ".zimaa", is a main file.
func zimPartLen(lower string) int {
	n := len(lower)
	if n < 6 || lower[n-6:n-2] != ".zim" || lower[n-2:] == "aa" {
		return 0
	}
	if a, b := lower[n-2], lower[n-1]; a < 'a' || a > 'z' || b < 'a' || b > 'z' {
		return 0
	}
	return 6
}

// ZIMPartSuffixes is zimPartSuffixes in order, for a caller asking about
// names it cannot stat (intake's probe of a download location). Not to be
// modified.
func ZIMPartSuffixes() []string { return zimPartSuffixes }

// ZIMParts lists the files of a split ZIM whose first part is src, in order
// and src first, up to the first missing part; nil when src is not a first
// part. The parts are spelled in the case of src's own suffix, so
// "X.ZIMAA" is followed by "X.ZIMAB" on a case-sensitive file system.
func ZIMParts(src string) []string {
	if MainExt(src) != ".zimaa" {
		return nil
	}
	ext := src[len(src)-len(".zimaa"):]
	upper := ext == strings.ToUpper(ext)
	base := src[:len(src)-len(ext)]
	out := []string{src}
	for _, suf := range zimPartSuffixes {
		if upper {
			suf = strings.ToUpper(suf)
		}
		p := base + suf
		if !fsx.FileExists(p) {
			break
		}
		out = append(out, p)
	}
	return out
}

// MediaSuffixes are the STEM suffixes a format keeps its images and audio
// under. The stat-based CompanionMedia resolves rather more than this - MDX's
// numbered parts, DSL's three spellings of the same zip, StarDict's shared
// res/ folder, all of which need a directory to look at - so this is the
// name-only half: what a media file is CALLED, for a caller holding a list of
// names and nothing to stat.
func MediaSuffixes(mainExt string) []string {
	switch mainExt {
	case ".mdx":
		// covers ".mdd" and every ".<n>.mdd" part, which end in it
		return []string{".mdd"}
	case ".dsl", ".dsl.dz", ".wudict.md", ".wudict.md.gz", ".wudict.md.dz", ".md":
		return []string{".files.zip"}
	}
	return nil
}

// fontExts are the font files a dictionary can be given (D204), in the order
// CompanionFont tries them: the four kinds a browser loads with @font-face.
// Collections (.ttc/.otc) are not among them - @font-face has no way to say
// which face of a collection it means.
//
// Not in CompanionSuffixes: that table is also intake's list of names to ask a
// download server about (intake/probe.go), and a font is never worth four more
// requests per import. The name-only vocabulary (classify.go) lists them
// itself.
var fontExts = []string{".ttf", ".otf", ".woff2", ".woff"}

// folderFontStem is the name of a font that serves every dictionary in its
// folder: "font.ttf". Named for the folder, like StarDict's res.zip, so it
// belongs to no one dictionary.
const folderFontStem = "font"

// CompanionFont is the font a dictionary whose main file is src is shown in:
// the file named after its stem ("oxford.ttf" beside "oxford.mdx" or
// "oxford.dsl.dz"), else the folder's "font.ttf". folder reports the second.
// "" when there is neither.
//
// Each extension is tried in lower case and then upper case, first hit wins, so
// a dictionary with no font costs 16 stats. Callers ask when a dictionary's
// row is derived, not per request (server/dictrows.go).
func CompanionFont(src string) (path string, folder bool) {
	if src == "" {
		return "", false
	}
	stem := Stem(src)
	// A dictionary that is itself called "font" has the folder's font as its
	// own name; it is still the folder's, and shared.
	shared := strings.EqualFold(filepath.Base(stem), folderFontStem)
	if p := firstFont(stem); p != "" {
		return p, shared
	}
	if shared {
		return "", false
	}
	if p := firstFont(filepath.Join(filepath.Dir(src), folderFontStem)); p != "" {
		return p, true
	}
	return "", false
}

// fontSpellings is fontExts as firstFont tries them: each in lower case, then
// upper case. Built once, so a probe allocates only its path.
var fontSpellings = func() []string {
	out := make([]string, 0, 2*len(fontExts))
	for _, e := range fontExts {
		out = append(out, e, strings.ToUpper(e))
	}
	return out
}()

func firstFont(base string) string {
	for _, e := range fontSpellings {
		if p := base + e; fsx.FileExists(p) {
			return p
		}
	}
	return ""
}

// fontExtLen is the length of the font extension lower ends with, 0 for none.
func fontExtLen(lower string) int {
	for _, e := range fontExts {
		if strings.HasSuffix(lower, e) {
			return len(e)
		}
	}
	return 0
}

// RequiredCompanions are the companions without which a main file is not a
// working dictionary, as groups of alternative spellings: each group must be
// satisfied by at least one member. A StarDict .ifo is a text header naming an
// index and an article blob that it does not contain, so an .ifo arriving
// alone is an incomplete dictionary and not a small one - the only format here
// where that is true. Every other format's main file carries its own articles.
//
// Used by intake to refuse a broken import rather than install it and let the
// failure surface later as a dictionary that opens and answers nothing.
func RequiredCompanions(mainExt string) [][]string {
	if mainExt == ".ifo" {
		return [][]string{
			{".idx", ".idx.gz", ".idx.dz"},
			{".dict", ".dict.dz"},
		}
	}
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
	switch MainExt(src) {
	case ".dsl", ".dsl.dz":
	default:
		return "", false
	}
	if abbrevParentStem(src) != "" {
		return "", false // a companion has no companion of its own
	}
	base := Stem(src)
	for _, suf := range abbrevSuffixes {
		if p := base + suf; !strings.EqualFold(p, src) && fsx.FileExists(p) {
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
	return fsx.FileExists(base+".dsl") || fsx.FileExists(base+".dsl.dz")
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
	if parts := ZIMParts(src); parts != nil {
		for _, p := range parts[1:] {
			add(p)
		}
	} else {
		base := Stem(src)
		for _, suf := range CompanionSuffixes(MainExt(src)) {
			p := base + suf
			if !strings.EqualFold(p, src) && fsx.FileExists(p) {
				add(p)
			}
		}
	}
	for _, p := range CompanionMedia(src) {
		add(p)
	}
	// The dictionary's own font only. A folder's "font.ttf" is every
	// dictionary's there, and removing one must not take it from the rest.
	if p, folder := CompanionFont(src); p != "" && !folder {
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
