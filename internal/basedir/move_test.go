// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package basedir

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// legacyTree builds a ~/.wudict holding every kind of entry, plus one that is
// not wudict's, and returns home.
func legacyTree(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	l := filepath.Join(home, LegacyName)
	files := map[string]string{
		"wudict.toml":                    `SERVER_PORT = "7000"`,
		"state.json":                     `{"version":3,"dicts":[]}`,
		"token":                          "secret",
		"history.txt":                    "word",
		"favourites.txt":                 "fav",
		"groups.ini":                     "G = x",
		"style/user.css":                 "b{}",
		"db/Oxford/text.db":              "SQLite",
		"db/Oxford/info.txt":             "info",
		"lemmas/pl.txt":                  "być\tjest",
		"builtin/wudict-howto.wudict.md": "# howto",
		"notes-of-mine.txt":              "mine",
	}
	for name, body := range files {
		p := filepath.Join(l, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(filepath.Join(l, "token"), 0o600); err != nil {
		t.Fatal(err)
	}
	return home
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	return string(b)
}

// wantMoved checks every entry arrived in its class folder with its content.
func wantMoved(t *testing.T, x Dirs) {
	t.Helper()
	for p, want := range map[string]string{
		filepath.Join(x.Config, "wudict.toml"):                     `SERVER_PORT = "7000"`,
		filepath.Join(x.Config, "groups.ini"):                      "G = x",
		filepath.Join(x.Config, "style", "user.css"):               "b{}",
		filepath.Join(x.Data, "db", "Oxford", "text.db"):           "SQLite",
		filepath.Join(x.Data, "lemmas", "pl.txt"):                  "być\tjest",
		filepath.Join(x.Data, "favourites.txt"):                    "fav",
		filepath.Join(x.Data, "builtin", "wudict-howto.wudict.md"): "# howto",
		filepath.Join(x.State, "state.json"):                       `{"version":3,"dicts":[]}`,
		filepath.Join(x.State, "token"):                            "secret",
		filepath.Join(x.State, "history.txt"):                      "word",
	} {
		if got := read(t, p); got != want {
			t.Errorf("%s = %q, want %q", p, got, want)
		}
	}
	if runtime.GOOS != "windows" {
		if fi, err := os.Stat(filepath.Join(x.State, "token")); err != nil || fi.Mode().Perm() != 0o600 {
			t.Errorf("token mode = %v (%v), want 0600", fi.Mode().Perm(), err)
		}
	}
	if _, err := os.Stat(x.Config + stageSuffix); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the staged config folder is left behind: %v", err)
	}
}

func plan(t *testing.T, home string, keep map[string]string) *Move {
	t.Helper()
	m, err := PlanMove("linux", home, env(nil), keep)
	if err != nil || m == nil {
		t.Fatalf("PlanMove = %v, %v", m, err)
	}
	return m
}

func TestMove(t *testing.T) {
	home := legacyTree(t)
	legacy := filepath.Join(home, LegacyName)
	old := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	os.Chtimes(filepath.Join(legacy, "db", "Oxford", "text.db"), old, old)

	res, err := plan(t, home, nil).Run()
	if err != nil {
		t.Fatal(err)
	}
	x := Split(home, env(nil))
	wantMoved(t, x)
	if Pending("linux", home, env(nil)) {
		t.Error("still pending after the move")
	}
	if len(res.Moved) != 10 {
		t.Errorf("moved %d entries, want 10", len(res.Moved))
	}
	// not wudict's: stays, and so does the folder holding it
	if len(res.Left) != 1 || filepath.Base(res.Left[0].Path) != "notes-of-mine.txt" || res.Removed {
		t.Errorf("Left = %v, Removed = %v; want only the user's own file, folder kept", res.Left, res.Removed)
	}
	if got := read(t, filepath.Join(legacy, "notes-of-mine.txt")); got != "mine" {
		t.Errorf("the user's own file changed: %q", got)
	}
	if fi, err := os.Stat(filepath.Join(x.Data, "db", "Oxford", "text.db")); err != nil || !fi.ModTime().Equal(old) {
		t.Errorf("modification time not kept: %v, %v", fi, err)
	}
}

func TestMoveRemovesEmptyFolder(t *testing.T) {
	home := legacyTree(t)
	os.Remove(filepath.Join(home, LegacyName, "notes-of-mine.txt"))
	res, err := plan(t, home, nil).Run()
	if err != nil {
		t.Fatal(err)
	}
	if !res.Removed {
		t.Error("an emptied ~/.wudict was not removed")
	}
	if _, err := os.Stat(filepath.Join(home, LegacyName)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("~/.wudict still there: %v", err)
	}
}

func TestMoveKeeps(t *testing.T) {
	home := legacyTree(t)
	res, err := plan(t, home, map[string]string{EntryLibrary: "DB_DIR points into it"}).Run()
	if err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(home, LegacyName, "db", "Oxford", "text.db")); got != "SQLite" {
		t.Errorf("a kept library moved: %q", got)
	}
	x := Split(home, env(nil))
	if _, err := os.Stat(filepath.Join(x.Data, "db")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a kept library appeared in the data folder: %v", err)
	}
	var kept bool
	for _, l := range res.Left {
		kept = kept || (filepath.Base(l.Path) == "db" && l.Why == "DB_DIR points into it")
	}
	if !kept {
		t.Errorf("Left = %v, want the library with its reason", res.Left)
	}
}

func TestMoveConflict(t *testing.T) {
	home := legacyTree(t)
	x := Split(home, env(nil))
	if err := os.MkdirAll(filepath.Join(x.Data, "db"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := PlanMove("linux", home, env(nil), nil); err == nil || !strings.Contains(err.Error(), "both exist") {
		t.Fatalf("PlanMove = %v, want the conflict named", err)
	}
	if !Pending("linux", home, env(nil)) {
		t.Error("a refused move committed")
	}
}

// A rename failing half-way - another file system, say - puts everything
// back: the old folder is whole, nothing committed, nothing staged, and the
// next attempt can succeed.
func TestMoveRollsBack(t *testing.T) {
	for _, failAt := range []int{1, 5, 11} { // first entry, mid-way, the commit itself
		t.Run("", func(t *testing.T) {
			home := legacyTree(t)
			calls := 0
			rename = func(a, b string) error {
				if calls++; calls == failAt {
					return &os.LinkError{Op: "rename", Old: a, New: b, Err: errors.New("invalid cross-device link")}
				}
				return os.Rename(a, b)
			}
			t.Cleanup(func() { rename = os.Rename })

			if _, err := plan(t, home, nil).Run(); err == nil || !strings.Contains(err.Error(), "cross-device") {
				t.Fatalf("Run = %v, want the failure", err)
			}
			if !Pending("linux", home, env(nil)) {
				t.Fatal("a failed move committed")
			}
			x := Split(home, env(nil))
			if _, err := os.Stat(x.Config + stageSuffix); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("the staged config folder is left behind: %v", err)
			}
			for _, p := range []string{"db/Oxford/text.db", "token", "wudict.toml", "style/user.css"} {
				if _, err := os.Stat(filepath.Join(home, LegacyName, p)); err != nil {
					t.Errorf("%s was not put back: %v", p, err)
				}
			}

			rename = os.Rename
			if _, err := plan(t, home, nil).Run(); err != nil {
				t.Fatal(err)
			}
			wantMoved(t, x)
		})
	}
}

// A run killed after some entries moved: the next one finishes the job -
// what already left ~/.wudict is not planned again, and the staged config
// folder is completed.
func TestMoveResumes(t *testing.T) {
	home := legacyTree(t)
	legacy := filepath.Join(home, LegacyName)
	x := Split(home, env(nil))
	// as the killed run left it: the library moved, groups.ini moved into
	// the stage, nothing committed
	for _, d := range []string{x.Data, x.Config + stageSuffix} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Rename(filepath.Join(legacy, "db"), filepath.Join(x.Data, "db")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(legacy, "groups.ini"), filepath.Join(x.Config+stageSuffix, "groups.ini")); err != nil {
		t.Fatal(err)
	}
	if !Pending("linux", home, env(nil)) {
		t.Fatal("not pending before the resumed run")
	}

	if _, err := plan(t, home, nil).Run(); err != nil {
		t.Fatal(err)
	}
	wantMoved(t, x)
}

func TestPlanMoveNothingPending(t *testing.T) {
	home := t.TempDir()
	if m, err := PlanMove("linux", home, env(nil), nil); m != nil || err != nil {
		t.Errorf("no ~/.wudict: %v, %v; want nil, nil", m, err)
	}
	home = legacyTree(t)
	if m, err := PlanMove("darwin", home, env(nil), nil); m != nil || err != nil {
		t.Errorf("darwin: %v, %v; want nil, nil", m, err)
	}
}
