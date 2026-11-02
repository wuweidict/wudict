// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"path/filepath"

	"github.com/wuweidict/wudict/internal/basedir"
)

// UserDir is where everything the user owns besides wudict.toml lives, at
// names derived here and nowhere else. It is the folders of the layout in
// effect (internal/basedir): beside a portable or --config wudict.toml all four
// are that file's folder, so one value moves all of it at once (D32); on a free
// Unix desktop each file goes to its XDG class. An empty folder is "no such
// folder": each path in it is then empty, and what lives there cannot be saved.
type UserDir struct{ d basedir.Dirs }

// NewUserDir is the user's files in layout d.
func NewUserDir(d basedir.Dirs) UserDir { return UserDir{d} }

// FolderUserDir keeps everything in one folder, dir.
func FolderUserDir(dir string) UserDir { return UserDir{basedir.OneFolder(dir)} }

// State is state.json: which dictionaries are searched, in what order.
func (d UserDir) State() string { return join(d.d.State, StateFile) }

// Style is the stylesheets' folder (style.go), and under it the user's files.
func (d UserDir) Style() string { return join(d.d.Config, StyleDirName) }

// Groups is groups.ini (groups.go).
func (d UserDir) Groups() string { return join(d.d.Config, GroupsFileName) }

// Failures is open-failures.json: the open failures kept across restarts
// (openfail.go).
func (d UserDir) Failures() string { return join(d.d.State, "open-failures.json") }

// History is the reader's history (words.go): a record of use, kept as state.
func (d UserDir) History() string { return join(d.d.State, HistoryFileName) }

// Favourites is the reader's own word list (words.go): data worth a backup.
func (d UserDir) Favourites() string { return join(d.d.Data, FavouritesFileName) }

// Builtin is where the wudict howto is written (internal/howto). Data, not
// cache, although the binary rewrites the guide: the folder also records that
// the user removed it, and a cache clean must not bring it back.
func (d UserDir) Builtin() string { return join(d.d.Data, "builtin") }

func join(dir, name string) string {
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, name)
}
