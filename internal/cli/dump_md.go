// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/wuweidict/wudict/internal/dict"
	"github.com/wuweidict/wudict/internal/format/wmd"
	"github.com/wuweidict/wudict/internal/htmlref"
	"github.com/wuweidict/wudict/internal/server"
	"github.com/wuweidict/wudict/internal/store"
)

// `wudict dump -format md` writes the dictionary as WuWeiDict markdown
// (docs/WUDICT-MARKDOWN.md §6): "<name>.wudict.md", or ".wudict.md.gz" with
// -compress gz, and its resources in "<name>.wudict.files/". -mode html (the
// default) keeps every article's HTML as it is, losing nothing; -mode clean
// writes markdown primitives only.

// mdDump is what a markdown dump wrote.
type mdDump struct {
	articles, empty, nameless, repaired int
}

// cleanFailure is a body -mode clean could not write (spec R6.8): the dump
// stops, the raw entry goes to stdout, and the error says what to do.
type cleanFailure struct {
	names    []string
	index    int
	body     string
	err      *wmd.CleanError
	src, out string
}

func (f *cleanFailure) Error() string {
	name := ""
	if len(f.names) > 0 {
		name = f.names[0]
	}
	return fmt.Sprintf("entry %q (#%d): %v\nhint: keep the dictionary's HTML instead:\n  wudict dump -format md -mode html -o %s %s",
		name, f.index, f.err, shellQuote(f.out), shellQuote(f.src))
}

// shellQuote quotes a path only when a shell would need it.
func shellQuote(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\n'\"\\$`!*?[](){}<>|&;#~") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// printRawEntry writes the entry a clean dump stopped at to stdout.
func printRawEntry(f *cleanFailure) {
	for _, n := range f.names {
		fmt.Println("## " + n)
	}
	fmt.Println()
	fmt.Println(f.body)
}

// entryWalk calls add for each entry of a source in order: an article with
// its HTML body, or a redirect with see set.
type entryWalk func(add func(names []string, body, see string) error) error

// openEntries opens src for one walk over its entries.
func openEntries(src string) (meta dict.Meta, each entryWalk, closeSrc func() error, err error) {
	if store.IsTextDB(src) {
		s, err := store.Open(src)
		if err != nil {
			return meta, nil, nil, err
		}
		meta, closeSrc = s.Meta(), s.Close
		each = func(add func([]string, string, string) error) error {
			return s.EachEntry(func(headword string, alts []string, body string) error {
				return add(append([]string{headword}, alts...), body, "")
			})
		}
	} else {
		rd, err := dict.OpenReader(src)
		if err != nil {
			return meta, nil, nil, err
		}
		meta, closeSrc = rd.Meta(), rd.Close
		each = func(add func([]string, string, string) error) error {
			for {
				e, err := rd.Next()
				if errors.Is(err, io.EOF) {
					return nil
				}
				if err != nil {
					return err
				}
				if e.LinkTo != "" {
					if err := add(e.Headwords, "", e.LinkTo); err != nil {
						return err
					}
					continue
				}
				body, err := store.NormalizeBody(e)
				if err != nil {
					return fmt.Errorf("%s: %w", firstWord(e.Headwords), err)
				}
				if err := add(e.Headwords, body, ""); err != nil {
					return err
				}
			}
		}
	}
	return meta, each, closeSrc, nil
}

// maxStyleProbe bounds the articles read looking for a stylesheet link: a
// dictionary that has one links it from every article.
const maxStyleProbe = 100

var errProbed = errors.New("probed")

// dumpStyles is the display table `clean` converts with (spec R6.6): the
// stylesheets linked by the first article, of the first maxStyleProbe, that
// links any - found and read as the server's `clean` finds and reads them.
// It is known before the first article is converted, so every article is
// converted alike. Nil when there is none, or it cannot be read - which is
// what the server does too.
func dumpStyles(src string) (htmlref.Styles, error) {
	meta, each, closeSrc, err := openEntries(src)
	if err != nil {
		return nil, err
	}
	var names []string
	seen := 0
	err = each(func(_ []string, body, see string) error {
		if see != "" || body == "" {
			return nil
		}
		if names = server.ArticleStylesheets(body); len(names) > 0 {
			return errProbed
		}
		if seen++; seen == maxStyleProbe {
			return errProbed
		}
		return nil
	})
	closeSrc()
	if err != nil && !errors.Is(err, errProbed) {
		return nil, err
	}
	if len(names) == 0 {
		return nil, nil
	}
	d, err := openResources(src, meta.Format)
	if err != nil {
		return nil, nil
	}
	defer d.Close()
	return server.DictStyles(d, names), nil
}

// dumpMarkdown writes the markdown file at path. The source is opened before
// the output folder is created, and the file appears only once it is whole.
func dumpMarkdown(src, outDir, path, stem string, mode wmd.Mode, gz bool) (r mdDump, err error) {
	var styles htmlref.Styles
	if mode == wmd.ModeClean {
		if styles, err = dumpStyles(src); err != nil {
			return r, err
		}
	}
	meta, each, closeSrc, err := openEntries(src)
	if err != nil {
		return r, err
	}
	defer closeSrc()

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return r, err
	}
	w, err := wmd.NewWriter(wmd.Head{
		Name: meta.Name, Stem: stem, From: meta.IndexLang, To: meta.ContentsLang,
		Fields: meta.Header, Description: meta.Description,
	}, mode, outDir)
	if err != nil {
		return r, err
	}
	defer w.Close()
	w.Styles = styles

	index := 0
	m := newMeter("entries read", meta.EntryCount, entryEvery)
	err = each(func(names []string, body, see string) error {
		index++
		m.Add(1)
		if see != "" {
			w.Redirect(names, see)
			return nil
		}
		if err := w.Article(names, body); err != nil {
			var ce *wmd.CleanError
			if errors.As(err, &ce) {
				return &cleanFailure{names: names, index: index, body: body, err: ce, src: src, out: outDir}
			}
			return err
		}
		return nil
	})
	m.Clear()
	if err != nil {
		return r, err
	}
	if m := newMeter("entries written", w.Entries(), entryEvery); m != nil {
		w.Progress = m.Set
		defer m.Clear()
	}

	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return r, err
	}
	defer os.Remove(tmp) // gone after the rename; left over only on failure
	var out io.Writer = f
	var zw *gzip.Writer
	if gz {
		zw = gzip.NewWriter(f) // mtime 0, no name, no comment (R2.4)
		out = zw
	}
	if _, err := w.WriteTo(out); err != nil {
		f.Close()
		var ce *wmd.CleanError
		if errors.As(err, &ce) {
			return r, &cleanFailure{names: []string{"(the description)"}, body: meta.Description, err: ce, src: src, out: outDir}
		}
		return r, err
	}
	if zw != nil {
		if err := zw.Close(); err != nil {
			f.Close()
			return r, err
		}
	}
	if err := f.Close(); err != nil {
		return r, err
	}
	if err := os.Rename(tmp, path); err != nil {
		return r, err
	}
	return mdDump{articles: w.Articles, empty: w.Empty, nameless: w.Nameless, repaired: w.Repaired}, nil
}
