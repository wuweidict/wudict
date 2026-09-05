// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// localReq issues a request from the machine running wudict - the caller that
// may always delete, whatever ALLOW_REMOTE_DELETE says.
func localReq(t *testing.T, s *Server, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := newRequest(method, path, nil)
	req.RemoteAddr = "127.0.0.1:5555"
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	return rec
}

// deleteReq is the ordinary case these tests are about: the user at the
// keyboard deleting from their own machine.
func deleteReq(t *testing.T, s *Server, path string) *httptest.ResponseRecorder {
	t.Helper()
	return localReq(t, s, "DELETE", path)
}

// remoteReq is a browser on another machine - httptest's default address,
// which is not loopback. ALLOW_REMOTE_DELETE governs this one, and it is off
// unless a test says otherwise.
func remoteReq(t *testing.T, s *Server, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, newRequest(method, path, nil))
	return rec
}

// idOf finds a dictionary's id by file name. Not pathID(path): discovery
// reports the path it resolved, which on macOS is /private/var where the test
// wrote to /var - the ids are only equal for the registry's own spelling.
func idOf(t *testing.T, s *Server, base string) string {
	t.Helper()
	for _, e := range s.reg.all() {
		if filepath.Base(e.Path) == base {
			return e.ID
		}
	}
	t.Fatalf("no dictionary named %q in the registry", base)
	return ""
}

// The desktop is where the user owns the files, so removal is offered there
// too (D63 amended) - the presence of a file manager is no longer the test,
// and Reveal is still offered beside it.
func TestRemovalOfferedOnADesktop(t *testing.T) {
	s := newTestServer(t)
	restore := revealPossible
	revealPossible = func() bool { return true }
	defer func() { revealPossible = restore }()

	var info map[string]any
	rec := localReq(t, s, "GET", "/api/config")
	if err := json.Unmarshal(rec.Body.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
	if info["canDelete"] != true || info["canReveal"] != true {
		t.Errorf("canDelete=%v canReveal=%v on a desktop, want true/true",
			info["canDelete"], info["canReveal"])
	}
	// and the endpoint agrees: it gets as far as validating the parameters
	// rather than refusing the caller.
	if rec := localReq(t, s, "DELETE", "/api/library?dict=nosuch"); rec.Code != 400 {
		t.Errorf("DELETE from the local machine = %d, want 400 (unknown dictionary), not a refusal: %s",
			rec.Code, rec.Body.String())
	}
}

// A browser on another machine is a different caller, and the one the setting
// is about. It is off by default: remote DELETE 403s naming the setting,
// canDelete tells the remote page not to draw the control, and the local
// machine is unaffected.
func TestRemoteRemovalRefusedByDefault(t *testing.T) {
	s := newTestServer(t)

	rec := remoteReq(t, s, "DELETE", "/api/library?dict=whatever")
	if rec.Code != 403 || !strings.Contains(rec.Body.String(), "ALLOW_REMOTE_DELETE") {
		t.Fatalf("remote DELETE = %d %s, want 403 naming the setting", rec.Code, rec.Body.String())
	}

	var info map[string]any
	grec := remoteReq(t, s, "GET", "/api/config")
	if err := json.Unmarshal(grec.Body.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
	if info["canDelete"] != false {
		t.Errorf("canDelete = %v for a remote page, want false", info["canDelete"])
	}
	if rec := localReq(t, s, "DELETE", "/api/library?dict=nosuch"); rec.Code == 403 {
		t.Error("the local machine was refused too; the setting is about remote callers only")
	}
}

// ALLOW_REMOTE_DELETE = "1" invites the remote browser in: canDelete says so
// whether or not this machine has a file manager, and the endpoint gets as far
// as validating parameters rather than refusing the caller.
func TestRemoteRemovalAllowedWhenEnabled(t *testing.T) {
	s := newTestServer(t)
	s.AllowRemoteDelete = true
	restore := revealPossible
	revealPossible = func() bool { return false }
	defer func() { revealPossible = restore }()

	var info map[string]any
	rec := remoteReq(t, s, "GET", "/api/config")
	if err := json.Unmarshal(rec.Body.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
	if info["canDelete"] != true || info["canReveal"] != false {
		t.Errorf("canDelete=%v canReveal=%v, want true/false", info["canDelete"], info["canReveal"])
	}

	if rec := remoteReq(t, s, "DELETE", "/api/library"); rec.Code != 400 {
		t.Errorf("DELETE with no dict = %d, want 400", rec.Code)
	}
	if rec := remoteReq(t, s, "DELETE", "/api/library?dict=nosuch"); rec.Code != 400 {
		t.Errorf("DELETE of an unknown dictionary = %d, want 400", rec.Code)
	}
}

// Deleting the originals alone would take the dictionary with them while the
// library is not enrolled (D19), and would strip a dictionary of media that is
// still only in its source (D24 §4). Both are refused, and nothing is touched.
func TestRemoveSourceOnlyRefused(t *testing.T) {
	s := newTestServer(t)
	src := s.reg.Dirs()[0]
	dsl := filepath.Join(src, "test.dsl")
	id := idOf(t, s, "test.dsl")

	rec := deleteReq(t, s, "/api/library?dict="+id+"&prepared=0&source=1")
	if rec.Code != 409 {
		t.Fatalf("source-only without USE_CACHED = %d, want 409: %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(dsl); err != nil {
		t.Fatalf("refusal deleted the source anyway: %v", err)
	}

	// With the library in use it gets as far as the media rule, which still
	// refuses: this dictionary's audio lives in its .files.zip only.
	s.reg.SetUseCached(true)
	rec = deleteReq(t, s, "/api/library?dict="+id+"&prepared=0&source=1")
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "pack media") {
		t.Fatalf("source-only with unpacked media = %d %s, want 400 naming the media",
			rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(dsl); err != nil {
		t.Fatalf("refusal deleted the source anyway: %v", err)
	}
}

// The whole point: one dictionary and everything that belongs to it, gone,
// with the rest of the folder untouched and the registry no longer listing it.
func TestRemoveEverything(t *testing.T) {
	s := newTestServer(t)
	src := s.reg.Dirs()[0]
	dsl := filepath.Join(src, "test.dsl")
	zip := dsl + ".files.zip"
	neighbour := filepath.Join(src, "other.dsl")
	if err := os.WriteFile(neighbour, []byte(sampleDSL), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.reg.Rescan(); err != nil {
		t.Fatal(err)
	}
	id := idOf(t, s, "test.dsl")

	rec := deleteReq(t, s, "/api/library?dict="+id)
	if rec.Code != 200 {
		t.Fatalf("DELETE = %d: %s", rec.Code, rec.Body.String())
	}
	var rep removal
	if err := json.Unmarshal(rec.Body.Bytes(), &rep); err != nil {
		t.Fatal(err)
	}
	if !rep.Gone {
		t.Errorf("report says the dictionary is still listed: %+v", rep)
	}
	if len(rep.Sources) != 2 {
		t.Errorf("sources removed = %v, want the .dsl and its .files.zip", rep.Sources)
	}
	if rep.Freed <= 0 {
		t.Errorf("freed = %d, want the bytes of the files it deleted", rep.Freed)
	}
	for _, p := range []string{dsl, zip} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s survived: %v", p, err)
		}
	}
	if _, err := os.Stat(neighbour); err != nil {
		t.Errorf("the neighbouring dictionary was taken too: %v", err)
	}
	if s.reg.has(id) {
		t.Error("registry still lists the removed dictionary")
	}
	if !s.reg.has(idOf(t, s, "other.dsl")) {
		t.Error("registry lost the neighbour")
	}
}
