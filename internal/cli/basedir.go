// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"os"
	"path/filepath"
	"runtime"

	"github.com/wuweidict/wudict/internal/basedir"
	"github.com/wuweidict/wudict/internal/config"
	"github.com/wuweidict/wudict/internal/logx"
	"github.com/wuweidict/wudict/internal/server"
	"github.com/wuweidict/wudict/internal/store"
)

// moveLegacyDirs moves ~/.wudict into the XDG base directories on a free Unix
// desktop (internal/basedir), and reports whether the layout changed - the
// caller then loads the configuration again from its new place.
//
// Called once the port is bound: a wudict already serving this configuration
// is then not running, and a second launch that hands over to one never gets
// here - moving files from under a running server would lose its writes.
//
// Nothing moves where the user chose one folder: a portable wudict.toml or an
// explicit --config. An explicit DB_DIR or LEMMA_DIR inside ~/.wudict keeps
// that folder where it is - the setting names it, and is not rewritten.
func moveLegacyDirs(cfg config.Config, explicitConfig bool) bool {
	home := basedir.Home()
	if explicitConfig || cfg.Portable || !basedir.Pending(runtime.GOOS, home, os.Getenv) {
		return false
	}
	legacy := basedir.Legacy(home)
	short := func(p string) string { return basedir.Short(p, home) }
	keep := map[string]string{}
	inUse := func(entry, key, val string) {
		if val != "" && basedir.Within(val, filepath.Join(legacy, entry)) {
			keep[entry] = key + " points into it"
		}
	}
	db := cfg.DBDir
	if db == "" {
		db = os.Getenv("WUDICT_DB_DIR")
	}
	inUse(basedir.EntryLibrary, "DB_DIR", db)
	if cfg.Origins["LEMMA_DIR"] != "" {
		inUse(basedir.EntryLemmas, "LEMMA_DIR", cfg.LemmaDir)
	}

	m, err := basedir.PlanMove(runtime.GOOS, home, os.Getenv, keep)
	if err != nil {
		logx.Warn("%s was not moved: %v", short(legacy), err)
		return false
	}
	if m == nil {
		return false
	}
	// state.json names prepared dictionaries by path, and their ids are
	// hashes of it: re-pointed before the library moves (RelocatePrefs says
	// why that order is safe either way). The howto needs none of this: its
	// id is fixed.
	for _, s := range m.Steps() {
		if s.Name != basedir.EntryLibrary {
			continue
		}
		if err := server.RelocatePrefs(filepath.Join(legacy, server.StateFile), s.From, s.To); err != nil {
			logx.Warn("%s was not moved: %v", short(legacy), err)
			return false
		}
	}
	logx.Status("moving %s to the standard folders (XDG base directories)", short(legacy))
	res, err := m.Run()
	if err != nil {
		logx.Warn("%s was not moved, and stays in use: %v\n"+
			"  move its contents by hand as \"wudict --help\" (FOLDERS) lists them, or fix the cause and start wudict again",
			short(legacy), err)
		return false
	}
	for _, s := range res.Moved {
		logx.Status("  %s → %s", short(s.From), short(s.To))
		// The howto has been prepared like any dictionary, and its library
		// folder names it by path: it follows the guide, and is not left
		// behind as a second "wudict howto".
		if s.Name == basedir.EntryHowto {
			store.Repoint(s.From, s.To)
		}
	}
	for _, l := range res.Left {
		logx.Warn("%s stays: %s", short(l.Path), l.Why)
	}
	if res.Removed {
		logx.Status("  %s removed: nothing is left in it", short(legacy))
	}
	return true
}
