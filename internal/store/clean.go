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

// Orphan is one stale cache database that is safe to delete: a superseded
// ingest (its source changed and re-ingested under a new content-hash name),
// an unreadable/corrupt cache, or a media.db with no dictionary to pair with.
//
// A .text.db whose source file merely VANISHED is deliberately NOT an orphan:
// it is a naturalized standalone dictionary now (see StandaloneNativeDBs), and
// deleting it would destroy the user's only copy. The db dir is the native
// dictionary root, not a disposable cache.
type Orphan struct {
	Path   string
	Size   int64
	Reason string
}

// FindOrphans scans the cache dir for deletable databases. It never flags a
// .text.db just because its source is gone — that .text.db is a standalone
// native dictionary. Orphans are: superseded ingests, unreadable caches, and
// media.db files whose .text.db partner is missing or itself an orphan.
func FindOrphans() ([]Orphan, error) {
	dir := DefaultDBDir()
	des, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	present := map[string]bool{}  // base (no .text.db) -> the text.db exists on disk
	orphaned := map[string]bool{} // base -> that text.db is itself an orphan
	var out []Orphan
	// pass 1: judge the .text.db files.
	for _, de := range des {
		name := de.Name()
		if de.IsDir() || !strings.HasSuffix(name, ".text.db") {
			continue
		}
		base := strings.TrimSuffix(name, ".text.db")
		present[base] = true
		p := filepath.Join(dir, name)
		info, err := de.Info()
		if err != nil {
			continue
		}
		reason := ""
		src, err := ReadMetaValue(p, "source_path")
		switch {
		case err != nil:
			reason = "unreadable metadata"
		case src == "":
			// no recorded source: a standalone native dictionary — keep.
		default:
			if _, statErr := os.Stat(src); statErr != nil {
				// source vanished: promoted to a standalone native dict — keep.
				break
			}
			// source still present: an orphan only if a newer ingest has
			// superseded this content-hash under a different name.
			dictName, _ := ReadMetaValue(p, "name")
			if expected := CacheBase(src, dictName) + ".text.db"; expected != p {
				reason = "source file changed (superseded by a newer ingest)"
			}
		}
		if reason == "" {
			continue
		}
		orphaned[base] = true
		out = append(out, Orphan{Path: p, Size: info.Size(), Reason: reason})
	}
	// pass 2: judge the .media.db files against their text.db partner.
	for _, de := range des {
		name := de.Name()
		if de.IsDir() || !strings.HasSuffix(name, ".media.db") {
			continue
		}
		base := strings.TrimSuffix(name, ".media.db")
		if present[base] && !orphaned[base] {
			continue // paired with a live dictionary (standalone or cached)
		}
		info, err := de.Info()
		if err != nil {
			continue
		}
		reason := "media.db with no dictionary"
		if orphaned[base] {
			reason = "paired with orphaned text.db"
		}
		out = append(out, Orphan{Path: filepath.Join(dir, name), Size: info.Size(), Reason: reason})
	}
	return out, nil
}

// StandaloneNativeDBs returns the .text.db files in dir that have become
// standalone native dictionaries: their recorded foreign source is gone (or
// was never recorded), so nothing else in the registry represents them. A
// .text.db whose source still EXISTS is omitted on purpose — that source
// already appears in the external root and listing the cache too would
// double-list it. (This is the same source-existence test FindOrphans uses,
// applied to the opposite conclusion: gone ⇒ dictionary, not garbage.)
func StandaloneNativeDBs(dir string) ([]string, error) {
	des, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, de := range des {
		if de.IsDir() || !strings.HasSuffix(de.Name(), ".text.db") {
			continue
		}
		p := filepath.Join(dir, de.Name())
		src, err := ReadMetaValue(p, "source_path")
		if err != nil {
			continue // unreadable/corrupt: not a listable dict (FindOrphans handles it)
		}
		if src != "" {
			if _, err := os.Stat(src); err == nil {
				continue // source present → represented by its external entry
			}
		}
		out = append(out, p)
	}
	sort.Strings(out)
	return out, nil
}
