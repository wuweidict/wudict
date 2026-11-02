// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// orphanFolders returns the folder names FindOrphans reports.
func orphanFolders(t *testing.T, skip func(string) bool) []string {
	t.Helper()
	list, err := FindOrphans(skip)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, o := range list {
		out = append(out, o.Folder)
	}
	return out
}

func TestFindOrphans(t *testing.T) {
	t.Setenv("WUDICT_DB_DIR", t.TempDir())
	src := t.TempDir()

	present := writeSrc(t, filepath.Join(src, "present.mdx"), "p")
	prepare(t, present, "Present")
	gone := writeSrc(t, filepath.Join(src, "gone.mdx"), "g")
	goneDir := prepare(t, gone, "Gone")
	kept := writeSrc(t, filepath.Join(src, "kept.mdx"), "k")
	keptDir := prepare(t, kept, "Kept")
	guide := writeSrc(t, filepath.Join(src, "guide.mdx"), "b")
	prepare(t, guide, "Guide")
	for _, p := range []string{gone, kept, guide} {
		if err := os.Remove(p); err != nil {
			t.Fatal(err)
		}
	}
	if err := MarkKept(keptDir); err != nil {
		t.Fatal(err)
	}
	skip := func(s string) bool { return s == guide }

	got := orphanFolders(t, skip)
	if len(got) != 1 || got[0] != "gone" {
		t.Fatalf("orphans = %v, want [gone]", got)
	}
	list, _ := FindOrphans(skip)
	if list[0].Name != "Gone" || list[0].Source != gone || list[0].Size <= 0 {
		t.Errorf("orphan = %+v", list[0])
	}

	// The keep marker survives a receipt regeneration.
	if err := WriteInfo(keptDir); err != nil {
		t.Fatal(err)
	}
	if !isKept(keptDir) {
		t.Fatal("WriteInfo dropped the keep marker")
	}

	// Refused: not a folder name, not an orphan, a kept one, a skipped one.
	for _, bad := range []string{"", ".", "..", "../x", "a/b", `a\b`, "present", "kept", "guide", "nosuch"} {
		if _, err := RemoveOrphan(bad, skip); err == nil {
			t.Errorf("RemoveOrphan(%q) succeeded", bad)
		}
	}

	// A source that comes back makes the folder a dictionary again.
	writeSrc(t, gone, "g")
	if got := orphanFolders(t, skip); len(got) != 0 {
		t.Fatalf("orphans after the source returned = %v", got)
	}
	if _, err := RemoveOrphan("gone", skip); err == nil {
		t.Fatal("RemoveOrphan deleted a folder whose source is back")
	}
	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}

	n, err := RemoveOrphan("gone", skip)
	if err != nil || n <= 0 {
		t.Fatalf("RemoveOrphan = %d, %v", n, err)
	}
	if _, err := os.Stat(goneDir); !os.IsNotExist(err) {
		t.Fatalf("folder still there: %v", err)
	}
}

func TestKeepOrphan(t *testing.T) {
	t.Setenv("WUDICT_DB_DIR", t.TempDir())
	src := writeSrc(t, filepath.Join(t.TempDir(), "x.mdx"), "x")
	dir := prepare(t, src, "X")
	if err := os.Remove(src); err != nil {
		t.Fatal(err)
	}
	if err := KeepOrphan("x", nil); err != nil {
		t.Fatal(err)
	}
	if got := orphanFolders(t, nil); len(got) != 0 {
		t.Fatalf("a kept folder is still offered: %v", got)
	}
	if err := MarkKept(dir); err != nil { // idempotent
		t.Fatal(err)
	}
	b, _ := os.ReadFile(InfoPath(dir))
	if c := strings.Count(string(b), keepKey+" = "); c != 1 {
		t.Fatalf("keep line written %d times:\n%s", c, b)
	}
	// The ownership claim is untouched, so the folder still serves its source.
	if own, _, _ := dirOwner(dir); own != src {
		t.Fatalf("owner = %q, want %q", own, src)
	}
}

func TestRelink(t *testing.T) {
	t.Setenv("WUDICT_DB_DIR", t.TempDir())
	root := t.TempDir()
	old := writeSrc(t, filepath.Join(root, "Moved.mdx"), "moved content")
	dir := prepare(t, old, "Moved")
	other := writeSrc(t, filepath.Join(root, "Edited.mdx"), "original")
	otherDir := prepare(t, other, "Edited")

	sub := filepath.Join(root, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(sub, "Moved.mdx")
	if err := os.Rename(old, moved); err != nil {
		t.Fatal(err)
	}
	// Same name, different content: a different file, not a move.
	if err := os.Remove(other); err != nil {
		t.Fatal(err)
	}
	impostor := writeSrc(t, filepath.Join(sub, "Edited.mdx"), "something else")

	got := Relink([]string{moved, impostor})
	if len(got) != 1 || got[0].Dir != dir || got[0].From != old || got[0].To != moved {
		t.Fatalf("Relink = %+v", got)
	}
	if d, ok := LookupDir(moved); !ok || d != dir {
		t.Fatalf("LookupDir(moved) = %q, %v; want %q", d, ok, dir)
	}
	if _, ok := PreparedFor(moved); !ok {
		t.Fatal("the relinked folder is not prepared for the moved file")
	}
	if got := orphanFolders(t, nil); len(got) != 1 || got[0] != filepath.Base(otherDir) {
		t.Fatalf("orphans = %v, want only the edited one", got)
	}
	if again := Relink([]string{moved, impostor}); len(again) != 0 {
		t.Fatalf("second Relink = %+v", again)
	}
}

// A moved file that was prepared again at its new place before the rescan
// already has its own folder: the old one is a genuine orphan, not relinked.
func TestRelinkLeavesAPreparedFileAlone(t *testing.T) {
	t.Setenv("WUDICT_DB_DIR", t.TempDir())
	root := t.TempDir()
	old := writeSrc(t, filepath.Join(root, "D.mdx"), "same")
	oldDir := prepare(t, old, "D")
	sub := filepath.Join(root, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(sub, "D.mdx")
	if err := os.Rename(old, moved); err != nil {
		t.Fatal(err)
	}
	newDir := prepare(t, moved, "D")
	if newDir == oldDir {
		t.Fatal("test setup: expected a second folder")
	}
	if got := Relink([]string{moved}); len(got) != 0 {
		t.Fatalf("Relink = %+v", got)
	}
	if got := orphanFolders(t, nil); len(got) != 1 || got[0] != filepath.Base(oldDir) {
		t.Fatalf("orphans = %v", got)
	}
}

// A folder of sources that moved as a whole takes its prepared folders with
// it - even a source changed since it was prepared - and nothing outside it.
func TestRepoint(t *testing.T) {
	t.Setenv("WUDICT_DB_DIR", t.TempDir())
	root := t.TempDir()
	from, to := filepath.Join(root, "old", "builtin"), filepath.Join(root, "new", "builtin")
	if err := os.MkdirAll(from, 0o755); err != nil {
		t.Fatal(err)
	}
	guide := writeSrc(t, filepath.Join(from, "Guide.mdx"), "guide")
	dir := prepare(t, guide, "Guide")
	other := writeSrc(t, filepath.Join(root, "Other.mdx"), "other")
	prepare(t, other, "Other")

	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(from, to); err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(to, "Guide.mdx")
	writeSrc(t, moved, "guide, a newer build") // rewritten after the move

	got := Repoint(from, to)
	if len(got) != 1 || got[0].Dir != dir || got[0].From != guide || got[0].To != moved {
		t.Fatalf("Repoint = %+v", got)
	}
	if d, ok := LookupDir(moved); !ok || d != dir {
		t.Fatalf("LookupDir(moved) = %q, %v; want %q", d, ok, dir)
	}
	if got := orphanFolders(t, nil); len(got) != 0 {
		t.Fatalf("orphans = %v, want none", got)
	}
	if again := Repoint(from, to); len(again) != 0 {
		t.Fatalf("second Repoint = %+v", again)
	}
}
