// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/wuweidict/wudict/internal/dict"
	"github.com/wuweidict/wudict/internal/logx"
	"github.com/wuweidict/wudict/internal/store"
)

// A dictionary's own font (D204): a file beside its source, named after it or
// the folder's font.ttf (dict.CompanionFont). The row says where it is served;
// the page makes it the font of every element of the dictionary's articles
// (index.html dictFont, D204 Am. 1).
//
// Served by FILE, not by dictionary (Am. 2): the dictionaries sharing a
// folder's font.ttf share one URL, so the page downloads and decodes it once
// however many of them it shows - a 20 MB CJK font, six dictionaries.
//
// Read from the source folder only, while it is there. Nothing is copied into
// the library folder, so the file being there is the whole switch: delete it
// and the dictionary goes back to the app's font.

// fontFile is what an entry's font resolved to: path ("" for none) and id,
// the file's URL key (pathID of the path), as found under dirSig, the source
// folder's signature then (sourceSig). ok is set once the file has been
// checked as a font, and only then is it served; bad once it has been checked
// and is not one (the row's FontError, the About line). Neither is set while
// it is only resolved (fontPath).
type fontFile struct {
	path, id, dirSig string
	ok, bad          bool
}

func newFontFile(path, dirSig string) *fontFile {
	ff := &fontFile{path: path, dirSig: dirSig}
	if path != "" {
		ff.id = pathID(path)
	}
	return ff
}

// fontUnusable is what a person reads about a bad font file, in the row and
// in the About.
func fontUnusable(path string) string {
	return filepath.Base(path) + " is not a font wudict can use."
}

// resolveFont finds the font file for e, or "" - never for a dictionary that
// ships with the app, or for one whose path is its prepared database (the
// source is gone, and there is no folder beside it to look in).
func (e *entry) resolveFont() string {
	if e.builtin || store.IsTextDB(e.Path) {
		return ""
	}
	p, _ := dict.CompanionFont(e.Path)
	return p
}

// fontPath is the entry's font file for the row key (dictrows.go), given the
// source folder's signature the key has just taken. Resolved again only when
// that signature moved - a font added or removed moves it - so a key for an
// unchanged folder costs no probe, and the key taken before a derivation is
// the key after it.
func (e *entry) fontPath(dirSig string) string {
	if f := e.font.Load(); f != nil && f.dirSig == dirSig {
		return f.path
	}
	ff := newFontFile(e.resolveFont(), dirSig)
	e.font.Store(ff)
	return ff.path
}

// deriveFont fills the row's font fields from the file as it is now.
func (e *entry) deriveFont(info *dictInfo) {
	ff, fi := e.checkFont()
	switch {
	case ff.bad:
		info.FontError = fontUnusable(ff.path)
	case ff.ok:
		info.Font = fontURL + ff.id + "?v=" + strconv.FormatInt(fi.Size(), 36) + "-" + strconv.FormatInt(fi.ModTime().UnixNano(), 36)
	}
}

// checkFont resolves e's font, checks it is one, and keeps the answer on the
// entry. A file that cannot be opened (gone between the stat and the open) is
// neither ok nor bad: the next row key notices.
func (e *entry) checkFont() (*fontFile, os.FileInfo) {
	ff := newFontFile(e.resolveFont(), sourceSig(filepath.Dir(e.Path)))
	defer e.font.Store(ff)
	if ff.path == "" {
		return ff, nil
	}
	f, fi, ok := openRegular(ff.path)
	if !ok {
		return ff, nil
	}
	defer f.Close()
	err := dict.CheckFont(f)
	if err != nil {
		logx.V("font %s: %v", ff.path, err)
	}
	ff.ok, ff.bad = err == nil, err != nil
	return ff, fi
}

// servableFont finds the font whose URL key is id among the entries, checked
// as a font in this process. Usually a row derivation has checked it; a page
// holding a list from before a restart (a 304 needs no derivation) may ask
// first, and that entry is checked now. An entry whose font no row key has
// resolved yet is resolved here, so such a page is not refused either.
func (s *Server) servableFont(id string) (string, bool) {
	var unresolved []*entry
	for _, e := range s.reg.all() {
		f := e.font.Load()
		if f == nil {
			unresolved = append(unresolved, e)
			continue
		}
		if f.id != id {
			continue
		}
		if f.ok {
			return f.path, true
		}
		if ff, _ := e.checkFont(); ff.ok && ff.id == id {
			return ff.path, true
		}
	}
	for _, e := range unresolved {
		if ff, _ := e.checkFont(); ff.ok && ff.id == id {
			return ff.path, true
		}
	}
	return "", false
}

// fontURL is the prefix a font is served under, by the file's key: the path
// is the server's to know, never the client's to name.
const fontURL = "/font/"

// handleFont serves /font/{key}. A page asset like /files/, not part of the
// extension contract (D69): the app page and its article frames are
// same-origin and carry the session.
//
// The URL carries the file's size and time, so a replaced font is a new URL
// and a day's cache never shows the old one.
func (s *Server) handleFont(w http.ResponseWriter, r *http.Request) {
	p, ok := s.servableFont(strings.TrimPrefix(r.URL.Path, fontURL))
	if !ok {
		http.NotFound(w, r)
		return
	}
	f, fi, ok := openRegular(p)
	if !ok {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", resolveMIME(p, "application/octet-stream"))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	http.ServeContent(w, r, "", fi.ModTime(), f)
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
