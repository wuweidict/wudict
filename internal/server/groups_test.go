// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"bufio"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/wuweidict/wudict/internal/facet"
)

type groupsResp struct {
	Text     string            `json:"text"`
	Custom   bool              `json:"custom"`
	Unusable bool              `json:"unusable"`
	File     string            `json:"file"`
	Writable bool              `json:"writable"`
	Problems []facet.Problem   `json:"problems"`
	Facets   []facet.FacetInfo `json:"facets"`
}

func newGroupsServer(t *testing.T) *Server {
	t.Helper()
	s := newTestServer(t)
	s.User = FolderUserDir(t.TempDir())
	return s
}

func callGroups(t *testing.T, s *Server, method, body string) (groupsResp, int) {
	t.Helper()
	return callGroupsAt(t, s, method, "/api/groups", body)
}

func callGroupsAt(t *testing.T, s *Server, method, target, body string) (groupsResp, int) {
	t.Helper()
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, newRequest(method, target, strings.NewReader(body)))
	var r groupsResp
	if rec.Code == 200 {
		if err := json.Unmarshal(rec.Body.Bytes(), &r); err != nil {
			t.Fatalf("%s /api/groups: bad JSON (%v): %s", method, err, rec.Body.String())
		}
	}
	return r, rec.Code
}

// rowGroups is what /api/dicts says the one test dictionary is in, as
// "facet/group" ids.
func rowGroups(t *testing.T, s *Server) map[string]bool {
	t.Helper()
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, newRequest("GET", "/api/dicts", nil))
	out := map[string]bool{}
	sc := bufio.NewScanner(rec.Body)
	sc.Buffer(make([]byte, 1<<20), 1<<22)
	for sc.Scan() {
		var o struct {
			T    string `json:"t"`
			Dict struct {
				Groups []facet.Group `json:"groups"`
			} `json:"dict"`
		}
		if json.Unmarshal(sc.Bytes(), &o) == nil && o.T == "dict" {
			for _, g := range o.Dict.Groups {
				out[g.F+"/"+g.V] = true
			}
		}
	}
	return out
}

// No file: the embedded default is in effect, shown as not customised.
func TestGroupsDefault(t *testing.T) {
	s := newGroupsServer(t)
	r, code := callGroups(t, s, "GET", "")
	if code != 200 || r.Custom || !r.Writable || r.Text != facet.DefaultText || len(r.Problems) != 0 {
		t.Fatalf("default: code %d, %+v", code, r)
	}
	if len(r.Facets) != 2 || r.Facets[0].ID != "content" || r.Facets[1].ID != "publisher" {
		t.Errorf("default facets = %+v", r.Facets)
	}
}

// A save is the whole truth: it applies to the next /api/dicts, the default's
// sections are gone, a bad line is reported and skipped, and the title and
// the file name both count.
func TestGroupsSave(t *testing.T) {
	s := newGroupsServer(t)
	body := "Testers = server test\n[Mine]\nnot a group\nFiles: test ; the file name\n"
	r, code := callGroups(t, s, "PUT", body)
	if code != 200 || !r.Custom {
		t.Fatalf("PUT: code %d, %+v", code, r)
	}
	if len(r.Problems) != 1 || r.Problems[0].Line != 3 {
		t.Errorf("problems = %+v, want line 3", r.Problems)
	}
	if b, _ := os.ReadFile(s.User.Groups()); string(b) != body {
		t.Errorf("file = %q, want the body as sent", b)
	}
	got := rowGroups(t, s)
	for _, want := range []string{"my groups/testers", "mine/files"} {
		if !got[want] {
			t.Errorf("row groups %v, want %s", got, want)
		}
	}
	for g := range got {
		if strings.HasPrefix(g, "content/") || strings.HasPrefix(g, "publisher/") {
			t.Errorf("a default section survived the user's file: %s", g)
		}
	}
}

// Saving the default unchanged - or resetting - leaves no file behind, so the
// list goes on following the built-in one.
func TestGroupsDefaultIsNoFile(t *testing.T) {
	s := newGroupsServer(t)
	for _, tc := range []struct{ name, method, body string }{
		{"save the default", "PUT", facet.DefaultText},
		{"save it with CRLF", "PUT", strings.ReplaceAll(facet.DefaultText, "\n", "\r\n")},
		{"reset", "DELETE", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, code := callGroups(t, s, "PUT", "Mine = x\n"); code != 200 {
				t.Fatal("setup save failed")
			}
			r, code := callGroups(t, s, tc.method, tc.body)
			if code != 200 || r.Custom || r.Text != facet.DefaultText {
				t.Fatalf("code %d, custom %v", code, r.Custom)
			}
			if _, err := os.Stat(s.User.Groups()); !os.IsNotExist(err) {
				t.Errorf("groups.ini still exists: %v", err)
			}
		})
	}
}

// A hand edit is read on the next check, not on every row.
func TestGroupsHandEdit(t *testing.T) {
	s := newGroupsServer(t)
	callGroups(t, s, "GET", "") // loads, and starts the recheck clock
	if err := os.WriteFile(s.User.Groups(), []byte("Edited = server\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.groups.mu.Lock()
	s.groups.checked = time.Time{} // the recheck interval has passed
	s.groups.mu.Unlock()
	r, _ := callGroups(t, s, "GET", "")
	if !r.Custom || r.Text != "Edited = server\n" {
		t.Fatalf("hand edit not read: %+v", r)
	}
	if !rowGroups(t, s)["my groups/edited"] {
		t.Error("the hand edit does not reach /api/dicts")
	}
}

func TestGroupsRefusals(t *testing.T) {
	t.Run("no config folder", func(t *testing.T) {
		s := newTestServer(t)
		if r, _ := callGroups(t, s, "GET", ""); r.Writable || r.Custom || r.Text != facet.DefaultText {
			t.Errorf("GET = %+v", r)
		}
		if _, code := callGroups(t, s, "PUT", "A = b"); code != 409 {
			t.Errorf("PUT code %d, want 409", code)
		}
	})
	s := newGroupsServer(t)
	if _, code := callGroups(t, s, "PUT", strings.Repeat("x", maxGroupsBytes+1)); code != 413 {
		t.Errorf("oversize: code %d, want 413", code)
	}
	if _, code := callGroups(t, s, "PUT", "A = \xff\xfe"); code != 400 {
		t.Errorf("not UTF-8: code %d, want 400", code)
	}
	if _, err := os.Stat(s.User.Groups()); !os.IsNotExist(err) {
		t.Error("a refused save wrote the file")
	}
}

// A groups.ini that cannot be used leaves the default in force and says why,
// without presenting the default as the user's file.
func TestGroupsUnusableFile(t *testing.T) {
	s := newGroupsServer(t)
	if err := os.Mkdir(s.User.Groups(), 0o755); err != nil {
		t.Fatal(err)
	}
	r, _ := callGroups(t, s, "GET", "")
	if !r.Custom || !r.Unusable || r.Text != "" || len(r.Problems) != 1 || r.Problems[0].Line != 0 {
		t.Errorf("unusable file: %+v", r)
	}
}

// A file that exists but cannot be used is the user's: the editor showed an
// empty box for it, so a save does not replace it unless the request says so.
func TestGroupsUnusableIsNotOverwritten(t *testing.T) {
	s := newGroupsServer(t)
	big := "Big = x\n" + strings.Repeat("; padding\n", maxGroupsBytes/10+1)
	if err := os.WriteFile(s.User.Groups(), []byte(big), 0o644); err != nil {
		t.Fatal(err)
	}
	if r, _ := callGroups(t, s, "GET", ""); !r.Unusable {
		t.Fatalf("an oversize file is not reported unusable: %+v", r)
	}
	if _, code := callGroups(t, s, "PUT", "Mine = a\n"); code != 409 {
		t.Errorf("PUT over an unusable file: code %d, want 409", code)
	}
	if b, _ := os.ReadFile(s.User.Groups()); string(b) != big {
		t.Fatal("a refused save changed the file")
	}
	r, code := callGroupsAt(t, s, "PUT", "/api/groups?replace=1", "Mine = a\n")
	if code != 200 || r.Unusable || r.Text != "Mine = a\n" {
		t.Fatalf("replace=1: code %d, %+v", code, r)
	}
}

// The cache follows the file's bytes, not its stat: saves and hand edits of
// the same size inside one mtime tick are still seen (FAT, FUSE storage).
func TestGroupsSameStatDifferentText(t *testing.T) {
	s := newGroupsServer(t)
	tick := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, body := range []string{"A = aaa\n", "B = bbb\n"} {
		r, code := callGroups(t, s, "PUT", body)
		if code != 200 || r.Text != body {
			t.Fatalf("PUT %q answered %q", body, r.Text)
		}
		os.Chtimes(s.User.Groups(), tick, tick)
	}
	if err := os.WriteFile(s.User.Groups(), []byte("C = ccc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	os.Chtimes(s.User.Groups(), tick, tick)
	s.groups.mu.Lock()
	s.groups.checked = time.Time{}
	s.groups.mu.Unlock()
	if r, _ := callGroups(t, s, "GET", ""); r.Text != "C = ccc\n" {
		t.Errorf("a same-size, same-mtime hand edit is not seen: %q", r.Text)
	}
}

// A file that could not be read is read again once it can be, with no change
// to its mtime or size.
func TestGroupsReadErrorRecovers(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs Unix permissions and a non-root user")
	}
	s := newGroupsServer(t)
	if err := os.WriteFile(s.User.Groups(), []byte("A = a\n"), 0o000); err != nil {
		t.Fatal(err)
	}
	if r, _ := callGroups(t, s, "GET", ""); !r.Unusable {
		t.Fatalf("an unreadable file is not reported: %+v", r)
	}
	os.Chmod(s.User.Groups(), 0o644)
	s.groups.mu.Lock()
	s.groups.checked = time.Time{}
	s.groups.mu.Unlock()
	if r, _ := callGroups(t, s, "GET", ""); r.Unusable || r.Text != "A = a\n" {
		t.Errorf("not recovered after chmod: %+v", r)
	}
}

// A save writes through a symlink and keeps the file's mode.
func TestGroupsSaveKeepsLinkAndMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	s := newGroupsServer(t)
	target := filepath.Join(t.TempDir(), "dotfiles-groups.ini")
	if err := os.WriteFile(target, []byte("Old = x\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, s.User.Groups()); err != nil {
		t.Fatal(err)
	}
	if _, code := callGroups(t, s, "PUT", "New = y\n"); code != 200 {
		t.Fatalf("PUT code %d", code)
	}
	if fi, err := os.Lstat(s.User.Groups()); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Error("the symlink was replaced by a file")
	}
	fi, err := os.Stat(target)
	if b, _ := os.ReadFile(target); err != nil || string(b) != "New = y\n" || fi.Mode().Perm() != 0o640 {
		t.Errorf("target = %q, mode %v", b, fi.Mode().Perm())
	}
}

// What groups.ini skipped is announced with the dictionary list, so the page
// can point at the editor without anyone opening it.
func TestGroupsProblemsReachTheList(t *testing.T) {
	s := newGroupsServer(t)
	if _, code := callGroups(t, s, "PUT", "G = `(`, ok\nno separator\n"); code != 200 {
		t.Fatal("PUT failed")
	}
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, newRequest("GET", "/api/dicts", nil))
	first, _, _ := strings.Cut(rec.Body.String(), "\n")
	var begin struct {
		T              string `json:"t"`
		GroupsProblems int    `json:"groupsProblems"`
	}
	if err := json.Unmarshal([]byte(first), &begin); err != nil || begin.T != "begin" || begin.GroupsProblems != 2 {
		t.Errorf("begin = %s, want groupsProblems 2", first)
	}
}

// Replacing a file this process could not read leaves one it can: the
// replacement keeps the old mode, plus the owner's read and write bits.
func TestGroupsReplaceUnreadable(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs Unix permissions and a non-root user")
	}
	s := newGroupsServer(t)
	if err := os.WriteFile(s.User.Groups(), []byte("Old = aa\n"), 0o000); err != nil {
		t.Fatal(err)
	}
	if _, code := callGroupsAt(t, s, "PUT", "/api/groups?replace=1", "New = bb\n"); code != 200 {
		t.Fatalf("replace code %d", code)
	}
	s.groups.mu.Lock()
	s.groups.checked = time.Time{}
	s.groups.mu.Unlock()
	if r, _ := callGroups(t, s, "GET", ""); r.Unusable || r.Text != "New = bb\n" {
		t.Errorf("after the next check: %+v", r)
	}
}

// The list's count is every problem, the ones past the listing cap included.
func TestGroupsProblemCountBeyondTheList(t *testing.T) {
	s := newGroupsServer(t)
	body := "G = " + strings.Repeat("`(`, ", 150) + "\n"
	r, code := callGroups(t, s, "PUT", body)
	if code != 200 || facet.CountProblems(r.Problems) != 150 || len(r.Problems) > 101 {
		t.Fatalf("code %d, %d listed, %d counted", code, len(r.Problems), facet.CountProblems(r.Problems))
	}
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, newRequest("GET", "/api/dicts", nil))
	first, _, _ := strings.Cut(rec.Body.String(), "\n")
	if !strings.Contains(first, `"groupsProblems":150`) {
		t.Errorf("begin = %s, want groupsProblems 150", first)
	}
}
