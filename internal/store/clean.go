// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Orphan is one deletable item in the db dir: an incomplete or unreadable
// library folder, an interrupted ingest temp file, or a file left over from
// the pre-folder flat layout.
//
// A prepared dictionary whose source file merely VANISHED is deliberately NOT
// an orphan — the folder is the user's only copy of that dictionary now, and
// deleting it would be data loss. Nor is a dictionary whose source CHANGED:
// re-indexing overwrites its text.db in place, so nothing is superseded.
type Orphan struct {
	Path   string
	Size   int64
	Reason string
	IsDir  bool
}

// FindOrphans scans the db dir for deletable items. It never flags a healthy
// prepared-dictionary folder, whatever became of its source.
func FindOrphans() ([]Orphan, error) {
	dir := DefaultDBDir()
	des, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Orphan
	for _, de := range des {
		p := filepath.Join(dir, de.Name())
		if de.IsDir() {
			if o, ok := judgeFolder(p); ok {
				out = append(out, o)
			}
			continue
		}
		fi, err := de.Info()
		if err != nil {
			continue
		}
		name := strings.ToLower(de.Name())
		reason := ""
		switch {
		case strings.Contains(name, ".ingest."):
			reason = "interrupted ingest (temp file)"
		case strings.HasSuffix(name, ".text.db"):
			// a loose database is real data, never garbage: AdoptLoose moves it
			// into a folder at startup. One survives that only when the same
			// dictionary already has a prepared folder — then it is a true
			// duplicate and deleting it loses nothing.
			reason = "superseded by a prepared folder for the same dictionary"
		case strings.HasSuffix(name, ".media.db"):
			// its text.db partner (if any) survived adoption, so it too is a
			// superseded duplicate; alone, it has nothing to pair with.
			reason = "media database with no dictionary to pair with"
			if fileExists(strings.TrimSuffix(p, ".media.db") + ".text.db") {
				reason = "paired with a superseded database"
			}
		}
		if reason != "" {
			out = append(out, Orphan{Path: p, Size: fi.Size(), Reason: reason})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// judgeFolder decides whether a library folder is garbage: no text.db at all
// (an interrupted claim, or a media.db with nothing to pair with) or one that
// cannot be read as a gonow database.
func judgeFolder(dir string) (Orphan, bool) {
	textDB := TextDBPath(dir)
	fi, err := os.Stat(textDB)
	if err != nil || fi.IsDir() {
		reason := "incomplete dictionary folder (no text.db)"
		if _, err := os.Stat(MediaDBPath(dir)); err == nil {
			reason = "media.db with no dictionary to pair with"
		}
		return Orphan{Path: dir, Size: dirSize(dir), Reason: reason, IsDir: true}, true
	}
	if _, err := ReadMeta(textDB); err != nil {
		return Orphan{Path: dir, Size: dirSize(dir), Reason: "unreadable database", IsDir: true}, true
	}
	return Orphan{}, false
}
