// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/wuweidict/wudict/internal/logx"
)

// Global user styling: two CSS files the user owns, applied to everything.
//
// D59 already lets a user replace a file a dictionary ships
// (<library folder>/res/<name>). That is per-dictionary and whole-file, and it
// cannot express either of the two things people actually ask for: "this
// dictionary's desktop side padding wastes half my phone screen" (one rule,
// but res/ demands a copy of the whole stylesheet, which then goes stale) and
// "give me sepia" (the chrome is not a dictionary resource at all). So this is
// the complementary layer: ADDITIVE, GLOBAL, and ordered last so it wins.
//
// Two files rather than one, because a single box leaks in the damaging
// direction: body{}, p{}, a{}, table{} and div{} are exactly what someone
// styling articles writes, and every one of them would also hit the chrome.
// Two boxes make that structurally impossible, and the split is one sentence:
// app.css is colours and the app's own layout, article.css is what
// dictionaries render. They do not fragment the "sepia everywhere" case,
// because :root custom properties set in app.css already inherit into every
// shadow root and are carried into iframes by the existing bridge - so tokens
// are written once, in app.css, and reach all three surfaces.
//
// Files on disk beside the wudict.toml in effect, not a blob in state.json:
// CSS is multi-line and users will want a real editor, a diff and a backup,
// and the res/ precedent is already "a file on disk, served no-cache, an edit
// lands on reload". state.json is for settings; this is a document.
const (
	// StyleDirName is the folder, beside the config file in effect, that holds
	// the two files. The CLI resolves the parent (see userDir), the same way
	// it resolves StateFile, so a portable install (D32) keeps its styling on
	// the same stick as its config.
	StyleDirName = "style"

	appCSSName     = "app.css"
	articleCSSName = "article.css"

	// maxUserCSSBytes caps one file. Generous for hand-written CSS by three
	// orders of magnitude, and the only thing standing between a PUT and the
	// user's disk.
	maxUserCSSBytes = 256 << 10
)

// styleNames is the whole namespace, stated once. Membership is checked by
// equality against it rather than by cleaning a path: there is no traversal to
// defend against when the only two reachable names are literals.
var styleNames = []string{appCSSName, articleCSSName}

func isStyleName(n string) bool {
	for _, s := range styleNames {
		if s == n {
			return true
		}
	}
	return false
}

// stylePath is the file's location, or "" when there is nowhere to put it -
// which is the same "no config file and no home directory" case that makes
// state in-memory rather than scattering a file somewhere nobody asked for.
func (s *Server) stylePath(name string) string {
	if s.StyleDir == "" || !isStyleName(name) {
		return ""
	}
	return filepath.Join(s.StyleDir, name)
}

// styleRead returns the file's bytes, or nil. It never fails: absent is the
// normal case, and an unreadable file must not stop the app from serving
// dictionaries - the worst outcome is unstyled, which is where everyone
// starts.
func (s *Server) styleRead(name string) []byte {
	p := s.stylePath(name)
	if p == "" {
		return nil
	}
	b, err := os.ReadFile(p)
	if err != nil {
		if !os.IsNotExist(err) {
			logx.Warn("could not read %s: %v", p, err)
		}
		return nil
	}
	if len(b) > maxUserCSSBytes {
		logx.Warn("%s is larger than %d bytes and is being ignored", p, maxUserCSSBytes)
		return nil
	}
	return b
}

// styleWrite replaces one file, temp-file + rename so a crash mid-save leaves
// the previous stylesheet intact rather than a truncated one. Empty content
// REMOVES the file: "I cleared the box" and "there is a file here containing
// nothing" must not be two different states, or the next reader has to
// explain the difference to somebody.
func (s *Server) styleWrite(name, body string) error {
	p := s.stylePath(name)
	if p == "" {
		return os.ErrPermission
	}
	if body == "" {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".style-*.css")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(body); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), p)
}

// appStyleTag is the content hash index.html stamps into its <link>, or "" when
// there is no app stylesheet. Same content addressing as assetTag's other
// callers (D45): the URL changes exactly when the bytes do.
func (s *Server) appStyleTag() string {
	b := s.styleRead(appCSSName)
	if len(b) == 0 {
		return ""
	}
	return assetTag(b)
}

// styleOff reports the escape hatch. app.css can hide its own editor - one
// `*{display:none}` is enough - so there has to be a way back in that does not
// require finding a text editor on a phone. ?style=off omits the chrome
// stylesheet and tells the client to skip the article one.
func styleOff(r *http.Request) bool {
	return r.URL.Query().Get("style") == "off"
}

// GET /style/app.css and /style/article.css.
//
// no-cache, exactly like a res/ override and for the same reason: this is a
// file the user is actively editing, and a cache that outlives their next
// attempt would look like the feature being broken. An absent file is 200 with
// an empty body rather than 404 - it is a stylesheet that says nothing, not a
// missing resource, and a 404 in the network panel would send someone hunting
// for a bug that is not there.
func (s *Server) handleUserCSS(w http.ResponseWriter, r *http.Request) {
	// The exact path, not filepath.Base: /style/anything/app.css must be a 404
	// and not a second URL for the same file. There are two names here, and
	// each of them has one address.
	name := strings.TrimPrefix(r.URL.Path, "/style/")
	if !isStyleName(name) {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	if styleOff(r) {
		return
	}
	_, _ = w.Write(s.styleRead(name))
}

// GET /api/style - both files, plus where they live and whether they can be
// written. The path is reported because the editor shows it: an in-app editor
// that hides which file it is editing turns a plain text file into a black
// box, and the point of using files was that an external editor works too.
func (s *Server) handleStyle(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{
		"app":      string(s.styleRead(appCSSName)),
		"article":  string(s.styleRead(articleCSSName)),
		"dir":      s.StyleDir,
		"writable": s.StyleDir != "",
	})
}

// PUT /api/style - replace either file, or both.
//
// Pointers, not strings: absent and empty are different. The App tab and the
// Article tab are the same editor sending different bodies, and a request that
// omits one must never be read as clearing it - the same trap /api/prefs has
// with `ui`.
func (s *Server) handleSaveStyle(w http.ResponseWriter, r *http.Request) {
	var req struct {
		App     *string `json:"app"`
		Article *string `json:"article"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4*maxUserCSSBytes+(1<<12))).Decode(&req); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}
	if s.StyleDir == "" {
		http.Error(w, "no config directory: there is nowhere to save custom styles", http.StatusConflict)
		return
	}
	for _, f := range []struct {
		name string
		body *string
	}{{appCSSName, req.App}, {articleCSSName, req.Article}} {
		if f.body == nil {
			continue
		}
		if len(*f.body) > maxUserCSSBytes {
			http.Error(w, f.name+" is too large", http.StatusRequestEntityTooLarge)
			return
		}
		if err := s.styleWrite(f.name, *f.body); err != nil {
			http.Error(w, "could not save "+f.name+": "+err.Error(), http.StatusInternalServerError)
			return
		}
	}
	s.handleStyle(w, r)
}
