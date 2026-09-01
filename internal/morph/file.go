// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package morph

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/aaaton/golem/v4"

	"github.com/wuweidict/wudict/internal/lang"
)

// Lemma data from a folder (LEMMA_DIR, D87).
//
// This is how wudict gets every language but English. Compiling them in was
// the wrong shape: the six golem packs were 9 MB of an 11 MB binary, charged
// to every user and every phone whether or not they read those languages, and
// the list of languages people keep dictionaries in does not end at six. A
// folder of files is what hunspell does, and it lets someone who wants Polish
// have Polish without anyone else carrying it.
//
// English stays built in because it is the language assumed when a dictionary
// declares none, so it has to answer with an empty folder.
//
// The format is golem's own, because golem is the parser: one line per lemma,
// fields separated by tabs, the lemma first and its forms after it, lower
// case. That is exactly what `make lemma-files` writes out of golem's own
// packs, and it is also exactly the shape of the raw lists at
// github.com/michmech/lemmatization-lists (`lemma<TAB>form`, one pair per
// line, repeated) - those load unchanged, they are merely larger than the
// grouped form.
//
// An en file overrides the built-in English rather than merging with it.
// Merging two lemma sets means deciding which one wins per word, and there is
// no principled answer; "the file you put there is the one you get" has one.

// MaxPackBytes is what one lemma file may decompress to. It is a memory bound,
// not a file-size preference: golem costs 6-10x its text in heap once it has
// built the map (Russian is 11.5 MB of text and 65 MB resident), so a gzip
// bomb pointed at this folder would otherwise be an OOM with no diagnostic.
// 64 MB is five times the largest language golem publishes and comfortably
// above a raw michmech list, so nothing legitimate meets it.
const MaxPackBytes = 64 << 20

// filePack is a golem.LanguagePack backed by a file instead of a generated Go
// const. golem takes an interface, so this is the whole of the mechanism -
// GetLocale is used only in its error messages.
type filePack struct {
	code string
	path string
}

func (p filePack) GetLocale() string { return p.code }

func (p filePack) GetResource() ([]byte, error) {
	f, err := os.Open(p.path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var r io.Reader = f
	if strings.HasSuffix(strings.ToLower(p.path), ".gz") {
		zr, err := gzip.NewReader(f)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p.path, err)
		}
		defer zr.Close()
		r = zr
	}
	b, err := io.ReadAll(io.LimitReader(r, MaxPackBytes+1))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", p.path, err)
	}
	if len(b) > MaxPackBytes {
		return nil, fmt.Errorf("%s: lemma data larger than %d MB", p.path, MaxPackBytes>>20)
	}
	return normalize(b), nil
}

// normalize makes the bytes safe to hand to golem, which is stricter than the
// files it will be given: ONE line with fewer than two tab fields fails the
// entire load, so a stray blank-ish line at the end of a hand-edited file
// would silently cost a user their whole language. Bad lines are dropped
// instead - a lemma list is data, and the answer to one broken row in it is
// not to discard the other four hundred thousand.
//
// It also does the two things the format requires and a hand-written file
// cannot be trusted to have done: lower-casing (Cache.Lemma only ever calls
// LemmaLower, so an upper-case key is a key that can never match) and CRLF /
// BOM removal (a Windows editor leaves both, and golem splits on "\n" alone,
// so every line would end in a form with an invisible "\r" glued to it).
func normalize(b []byte) []byte {
	b = bytes.TrimPrefix(b, []byte("\xef\xbb\xbf"))
	b = bytes.ToLower(b)

	out := make([]byte, 0, len(b))
	for len(b) > 0 {
		line := b
		if i := bytes.IndexByte(b, '\n'); i >= 0 {
			line, b = b[:i], b[i+1:]
		} else {
			b = nil
		}
		out = appendFields(out, line)
	}
	return out
}

// appendFields writes line to dst as tab-separated non-empty fields, and
// writes nothing at all when fewer than two survive. Empty fields are dropped
// rather than kept: golem indexes every field, so "word\t\tform" would map ""
// to a lemma, and a repeated separator in a hand-edited file should not put a
// nameless entry in the table.
func appendFields(dst, line []byte) []byte {
	start, n := len(dst), 0
	for len(line) > 0 {
		f := line
		if i := bytes.IndexByte(line, '\t'); i >= 0 {
			f, line = line[:i], line[i+1:]
		} else {
			line = nil
		}
		// TrimSpace, not a split: a multi-word form keeps the space inside it,
		// and loses the "\r" or the stray indent around it.
		if f = bytes.TrimSpace(f); len(f) == 0 {
			continue
		}
		if n > 0 {
			dst = append(dst, '\t')
		}
		dst = append(dst, f...)
		n++
	}
	if n < 2 {
		return dst[:start]
	}
	return append(dst, '\n')
}

// lemmaExts are the names a lemma file may have. Extensions are matched, not
// content sniffed, so an unrelated file in the folder is skipped rather than
// read; ".gz" may follow either.
var lemmaExts = []string{".txt", ".tsv"}

// scanDir indexes the folder ONCE, at Cache construction. Not per lookup:
// Supports is called for every dictionary in a failed search, and answering it
// with a stat() storm would put the filesystem on the path of a query that has
// already found nothing.
//
// The stem names the language the way anything else in wudict does - through
// internal/lang - so "pl.txt", "pol.txt" and "polish.tsv.gz" are the same
// file to us and a user does not have to guess which spelling we wanted. A
// stem that names no language is ignored; a folder that does not exist is not
// an error, because not having one is the normal case.
func scanDir(dir string) map[string]string {
	if dir == "" {
		return nil
	}
	ents, err := os.ReadDir(dir) // sorted, so a collision resolves the same way twice
	if err != nil {
		return nil
	}
	var out map[string]string
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		code, ok := LemmaFile(e.Name())
		if !ok {
			continue
		}
		if _, dup := out[code]; dup {
			continue // "pl.txt" and "polish.txt" both present: first by name wins
		}
		if out == nil {
			out = map[string]string{}
		}
		out[code] = filepath.Join(dir, e.Name())
	}
	return out
}

// LemmaFile reports the language a file in LEMMA_DIR supplies, from its name
// alone. It is exported because the naming rule has to be single-sourced: the
// installer (internal/lemmas) has to agree with the lemmatizer about what
// "ru.tsv.gz" and "polish.txt" mean, and two copies of that rule would diverge
// the first time either grew an extension.
func LemmaFile(name string) (string, bool) {
	stem, ok := lemmaStem(name)
	if !ok {
		return "", false
	}
	code := lang.Normalize(stem)
	return code, code != ""
}

func lemmaStem(name string) (string, bool) {
	s := strings.TrimSuffix(strings.ToLower(name), ".gz")
	for _, ext := range lemmaExts {
		if stem := strings.TrimSuffix(s, ext); stem != s {
			return stem, stem != ""
		}
	}
	return "", false
}

// pack resolves a language to the data it will be loaded from, disk first -
// which for everything but English is the only place it can come from. Returns
// nil for a language with neither, which is the same answer Supports gives and
// the reason Lemma never reaches here for one.
func (c *Cache) pack(code string) golem.LanguagePack {
	if path, ok := c.installed()[code]; ok {
		return filePack{code: code, path: path}
	}
	if mk, ok := packs[code]; ok {
		return mk()
	}
	return nil
}
