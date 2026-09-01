// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"encoding/csv"
	"errors"
	"flag"
	"fmt"
	"html"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/wuweidict/wudict/internal/dict"
	"github.com/wuweidict/wudict/internal/logx"
	"github.com/wuweidict/wudict/internal/resource"
	"github.com/wuweidict/wudict/internal/store"
)

// `wudict dump` writes one dictionary out as pyglossary's CSV: a two-column
// file whose leading rows are "#key","value" metadata and whose remaining rows
// are word, definition and - when an entry has aliases - a third column of
// alternates joined by commas. Resources go beside it in "<file>.csv_res",
// which is the directory name pyglossary's CSV writer derives and its reader
// looks for, so a dump round-trips through `pyglossary dict.csv out.xyz`
// without being told anything about it.
//
// The point is an exit door: every dictionary wudict can read becomes a format
// forty other tools can read, using their converter rather than a matrix of
// writers here.

func cmdDump(args []string) error {
	fs := flag.NewFlagSet("dump", flag.ExitOnError)
	var out string
	fs.StringVar(&out, "o", "", "output folder for <name>.csv and its <name>.csv_res resources (created if missing)")
	fs.StringVar(&out, "output", "", "long form of -o")
	fs.Parse(args)
	if fs.NArg() != 1 || out == "" {
		return fmt.Errorf("usage: wudict dump -o <outdir> <dictfile>")
	}
	applyLibrarySettings()
	src := fs.Arg(0)

	base := dumpBase(src)
	if base == "" {
		return fmt.Errorf("cannot derive an output name from %q: rename the file, or dump it from its library folder", src)
	}
	csvPath := filepath.Join(out, base+".csv")
	resDir := csvPath + "_res" // pyglossary: the csv's own name plus "_res"

	// Said before anything is written, because after it is too late to be a
	// warning. Named files, not a general caution: "will be overwritten" is
	// only useful if you can tell whether it means yours.
	if dirExists(out) {
		fmt.Fprintf(os.Stderr, "%s already exists - %s and anything already in %s/ under the same name will be overwritten\n",
			out, filepath.Base(csvPath), filepath.Base(resDir))
	}
	n, err := dumpEntries(src, out, csvPath)
	if err != nil {
		return err
	}
	size, _ := fileSize(csvPath)
	fmt.Printf("%s → %s (%s)\n", plural(n, "entry", "entries"), csvPath, humanSize(size))

	files, bytes, err := dumpResources(src, resDir)
	if err != nil {
		return err
	}
	if files > 0 {
		fmt.Printf("%s → %s (%s)\n", plural(files, "resource", "resources"), resDir, humanSize(bytes))
	}
	return nil
}

// dumpBase names the output after the SOURCE FILE rather than after the
// dictionary's own title: the title is free text that may be empty, may repeat
// across a collection, and routinely holds characters no filesystem accepts.
// A prepared dictionary is a folder whose file is always called text.db, so
// there the folder is the name - dumping `~/.wudict/db/Oxford/text.db` writes
// Oxford.csv, not text.csv.
func dumpBase(src string) string {
	if store.IsTextDB(src) {
		b := filepath.Base(src)
		if strings.EqualFold(b, store.TextDBName) {
			return safeName(filepath.Base(filepath.Dir(src)))
		}
		return safeName(b[:len(b)-len(".text.db")])
	}
	return store.FolderName(src)
}

// dumpEntries writes the CSV. The ingest Reader is preferred wherever a format
// has one: it is a single sequential pass that yields each entry's aliases with
// it, where the query interface would have to be walked headword by headword
// and would report an alias as another copy of its entry. A prepared
// dictionary has no Reader - it IS the index - and is streamed from its own
// tables instead.
//
// The source is opened BEFORE the output folder is created, so a dictionary
// that cannot be read leaves nothing behind.
func dumpEntries(src, outDir, csvPath string) (int, error) {
	var meta dict.Meta
	var each func(row func([]string, string) error) error
	var closeSrc func() error

	if store.IsTextDB(src) {
		s, err := store.Open(src)
		if err != nil {
			return 0, err
		}
		meta, closeSrc = s.Meta(), s.Close
		each = func(row func([]string, string) error) error {
			return s.EachEntry(func(headword string, alts []string, body string) error {
				return row(append([]string{headword}, alts...), body)
			})
		}
	} else {
		r, err := dict.OpenReader(src)
		if err != nil {
			return 0, err
		}
		meta, closeSrc = r.Meta(), r.Close
		each = func(row func([]string, string) error) error { return readAll(r, row) }
	}
	defer closeSrc()

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return 0, err
	}
	f, err := os.Create(csvPath)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	w := csv.NewWriter(f) // buffered internally

	n := 0
	row := func(words []string, body string) error {
		if len(words) == 0 || words[0] == "" {
			return nil // a row pyglossary would drop: never written
		}
		rec := []string{words[0], body}
		if len(words) > 1 {
			// Comma-joined regardless of the delimiter, which is what
			// pyglossary's reader splits this column on.
			rec = append(rec, strings.Join(words[1:], ","))
		}
		n++
		return w.Write(rec)
	}

	if err := writeInfoRows(w, meta); err != nil {
		return n, err
	}
	if err := each(row); err != nil {
		return n, err
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return n, err
	}
	return n, f.Close()
}

// readAll drains a format Reader into row(), rendering each body exactly as an
// ingest would.
func readAll(r dict.Reader, row func([]string, string) error) error {
	for {
		e, err := r.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if e.LinkTo != "" {
			// A pure redirect has no body of its own. bword: is the
			// cross-reference scheme Babylon and GoldenDict use and every
			// importer understands, so the pointer survives the conversion
			// instead of becoming a dangling headword.
			esc := html.EscapeString(e.LinkTo)
			if err := row(e.Headwords, `<a href="bword://`+esc+`">`+esc+`</a>`); err != nil {
				return err
			}
			continue
		}
		body, err := store.NormalizeBody(e)
		if err != nil {
			return fmt.Errorf("%s: %w", firstWord(e.Headwords), err)
		}
		if err := row(e.Headwords, body); err != nil {
			return err
		}
	}
}

// writeInfoRows emits the "#key","value" header block. Only fields the
// dictionary actually carries are written: pyglossary reads these into the
// glossary's info, and an invented author or target language would be carried
// into every format converted from here.
func writeInfoRows(w *csv.Writer, m dict.Meta) error {
	info := [][2]string{{"name", m.Name}}
	if m.IndexLang != "" {
		// Headwords are the source side. Declared only - a language worked out
		// from a file name is the search path's guess, not the file's claim.
		info = append(info, [2]string{"sourceLang", m.IndexLang})
	}
	if m.Description != "" {
		info = append(info, [2]string{"description", m.Description})
	}
	for _, kv := range info {
		if kv[1] == "" {
			continue
		}
		if err := w.Write([]string{"#" + kv[0], kv[1]}); err != nil {
			return err
		}
	}
	return nil
}

// dumpResources unpacks every resource the dictionary holds into resDir,
// preserving the folder structure the names carry so that an article's
// `src="audio/x.mp3"` still resolves after the conversion.
//
// A resource that cannot be read is reported and skipped rather than ending
// the dump: the names come from a container that may be truncated or lying,
// and 40,000 good files are not worth losing to one bad record.
func dumpResources(src, resDir string) (files int, written int64, err error) {
	d, err := dict.Open(src)
	if err != nil {
		return 0, 0, err
	}
	defer d.Close()
	var names []string
	switch t := d.(type) {
	case *store.Store:
		names = t.MediaNames() // packed media.db, if this library folder has one
	default:
		if l, ok := d.(dict.ResourceLister); ok {
			names = l.Resources()
		}
	}
	names = resource.Filter(names) // a dump is for humans; .DS_Store is not
	if len(names) == 0 {
		return 0, 0, nil
	}
	if err := os.MkdirAll(resDir, 0o755); err != nil {
		return 0, 0, err
	}
	var failed int
	for i, name := range names {
		if i%256 == 0 {
			logx.Progress("  %d/%d resources", i, len(names))
		}
		n, err := dumpOneResource(d, resDir, name)
		if err != nil {
			logx.ClearLine()
			fmt.Fprintf(os.Stderr, "  %s: %v\n", name, err)
			failed++
			continue
		}
		files++
		written += n
	}
	logx.ClearLine()
	if failed > 0 {
		fmt.Fprintf(os.Stderr, "  %d of %d resources could not be extracted\n", failed, len(names))
	}
	return files, written, nil
}

func dumpOneResource(d dict.Dictionary, resDir, name string) (int64, error) {
	dest := resFilePath(resDir, name)
	if dest == "" {
		return 0, fmt.Errorf("no usable file name")
	}
	rc, _, err := d.Resource(name)
	if err != nil {
		return 0, err
	}
	defer rc.Close()
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return 0, err
	}
	f, err := os.Create(dest)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(f, rc)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return n, err
}

// resFilePath maps a stored resource name to a path inside resDir.
//
// The name comes from the DICTIONARY, and a container can hold "..", an
// absolute path, a drive letter or a NUL. resource.Clean removes the climbing
// and the leading slash; safeName then takes each remaining component, so
// nothing this writes can land outside resDir. The structure BELOW resDir is
// kept, unlike `wudict res` which flattens to a basename: there the user asked
// for one file in a place they named, here the tree is the point - the HTML
// being dumped alongside references these paths.
func resFilePath(resDir, name string) string {
	rel := resource.Clean(name)
	if rel == "" {
		return ""
	}
	parts := strings.Split(rel, "/")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if s := safeName(p); s != "" {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return ""
	}
	return filepath.Join(append([]string{resDir}, out...)...)
}

// safeName makes one path component legal on every host wudict runs on, the
// same rules store.FolderName applies to a library folder - minus its length
// cap, which would silently merge two resources into one file.
func safeName(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r < 0x20, r == 0x7f:
			// control characters (NUL included): drop
		case strings.ContainsRune(`/\:*?"<>|`, r):
			b.WriteByte('-')
		default:
			b.WriteRune(r)
		}
	}
	// trailing dots and spaces are illegal in Windows names; "." and ".." are
	// gone with them, so no component can mean "the parent".
	return strings.TrimRight(strings.TrimSpace(b.String()), " .")
}

func firstWord(words []string) string {
	if len(words) == 0 {
		return "(no headword)"
	}
	return words[0]
}

func fileSize(path string) (int64, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return fi.Size(), nil
}
