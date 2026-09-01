// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/wuweidict/wudict/internal/config"
	"github.com/wuweidict/wudict/internal/lang"
	"github.com/wuweidict/wudict/internal/lemmas"
)

// `wudict lemmas` - obtaining the lemma data LEMMA_DIR holds (D88).
//
// wudict compiles in English and nothing else, so every other language is a
// file the user has to get from somewhere. `make lemma-files` is not that
// somewhere: it needs a Go toolchain and a populated module cache, which a
// user who downloaded a release binary does not have. These three commands are
// the whole of the answer, and they are shaped for someone who does not
// program - a list with the installed ones ticked, and a download that takes
// language names as readily as codes.

const lemmasUsage = `usage: wudict lemmas <command>

  list                    show installed and available languages
  download <lang…>        install one or more ("pl ru", "polish russian")
  download -all           install every language in the catalogue
  remove <lang…>          delete installed lemma data

Flags (all three): -dir <folder> overrides LEMMA_DIR, -url <manifest>
overrides LEMMA_URL - a URL, or a path to a manifest.json on disk.
`

func cmdLemmas(args []string) error {
	if len(args) == 0 {
		fmt.Print(lemmasUsage)
		return nil
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "list", "ls":
		return cmdLemmasList(rest)
	case "download", "install", "add", "get":
		return cmdLemmasDownload(rest)
	case "remove", "rm", "delete", "uninstall":
		return cmdLemmasRemove(rest)
	case "-h", "--help", "help":
		fmt.Print(lemmasUsage)
		return nil
	}
	return fmt.Errorf("unknown command %q\n\n%s", sub, lemmasUsage)
}

// lemmaFlags is the folder and catalogue every subcommand resolves the same
// way: flag, then config (env / wudict.toml), then the built-in default.
func lemmaFlags(name string) (*flag.FlagSet, *string, *string) {
	fs := flag.NewFlagSet("lemmas "+name, flag.ExitOnError)
	dir := fs.String("dir", "", "folder to install into (default: LEMMA_DIR)")
	url := fs.String("url", "", "catalogue to read (default: LEMMA_URL)")
	return fs, dir, url
}

func lemmaPaths(dir, url string) (string, string) {
	cfg, err := config.Load("", nil)
	if err == nil {
		if dir == "" {
			dir = cfg.LemmaDir
		}
		if url == "" {
			url = cfg.LemmaURL
		}
	}
	if url == "" {
		url = config.DefaultLemmaURL
	}
	return config.ExpandHome(dir), url
}

// interruptible makes Ctrl-C stop a download at the byte it has reached rather
// than after the HTTP client's timeouts expire. The partial file is a
// temporary that never gets renamed, so an interrupted install leaves the
// folder exactly as it was.
func interruptible() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt)
}

// row is one line of `lemmas list`, merged from what is on disk and what the
// catalogue offers - either of which may be missing.
type row struct {
	code, name, mark, note string
	size, ram              int64
}

func cmdLemmasList(args []string) error {
	fs, dirF, urlF := lemmaFlags("list")
	fs.Parse(args)
	dir, url := lemmaPaths(*dirF, *urlF)

	local, shadowed := lemmas.Installed(dir)

	ctx, cancel := interruptible()
	defer cancel()
	cat, ferr := lemmas.Fetch(ctx, url)

	rows := make([]row, 0, len(local)+24)
	seen := map[string]bool{}
	if cat != nil {
		for _, e := range cat.Languages {
			seen[e.Code] = true
			rows = append(rows, catalogRow(e, local))
		}
	}
	// Anything on disk the catalogue does not list: a hand-written file, or a
	// language that was dropped from the catalogue after it was installed.
	// Either way it still works, so it is shown as working.
	for code, l := range local {
		if seen[code] {
			continue
		}
		rows = append(rows, row{
			code: code, name: lang.Name(code), mark: "[x]",
			size: l.Size, note: "local file, not in the catalogue",
		})
	}
	if !seen["en"] && local["en"].Path == "" {
		rows = append(rows, row{code: "en", name: "English", mark: "[x]", note: "built in"})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].name < rows[j].name })

	fmt.Printf("Lemma files in %s\n", dir)
	fmt.Printf("%d installed", len(local))
	if cat != nil {
		fmt.Printf(", %d available", len(cat.Languages))
	}
	fmt.Println()
	fmt.Println()

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	for _, r := range rows {
		size, ram := "", ""
		if r.size > 0 {
			size = humanSize(r.size)
		}
		if r.ram > 0 {
			ram = fmt.Sprintf("~%d MB RAM", r.ram)
		}
		// Trailing empty columns are dropped rather than padded: tabwriter would
		// otherwise end most lines in a run of spaces nobody can see and every
		// diff can.
		cells := []string{"  " + r.mark, r.code, r.name, size, ram, r.note}
		for len(cells) > 0 && cells[len(cells)-1] == "" {
			cells = cells[:len(cells)-1]
		}
		fmt.Fprintln(w, strings.Join(cells, "\t"))
	}
	w.Flush()

	fmt.Println()
	fmt.Println("  wudict lemmas download pl ru    install       [x] ready   [ ] not installed")
	fmt.Println("  wudict lemmas remove pl         delete        [!] installed, differs from the catalogue")

	for _, s := range shadowed {
		fmt.Fprintln(os.Stderr, "warning:", s)
	}
	if ferr != nil {
		// Not an error exit: what is installed is the half of the answer that
		// does not need a network, and printing it is the point of the command.
		fmt.Fprintf(os.Stderr, "\ncould not read the catalogue: %v\n", ferr)
		fmt.Fprintln(os.Stderr, "installed languages are listed above; available ones need a connection.")
	}
	return nil
}

func catalogRow(e lemmas.Entry, local map[string]lemmas.Local) row {
	r := row{code: e.Code, name: e.Name, mark: "[ ]", size: e.Size, ram: int64(e.HeapMB)}
	l, ok := local[e.Code]
	switch {
	case !ok && e.Code == "en":
		r.mark, r.note = "[x]", "built in"
	case !ok:
		// nothing installed: the catalogue's size is what a download costs
	default:
		r.mark, r.size = "[x]", l.Size
		if h, err := lemmas.Hash(l.Path); err != nil || h != e.SHA256 {
			r.mark, r.note = "[!]", "installed, differs from the catalogue"
		} else if e.Code == "en" {
			r.note = "built in, replaced by this file"
		}
	}
	return r
}

func cmdLemmasDownload(args []string) error {
	fs, dirF, urlF := lemmaFlags("download")
	all := fs.Bool("all", false, "install every language in the catalogue")
	force := fs.Bool("f", false, "re-download even when the installed file already matches")
	fs.Parse(args)
	dir, url := lemmaPaths(*dirF, *urlF)

	if fs.NArg() == 0 && !*all {
		// Deliberately not "download everything": that is a few hundred MB of
		// data and, once loaded, several GB of heap nobody asked for.
		return fmt.Errorf("name at least one language, or pass -all\n\n%s", lemmasUsage)
	}
	if fs.NArg() > 0 && *all {
		return fmt.Errorf("-all takes no language arguments")
	}

	// Argument shapes are checked before anything opens a socket, and one bad
	// argument stops the whole command: a download that installed three of
	// four languages and then failed would leave the user guessing which.
	want := make([]string, 0, fs.NArg())
	var bad []string
	for _, a := range fs.Args() {
		code := lang.Normalize(a)
		if code == "" {
			bad = append(bad, a)
			continue
		}
		if !contains(want, code) {
			want = append(want, code)
		}
	}
	if len(bad) > 0 {
		return fmt.Errorf("not a language: %s (use a code like \"pl\" or a name like \"polish\")",
			strings.Join(bad, ", "))
	}

	ctx, cancel := interruptible()
	defer cancel()
	cat, err := lemmas.Fetch(ctx, url)
	if err != nil {
		return err
	}
	if *all {
		for _, e := range cat.Languages {
			want = append(want, e.Code)
		}
	}

	todo := make([]lemmas.Entry, 0, len(want))
	var missing []string
	for _, code := range want {
		e, ok := cat.Find(code)
		if !ok {
			missing = append(missing, code)
			continue
		}
		todo = append(todo, e)
	}
	if len(missing) > 0 {
		return fmt.Errorf("the catalogue has no lemma data for %s\navailable: %s",
			strings.Join(missing, ", "), strings.Join(codesOf(cat), " "))
	}

	local, _ := lemmas.Installed(dir)
	pending := todo[:0:0]
	for _, e := range todo {
		if !*force {
			if l, ok := local[e.Code]; ok {
				if h, err := lemmas.Hash(l.Path); err == nil && h == e.SHA256 {
					fmt.Printf("%-4s %-14s already installed\n", e.Code, e.Name)
					continue
				}
			}
		}
		pending = append(pending, e)
	}
	if len(pending) == 0 {
		return nil
	}

	var bytes, ram int64
	for _, e := range pending {
		bytes += e.Size
		ram += int64(e.HeapMB)
	}
	if len(pending) > 1 {
		fmt.Printf("%d languages, %s to download", len(pending), humanSize(bytes))
		if ram > 0 {
			fmt.Printf(" (~%d MB of memory when all are loaded at once)", ram)
		}
		fmt.Println()
	}

	for _, e := range pending {
		path, err := cat.Install(ctx, dir, e, nil)
		if err != nil {
			return err
		}
		fmt.Printf("%-4s %-14s %9s  %s\n", e.Code, e.Name, humanSize(e.Size), path)
	}
	// The CLI is not the running server, so it cannot tell that one to re-read
	// the folder. Saying so is more useful than a silent partial effect.
	fmt.Println("restart wudict for a running server to pick these up")
	return nil
}

func cmdLemmasRemove(args []string) error {
	// No -url: removing is a local operation and offering a catalogue flag
	// would imply it consults one.
	fs := flag.NewFlagSet("lemmas remove", flag.ExitOnError)
	dirF := fs.String("dir", "", "folder to remove from (default: LEMMA_DIR)")
	fs.Parse(args)
	dir, _ := lemmaPaths(*dirF, "")

	if fs.NArg() == 0 {
		return fmt.Errorf("name at least one language\n\n%s", lemmasUsage)
	}
	codes := make([]string, 0, fs.NArg())
	var bad []string
	for _, a := range fs.Args() {
		if code := lang.Normalize(a); code != "" {
			codes = append(codes, code)
		} else {
			bad = append(bad, a)
		}
	}
	if len(bad) > 0 {
		return fmt.Errorf("not a language: %s", strings.Join(bad, ", "))
	}
	for _, code := range codes {
		gone, err := lemmas.Remove(dir, code)
		if err != nil {
			return err
		}
		if len(gone) == 0 {
			fmt.Printf("%-4s nothing installed\n", code)
			continue
		}
		for _, p := range gone {
			fmt.Printf("%-4s removed %s\n", code, p)
		}
		if code == "en" {
			fmt.Println("     English falls back to the built-in data")
		}
	}
	return nil
}

func codesOf(c *lemmas.Catalog) []string {
	out := make([]string, 0, len(c.Languages))
	for _, e := range c.Languages {
		out = append(out, e.Code)
	}
	sort.Strings(out)
	return out
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
