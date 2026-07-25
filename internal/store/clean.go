// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"os"
	"path/filepath"
	"strings"
)

// Orphan is one stale cache database: its source file vanished or
// changed (content hash no longer matches the cache name).
type Orphan struct {
	Path   string
	Size   int64
	Reason string
}

// FindOrphans scans the cache dir for .text.db/.media.db files whose
// recorded source is gone or whose content hash is stale (a changed
// source re-ingests under a new name, leaving the old file behind).
func FindOrphans() ([]Orphan, error) {
	dir := DefaultDBDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Orphan
	for _, de := range entries {
		name := de.Name()
		if !strings.HasSuffix(name, ".text.db") {
			continue
		}
		p := filepath.Join(dir, name)
		info, err := de.Info()
		if err != nil {
			continue
		}
		src, err := ReadMetaValue(p, "source_path")
		if err != nil {
			out = append(out, Orphan{Path: p, Size: info.Size(), Reason: "unreadable metadata"})
			continue
		}
		reason := ""
		if _, err := os.Stat(src); err != nil {
			reason = "source file no longer exists: " + src
		} else {
			dictName, _ := ReadMetaValue(p, "name")
			if expected := CacheBase(src, dictName) + ".text.db"; expected != p {
				reason = "source file changed (superseded by a newer ingest)"
			}
		}
		if reason == "" {
			continue
		}
		out = append(out, Orphan{Path: p, Size: info.Size(), Reason: reason})
		// the paired media.db is orphaned with it
		mediaPath := strings.TrimSuffix(p, ".text.db") + ".media.db"
		if st, err := os.Stat(mediaPath); err == nil {
			out = append(out, Orphan{Path: mediaPath, Size: st.Size(), Reason: "paired with orphaned text.db"})
		}
	}
	return out, nil
}
