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
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/wuweidict/wudict/internal/dict"
	"github.com/wuweidict/wudict/internal/format/wmd"
	"github.com/wuweidict/wudict/internal/fsx"
	"github.com/wuweidict/wudict/internal/htmlref"
	"github.com/wuweidict/wudict/internal/logx"
	"github.com/wuweidict/wudict/internal/resource"
	"github.com/wuweidict/wudict/internal/store"
)

// `wudict dump` writes one dictionary out, as pyglossary's CSV (the default)
// or as WuWeiDict markdown (-format md, dump_md.go).
//
// The CSV is a two-column
// file whose leading rows are "#key","value" metadata and whose remaining rows
// are word, definition and - when an entry has aliases - a third column of
// alternates joined by commas. Resources go beside it in "<file>.csv_res",
// which is the directory name pyglossary's CSV writer derives and its reader
// looks for, so a dump round-trips through `pyglossary dict.csv out.xyz`
// without being told anything about it.
//
// Cross-references are written in their canonical spelling, `entry://`: a
// source's `bword:` links are respelled on the way out (htmlref.CanonRef), never
// copied, whether the body comes from a format Reader or a prepared text.db.
//
// The point is an exit door: every dictionary wudict can read becomes a format
// forty other tools can read, using their converter rather than a matrix of
// writers here.

func cmdDump(args []string) error {
	fs := flag.NewFlagSet("dump", flag.ExitOnError)
	var out, format, modeFlag, compress, resFlag string
	var include, exclude multiFlag
	fs.StringVar(&out, "o", "", "output folder for the dump and its resources (created if missing)")
	fs.StringVar(&out, "output", "", "long form of -o")
	fs.StringVar(&format, "format", "csv", "csv, or md (WuWeiDict markdown)")
	fs.StringVar(&modeFlag, "mode", "html", "md only: html (each article's HTML kept) or clean (markdown only, lossy)")
	fs.StringVar(&compress, "compress", "", "md only: gz")
	fs.StringVar(&resFlag, "resources", "all", "all, text (only .css, .js and other text files), or none")
	fs.Var(&include, "include", "write only the resources that match this pattern (as `wudict res` reads one); repeat for several")
	fs.Var(&exclude, "exclude", "do not write the resources that match this pattern; repeat for several")
	articles := fs.Bool("articles", true, "write the articles; -articles=false writes the resources only")
	fs.Parse(args)
	if fs.NArg() != 1 || out == "" {
		return fmt.Errorf("usage: wudict dump [-format csv|md] [-mode html|clean] [-compress gz] [-resources all|text|none] [-include pattern]… [-exclude pattern]… [-articles=false] -o <outdir> <dictfile>")
	}
	scope, err := parseResScope(resFlag)
	if err != nil {
		return err
	}
	md := false
	switch strings.ToLower(format) {
	case "csv":
	case "md", "markdown":
		md = true
	default:
		return fmt.Errorf("-format %q: want csv or md", format)
	}
	mode, err := wmd.ParseMode(modeFlag)
	if err != nil {
		return fmt.Errorf("-%v", err)
	}
	gz := false
	switch strings.ToLower(compress) {
	case "":
	case "gz":
		gz = true
	default:
		return fmt.Errorf("-compress %q: want gz", compress)
	}
	modeSet := false
	fs.Visit(func(f *flag.Flag) { modeSet = modeSet || f.Name == "mode" })
	if !md && (gz || modeSet) {
		return fmt.Errorf("-mode and -compress are for -format md")
	}
	sel, err := newResSelect(scope, include, exclude, *articles, gz || modeSet)
	if err != nil {
		return err
	}
	applyLibrarySettings()
	// A prepared dictionary is a folder, and the folder is the name a user
	// types for it: resolved to its text.db here, once, so every stage below
	// reads the same file.
	src := dict.MainFile(fs.Arg(0))
	// A self-preparing format's resources are read from its containers, which
	// for a missing file are simply absent: say what is missing instead.
	if _, err := os.Stat(src); err != nil {
		return err
	}

	base := dumpBase(src)
	if base == "" {
		return fmt.Errorf("cannot derive an output name from %q: rename the file, or dump it from its library folder", src)
	}
	path := filepath.Join(out, base+".csv")
	resDir := path + "_res" // pyglossary: the csv's own name plus "_res"
	if md {
		path = filepath.Join(out, base+wmd.Ext)
		if gz {
			path += ".gz"
		}
		resDir = filepath.Join(out, base+".wudict.files") // spec R2.3
	}

	// Said before anything is written, because after it is too late to be a
	// warning. Named files, not a general caution: "will be overwritten" is
	// only useful if you can tell whether it means yours.
	if fsx.DirExists(out) {
		switch {
		case !*articles:
			fmt.Fprintf(os.Stderr, "%s already exists - anything already in %s/ under the same name will be overwritten\n",
				out, filepath.Base(resDir))
		case scope == resNone:
			fmt.Fprintf(os.Stderr, "%s already exists - %s will be overwritten\n", out, filepath.Base(path))
		default:
			fmt.Fprintf(os.Stderr, "%s already exists - %s and anything already in %s/ under the same name will be overwritten\n",
				out, filepath.Base(path), filepath.Base(resDir))
		}
	}
	if scope == resNone {
		return dumpArticles(src, out, path, base, md, mode, gz)
	}
	if !*articles { // one open: there is no article pass to keep it out of
		return withResources(src, func(d *resSource) error {
			plan, err := planDump(d, resDir, sel)
			if err != nil {
				return err
			}
			if len(plan.jobs)+len(plan.problems) == 0 {
				return fmt.Errorf("%s: no resources to write", filepath.Base(src))
			}
			writeDump(d, resDir, plan)
			return nil
		})
	}
	// An -include is checked against the resources before the articles are
	// written, so a mistyped one fails before the article pass. Without one
	// there is nothing to check early, and the resources are planned and
	// written after the articles, with one open of the dictionary.
	var plan dumpPlan
	early := len(sel.include) > 0
	if early {
		d, err := openResources(src, selfPreparedFormat(src))
		if err != nil {
			return fmt.Errorf("%w\nhint: -resources none dumps the articles without the resources", err)
		}
		plan, err = planDump(d, resDir, sel)
		d.Close()
		if err != nil {
			return err
		}
	}
	if err := dumpArticles(src, out, path, base, md, mode, gz); err != nil {
		return err
	}
	if early && len(plan.jobs) == 0 {
		writeDump(nil, resDir, plan)
		return nil
	}
	return withResources(src, func(d *resSource) error {
		if !early {
			var err error
			if plan, err = planDump(d, resDir, sel); err != nil {
				return err
			}
		}
		writeDump(d, resDir, plan)
		return nil
	})
}

// withResources opens src for its resources (openResources) for the length
// of fn.
func withResources(src string, fn func(d *resSource) error) error {
	d, err := openResources(src, selfPreparedFormat(src))
	if err != nil {
		return err
	}
	defer d.Close()
	return fn(d)
}

// dumpArticles writes the dictionary's entries to path, as markdown or CSV,
// and reports what it wrote.
func dumpArticles(src, out, path, base string, md bool, mode wmd.Mode, gz bool) error {
	var n int
	if md {
		r, err := dumpMarkdown(src, out, path, base+".wudict", mode, gz)
		if err != nil {
			var cf *cleanFailure
			if errors.As(err, &cf) {
				printRawEntry(cf)
			}
			return err
		}
		n = r.articles
		dumpNote(r.empty, "article with an empty body SKIPPED")
		dumpNote(r.nameless, "entry without a headword SKIPPED")
		dumpNote(r.repaired, "name or header value repaired (control characters, invalid UTF-8)")
	} else {
		r, err := dumpEntries(src, out, path)
		if err != nil {
			return err
		}
		n = r.rows
		dumpNote(r.nameless, "entry without a headword SKIPPED")
	}
	size, _ := fileSize(path)
	fmt.Printf("%s → %s (%s)\n", logx.Plural(n, "entry", "entries"), path, logx.Size(size))
	return nil
}

// dumpNote reports, when n > 0, what the dump changed on the way out.
func dumpNote(n int, what string) {
	if n > 0 {
		fmt.Fprintf(os.Stderr, "  %d %s\n", n, what)
	}
}

// resScope is which resources a dump writes (-resources).
type resScope int

const (
	resAll  resScope = iota
	resText          // stylesheets, scripts and other text an article uses directly
	resNone          // the articles only
)

func parseResScope(s string) (resScope, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "all":
		return resAll, nil
	case "text":
		return resText, nil
	case "none":
		return resNone, nil
	}
	return 0, fmt.Errorf("-resources %q: want all, text or none", s)
}

// textExts are the resources -resources text keeps: text an article loads
// as it is. Images - svg included - audio, video and fonts are media.
var textExts = map[string]bool{
	".css": true, ".js": true, ".mjs": true, ".json": true,
	".html": true, ".htm": true, ".xml": true, ".txt": true,
}

func isTextResource(name string) bool { return textExts[strings.ToLower(path.Ext(name))] }

// resSelect is which resources a dump writes: those that pass -resources,
// match an -include when one is given, and match no -exclude. Every filter
// narrows, so the order of the flags does not count.
type resSelect struct {
	scope            resScope
	include, exclude []*resource.Glob
	incSrc, excSrc   []string // the patterns as typed, for messages
}

// newResSelect compiles the resource flags of a dump, and refuses the
// combinations that contradict themselves. articleFlags is whether -mode or
// -compress was given.
func newResSelect(scope resScope, include, exclude []string, articles, articleFlags bool) (resSelect, error) {
	if scope == resNone && len(include)+len(exclude) > 0 {
		return resSelect{}, errors.New("-include and -exclude choose resources, and -resources none writes none")
	}
	if !articles && scope == resNone {
		return resSelect{}, errors.New("-articles=false with -resources none writes nothing")
	}
	if !articles && articleFlags {
		return resSelect{}, errors.New("-mode and -compress are for the articles, and -articles=false writes none")
	}
	sel := resSelect{scope: scope, incSrc: include, excSrc: exclude}
	for _, p := range include {
		g, err := resource.NewGlob(p)
		if err != nil {
			return resSelect{}, fmt.Errorf("-include %q: %w", p, err)
		}
		sel.include = append(sel.include, g)
	}
	for _, p := range exclude {
		g, err := resource.NewGlob(p)
		if err != nil {
			return resSelect{}, fmt.Errorf("-exclude %q: %w", p, err)
		}
		sel.exclude = append(sel.exclude, g)
	}
	return sel, nil
}

// pick returns the names sel writes, in their order, minus the operating
// system's own files (a dump is for humans; .DS_Store is not). noInclude and
// noExclude are the patterns that matched nothing they were asked about: an
// -include among the names -resources keeps, an -exclude among the names the
// includes keep.
func (sel resSelect) pick(names []string) (kept, noInclude, noExclude []string) {
	incHit := make([]bool, len(sel.include))
	excHit := make([]bool, len(sel.exclude))
	for _, n := range resource.Filter(names) {
		if sel.scope == resText && !isTextResource(n) {
			continue
		}
		k := resource.Key(n)
		if len(sel.include) > 0 {
			in := false
			for i, g := range sel.include {
				if g.MatchKey(k) {
					incHit[i], in = true, true
				}
			}
			if !in {
				continue
			}
		}
		out := false
		for i, g := range sel.exclude {
			if g.MatchKey(k) {
				excHit[i], out = true, true
			}
		}
		if !out {
			kept = append(kept, n)
		}
	}
	for i, hit := range incHit {
		if !hit {
			noInclude = append(noInclude, sel.incSrc[i])
		}
	}
	for i, hit := range excHit {
		if !hit {
			noExclude = append(noExclude, sel.excSrc[i])
		}
	}
	return kept, noInclude, noExclude
}

// dumpPlan is the resources a dump writes, decided before the articles are
// written: a mistyped -include fails before a long article pass, not after it.
type dumpPlan struct {
	jobs     []resJob
	problems []string // what planRes left out
}

// planDump picks d's resources and maps them to files in resDir. A dump that
// plans before its articles closes d again before reading them, so the
// article pass does not hold a second open dictionary.
func planDump(d *resSource, resDir string, sel resSelect) (dumpPlan, error) {
	names, noInclude, noExclude := sel.pick(d.list())
	if len(noInclude) > 0 {
		q := make([]string, len(noInclude))
		for i, p := range noInclude {
			q[i] = strconv.Quote(p)
		}
		return dumpPlan{}, fmt.Errorf("-include %s: matches no resource", strings.Join(q, ", "))
	}
	for _, p := range noExclude {
		fmt.Fprintf(os.Stderr, "  -exclude %q matches no resource\n", p)
	}
	jobs, problems := planRes(names, resDir)
	return dumpPlan{jobs: jobs, problems: problems}, nil
}

// writeDump writes the planned resources into resDir, preserving the folders
// the names carry so that an article's `src="audio/x.mp3"` still resolves
// after the conversion. d may be nil when nothing is to be written. A resource
// that cannot be written is reported, and the dump still succeeds (writeRes).
func writeDump(d dict.ResourceOpener, resDir string, plan dumpPlan) {
	for _, p := range plan.problems {
		fmt.Fprintf(os.Stderr, "  %s\n", p)
	}
	var files, failed int
	var written int64
	if len(plan.jobs) > 0 {
		files, written, failed = writeRes(d, plan.jobs)
	}
	if failed += len(plan.problems); failed > 0 {
		fmt.Fprintf(os.Stderr, "  %d of %d resources could not be extracted\n", failed, files+failed)
	}
	if files > 0 {
		fmt.Printf("%s → %s (%s)\n", logx.Plural(files, "resource", "resources"), resDir, logx.Size(written))
	}
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

// csvDump is what a CSV dump wrote.
type csvDump struct {
	rows     int // entries written
	nameless int // entries left out for having no headword
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
func dumpEntries(src, outDir, csvPath string) (r csvDump, err error) {
	var meta dict.Meta
	var each func(row func([]string, string) error) error
	var closeSrc func() error

	if store.IsTextDB(src) {
		s, err := store.Open(src)
		if err != nil {
			return r, err
		}
		meta, closeSrc = s.Meta(), s.Close
		each = func(row func([]string, string) error) error {
			return s.EachEntry(func(headword string, alts []string, body string) error {
				return row(append([]string{headword}, alts...), htmlref.CanonLinks(body))
			})
		}
	} else {
		rd, err := dict.OpenReader(src)
		if err != nil {
			return r, err
		}
		meta, closeSrc = rd.Meta(), rd.Close
		each = func(row func([]string, string) error) error { return readAll(rd, row) }
	}
	defer closeSrc()

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return r, err
	}
	f, err := os.Create(csvPath)
	if err != nil {
		return r, err
	}
	defer f.Close()
	w := csv.NewWriter(f) // buffered internally

	m := newMeter("entries", meta.EntryCount, entryEvery)
	defer m.Clear()
	row := func(words []string, body string) error {
		m.Add(1)
		if len(words) == 0 || words[0] == "" {
			r.nameless++
			return nil // a row pyglossary would drop: never written
		}
		rec := []string{words[0], body}
		if len(words) > 1 {
			// Comma-joined regardless of the delimiter, which is what
			// pyglossary's reader splits this column on.
			rec = append(rec, strings.Join(words[1:], ","))
		}
		r.rows++
		return w.Write(rec)
	}

	if err := writeInfoRows(w, meta); err != nil {
		return r, err
	}
	if err := each(row); err != nil {
		return r, err
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return r, err
	}
	return r, f.Close()
}

// readAll drains a format Reader into row(), rendering each body exactly as an
// ingest would, with its cross-references in the canonical `entry://` spelling.
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
			// A pure redirect has no body of its own, so it becomes an
			// entry:// anchor and the pointer survives the conversion instead
			// of becoming a dangling headword.
			href := htmlref.EntryHref(e.LinkTo)
			esc := html.EscapeString(e.LinkTo)
			if err := row(e.Headwords, `<a href="`+html.EscapeString(href)+`">`+esc+`</a>`); err != nil {
				return err
			}
			continue
		}
		body, err := store.NormalizeBody(e)
		if err != nil {
			return fmt.Errorf("%s: %w", firstWord(e.Headwords), err)
		}
		if err := row(e.Headwords, htmlref.CanonLinks(body)); err != nil {
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
