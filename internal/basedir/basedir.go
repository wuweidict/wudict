// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Package basedir decides where wudict keeps what it writes for the user.
//
// On a free Unix desktop that is the XDG Base Directory layout
// (https://specifications.freedesktop.org/basedir/latest/), one `wudict`
// folder per class:
//
//	Config  $XDG_CONFIG_HOME/wudict  (~/.config)       wudict.toml, groups.ini, style/
//	Data    $XDG_DATA_HOME/wudict    (~/.local/share)  db/, lemmas/, favourites.txt, builtin/
//	State   $XDG_STATE_HOME/wudict   (~/.local/state)  state.json, history, token, log
//
// Nothing is cache: builtin/ is data although the binary rewrites the howto in
// it, because it also holds the user's decision to remove the howto, which a
// cache clean must not undo.
//
// Everywhere else - macOS, Windows, Android, whose HOME is the app's own
// directory - all three are the one folder ~/.wudict. So is a Unix desktop whose
// ~/.wudict has not been moved yet (Pending): the old layout stays whole until
// Move has carried all of it, so no run ever sees half of each.
//
// Standard library only, so store, config and the CLI can all ask.
package basedir

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// App is the folder name inside each XDG base directory.
const App = "wudict"

// LegacyName is the single folder under the home directory.
const LegacyName = ".wudict"

// Dirs is where each class of file lives. A field is "" when the platform
// cannot say (no home directory and no usable variable): what lives there then
// cannot be saved.
type Dirs struct {
	Config, Data, State string
}

// OneFolder is the layout that keeps everything in dir.
func OneFolder(dir string) Dirs { return Dirs{dir, dir, dir} }

// XDG reports whether goos follows the freedesktop base directory spec. A
// runtime question, never a build tag: GOOS=android satisfies the "linux" tag,
// and Android keeps $HOME/.wudict (the shell seeds wudict.toml there, D62).
func XDG(goos string) bool {
	switch goos {
	case "linux", "freebsd", "openbsd", "netbsd", "dragonfly", "illumos", "solaris":
		return true
	}
	return false
}

// Legacy is the single folder, <home>/.wudict; "" without a home.
func Legacy(home string) string {
	if home == "" {
		return ""
	}
	return filepath.Join(home, LegacyName)
}

// Split is the XDG layout. The spec makes a relative value invalid, to be
// ignored - so it falls back to the default exactly like an unset or empty
// one. (os.UserConfigDir returns an error instead, which is why it is not
// used.)
func Split(home string, getenv func(string) string) Dirs {
	base := func(key, def string) string {
		if v := getenv(key); v != "" && filepath.IsAbs(v) {
			return filepath.Join(v, App)
		}
		if home == "" {
			return ""
		}
		return filepath.Join(home, def, App)
	}
	return Dirs{
		Config: base("XDG_CONFIG_HOME", ".config"),
		Data:   base("XDG_DATA_HOME", filepath.Join(".local", "share")),
		State:  base("XDG_STATE_HOME", filepath.Join(".local", "state")),
	}
}

// SystemConfigDirs are the machine-wide config folders, most important first:
// each absolute entry of $XDG_CONFIG_DIRS (default /etc/xdg) + /wudict.
func SystemConfigDirs(getenv func(string) string) []string {
	v := getenv("XDG_CONFIG_DIRS")
	if v == "" {
		v = "/etc/xdg"
	}
	var out []string
	for d := range strings.SplitSeq(v, ":") {
		if filepath.IsAbs(d) {
			out = append(out, filepath.Join(d, App))
		}
	}
	return out
}

// Pending reports a ~/.wudict that has not been moved into the XDG layout: on
// an XDG platform, the old folder exists and the XDG config folder does not.
// Move creates the config folder last, so its existence is the commit.
func Pending(goos, home string, getenv func(string) string) bool {
	if !XDG(goos) || home == "" {
		return false
	}
	x := Split(home, getenv)
	if x.Config == "" || x.Data == "" || x.State == "" {
		return false
	}
	return isDir(Legacy(home)) && !exists(x.Config)
}

// Resolve is the layout in effect for goos, home and the environment.
func Resolve(goos, home string, getenv func(string) string) Dirs {
	if !XDG(goos) || Pending(goos, home, getenv) {
		return OneFolder(Legacy(home))
	}
	return Split(home, getenv)
}

// Current is the layout in effect for this process. Not cached: it is a few
// stat calls, and a cached answer would outlive Move and every test that
// points HOME somewhere else.
func Current() Dirs { return Resolve(runtime.GOOS, Home(), os.Getenv) }

// Home is the user's home directory, "" when the OS cannot say.
func Home() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return h
}

// Within reports that path is dir or lies inside it, after both are made
// absolute and clean. Lexical: a symlink pointing in is not followed.
func Within(path, dir string) bool {
	if path == "" || dir == "" {
		return false
	}
	p, err1 := filepath.Abs(path)
	d, err2 := filepath.Abs(dir)
	if err1 != nil || err2 != nil {
		return false
	}
	rel, err := filepath.Rel(d, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// Short is p with the home directory written as ~, for a message.
func Short(p, home string) string {
	if home != "" {
		if rest, ok := strings.CutPrefix(p, home); ok && (rest == "" || rest[0] == filepath.Separator) {
			return "~" + rest
		}
	}
	return p
}

func exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}
