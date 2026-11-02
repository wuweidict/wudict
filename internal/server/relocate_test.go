// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// RelocatePrefs re-points the records under a moving folder - and only those,
// on a path boundary - keeping every id and every other field, so healList can
// re-attach them on either side of the move.
func TestRelocatePrefs(t *testing.T) {
	dir := t.TempDir()
	from, to := filepath.Join(dir, "old", "db"), filepath.Join(dir, "new", "db")
	title := "Mine"
	in := prefsFile{Version: prefsVersion, Dicts: []DictPref{
		{ID: "a", Path: filepath.Join(from, "Oxford", "text.db"), Off: true, Title: &title},
		{ID: "b", Path: filepath.Join(dir, "old", "dbx", "text.db")}, // a sibling, not inside
		{ID: "c", Path: filepath.Join(dir, "src", "x.mdx"), Pin: true},
	}}
	path := filepath.Join(dir, StateFile)
	data, _ := json.Marshal(in)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RelocatePrefs(path, from, to); err != nil {
		t.Fatal(err)
	}
	var out prefsFile
	b, _ := os.ReadFile(path)
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	want := in.Dicts
	want[0].Path = filepath.Join(to, "Oxford", "text.db")
	if len(out.Dicts) != len(want) {
		t.Fatalf("%d records, want %d", len(out.Dicts), len(want))
	}
	for i, d := range out.Dicts {
		w := want[i]
		if d.ID != w.ID || d.Path != w.Path || d.Off != w.Off || d.Pin != w.Pin || (d.Title == nil) != (w.Title == nil) {
			t.Errorf("record %d = %+v, want %+v", i, d, w)
		}
	}
	if out.Version != prefsVersion {
		t.Errorf("version = %d, want %d", out.Version, prefsVersion)
	}

	if err := RelocatePrefs(filepath.Join(dir, "missing.json"), from, to); err != nil {
		t.Errorf("a missing state file is nothing to do: %v", err)
	}
	bad := filepath.Join(dir, "bad.json")
	os.WriteFile(bad, []byte("{"), 0o600)
	if err := RelocatePrefs(bad, from, to); err == nil {
		t.Error("a malformed state file must be reported, not rewritten")
	}
	if b, _ := os.ReadFile(bad); string(b) != "{" {
		t.Errorf("a malformed state file was rewritten: %q", b)
	}
}
