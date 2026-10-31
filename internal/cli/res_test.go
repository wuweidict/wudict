// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/wuweidict/wudict/internal/dict"
	"github.com/wuweidict/wudict/internal/fsx"
	"github.com/wuweidict/wudict/internal/resource"
)

// The names here come from real containers and from what a hostile one could
// hold: MDX stores `\audio\x.spx`, and nothing validates what a .mdd author
// puts in a key. Anything that could escape the current directory must reduce
// to a plain file name or to "" - never to a path with a separator in it.
func TestResBasename(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "word.mp3", "word.mp3"},
		{"posix path", "audio/word.mp3", "word.mp3"},
		{"mdx backslash", `\audio\word.spx`, "word.spx"},
		{"mixed separators", `audio\sub/word.png`, "word.png"},
		{"leading slash", "/etc/passwd", "passwd"},
		{"traversal", "../../../../etc/passwd", "passwd"},
		{"traversal backslash", `..\..\windows\system32\x.dll`, "x.dll"},
		{"bare dotdot", "..", ""},
		{"bare dot", ".", ""},
		{"dotdot with separators", "../..", ""},
		{"trailing separator", "audio/", ""},
		{"root", "/", ""},
		{"empty", "", ""},
		{"drive relative", `c:x.png`, "x.png"},
		{"drive absolute", `C:\Windows\x.png`, "x.png"},
		{"unc", `\\server\share\x.png`, "x.png"},
		{"nul byte", "word\x00.mp3", ""},
		{"newline", "word\n.mp3", ""},
		{"escape sequence", "\x1b[2Jword.mp3", ""},
		{"spaces trimmed", "  word.mp3  ", "word.mp3"},
		{"whitespace only", "   ", ""},
		{"no extension", "audio/word", "word"},
		{"dotfile", "audio/.hidden", ".hidden"},
		{"unicode", "audio/κοράκι.mp3", "κοράκι.mp3"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resBasename(tt.in)
			if got != tt.want {
				t.Fatalf("resBasename(%q) = %q, want %q", tt.in, got, tt.want)
			}
			// The invariant the security of this rests on: whatever comes back
			// is a single element that filepath.Join cannot walk out of.
			if got != "" {
				if strings.ContainsAny(got, `/\`) {
					t.Fatalf("resBasename(%q) = %q still holds a separator", tt.in, got)
				}
				if filepath.Base(got) != got {
					t.Fatalf("resBasename(%q) = %q is not a bare base name", tt.in, got)
				}
			}
		})
	}
}

// statFunc fakes os.Stat for the "-o names a directory" branch, so the
// decision is tested without a filesystem.
func statFunc(dirs ...string) func(string) (os.FileInfo, error) {
	set := map[string]bool{}
	for _, d := range dirs {
		set[d] = true
	}
	return func(p string) (os.FileInfo, error) {
		if set[p] {
			return fakeDir{}, nil
		}
		return nil, errors.New("not found")
	}
}

type fakeDir struct{ os.FileInfo }

func (fakeDir) IsDir() bool { return true }

func TestResDest(t *testing.T) {
	// A pipe is the non-terminal case; the process's own stdout may be either
	// when the tests run, so neither is assumed.
	pipeR, pipeW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer pipeR.Close()
	defer pipeW.Close()

	tests := []struct {
		name        string
		out         string
		res         string
		stdout      *os.File
		wantDest    string
		wantDerived bool
		wantErr     bool
	}{
		// "-" is an instruction and outranks the terminal check.
		{name: "dash is stdout on a pipe", out: "-", res: "x.png", stdout: pipeW},
		// Explicit paths are taken verbatim, parents and all.
		{name: "explicit path", out: "a/b/c.png", res: "x.png", stdout: pipeW, wantDest: "a/b/c.png"},
		{name: "explicit path beats terminal", out: "out.png", res: "x.png", stdout: nil, wantDest: "out.png"},
		// A directory means "in there", under the SAFE name, and is still not
		// "derived" - the user chose the location.
		{name: "directory target", out: "/tmp/dir", res: `\audio\x.png`, stdout: pipeW, wantDest: filepath.Join("/tmp/dir", "x.png")},
		{name: "directory target unusable name", out: "/tmp/dir", res: "..", stdout: pipeW, wantErr: true},
		// No -o: a pipe keeps today's behaviour exactly.
		{name: "pipe stays stdout", out: "", res: "x.png", stdout: pipeW},
		// No -o on a terminal: derive, and say so.
		{name: "terminal derives", out: "", res: `\audio\x.png`, stdout: nil, wantDest: "x.png", wantDerived: true},
		{name: "terminal unusable name", out: "", res: "../", stdout: nil, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdout := tt.stdout
			if stdout == nil { // stand in for a terminal
				stdout = termStub(t)
			}
			dest, derived, err := resDest(tt.out, tt.res, stdout, statFunc("/tmp/dir"))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("resDest(%q, %q) = %q, want an error", tt.out, tt.res, dest)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if dest != tt.wantDest || derived != tt.wantDerived {
				t.Fatalf("resDest(%q, %q) = (%q, %v), want (%q, %v)",
					tt.out, tt.res, dest, derived, tt.wantDest, tt.wantDerived)
			}
		})
	}
}

// termStub returns a *os.File that is a character device, which is what
// isTerminal actually tests. /dev/null is one on every unix; on a platform
// where it cannot be opened the terminal cases are skipped rather than
// silently asserted against a regular file.
func termStub(t *testing.T) *os.File {
	t.Helper()
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Skipf("no %s to stand in for a terminal: %v", os.DevNull, err)
	}
	t.Cleanup(func() { f.Close() })
	if !isTerminal(f) {
		t.Skipf("%s is not a character device on this platform", os.DevNull)
	}
	return f
}

func TestWriteFileAtomic(t *testing.T) {
	dir := t.TempDir()

	t.Run("creates parents and content", func(t *testing.T) {
		dst := filepath.Join(dir, "a", "b", "c.bin")
		want := bytes.Repeat([]byte{0xDE, 0xAD}, 4096)
		n, err := fsx.WriteAtomicFrom(dst, bytes.NewReader(want), 0o644)
		if err != nil {
			t.Fatal(err)
		}
		if n != int64(len(want)) {
			t.Fatalf("wrote %d bytes, want %d", n, len(want))
		}
		got, err := os.ReadFile(dst)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Fatal("content differs")
		}
	})

	t.Run("a failed copy leaves nothing behind", func(t *testing.T) {
		dst := filepath.Join(dir, "partial.bin")
		src := io.MultiReader(bytes.NewReader(bytes.Repeat([]byte{1}, 1024)), errReader{})
		if _, err := fsx.WriteAtomicFrom(dst, src, 0o644); err == nil {
			t.Fatal("expected the read error to surface")
		}
		if _, err := os.Stat(dst); !os.IsNotExist(err) {
			t.Fatal("a truncated file was left at the destination")
		}
		// And no temp file either: a directory littered with .part files
		// would be its own bug report.
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			if strings.Contains(e.Name(), ".part") {
				t.Fatalf("left a temp file behind: %s", e.Name())
			}
		}
	})

	t.Run("replaces an existing file", func(t *testing.T) {
		dst := filepath.Join(dir, "existing.bin")
		if err := os.WriteFile(dst, []byte("old and longer"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := fsx.WriteAtomicFrom(dst, strings.NewReader("new"), 0o644); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(dst)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != "new" {
			t.Fatalf("got %q, want %q", got, "new")
		}
	})
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("decompression failed") }

// mapRes is a dictionary's resources in memory, looked up as a container
// looks them up: by resource.Key.
type mapRes map[string]string

func (m mapRes) Resource(name string) (io.ReadCloser, string, error) {
	for n, body := range m {
		if resource.Key(n) == resource.Key(name) {
			return io.NopCloser(strings.NewReader(body)), "", nil
		}
	}
	return nil, "", dict.ErrNotFound
}

func (m mapRes) open() *resSource {
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	slices.Sort(names)
	return &resSource{ResourceOpener: m, list: func() []string { return names }, close: func() error { return nil }}
}

var testRes = mapRes{
	"audio/US/ab/1.ogg": "us-ab",
	"audio/US/ac/1.ogg": "us-ac",
	"audio/UK/1.ogg":    "uk",
	"img/A.png":         "png",
	"what?.html":        "literal",
	"whatX.html":        "other",
	"audio/.DS_Store":   "junk",
}

func TestSelectRes(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		want    []string
		wantErr string
	}{
		{name: "any depth", args: []string{"*.ogg"}, want: []string{"audio/UK/1.ogg", "audio/US/ab/1.ogg", "audio/US/ac/1.ogg"}},
		{name: "folder", args: []string{"audio/US/"}, want: []string{"audio/US/ab/1.ogg", "audio/US/ac/1.ogg"}},
		{name: "folder, any case", args: []string{"AUDIO/us/"}, want: []string{"audio/US/ab/1.ogg", "audio/US/ac/1.ogg"}},
		{name: "exact name wins over its wildcard", args: []string{"what?.html"}, want: []string{"what?.html"}},
		{name: "one resource once", args: []string{"audio/UK/1.ogg", "*.ogg"}, want: []string{"audio/UK/1.ogg", "audio/US/ab/1.ogg", "audio/US/ac/1.ogg"}},
		{name: "literal keeps its spelling", args: []string{"IMG/a.PNG", "img/*"}, want: []string{"IMG/a.PNG"}},
		{name: "everything but junk", args: []string{"**"}, want: []string{"audio/UK/1.ogg", "audio/US/ab/1.ogg", "audio/US/ac/1.ogg", "img/A.png", "what?.html", "whatX.html"}},
		{name: "no match", args: []string{"*.ogg", "*.wav"}, wantErr: "matches no resource"},
		{name: "folder without slash", args: []string{"audio/US"}, wantErr: `"audio/US/" extracts its 2 resources`},
		{name: "missing name", args: []string{"img/b.png"}, wantErr: `no resource "img/b.png"`},
		{name: "flag after the dictionary", args: []string{"img/A.png", "-o"}, wantErr: "flags go before the dictionary file"},
	}
	d := testRes.open()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := selectRes(d, tt.args)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("selectRes(%q) error = %v, want one containing %q", tt.args, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(got, tt.want) {
				t.Fatalf("selectRes(%q) = %q, want %q", tt.args, got, tt.want)
			}
		})
	}
}

// Every destination stays below the output folder, and what cannot be written
// as asked is a problem, not a second write to the same file.
func TestPlanRes(t *testing.T) {
	out := filepath.Join("x", "out")
	tests := []struct {
		name     string
		names    []string
		want     []string // destinations below out, slash-separated
		problems int
	}{
		{name: "tree kept", names: []string{"audio/US/ab/1.ogg", `\audio\x.spx`}, want: []string{"audio/US/ab/1.ogg", "audio/x.spx"}},
		{name: "climbing removed", names: []string{"../../etc/passwd", "/abs/x.png", `C:\w\x.png`}, want: []string{"etc/passwd", "abs/x.png", "C-/w/x.png"}},
		{name: "no usable name", names: []string{"..", "/", "ok.png"}, want: []string{"ok.png"}, problems: 2},
		{name: "same file after sanitizing", names: []string{"a:b.png", "a-b.png"}, want: []string{"a-b.png"}, problems: 1},
		{name: "same file but for case", names: []string{"Img/A.png", "img/a.png"}, want: []string{"Img/A.png"}, problems: 1},
		{name: "file named like a folder", names: []string{"audio", "audio/x.ogg"}, want: []string{"audio/x.ogg"}, problems: 1},
		{name: "file named like a nested folder", names: []string{"a/b", "a/b/c/d.ogg"}, want: []string{"a/b/c/d.ogg"}, problems: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			jobs, problems := planRes(tt.names, out)
			var got []string
			for _, j := range jobs {
				rel, err := filepath.Rel(out, j.dest)
				if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
					t.Fatalf("%q is written to %q, outside %q", j.name, j.dest, out)
				}
				got = append(got, filepath.ToSlash(rel))
			}
			if !slices.Equal(got, tt.want) || len(problems) != tt.problems {
				t.Fatalf("planRes(%q) = %q with problems %q, want %q with %d", tt.names, got, problems, tt.want, tt.problems)
			}
		})
	}
}

func TestResMany(t *testing.T) {
	out := t.TempDir()
	d := testRes.open()
	read := func(rel string) string {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(out, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}

	// a dry run writes nothing and prints each name with its file
	stdout := os.Stdout
	f, err := os.Create(filepath.Join(t.TempDir(), "stdout"))
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = f
	err = resMany(d, []string{"audio/US/"}, out, false, true)
	os.Stdout = stdout
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	printed, _ := os.ReadFile(f.Name())
	wantLine := "audio/US/ab/1.ogg\t" + filepath.Join(out, "audio", "US", "ab", "1.ogg") + "\n"
	if !strings.HasPrefix(string(printed), wantLine) || strings.Count(string(printed), "\n") != 2 {
		t.Fatalf("dry run printed %q, want 2 lines starting %q", printed, wantLine)
	}
	if entries, _ := os.ReadDir(out); len(entries) != 0 {
		t.Fatalf("a dry run wrote %d entries", len(entries))
	}

	if err := resMany(d, []string{"audio/US/"}, out, false, false); err != nil {
		t.Fatal(err)
	}
	if read("audio/US/ab/1.ogg") != "us-ab" || read("audio/US/ac/1.ogg") != "us-ac" {
		t.Fatal("extracted files differ from the resources")
	}

	// a second run would overwrite: refused before anything is written
	if err := os.WriteFile(filepath.Join(out, "audio", "US", "ab", "1.ogg"), []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	err = resMany(d, []string{"*.ogg"}, out, false, false)
	if err == nil || !strings.Contains(err.Error(), "2 files would be overwritten") {
		t.Fatalf("second run: err = %v, want the overwrite refusal", err)
	}
	if read("audio/US/ab/1.ogg") != "mine" {
		t.Fatal("the refused run replaced a file")
	}
	if _, err := os.Stat(filepath.Join(out, "audio", "UK", "1.ogg")); !os.IsNotExist(err) {
		t.Fatal("the refused run wrote a new file")
	}

	if err := resMany(d, []string{"*.ogg"}, out, true, false); err != nil {
		t.Fatal(err)
	}
	if read("audio/US/ab/1.ogg") != "us-ab" || read("audio/UK/1.ogg") != "uk" {
		t.Fatal("-f did not overwrite")
	}

	if err := resMany(d, []string{"*.ogg"}, "-", true, false); err == nil {
		t.Fatal("-o - with a pattern: want an error")
	}
	if err := resMany(d, []string{"*.ogg"}, filepath.Join(out, "audio", "UK", "1.ogg"), true, false); err == nil {
		t.Fatal("-o naming a file: want an error")
	}
}
