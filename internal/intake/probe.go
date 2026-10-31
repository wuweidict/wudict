// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package intake

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/wuweidict/wudict/internal/dict"
)

// Finding the other half of a dictionary that was offered as several links.
//
// The sites these formats come from routinely publish "oxford.mdx" and
// "oxford.mdd" as two entries in one list, and a StarDict dictionary as three
// or four. A user who shares the first link has, from their point of view,
// shared the dictionary - so after the main file lands, the siblings dict's
// tables say it can have are LOOKED FOR at the same location, by name.
//
// Two rules keep this from being a crawler. Nothing is guessed that the format
// tables do not already name; and nothing is downloaded on the strength of a
// probe - what a probe produces is a list the user is shown and ticks, because
// a companion can be hundreds of megabytes on somebody's phone data, and
// fetching it because it happened to exist is a decision that is not ours to
// make.
//
// The probe itself is a HEAD, falling back to a one-byte ranged GET for the
// servers that refuse HEAD. Either way nothing but headers crosses the wire.

// maxProbes bounds the requests one import may spend looking. The alternative
// spellings of a StarDict index are the widest case in the tables and come to
// well under this; the number exists so that a format growing a long companion
// list cannot turn one import into a scan.
const maxProbes = 24

// maxParts bounds MDict's numbered resource series. Real dictionaries split
// media across a handful of parts; the series stops at the first gap anyway,
// and this is the backstop for a server that answers 200 to everything.
const maxParts = 20

// maxZIMParts bounds a split ZIM's run of parts, which is not counted against
// maxProbes: every part is required, and a probe budget that cut the run short
// would offer an archive that cannot open. 64 parts of 4 GiB is 256 GiB, the
// largest file an import accepts (maxCandidateBytes).
const maxZIMParts = 64

// Extra is a companion file found beside the one that was downloaded, offered
// to the user rather than taken.
type Extra struct {
	// Name is the file as it would be saved, which is also how it is shown:
	// the user recognises "oxford.mdd", and the URL it came from is machinery
	// they neither need nor chose (D102).
	Name string `json:"name"`
	// Size is what the server declared, 0 when it declared nothing.
	Size int64 `json:"size"`
	// Need says why this file is worth the download, in the three grades the
	// tables actually distinguish: "needed" (the dictionary does not work
	// without it), "media" (images and sounds), "extra" (everything else).
	Need string `json:"need"`

	// url is the absolute link. Unexported: it is never shown and never taken
	// from a caller, so a confirmation cannot be steered at another host.
	url string
	// row is the collection row this file belongs to (collection.go); unused
	// for the companions of a single download.
	row int
}

// Need grades.
const (
	NeedRequired = "needed"
	NeedMedia    = "media"
	NeedExtra    = "extra"
)

// probeCompanions looks for the companions of the file just downloaded and
// returns the ones that are there. dir is where the download landed: a
// companion already sitting in it is not offered, because the sibling scan
// will find it without a request.
//
// Errors are not returned. A probe that cannot be made is a companion that is
// not offered, and an import that failed because an optional second file could
// not be asked about would be a worse outcome than one that installs without
// it.
func probeCompanions(ctx context.Context, f Fetcher, base *url.URL, fileName, dir string) []Extra {
	if kindOf(fileName, onDisk(filepath.Join(dir, fileName))) != dict.KindMain {
		return nil // a companion has no companions of its own
	}
	return probeSiblings(ctx, f, base, fileName, func(name string) bool { return have(dir, name) })
}

// probeSiblings is the search itself, for a main file whose name is already
// known to be one. known reports a name that needs no request - on disk
// already, or listed by the collection the file came from (collection.go).
func probeSiblings(ctx context.Context, f Fetcher, base *url.URL, fileName string, known func(string) bool) []Extra {
	stem := dict.Stem(fileName)
	if _, _, mega := megaLink(base); stem == "" || driveID(base) != "" || mega {
		// A Drive or MEGA file's address is its id: there is no "beside it" to ask.
		return nil
	}
	var out []Extra
	spent := 0
	// find asks the server for one name and offers it when it is there.
	find := func(name, need string) bool {
		u, ok := sibling(f, base, name)
		if !ok {
			return false
		}
		h := headInfo(ctx, f, u)
		if !h.found {
			return false
		}
		out = append(out, Extra{Name: name, Size: h.size, Need: need, url: u.String()})
		return true
	}
	// ask reports that the name is ACCOUNTED FOR - already on disk, or found
	// on the server and now offered. The distinction matters twice: a file
	// already downloaded satisfies its group without a request, and it must
	// not break the numbered series that would otherwise continue past it.
	ask := func(name, need string) bool {
		if known(name) {
			return true
		}
		if spent >= maxProbes {
			return false
		}
		spent++
		return find(name, need)
	}

	for _, g := range companionGroups(dict.MainExt(fileName)) {
		for _, suf := range g.suffixes {
			if ask(stem+suf, g.need) && g.firstWins {
				// The alternatives in a group are spellings of ONE file - an
				// index plain, gzipped or dictzipped - so the first one that
				// exists is the answer and the rest are not worth asking.
				break
			}
		}
	}
	if dict.MainExt(fileName) == ".mdx" {
		// MDict's resources are one ".mdd", or a numbered run, or both: the
		// stat-based CompanionMedia treats ".mdd" and ".1.mdd" alike as first
		// parts. So the unnumbered name is asked for on its own - a miss there
		// says nothing about the run - and the run then stops at the first
		// gap, which is the rule that keeps this from being a scan.
		ask(stem+".mdd", NeedMedia)
		for n := 1; n <= maxParts; n++ {
			if !ask(stem+"."+strconv.Itoa(n)+".mdd", NeedMedia) {
				break
			}
		}
	}
	if dict.MainExt(fileName) == ".ifo" {
		// Named after the folder rather than the stem, so it is asked for by
		// its own name or not at all.
		ask("res.zip", NeedMedia)
	}
	if dict.MainExt(fileName) == ".zimaa" {
		// A split ZIM: ".zimab" onwards, each required, the run ending at the
		// first gap - outside the probe budget (maxZIMParts).
		for k, suf := range dict.ZIMPartSuffixes() {
			if k == maxZIMParts || !(known(stem+suf) || find(stem+suf, NeedRequired)) {
				break
			}
		}
	}
	return out
}

// group is one companion slot: the spellings that would satisfy it, and
// whether finding one ends the question.
type group struct {
	suffixes  []string
	need      string
	firstWins bool
}

// companionGroups is dict's tables, arranged as the questions to ask. Required
// groups come first so that the files a dictionary cannot work without are the
// ones a capped probe budget spends itself on.
func companionGroups(mainExt string) []group {
	var out []group
	for _, g := range dict.RequiredCompanions(mainExt) {
		out = append(out, group{suffixes: g, need: NeedRequired, firstWins: true})
	}
	required := map[string]bool{}
	for _, g := range dict.RequiredCompanions(mainExt) {
		for _, s := range g {
			required[s] = true
		}
	}
	var rest []string
	for _, s := range dict.CompanionSuffixes(mainExt) {
		if !required[s] {
			rest = append(rest, s)
		}
	}
	if len(rest) > 0 {
		// Optional extras are asked about one by one: ".syn" and ".ann" are
		// different files, not spellings of one, so finding one says nothing
		// about the next.
		for _, s := range rest {
			out = append(out, group{suffixes: []string{s}, need: NeedExtra})
		}
	}
	for _, s := range dict.MediaSuffixes(mainExt) {
		if s == ".mdd" {
			continue // handled as a numbered series by the caller
		}
		// A Lingvo media zip is written three ways; one of them is the file.
		out = append(out, group{suffixes: dslMediaSpellings(s), need: NeedMedia, firstWins: true})
	}
	return out
}

// dslMediaSpellings is the three names one DSL media archive is published
// under, which the stat-based CompanionMedia resolves with a directory listing
// and this resolves with three questions.
func dslMediaSpellings(suf string) []string {
	if suf != ".files.zip" {
		return []string{suf}
	}
	return []string{".files.zip", ".dsl.files.zip", ".dsl.dz.files.zip"}
}

// sibling is the URL of a file beside base, with the same policy applied to it
// as to the link the user gave: it is a different URL, and a redirect chain
// that ended somewhere unexpected must not become a way to fetch from there.
func sibling(f Fetcher, base *url.URL, name string) (*url.URL, bool) {
	u := *base
	u.RawQuery, u.Fragment = "", ""
	u.Path = path.Join(path.Dir(base.Path), name)
	u.RawPath = ""
	if _, err := f.Check(u.String()); err != nil {
		return nil, false
	}
	return &u, true
}

// have reports whether the file is already in the download folder, in which
// case there is nothing to ask: the sibling scan takes what is on disk.
func have(dir, name string) bool {
	fi, err := os.Stat(filepath.Join(dir, name))
	return err == nil && fi.Mode().IsRegular()
}

// head is what a probe learned about a URL: whether it is there, how big it
// is (0 when the server did not say) and when it last changed (zero when the
// server did not say).
type head struct {
	size     int64
	modified time.Time
	found    bool
	name     string // Content-Disposition's file name, when the server sent one
}

// headInfo asks whether a URL is there, how big it is and how old. A HEAD
// first, because that is the request that exists for this question; a
// one-byte ranged GET after it, because a number of the servers that host
// these files answer HEAD with 403 or 405 and serve the same file perfectly
// well.
func headInfo(ctx context.Context, f Fetcher, u *url.URL) head {
	if h, decisive := probe(ctx, f, u, http.MethodHead, ""); decisive {
		return h
	}
	h, _ := probe(ctx, f, u, http.MethodGet, "bytes=0-0")
	return h
}

// probe makes one request and reports what it learned, and whether the answer
// settles the question. A 404 settles it; a refusal of the METHOD does not.
func probe(ctx context.Context, f Fetcher, u *url.URL, method, rng string) (h head, decisive bool) {
	req, err := http.NewRequestWithContext(ctx, method, u.String(), nil)
	if err != nil {
		return head{}, true
	}
	req.Header.Set("Accept", "*/*")
	if rng != "" {
		req.Header.Set("Range", rng)
	}
	resp, err := f.Client().Do(req)
	if err != nil {
		return head{}, true // unreachable is not "ask again differently"
	}
	defer resp.Body.Close()
	// Drain the byte a ranged probe may have produced, so the connection can
	// be reused rather than torn down for one byte.
	_, _ = io.CopyN(io.Discard, resp.Body, 64)

	// Unparseable or absent is the zero time, which every reader treats as
	// "the site did not say".
	mod, _ := http.ParseTime(resp.Header.Get("Last-Modified"))
	switch resp.StatusCode {
	case http.StatusOK:
		if rng != "" && resp.ContentLength == 1 {
			// The server ignored the range and is about to send the whole
			// file; it exists, and its length is the one header we can trust
			// least here, so it is left unstated rather than guessed.
			return head{modified: mod, found: true, name: dispositionName(resp.Header)}, true
		}
		return head{size: max(resp.ContentLength, 0), modified: mod, found: true, name: dispositionName(resp.Header)}, true
	case http.StatusPartialContent:
		return head{size: contentRangeTotal(resp.Header.Get("Content-Range")), modified: mod, found: true,
			name: dispositionName(resp.Header)}, true
	case http.StatusMethodNotAllowed, http.StatusNotImplemented, http.StatusForbidden:
		return head{}, method != http.MethodHead
	}
	return head{}, true
}

// contentRangeTotal reads the total length out of "bytes 0-0/12345".
func contentRangeTotal(v string) int64 {
	i := strings.LastIndexByte(v, '/')
	if i < 0 {
		return 0
	}
	n, err := strconv.ParseInt(strings.TrimSpace(v[i+1:]), 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	return n
}
