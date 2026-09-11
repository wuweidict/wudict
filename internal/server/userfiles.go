// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// The user's own file store: whatever they need their custom CSS - or their
// own hand-written dictionary - to be able to reference by URL.
//
// The problem it closes is narrow and was reported as a bug. Custom CSS
// (style.go) can say background-image:url(...), but there was nothing a user
// could put in those parentheses: file:// is refused by every browser as a
// subresource of an http page, and the one writable-by-URL surface that
// existed, the D59 res/ override, is addressed by a dictionary id the UI
// deliberately never shows (D102) and disappears when that dictionary is
// removed. So the store is global, lives beside the stylesheets it serves, and
// is addressed by the name the user sees.
//
// Deliberately NOT restricted by type. The store is "files the page can reach
// by URL", not "images for backgrounds": fonts (@font-face), an imported
// stylesheet, a script, a PDF a private dictionary links to, all work through
// the same door, and an extension allowlist would only mean the feature is
// missing for whoever needed the entry it lacks. It is the user's own machine,
// reachable from loopback and behind the same capability token as everything
// else; what it is not is a place to smuggle content in from, which is why the
// caps below are small and the name is not a path.
const (
	// assetsDirName sits under StyleDir, so the whole customisation surface -
	// two stylesheets and their files - is one folder to back up, sync or
	// carry on a stick (D32).
	assetsDirName = "assets"

	// One file. Large enough for a wallpaper, a font family or a small PDF,
	// small enough that a mis-aimed upload fails fast rather than filling a
	// phone.
	maxUserFileBytes = 8 << 20
	// The whole store, and how many. Two limits rather than one: 64 files of
	// 8 MiB is 512 MiB, and "the total is fine" must not be reachable by a
	// thousand tiny files either.
	maxUserFilesBytes = 64 << 20
	maxUserFiles      = 64
)

// userFileRe is the whole name grammar: one path segment, ASCII, starting with
// a letter or digit. It is an allowlist and not a cleaning pass for the same
// reason isStyleName is an equality check - a name that cannot contain a
// slash, a backslash, a colon or a leading dot cannot climb out of the
// directory, name a Windows device, or hide itself, so there is no traversal
// to defend against further down.
var userFileRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

func isUserFileName(n string) bool { return userFileRe.MatchString(n) }

// userFilesDir is the folder, or "" when there is nowhere to put one - the
// same "no config file and no home directory" case that leaves state in
// memory.
func (s *Server) userFilesDir() string {
	if s.StyleDir == "" {
		return ""
	}
	return filepath.Join(s.StyleDir, assetsDirName)
}

// userFilePath is one file's location, or "" when the name is not one this
// store can hold.
func (s *Server) userFilePath(name string) string {
	d := s.userFilesDir()
	if d == "" || !isUserFileName(name) {
		return ""
	}
	return filepath.Join(d, name)
}

// userFile is one row of the manager: what the UI needs to show it, insert a
// reference to it, and decide whether it is worth a thumbnail.
type userFile struct {
	Name  string `json:"name"`
	Size  int64  `json:"size"`
	MTime int64  `json:"mtime"` // unix seconds
	Type  string `json:"type"`  // resolved from the extension, "" when unknown
	URL   string `json:"url"`
}

// userFileList reads the store. It never fails: an absent or unreadable folder
// is an empty store, which is where everyone starts, and a name that no longer
// matches the grammar (someone dropped it in from a shell) is skipped rather
// than listed as something the UI cannot address.
func (s *Server) userFileList() []userFile {
	d := s.userFilesDir()
	if d == "" {
		return nil
	}
	ents, err := os.ReadDir(d)
	if err != nil {
		return nil
	}
	out := make([]userFile, 0, len(ents))
	for _, e := range ents {
		if e.IsDir() || !isUserFileName(e.Name()) {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, userFile{
			Name:  e.Name(),
			Size:  fi.Size(),
			MTime: fi.ModTime().Unix(),
			Type:  resolveMIME(e.Name(), ""),
			URL:   userFileURL + e.Name(),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// userFileURL is the prefix the files are served under, stated once because
// three places need to agree on it: the route, the JSON the manager renders,
// and the rewriter exemption that keeps a dictionary's own markup from having
// /files/x.png rewritten into /res/<id>/files/x.png.
const userFileURL = "/files/"

// GET /files/<name>.
//
// no-cache, exactly like the stylesheets and the res/ overrides: these are
// files the user is actively replacing, and a cache that outlived their next
// upload would look like the feature being broken. nosniff because the store
// holds arbitrary bytes under a name the user chose - the declared type is the
// only type, and a .txt must never be executed because it happens to start
// with markup.
//
// Auth-gated like everything outside authFree. The two surfaces that consume
// these files both carry the session cookie: the app page is same-origin, and
// the article iframe is a srcdoc with allow-same-origin, so its subresource
// requests are same-origin too.
func (s *Server) handleUserFile(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, userFileURL)
	p := s.userFilePath(name)
	if p == "" {
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(p)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || fi.IsDir() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", resolveMIME(name, "application/octet-stream"))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeContent(w, r, name, fi.ModTime(), f)
}

// userFilesPayload is what the manager renders from: the rows, where they
// live, whether they can be written, and the limits - reported rather than
// hard-coded in the page, so the numbers cannot drift between the check and
// the message that explains it.
func (s *Server) userFilesPayload() map[string]any {
	files := s.userFileList()
	var total int64
	for _, f := range files {
		total += f.Size
	}
	return map[string]any{
		"files":    files,
		"dir":      s.userFilesDir(),
		"writable": s.StyleDir != "",
		"total":    total,
		"limits": map[string]any{
			"file":  int64(maxUserFileBytes),
			"total": int64(maxUserFilesBytes),
			"count": maxUserFiles,
		},
	}
}

// GET /api/files
func (s *Server) handleFiles(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.userFilesPayload())
}

// POST /api/files?name=<name>[&replace=1] - the body is the file, raw.
//
// Raw rather than multipart: there is exactly one file per request and no
// other field, so multipart would add a parser and a temp-file spill to carry
// nothing. ?replace=1 rather than a silent overwrite - an upload that quietly
// destroys a file the user's CSS already points at is the one mistake this
// endpoint can make that they cannot undo.
func (s *Server) handleFileUpload(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if !isUserFileName(name) {
		http.Error(w, "bad request: name must be 1-64 characters of A-Z a-z 0-9 . _ - and start with a letter or digit", http.StatusBadRequest)
		return
	}
	dir := s.userFilesDir()
	if dir == "" {
		http.Error(w, "no config directory: there is nowhere to save files", http.StatusConflict)
		return
	}
	replace := r.URL.Query().Get("replace") == "1"

	// The caps are measured against the store as it will be AFTER the write,
	// so replacing an 8 MiB file with a smaller one is never refused for being
	// the 65th file or for a total it is about to reduce.
	var total int64
	count := 0
	exists := false
	for _, f := range s.userFileList() {
		if f.Name == name {
			exists = true
			continue
		}
		total += f.Size
		count++
	}
	if exists && !replace {
		http.Error(w, "a file named "+name+" is already there", http.StatusConflict)
		return
	}
	if count+1 > maxUserFiles {
		http.Error(w, "too many files", http.StatusRequestEntityTooLarge)
		return
	}

	// One byte past the cap, so a body that is exactly at the limit is
	// accepted and the first one over is refused by length rather than by the
	// reader erroring at an arbitrary point.
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxUserFileBytes+1))
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			http.Error(w, "file is too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}
	if len(body) == 0 {
		http.Error(w, "bad request: empty file", http.StatusBadRequest)
		return
	}
	if len(body) > maxUserFileBytes {
		http.Error(w, "file is too large", http.StatusRequestEntityTooLarge)
		return
	}
	if total+int64(len(body)) > maxUserFilesBytes {
		http.Error(w, "not enough room: the files folder would be over its limit", http.StatusRequestEntityTooLarge)
		return
	}

	if err := s.userFileWrite(name, body); err != nil {
		http.Error(w, "could not save "+name+": "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, s.userFilesPayload())
}

// userFileWrite replaces one file, temp-file + rename like styleWrite: a crash
// or a dropped connection mid-upload leaves the previous file intact rather
// than a truncated one that the page would then render as a broken image.
func (s *Server) userFileWrite(name string, body []byte) error {
	p := s.userFilePath(name)
	if p == "" {
		return os.ErrPermission
	}
	dir := filepath.Dir(p)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".upload-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), p)
}

// DELETE /api/files?name=<name>. Removing what is not there is a success: the
// caller asked for it to be gone and it is, and a 404 would only mean the
// manager has to explain a race it cannot prevent.
func (s *Server) handleFileDelete(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	p := s.userFilePath(name)
	if p == "" {
		http.Error(w, "bad request: unknown file name", http.StatusBadRequest)
		return
	}
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		http.Error(w, "could not remove "+name+": "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, s.userFilesPayload())
}
