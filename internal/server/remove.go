// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/wuweidict/wudict/internal/dict"
	"github.com/wuweidict/wudict/internal/logx"
	"github.com/wuweidict/wudict/internal/store"
)

// Removing a dictionary from the running app (D63).
//
// D7 answered "how does a user delete a dictionary?" with *the file manager
// does that*, and gave every path a Reveal control to get there in one click.
// That is a delegation, and it has a precondition: another custodian exists
// and the user can reach it. Android falsifies the precondition - the library
// and (in the Play flavour) the dictionaries themselves live in
// /sdcard/Android/data/<pkg>/files, which DocumentsUI filters out of the
// picker, which every third-party file manager is locked out of since Android
// 11, and which ACTION_OPEN_DOCUMENT_TREE refuses to grant. The only
// operations Android defines on those bytes are *uninstall the app* and *clear
// storage*, both of which take the whole library and the config with them.
//
// So this is not "a delete button for Android". It is the missing primitive:
// wudict had no removal at any layer - not in the API, not in the CLI, only
// `clean` for orphans - and the desktop hid that behind Finder. The endpoint
// is offered exactly where the handoff is not (see removalOffered), the CLI
// has it everywhere, and the desktop UI is unchanged.
//
// Two objects, never conflated:
//
//   - the PREPARED FOLDER (<db dir>/<name>/) - ours, D20-shaped, unambiguous.
//   - the ORIGINAL FILES - a dictionary is rarely one file, so the set comes
//     from dict.SourceFiles and is shown to the user before anything happens.
//
// And the trap that sets the default: preparation is automatic (AUTO_INDEX).
// Deleting only the prepared folder of a dictionary whose source is still in a
// scanned folder frees space until the next search re-prepares it. Removing
// both is therefore the default, and "index only" reports what will happen.

// removal is what a removal did - reported back so the UI states outcomes
// rather than assuming them, and so a partial failure is visible.
type removal struct {
	Name    string   `json:"name"`
	Folder  string   `json:"folder,omitempty"`  // prepared folder removed
	Sources []string `json:"sources,omitempty"` // original files removed
	Freed   int64    `json:"freed"`             // bytes
	Gone    bool     `json:"gone"`              // no longer listed at all
	Note    string   `json:"note,omitempty"`    // consequence the user should know
}

// Remove deletes a dictionary's prepared folder, its original files, or both,
// and rescans so the registry reflects what is actually on disk.
//
// dropPrepared+dropSource is "remove this dictionary". dropPrepared alone
// frees the indexes and leaves a dictionary that will be re-indexed on next
// use. dropSource alone is the Android-shaped case - reclaim the imported
// original and keep the prepared dictionary, which D24 §4 already governs:
// the source is the safety net, so it may only be cut once the media that
// would be lost with it has been packed.
func (r *Registry) Remove(id string, dropPrepared, dropSource bool) (removal, error) {
	var rep removal
	if !dropPrepared && !dropSource {
		return rep, fmt.Errorf("nothing to remove")
	}
	e, err := r.get(id)
	if err != nil {
		return rep, err
	}
	// Blocks (and is blocked by) an ingest on this dictionary: deleting the
	// folder a rebuild is writing into would leave the rebuild finishing into
	// nowhere.
	e.ingestMu.Lock()
	defer e.ingestMu.Unlock()

	rep.Name = e.probeName()
	native := store.IsTextDB(e.Path)

	prepared := ""
	if native {
		// the entry IS the prepared dictionary: its folder is its parent
		prepared = filepath.Dir(e.Path)
	} else if dir, ok := store.LookupDir(e.Path); ok {
		prepared = dir
	}
	var sources []string
	if !native {
		sources = dict.SourceFiles(e.Path)
	}

	if dropSource && len(sources) == 0 {
		if !dropPrepared {
			return rep, fmt.Errorf("%q has no original files - it is the prepared dictionary itself", rep.Name)
		}
		// "remove this dictionary" on a dictionary that IS the prepared folder:
		// there is nothing else to remove, so this is that request satisfied,
		// not a request that cannot be met.
		dropSource = false
	}
	if dropPrepared && prepared == "" && !dropSource {
		return rep, fmt.Errorf("%q has nothing prepared to remove", rep.Name)
	}
	if dropSource && !dropPrepared {
		// D24 §4, read in the other direction. Media that is not packed lives
		// only in the original, and the prepared text.db would keep serving
		// articles whose images and audio had been deleted.
		packed := prepared != "" && fileExists(store.MediaDBPath(prepared))
		if !packed && !e.noPackableMedia() {
			return rep, fmt.Errorf(
				"pack media for %q first - its images and audio are still only in the original files", rep.Name)
		}
		if prepared == "" {
			return rep, fmt.Errorf("%q is not prepared, so deleting its files would delete the dictionary", rep.Name)
		}
	}

	// Closed synchronously, not on the usual grace timer: the files are about
	// to be unlinked, Windows refuses to delete an open one, and a reader that
	// gets an error is a better outcome than a half-deleted folder. Requests
	// already in flight fail; the next one reopens, or finds it gone.
	e.closeNow()

	if dropPrepared && prepared != "" {
		n, err := store.RemovePrepared(prepared)
		if err != nil {
			return rep, err
		}
		rep.Folder, rep.Freed = prepared, rep.Freed+n
		logx.V("removed prepared dictionary %s (%d MB)", prepared, n>>20)
	}
	if dropSource {
		var failed []string
		for _, p := range sources {
			n := store.TreeSize(p)
			if err := os.RemoveAll(p); err != nil {
				failed = append(failed, fmt.Sprintf("%s (%v)", filepath.Base(p), err))
				continue
			}
			rep.Sources = append(rep.Sources, p)
			rep.Freed += n
		}
		if len(failed) > 0 {
			// Reported, not fatal: whatever was deleted stays deleted, and the
			// user needs to know which files are still there.
			rep.Note = "could not delete " + strings.Join(failed, ", ")
		}
		logx.V("removed %d original file(s) of %s", len(rep.Sources), rep.Name)
	}

	if err := r.Rescan(); err != nil {
		logx.V("rescan after removing %s: %v", rep.Name, err)
	}
	rep.Gone = !r.has(id)
	if !rep.Gone && dropPrepared && !dropSource {
		rep.Note = "the original files are still in a scanned folder, so this dictionary will be indexed again the next time it is searched"
	}
	return rep, nil
}

// has reports whether an id survived the last rescan.
func (r *Registry) has(id string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.byID[id]
	return ok
}

// closeNow drops this entry's open backend immediately. evict() defers the
// close so in-flight readers finish; removal cannot, because the file is about
// to disappear underneath them either way.
func (e *entry) closeNow() {
	e.dMu.Lock()
	d := e.d
	e.d, e.err = nil, nil
	e.weight.Store(0)
	e.dMu.Unlock()
	if d != nil {
		_ = d.Close()
	}
}

// handleRemoveLibrary deletes one dictionary. DELETE, because it is one, and
// because a destructive action must not be reachable by following a link or by
// a page that only knows how to GET.
//
//	DELETE /api/library?dict=<id>[&prepared=0|1][&source=0|1]
//
// Both default to 1: "remove this dictionary". See Remove for why that is the
// default rather than the cautious-looking prepared-only.
func (s *Server) handleRemoveLibrary(w http.ResponseWriter, r *http.Request) {
	if !removalOffered(r) {
		httpErr(w, 403, "this machine has a file manager: remove the folder there instead (%s)",
			revealLabel())
		return
	}
	q := r.URL.Query()
	id := strings.TrimSpace(q.Get("dict"))
	if id == "" {
		httpErr(w, 400, "missing dict parameter")
		return
	}
	prepared := q.Get("prepared") != "0"
	source := q.Get("source") != "0"
	if source && !prepared && !s.reg.UseCached() {
		// Its folder would survive but nothing would list it: the library is
		// only enrolled when the user opted in (D19).
		httpErr(w, 409, "turn on prepared dictionaries first, or this one would vanish from the list with its files")
		return
	}
	rep, err := s.reg.Remove(id, prepared, source)
	if err != nil {
		httpErr(w, 400, "%v", err)
		return
	}
	writeJSON(w, rep)
}

// removalOffered is the exact complement of the Reveal control: the app offers
// to delete a dictionary precisely when it cannot hand the user to a file
// manager that would. On a desktop at the keyboard that is never - D7 stands
// untouched - and on Android, where no file manager may open the app's own
// external files dir, it is always.
func removalOffered(r *http.Request) bool {
	return !(isLoopback(r) && revealPossible())
}
