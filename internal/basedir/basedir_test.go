// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package basedir

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

// skipWindows: the XDG layout is Unix-only (POSIX paths, ":" between config
// dirs); wudict never resolves it on Windows, where a drive letter breaks both.
func skipWindows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the XDG layout is not resolved on Windows")
	}
}

func TestSplit(t *testing.T) {
	skipWindows(t)
	const home = "/home/u"
	tests := []struct {
		name string
		env  map[string]string
		home string
		want Dirs
	}{
		{"defaults", nil, home, Dirs{
			"/home/u/.config/wudict", "/home/u/.local/share/wudict", "/home/u/.local/state/wudict"}},
		{"absolute values win", map[string]string{
			"XDG_CONFIG_HOME": "/c", "XDG_DATA_HOME": "/d", "XDG_STATE_HOME": "/s"},
			home, Dirs{"/c/wudict", "/d/wudict", "/s/wudict"}},
		{"relative or empty values are ignored", map[string]string{
			"XDG_CONFIG_HOME": "rel", "XDG_DATA_HOME": "./d", "XDG_STATE_HOME": ""},
			home, Dirs{
				"/home/u/.config/wudict", "/home/u/.local/share/wudict", "/home/u/.local/state/wudict"}},
		{"no home: only what a variable names", map[string]string{"XDG_DATA_HOME": "/d"},
			"", Dirs{Data: "/d/wudict"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Split(tt.home, env(tt.env)); got != tt.want {
				t.Errorf("Split = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestSystemConfigDirs(t *testing.T) {
	skipWindows(t)
	tests := []struct {
		val  string
		want []string
	}{
		{"", []string{"/etc/xdg/wudict"}},
		{"/a:/b", []string{"/a/wudict", "/b/wudict"}},
		{"rel:/b::", []string{"/b/wudict"}},
	}
	for _, tt := range tests {
		if got := SystemConfigDirs(env(map[string]string{"XDG_CONFIG_DIRS": tt.val})); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("SystemConfigDirs(%q) = %q, want %q", tt.val, got, tt.want)
		}
	}
}

func TestResolve(t *testing.T) {
	home := t.TempDir()
	legacy := filepath.Join(home, LegacyName)
	noEnv := env(nil)
	xdg := Split(home, noEnv)

	for _, goos := range []string{"darwin", "windows", "android", "ios"} {
		if got := Resolve(goos, home, noEnv); got != OneFolder(legacy) {
			t.Errorf("%s: %+v, want ~/.wudict for everything", goos, got)
		}
	}
	if got := Resolve("linux", home, noEnv); got != xdg {
		t.Errorf("linux, fresh: %+v, want %+v", got, xdg)
	}
	if err := os.Mkdir(legacy, 0o700); err != nil {
		t.Fatal(err)
	}
	if !Pending("linux", home, noEnv) || Pending("darwin", home, noEnv) {
		t.Error("an unmoved ~/.wudict is pending on linux only")
	}
	if got := Resolve("freebsd", home, noEnv); got != OneFolder(legacy) {
		t.Errorf("pending: %+v, want the old folder whole", got)
	}
	if err := os.MkdirAll(xdg.Config, 0o700); err != nil {
		t.Fatal(err)
	}
	if Pending("linux", home, noEnv) {
		t.Error("the XDG config folder is the commit: no longer pending")
	}
	if got := Resolve("linux", home, noEnv); got != xdg {
		t.Errorf("committed: %+v, want %+v", got, xdg)
	}
	if got := Resolve("linux", "", noEnv); got != (Dirs{}) {
		t.Errorf("no home, no variables: %+v, want nothing", got)
	}
}

func TestWithin(t *testing.T) {
	tests := []struct {
		path, dir string
		want      bool
	}{
		{"/a/b", "/a/b", true},
		{"/a/b/c", "/a/b", true},
		{"/a/b/../c", "/a/b", false},
		{"/a/bc", "/a/b", false},
		{"/a/b/..c", "/a/b", true},
		{"", "/a", false},
	}
	for _, tt := range tests {
		if got := Within(tt.path, tt.dir); got != tt.want {
			t.Errorf("Within(%q, %q) = %v, want %v", tt.path, tt.dir, got, tt.want)
		}
	}
}

func TestShort(t *testing.T) {
	// native separators: Short runs on every platform
	n := filepath.FromSlash
	for _, tt := range []struct{ p, home, want string }{
		{n("/home/u/.config/wudict"), n("/home/u"), n("~/.config/wudict")},
		{n("/home/u"), n("/home/u"), "~"},
		{n("/home/user2/x"), n("/home/u"), n("/home/user2/x")},
		{n("/x"), "", n("/x")},
	} {
		if got := Short(tt.p, tt.home); got != tt.want {
			t.Errorf("Short(%q, %q) = %q, want %q", tt.p, tt.home, got, tt.want)
		}
	}
}
