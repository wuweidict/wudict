// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package intake

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/wuweidict/wudict/internal/dict"
)

// site serves a fixed set of file names and records every path asked for. What
// it is NOT asked is half of what these tests check: the series that stops at a
// gap and the group that stops at a hit are both statements about requests not
// made.
type site struct {
	mu    sync.Mutex
	files map[string]int64 // "/dicts/Oxford.mdd" -> size
	seen  []string
	// headStatus, when set, is what HEAD is answered with instead of serving:
	// the shape of the hosts that refuse the method and serve the file.
	headStatus int
	// noLength suppresses the size, for a server that declares nothing.
	noLength bool
}

func (s *site) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.seen = append(s.seen, r.URL.Path)
	size, ok := s.files[r.URL.Path]
	head := s.headStatus
	noLen := s.noLength
	s.mu.Unlock()

	if r.Method == http.MethodHead && head != 0 {
		w.WriteHeader(head)
		return
	}
	if !ok {
		http.NotFound(w, r)
		return
	}
	if noLen {
		w.WriteHeader(http.StatusOK)
		return
	}
	if rng := r.Header.Get("Range"); rng != "" && r.Method == http.MethodGet {
		w.Header().Set("Content-Range", "bytes 0-0/"+strconv.FormatInt(size, 10))
		w.Header().Set("Content-Length", "1")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write([]byte{0})
		return
	}
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodGet {
		_, _ = w.Write(make([]byte, size))
	}
}

func (s *site) asked(p string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, v := range s.seen {
		if v == p {
			return true
		}
	}
	return false
}

func (s *site) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.seen)
}

// serve starts a site holding the named files under /dicts, each 1000 bytes.
func serve(t *testing.T, names ...string) (*site, *httptest.Server) {
	t.Helper()
	s := &site{files: map[string]int64{}}
	for i, n := range names {
		s.files["/dicts/"+n] = int64(1000 + i)
	}
	srv := httptest.NewServer(s)
	t.Cleanup(srv.Close)
	return s, srv
}

// probeFor runs a probe for mainName against srv, with dir as the folder the
// download landed in.
func probeFor(t *testing.T, srv *httptest.Server, mainName, dir string) []Extra {
	t.Helper()
	f := loopback()
	u, err := f.Check(srv.URL + path.Join("/dicts", mainName))
	if err != nil {
		t.Fatal(err)
	}
	return probeCompanions(context.Background(), f, u, mainName, dir)
}

func extraNames(extras []Extra) []string {
	out := make([]string, 0, len(extras))
	for _, e := range extras {
		out = append(out, e.Name)
	}
	return out
}

// The series the user described: .mdd, then .1.mdd, .2.mdd, stopping at the
// first one that is not there.
func TestProbeMDDSeriesStopsAtTheFirstGap(t *testing.T) {
	s, srv := serve(t, "Oxford.mdd", "Oxford.1.mdd", "Oxford.2.mdd", "Oxford.4.mdd")
	got := probeFor(t, srv, "Oxford.mdx", t.TempDir())

	want := []string{"Oxford.mdd", "Oxford.1.mdd", "Oxford.2.mdd"}
	if !equal(extraNames(got), want) {
		t.Fatalf("extras = %v, want %v", extraNames(got), want)
	}
	// .3.mdd is the gap: asked once, and the run ends there. .4.mdd exists on
	// the server and must never be reached, or the stop rule is not a rule.
	if !s.asked("/dicts/Oxford.3.mdd") {
		t.Error("the gap itself must be asked about")
	}
	if s.asked("/dicts/Oxford.4.mdd") {
		t.Error("the series continued past the gap")
	}
	for _, e := range got {
		if e.Need != NeedMedia {
			t.Errorf("%s: need = %q, want %q", e.Name, e.Need, NeedMedia)
		}
		if e.Size == 0 {
			t.Errorf("%s: size not reported", e.Name)
		}
	}
}

// A host that refuses HEAD still has its files found: the one-byte ranged GET
// is the fallback, and the total comes out of Content-Range.
func TestProbeFallsBackToRangedGET(t *testing.T) {
	s, srv := serve(t, "Oxford.mdd")
	s.mu.Lock()
	s.headStatus = http.StatusMethodNotAllowed
	s.mu.Unlock()

	got := probeFor(t, srv, "Oxford.mdx", t.TempDir())
	if !equal(extraNames(got), []string{"Oxford.mdd"}) {
		t.Fatalf("extras = %v", extraNames(got))
	}
	if got[0].Size != 1000 {
		t.Errorf("size = %d, want the Content-Range total 1000", got[0].Size)
	}
}

// A 403 to HEAD is the other spelling of the same refusal, and a genuine 404
// after it is still a miss.
func TestProbeForbiddenHeadIsNotAMiss(t *testing.T) {
	s, srv := serve(t, "Oxford.mdd")
	s.mu.Lock()
	s.headStatus = http.StatusForbidden
	s.mu.Unlock()
	if got := probeFor(t, srv, "Oxford.mdx", t.TempDir()); len(got) != 1 {
		t.Fatalf("extras = %v", extraNames(got))
	}
}

// A server that declares no length reports the file, not a size: a made-up
// number is worse than an absent one.
func TestProbeWithoutContentLength(t *testing.T) {
	s, srv := serve(t, "Oxford.mdd")
	s.mu.Lock()
	s.noLength = true
	s.mu.Unlock()
	got := probeFor(t, srv, "Oxford.mdx", t.TempDir())
	if len(got) != 1 || got[0].Size != 0 {
		t.Fatalf("extras = %+v, want one with no size", got)
	}
}

// StarDict: the index and the article blob are each published under several
// spellings, and those are alternatives, not a list. The first hit ends the
// group; the optional companions are asked one by one because they are
// different files.
func TestProbeStarDictGroupsStopAtTheFirstHit(t *testing.T) {
	s, srv := serve(t, "abc.idx.gz", "abc.dict.dz", "abc.syn", "res.zip")
	got := probeFor(t, srv, "abc.ifo", t.TempDir())

	byName := map[string]string{}
	for _, e := range got {
		byName[e.Name] = e.Need
	}
	for name, need := range map[string]string{
		"abc.idx.gz":  NeedRequired,
		"abc.dict.dz": NeedRequired,
		"abc.syn":     NeedExtra,
		"res.zip":     NeedMedia,
	} {
		if byName[name] != need {
			t.Errorf("%s: need = %q, want %q (extras %v)", name, byName[name], need, extraNames(got))
		}
	}
	if len(got) != 4 {
		t.Fatalf("extras = %v, want exactly the four", extraNames(got))
	}
	// ".idx" missed and ".idx.gz" hit, so ".idx.dz" is a question already
	// answered.
	if !s.asked("/dicts/abc.idx") {
		t.Error("the first spelling must be asked about")
	}
	if s.asked("/dicts/abc.idx.dz") {
		t.Error("the group continued past a hit")
	}
	// ".dict" is asked and misses, ".dict.dz" is asked and hits: the group
	// only stops once something is found.
	if !s.asked("/dicts/abc.dict") || !s.asked("/dicts/abc.dict.dz") {
		t.Error("both spellings of the article blob should have been asked")
	}
}

// A file already in the download folder is not offered and costs no request -
// the sibling scan takes it off disk. It must also not end the numbered run,
// which is what would happen if "already here" and "not there" were the same
// answer.
func TestProbeSkipsWhatIsAlreadyOnDisk(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"Oxford.mdx":   "main",
		"Oxford.1.mdd": "already downloaded",
	})
	s, srv := serve(t, "Oxford.1.mdd", "Oxford.2.mdd")
	got := probeFor(t, srv, "Oxford.mdx", dir)

	if !equal(extraNames(got), []string{"Oxford.2.mdd"}) {
		t.Fatalf("extras = %v, want only the part that is not here yet", extraNames(got))
	}
	if s.asked("/dicts/Oxford.1.mdd") {
		t.Error("a file already on disk was asked for anyway")
	}
}

// A companion has no companions of its own: sharing "oxford.mdd" is sharing
// half a dictionary, and probing from it would look for "oxford.mdd.mdd".
func TestProbeOnlyFromAMainFile(t *testing.T) {
	_, srv := serve(t, "Oxford.mdd")
	for _, name := range []string{"Oxford.mdd", "readme.txt", "bundle.zip"} {
		if got := probeFor(t, srv, name, t.TempDir()); got != nil {
			t.Errorf("probe from %q returned %v", name, extraNames(got))
		}
	}
}

// Nothing there is nothing offered, and the asking is bounded: a dictionary
// whose companions do not exist must not cost an unbounded scan.
func TestProbeFindsNothingAndStaysBounded(t *testing.T) {
	s, srv := serve(t)
	if got := probeFor(t, srv, "abc.ifo", t.TempDir()); len(got) != 0 {
		t.Fatalf("extras = %v", extraNames(got))
	}
	if n := s.count(); n > maxProbes {
		t.Errorf("%d requests, budget is %d", n, maxProbes)
	}
}

// The sibling URL is derived, never taken from anywhere else: same host, same
// folder, and the query string of the original - which may be a session token -
// is not carried onto it.
func TestSiblingURL(t *testing.T) {
	f := loopback()
	base, err := url.Parse("https://example.com/files/dicts/Oxford.mdx?sig=abc123#frag")
	if err != nil {
		t.Fatal(err)
	}
	u, ok := sibling(f, base, "Oxford.mdd")
	if !ok {
		t.Fatal("sibling refused")
	}
	if got := u.String(); got != "https://example.com/files/dicts/Oxford.mdd" {
		t.Fatalf("sibling = %q", got)
	}

	// And the policy is applied again: a fetcher that would refuse the host
	// refuses the sibling too.
	strict := Fetcher{Hosts: []string{"freemdict.com"}}
	if _, ok := sibling(strict, base, "Oxford.mdd"); ok {
		t.Error("an unlisted host was accepted for a derived URL")
	}
}

func TestContentRangeTotal(t *testing.T) {
	cases := map[string]int64{
		"bytes 0-0/12345": 12345,
		"bytes 0-0/*":     0,
		"":                0,
		"nonsense":        0,
		"bytes 0-0/-3":    0,
	}
	keys := make([]string, 0, len(cases))
	for k := range cases {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if got := contentRangeTotal(k); got != cases[k] {
			t.Errorf("contentRangeTotal(%q) = %d, want %d", k, got, cases[k])
		}
	}
}

// The case that prompted this: a directory listing offering OED.mdx, OED.mdd
// and OED.css as three links. Sharing the first has to find the other two -
// the stylesheet included, because an MDX repack keeps it loose beside the
// .mdx and without it the articles render as unstyled text.
func TestProbeFindsLooseStylesheetAndScript(t *testing.T) {
	_, srv := serve(t, "OED.mdd", "OED.css", "OED.js")
	got := probeFor(t, srv, "OED.mdx", t.TempDir())

	if !equal(extraNames(got), []string{"OED.css", "OED.js", "OED.mdd"}) {
		t.Fatalf("extras = %v", extraNames(got))
	}
	for _, e := range got[:2] {
		if e.Need != NeedExtra {
			t.Errorf("%s: need = %q, want %q", e.Name, e.Need, NeedExtra)
		}
	}
	if got[2].Need != NeedMedia {
		t.Errorf("%s: need = %q, want %q", got[2].Name, got[2].Need, NeedMedia)
	}
}

// A share sheet hands over the href exactly as the page wrote it, and a listing
// whose base already ends in "/" produces "…/OED//OED.mdx". The download works
// on that URL, so the probe has to work from it too.
func TestProbeFromADoubledSlashLink(t *testing.T) {
	s, srv := serve(t, "OED.mdd")
	f := loopback()
	u, err := f.Check(srv.URL + "/dicts//OED.mdx")
	if err != nil {
		t.Fatal(err)
	}
	got := probeCompanions(context.Background(), f, u, "OED.mdx", t.TempDir())
	if !equal(extraNames(got), []string{"OED.mdd"}) {
		t.Fatalf("extras = %v", extraNames(got))
	}
	if !s.asked("/dicts/OED.mdd") {
		t.Errorf("asked %v", s.seen)
	}
}

// A stylesheet is a companion, and a companion is not a link this package acts
// on: ".css" and ".js" name a dictionary to nobody, so they are found from the
// main file and never begin an import of their own.
func TestLooseAssetsAreCarriedNotImported(t *testing.T) {
	for _, n := range []string{"style.css", "OED.css", "entry.js"} {
		if Installable(n) {
			t.Errorf("Installable(%q) = true", n)
		}
	}
	for _, n := range []string{"OED.mdx", "OED.mdd", "star.ifo", "star.idx"} {
		if !Installable(n) {
			t.Errorf("Installable(%q) = false", n)
		}
	}
}

// The state a confirmed job passes through when companions were ticked, which
// is the contract every polling surface is written against: it goes BACK to
// downloading - the extras are fetched before the set is re-sniffed with them
// in it - and only then installs. A poller that reads any non-installing state
// as "the job is gone" reports nothing was added over an import that is still
// running and about to succeed.
//
// Observed by HOLDING the companion's response open rather than by sampling,
// so the assertion does not depend on a thousand bytes over loopback taking
// long enough to be seen.
func TestConfirmWithExtrasDownloadsBeforeInstalling(t *testing.T) {
	base := &site{files: map[string]int64{"/dicts/OED.mdx": 1000, "/dicts/OED.mdd": 4000}}
	held, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The one GET for the companion is its download: the probe settles the
		// question with a HEAD, which this server answers.
		if r.Method == http.MethodGet && r.URL.Path == "/dicts/OED.mdd" {
			once.Do(func() {
				close(held)
				<-release
			})
		}
		base.ServeHTTP(w, r)
	}))
	defer srv.Close()
	dest := t.TempDir()

	m := &Manager{}
	if _, err := m.BeginURL(dest, srv.URL+"/dicts/OED.mdx", loopback()); err != nil {
		t.Fatalf("BeginURL: %v", err)
	}
	m.Wait()
	st := m.Status()
	if st.State != StateReady {
		t.Fatalf("state = %q (%s), want %q", st.State, st.Error, StateReady)
	}
	at := -1
	for i, e := range st.Extras {
		if e.Name == "OED.mdd" {
			at = i
		}
	}
	if at < 0 {
		t.Fatalf("the companion was not offered: %v", extraNames(st.Extras))
	}

	if _, err := m.Confirm(dest, []int{0}, Options{Keep: true, Extras: []int{at}}); err != nil {
		t.Fatalf("Confirm: %v", err)
	}

	select {
	case <-held:
	case <-time.After(10 * time.Second):
		t.Fatal("the companion was never fetched")
	}
	// The job is mid-companion, so this is what a shell polling right now
	// reads. Not "installing", and emphatically not nothing.
	switch mid := m.Status(); {
	case mid.State != StateDownloading:
		close(release)
		t.Fatalf("state while fetching a companion = %q, want %q", mid.State, StateDownloading)
	case mid.Source != "OED.mdd":
		// Its OWN name, or the line repeats the dictionary's while a second,
		// much larger file is what is actually arriving.
		close(release)
		t.Fatalf("source while fetching a companion = %q, want OED.mdd", mid.Source)
	}
	close(release)

	m.Wait()
	st = m.Status()
	if st.State != StateDone {
		t.Fatalf("state = %q (%s), want %q", st.State, st.Error, StateDone)
	}
	if len(st.Installed) == 0 {
		t.Fatalf("installed nothing: %+v", st)
	}
	// The companion is part of the installed dictionary, not merely fetched.
	if _, err := os.Stat(filepath.Join(dest, st.Installed[0], "OED.mdd")); err != nil {
		t.Fatalf("the companion did not reach the library: %v", err)
	}
}

// A split ZIM's parts are all required, asked for in order past the probe
// budget (30 parts here, maxProbes is 24), and the run ends at the first gap.
func TestProbeZIMPartsRunPastTheBudget(t *testing.T) {
	var names []string
	for _, suf := range dict.ZIMPartSuffixes()[:30] {
		names = append(names, "Wiki"+suf)
	}
	beyond := "Wiki" + dict.ZIMPartSuffixes()[31] // [30] is the gap
	s, srv := serve(t, append(names, beyond)...)
	got := probeFor(t, srv, "Wiki.zimaa", t.TempDir())

	if !equal(extraNames(got), names) {
		t.Fatalf("extras = %v, want the 30 parts in order", extraNames(got))
	}
	for _, e := range got {
		if e.Need != NeedRequired {
			t.Errorf("%s: need = %q, want %q", e.Name, e.Need, NeedRequired)
		}
	}
	if !s.asked("/dicts/Wiki" + dict.ZIMPartSuffixes()[30]) {
		t.Error("the gap itself must be asked about")
	}
	if s.asked("/dicts/" + beyond) {
		t.Error("the run continued past the gap")
	}
}
