// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package store

// The library: prepared dictionaries as self-contained folders.
//
//	<db dir>/
//	  AHD5-2017/            ← folder name mirrors the source FILE name
//	    text.db             ← articles + search indexes
//	    media.db            ← packed images/audio (only after "pack media")
//	    info.txt            ← human-readable receipt; also the fast source index
//	  Oxford Advanced/
//	    text.db
//	    info.txt
//
// One dictionary is one folder, so it can be copied, moved, zipped or handed
// to someone else as a single unit - and dropped into a dictionary folder to
// be used elsewhere (dict.Discover recognizes the bundle by its text.db).
//
// Two registry levels, one source of truth:
//   - level 1 (inventory): the folder names - `ls` is the library listing;
//   - level 2 (authoritative): each text.db's meta table.
//
// info.txt sits between them as a *derived receipt*: regenerated from the
// text.db meta after every ingest (WriteInfo) and never edited by hand. It
// carries exactly one fact of its own - `source`, the path this folder was
// claimed for, written before the ingest starts. That is the ownership record
// (meta's own source_path is only what a format reader chose to report, and
// may be relative, stale or absent); everything else in the receipt is copied
// from the meta. No library-wide manifest exists, and none should: that would
// be a genuine second source of truth.
//
// The folder replaces the old `<slug>-<hash8>.text.db` flat naming. The hash
// used to key the cache to the source content; that job now belongs to the
// meta (source_size/source_mtime/source_sha256_1M via SourceChanged), which
// re-indexes a changed source *in place* instead of piling up stale files.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/wuweidict/wudict/internal/dict"
)

// Names of the files inside a prepared-dictionary folder.
const (
	TextDBName  = "text.db"
	MediaDBName = "media.db"
	InfoName    = "info.txt"
)

// maxCandidates bounds the disambiguation search for a folder name.
const maxCandidates = 12

// TextDBPath and MediaDBPath name the databases inside a library folder.
func TextDBPath(dir string) string  { return filepath.Join(dir, TextDBName) }
func MediaDBPath(dir string) string { return filepath.Join(dir, MediaDBName) }
func InfoPath(dir string) string    { return filepath.Join(dir, InfoName) }

// IsTextDB reports whether path names a wudict text database: the bundle main
// file (`text.db`) or a loose `<name>.text.db` copied out of its folder.
func IsTextDB(path string) bool {
	base := strings.ToLower(filepath.Base(path))
	return base == TextDBName || strings.HasSuffix(base, ".text.db")
}

// MediaSibling returns the media.db that pairs with a text.db path, in either
// the bundle (`<dir>/media.db`) or the loose (`<name>.media.db`) form.
func MediaSibling(textDB string) string {
	if strings.EqualFold(filepath.Base(textDB), TextDBName) {
		return MediaDBPath(filepath.Dir(textDB))
	}
	if base, ok := strings.CutSuffix(textDB, ".text.db"); ok {
		return base + ".media.db"
	}
	return ""
}

// FolderName derives a library folder name from a source file name: the file
// name with its extension dropped (a compression suffix drops the one under
// it too, so "big.dsl.dz" → "big"), sanitized for every platform we build for.
// Spaces and case are preserved - the point is that the folder is recognizably
// the dictionary the user already knows.
func FolderName(srcPath string) string {
	name := filepath.Base(srcPath)
	ext := strings.ToLower(filepath.Ext(name))
	name = strings.TrimSuffix(name, filepath.Ext(name))
	if ext == ".dz" || ext == ".gz" {
		name = strings.TrimSuffix(name, filepath.Ext(name))
	}
	var b strings.Builder
	for _, r := range name {
		switch {
		case r < 0x20, r == 0x7f:
			// control characters: drop
		case strings.ContainsRune(`/\:*?"<>|`, r):
			b.WriteByte('-')
		default:
			b.WriteRune(r)
		}
	}
	// trailing dots and spaces are illegal in Windows directory names
	out := strings.TrimRight(strings.TrimSpace(b.String()), " .")
	if len(out) > 80 {
		out = strings.TrimRight(strings.TrimSpace(out[:80]), " .")
	}
	if out == "" {
		return "dictionary"
	}
	return out
}

// formatTag is the disambiguating suffix for same-named sources in different
// formats: "AHD5-2017.slob" and "AHD5-2017.mdx" become "AHD5-2017" and
// "AHD5-2017 (mdx)" - whichever was prepared second takes the tag.
func formatTag(srcPath string) string {
	ext := strings.ToLower(filepath.Ext(srcPath))
	if ext == ".dz" || ext == ".gz" {
		if inner := strings.ToLower(filepath.Ext(strings.TrimSuffix(srcPath, ext))); inner != "" {
			ext = inner
		}
	}
	tag := strings.TrimPrefix(ext, ".")
	if tag == "" {
		return "alt"
	}
	return tag
}

// candidateDirs lists the folder names a source may occupy, in order.
func candidateDirs(srcPath string) []string {
	root := DefaultDBDir()
	base := FolderName(srcPath)
	tag := formatTag(srcPath)
	out := []string{filepath.Join(root, base)}
	out = append(out, filepath.Join(root, fmt.Sprintf("%s (%s)", base, tag)))
	for i := 2; i < maxCandidates; i++ {
		out = append(out, filepath.Join(root, fmt.Sprintf("%s (%s %d)", base, tag, i)))
	}
	return out
}

// dirOwner reports what a library folder holds: the source path it was
// prepared from (the info.txt claim, falling back to the text.db meta for
// folders written before a claim existed), whether it has a text.db at all,
// and whether it exists.
func dirOwner(dir string) (owner string, hasDB, exists bool) {
	st, err := os.Stat(dir)
	if err != nil || !st.IsDir() {
		return "", false, false
	}
	textDB := TextDBPath(dir)
	if fi, err := os.Stat(textDB); err == nil && !fi.IsDir() {
		hasDB = true
	}
	if info, err := readInfo(InfoPath(dir)); err == nil {
		if s := info["source"]; s != "" {
			return s, hasDB, true
		}
	}
	if hasDB {
		if s, err := ReadMetaValue(textDB, "source_path"); err == nil {
			return s, hasDB, true
		}
	}
	return "", hasDB, true
}

// sameSource compares two source paths as filesystem locations.
func sameSource(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if a == b {
		return true
	}
	ca, err1 := filepath.Abs(a)
	cb, err2 := filepath.Abs(b)
	return err1 == nil && err2 == nil && filepath.Clean(ca) == filepath.Clean(cb)
}

// LookupDir returns the library folder already prepared for a source file.
// It never creates anything: read paths (dictionary list, open, provenance)
// use this, so browsing dictionaries cannot litter the library with empty
// folders. Ownership is verified against the folder's recorded source, so a
// same-named dictionary in another format can never be served from the wrong
// folder.
func LookupDir(srcPath string) (string, bool) {
	for _, cand := range candidateDirs(srcPath) {
		owner, hasDB, exists := dirOwner(cand)
		if !exists {
			continue
		}
		if hasDB && sameSource(owner, srcPath) {
			return cand, true
		}
	}
	return "", false
}

// ClaimDir returns the library folder for a source file, creating and
// claiming it when needed. Ingest calls this; everything else calls LookupDir.
//
// The claim is the os.Mkdir itself - atomic, so two concurrent first-time
// ingests of same-named sources cannot both take the same folder: the loser
// sees IsExist, reads the recorded owner, and moves to the next candidate.
func ClaimDir(srcPath string) (string, error) {
	return claimFrom(candidateDirs(srcPath), srcPath)
}

// claimFrom walks candidate folder names and takes the first free one (or the
// one already owned by claim). claim is the source path recorded in the
// receipt; it may be empty for a database with no known source.
func claimFrom(candidates []string, claim string) (string, error) {
	if err := os.MkdirAll(DefaultDBDir(), 0o755); err != nil {
		return "", err
	}
	for _, cand := range candidates {
		err := os.Mkdir(cand, 0o755)
		switch {
		case err == nil:
			// record ownership immediately, before the (long) ingest, so a
			// concurrent claim sees this folder as taken.
			if werr := writeClaim(cand, claim); werr != nil {
				return "", werr
			}
			return cand, nil
		case os.IsExist(err):
			owner, hasDB, _ := dirOwner(cand)
			if sameSource(owner, claim) {
				return cand, nil
			}
			if owner == "" && !hasDB {
				// an empty leftover from an interrupted claim: adopt it.
				if werr := writeClaim(cand, claim); werr != nil {
					return "", werr
				}
				return cand, nil
			}
		default:
			return "", err
		}
	}
	return "", fmt.Errorf("library: no free folder name for %q", claim)
}

// writeClaim stamps a minimal receipt so the folder's owner is knowable before
// its text.db exists. WriteInfo replaces it with the full receipt after ingest.
func writeClaim(dir, srcPath string) error {
	body := fmt.Sprintf("# wudict - preparing this dictionary…\nsource = %s\nclaimed = %s\n",
		srcPath, time.Now().UTC().Format(time.RFC3339))
	return os.WriteFile(InfoPath(dir), []byte(body), 0o644)
}

// SourceChanged reports whether a source file no longer matches what its
// prepared text.db was built from - the job the old content-hash file name
// used to do. Cheap first (size + mtime), and only when those differ does it
// re-hash the first 1 MiB, so a mere touch or copy does not force a re-index.
// A missing source is NOT "changed": the prepared dictionary stands on its own.
func SourceChanged(textDB, srcPath string) bool {
	st, err := os.Stat(srcPath)
	if err != nil {
		return false
	}
	meta, err := ReadMeta(textDB)
	if err != nil {
		return false // unreadable meta is FindOrphans' business, not ours
	}
	if size := meta["source_size"]; size != "" {
		if n, err := strconv.ParseInt(size, 10, 64); err == nil && n == st.Size() {
			if mt := meta["source_mtime"]; mt != "" {
				if t, err := time.Parse(time.RFC3339, mt); err == nil && t.Equal(st.ModTime().UTC().Truncate(time.Second)) {
					return false
				}
			}
		}
	}
	want := meta["source_sha256_1M"]
	if want == "" {
		return false // nothing recorded to compare against
	}
	return sourceHash(srcPath) != want
}

// AbbrevChanged reports whether the abbreviation glossary a text.db was built
// with differs from the one beside its source now. DSL absorbs that companion
// at ingest and bakes the expansions into the articles, so the answer decides
// whether those articles still tell the truth.
//
// The four cases, and why the "neither" one matters: a companion that is
// present but was never recorded means the data was prepared before this
// existed and has no tooltips; one that was recorded but is gone means the
// baked expansions must come out; a size or mtime that moved means it was
// edited. A dictionary that has no companion and recorded none is FRESH - which
// is what keeps every companion-less dictionary in the library out of the
// upgrade sweep.
func AbbrevChanged(textDB, companionPath string) bool {
	meta, err := ReadMeta(textDB)
	if err != nil {
		return false // unreadable meta is FindOrphans' business, not ours
	}
	recorded := meta["abbrev_size"] != ""
	if companionPath == "" {
		return recorded
	}
	st, err := os.Stat(companionPath)
	if err != nil {
		return recorded
	}
	if !recorded {
		return true
	}
	if n, err := strconv.ParseInt(meta["abbrev_size"], 10, 64); err != nil || n != st.Size() {
		return true
	}
	t, err := time.Parse(time.RFC3339, meta["abbrev_mtime"])
	return err != nil || !t.Equal(st.ModTime().UTC().Truncate(time.Second))
}

// PreparedFor returns the text.db already prepared for a source file, if one
// exists and still matches the source. Purely read-only - the caller decides
// whether to fall back to the direct backend or to (re)build.
func PreparedFor(srcPath string) (string, bool) {
	dir, ok := LookupDir(srcPath)
	if !ok {
		return "", false
	}
	textDB := TextDBPath(dir)
	if SourceChanged(textDB, srcPath) {
		return "", false // source edited/replaced: re-index overwrites in place
	}
	return textDB, true
}

// PrepareTarget claims the library folder for a source file and returns the
// text.db path to ingest into.
func PrepareTarget(srcPath string) (string, error) {
	dir, err := ClaimDir(srcPath)
	if err != nil {
		return "", err
	}
	return TextDBPath(dir), nil
}

// LibEntry is one prepared dictionary folder.
type LibEntry struct {
	Dir          string `json:"dir"`
	TextDB       string `json:"textDB"`
	MediaDB      string `json:"mediaDB,omitempty"`
	Name         string `json:"name"`
	Format       string `json:"format"`
	Source       string `json:"source,omitempty"`
	SourceExists bool   `json:"sourceExists"`
	Entries      int    `json:"entries"`
	FullText     bool   `json:"fullText"`
	Contains     bool   `json:"contains"`
	Media        bool   `json:"media"`
	Size         int64  `json:"size"`
	Created      string `json:"created,omitempty"`
}

// Library lists every prepared dictionary in the db dir, newest name order.
// Folders without a readable text.db are skipped (FindOrphans reports those).
func Library() ([]LibEntry, error) {
	root := DefaultDBDir()
	des, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []LibEntry
	for _, de := range des {
		if !de.IsDir() {
			continue
		}
		dir := filepath.Join(root, de.Name())
		textDB := TextDBPath(dir)
		meta, err := ReadMeta(textDB)
		if err != nil {
			continue
		}
		e := LibEntry{
			Dir:      dir,
			TextDB:   textDB,
			Name:     dict.DisplayText(meta["name"]), // as store.Open: repair an over-escaped title without a re-ingest
			Format:   meta["format"],
			Source:   meta["source_path"],
			FullText: meta["ingest_level"] != string(LevelHeadwords),
			Contains: meta["has_trigram"] == "1",
			Created:  meta["created"],
		}
		if e.Name == "" {
			e.Name = de.Name()
		}
		e.Entries, _ = strconv.Atoi(meta["entry_count"])
		if e.Source != "" {
			if _, err := os.Stat(e.Source); err == nil {
				e.SourceExists = true
			}
		}
		if fi, err := os.Stat(MediaDBPath(dir)); err == nil && !fi.IsDir() {
			e.MediaDB = MediaDBPath(dir)
			e.Media = true
		}
		e.Size = dirSize(dir)
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

func dirSize(dir string) int64 {
	des, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	var n int64
	for _, de := range des {
		if fi, err := de.Info(); err == nil && !fi.IsDir() {
			n += fi.Size()
		}
	}
	return n
}

// WriteInfo regenerates a folder's info.txt from its text.db meta and the
// files actually present. Called after every ingest and media pack, so the
// receipt can never drift from the database it describes.
func WriteInfo(dir string) error {
	textDB := TextDBPath(dir)
	meta, err := ReadMeta(textDB)
	if err != nil {
		return err
	}
	level := "headwords only (exact · prefix · contains)"
	if meta["ingest_level"] != string(LevelHeadwords) {
		level = "full text (exact · prefix · contains · full-text)"
	}
	media := "not packed - resources come from the original files"
	if fi, err := os.Stat(MediaDBPath(dir)); err == nil {
		media = fmt.Sprintf("%s (%s)", MediaDBName, humanSize(fi.Size()))
	}
	// The claim written by ClaimDir is the ownership record and wins here: it
	// is the path this folder was prepared FROM, while meta's source_path is
	// whatever the format reader chose to report - which may be relative,
	// stale, or empty. Overwriting the claim with it would orphan the folder
	// from its own source (LookupDir would never find it again).
	source := ""
	if prior, err := readInfo(InfoPath(dir)); err == nil {
		source = prior["source"]
	}
	if source == "" {
		source = meta["source_path"]
	}
	src := source
	if src == "" {
		src = "(unknown)"
	} else if _, err := os.Stat(src); err != nil {
		src += "  [no longer on disk - this folder is now the only copy]"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# wudict - prepared dictionary\n")
	fmt.Fprintf(&b, "# This folder is one dictionary. Copy, move or zip it as a unit;\n")
	fmt.Fprintf(&b, "# drop it into your dictionary folder to use it on another machine.\n")
	fmt.Fprintf(&b, "# Regenerated automatically - edits are overwritten.\n\n")
	fmt.Fprintf(&b, "name = %s\n", dict.DisplayText(meta["name"])) // the receipt is read by people, so it shows the decoded title
	fmt.Fprintf(&b, "format = %s\n", meta["format"])
	fmt.Fprintf(&b, "entries = %s\n", meta["entry_count"])
	fmt.Fprintf(&b, "index = %s\n", level)
	fmt.Fprintf(&b, "media = %s\n", media)
	if l := meta["index_lang"]; l != "" {
		// Only ever what the source declared, so this line is a fact about the
		// dictionary and not a guess this folder would then keep forever.
		fmt.Fprintf(&b, "language = %s\n", l)
	}
	fmt.Fprintf(&b, "source = %s\n", source)
	fmt.Fprintf(&b, "source_size = %s\n", meta["source_size"])
	fmt.Fprintf(&b, "source_mtime = %s\n", meta["source_mtime"])
	fmt.Fprintf(&b, "imported = %s\n", meta["created"])
	fmt.Fprintf(&b, "uuid = %s\n", meta["dict_uuid"])
	fmt.Fprintf(&b, "\n# origin: %s\n", src)
	fmt.Fprintf(&b, "# files: %s (articles + search index)", TextDBName)
	if _, err := os.Stat(MediaDBPath(dir)); err == nil {
		fmt.Fprintf(&b, ", %s (images/audio)", MediaDBName)
	}
	fmt.Fprintf(&b, ", %s (this receipt)\n", InfoName)
	return os.WriteFile(InfoPath(dir), []byte(b.String()), 0o644)
}

// readInfo parses an info.txt receipt (`key = value`, # comments).
func readInfo(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		out[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	return out, nil
}

func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGT"[exp])
}
