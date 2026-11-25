// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/wuweidict/wudict/internal/dict"
	"github.com/wuweidict/wudict/internal/logx"
	"github.com/wuweidict/wudict/internal/resource"
	"github.com/wuweidict/wudict/internal/store"
)

// A dictionary's companion files: the font (D204) and the stylesheets (D205)
// beside its source that the page applies to its articles itself - the
// file named after it, and the folder's font.ttf and custom.css
// (dict.CompanionFont, dict.CompanionCSS). The row says where each is
// served; the page declares the font over every element and links the
// stylesheets after the dictionary's own (index.html dictSide).
//
// Read from the source folder only, while it is there. Nothing is copied into
// the library folder, so a file being there is the whole switch: delete it and
// the dictionary goes back to how it looked without it.

// companionFiles is what an entry's companions resolved to, as found under
// dirSig, the source folder's signature then (sourceSig): font is a path or
// "", css paths in cascade order. fontOK is set once the font has been checked
// (deriveCompanions), fontBad once it has been checked and is not one (the
// row's FontError, the About line); neither while it is only resolved
// (companionPaths).
type companionFiles struct {
	dirSig          string
	font            string
	css             []string
	fontOK, fontBad bool
}

// resolveCompanions finds e's companion files - never for a dictionary that
// ships with the app, or for one whose path is its prepared database (the
// source is gone, and there is no folder beside it to look in).
func (e *entry) resolveCompanions(dirSig string) *companionFiles {
	c := &companionFiles{dirSig: dirSig}
	if e.builtin || store.IsTextDB(e.Path) {
		return c
	}
	c.font, _ = dict.CompanionFont(e.Path)
	c.css, _ = dict.CompanionCSS(e.Path)
	return c
}

// companionPaths is the entry's companion files for the row key
// (dictrows.go), given the source folder's signature the key has just taken.
// Resolved again only when that signature moved - a file added or removed
// moves it - so a key for an unchanged folder costs no probe, and the key
// taken before a derivation is the key after it.
func (e *entry) companionPaths(dirSig string) *companionFiles {
	if c := e.companions.Load(); c != nil && c.dirSig == dirSig {
		return c
	}
	c := e.resolveCompanions(dirSig)
	e.companions.Store(c)
	return c
}

// deriveCompanions fills the row's companion fields from the files as they
// are now.
func (e *entry) deriveCompanions(info *dictInfo) {
	c := e.resolveCompanions(sourceSig(filepath.Dir(e.Path)))
	defer e.companions.Store(c)
	if c.font == "" && len(c.css) == 0 {
		return
	}
	key := pathID(filepath.Dir(e.Path))
	for _, p := range c.css {
		if fi, err := os.Stat(p); err == nil {
			info.CSS = append(info.CSS, folderURLOf(key, p, fi))
		}
	}
	if c.font == "" {
		return
	}
	f, fi, ok := openRegular(c.font)
	if !ok {
		return // gone between the stat and the open: the next key notices
	}
	defer f.Close()
	if err := dict.CheckFont(f); err != nil {
		logx.V("font %s: %v", c.font, err)
		c.fontBad = true
		info.FontError = fontUnusable(c.font)
		return
	}
	c.fontOK = true
	info.Font = folderURLOf(key, c.font, fi)
}

// fontUnusable is what a person reads about a bad font file, in the row and
// in the About.
func fontUnusable(path string) string {
	return filepath.Base(path) + " is not a font wudict can use."
}

// folderURL is the prefix a source folder's companion files are served under:
// /folder/{key}/{name}, key the folder's pathID. One URL per FILE - the
// dictionaries sharing a folder's font.ttf or custom.css share it, so the page
// fetches and decodes it once - and a stylesheet's relative url(bg.png)
// resolves into the same folder.
const folderURL = "/folder/"

// folderURLOf is p's URL, versioned by its size and time: a replaced file is
// a new URL, so a day's cache never shows the old one.
func folderURLOf(key, p string, fi os.FileInfo) string {
	return folderURL + key + "/" + url.PathEscape(filepath.Base(p)) +
		"?v=" + strconv.FormatInt(fi.Size(), 36) + "-" + strconv.FormatInt(fi.ModTime().UnixNano(), 36)
}

// handleFolder serves /folder/{key}/{name}: a file in the folder a registered
// dictionary's source sits in, by exact name and only if it is an asset
// (resource.NewDirExact, the guard every format's loose files go through). A
// page asset like /files/, not part of the extension contract (D69): the app
// page and its article frames are same-origin and carry the session.
//
// The key is found by walking the entries, ~160 short hashes: a file is asked
// for once per page and then cached, so a table to keep in step with every
// rescan would cost more than it saves.
func (s *Server) handleFolder(w http.ResponseWriter, r *http.Request) {
	key, name, ok := strings.Cut(strings.TrimPrefix(r.URL.Path, folderURL), "/")
	if !ok || name == "" {
		http.NotFound(w, r)
		return
	}
	dir := ""
	for _, e := range s.reg.all() {
		if e.builtin || store.IsTextDB(e.Path) {
			continue
		}
		if d := filepath.Dir(e.Path); pathID(d) == key {
			dir = d
			break
		}
	}
	p := ""
	if dir != "" {
		p = resource.NewDirExact(dir).ExactPath(name)
	}
	if p == "" {
		http.NotFound(w, r)
		return
	}
	f, fi, ok := openRegular(p)
	if !ok {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// A URL from a row carries the file's version (folderURLOf), so a day's
	// cache is safe; one a stylesheet names itself (url(paper.png)) does not,
	// and is revalidated instead - Last-Modified makes that a 304.
	cache := "no-cache"
	if r.URL.Query().Has("v") {
		cache = "public, max-age=86400"
	}
	serveLocalFile(w, r, name, f, fi, cache)
}

// openRegular opens path for serving when it is a regular file; ok is false
// for anything else, which every caller answers as "not here".
func openRegular(path string) (*os.File, os.FileInfo, bool) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, false
	}
	fi, err := f.Stat()
	if err != nil || fi.IsDir() {
		f.Close()
		return nil, nil, false
	}
	return f, fi, true
}
