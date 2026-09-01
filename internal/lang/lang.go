// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Package lang resolves the language a dictionary is indexed in, so the search
// path knows which lemmatizer (if any) may speak for it.
//
// Four sources, in descending order of authority, and the caller applies them
// in that order:
//
//  1. what the dictionary itself declares - Lingvo's #INDEX_LANGUAGE, Babylon's
//     source language code (FromDeclared);
//  2. a language code or English language name at the START of the file name;
//  3. an ancestor folder whose whole name is a language code or English name;
//  4. the dictionary's own TITLE - "Dahl's Russian Dictionary", "(Ru-Ru)"
//     (FromTitle).
//
// (2) and (3) are FromPath, and both are deliberately strict. A hint at that
// level is a convention the user opts into by naming things -
// "en-es-apresyan.mdx", "Spanish/collins.mdx" - so a name either says a
// language in the first token or it says nothing, because a rule that fires
// halfway through a stem fires on "sale", "german-english-for-italians" and
// every other string that happens to contain a language word.
//
// (4) is free text and so cannot be read that way; it has its own rules, and
// they are looser by exactly as much as a title is more informative than a file
// name. See FromTitle. It is LAST because the title is the one label the user
// did not choose: a file can be renamed and a folder created, but a title is
// whatever its builder typed.
//
// The empty string means undecided, and is a real answer. What the caller does
// with it is the caller's business (internal/server falls back to English, the
// one language it will assume without evidence).
package lang

import (
	"path/filepath"
	"sort"
	"strings"
)

var (
	codeToISO = map[string]string{} // "en", "eng", "deu", "ger" -> "en"
	nameToISO = map[string]string{} // "english" -> "en"
	isoToName = map[string]string{} // "en" -> "English"
	namesDesc []string              // lower-case English names, longest first
)

func init() {
	for _, l := range languages {
		codeToISO[l.code2] = l.code2
		for _, c := range l.code3 {
			codeToISO[c] = l.code2
		}
		if len(l.names) > 0 {
			isoToName[l.code2] = l.names[0] // the first is the one to show
		}
		for _, n := range l.names {
			low := strings.ToLower(n)
			nameToISO[low] = l.code2
			namesDesc = append(namesDesc, low)
		}
	}
	// Longest first, so "Malayalam" is never read as "Malay" and
	// "Norwegian Bokmal" is never read as "Norwegian".
	sort.Slice(namesDesc, func(i, j int) bool {
		if len(namesDesc[i]) != len(namesDesc[j]) {
			return len(namesDesc[i]) > len(namesDesc[j])
		}
		return namesDesc[i] < namesDesc[j]
	})
}

// Normalize maps one token - a 2- or 3-letter ISO code, or an English language
// name - to its ISO 639-1 code. Case-insensitive. "" when the token names no
// language in the table.
//
// English names only: "Spanish" and "spanish" resolve, "Espanol" does not. A
// dictionary's own idea of its language is written in its own language, and
// accepting every endonym would mean accepting "Deutsch", "Nederlands" and
// several hundred others whose spellings collide with ordinary words.
func Normalize(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return ""
	}
	if c, ok := codeToISO[s]; ok {
		return c
	}
	return nameToISO[s]
}

// Name is the English name of a language, for showing a code back to a user
// ("pl" -> "Polish"). The code itself when the table has no name for it, so a
// caller can print the result unconditionally and never show an empty column.
func Name(code string) string {
	if n, ok := isoToName[Normalize(code)]; ok {
		return n
	}
	return code
}

// FromDeclared reads a value a dictionary format declares. Exact first, then
// longest English name that PREFIXES the value: Lingvo writes collation names
// in the same field ("SpanishModernSort", "GermanNewSpelling"), and the
// language is the part in front.
//
// Babylon's positional table is passed through the same function, and its
// group entries ("Other Russian languages", "Other Eastern-European
// languages") resolve to nothing on both paths - which is the wanted answer:
// "some Cyrillic language" is not an attribution.
func FromDeclared(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	if c := Normalize(v); c != "" {
		return c
	}
	low := strings.ToLower(v)
	for _, n := range namesDesc {
		if strings.HasPrefix(low, n) {
			return nameToISO[n]
		}
	}
	return ""
}

// FromPath is the naming-convention fallback for a dictionary that declares
// nothing: the file name's leading token, then the ancestor folders.
//
// roots are the configured dictionary directories. The upward walk stops at
// the root that contains path, so an accident of where the collection lives -
// /Users/is/dicts, /mnt/no/data - can never decide a language. When path lies
// under none of them (or none are configured) only the immediate parent folder
// is consulted, which bounds the same risk without needing a root at all.
func FromPath(path string, roots []string) string {
	if path == "" {
		return ""
	}
	if c := fromStem(filepath.Base(path)); c != "" {
		return c
	}
	return fromAncestors(path, roots)
}

// trimExt strips the format extension, and the compression extension in front
// of it: "dict.dsl.dz" is one dictionary named "dict", not a file named
// "dict.dsl". Only one format extension is removed, so "en.oxford.mdx" keeps
// the dot that separates its two name tokens.
func trimExt(base string) string {
	for range 2 {
		ext := filepath.Ext(base)
		if ext == "" {
			return base
		}
		base = base[:len(base)-len(ext)]
		switch strings.ToLower(ext) {
		case ".dz", ".gz", ".zip", ".bz2", ".xz":
			continue // compression: the real extension is underneath
		}
		return base
	}
	return base
}

// fromStem reads the FIRST token of a file (or library folder) name.
// "en-es-apresyan.mdx" and "eng_eng_apresyan.mdx" give English;
// "russian-apresyan.mdx" gives Russian; "oxford-en.mdx" gives nothing,
// because a language at the end is a target, a topic or a coincidence.
func fromStem(base string) string {
	stem := trimExt(base)
	if i := strings.IndexAny(stem, "-_. "); i >= 0 {
		stem = stem[:i]
	}
	return Normalize(stem)
}

func fromAncestors(path string, roots []string) string {
	dir := absClean(filepath.Dir(path))
	stops := make(map[string]bool, len(roots))
	for _, r := range roots {
		if r != "" {
			stops[absClean(r)] = true
		}
	}
	if !under(dir, stops) {
		return Normalize(filepath.Base(dir))
	}
	for {
		// The root folder itself is included: a user who points wudict at
		// ~/dicts/Spanish named that folder on purpose. Its PARENT is not.
		if c := Normalize(filepath.Base(dir)); c != "" {
			return c
		}
		if stops[dir] {
			return ""
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func absClean(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return filepath.Clean(p)
}

func under(dir string, stops map[string]bool) bool {
	for d := range stops {
		if dir == d || strings.HasPrefix(dir, d+string(filepath.Separator)) {
			return true
		}
	}
	return false
}
