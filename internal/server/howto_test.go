// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	_ "github.com/wuweidict/wudict/internal/format/wmd"
	"github.com/wuweidict/wudict/internal/howto"
)

// TestBuiltinGuide: the wudict howto is listed under its fixed id beside the
// user's dictionaries; removing it deletes it and keeps it removed, Setup's
// restore brings it back; and it stands down for the user's own copy - which
// POST /api/howto puts in the import folder, under the guide's id.
func TestBuiltinGuide(t *testing.T) {
	t.Setenv("WUDICT_DB_DIR", t.TempDir())
	user := t.TempDir()
	hdir := FolderUserDir(user).Builtin()
	guide, err := howto.Install(hdir)
	if err != nil {
		t.Fatal(err)
	}
	folder := t.TempDir()
	reg, err := NewRegistry([]string{folder}, false, WithBuiltin(Builtin{ID: howto.ID, Path: guide}))
	if err != nil {
		t.Fatal(err)
	}
	e, err := reg.get(howto.ID)
	if err != nil {
		t.Fatalf("the guide is not listed under %q: %v", howto.ID, err)
	}
	s := New(reg)
	s.User = FolderUserDir(user)
	if info, _ := s.dictInfoFor(e, "", ""); !info.Builtin || info.Name != "wudict howto" {
		t.Errorf("row = builtin %v, name %q", info.Builtin, info.Name)
	}
	if reg.UserCount() != 0 {
		t.Errorf("UserCount = %d with only the guide", reg.UserCount())
	}
	local := func(method, target string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, target, nil)
		req.RemoteAddr = "127.0.0.1:50000"
		rec := httptest.NewRecorder()
		switch {
		case method == http.MethodDelete:
			s.handleRemoveLibrary(rec, req)
		default:
			s.handleHowtoCopy(rec, req)
		}
		return rec
	}

	// Removed whole, and it stays removed.
	if rec := local(http.MethodDelete, "/api/library?dict="+howto.ID+"&prepared=1&source=0"); rec.Code == 200 {
		t.Error("the guide's index alone was removable")
	}
	if rec := local(http.MethodDelete, "/api/library?dict="+howto.ID); rec.Code != 200 {
		t.Fatalf("remove: %d %s", rec.Code, rec.Body)
	}
	if _, err := reg.get(howto.ID); err == nil {
		t.Error("the removed guide is still listed")
	}
	if _, err := os.Stat(guide); err == nil {
		t.Error("the removed guide's file is still there")
	}
	if !howto.IsRemoved(hdir) {
		t.Error("the removal was not recorded")
	}
	if err := reg.Rescan(); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.get(howto.ID); err == nil {
		t.Error("a rescan brought the removed guide back")
	}

	// Setup brings it back.
	if rec := local(http.MethodPost, "/api/howto?restore=1"); rec.Code != 200 {
		t.Fatalf("restore: %d %s", rec.Code, rec.Body)
	}
	if e, err := reg.get(howto.ID); err != nil || !e.builtin {
		t.Errorf("the restored guide is not listed as built in: %v", err)
	}
	if howto.IsRemoved(hdir) {
		t.Error("still recorded as removed after restore")
	}

	// The user's copy stands in under the guide's id, so the guide's own
	// links (/browse?dict=wudict-howto) reach it - and it is the user's file.
	rec := local(http.MethodPost, "/api/howto")
	if rec.Code != 200 {
		t.Fatalf("copy: %d %s", rec.Code, rec.Body)
	}
	var got struct{ Path string }
	json.Unmarshal(rec.Body.Bytes(), &got)
	if got.Path != filepath.Join(folder, howto.FileName) {
		t.Errorf("copied to %q", got.Path)
	}
	ce, err := reg.get(howto.ID)
	if err != nil {
		t.Fatalf("the copy is not listed under %q: %v", howto.ID, err)
	}
	// the same file, however the scan spelled its path (/var vs /private/var)
	if a, b := stat(t, ce.Path), stat(t, got.Path); !os.SameFile(a, b) {
		t.Errorf("listed under %q: %s, want the copy %s", howto.ID, ce.Path, got.Path)
	}
	if ce.builtin {
		t.Error("the user's copy is flagged builtin")
	}
	if reg.Count() != 1 || reg.UserCount() != 1 {
		t.Errorf("%d listed (%d the user's), want the user's copy alone", reg.Count(), reg.UserCount())
	}
	if rec := local(http.MethodPost, "/api/howto"); rec.Code != 409 {
		t.Errorf("second copy: %d, want 409", rec.Code)
	}
}

func stat(t *testing.T, p string) os.FileInfo {
	t.Helper()
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	return fi
}
