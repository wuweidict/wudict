// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package dsl

import (
	"io"
	"os"
	"strings"

	"github.com/wuweidict/wudict/internal/dict"
	"github.com/wuweidict/wudict/internal/logx"
)

// A Lingvo dictionary may ship its editorial blurb in a "<stem>.ann" sidecar
// rather than in the .dsl header: the publisher, the edition, the copyright,
// often in several languages at once. goldendict-ng reads it the same way
// (src/dict/dsl.cc:1008) - lazily, at display time, and NOT as an index
// dependency, which is exactly right: an annotation is display text, it is
// never part of an article, and a library must not go stale because someone
// edited it. So this is read live on every request and nothing is stored.
//
// Where goldendict picks ONE #LANGUAGE section by system locale, every section
// is kept here in file order. Picking by locale hides the Russian annotation of
// a Ru-Ru dictionary from a reader running an English UI, and the sections are
// short.

// An annotation is a paragraph or two. The cap is generous enough for the
// worst real one (LingvoUniversalEnRu.ann carries 698-character lines) and
// small enough that a file misnamed .ann cannot be pulled into a panel draw.
const maxAnnBytes = 256 << 10

func init() { dict.RegisterAbout("dsl", loadAnn) }

// annPath maps a DSL source path to its annotation sidecar: chop the .dsl /
// .dsl.dz suffix, append .ann - goldendict's rule. An _abrv companion needs no
// rule of its own: it is not a dictionary (D97), so nothing ever asks it for an
// About, and its _abrv.ann is therefore never read.
func annPath(srcPath string) (string, bool) {
	low := strings.ToLower(srcPath)
	switch {
	case strings.HasSuffix(low, ".dsl.dz"):
		return srcPath[:len(srcPath)-len(".dsl.dz")] + ".ann", true
	case strings.HasSuffix(low, ".dsl"):
		return srcPath[:len(srcPath)-len(".dsl")] + ".ann", true
	}
	return "", false
}

// loadAnn is the dict.AboutProvider for DSL. It never returns an error: a
// missing, unreadable, empty or absurd sidecar is simply "no annotation", and
// the server then falls back to the description the .dsl header declared.
// isLangLine reports whether a line is a #LANGUAGE directive rather than a
// word that merely starts with one. The tag is followed by whitespace, or by
// the quote that opens its value, or by nothing at all.
func isLangLine(line string) bool {
	const tag = "#LANGUAGE"
	if !strings.HasPrefix(line, tag) {
		return false
	}
	rest := line[len(tag):]
	if rest == "" {
		return true
	}
	switch rest[0] {
	case ' ', '\t', '"', '\'':
		return true
	}
	return false
}

func loadAnn(srcPath string) (dict.About, bool) {
	path, ok := annPath(srcPath)
	if !ok {
		return dict.About{}, false
	}
	// Two spellings, because a Windows-authored set beside a case-sensitive
	// filesystem is the one way an all-caps .ANN reaches a Linux box.
	f, err := os.Open(path)
	if err != nil {
		alt := path[:len(path)-len(".ann")] + ".ANN"
		if f, err = os.Open(alt); err != nil {
			return dict.About{}, false
		}
		path = alt
	}
	defer f.Close()

	// Sniffed, not derived from the name: a .ann is never called .dz, and the
	// two magic bytes cost nothing.
	var magic [2]byte
	n, _ := io.ReadFull(f, magic[:])
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return dict.About{}, false
	}
	sc, err := decodedScanner(f, path, n == 2 && magic[0] == 0x1f && magic[1] == 0x8b)
	if err != nil {
		logx.V("dsl annotation: %s: %v", path, err)
		return dict.About{}, false
	}

	var (
		secs  []dict.Section
		cur   *strings.Builder
		total int
		cut   bool
	)
	// The unlabelled section is created lazily, so a file that opens with
	// #LANGUAGE does not carry an empty block in front of its first language.
	open := func(lang string) {
		secs = append(secs, dict.Section{Lang: lang})
		cur = &strings.Builder{}
	}
	flush := func() {
		if cur != nil {
			secs[len(secs)-1].Text = strings.TrimSpace(cur.String())
		}
	}
	for !cut && sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if len(secs) == 0 {
			line = strings.TrimPrefix(line, "\uFEFF")
		}
		// A #LANGUAGE line opens a section only where goldendict looks for
		// one: as the very first line, or in a file whose first line already
		// was one. In a file that opens with prose, a later "#LANGUAGE" stays
		// prose - which is the case worth protecting, since an annotation is
		// prose and may well discuss languages. In a file that opens with
		// #LANGUAGE, every later one splits, exactly as goldendict reads it.
		//
		// The separator matters: without it "#LANGUAGES SUPPORTED" opened a
		// section named "SUPPORTED".
		if isLangLine(line) && (len(secs) == 0 || secs[0].Lang != "") {
			flush()
			open(strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, "#LANGUAGE")), `"'`))
			continue
		}
		if cur == nil {
			open("")
		}
		if total+len(line)+1 > maxAnnBytes {
			cur.WriteString("…")
			cut = true
			break
		}
		cur.WriteString(line)
		cur.WriteString("\n")
		total += len(line) + 1
	}
	flush()
	if err := sc.Err(); err != nil && len(secs) == 0 {
		logx.V("dsl annotation: %s: %v", path, err)
		return dict.About{}, false
	}
	// Drop sections that hold nothing but their heading; About.Empty then
	// rejects a file that was all headings.
	kept := secs[:0]
	for _, s := range secs {
		if s.Text != "" {
			kept = append(kept, s)
		}
	}
	a := dict.About{Sections: kept, Source: path}
	if a.Empty() {
		return dict.About{}, false
	}
	return a, true
}
