// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/wuweidict/wudict/internal/fsx"
)

// Leftover is one deletable item in the db dir: an incomplete or unreadable
// library folder, an interrupted ingest temp file, or a file left over from
// the pre-folder flat layout.
//
// A healthy prepared dictionary is never a leftover. One whose source file
// VANISHED is an orphan (orphans.go, D156): real data the user may still want,
// so it is offered on its own, answered one by one, and never swept up with
// this debris by a plain `clean -f`. One whose source CHANGED is neither:
// re-indexing overwrites its text.db in place, so nothing is superseded.
type Leftover struct {
	Path   string
	Size   int64
	Reason string
	IsDir  bool
}

// FindLeftovers scans the db dir for deletable items. It never flags a healthy
// prepared-dictionary folder, whatever became of its source.
func FindLeftovers() ([]Leftover, error) {
	dir := DefaultDBDir()
	des, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Leftover
	for _, de := range des {
		p := filepath.Join(dir, de.Name())
		if de.IsDir() {
			out = append(out, judgeFolder(p)...)
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
			// dictionary already has a prepared folder - then it is a true
			// duplicate and deleting it loses nothing.
			reason = "superseded by a prepared folder for the same dictionary"
		case strings.HasSuffix(name, ".media.db"):
			// its text.db partner (if any) survived adoption, so it too is a
			// superseded duplicate; alone, it has nothing to pair with.
			reason = "media database with no dictionary to pair with"
			if fsx.FileExists(strings.TrimSuffix(p, ".media.db") + ".text.db") {
				reason = "paired with a superseded database"
			}
		}
		if reason != "" {
			out = append(out, Leftover{Path: p, Size: fi.Size(), Reason: reason})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// judgeFolder reports what is deletable in one library folder: the folder
// itself when it is garbage - no text.db at all (an interrupted claim, or a
// media.db with nothing to pair with), or a text.db that cannot be read - and
// otherwise the interrupted ingest temps lying inside a perfectly healthy one.
//
// That second case is the only one that can still arise. store.tempDBName
// builds every ingest at <dbPath>.ingest.<rand> and renames it onto text.db on
// success, and since D20 put each dictionary in its own folder, dbPath is
// always inside one - so a crash or a kill leaves the temp HERE, never beside
// the folder, and a scan that only asked "does this folder have a readable
// text.db" answered yes and walked past 21 MB of debris. The loop below is not
// a general sweep of the folder: a file that is not an ingest temp is left
// alone, because a folder is the user's unit to copy and move (D20) and
// whatever else they put in it is theirs.
func judgeFolder(dir string) []Leftover {
	textDB := TextDBPath(dir)
	fi, err := os.Stat(textDB)
	if err != nil || fi.IsDir() {
		reason := "incomplete dictionary folder (no text.db)"
		if _, err := os.Stat(MediaDBPath(dir)); err == nil {
			reason = "media.db with no dictionary to pair with"
		} else if _, err := os.Stat(FullTextDBPath(dir)); err == nil {
			reason = "fulltext.db with no dictionary to pair with"
		}
		return []Leftover{{Path: dir, Size: dirSize(dir), Reason: reason, IsDir: true}}
	}
	if _, err := ReadMeta(textDB); err != nil {
		return []Leftover{{Path: dir, Size: dirSize(dir), Reason: "unreadable database", IsDir: true}}
	}
	return staleIngests(dir)
}

// ingestGrace is how long an ingest temp must have gone untouched before it
// counts as abandoned. An ingest writes continuously, so its temp's mtime is
// always within seconds of now; an hour of silence means the process that
// owned it is gone. Without this, `wudict clean -f` run while a big dictionary
// is being prepared in another window would delete that ingest's scratch file
// out from under it - and the ingest would fail at the rename, having done all
// of the work.
const ingestGrace = time.Hour

// staleIngests lists abandoned ingest temps directly inside a healthy folder.
func staleIngests(dir string) []Leftover {
	des, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	cutoff := time.Now().Add(-ingestGrace)
	var out []Leftover
	for _, de := range des {
		if de.IsDir() || !strings.Contains(strings.ToLower(de.Name()), ".ingest.") {
			continue
		}
		fi, err := de.Info()
		if err != nil || fi.ModTime().After(cutoff) {
			continue // still being written, or unreadable: not ours to judge
		}
		out = append(out, Leftover{
			Path:   filepath.Join(dir, de.Name()),
			Size:   fi.Size(),
			Reason: "interrupted ingest (temp file)",
		})
	}
	return out
}
