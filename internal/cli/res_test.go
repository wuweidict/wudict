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
	"strings"
	"testing"
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
		n, err := writeFileAtomic(dst, bytes.NewReader(want))
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
		if _, err := writeFileAtomic(dst, src); err == nil {
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
		if _, err := writeFileAtomic(dst, strings.NewReader("new")); err != nil {
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
