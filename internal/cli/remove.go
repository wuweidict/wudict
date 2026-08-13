// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/wuweidict/wudict/internal/dict"
	"github.com/wuweidict/wudict/internal/store"
)

// `wudict rm` — the CLI half of D63.
//
// `clean` removes what the library should not contain; this removes what it
// should, on purpose, by name. It is the one place where the two objects a
// dictionary consists of — the prepared folder and the original files — are
// both deletable, and it is dry-run by default for exactly that reason: the
// user names a dictionary, sees the concrete file list and the bytes, and then
// repeats the command with -f.
//
// It deliberately does not talk to a running server: this is a local file
// operation, and a running instance rescans on its own. Deleting a prepared
// folder out from under a live server closes its handle at the next use — the
// same thing that happens when a source is unmounted.

func cmdRemove(args []string) error {
	applyLibrarySettings()
	fs := flag.NewFlagSet("rm", flag.ExitOnError)
	force := fs.Bool("f", false, "actually delete (default: dry run, list only)")
	keepSource := fs.Bool("keep-source", false, "delete the prepared folder only, leaving the original files")
	keepIndex := fs.Bool("keep-index", false, "delete the original files only, leaving the prepared dictionary")
	fs.Parse(args)
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: wudict rm [-f] [-keep-source|-keep-index] <name|path>")
	}
	if *keepSource && *keepIndex {
		return fmt.Errorf("-keep-source and -keep-index cancel each other out — that would delete nothing")
	}
	dropPrepared, dropSource := !*keepIndex, !*keepSource

	preparedDir, allSources, name, err := resolveRemoval(fs.Arg(0))
	if err != nil {
		return err
	}
	// D24 §4 read backwards: the source is the safety net, so it may only be
	// cut while the prepared dictionary can stand without it. Media that is
	// still only in the original would leave a prepared text.db serving
	// articles whose images and audio had been deleted.
	if dropSource && !dropPrepared {
		if preparedDir == "" {
			return fmt.Errorf("%q is not prepared, so deleting its files would delete the dictionary", name)
		}
		if len(allSources) > 0 && len(dict.CompanionMedia(allSources[0])) > 0 &&
			!fileExists(store.MediaDBPath(preparedDir)) {
			return fmt.Errorf("pack media for %q first (wudict ingest -full) — its images and audio are still only in the original files", name)
		}
	}

	prepared, sources := preparedDir, allSources
	if !dropPrepared {
		prepared = ""
	}
	if !dropSource {
		sources = nil
	}
	if prepared == "" && len(sources) == 0 {
		if !dropSource {
			return fmt.Errorf("%q has nothing prepared to remove", name)
		}
		return fmt.Errorf("%q has no original files to remove", name)
	}

	var total int64
	if prepared != "" {
		n := store.TreeSize(prepared)
		total += n
		fmt.Printf("prepared  %s  (%.1f MB)\n", prepared, mb(n))
	}
	for _, p := range sources {
		n := store.TreeSize(p)
		total += n
		fmt.Printf("original  %s  (%.1f MB)\n", p, mb(n))
	}
	fmt.Printf("%s — %.1f MB total\n", name, mb(total))

	if !*force {
		fmt.Println("dry run — re-run with -f to delete")
		return nil
	}

	var failed int
	if prepared != "" {
		if _, err := store.RemovePrepared(prepared); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			failed++
		}
	}
	for _, p := range sources {
		if err := os.RemoveAll(p); err != nil {
			fmt.Fprintf(os.Stderr, "delete %s: %v\n", p, err)
			failed++
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d deletions failed", failed)
	}
	fmt.Printf("deleted — %.1f MB freed\n", mb(total))
	if dropPrepared && !dropSource && len(allSources) > 0 {
		fmt.Println("note: the original files are still in place, so this dictionary will be indexed again the next time it is searched")
	}
	return nil
}

// resolveRemoval turns one argument into (prepared folder, original files,
// display name). The argument may be a library entry's name, its folder name,
// the folder itself, a text.db inside it, or the path of an original
// dictionary file — every string the app shows a user for a dictionary.
func resolveRemoval(arg string) (prepared string, sources []string, name string, err error) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return "", nil, "", fmt.Errorf("no dictionary given")
	}
	lib, _ := store.Library()

	var hits []store.LibEntry
	for _, e := range lib {
		if matchesEntry(e, arg) {
			hits = append(hits, e)
		}
	}
	if len(hits) > 1 {
		var names []string
		for _, e := range hits {
			names = append(names, e.Dir)
		}
		return "", nil, "", fmt.Errorf("%q matches %d prepared dictionaries: %s", arg, len(hits), strings.Join(names, ", "))
	}
	if len(hits) == 1 {
		e := hits[0]
		if e.Source != "" && e.SourceExists {
			sources = dict.SourceFiles(e.Source)
		}
		return e.Dir, sources, e.Name, nil
	}

	// Not in the library: a path to an original dictionary is still removable,
	// and may have a prepared folder that Library() skipped as unreadable.
	if fi, statErr := os.Stat(arg); statErr == nil && !fi.IsDir() {
		abs, _ := filepath.Abs(arg)
		if dir, ok := store.LookupDir(abs); ok {
			prepared = dir
		}
		return prepared, dict.SourceFiles(abs), filepath.Base(abs), nil
	}
	return "", nil, "", fmt.Errorf("no dictionary named %q in the library (%s) and no such file", arg, store.DefaultDBDir())
}

// matchesEntry accepts any of the identifiers the UI and `list` print.
func matchesEntry(e store.LibEntry, arg string) bool {
	if strings.EqualFold(arg, e.Name) || strings.EqualFold(arg, filepath.Base(e.Dir)) {
		return true
	}
	if dict.SameDir(arg, e.Dir) || dict.SameDir(arg, e.TextDB) {
		return true
	}
	return e.Source != "" && dict.SameDir(arg, e.Source)
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

func mb(n int64) float64 { return float64(n) / (1 << 20) }
