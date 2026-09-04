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
	"sort"
	"strings"

	"github.com/wuweidict/wudict/internal/config"
	"github.com/wuweidict/wudict/internal/dict"
	"github.com/wuweidict/wudict/internal/store"
)

// configInfo is what the panel's "Folders & configuration" section shows: the
// folders being scanned, where prepared dictionaries live, which config file
// is in effect - and, for each, WHICH layer set it. Everything here is
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
	// manager" - the phrase a user of that OS already recognises.
	RevealLabel string `json:"revealLabel"`
	CanReveal   bool   `json:"canReveal"`

	// CanDelete says this caller may remove a dictionary (D63 amended). True
	// from the machine running wudict, whatever platform that is - the user at
	// the keyboard owns those files, and managing a library includes throwing
	// part of it away. From a remote browser it follows ALLOW_REMOTE_DELETE,
	// which defaults to OFF. It used to be CanReveal's exact complement, which
	// withheld the control from precisely the user who owns the files and
	// handed it to every browser on the LAN.
	CanDelete bool `json:"canDelete"`

	// PathAliases shorten the prefix that is identical on every row and
	// therefore carries no information: {prefix, label} pairs, first match
	// wins. On a desktop that is one entry, the home directory shown as "~"
	// (D48). DISPLAY ONLY: the clipboard and /api/reveal keep the absolute
	// path, which is what a user pasting into a script - or the file manager -
	// needs. A label is a NAME, not a path: "~" is one only where a shell
	// would accept it, which is why the platform decides (see pathAliases).
	PathAliases [][2]string `json:"pathAliases,omitempty"`
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

// pathAliases builds the display shortenings for the paths this panel is about
// to show. The SERVER decides them because only the server knows which machine
// it is on, and a label is a claim about that machine: "~" says "a shell here
// would expand this", which is true on a desktop and false on Android, where
// HOME is the app's external files dir - a directory with no shell, that cannot
// be typed anywhere, and that no file manager may open. Android instead gets
// the names the Files app itself uses for the volumes, so a path the user reads
// here is a path they can find. The page applies the pairs blindly (D54: the
// page must not learn what Android is).
func pathAliases(paths []string) [][2]string {
	if runtime.GOOS == "android" {
		return androidAliases(paths)
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return [][2]string{{filepath.Clean(home), "~"}}
	}
	return nil
}

// androidAliases names the storage volumes the given paths actually live on.
// Derived from the paths rather than fixed, so a device with no removable card
// is never told about one, and the aliases are sorted longest-prefix-first so a
// more specific mount wins over the volume that contains it.
//
// The app's own directories are NOT hidden: on this platform the two flavours
// (D52) put dictionaries in different places, and "Internal storage/Android/
// data/…" is exactly what a user comparing the panel with the Files app sees.
func androidAliases(paths []string) [][2]string {
	var out [][2]string
	seen := map[string]bool{}
	add := func(prefix, label string) {
		if prefix == "" || seen[prefix] {
			return
		}
		seen[prefix] = true
		out = append(out, [2]string{prefix, label})
	}
	// under reports whether p is root or lives under it, returning the
	// remainder. String work, not filepath.Rel: these are Android mount points,
	// spelled the same way on every device, and "/storage/emulatedX" must not
	// match "/storage/emulated".
	under := func(p, root string) (string, bool) {
		if p == root {
			return "", true
		}
		if strings.HasPrefix(p, root+"/") {
			return p[len(root)+1:], true
		}
		return "", false
	}
	for _, p := range paths {
		if p == "" {
			continue
		}
		p = filepath.Clean(p)
		if _, ok := under(p, "/sdcard"); ok {
			// the legacy symlink for the primary volume
			add("/sdcard", "Internal storage")
			continue
		}
		if _, ok := under(p, "/storage/emulated/0"); ok {
			add("/storage/emulated/0", "Internal storage")
			continue
		}
		if rest, ok := under(p, "/storage"); ok {
			id := rest
			if i := strings.IndexByte(id, '/'); i >= 0 {
				id = id[:i]
			}
			// "self" is a per-process symlink, "emulated" without the user id
			// is not a volume: neither is a name to show anyone.
			if id == "" || id == "self" || id == "emulated" {
				continue
			}
			// The id IS what a removable volume is called at the mount point
			// (a UUID like 1A2B-3C4D); carrying it distinguishes two cards.
			add("/storage/"+id, "SD card ("+id+")")
			continue
		}
		// app-private internal flash: /data/user/<n>/<pkg> or /data/data/<pkg>
		for _, root := range []string{"/data/user", "/data/data"} {
			rest, ok := under(p, root)
			if !ok || rest == "" {
				continue
			}
			seg := strings.Split(rest, "/")
			n := 2 // /data/user/<n>/<pkg>
			if root == "/data/data" {
				n = 1
			}
			if len(seg) < n {
				break
			}
			add(root+"/"+strings.Join(seg[:n], "/"), "App storage")
			break
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return len(out[i][0]) > len(out[j][0]) })
	return out
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	lib, _ := store.Library()
	roots := s.reg.Roots()
	shown := []string{store.DefaultDBDir(), s.ConfigPath}
	for _, r := range roots {
		shown = append(shown, r.Path)
	}
	info := configInfo{
		PathAliases:     pathAliases(shown),
		Roots:           roots,
		LibDir:          store.DefaultDBDir(),
		Prepared:        len(lib),
		UseCached:       s.reg.UseCached(),
		ConfigPath:      s.ConfigPath,
		Total:           s.reg.Count(),
		DictDirOrigin:   s.DictDirOrigin,
		DictDirEditable: s.DictDirEditable,
		RevealLabel:     revealLabel(),
		// revealing opens a window on the machine running the server, which is
		// only useful when that is also the machine at the keyboard - and only
		// works where there is something to open it with. Offering a control
		// whose command does not exist (Android has no xdg-open) is worse than
		// not offering it: the click fails, and the user concludes the files
		// are unreachable rather than that this button is.
		CanReveal: isLoopback(r) && revealPossible(),
		// Deleting, unlike revealing, is something this app can always do
		// itself - so the question is only WHO is asking (D63 amended).
		CanDelete: s.removalOffered(r),
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

// revealPossible reports whether this machine has a file manager reveal can
// drive. A var so a test can state which world it is testing rather than
// inheriting the one it happens to run on.
var revealPossible = func() bool {
	switch runtime.GOOS {
	case "darwin", "windows":
		return true // open(1) and explorer.exe are part of the OS
	default:
		// Linux, and Android - where there is no xdg-open, no file manager,
		// and (since Android 11) nothing that may open the app's data dir.
		_, err := exec.LookPath("xdg-open")
		return err == nil
	}
}

// Reveal shows path in the platform file manager. Exported for the tray's
// "Open dictionary folder" item (D74), which drives the same action as
// /api/reveal without going through the mux.
func Reveal(path string) error { return reveal(path) }

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
		// Hand over the foreground right first, so the window that appears is
		// focused instead of merely flashing in the taskbar. Only effective
		// when this process holds that right - the tray menu, not the web
		// panel; see allowForeground in foreground_windows.go.
		allowForeground()
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
