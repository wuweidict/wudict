// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Package lemmas installs the lemma data internal/morph reads (D88).
//
// wudict compiles in English and nothing else (D87), which leaves every other
// language as a file in LEMMA_DIR - correct, and useless to anyone who cannot
// produce one. `make lemma-files` needs a Go toolchain and a populated module
// cache; a user who downloaded a release binary has neither. This package is
// how those files are obtained instead: `wudict lemmas list` and
// `wudict lemmas download`.
//
// # The client knows exactly one URL
//
// LEMMA_URL names a MANIFEST, and the manifest names everything else - the
// languages that exist, their sizes, their hashes, and the file each one lives
// in, relative to the manifest's own folder. Nothing else is discovered, and
// in particular nothing is discovered through a code-hosting API: an
// unauthenticated GitHub API is 60 requests an hour PER IP, so `lemmas list`
// behind a corporate NAT or a mobile carrier would fail with a 403 for reasons
// no user could act on. A static file has no such limit and no such failure.
//
// # sha256 is the whole trust model
//
// Every asset is checked against the hash the manifest declares, so the
// transport does not have to be trusted: a redirect, a mirror, a proxy or a
// plain local folder are all equally acceptable, and LEMMA_URL may be a path
// on disk for an install with no network at all. What the manifest cannot be
// trusted about is itself - it is remote data, so every field in it is checked
// before it is used, and the local file name is CONSTRUCTED here from the
// validated language code rather than taken from the manifest.
package lemmas

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/wuweidict/wudict/internal/lang"
	"github.com/wuweidict/wudict/internal/morph"
)

// Version is the manifest format this build understands. A manifest declaring
// anything else is refused rather than read leniently: the fields are a
// contract about what has been VERIFIED (a hash, a length), and guessing at a
// future one would mean installing bytes on the strength of a field that may
// have changed meaning.
const Version = 1

// maxManifest bounds the manifest body. It is JSON describing a few dozen
// languages; anything larger is a wrong URL, and reading it into memory to
// find that out is the failure mode this prevents.
const maxManifest = 1 << 20

// Entry is one installable language, exactly as the manifest declares it and
// after validation. Code is canonical ISO 639-1 (or 639-3 where no 639-1 code
// exists), as internal/lang returns it.
type Entry struct {
	Code    string `json:"code"`
	Name    string `json:"name"`
	File    string `json:"file"`     // bare file name, relative to the manifest's folder
	Size    int64  `json:"size"`     // bytes on the wire, verified after download
	RawSize int64  `json:"raw_size"` // bytes after gunzip; 0 = not declared
	Lemmas  int64  `json:"lemmas"`   // 0 = not declared
	SHA256  string `json:"sha256"`
	HeapMB  int    `json:"heap_mb"` // measured cost of holding it; 0 = not declared
	Source  string `json:"source"`
	License string `json:"license"`
}

// Catalog is a fetched manifest plus where it was fetched from, which is what
// makes an Entry installable: assets are named relative to the manifest.
type Catalog struct {
	Version   int     `json:"version"`
	Generated string  `json:"generated"`
	Languages []Entry `json:"languages"`

	src source
}

// Find returns the entry for a canonical language code.
func (c *Catalog) Find(code string) (Entry, bool) {
	for _, e := range c.Languages {
		if e.Code == code {
			return e, true
		}
	}
	return Entry{}, false
}

// Fetch reads and validates the manifest at src, which is an http(s) URL, a
// file:// URL, or a plain path on disk. The last two are not a convenience for
// testing: an offline or air-gapped install copies the published folder onto a
// stick and points LEMMA_URL at it, and every check below still applies.
func Fetch(ctx context.Context, src string) (*Catalog, error) {
	if strings.TrimSpace(src) == "" {
		return nil, fmt.Errorf("no lemma catalogue configured (LEMMA_URL)")
	}
	s, err := parseSource(src)
	if err != nil {
		return nil, err
	}
	rc, err := s.open(ctx, "")
	if err != nil {
		return nil, err
	}
	defer rc.Close()

	b, err := io.ReadAll(io.LimitReader(rc, maxManifest+1))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", src, err)
	}
	if len(b) > maxManifest {
		return nil, fmt.Errorf("%s: not a lemma catalogue (larger than %d KB)", src, maxManifest>>10)
	}
	var cat Catalog
	if err := json.Unmarshal(b, &cat); err != nil {
		return nil, fmt.Errorf("%s: not a lemma catalogue: %w", src, err)
	}
	if cat.Version != Version {
		return nil, fmt.Errorf("%s: catalogue version %d, this wudict reads version %d - upgrade wudict",
			src, cat.Version, Version)
	}
	if err := cat.validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", src, err)
	}
	cat.src = s
	return &cat, nil
}

// validate rejects the whole catalogue on the first bad entry rather than
// quietly skipping it. A manifest is generated, not hand-written: one broken
// row means the generator is broken, and a list that silently lost a language
// is a bug report nobody can file.
func (c *Catalog) validate() error {
	seen := make(map[string]bool, len(c.Languages))
	for i := range c.Languages {
		e := &c.Languages[i]
		code := lang.Normalize(e.Code)
		if code == "" {
			return fmt.Errorf("entry %d: %q names no language wudict knows", i, e.Code)
		}
		e.Code = code
		if seen[code] {
			return fmt.Errorf("entry %d: %s listed twice", i, code)
		}
		seen[code] = true

		if e.File == "" || e.File != path.Base(e.File) ||
			strings.ContainsAny(e.File, `/\`) || strings.HasPrefix(e.File, ".") {
			return fmt.Errorf("%s: %q is not a plain file name", code, e.File)
		}
		if ext(e.File) == "" {
			return fmt.Errorf("%s: %q is not lemma data (%s)", code, e.File,
				strings.Join(lemmaExts, ", "))
		}
		if e.Size <= 0 || e.Size > morph.MaxPackBytes {
			return fmt.Errorf("%s: declared size %d is out of range", code, e.Size)
		}
		if e.RawSize < 0 || e.RawSize > morph.MaxPackBytes {
			// The cap that matters: morph refuses to decompress past it, so
			// installing such a file would produce a language that never loads.
			return fmt.Errorf("%s: uncompressed size %d exceeds the %d MB limit",
				code, e.RawSize, morph.MaxPackBytes>>20)
		}
		if !isSHA256(e.SHA256) {
			return fmt.Errorf("%s: %q is not a sha256 digest", code, e.SHA256)
		}
		if e.Name == "" {
			e.Name = code
		}
		if e.Lemmas < 0 {
			e.Lemmas = 0
		}
		if e.HeapMB < 0 {
			e.HeapMB = 0
		}
	}
	return nil
}

// lemmaExts are the names a published asset may have - the same set
// internal/morph reads, because the file is going into LEMMA_DIR unchanged.
var lemmaExts = []string{".tsv.gz", ".txt.gz", ".tsv", ".txt"}

func ext(name string) string {
	low := strings.ToLower(name)
	for _, e := range lemmaExts {
		if strings.HasSuffix(low, e) && len(low) > len(e) {
			return e
		}
	}
	return ""
}

// LocalName is what the entry is called once installed: the language code
// wudict resolved, plus the published extension. Built here rather than taken
// from the manifest so a catalogue can choose WHAT wudict downloads and never
// WHERE it writes - and so two catalogues naming the same language differently
// ("polish.tsv.gz", "pl.tsv.gz") still install over each other instead of
// leaving two files fighting for one language.
func (e Entry) LocalName() string { return e.Code + ext(e.File) }

func isSHA256(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// source is a manifest and the folder its assets sit in - one or the other of
// a URL and a directory, never both.
type source struct {
	manifest *url.URL // http(s) only
	dir      string   // asset folder, when the manifest is a local file
	file     string   // manifest path, when local
}

func parseSource(src string) (source, error) {
	if strings.HasPrefix(src, "http://") || strings.HasPrefix(src, "https://") {
		u, err := url.Parse(src)
		if err != nil {
			return source{}, fmt.Errorf("%s: %w", src, err)
		}
		return source{manifest: u}, nil
	}
	p := src
	if strings.HasPrefix(src, "file://") {
		u, err := url.Parse(src)
		if err != nil {
			return source{}, fmt.Errorf("%s: %w", src, err)
		}
		p = u.Path
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return source{}, err
	}
	return source{dir: filepath.Dir(abs), file: abs}, nil
}

// String is what an error message shows the user: the thing they configured.
func (s source) String() string {
	if s.manifest != nil {
		return s.manifest.String()
	}
	return s.file
}

// open returns the manifest when name is "", else the asset of that name from
// the same folder. The name has already been validated as a bare file name,
// so joining it cannot escape the folder.
func (s source) open(ctx context.Context, name string) (io.ReadCloser, error) {
	if s.manifest == nil {
		p := s.file
		if name != "" {
			p = filepath.Join(s.dir, name)
		}
		f, err := os.Open(p)
		if err != nil {
			return nil, err
		}
		return f, nil
	}
	u := *s.manifest
	if name != "" {
		u.Path = path.Join(path.Dir(u.Path), name)
	}
	return httpGet(ctx, &u)
}

// client uses the environment's proxy - wudict is frequently run inside a
// network that has one, and a download that ignores it fails with a timeout
// instead of a reason. The timeouts are per phase rather than one overall
// deadline, because a slow 2 MB file on a slow connection is legitimate while
// a server that accepts and then says nothing is not.
var client = &http.Client{
	Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 15 * time.Second}).DialContext,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
	},
}

// httpGet does not retry. A failure here is one line the user can read and act
// on - a typo, no network, a 404 - and a silent backoff loop would turn all
// three into the same thirty-second pause with no explanation.
func httpGet(ctx context.Context, u *url.URL) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("%s: %s", u, resp.Status)
	}
	return resp.Body, nil
}
