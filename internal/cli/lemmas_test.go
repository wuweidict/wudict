// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

var lemmaAsset = []byte("kutya\tkutyák\n")

// catalogue serves a one-language manifest and counts every request, so a test
// can assert that a command did NOT reach for the network.
func catalogue(t *testing.T, hits *int32) string {
	t.Helper()
	sum := sha256.Sum256(lemmaAsset)
	body := fmt.Sprintf(`{"version":1,"languages":[{"code":"hu","name":"Hungarian",
	  "file":"hu.tsv.gz","size":%d,"raw_size":13,"lemmas":1,"sha256":%q,"heap_mb":6}]}`,
		len(lemmaAsset), hex.EncodeToString(sum[:]))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(hits, 1)
		switch r.URL.Path {
		case "/manifest.json":
			fmt.Fprint(w, body)
		case "/hu.tsv.gz":
			w.Write(lemmaAsset)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/manifest.json"
}

// One unrecognised argument stops the command before it opens a socket, so a
// typo cannot leave three of four languages installed.
func TestLemmasDownloadValidatesBeforeFetching(t *testing.T) {
	var hits int32
	url := catalogue(t, &hits)
	dir := t.TempDir()

	err := cmdLemmasDownload([]string{"-dir", dir, "-url", url, "hu", "zz"})
	if err == nil || !strings.Contains(err.Error(), "not a language: zz") {
		t.Fatalf("got %v, want a rejection naming zz", err)
	}
	if n := atomic.LoadInt32(&hits); n != 0 {
		t.Fatalf("%d requests made before validating the arguments", n)
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 0 {
		t.Fatalf("wrote %d files despite failing", len(ents))
	}

	// No argument at all is a usage error, never "install everything".
	if err := cmdLemmasDownload([]string{"-dir", dir, "-url", url}); err == nil ||
		!strings.Contains(err.Error(), "-all") {
		t.Fatalf("got %v, want a usage error mentioning -all", err)
	}
	if n := atomic.LoadInt32(&hits); n != 0 {
		t.Fatalf("%d requests made for an empty download", n)
	}
}

// A language the catalogue does not carry fails naming what it does carry, and
// installs nothing at all - not even the languages that were available.
func TestLemmasDownloadUnavailable(t *testing.T) {
	var hits int32
	url := catalogue(t, &hits)
	dir := t.TempDir()

	err := cmdLemmasDownload([]string{"-dir", dir, "-url", url, "hu", "polish"})
	if err == nil || !strings.Contains(err.Error(), "pl") {
		t.Fatalf("got %v, want a failure naming pl", err)
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 0 {
		t.Fatalf("wrote %d files despite failing", len(ents))
	}
}

func TestLemmasDownloadInstallsAndRemoves(t *testing.T) {
	var hits int32
	url := catalogue(t, &hits)
	dir := filepath.Join(t.TempDir(), "lemmas")

	// By name, not code: someone who does not program should not have to know
	// that Hungarian is "hu".
	if err := cmdLemmasDownload([]string{"-dir", dir, "-url", url, "hungarian"}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "hu.tsv.gz"))
	if err != nil || string(got) != string(lemmaAsset) {
		t.Fatalf("installed file = %q, %v", got, err)
	}

	// Installing again must not re-download: the local hash already matches.
	before := atomic.LoadInt32(&hits)
	if err := cmdLemmasDownload([]string{"-dir", dir, "-url", url, "hu"}); err != nil {
		t.Fatal(err)
	}
	if n := atomic.LoadInt32(&hits) - before; n != 1 {
		t.Fatalf("%d requests for an up-to-date language, want 1 (the manifest)", n)
	}
	// -f overrides that.
	before = atomic.LoadInt32(&hits)
	if err := cmdLemmasDownload([]string{"-dir", dir, "-url", url, "-f", "hu"}); err != nil {
		t.Fatal(err)
	}
	if n := atomic.LoadInt32(&hits) - before; n != 2 {
		t.Fatalf("-f made %d requests, want 2 (manifest + asset)", n)
	}

	if err := cmdLemmasList([]string{"-dir", dir, "-url", url}); err != nil {
		t.Fatal(err)
	}
	if err := cmdLemmasRemove([]string{"-dir", dir, "hu"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "hu.tsv.gz")); !os.IsNotExist(err) {
		t.Fatal("remove left the file behind")
	}
	// Removing what is not there is not an error - it is the state asked for.
	if err := cmdLemmasRemove([]string{"-dir", dir, "hu"}); err != nil {
		t.Fatal(err)
	}
	if err := cmdLemmasRemove([]string{"-dir", dir, "zz"}); err == nil {
		t.Fatal("remove must reject a non-language")
	}
}

// list is the command a user runs to find out what is going on, so it has to
// work when the catalogue does not: installed languages are the half of the
// answer that needs no network.
func TestLemmasListSurvivesNoCatalogue(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "hu.tsv"), lemmaAsset, 0o644); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL + "/manifest.json"
	srv.Close() // nothing is listening now

	if err := cmdLemmasList([]string{"-dir", dir, "-url", url}); err != nil {
		t.Fatalf("list must not fail without a catalogue: %v", err)
	}
	if err := cmdLemmasList([]string{"-dir", filepath.Join(dir, "absent"), "-url", url}); err != nil {
		t.Fatalf("list of an empty folder: %v", err)
	}
}

func TestLemmasUnknownSubcommand(t *testing.T) {
	if err := cmdLemmas([]string{"frobnicate"}); err == nil {
		t.Fatal("unknown subcommand must fail")
	}
	for _, a := range [][]string{nil, {"help"}} {
		if err := cmdLemmas(a); err != nil {
			t.Fatalf("cmdLemmas(%v) = %v", a, err)
		}
	}
}
