// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package basedir

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// class names the base directory an entry of ~/.wudict belongs to.
type class int

const (
	classConfig class = iota
	classData
	classState
)

func (d Dirs) of(c class) string {
	switch c {
	case classConfig:
		return d.Config
	case classData:
		return d.Data
	}
	return d.State
}

// entries is everything wudict keeps in ~/.wudict, by class. Anything else in
// the folder is not wudict's and stays where it is.
var entries = []struct {
	name  string
	class class
}{
	{"db", classData},
	{"lemmas", classData},
	{"favourites.txt", classData},
	{"builtin", classData},
	{"state.json", classState},
	{"history.txt", classState},
	{"open-failures.json", classState},
	{"token", classState},
	{"wudict.log", classState},
	{"groups.ini", classConfig},
	{"style", classConfig},
	{"wudict.toml", classConfig},
}

// Entry names, for callers that state which of them must stay (PlanMove) or
// act on one that moved.
const (
	EntryLibrary = "db"
	EntryLemmas  = "lemmas"
	EntryHowto   = "builtin"
)

// stageSuffix names the config folder while it is assembled.
const stageSuffix = ".wudict-moving"

// Step is one entry carried out of ~/.wudict.
type Step struct {
	Name, From, To string
}

// Left is something that stays in ~/.wudict, and why.
type Left struct {
	Path, Why string
}

// Result is what a Move did.
type Result struct {
	Moved []Step
	Left  []Left
	// Removed: ~/.wudict ended up empty and is gone.
	Removed bool
}

// Move carries ~/.wudict into the XDG layout. It is built by PlanMove, which
// changes nothing, so a caller can act on the plan first - the state file is
// re-pointed at the new library paths before the library moves (D33's ids are
// path hashes).
type Move struct {
	legacy string
	to     Dirs
	stage  string // <Config>.wudict-moving
	steps  []Step
	left   []Left
}

// rename is os.Rename; tests make it fail to exercise the roll-back.
var rename = os.Rename

// PlanMove works out how ~/.wudict moves into the XDG layout. keep names
// entries that stay, each with its reason - an explicit setting (DB_DIR,
// LEMMA_DIR) points into them. It returns nil when nothing is pending.
//
// An entry present in both places stops the move before anything changes:
// which one is the user's is not a question a heuristic may answer.
func PlanMove(goos, home string, getenv func(string) string, keep map[string]string) (*Move, error) {
	if !Pending(goos, home, getenv) {
		return nil, nil
	}
	m := &Move{legacy: Legacy(home), to: Split(home, getenv)}
	m.stage = m.to.Config + stageSuffix
	for _, e := range entries {
		from := filepath.Join(m.legacy, e.name)
		if !exists(from) {
			continue
		}
		if why, ok := keep[e.name]; ok {
			m.left = append(m.left, Left{from, why})
			continue
		}
		s := Step{e.name, from, filepath.Join(m.to.of(e.class), e.name)}
		if exists(m.target(s)) {
			return nil, fmt.Errorf("%s and %s both exist; keep one of them (move or delete the other) and start wudict again", from, s.To)
		}
		m.steps = append(m.steps, s)
	}
	return m, nil
}

// Steps lists what Run will move, in order.
func (m *Move) Steps() []Step { return m.steps }

// Run renames every planned entry into place - a config entry into the staged
// config folder - and then renames that folder to its real name: the commit,
// after which the layout is no longer Pending. A rename that fails puts back
// the ones before it, so ~/.wudict stays whole and in use, and the error names
// the entry and the reason. Nothing is copied: a destination on another file
// system is such a failure.
//
// A process killed half-way leaves some entries in their new places and
// ~/.wudict still pending; the next Run moves the rest - an entry already gone
// is not planned again - and commits.
func (m *Move) Run() (Result, error) {
	for _, d := range []string{m.to.Data, m.to.State, m.stage} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return Result{}, err
		}
	}
	done := 0
	var err error
	for _, s := range m.steps {
		if err = rename(s.From, m.target(s)); err != nil {
			break
		}
		done++
	}
	if err == nil {
		if err = rename(m.stage, m.to.Config); err == nil {
			res := Result{Moved: m.steps, Left: append(m.left, m.foreign()...)}
			if names, rerr := readNames(m.legacy); rerr == nil && len(names) == 0 && os.Remove(m.legacy) == nil {
				res.Removed = true
			}
			return res, nil
		}
	}
	// Undo, newest first.
	for i := done - 1; i >= 0; i-- {
		s := m.steps[i]
		if uerr := os.Rename(m.target(s), s.From); uerr != nil {
			err = fmt.Errorf("%w; and %s could not be put back (it is at %s): %v", err, s.From, m.target(s), uerr)
		}
	}
	os.Remove(m.stage) // only when empty
	return Result{}, err
}

// target is where a step lands during Run: a config entry in the staged
// folder, everything else in its final place.
func (m *Move) target(s Step) string {
	if filepath.Dir(s.To) == m.to.Config {
		return filepath.Join(m.stage, s.Name)
	}
	return s.To
}

// foreign lists what is in ~/.wudict that wudict did not put there.
func (m *Move) foreign() []Left {
	names, err := readNames(m.legacy)
	if err != nil {
		return nil
	}
	known := map[string]bool{}
	for _, e := range entries {
		known[e.name] = true
	}
	var out []Left
	for _, n := range names {
		if !known[n] {
			out = append(out, Left{filepath.Join(m.legacy, n), "not wudict's"})
		}
	}
	return out
}

// readNames lists a folder's entries, sorted.
func readNames(dir string) ([]string, error) {
	es, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	names := make([]string, len(es))
	for i, e := range es {
		names[i] = e.Name()
	}
	sort.Strings(names)
	return names, nil
}
