// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Command lemmafiles builds the lemma catalogue `wudict lemmas` installs from
// (D87, D88). It is a maintainer's tool, not part of the product:
// `make lemma-files`. The wudict binary does not link it and does not need it.
//
// # Where the data comes from
//
// github.com/michmech/lemmatization-lists - 24 languages, one
// `lemmatization-<code>.txt` per language, each line `lemma<TAB>form`. That
// repository is golem's own upstream: golem's published packs are these lists
// run through its `cmd/simplify` and embedded in Go source. Reading the lists
// directly rather than golem's `dicts/<lang>` modules buys three things:
//
//   - 24 languages instead of the 8 golem publishes,
//   - no dependency on a populated Go module cache, which is not something a
//     release build can assume and not something a user has at all,
//   - no coupling to the formatting of generated Go source.
//
// # What it emits
//
//	<out>/<code>.tsv.gz    the lemma data, exactly what LEMMA_DIR reads
//	<out>/manifest.json    the catalogue LEMMA_URL points at
//	<out>/ATTRIBUTION.txt  the ODbL notice redistribution requires
//
// Publishing is deliberately NOT automated here: upload the folder wherever
// LEMMA_URL points (a release asset, a repository path, a static host). The
// client trusts sha256 and nothing else, so the host is interchangeable.
//
// # What it does to the data
//
// Per language: strip BOM, lower-case, drop the "\r" a Windows-authored list
// carries, drop lines with fewer than two fields, then GROUP - michmech
// repeats the lemma once per form, and golem's parser indexes every field on
// a line against the first, so `lemma<TAB>form1<TAB>form2` says exactly what
// three separate pairs say in a third of the bytes.
//
// Grouping is not purely cosmetic and this is the honest caveat: when one form
// belongs to two lemmas, golem keeps the lemma of the line that introduced the
// form first, and moving a lemma's later lines up next to its first can change
// which line that is. Both answers were already arbitrary - golem stores one
// base per word and picks it by input order - and this is what golem's own
// pipeline does to the same lists, so the shipped files stay comparable to the
// packs it publishes.
//
// A language internal/lang cannot resolve to an ISO 639-1 code is skipped with
// a warning rather than written: wudict indexes LEMMA_DIR by language, so a
// file it can never attribute to a dictionary is dead weight in a download.
package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/aaaton/golem/v4"

	"github.com/wuweidict/wudict/internal/lang"
	"github.com/wuweidict/wudict/internal/lemmas"
	"github.com/wuweidict/wudict/internal/morph"
)

// defaultBase is where -fetch reads from. Raw file access, not the contents
// API: the API is rate limited per IP and this tool would hit it 24 times.
const defaultBase = "https://raw.githubusercontent.com/michmech/lemmatization-lists/master/"

// upstream is what -fetch asks for, because a raw endpoint cannot be listed.
// It is the michmech set as of 2026-09-01; a language added there is added
// here, and -src needs no list at all because a directory can be read.
var upstream = []string{
	"ast", "bg", "ca", "cs", "cy", "de", "en", "es", "et", "fa", "fr", "ga",
	"gd", "gl", "gv", "hu", "it", "pt", "ro", "ru", "sk", "sl", "sv", "uk",
}

const attribution = `Lemma data shipped with WuWeiDict
=================================

Source:  https://github.com/michmech/lemmatization-lists
License: Open Database License (ODbL) v1.0
         https://opendatacommons.org/licenses/odbl/1-0/

These files are a derived, reformatted copy of the lemmatization lists
published by Michal Boleslav Mechura: lower-cased, and with the forms of one
lemma grouped onto a single tab-separated line. No entries were added and none
were translated.

The ODbL is a share-alike licence. If you redistribute these files, or a
database derived from them, you must keep this notice, credit the source, and
offer the derived database under the same licence.

Languages in this catalogue:
`

type input struct {
	raw  string // the code as the file names it
	code string // resolved ISO 639-1
	name string
	open func(context.Context) (io.ReadCloser, error)
}

func main() {
	out := flag.String("o", "dist/lemmas", "output directory (created if missing)")
	src := flag.String("src", "", "directory holding lemmatization-<code>.txt files")
	fetch := flag.Bool("fetch", false, "download the lists from michmech instead of -src")
	base := flag.String("base", defaultBase, "base URL for -fetch")
	flag.Parse()

	if (*src == "") == !*fetch {
		fmt.Fprintln(os.Stderr, "lemmafiles: give exactly one of -src <dir> or -fetch")
		os.Exit(2)
	}
	only := map[string]bool{}
	for _, a := range flag.Args() {
		only[strings.ToLower(a)] = true
	}
	if err := run(*out, *src, *base, *fetch, only); err != nil {
		fmt.Fprintln(os.Stderr, "lemmafiles:", err)
		os.Exit(1)
	}
}

func run(out, src, base string, fetch bool, only map[string]bool) error {
	ins, err := inputs(src, base, fetch)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}

	ctx := context.Background()
	var entries []lemmas.Entry
	for _, in := range ins {
		if len(only) > 0 && !only[in.raw] && !only[in.code] {
			continue
		}
		e, err := build(ctx, out, in)
		if err != nil {
			return fmt.Errorf("%s: %w", in.raw, err)
		}
		entries = append(entries, e)
		fmt.Printf("%-4s %-12s %8s gz  %8s raw  %6d lemmas  ~%d MB RAM\n",
			e.Code, e.Name, human(e.Size), human(e.RawSize), e.Lemmas, e.HeapMB)
	}
	if len(entries) == 0 {
		return fmt.Errorf("no lemma lists found")
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Code < entries[j].Code })

	if err := writeManifest(out, entries); err != nil {
		return err
	}
	if err := writeAttribution(out, entries); err != nil {
		return err
	}
	fmt.Printf("\n%d languages in %s\n", len(entries), out)
	return nil
}

// inputs resolves what is to be built, and resolves the language BEFORE
// reading anything: a list wudict could not name is not worth downloading.
func inputs(src, base string, fetch bool) ([]input, error) {
	var raws []string
	if fetch {
		raws = upstream
	} else {
		names, err := filepath.Glob(filepath.Join(src, "lemmatization-*.txt"))
		if err != nil {
			return nil, err
		}
		if len(names) == 0 {
			return nil, fmt.Errorf("no lemmatization-*.txt in %s", src)
		}
		sort.Strings(names)
		for _, n := range names {
			stem := strings.TrimSuffix(filepath.Base(n), ".txt")
			raws = append(raws, strings.TrimPrefix(stem, "lemmatization-"))
		}
	}

	seen := map[string]bool{}
	var ins []input
	for _, raw := range raws {
		raw = strings.ToLower(raw)
		code := lang.Normalize(raw)
		if code == "" {
			fmt.Fprintf(os.Stderr, "skipping %q: not a language internal/lang knows\n", raw)
			continue
		}
		if seen[code] {
			fmt.Fprintf(os.Stderr, "skipping %q: %s already supplied\n", raw, code)
			continue
		}
		seen[code] = true

		name := "lemmatization-" + raw + ".txt"
		in := input{raw: raw, code: code, name: lang.Name(code)}
		if fetch {
			u := base + name
			in.open = func(ctx context.Context) (io.ReadCloser, error) { return get(ctx, u) }
		} else {
			p := filepath.Join(src, name)
			in.open = func(context.Context) (io.ReadCloser, error) { return os.Open(p) }
		}
		ins = append(ins, in)
	}
	return ins, nil
}

// build reads one list, writes one asset, and measures what holding it costs.
func build(ctx context.Context, out string, in input) (lemmas.Entry, error) {
	rc, err := in.open(ctx)
	if err != nil {
		return lemmas.Entry{}, err
	}
	raw, err := io.ReadAll(io.LimitReader(rc, morph.MaxPackBytes+1))
	rc.Close()
	if err != nil {
		return lemmas.Entry{}, err
	}
	if len(raw) > morph.MaxPackBytes {
		return lemmas.Entry{}, fmt.Errorf("source list larger than %d MB", morph.MaxPackBytes>>20)
	}

	data, lines := group(raw)
	if lines == 0 {
		return lemmas.Entry{}, fmt.Errorf("no usable lines")
	}
	if len(data) > morph.MaxPackBytes {
		return lemmas.Entry{}, fmt.Errorf("grouped data larger than %d MB", morph.MaxPackBytes>>20)
	}

	// Loading it here is the acceptance test: golem fails a whole file on one
	// malformed line, so a language that cannot be loaded now is a language
	// that would have failed silently on a user's machine.
	heap, err := heapMB(in.code, data)
	if err != nil {
		return lemmas.Entry{}, err
	}

	name := in.code + ".tsv.gz"
	gz, err := squeeze(data)
	if err != nil {
		return lemmas.Entry{}, err
	}
	if err := write(filepath.Join(out, name), gz); err != nil {
		return lemmas.Entry{}, err
	}
	sum := sha256.Sum256(gz)

	return lemmas.Entry{
		Code:    in.code,
		Name:    in.name,
		File:    name,
		Size:    int64(len(gz)),
		RawSize: int64(len(data)),
		Lemmas:  int64(lines),
		SHA256:  hex.EncodeToString(sum[:]),
		HeapMB:  heap,
		Source:  "michmech/lemmatization-lists",
		License: "ODbL-1.0",
	}, nil
}

// group turns michmech's repeated pairs into golem's grouped form, preserving
// first-seen order throughout so the same input always produces the same
// bytes - the manifest publishes a hash of them.
func group(b []byte) ([]byte, int) {
	b = bytes.TrimPrefix(b, []byte("\xef\xbb\xbf"))
	b = bytes.ToLower(b)

	type entry struct {
		forms []string
		seen  map[string]bool
	}
	index := map[string]int{}
	var order []string
	var groups []*entry

	for len(b) > 0 {
		line := b
		if i := bytes.IndexByte(b, '\n'); i >= 0 {
			line, b = b[:i], b[i+1:]
		} else {
			b = nil
		}
		fields := fields(line)
		if len(fields) < 2 {
			continue
		}
		lemma := fields[0]
		g, ok := index[lemma]
		if !ok {
			g = len(groups)
			index[lemma] = g
			order = append(order, lemma)
			groups = append(groups, &entry{seen: map[string]bool{lemma: true}})
		}
		for _, f := range fields[1:] {
			// A form equal to its own lemma is dropped: golem indexes the
			// first field too, so the mapping is already there.
			if groups[g].seen[f] {
				continue
			}
			groups[g].seen[f] = true
			groups[g].forms = append(groups[g].forms, f)
		}
	}

	var out bytes.Buffer
	n := 0
	for i, lemma := range order {
		g := groups[i]
		if len(g.forms) == 0 {
			continue // one field is a line golem refuses; it says nothing anyway
		}
		out.WriteString(lemma)
		for _, f := range g.forms {
			out.WriteByte('\t')
			out.WriteString(f)
		}
		out.WriteByte('\n')
		n++
	}
	return out.Bytes(), n
}

// fields splits one line into non-empty trimmed fields. TrimSpace is what
// removes the "\r" of a CRLF list, and it is applied per field rather than per
// line so a multi-word form keeps the space inside it.
func fields(line []byte) []string {
	var out []string
	for len(line) > 0 {
		f := line
		if i := bytes.IndexByte(line, '\t'); i >= 0 {
			f, line = line[:i], line[i+1:]
		} else {
			line = nil
		}
		if f = bytes.TrimSpace(f); len(f) > 0 {
			out = append(out, string(f))
		}
	}
	return out
}

type bytePack struct {
	code string
	data []byte
}

func (p bytePack) GetLocale() string            { return p.code }
func (p bytePack) GetResource() ([]byte, error) { return p.data, nil }

// heapMB measures what golem's map for this language costs, live, rather than
// guessing from the file size - the ratio is between 6x and 10x depending on
// how long the words are, which is too wide a band to publish an estimate
// from. `wudict lemmas list` shows this so a user with a small machine (or
// Android, D52) can see that Russian is 65 MB before installing it.
func heapMB(code string, data []byte) (int, error) {
	runtime.GC()
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)

	l, err := golem.New(bytePack{code, data})
	if err != nil {
		return 0, err
	}
	runtime.GC()
	runtime.ReadMemStats(&after)
	runtime.KeepAlive(l)

	var d uint64
	if after.HeapAlloc > before.HeapAlloc {
		d = after.HeapAlloc - before.HeapAlloc
	}
	mb := int((d + (1 << 20) - 1) >> 20)
	if mb < 1 {
		mb = 1
	}
	return mb, nil
}

func squeeze(b []byte) ([]byte, error) {
	var buf bytes.Buffer
	zw, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return nil, err
	}
	if _, err := zw.Write(b); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// write goes through a temporary file in the same directory: a half-written
// lemma file is one golem would refuse to load, and overwriting a good one
// with it because the disk filled is not a trade worth making.
func write(path string, b []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".lemmafiles-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)

	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func writeManifest(out string, entries []lemmas.Entry) error {
	b, err := json.MarshalIndent(lemmas.Catalog{
		Version:   lemmas.Version,
		Generated: time.Now().UTC().Format(time.RFC3339),
		Languages: entries,
	}, "", "  ")
	if err != nil {
		return err
	}
	return write(filepath.Join(out, "manifest.json"), append(b, '\n'))
}

func writeAttribution(out string, entries []lemmas.Entry) error {
	var b strings.Builder
	b.WriteString(attribution)
	for _, e := range entries {
		fmt.Fprintf(&b, "  %-4s %-12s %s\n", e.Code, e.Name, e.Source)
	}
	return write(filepath.Join(out, "ATTRIBUTION.txt"), []byte(b.String()))
}

func get(ctx context.Context, u string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
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

var client = &http.Client{Transport: &http.Transport{
	Proxy:                 http.ProxyFromEnvironment,
	DialContext:           (&net.Dialer{Timeout: 15 * time.Second}).DialContext,
	TLSHandshakeTimeout:   15 * time.Second,
	ResponseHeaderTimeout: 30 * time.Second,
}}

func human(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}
