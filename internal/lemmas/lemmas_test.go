// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package lemmas

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// asset is the body of one published language, and its digest.
var asset = []byte("kutya\tkutyák\tkutyát\n")

func digest(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// manifestFor writes a well-formed catalogue naming one asset, so a test only
// has to say how it wants that catalogue to be wrong.
func manifestFor(file string, size int64, sum string) string {
	return fmt.Sprintf(`{"version":1,"generated":"2026-09-01T00:00:00Z","languages":[
	  {"code":"hu","name":"Hungarian","file":%q,"size":%d,"raw_size":20,
	   "lemmas":1,"sha256":%q,"heap_mb":6,
	   "source":"michmech/lemmatization-lists","license":"ODbL-1.0"}]}`,
		file, size, sum)
}

// serve publishes a manifest body and a fixed asset body. body may be shorter
// or longer than the manifest declares, which is how the truncation and
// overrun cases are built.
func serve(t *testing.T, manifest string, body []byte) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/lemmas/manifest.json":
			fmt.Fprint(w, manifest)
		case "/lemmas/hu.tsv.gz":
			w.Write(body)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/lemmas/manifest.json"
}

func TestFetchRejects(t *testing.T) {
	good := digest(asset)
	tests := []struct {
		name     string
		manifest string
		want     string
	}{
		{"not json", `<html>nope</html>`, "not a lemma catalogue"},
		{"future version",
			strings.Replace(manifestFor("hu.tsv.gz", 20, good), `"version":1`, `"version":2`, 1),
			"upgrade wudict"},
		{"unknown language",
			strings.Replace(manifestFor("hu.tsv.gz", 20, good), `"code":"hu"`, `"code":"zz"`, 1),
			"names no language"},
		{"traversal", manifestFor("../../etc/passwd", 20, good), "not a plain file name"},
		{"absolute path", manifestFor("/etc/passwd", 20, good), "not a plain file name"},
		{"dot file", manifestFor(".ssh", 20, good), "not a plain file name"},
		{"unknown extension", manifestFor("hu.exe", 20, good), "not lemma data"},
		{"zero size", manifestFor("hu.tsv.gz", 0, good), "out of range"},
		{"absurd size", manifestFor("hu.tsv.gz", 1<<30, good), "out of range"},
		{"oversize raw",
			strings.Replace(manifestFor("hu.tsv.gz", 20, good), `"raw_size":20`, `"raw_size":1073741824`, 1),
			"exceeds the 64 MB limit"},
		{"bad digest", manifestFor("hu.tsv.gz", 20, "not-a-digest"), "not a sha256 digest"},
		{"duplicate language",
			strings.Replace(manifestFor("hu.tsv.gz", 20, good), `"license":"ODbL-1.0"}]}`,
				`"license":"ODbL-1.0"},{"code":"hun","file":"hu.tsv","size":9,"sha256":"`+good+`"}]}`, 1),
			"listed twice"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Fetch(context.Background(), serve(t, tt.manifest, asset))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Fetch: got %v, want error containing %q", err, tt.want)
			}
		})
	}
}

func TestFetchMissing(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(srv.Close)
	if _, err := Fetch(context.Background(), srv.URL+"/nope.json"); err == nil {
		t.Fatal("Fetch of a 404 must fail")
	}
	if _, err := Fetch(context.Background(), ""); err == nil {
		t.Fatal("Fetch of an empty source must fail")
	}
}

func TestInstall(t *testing.T) {
	url := serve(t, manifestFor("hu.tsv.gz", int64(len(asset)), digest(asset)), asset)
	cat, err := Fetch(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	e, ok := cat.Find("hu")
	if !ok {
		t.Fatal("hu missing from the catalogue")
	}
	if e.Name != "Hungarian" || e.LocalName() != "hu.tsv.gz" {
		t.Fatalf("entry = %+v", e)
	}

	dir := filepath.Join(t.TempDir(), "lemmas") // not created: Install must
	var last int64
	path, err := cat.Install(context.Background(), dir, e, func(n int64) { last = n })
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(dir, "hu.tsv.gz") {
		t.Fatalf("wrote %s", path)
	}
	if last != int64(len(asset)) {
		t.Fatalf("progress ended at %d, want %d", last, len(asset))
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(asset) {
		t.Fatalf("content = %q", got)
	}
	if sum, err := Hash(path); err != nil || sum != e.SHA256 {
		t.Fatalf("Hash = %q, %v", sum, err)
	}
	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o644 {
		t.Fatalf("mode = %v, %v", fi.Mode(), err)
	}
	// A second install over the top must succeed, not trip over the first.
	if _, err := cat.Install(context.Background(), dir, e, nil); err != nil {
		t.Fatal(err)
	}
	assertNoLeftovers(t, dir)
}

func TestInstallRejects(t *testing.T) {
	n := int64(len(asset))
	tests := []struct {
		name string
		size int64
		sum  string
		body []byte
		want string
	}{
		{"wrong digest", n, digest([]byte("other")), asset, "checksum mismatch"},
		{"truncated", n, digest(asset), asset[:5], "got 5"},
		{"longer than declared", n, digest(asset), append(append([]byte{}, asset...), 'x'), "expected"},
		{"missing asset", n, digest(asset), nil, "got 0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cat, err := Fetch(context.Background(),
				serve(t, manifestFor("hu.tsv.gz", tt.size, tt.sum), tt.body))
			if err != nil {
				t.Fatal(err)
			}
			e, _ := cat.Find("hu")
			dir := t.TempDir()
			if _, err := cat.Install(context.Background(), dir, e, nil); err == nil ||
				!strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Install: got %v, want error containing %q", err, tt.want)
			}
			if _, err := os.Stat(filepath.Join(dir, "hu.tsv.gz")); !os.IsNotExist(err) {
				t.Fatal("a rejected download must leave no lemma file")
			}
			assertNoLeftovers(t, dir)
		})
	}
}

// assertNoLeftovers fails if anything remains that morph would skip but a user
// would find - the temporary download file.
func assertNoLeftovers(t *testing.T, dir string) {
	t.Helper()
	parts, err := filepath.Glob(filepath.Join(dir, ".wudict-lemma-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) > 0 {
		t.Fatalf("left behind: %v", parts)
	}
}

// A published folder copied onto a stick works exactly like the URL, because
// an air-gapped install is the reason the local path is accepted at all.
func TestLocalSource(t *testing.T) {
	pub := t.TempDir()
	if err := os.WriteFile(filepath.Join(pub, "hu.tsv.gz"), asset, 0o644); err != nil {
		t.Fatal(err)
	}
	man := filepath.Join(pub, "manifest.json")
	body := manifestFor("hu.tsv.gz", int64(len(asset)), digest(asset))
	if err := os.WriteFile(man, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, src := range []string{man, "file://" + man} {
		cat, err := Fetch(context.Background(), src)
		if err != nil {
			t.Fatalf("%s: %v", src, err)
		}
		e, _ := cat.Find("hu")
		dir := t.TempDir()
		if _, err := cat.Install(context.Background(), dir, e, nil); err != nil {
			t.Fatalf("%s: %v", src, err)
		}
	}
}

func TestInstalledShadowing(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"ru.tsv", "ru.tsv.gz", "polish.txt", "notes.md"} {
		if err := os.WriteFile(filepath.Join(dir, n), asset, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	local, shadowed := Installed(dir)
	if len(local) != 2 || local["ru"].Path != filepath.Join(dir, "ru.tsv") ||
		local["pl"].Path != filepath.Join(dir, "polish.txt") {
		t.Fatalf("Installed = %+v", local)
	}
	if len(shadowed) != 1 || !strings.Contains(shadowed[0], "ru.tsv.gz") {
		t.Fatalf("shadowed = %v", shadowed)
	}
	// Reporting a shadowed file must never be a licence to delete it.
	if _, err := os.Stat(filepath.Join(dir, "ru.tsv.gz")); err != nil {
		t.Fatal("the shadowed file was removed")
	}
	if local["ru"].Size != int64(len(asset)) {
		t.Fatalf("size = %d", local["ru"].Size)
	}
}

// Remove takes every file supplying the language, including the shadowed one:
// after it, the language has to actually be gone.
func TestRemove(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"ru.tsv", "ru.tsv.gz", "russian.txt", "en.tsv"} {
		if err := os.WriteFile(filepath.Join(dir, n), asset, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gone, err := Remove(dir, "ru")
	if err != nil {
		t.Fatal(err)
	}
	if len(gone) != 3 {
		t.Fatalf("removed %v", gone)
	}
	if local, _ := Installed(dir); len(local) != 1 || local["en"].Code != "en" {
		t.Fatalf("left %+v", local)
	}
	if gone, err := Remove(dir, "ru"); err != nil || gone != nil {
		t.Fatalf("removing nothing = %v, %v", gone, err)
	}
	if gone, err := Remove(filepath.Join(dir, "absent"), "ru"); err != nil || gone != nil {
		t.Fatalf("removing from a missing folder = %v, %v", gone, err)
	}
}
