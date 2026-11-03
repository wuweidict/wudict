// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package intake

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Every test here talks to a loopback httptest server, so the fetcher under
// test is the INSECURE one: the address guard exists to stop a request from
// reaching the local network, and a test server is on the local network. The
// guard itself is tested on its own, against a secure fetcher.
func loopback() Fetcher { return Fetcher{Insecure: true} }

func TestFetcherCheck(t *testing.T) {
	f := Fetcher{Hosts: []string{"freemdict.com", "pkemb.com"}}
	cases := []struct {
		name string
		url  string
		ok   bool
	}{
		{"plain host", "https://freemdict.com/a.zip", true},
		{"subdomain", "https://down.freemdict.com/a.zip", true},
		{"other listed host", "https://pkemb.com/a.zip", true},
		// The reason the suffix match is anchored on a dot: this ends with
		// "freemdict.com" as a string and is a different site.
		{"suffix lookalike", "https://notfreemdict.com/a.zip", false},
		{"unlisted", "https://example.com/a.zip", false},
		{"http refused", "http://freemdict.com/a.zip", false},
		{"other scheme", "file:///etc/passwd", false},
		{"not a url", "freemdict.com/a.zip", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := f.Check(c.url)
			if (err == nil) != c.ok {
				t.Fatalf("Check(%q) err=%v, want ok=%v", c.url, err, c.ok)
			}
		})
	}

	// An empty list is no restriction - which a user chooses deliberately and
	// is never the default.
	if _, err := (Fetcher{}).Check("https://anywhere.example/a.zip"); err != nil {
		t.Fatalf("empty allowlist should permit any host: %v", err)
	}
	// Insecure lifts http as well as the address guard, because it is one
	// question.
	if _, err := (Fetcher{Insecure: true}).Check("http://anywhere.example/a.zip"); err != nil {
		t.Fatalf("insecure should permit http: %v", err)
	}

	// The refusal names the setting only where the setting lifts it.
	for _, c := range []struct {
		url  string
		hint bool
	}{
		{"http://anywhere.example/a.zip", true},
		{"ftp://anywhere.example/a.zip", false},
	} {
		_, err := (Fetcher{}).Check(c.url)
		if !errors.Is(err, ErrInsecureURL) {
			t.Fatalf("Check(%q) err = %v, want ErrInsecureURL", c.url, err)
		}
		if got := strings.Contains(err.Error(), "IMPORT_INSECURE=1"); got != c.hint {
			t.Fatalf("Check(%q) err = %q, hint = %v, want %v", c.url, err, got, c.hint)
		}
	}
}

func TestFetchDownloads(t *testing.T) {
	body := strings.Repeat("wudict", 5000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"v1"`)
		w.Header().Set("Content-Disposition", `attachment; filename="bundle.zip"`)
		w.Header().Set("Content-Type", "application/zip")
		http.ServeContent(w, r, "bundle.zip", zeroTime, strings.NewReader(body))
	}))
	defer srv.Close()

	dest := t.TempDir()
	var lastDone, lastTotal int64
	// The URL path deliberately does not name a zip: the name must come from
	// Content-Disposition.
	var lastName string
	src, err := loopback().Fetch(context.Background(), dest, srv.URL+"/get?id=7",
		func(name string, done, total int64) { lastName, lastDone, lastTotal = name, done, total })
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if src.Name != "bundle.zip" {
		t.Fatalf("name = %q, want bundle.zip", src.Name)
	}
	// Never Temp: a download is not an intermediary, and its fate is the
	// user's keep/delete choice rather than the job's teardown.
	if src.Temp {
		t.Fatal("a download must not be marked Temp")
	}
	if want := filepath.Join(linkDirOf(t, dest, srv.URL), "bundle.zip"); src.Path != want {
		t.Fatalf("path = %q, want %q", src.Path, want)
	}
	got, err := os.ReadFile(src.Path)
	if err != nil || string(got) != body {
		t.Fatalf("content mismatch (err %v, %d bytes)", err, len(got))
	}
	if lastDone != int64(len(body)) || lastTotal != int64(len(body)) {
		t.Fatalf("progress ended at %d/%d, want %d/%d", lastDone, lastTotal, len(body), len(body))
	}
	// The name the server decided, carried by the progress report itself: it
	// is the only place a caller can learn it before the download finishes.
	if lastName != "bundle.zip" {
		t.Fatalf("progress name = %q, want bundle.zip", lastName)
	}
	// Nothing unfinished is left behind under a name anything would take
	// seriously.
	if _, err := os.Stat(src.Path + ".part"); !os.IsNotExist(err) {
		t.Fatal(".part survived a completed download")
	}
	if _, err := os.Stat(src.Path + ".part.meta"); !os.IsNotExist(err) {
		t.Fatal(".part.meta survived a completed download")
	}
}

// The case the .part file exists for: a transfer that stopped half way is
// continued rather than fetched again.
func TestFetchResumes(t *testing.T) {
	body := strings.Repeat("abcdefgh", 4096)
	const cut = 9000
	var ranged string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ranged = r.Header.Get("Range")
		if got := r.Header.Get("If-Range"); got != `"v1"` {
			t.Errorf("If-Range = %q, want the recorded validator", got)
		}
		w.Header().Set("ETag", `"v1"`)
		http.ServeContent(w, r, "bundle.zip", zeroTime, strings.NewReader(body))
	}))
	defer srv.Close()

	dest := t.TempDir()
	dir := linkDirOf(t, dest, srv.URL)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	url := srv.URL + "/bundle.zip"
	part := filepath.Join(dir, "bundle.zip.part")
	if err := os.WriteFile(part, []byte(body[:cut]), 0o644); err != nil {
		t.Fatal(err)
	}
	writeMeta(part+".meta", partMeta{URL: url, ETag: `"v1"`, Total: int64(len(body))})

	src, err := loopback().Fetch(context.Background(), dest, url, nil)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if ranged == "" {
		t.Fatal("no Range header: the partial file was ignored")
	}
	got, err := os.ReadFile(src.Path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != body {
		t.Fatalf("resumed file is %d bytes and does not match the source", len(got))
	}
}

// The failure resuming is there to prevent: the file behind the URL changed,
// so the server answers the whole thing and the old head must be DISCARDED
// rather than kept as a prefix. A spliced file is corrupt in a way nothing
// downstream would catch.
func TestFetchRestartsWhenTheFileChanged(t *testing.T) {
	body := strings.Repeat("xyz", 3000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A server whose validator no longer matches answers 200, not 206.
		w.Header().Set("ETag", `"v2"`)
		w.Header().Set("Content-Length", itoa(len(body)))
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	dest := t.TempDir()
	dir := linkDirOf(t, dest, srv.URL)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	url := srv.URL + "/bundle.zip"
	part := filepath.Join(dir, "bundle.zip.part")
	if err := os.WriteFile(part, []byte("stale bytes from another file"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeMeta(part+".meta", partMeta{URL: url, ETag: `"v1"`})

	src, err := loopback().Fetch(context.Background(), dest, url, nil)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	got, err := os.ReadFile(src.Path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != body {
		t.Fatalf("file was spliced: %d bytes, want %d", len(got), len(body))
	}
}

// A .part with no sidecar has no provenance, so there is nothing to resume
// safely from and it is started over.
func TestFetchIgnoresPartialWithoutMeta(t *testing.T) {
	body := "0123456789"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") != "" {
			t.Error("ranged request for a partial with no recorded validator")
		}
		w.Header().Set("Content-Length", itoa(len(body)))
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	dest := t.TempDir()
	dir := linkDirOf(t, dest, srv.URL)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bundle.zip.part"), []byte("junk"), 0o644); err != nil {
		t.Fatal(err)
	}
	src, err := loopback().Fetch(context.Background(), dest, srv.URL+"/bundle.zip", nil)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	got, _ := os.ReadFile(src.Path)
	if string(got) != body {
		t.Fatalf("content = %q, want %q", got, body)
	}
}

// The common failure of a shared link: it is a login page or a forum thread.
// Refused from the headers, before the body is downloaded.
func TestFetchRefusesWhatIsNotAnArchive(t *testing.T) {
	var served bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		served = true
		_, _ = w.Write([]byte("<html>sign in</html>"))
	}))
	defer srv.Close()

	dest := t.TempDir()
	_, err := loopback().Fetch(context.Background(), dest, srv.URL+"/thread/1234", nil)
	if err == nil || !strings.Contains(err.Error(), "archive") {
		t.Fatalf("err = %v, want a not-an-archive refusal", err)
	}
	_ = served
	ents, _ := os.ReadDir(DownloadDir(dest))
	if len(ents) != 0 {
		t.Fatalf("refused download left %d files behind", len(ents))
	}
}

// The address guard, with a secure fetcher: a loopback destination is exactly
// what this server must not be talked into reaching on a caller's behalf.
func TestFetchRefusesLocalAddresses(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("the request should never have been dialled")
	}))
	defer srv.Close()

	// https, and no host restriction: the only thing that can refuse this is
	// the address the name resolves to.
	url := strings.Replace(srv.URL, "127.0.0.1", "localhost", 1) + "/a.zip"
	_, err := Fetcher{}.Fetch(context.Background(), t.TempDir(), url, nil)
	// The message, not merely an error: a self-signed test certificate would
	// also fail, and the point is that the DIAL was refused before any of that.
	if err == nil || !strings.Contains(err.Error(), "local network") {
		t.Fatalf("err = %v, want the address guard's refusal", err)
	}
}

func TestLocalAddresses(t *testing.T) {
	for _, s := range []string{
		"127.0.0.1", "::1", "10.1.2.3", "192.168.0.5", "172.16.9.9",
		"169.254.1.1", "0.0.0.0", "100.64.0.1", "100.127.255.255", "fe80::1",
	} {
		if !IsLocal(parseIP(t, s)) {
			t.Errorf("%s should be treated as local", s)
		}
	}
	for _, s := range []string{"8.8.8.8", "1.1.1.1", "100.63.255.255", "100.128.0.1", "2606:4700::1"} {
		if IsLocal(parseIP(t, s)) {
			t.Errorf("%s should not be treated as local", s)
		}
	}
}

func TestSweepPartials(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "old.zip.part")
	fresh := filepath.Join(dir, "fresh.zip.part")
	keep := filepath.Join(dir, "finished.zip")
	for _, p := range []string{old, fresh, keep} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	stale := zeroTime
	if err := os.Chtimes(old, stale, stale); err != nil {
		t.Fatal(err)
	}
	sweepPartials(dir)
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Error("an abandoned partial survived the sweep")
	}
	for _, p := range []string{fresh, keep} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s was removed: %v", filepath.Base(p), err)
		}
	}
}

func TestPartMetaRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.part.meta")
	writeMeta(p, partMeta{URL: "https://example.com/a.zip", ETag: `"v1"`, Total: 42})
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var m partMeta
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if m.URL != "https://example.com/a.zip" || m.ETag != `"v1"` || m.Total != 42 {
		t.Fatalf("round trip lost fields: %+v", m)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

func parseIP(t *testing.T, s string) net.IP {
	t.Helper()
	ip := net.ParseIP(s)
	if ip == nil {
		t.Fatalf("bad test address %q", s)
	}
	return ip
}

// zeroTime is both "no modification time" for ServeContent and a timestamp far
// enough in the past that the sweep treats a file as abandoned.
var zeroTime = time.Unix(0, 0)

// The whole URL path end to end: fetch, sniff, offer, install. It also pins
// the shape the page and the shell poll - a job that is claimed before the
// download finishes, and a source name that only becomes the file's name once
// there is a file.
func TestBeginURLDownloadsThenSniffs(t *testing.T) {
	zipPath := buildZip(t, member{"Oxford.mdx", "main"}, member{"Oxford.mdd", "media"})
	body, err := os.ReadFile(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Length", itoa(len(body)))
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	dest := t.TempDir()
	m := &Manager{}
	j, err := m.BeginURL(dest, srv.URL+"/bundle.zip", loopback())
	if err != nil {
		t.Fatalf("BeginURL: %v", err)
	}
	if j.State != StateDownloading {
		t.Fatalf("state = %q, want %q", j.State, StateDownloading)
	}
	// Source is the FILE, and at this instant there is not one yet - so it is
	// empty rather than wrong, and Host carries where the bytes are coming
	// from. The bare host and not the link: a URL can carry a session token,
	// which is not a line to put on somebody's screen (D102).
	if j.Source != "" {
		t.Fatalf("source = %q, want empty until the file is named", j.Source)
	}
	if j.Host == "" || strings.Contains(j.Host, "/") {
		t.Fatalf("host = %q, want a bare host", j.Host)
	}
	m.Wait()

	st := m.Status()
	if st.State != StateReady {
		t.Fatalf("state = %q (%s), want %q", st.State, st.Error, StateReady)
	}
	if st.Source != "bundle.zip" {
		t.Fatalf("source = %q, want the downloaded file's name", st.Source)
	}
	// The host outlives the download: the found screen says where this came
	// from, which is half of what a user needs to recognise it.
	if st.Host == "" {
		t.Fatal("host was dropped once the download finished")
	}
	if len(st.Candidates) != 1 || st.Candidates[0].Name != "Oxford" {
		t.Fatalf("candidates = %+v", st.Candidates)
	}

	if _, err := m.Confirm(dest, []int{0}, Options{Keep: true}); err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	m.Wait()
	if st := m.Status(); st.State != StateDone {
		t.Fatalf("state = %q (%s)", st.State, st.Error)
	}
	if _, err := os.Stat(filepath.Join(dest, "Oxford", "Oxford.mdx")); err != nil {
		t.Fatalf("not installed: %v", err)
	}
	// Kept, because keep was true - and kept where the user can find it,
	// which is the reason a download does not live in the staging area.
	if _, err := os.Stat(filepath.Join(linkDirOf(t, dest, srv.URL), "bundle.zip")); err != nil {
		t.Fatalf("the downloaded archive was not kept: %v", err)
	}
}

// A refusal must not claim the job: there would be nothing to poll and nothing
// to cancel, and the next import would be told one is already running.
func TestBeginURLRefusesBeforeClaimingAJob(t *testing.T) {
	m := &Manager{}
	if _, err := m.BeginURL(t.TempDir(), "https://elsewhere.example/a.zip",
		Fetcher{Hosts: []string{"freemdict.com"}}); !errors.Is(err, ErrHostNotAllowed) {
		t.Fatalf("err = %v, want ErrHostNotAllowed", err)
	}
	if st := m.Status(); st.State != "" {
		t.Fatalf("a refused link left a job in state %q", st.State)
	}
	// And with no destination there is nowhere to download to, which is a
	// different answer from "that link is not allowed".
	if _, err := m.BeginURL("", "https://freemdict.com/a.zip", Fetcher{}); !errors.Is(err, ErrNoDestination) {
		t.Fatalf("err = %v, want ErrNoDestination", err)
	}
}

// A link that answers with something else fails the job rather than the
// request, because by then the caller has already been told 202.
func TestBeginURLReportsANonArchiveAsAFailedJob(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html>sign in</html>"))
	}))
	defer srv.Close()

	m := &Manager{}
	if _, err := m.BeginURL(t.TempDir(), srv.URL+"/thread", loopback()); err != nil {
		t.Fatalf("BeginURL: %v", err)
	}
	m.Wait()
	st := m.Status()
	if st.State != StateError || st.Error == "" {
		t.Fatalf("status = %+v, want an error state", st)
	}
}

// The same link twice: the second import must not spend the user's data
// again, and must not leave a second copy of the archive on their disk
// (D134). ServeContent answers the conditional request with 304 by itself,
// which is exactly the server behaviour being relied on here.
func TestFetchReusesAnUnchangedDownload(t *testing.T) {
	body := strings.Repeat("wudict", 5000)
	var hits, conditional int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.Header.Get("If-None-Match") != "" || r.Header.Get("If-Modified-Since") != "" {
			conditional++
		}
		w.Header().Set("ETag", `"v1"`)
		http.ServeContent(w, r, "bundle.zip", zeroTime, strings.NewReader(body))
	}))
	defer srv.Close()

	dest := t.TempDir()
	url := srv.URL + "/bundle.zip"
	first, err := loopback().Fetch(context.Background(), dest, url, nil)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := loopback().Fetch(context.Background(), dest, url, nil)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if second.Path != first.Path {
		t.Fatalf("second fetch landed at %q, want the file already there (%q)", second.Path, first.Path)
	}
	if conditional != 1 {
		t.Errorf("conditional requests = %d, want the second fetch to ask", conditional)
	}
	if hits != 2 {
		t.Errorf("requests = %d, want two (the second answered 304)", hits)
	}
	ents, err := os.ReadDir(linkDirOf(t, dest, srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	// the archive and its .done sidecar, and nothing else
	if len(ents) != 2 {
		t.Errorf("downloads = %v, want the file and its record", dirNames(ents))
	}
	got, err := os.ReadFile(second.Path)
	if err != nil || string(got) != body {
		t.Fatalf("reused file is wrong (err %v, %d bytes)", err, len(got))
	}
}

// A server that offers no validator at all cannot be asked, so the body is
// already arriving when the answer comes. Length is then the evidence - the
// same evidence store.SourceChanged acts on - and the transfer is dropped.
func TestFetchReusesOnSizeWhenThereIsNoValidator(t *testing.T) {
	body := strings.Repeat("q", 4096)
	var served int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		served++
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Length", itoa(len(body)))
		_, _ = io.WriteString(w, body)
	}))
	defer srv.Close()

	dest := t.TempDir()
	url := srv.URL + "/bundle.zip"
	if _, err := loopback().Fetch(context.Background(), dest, url, nil); err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := loopback().Fetch(context.Background(), dest, url, nil)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if filepath.Base(second.Path) != "bundle.zip" {
		t.Fatalf("second fetch produced %q, want the file already there", filepath.Base(second.Path))
	}
	if served != 2 {
		t.Errorf("requests = %d, want two", served)
	}
}

// The other half of the rule: a file that DID change is fetched, and replaces
// the older revision of itself. Never a numbered one beside it: that would be a
// second dictionary called "bundle (2)" instead of an update (D155 Am. 4).
func TestFetchReplacesAChangedDownload(t *testing.T) {
	bodies := []string{strings.Repeat("a", 2048), strings.Repeat("b", 4096)}
	var n int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		i := n
		if i >= len(bodies) {
			i = len(bodies) - 1
		}
		n++
		w.Header().Set("ETag", `"v`+itoa(i)+`"`)
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Length", itoa(len(bodies[i])))
		_, _ = io.WriteString(w, bodies[i])
	}))
	defer srv.Close()

	dest := t.TempDir()
	url := srv.URL + "/bundle.zip"
	first, err := loopback().Fetch(context.Background(), dest, url, nil)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := loopback().Fetch(context.Background(), dest, url, nil)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if second.Path != first.Path {
		t.Fatalf("the changed file landed at %s beside %s instead of replacing it", second.Path, first.Path)
	}
	if got, err := os.ReadFile(second.Path); err != nil || string(got) != bodies[1] {
		t.Fatalf("the download is not the new revision (err %v)", err)
	}
	ents, _ := os.ReadDir(filepath.Dir(second.Path))
	for _, e := range ents {
		if strings.Contains(e.Name(), "(2)") {
			t.Errorf("a numbered file was made: %s", e.Name())
		}
	}
}

// A record whose file the user deleted is a record of nothing, and the sweep
// is what stops it accumulating after every IMPORT_KEEP=delete import.
func TestSweepRemovesOrphanedDownloadRecords(t *testing.T) {
	dir := t.TempDir()
	live := filepath.Join(dir, "a.zip")
	if err := os.WriteFile(live, []byte("zip"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeJSON(filepath.Join(dir, "a.done"), doneMeta{URL: "https://x/a.zip", Size: 3, File: "a.zip"})
	writeJSON(filepath.Join(dir, "b.done"), doneMeta{URL: "https://x/b.zip", Size: 3, File: "b.zip"})

	sweepPartials(dir)

	if _, err := os.Stat(filepath.Join(dir, "a.done")); err != nil {
		t.Errorf("a live record was swept: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "b.done")); !os.IsNotExist(err) {
		t.Errorf("an orphaned record survived: %v", err)
	}
}

// readDone is a trust boundary, not a parser: the sidecar sits in a folder the
// user can write to, so every claim in it is re-checked against the disk.
func TestReadDoneRefusesWhatItCannotVerify(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.zip"), []byte("zip"), 0o644); err != nil {
		t.Fatal(err)
	}
	const raw = "https://x/a.zip"
	cases := []struct {
		name string
		m    doneMeta
	}{
		{"another url", doneMeta{URL: "https://y/a.zip", Size: 3, File: "a.zip"}},
		{"a path, not a name", doneMeta{URL: raw, Size: 3, File: "../a.zip"}},
		{"a file that is not there", doneMeta{URL: raw, Size: 3, File: "gone.zip"}},
		{"the wrong size", doneMeta{URL: raw, Size: 99, File: "a.zip"}},
		{"not an archive", doneMeta{URL: raw, Size: 3, File: "a.txt"}},
	}
	side := filepath.Join(dir, "a.done")
	for _, tc := range cases {
		writeJSON(side, tc.m)
		if m, full := readDone(side, dir, raw); m != nil || full != "" {
			t.Errorf("%s: accepted %+v", tc.name, tc.m)
		}
	}
	writeJSON(side, doneMeta{URL: raw, Size: 3, File: "a.zip"})
	if m, full := readDone(side, dir, raw); m == nil || full != filepath.Join(dir, "a.zip") {
		t.Errorf("a good record was refused: %v %q", m, full)
	}
}

// linkDirOf is where Fetch keeps a download of raw (LinkDir).
func linkDirOf(t *testing.T, dest, raw string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return LinkDir(dest, u)
}

// Each download lives under its own place on the web, so two links with one
// file name never meet; nothing in a URL can climb out or hide.
func TestLinkDir(t *testing.T) {
	dest := t.TempDir()
	for _, tc := range []struct{ in, want string }{
		{"https://A.org/dict/en/oxford.mdx", "a.org/dict/en"},
		{"https://b.org/oxford.mdx", "b.org"},
		{"https://c.org/../../x/./.hidden/y.zip", "c.org/x/hidden"},
		{"https://d.org/a%2Fb/c:d/x.mdx", "d.org/a/b/c_d"},
	} {
		u, _ := url.Parse(tc.in)
		got, err := filepath.Rel(DownloadDir(dest), LinkDir(dest, u))
		if err != nil || filepath.ToSlash(got) != tc.want {
			t.Errorf("LinkDir(%s) = %s, want %s", tc.in, got, tc.want)
		}
	}
}

// Every request the fetcher's client makes names wudict - the transport sets
// it, so no call site has to remember to.
func TestClientUserAgent(t *testing.T) {
	got := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got <- r.Header.Get("User-Agent")
	}))
	defer srv.Close()
	resp, err := Fetcher{Insecure: true}.Client().Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if ua := <-got; ua != "wudict" {
		t.Errorf("User-Agent = %q, want wudict", ua)
	}
}
