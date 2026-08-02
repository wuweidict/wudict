// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/legbehindneck/wudict/internal/config"
	"github.com/legbehindneck/wudict/internal/dict"
	"github.com/legbehindneck/wudict/internal/store"
)

// configInfo is what the panel's "Folders & configuration" section shows: the
// folders being scanned, where prepared dictionaries live, which config file
// is in effect — and, for each, WHICH layer set it. Everything here is
// otherwise visible only on the terminal at startup, which a user who opened
// the app in a browser never sees.
type configInfo struct {
	Roots      []Root `json:"roots"`
	LibDir     string `json:"libDir"`
	Prepared   int    `json:"prepared"`
	UseCached  bool   `json:"useCached"`
	ConfigPath string `json:"configPath"`
	Total      int    `json:"total"`

	// DictDirOrigin is "flag", "env", "file" or "default"; DictDirEditable is
	// false when a flag or environment variable outranks the config file, so
	// the UI can say that saving here will not take effect rather than
	// silently doing nothing on the next launch.
	DictDirOrigin   string `json:"dictDirOrigin"`
	DictDirEditable bool   `json:"dictDirEditable"`

	// RevealLabel is the platform's own name for "show this in the file
	// manager" — the phrase a user of that OS already recognises.
	RevealLabel string `json:"revealLabel"`
	CanReveal   bool   `json:"canReveal"`
}

// revealLabel returns the established wording for each desktop:
// macOS Finder says "Reveal in Finder", Windows Explorer "Show in File
// Explorer", and on Linux the desktop-neutral phrase GNOME Files and friends
// use is "Open Containing Folder".
func revealLabel() string {
	switch runtime.GOOS {
	case "darwin":
		return "Reveal in Finder"
	case "windows":
		return "Show in File Explorer"
	default:
		return "Open Containing Folder"
	}
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	lib, _ := store.Library()
	info := configInfo{
		Roots:           s.reg.Roots(),
		LibDir:          store.DefaultDBDir(),
		Prepared:        len(lib),
		UseCached:       s.reg.UseCached(),
		ConfigPath:      s.ConfigPath,
		Total:           s.reg.Count(),
		DictDirOrigin:   s.DictDirOrigin,
		DictDirEditable: s.DictDirEditable,
		RevealLabel:     revealLabel(),
		// revealing opens a window on the machine running the server, which is
		// only useful when that is also the machine at the keyboard
		CanReveal: isLoopback(r),
	}
	if info.DictDirOrigin == "" {
		info.DictDirOrigin = config.OriginDefault
	}
	writeJSON(w, info)
}

// isLoopback reports whether the request came from this machine. A remote
// browser must not be offered a button that opens a file manager window on
// someone else's desktop.
func isLoopback(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// handleReveal shows a folder (or a file, selected inside its folder) in the
// desktop's file manager.
func (s *Server) handleReveal(w http.ResponseWriter, r *http.Request) {
	if !isLoopback(r) {
		httpErr(w, 403, "only available from the machine running wudict")
		return
	}
	path := strings.TrimSpace(r.URL.Query().Get("path"))
	if path == "" {
		httpErr(w, 400, "missing path parameter")
		return
	}
	if !s.revealAllowed(path) {
		// never hand an arbitrary path to the shell: only the folders and
		// files this app is already showing may be opened.
		httpErr(w, 403, "not a wudict folder")
		return
	}
	if err := reveal(path); err != nil {
		httpErr(w, 500, "%v", err)
		return
	}
	writeJSON(w, map[string]any{"revealed": path})
}

// revealAllowed limits reveal to what the UI legitimately displays: a
// dictionary folder or anything under it, the library or anything under it,
// and the config file itself.
func (s *Server) revealAllowed(path string) bool {
	if s.ConfigPath != "" && dict.SameDir(path, s.ConfigPath) {
		return true
	}
	roots := append(s.reg.Dirs(), store.DefaultDBDir())
	for _, root := range roots {
		if within(root, path) {
			return true
		}
	}
	return false
}

// within reports whether path is root or lives underneath it, comparing
// cleaned absolute paths (so "/a/b" contains "/a/b/c" but not "/a/bc").
func within(root, path string) bool {
	r, err1 := filepath.Abs(root)
	p, err2 := filepath.Abs(path)
	if err1 != nil || err2 != nil {
		return false
	}
	r, p = filepath.Clean(r), filepath.Clean(p)
	if r == p {
		return true
	}
	rel, err := filepath.Rel(r, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// reveal opens the platform file manager on path. Each OS gets the command its
// users expect: Finder selects the item, Explorer selects it, and on Linux
// xdg-open hands the *containing folder* to whatever file manager is
// installed (selection is not portable there).
func reveal(path string) error {
	switch runtime.GOOS {
	case "darwin":
		// -R reveals the item in its parent folder, selected
		return exec.Command("open", "-R", path).Start()
	case "windows":
		// explorer exits with a non-zero status even on success, so Start()
		// (which does not wait) is both correct and simpler here
		return exec.Command("explorer", "/select,"+filepath.Clean(path)).Start()
	default:
		dir := path
		if fi, err := os.Stat(path); err == nil && !fi.IsDir() {
			dir = filepath.Dir(path)
		}
		if _, err := exec.LookPath("xdg-open"); err != nil {
			return fmt.Errorf("no file manager available (xdg-open not found)")
		}
		return exec.Command("xdg-open", dir).Start()
	}
}
