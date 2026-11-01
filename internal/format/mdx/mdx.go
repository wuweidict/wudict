// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Package mdx is the direct backend for Octopus MDict dictionaries: one
// .mdx plus its companion .mdd resource files (NAME.mdd, NAME.1.mdd, …).
//
// It adapts the vendored go-mdict block parser (internal/gomdict) to the
// dict.Dictionary interface. Compared to mdict-go-web's reader it indexes
// headwords in maps at open (O(1) exact/folded lookup instead of full
// scans) and streams .mdd resources directly instead of extracting them
// to an asset directory.
package mdx

import (
	"fmt"
	"html"
	"io"
	"mime"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"

	"github.com/wuweidict/wudict/internal/dict"
	"github.com/wuweidict/wudict/internal/fsx"
	gomdict "github.com/wuweidict/wudict/internal/gomdict"
	"github.com/wuweidict/wudict/internal/logx"
	"github.com/wuweidict/wudict/internal/resource"
)

func init() {
	dict.RegisterFormat(".mdx", func(path string) (dict.Dictionary, error) { return Open(path) })
	// Openable by name, never discovered: see dict.RegisterInspectable and MDD.
	dict.RegisterInspectable(".mdd", func(path string) (dict.Dictionary, error) { return OpenMDD(path) })
	dict.RegisterProber(".mdx", probe)
}

// probe reads name/description/entry-count from the MDX header only (no
// BuildIndex, no fold-maps) for the cheap dictionary-list path. It shares
// dictName with Open so both report the same display name.
func probe(filename string) (dict.Meta, error) {
	md, err := gomdict.New(filename)
	if err != nil {
		return dict.Meta{}, err
	}
	return dict.Meta{
		Name:        dictName(md, filename),
		Format:      "mdx",
		Path:        filename,
		Description: dict.DisplayText(strings.TrimSpace(md.Description())),
		EntryCount:  int(md.EntryCount()),
	}, nil
}

const linkPrefix = "@@@LINK="

// reMarkupBlock and reTag strip markup from a record so what a reader would
// actually SEE can be examined: the paired blocks first, because their content
// is code rather than text and would otherwise survive tag removal.
var (
	// Spelled out per tag rather than captured and back-referenced: RE2 has
	// no backreferences.
	reMarkupBlock = regexp.MustCompile(`(?is)<script\b[^>]*>.*?</script[^>]*>|<style\b[^>]*>.*?</style[^>]*>`)
	reTag         = regexp.MustCompile(`(?s)<[^>]*>`)
)

// visibleText is what is left of a record fragment once markup, NULs and the
// byte-order mark are gone. Empty means the fragment is a wrapper and nothing
// else.
func visibleText(s string) string {
	s = reMarkupBlock.ReplaceAllString(s, "")
	s = reTag.ReplaceAllString(s, "")
	return strings.TrimSpace(strings.Trim(s, "\x00\ufeff"))
}

// linkTarget reports whether a record is an MDict redirect and, if so, the
// headword it points at (empty when the pointer itself is broken - a redirect
// to nowhere, which is dropped rather than shown).
//
// The marker is documented as the WHOLE record - "@@@LINK=headword" - and most
// makers write exactly that. Some dictionaries wrap it
// in the same HTML shell it gives every article:
//
//	<head><link rel="stylesheet" type="text/css" href="OED.css"/></head>
//	<div class="entry">@@@LINK=pandore</div entry>
//
// Testing only the first bytes leaves that record looking like an article, so
// the redirect reached the screen as the literal text "@@@LINK=pandore" under
// the headword the user searched for. The shell is therefore discounted: a
// record is a redirect when the marker is the only VISIBLE text in it, however
// much markup surrounds it. An article that merely mentions the marker among
// its prose keeps its body, because text survives the strip on either side.
func linkTarget(body string) (string, bool) {
	i := strings.Index(body, linkPrefix)
	if i < 0 || visibleText(body[:i]) != "" {
		return "", false
	}
	rest := body[i+len(linkPrefix):]
	end := strings.IndexAny(rest, "<\r\n\x00")
	if end < 0 {
		end = len(rest)
	}
	if visibleText(rest[end:]) != "" {
		return "", false
	}
	// Unescaped because the target is a headword to look up, not markup to
	// render: a repack that writes &amp; in one would otherwise point nowhere.
	return strings.TrimSpace(html.UnescapeString(rest[:end])), true
}

type mddFile struct {
	path    string
	md      *gomdict.Mdict
	entries []*gomdict.MDictKeywordEntry
}

type resHit struct {
	md    *gomdict.Mdict
	entry *gomdict.MDictKeywordEntry
}

// Dict is one opened MDX dictionary. Safe for concurrent readers after Open.
type Dict struct {
	meta      dict.Meta
	mdx       *gomdict.Mdict
	entries   []*gomdict.MDictKeywordEntry // headwords decoded to UTF-8, dictionary order
	exactOnce sync.Once                    // exactIdx built on first exact lookup only
	exactIdx  map[string][]int             // headword -> entry indexes
	foldOnce  sync.Once                    // foldIdx built on first accent-folded lookup only
	foldIdx   map[string][]int             // folded headword -> entry indexes
	enc       int
	plain     bool              // records are plain text (Format="Text")
	compact   bool              // StyleSheet tokens are substituted (Compact="Yes")
	styles    map[int][2]string // the StyleSheet: token -> begin, end
	mddOnce   sync.Once         // mdds is built on first use (companions)
	mdds      []mddFile

	resOnce sync.Once
	resMap  map[string]resHit

	loose looseNames
}

// looseNames caches the names in the .mdx's folder, so the loose-first
// resource lookup (Resource) costs a map probe instead of a failed open for
// the packed resources that are nearly every request: a failed open is ~1 µs
// on a local disk and far more on Android's shared storage, paid per image,
// per sound and per packed item. The listing is re-validated at most every
// looseTTL against the folder's modification time, so an override dropped in
// beside a dictionary being read takes effect within that.
type looseNames struct {
	mu    sync.Mutex
	at    time.Time       // when the listing was last validated
	mtime time.Time       // the folder's modification time at that listing
	names map[string]bool // looseKey of every entry; nil when unlistable
}

const looseTTL = 2 * time.Second

// mayBeLoose reports whether the first path component of rel names an entry
// in dir, compared the way a case- and normalisation-insensitive filesystem
// compares, so that a false "no" never hides a file the open would find.
func (l *looseNames) mayBeLoose(dir, rel string) bool {
	first, _, _ := strings.Cut(rel, "/")
	l.mu.Lock()
	defer l.mu.Unlock()
	if now := time.Now(); l.at.IsZero() || now.Sub(l.at) >= looseTTL {
		fi, err := os.Stat(dir)
		switch {
		case err != nil:
			l.names = nil
		case l.at.IsZero() || !fi.ModTime().Equal(l.mtime):
			l.names, l.mtime = listLoose(dir), fi.ModTime()
		}
		l.at = now
	}
	if l.names == nil {
		return true // no listing: let the open decide
	}
	return l.names[looseKey(first)]
}

func listLoose(dir string) map[string]bool {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	m := make(map[string]bool, len(ents))
	for _, e := range ents {
		m[looseKey(e.Name())] = true
	}
	return m
}

func looseKey(name string) string { return strings.ToLower(norm.NFC.String(name)) }

// Open opens an .mdx file. Its companion .mdd files are opened and indexed
// on the first resource request (companions): the ingest scan reads articles
// only, and an .mdd's key index is the bulk of an open's time and memory.
func Open(filename string) (*Dict, error) {
	md, entries, err := openIndexed(filename)
	if err != nil {
		return nil, err
	}
	// Headwords arrive decoded to UTF-8 and trimmed (gomdict). No headword
	// index is built here - both the raw (ensureExact) and accent-folded
	// (ensureFold) indexes are lazy, so an open that only serves resources or
	// feeds the ingest scan builds nothing. Runtime readers keep no in-memory
	// headword index; our ingested SQLite path is that index.
	d := &Dict{
		mdx:     md,
		entries: entries,
		enc:     md.Encoding(),
		plain:   md.IsPlainText(),
		compact: md.IsCompact(),
		styles:  parseStylesheet(md.StyleSheet()),
	}
	d.meta = dict.Meta{
		Name:        dictName(md, filename),
		Format:      "mdx",
		Path:        filename,
		Description: dict.DisplayText(strings.TrimSpace(md.Description())),
		EntryCount:  len(entries),
		Header:      headerFields(md),
	}

	return d, nil
}

// companions opens and indexes the companion .mdd files, once.
func (d *Dict) companions() []mddFile {
	d.mddOnce.Do(func() {
		for _, f := range companionMdds(d.meta.Path) {
			rmd, rents, err := openIndexed(f)
			if err != nil {
				logx.Warn("%smedia file %s cannot be read: %v - its audio and images will be missing",
					logx.Dict(d.meta.Name), filepath.Base(f), err)
				continue
			}
			d.mdds = append(d.mdds, mddFile{path: f, md: rmd, entries: rents})
		}
	})
	return d.mdds
}

func openIndexed(filename string) (*gomdict.Mdict, []*gomdict.MDictKeywordEntry, error) {
	md, err := gomdict.New(filename)
	if err != nil {
		return nil, nil, err
	}
	if err := md.BuildIndex(); err != nil {
		return nil, nil, err
	}
	entries, err := md.GetKeyWordEntries()
	if err != nil {
		return nil, nil, err
	}
	return md, entries, nil
}

// companionMdds lists the .mdd resource containers belonging to one .mdx.
//
// The directory is READ, not probed. Probing NAME.mdd, NAME.1.mdd, NAME.2.mdd …
// and stopping at the first gap loses every later file of a set numbered with a
// hole in it, and loses it silently - a missing companion is indistinguishable
// from a dictionary that never had one, so the only symptom is images and audio
// that 404. Reading the folder also settles the extension's case, which matters
// on Linux and Android where NAME.MDD is a different name from NAME.mdd.
//
// Three spellings count as companions, the set GoldenDict and unidict accept:
//
//	NAME.mdd        the primary container
//	NAME.<n>.mdd    numbered, any n, gaps allowed
//	NAME.<a><n>.mdd a letter then digits, as some repacks number their parts
//
// Order is the load order, and load order decides which file wins a duplicate
// resource name (resourceIndex keeps the first): NAME.mdd, then the numbered
// ones by VALUE - .2 before .10, which a string sort gets backwards - then the
// rest by name.
func companionMdds(mdxPath string) []string {
	dir := filepath.Dir(mdxPath)
	base := filepath.Base(mdxPath)
	stem := strings.TrimSuffix(base, filepath.Ext(base))

	ents, err := os.ReadDir(dir)
	if err != nil {
		// An unreadable folder still has one name worth trying: the .mdx opened,
		// so its own directory entry exists even if the listing was refused.
		if f := strings.TrimSuffix(mdxPath, filepath.Ext(mdxPath)) + ".mdd"; fsx.FileExists(f) {
			return []string{f}
		}
		return nil
	}

	type cand struct {
		name string
		rank int // 0 primary, 1 numbered, 2 lettered
		num  int
	}
	var found []cand
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		n := e.Name()
		ext := filepath.Ext(n)
		if !strings.EqualFold(ext, ".mdd") {
			continue
		}
		s := strings.TrimSuffix(n, ext)
		// Case-insensitive on the stem too: a folder holding both NAME.mdx and
		// name.mdd is one dictionary spelled twice, not two.
		if strings.EqualFold(s, stem) {
			found = append(found, cand{n, 0, 0})
			continue
		}
		if len(s) <= len(stem)+1 || !strings.EqualFold(s[:len(stem)], stem) || s[len(stem)] != '.' {
			continue
		}
		suf := s[len(stem)+1:]
		if num, ok := allDigits(suf); ok {
			found = append(found, cand{n, 1, num})
			continue
		}
		if isAlpha(suf[0]) {
			if num, ok := allDigits(suf[1:]); ok {
				found = append(found, cand{n, 2, num})
			}
		}
	}
	sort.Slice(found, func(i, j int) bool {
		a, b := found[i], found[j]
		if a.rank != b.rank {
			return a.rank < b.rank
		}
		if a.num != b.num {
			return a.num < b.num
		}
		return a.name < b.name
	})
	out := make([]string, 0, len(found))
	for _, c := range found {
		out = append(out, filepath.Join(dir, c.name))
	}
	return out
}

// allDigits parses a non-empty run of ASCII digits. It is deliberately not
// strconv.Atoi: an empty string, a sign, or leading whitespace must all fail,
// and a suffix too long to fit an int is not a part number.
func allDigits(s string) (int, bool) {
	if s == "" || len(s) > 9 {
		return 0, false
	}
	n := 0
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
		n = n*10 + int(s[i]-'0')
	}
	return n, true
}

func isAlpha(c byte) bool { return c|0x20 >= 'a' && c|0x20 <= 'z' }

func (d *Dict) Meta() dict.Meta { return d.meta }

func (d *Dict) Caps() dict.Caps { return dict.Caps{Exact: true, Prefix: true} }

func (d *Dict) Close() error { return nil } // gomdict opens files per read

// Exact returns all entries whose headword equals word; if none, all
// case/accent-folded matches.
func (d *Dict) Exact(word string, limit int) ([]dict.Result, error) {
	word = strings.TrimSpace(word)
	d.ensureExact()
	idxs := d.exactIdx[word]
	if len(idxs) == 0 {
		d.ensureFold()
		idxs = d.foldIdx[fold(word)]
	}
	return d.results(idxs, word, limit), nil
}

// ensureExact builds the raw headword index on first lookup, so opens that only
// serve resources or feed the ingest scan never build it.
func (d *Dict) ensureExact() {
	d.exactOnce.Do(func() {
		idx := make(map[string][]int, len(d.entries))
		for i, e := range d.entries {
			idx[e.KeyWord] = append(idx[e.KeyWord], i)
		}
		d.exactIdx = idx
	})
}

// ensureFold builds the accent/case-folded headword index on first use,
// deferring the per-headword NFD fold that dominates open time and RAM.
func (d *Dict) ensureFold() {
	d.foldOnce.Do(func() {
		idx := make(map[string][]int, len(d.entries))
		for i, e := range d.entries {
			f := fold(e.KeyWord)
			idx[f] = append(idx[f], i)
		}
		d.foldIdx = idx
	})
}

// Prefix returns up to limit headwords starting with word, the exact matches
// first (raw pass first, folded passes only when the raw pass is empty).
//
// An exact hit does not end the search: "starts with" must not answer a
// complete headword with that one article and drop every longer key under it,
// so the exact hit takes precedence, not the whole answer. It is seeded from the
// headword index rather than left to the scan because the scan walks the file
// in storage order and stops at limit: under a prefix with more than `limit`
// entries behind it, the word actually typed could be the one cut.
func (d *Dict) Prefix(word string, limit int) ([]dict.Result, error) {
	word = strings.TrimSpace(word)
	d.ensureExact()
	idxs := append([]int(nil), d.exactIdx[word]...)
	seen := make(map[int]bool, len(idxs))
	for _, i := range idxs {
		seen[i] = true
	}
	scan := func(useFold bool) {
		key := word
		if useFold {
			key = fold(word)
		}
		for i, e := range d.entries {
			if seen[i] {
				continue
			}
			hw := e.KeyWord
			if useFold {
				hw = fold(hw)
			}
			if strings.HasPrefix(hw, key) {
				idxs = append(idxs, i)
				if limit > 0 && len(idxs) >= limit {
					return
				}
			}
		}
	}
	scan(false)
	if len(idxs) == 0 {
		// The folded EXACT index first, and the folded scan only behind it:
		// the index is built once and answers every later keystroke, while the
		// scan re-folds every headword on each call.
		d.ensureFold()
		idxs = append(idxs, d.foldIdx[fold(word)]...)
		for _, i := range idxs {
			seen[i] = true
		}
		scan(true)
	}
	return d.results(idxs, word, limit), nil
}

func (d *Dict) results(idxs []int, word string, limit int) []dict.Result {
	var out []dict.Result
	for _, i := range idxs {
		e := d.entries[i]
		seen := map[string]bool{e.KeyWord: true, word: true}
		for _, body := range d.render(e, seen) {
			out = append(out, dict.Result{Headword: e.KeyWord, Body: body})
		}
		if limit > 0 && len(out) >= limit {
			out = out[:limit]
			break
		}
	}
	return out
}

// render locates one record and converts it to HTML, following @@@LINK
// redirects (cycle-guarded). One entry can yield several bodies when a
// link target has homographs.
func (d *Dict) render(e *gomdict.MDictKeywordEntry, seen map[string]bool) []string {
	raw, err := d.mdx.LocateByKeywordEntry(e)
	if err != nil {
		logx.V("%sentry %q could not be read: %v", logx.Dict(d.meta.Name), e.KeyWord, err)
		return nil
	}
	body, target, ok := d.article(raw)
	if ok {
		if target == "" || seen[target] {
			return nil
		}
		seen[target] = true
		var out []string
		d.ensureExact()
		idxs := d.exactIdx[target]
		if len(idxs) == 0 {
			d.ensureFold()
			idxs = d.foldIdx[fold(target)]
		}
		for _, i := range idxs {
			out = append(out, d.render(d.entries[i], seen)...)
		}
		return out
	}
	return []string{body}
}

// article turns one record into what the dictionary shows, in MDict's order
// (docs/mdict/SEMANTICS.md §1, §3): a redirect is recognised first and
// reported as its target; otherwise a plain-text dictionary is HTML-escaped,
// and a compact one has its StyleSheet tokens substituted. Both the runtime
// and the ingest scan call it, so a dictionary reads the same either way.
func (d *Dict) article(raw []byte) (body, target string, isLink bool) {
	body = strings.TrimSpace(strings.Trim(decodeEnc(raw, d.enc), "\x00"))
	if target, ok := linkTarget(body); ok {
		return "", target, true
	}
	switch {
	case d.plain:
		body = textToHTML(body)
	case d.compact:
		body = substituteStylesheet(body, d.styles)
	}
	return body, "", false
}

func (d *Dict) Keywords(offset, n int) []string {
	lo, hi, ok := dict.KeywordRange(len(d.entries), offset, n)
	if !ok {
		return nil
	}
	out := make([]string, 0, hi-lo)
	for _, e := range d.entries[lo:hi] {
		out = append(out, e.KeyWord)
	}
	return out
}

// Resource streams one resource. name uses forward slashes without a leading
// slash (e.g. "audio/word.mp3").
//
// A file beside the .mdx comes first and the .mdd files second, which is
// MDict's order (mdx::MdxDict::getDictData): it is how a user overrides a
// packed stylesheet - drop a corrected NAME.css next to the .mdx - and how
// repacks ship their stylesheet and scripts with no .mdd at all (LDOCE6 keeps
// LDOCE6.css and entry.js loose). The check is one stat, ~1 µs against a
// ~100 µs HTTP round trip. Packed lookup is case-insensitive, as MDD keys are
// case-preserving but referenced loosely.
func (d *Dict) Resource(name string) (io.ReadCloser, string, error) {
	norm := strings.ToLower(strings.TrimLeft(path.Clean(name), "/"))
	if norm == "" || norm == "." || strings.HasPrefix(norm, "..") {
		return nil, "", dict.ErrNotFound
	}
	if rc, mt, err := d.looseFile(name); err == nil {
		return rc, mt, nil
	}
	hit, ok := d.resourceIndex()[norm]
	if !ok {
		return nil, "", dict.ErrNotFound
	}
	data, err := hit.md.LocateByKeywordEntry(hit.entry)
	if err != nil {
		return nil, "", fmt.Errorf("mdx: locate resource %q: %w", name, err)
	}
	return resource.Bytes(data), mime.TypeByExtension(path.Ext(norm)), nil
}

// The allowlist of what may be served from beside the .mdx lives in
// dict.IsAssetName: the article rewriter needs the same answer to tell a
// pronunciation link from a cross-reference, and two copies of a security
// allowlist is one copy too many.

// looseFile serves a resource from the .mdx's own directory. The ORIGINAL name
// is used, not the lower-cased index key: on a case-sensitive filesystem
// "LDOCE6.css" is not "ldoce6.css".
func (d *Dict) looseFile(name string) (io.ReadCloser, string, error) {
	rel := path.Clean("/" + strings.ReplaceAll(name, "\\", "/"))[1:]
	if rel == "" || rel == "." {
		return nil, "", dict.ErrNotFound
	}
	if !dict.IsAssetName(rel) {
		return nil, "", dict.ErrNotFound
	}
	dir := filepath.Dir(d.meta.Path)
	if !d.loose.mayBeLoose(dir, rel) {
		return nil, "", dict.ErrNotFound
	}
	p := filepath.Join(dir, filepath.FromSlash(rel))
	// belt and braces: Clean above removes "..", this proves the result stayed
	// inside the dictionary's folder even through symlinked spellings
	if !strings.HasPrefix(p, dir+string(filepath.Separator)) {
		return nil, "", dict.ErrNotFound
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, "", dict.ErrNotFound
	}
	if fi, err := f.Stat(); err != nil || fi.IsDir() {
		f.Close()
		return nil, "", dict.ErrNotFound
	}
	return f, mime.TypeByExtension(path.Ext(rel)), nil
}

// Resources lists all .mdd resource names (lowercased, forward-slash).
func (d *Dict) Resources() []string {
	idx := d.resourceIndex()
	out := make([]string, 0, len(idx))
	for name := range idx {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// resourceIndex maps lowercased forward-slash resource paths to their mdd
// entry. MDD keys look like `\path\name`; first mdd file wins on duplicates.
func (d *Dict) resourceIndex() map[string]resHit {
	d.resOnce.Do(func() {
		m := make(map[string]resHit)
		mdds := d.companions()
		for i := range mdds {
			md := &mdds[i]
			for _, e := range md.entries {
				key := strings.ReplaceAll(strings.TrimLeft(e.KeyWord, "\\/"), "\\", "/")
				k := strings.ToLower(key)
				if _, ok := m[k]; !ok {
					m[k] = resHit{md: md.md, entry: e}
				}
			}
		}
		d.resMap = m
	})
	return d.resMap
}

// decodeEnc converts record bytes in the dictionary's encoding to UTF-8.
// UTF-16 records arrive already decoded (gomdict).
func decodeEnc(raw []byte, enc int) string {
	if enc == gomdict.EncodingUtf16 {
		return string(raw)
	}
	return gomdict.DecodeText(raw, enc)
}

// fold delegates to the shared accent/case folding.
func fold(s string) string { return dict.Fold(s) }

// textToHTML renders a plain-text record (Format="Text") as MDict does
// (mdx::HtmlEscapeMDXText): & < > " escaped, a line feed and the two
// characters \n become <br>, and a backslash before any other character is
// dropped together with that character - the escape convention of the
// SugarDict and KDict sources MdxBuilder builds such dictionaries from.
func textToHTML(s string) string {
	var b strings.Builder
	b.Grow(len(s) + len(s)/8)
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '"':
			b.WriteString("&quot;")
		case '\n':
			b.WriteString("<br>")
		case '\\':
			if i+1 < len(s) && s[i+1] == 'n' {
				b.WriteString("<br>")
			}
			if i+1 < len(s) {
				_, size := utf8.DecodeRuneInString(s[i+1:])
				i += size
			}
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// parseStylesheet parses the StyleSheet header attribute: lines in triplets
// of token (a number, 0-255), begin string, end string (docs/mdict/
// SEMANTICS.md §3.1). A token MDict would reject costs only its own triplet.
func parseStylesheet(s string) map[int][2]string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	lines := strings.Split(strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n"), "\n")
	m := make(map[int][2]string)
	for i := 0; i+2 < len(lines); i += 3 {
		if n, ok := styleToken(strings.TrimSpace(lines[i])); ok {
			m[n] = [2]string{lines[i+1], lines[i+2]}
		}
	}
	return m
}

// styleToken reads a style number: digits, at most 255.
func styleToken(s string) (int, bool) {
	if s == "" || len(s) > 3 {
		return 0, false
	}
	n := 0
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
		n = n*10 + int(s[i]-'0')
	}
	return n, n <= 255
}

// substituteStylesheet applies StyleSheet tokens as MDict does
// (mdx::MdxDict::reFormat): `N` switches the style, and the text up to the
// next token, or to the end, is emitted between style N's begin and end
// strings. Text before the first token takes style 0. A token with no
// definition has empty strings, so its text is kept and only the marker goes.
// Whitespace is left as it is. An article without a backtick - every article
// of nearly every compact dictionary - is returned as it is, unallocated.
func substituteStylesheet(txt string, styles map[int][2]string) string {
	start, end, n, ok := nextStyleToken(txt, 0)
	if !ok {
		if s, ok := styles[0]; ok {
			return s[0] + txt + s[1]
		}
		return txt
	}
	var b strings.Builder
	b.Grow(len(txt) + len(txt)/4)
	style, prev := 0, 0
	emit := func(seg string) {
		s := styles[style]
		b.WriteString(s[0])
		b.WriteString(seg)
		b.WriteString(s[1])
	}
	for ok {
		emit(txt[prev:start])
		style, prev = n, end
		start, end, n, ok = nextStyleToken(txt, end)
	}
	if prev < len(txt) {
		emit(txt[prev:])
	}
	return b.String()
}

// nextStyleToken finds the next `N` token (a backtick, ASCII digits, a
// backtick) at or after from: where it starts and ends, and its number, -1
// when it is above 255 and so can have no definition.
func nextStyleToken(s string, from int) (start, end, n int, ok bool) {
	for {
		i := strings.IndexByte(s[from:], '`')
		if i < 0 {
			return 0, 0, 0, false
		}
		i += from
		j := i + 1
		for j < len(s) && s[j] >= '0' && s[j] <= '9' {
			j++
		}
		if j > i+1 && j < len(s) && s[j] == '`' {
			n, valid := styleToken(s[i+1 : j])
			if !valid {
				n = -1
			}
			return i, j + 1, n, true
		}
		from = i + 1
	}
}

// headerFields is the MDX header minus what Meta already carries (Title,
// Description) and minus StyleSheet, which is code the renderer applies - a
// numbered table of HTML fragments, not a statement about the dictionary.
func headerFields(md *gomdict.Mdict) []dict.Field {
	var out []dict.Field
	for _, a := range md.HeaderAttrs() {
		switch strings.ToLower(a[0]) {
		case "title", "description", "stylesheet":
			continue
		}
		if v := strings.TrimSpace(dict.DisplayText(a[1])); v != "" {
			out = append(out, dict.Field{Name: a[0], Value: v})
		}
	}
	return out
}

func dictName(md *gomdict.Mdict, filename string) string {
	// DisplayText, not the raw title: an MDX header is XML, and a builder that
	// HTML-escaped the title before writing it leaves "Learner&#x27;s" behind
	// once the XML layer is undone (dict.DisplayText).
	title := strings.TrimSpace(dict.DisplayText(md.Title()))
	if title == "Title (No HTML code allowed)" { // MdxBuilder placeholder
		title = ""
	}
	if title != "" {
		return title
	}
	base := filepath.Base(filename)
	return strings.TrimSuffix(base, filepath.Ext(base))
}
