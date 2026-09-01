// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Package resource matches the file name an article asks for against the
// names a container actually stores. They are the same file under two
// different strings more often than not:
//
//   - A zip written by a Windows archiver stores names in that machine's code
//     page and records nothing about which one. The UTF-8 flag is not the
//     answer: an ordinary all-ASCII zip leaves it clear on every entry too
//     (the 54,169-entry Espasa-Calpe .files.zip does), so a clear flag means
//     "unknown", not "cp1251". Read as UTF-8, "кубок.jpg" is not text at all.
//   - Case differs: articles reference loosely, containers preserve case.
//   - Unicode normalization differs: a filesystem may hand back the NFD
//     spelling of a name the dictionary wrote in NFC.
//   - The container may keep files in a folder ("files/кубок.jpg") that the
//     article, which names the file alone, knows nothing about.
//
// The article is the authority: its name is text in a known encoding. The
// container's name is bytes. So each stored entry is indexed under EVERY
// plausible reading of its bytes and the article's name selects one - no
// guess about the container's code page is made or needed, which is what
// makes this work for a language nobody anticipated. Guessing enters only in
// the display name (Resources, and through it media packing), where a wrong
// guess costs a duplicate blob and never a missing file.
package resource

import (
	"io"
	"mime"
	"path"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/unicode/norm"
)

// Source is one place a dictionary's resources live: an archive, a folder.
type Source interface {
	// Open returns the file's bytes, or dict.ErrNotFound.
	Open(name string) (io.ReadCloser, error)
	// List returns the stored names, in each container's own spelling.
	List() []string
	Close() error
}

// MIME is the content type implied by a resource name's extension.
func MIME(name string) string { return mime.TypeByExtension(path.Ext(name)) }

// Clean puts a name in the form used for both lookup and storage: forward
// slashes, no leading "/" or "./", and no path climbing out of the container.
// Exported for callers that WRITE a resource to disk (`wudict dump`), where
// "cannot climb out of the container" is the difference between unpacking an
// archive and obeying it.
func Clean(name string) string {
	s := strings.ReplaceAll(name, `\`, "/")
	s = path.Clean("/" + s)[1:] // rooted Clean: "../x" and "/../x" both fold to "x"
	if s == "." {
		return ""
	}
	return s
}

// Key is the canonical match key: cleaned, NFC, lower case. Case and
// normalization are folded here and nowhere else, so every container agrees
// on what "the same name" means.
func Key(name string) string {
	s := Clean(name)
	if s == "" {
		return ""
	}
	return strings.ToLower(norm.NFC.String(s))
}

// legacy lists the code pages a dictionary's file names are plausibly stored
// in. Every one is single-byte: the double-byte CJK tables (GBK, Shift-JIS,
// Big5, EUC-KR) would add megabytes to a binary that ships inside an APK,
// and are the case an archiver is most likely to have flagged UTF-8 anyway.
// Adding one is a one-line change here; nothing in the lookup path knows
// which encodings are in the list.
var legacy = []encoding.Encoding{
	charmap.Windows1251, // Russian and the other Cyrillic languages
	charmap.Windows1252, // Western European
	charmap.CodePage866, // Cyrillic DOS, still emitted by old archivers
	charmap.KOI8R,
	charmap.Windows1250, // Central European
	charmap.Windows1253, // Greek
	charmap.Windows1254, // Turkish
	charmap.Windows1255, // Hebrew
	charmap.Windows1256, // Arabic
	charmap.Windows1257, // Baltic
	charmap.Windows1258, // Vietnamese
	charmap.ISO8859_2,
	charmap.ISO8859_5,
	charmap.ISO8859_7,
	charmap.CodePage437, // DOS Western
	charmap.CodePage850,
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

// readings returns one stored name's display form and every key it may be
// matched by. utf8Declared is the container's own claim (a zip's flag, or a
// filesystem name that is valid UTF-8); it is trusted when the bytes agree
// with it, and never otherwise.
func readings(raw string, utf8Declared bool) (display string, keys []string) {
	keys = []string{Key(raw)}
	if isASCII(raw) {
		return raw, keys
	}
	valid := utf8.ValidString(raw)
	if utf8Declared && valid {
		return raw, keys
	}
	best, bestScore := "", 0
	for _, enc := range legacy {
		s, err := enc.NewDecoder().String(raw)
		// A byte with no character in that code page decodes to U+FFFD: the
		// reading is wrong, and keeping it would only add a key nothing asks
		// for.
		if err != nil || s == raw || strings.ContainsRune(s, utf8.RuneError) {
			continue
		}
		if k := Key(s); k != "" && !slices.Contains(keys, k) {
			keys = append(keys, k)
		}
		if sc := score(s); sc > bestScore {
			best, bestScore = s, sc
		}
	}
	// Bytes that are valid UTF-8 ARE the name; the extra keys above only
	// cover the archiver that wrote a code page and never said so.
	if valid || best == "" {
		return raw, keys
	}
	return best, keys
}

// score ranks a candidate reading by how much it looks like a name a person
// typed, and is used only to choose the one to show. Mojibake is not random,
// which is what makes this decidable: reading Cyrillic cp1251 bytes as cp1252
// gives a word of nothing but accented Latin ("ÊÓÁÎÊ"), while a genuinely
// accented name mixes its diacritics into ASCII ("café"). So the letters of
// each word are judged together - an all-non-ASCII word is expected to be
// another script, a mixed word is expected to be Latin - rather than one
// rune at a time, which would have to choose between "café" and "cafй" by
// coin toss.
func score(s string) int {
	total := 0
	var ascii, latin, other int
	flush := func() {
		switch {
		case ascii+latin+other == 0:
			return
		case latin+other == 0: // plain ASCII word
			total += ascii
		case ascii > 0: // mixed: diacritics on Latin text
			total += ascii + 2*latin - 2*other
		default: // a word written entirely outside ASCII
			total += 2*other - 2*latin
		}
		ascii, latin, other = 0, 0, 0
	}
	for _, r := range s {
		switch {
		case unicode.IsLetter(r) && r < utf8.RuneSelf:
			ascii++
			continue
		case unicode.IsLetter(r) && unicode.Is(unicode.Latin, r):
			latin++
			continue
		case unicode.IsLetter(r):
			other++
			continue
		}
		flush()
		switch {
		case r < utf8.RuneSelf && (unicode.IsDigit(r) || strings.ContainsRune(" ._-()[]{}'’,;&+#!~@%", r)):
			total++
		case r < utf8.RuneSelf && unicode.IsPrint(r):
		case unicode.IsDigit(r), unicode.IsSpace(r):
		default:
			// A control character, a box-drawing glyph or a stray currency
			// sign: no file name has one, every wrong code page produces them.
			total -= 4
		}
	}
	flush()
	return total
}

// Index maps every plausible spelling of a stored name to its slot. Slots are
// handed out in Add order, so a container can keep its own entries in a slice
// and index into it with what Lookup returns.
type Index struct {
	slot  map[string]int
	base  map[string]int  // basename -> slot, for names stored under a folder
	dupe  map[string]bool // basenames that name more than one file
	names []string
}

// Add records one stored name and returns its slot.
func (ix *Index) Add(raw string, utf8Declared bool) int {
	if ix.slot == nil {
		ix.slot = map[string]int{}
		ix.base = map[string]int{}
		ix.dupe = map[string]bool{}
	}
	display, keys := readings(raw, utf8Declared)
	slot := len(ix.names)
	ix.names = append(ix.names, display)
	for _, k := range keys {
		if k == "" {
			continue
		}
		if _, taken := ix.slot[k]; !taken {
			ix.slot[k] = slot
		}
		b := path.Base(k)
		if prev, seen := ix.base[b]; seen {
			if prev != slot {
				ix.dupe[b] = true
			}
			continue
		}
		ix.base[b] = slot
	}
	return slot
}

// Lookup resolves an article's name to a slot. The full path is tried first;
// a bare file name then matches a file stored inside a folder, but only when
// exactly one file in the container carries that name - a zip made by
// compressing the .files folder itself puts everything one level down, and
// the articles still refer to the files by name alone.
func (ix *Index) Lookup(name string) (int, bool) {
	k := Key(name)
	if k == "" {
		return 0, false
	}
	if slot, ok := ix.slot[k]; ok {
		return slot, true
	}
	b := path.Base(k)
	if ix.dupe[b] {
		return 0, false
	}
	slot, ok := ix.base[b]
	return slot, ok
}

// Names returns the stored names, slot order.
func (ix *Index) Names() []string { return ix.names }

// Len returns how many entries are indexed.
func (ix *Index) Len() int { return len(ix.names) }
