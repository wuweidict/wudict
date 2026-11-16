// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/wuweidict/wudict/internal/dict"
	"github.com/wuweidict/wudict/internal/facet"
	"github.com/wuweidict/wudict/internal/search"
	"github.com/wuweidict/wudict/internal/store"
)

// dictRowsVersion is part of every row key and list tag: bump it when a
// change to dictInfoFor alters what a row says for the same files, so a
// client holding the previous derivation refetches it even from a build whose
// executable stat happens to match.
const dictRowsVersion = "1"

// rowCache keeps each dictionary's /api/dicts row under the key of what it was
// derived from (rowKey). A row costs a SQLite meta read, a header probe or a
// full open (server.go baseDictInfo); its key costs a handful of stat calls. So
// a page load re-derives only the rows whose files changed, and a list nothing
// changed in is answered with a 304 and no row at all (handleDicts).
//
// Not cached: rows that carry an error (an open may succeed on the next try)
// and the fields that live in memory rather than on disk - builtin, job,
// mediaEmpty - which dictInfoFor sets fresh on every request.
type rowCache struct {
	mu   sync.Mutex
	rows map[string]cachedRow // by entry id
}

type cachedRow struct {
	key  string
	info dictInfo
}

func (c *rowCache) get(id, key string) (dictInfo, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, ok := c.rows[id]
	if !ok || r.key != key {
		return dictInfo{}, false
	}
	return r.info, true
}

func (c *rowCache) put(id, key string, info dictInfo) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.rows == nil {
		c.rows = map[string]cachedRow{}
	}
	c.rows[id] = cachedRow{key: key, info: info}
}

// each calls fn on every cached row.
func (c *rowCache) each(fn func(dictInfo)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, r := range c.rows {
		fn(r.info)
	}
}

// keep drops every row whose id is not in ids: a dictionary the registry no
// longer lists has nothing to be cached for.
func (c *rowCache) keep(ids map[string]bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for id := range c.rows {
		if !ids[id] {
			delete(c.rows, id)
		}
	}
}

// flush forgets every row. A rescan is the user asking for everything to be
// looked at again, so it also re-derives what the stat keys say is unchanged.
func (c *rowCache) flush() {
	c.mu.Lock()
	c.rows = nil
	c.mu.Unlock()
}

var (
	buildOnce sync.Once
	buildSig  string
)

// buildID identifies the executable: its size and mtime, so a rebuild with
// an unchanged version string ("dev") still invalidates every key.
func buildID() string {
	buildOnce.Do(func() {
		if exe, err := os.Executable(); err == nil {
			buildSig = sourceSig(exe)
		}
	})
	return buildSig
}

// rowsGlobal is the part of every row key that is not one dictionary's: what
// langFacts reads besides the row itself (the folders and groups.ini), which
// build derives it, and the reader versions "outdated" is judged against
// (store.Outdated; registered at init, so fixed per build outside tests).
// st is groups.ini as this request reads it.
func (s *Server) rowsGlobal(st fileState[*facet.Rules]) string {
	sum := sha256.Sum256([]byte(st.text))
	return strings.Join([]string{dictRowsVersion, s.Version, buildID(), dict.ReaderVersionsSig(),
		strings.Join(s.reg.Dirs(), "\x00"), fmt.Sprint(st.exists), hex.EncodeToString(sum[:8])}, "|")
}

// rowKey names everything on disk a row of e is derived from, by stat alone:
// the source, its folder (a companion .mdd, .files.zip or abbreviation file
// appearing or vanishing changes the folder's mtime), the abbreviation file's
// content, the library folder LookupDir resolves and the text.db and media.db
// in it. The prepared databases run with journal_mode=OFF, so every write
// lands in the .db file itself and moves its size or mtime.
//
// e.gen covers what the stats cannot see: changes the app makes itself
// (entry.reconcile, D157) and a rescan dropping a backend the row was derived
// from (revalidate).
func rowKey(e *entry, global string) string {
	var b strings.Builder
	sig := func(p string) { b.WriteString("|" + p + "=" + sourceSig(p)) }
	fmt.Fprintf(&b, "%s|%d", global, e.gen.Load())
	sig(e.Path)
	sig(filepath.Dir(e.Path))
	if p, ok := dict.AbbrevCompanion(e.Path); ok {
		sig(p)
	}
	textDB := e.Path
	if !store.IsTextDB(e.Path) {
		textDB = ""
		if dir, ok := store.LookupDir(e.Path); ok {
			sig(dir)
			textDB = store.TextDBPath(dir)
		}
	}
	if textDB != "" {
		sig(textDB)
		if m := store.MediaSibling(textDB); m != "" {
			sig(m)
		}
		if ft := store.FullTextSibling(textDB); ft != "" {
			sig(ft)
		}
	}
	return b.String()
}

// rowKeys keys every entry, as wide as the row fan-out itself
// (search.Workers): all of it precedes `begin`, because the 304 must be
// decided before any byte of the body, and on a slow filesystem (Android
// FUSE) a thousand stat calls in a row would hold the first frame back.
func rowKeys(entries []*entry, global string) []string {
	keys := make([]string, len(entries))
	sem := make(chan struct{}, search.Workers())
	var wg sync.WaitGroup
	for i, e := range entries {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, e *entry) {
			defer wg.Done()
			defer func() { <-sem }()
			keys[i] = rowKey(e, global)
		}(i, e)
	}
	wg.Wait()
	return keys
}

// rowState is the in-memory part of a row, applied after the cache: it
// changes without any file changing, so it is never part of a cached row.
func (s *Server) rowState(e *entry, info *dictInfo) {
	info.Builtin = e.builtin
	if js, ok := s.jobs.status(ingestKey(e.ID)); ok && js.Running {
		info.Job = &jobProgress{Done: js.Done, Total: js.Total}
	}
	if e.noPackableMedia() {
		info.HasMedia = false // a prior pack found nothing - stop offering it
	}
}

// listTag is the validator of one /api/dicts answer: the registry's order of
// ids and paths, each row's key and in-memory state, and what `begin` says.
// Equal tags mean an equal stream up to row order, which a client never
// relies on (rows arrive in completion order).
func (s *Server) listTag(entries []*entry, keys []string, problems int) string {
	h := sha256.New()
	fmt.Fprintf(h, "%d\n", problems)
	for i, e := range entries {
		// the same in-memory state rowState applies; the rows read it again
		// later, and a job ending in between only costs the next load a 200
		job := ""
		if js, ok := s.jobs.status(ingestKey(e.ID)); ok && js.Running {
			job = fmt.Sprintf("%d/%d", js.Done, js.Total)
		}
		fmt.Fprintf(h, "%s\x00%s\x00%v\x00%s\x00%v\x00%s\n", e.ID, e.Path, e.builtin, job, e.noPackableMedia(), keys[i])
	}
	return `"` + hex.EncodeToString(h.Sum(nil)[:12]) + `"`
}

// matchesTag reports whether an If-None-Match header names tag. The header is
// a list, may be `*`, and is compared weakly for a GET, so `W/"…"` matches.
func matchesTag(header, tag string) bool {
	for _, t := range strings.Split(header, ",") {
		t = strings.TrimPrefix(strings.TrimSpace(t), "W/")
		if t == "*" || t == tag {
			return true
		}
	}
	return false
}
