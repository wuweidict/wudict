// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/wuweidict/wudict/internal/format/wmd"
)

// silence runs f with stdout and stderr discarded.
func silence(t *testing.T, f func() error) error {
	t.Helper()
	saved, savedErr := os.Stdout, os.Stderr
	null, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer null.Close()
	os.Stdout, os.Stderr = null, null
	defer func() { os.Stdout, os.Stderr = saved, savedErr }()
	return f()
}

func example(t *testing.T, mode string) string {
	t.Helper()
	b, err := os.ReadFile("../../docs/wudict-markdown/examples/" + mode + wmd.Ext)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestDumpMarkdownRoundTrip: a wudict markdown source dumps back to itself,
// in the mode it was written in (spec R7.3), through the whole command.
func TestDumpMarkdownRoundTrip(t *testing.T) {
	t.Setenv("WUDICT_DB_DIR", t.TempDir())
	for _, mode := range []string{"clean", "html"} {
		t.Run(mode, func(t *testing.T) {
			src := filepath.Join(t.TempDir(), "Sample"+wmd.Ext)
			text := example(t, mode)
			if err := os.WriteFile(src, []byte(text), 0o644); err != nil {
				t.Fatal(err)
			}
			out := filepath.Join(t.TempDir(), "out")
			if err := silence(t, func() error { return cmdDump([]string{"-format", "md", "-mode", mode, "-o", out, src}) }); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(filepath.Join(out, "Sample"+wmd.Ext))
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != text {
				t.Errorf("got:\n%s\nwant:\n%s", got, text)
			}
			if left, _ := filepath.Glob(filepath.Join(out, "*.tmp")); len(left) != 0 {
				t.Errorf("temporary files left: %v", left)
			}
			if left, _ := filepath.Glob(filepath.Join(out, ".wudict-md-*")); len(left) != 0 {
				t.Errorf("spool files left: %v", left)
			}
		})
	}
}

// TestDumpMarkdownGzip: -compress gz is the plain file gzipped, and the same
// bytes every time (spec R2.4); with no -mode, in the lossless html mode.
func TestDumpMarkdownGzip(t *testing.T) {
	t.Setenv("WUDICT_DB_DIR", t.TempDir())
	src := filepath.Join(t.TempDir(), "Sample"+wmd.Ext)
	if err := os.WriteFile(src, []byte(example(t, "html")), 0o644); err != nil {
		t.Fatal(err)
	}
	var runs [][]byte
	for range 2 {
		out := filepath.Join(t.TempDir(), "out")
		if err := silence(t, func() error { return cmdDump([]string{"-format", "md", "-compress", "gz", "-o", out, src}) }); err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(filepath.Join(out, "Sample"+wmd.Ext+".gz"))
		if err != nil {
			t.Fatal(err)
		}
		runs = append(runs, b)
	}
	if !bytes.Equal(runs[0], runs[1]) {
		t.Error("two runs differ")
	}
	zr, err := gzip.NewReader(bytes.NewReader(runs[0]))
	if err != nil {
		t.Fatal(err)
	}
	plain, _ := io.ReadAll(zr)
	if string(plain) != example(t, "html") {
		t.Errorf("decompressed:\n%s", plain)
	}
	if !zr.ModTime.IsZero() || zr.Name != "" || zr.Comment != "" {
		t.Errorf("gzip header: %+v", zr.Header)
	}
}

// TestDumpMarkdownResources: resources go to <name>.wudict.files (R2.3), and
// the dump reads back with them.
func TestDumpMarkdownResources(t *testing.T) {
	t.Setenv("WUDICT_DB_DIR", t.TempDir())
	dir := t.TempDir()
	src := filepath.Join(dir, "Pics"+wmd.Ext)
	if err := os.WriteFile(src, []byte("# Pics\nwudict: 1\n\n## cat\n\n![a cat](cat.png)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "Pics.wudict.files"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Pics.wudict.files", "cat.png"), []byte("PNG"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "out")
	if err := silence(t, func() error { return cmdDump([]string{"-format", "md", "-mode", "clean", "-o", out, src}) }); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(out, "Pics.wudict.files", "cat.png")); err != nil || string(b) != "PNG" {
		t.Errorf("resource: %q, %v", b, err)
	}
	if b, _ := os.ReadFile(filepath.Join(out, "Pics"+wmd.Ext)); !strings.Contains(string(b), "![a cat](cat.png)") {
		t.Errorf("markdown:\n%s", b)
	}
}

func TestDumpFlags(t *testing.T) {
	for _, args := range [][]string{
		{"-format", "xml", "-o", "x", "d.mdx"},
		{"-mode", "html", "-o", "x", "d.mdx"},
		{"-compress", "gz", "-o", "x", "d.mdx"},
		{"-format", "md", "-mode", "raw", "-o", "x", "d.mdx"},
		{"-format", "md", "-compress", "zip", "-o", "x", "d.mdx"},
	} {
		if err := cmdDump(args); err == nil {
			t.Errorf("%v accepted", args)
		}
	}
}

// TestCleanFailureMessage: R6.8 - what failed, and the exact command that
// keeps the HTML instead.
func TestCleanFailureMessage(t *testing.T) {
	f := &cleanFailure{
		names: []string{"run", "runs"}, index: 1234, body: "<x>",
		err: &wmd.CleanError{Construct: "a heading", Reason: "it would read as the start of an entry"},
		src: "/dicts/My Dict.mdx", out: "out",
	}
	want := `entry "run" (#1234): a heading cannot be written as clean markdown: it would read as the start of an entry
hint: keep the dictionary's HTML instead:
  wudict dump -format md -mode html -o out '/dicts/My Dict.mdx'`
	if got := f.Error(); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	for in, want := range map[string]string{"plain.mdx": "plain.mdx", "it's.mdx": `'it'\''s.mdx'`, "": "''"} {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestDumpResourceScope: -resources all|text|none, for both formats.
func TestDumpResourceScope(t *testing.T) {
	t.Setenv("WUDICT_DB_DIR", t.TempDir())
	dir := t.TempDir()
	src := filepath.Join(dir, "Pics"+wmd.Ext)
	if err := os.WriteFile(src, []byte("# Pics\nwudict: 1\n\n## cat\n\n![a cat](cat.png)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	files := filepath.Join(dir, "Pics.wudict.files")
	if err := os.MkdirAll(files, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{"cat.png": "PNG", "style.css": "p{}"} {
		if err := os.WriteFile(filepath.Join(files, name), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, format := range []string{"csv", "md"} {
		resDir := map[string]string{"csv": "Pics.csv_res", "md": "Pics.wudict.files"}[format]
		for scope, want := range map[string][]string{
			"all":  {"cat.png", "style.css"},
			"text": {"style.css"},
			"none": nil,
		} {
			t.Run(format+"/"+scope, func(t *testing.T) {
				out := filepath.Join(t.TempDir(), "out")
				if err := silence(t, func() error {
					return cmdDump([]string{"-format", format, "-resources", scope, "-o", out, src})
				}); err != nil {
					t.Fatal(err)
				}
				var got []string
				entries, err := os.ReadDir(filepath.Join(out, resDir))
				if err != nil && want != nil {
					t.Fatalf("%s: %v", resDir, err)
				}
				for _, e := range entries {
					got = append(got, e.Name())
				}
				if strings.Join(got, ",") != strings.Join(want, ",") {
					t.Errorf("resources = %v, want %v", got, want)
				}
			})
		}
	}
	if err := cmdDump([]string{"-resources", "some", "-o", "x", src}); err == nil {
		t.Error("-resources some accepted")
	}
}

// TestDumpResourcesOnly: -include, -exclude and -articles=false pick from the
// same resources as -resources, into the folder a full dump uses, so a later
// -resources none dump completes it.
func TestDumpResourcesOnly(t *testing.T) {
	t.Setenv("WUDICT_DB_DIR", t.TempDir())
	dir := t.TempDir()
	src := filepath.Join(dir, "Pics"+wmd.Ext)
	if err := os.WriteFile(src, []byte("# Pics\nwudict: 1\n\n## cat\n\n![a cat](cat.png)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	files := filepath.Join(dir, "Pics.wudict.files")
	for name, data := range map[string]string{"cat.png": "PNG", "style.css": "p{}", "audio/x.ogg": "OGG", "audio/y.wav": "WAV"} {
		p := filepath.Join(files, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tree := func(root string) []string {
		var got []string
		filepath.WalkDir(root, func(p string, e os.DirEntry, err error) error {
			if err == nil && !e.IsDir() {
				rel, _ := filepath.Rel(root, p)
				got = append(got, filepath.ToSlash(rel))
			}
			return nil
		})
		return got
	}
	dump := func(args ...string) error {
		return silence(t, func() error { return cmdDump(args) })
	}

	out := filepath.Join(t.TempDir(), "out")
	if err := dump("-articles=false", "-include", "audio/", "-exclude", "*.wav", "-o", out, src); err != nil {
		t.Fatal(err)
	}
	if got, want := tree(out), []string{"Pics.csv_res/audio/x.ogg"}; !slices.Equal(got, want) {
		t.Fatalf("-articles=false -include audio/ -exclude *.wav wrote %q, want %q", got, want)
	}

	out = filepath.Join(t.TempDir(), "out")
	if err := dump("-format", "md", "-articles=false", "-o", out, src); err != nil {
		t.Fatal(err)
	}
	want := []string{"Pics.wudict.files/audio/x.ogg", "Pics.wudict.files/audio/y.wav", "Pics.wudict.files/cat.png", "Pics.wudict.files/style.css"}
	if got := tree(out); !slices.Equal(got, want) {
		t.Fatalf("-format md -articles=false wrote %q, want %q", got, want)
	}
	if err := dump("-format", "md", "-resources", "none", "-o", out, src); err != nil {
		t.Fatal(err)
	}
	if got, want := tree(out), append(slices.Clone(want), "Pics.wudict.md"); !slices.Equal(got, want) {
		t.Fatalf("a -resources none dump beside it: %q, want %q", got, want)
	}

	// refused before anything is written
	for _, args := range [][]string{
		{"-include", "*.mp3"},
		{"-articles=false", "-resources", "text", "-include", "*.png"},
		{"-articles=false", "-exclude", "*"},
		{"-articles=false", "-format", "md", "-mode", "clean"},
		{"-articles=false", "-resources", "none"},
		{"-resources", "none", "-exclude", "*.wav"},
	} {
		out := filepath.Join(t.TempDir(), "out")
		if err := dump(append(args, "-o", out, src)...); err == nil {
			t.Errorf("%q accepted", args)
		}
		if _, err := os.Stat(out); !os.IsNotExist(err) {
			t.Errorf("%q wrote %s", args, out)
		}
	}
}

// TestDumpIncludeOpenFailure: when -include makes the dump read the
// resources before the articles and they cannot be opened, the error names
// the way to get the articles alone.
func TestDumpIncludeOpenFailure(t *testing.T) {
	src := filepath.Join(t.TempDir(), "Broken.bgl")
	if err := os.WriteFile(src, []byte("not a bgl file"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "out")
	err := silence(t, func() error { return cmdDump([]string{"-include", "*.png", "-o", out, src}) })
	if err == nil || !strings.Contains(err.Error(), "-resources none") {
		t.Fatalf("err = %v, want the -resources none hint", err)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatalf("wrote %s", out)
	}
}

// TestDumpCleanStyles: -mode clean lays articles out as the dictionary's own
// stylesheet does (spec R6.6) - the one its articles link, read from its
// resources - and writes a relative link that names no file as a lookup link
// (R6.9).
func TestDumpCleanStyles(t *testing.T) {
	t.Setenv("WUDICT_DB_DIR", t.TempDir())
	dir := t.TempDir()
	src := filepath.Join(dir, "Sty"+wmd.Ext)
	body := `<div><link rel="stylesheet" href="s.css"><span class="hw">apple</span><span class="x">hidden</span>ap‧ple <a href="cooking apple#f">c</a></div>`
	if err := os.WriteFile(src, []byte("# Sty\nwudict: 1\n\n## apple\n\n"+body+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	files := filepath.Join(dir, "Sty.wudict.files")
	if err := os.MkdirAll(files, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(files, "s.css"), []byte(".hw { display: block } .x { display: none }"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, res := range []string{"all", "none"} { // the stylesheet is read either way
		out := filepath.Join(t.TempDir(), "out")
		if err := silence(t, func() error {
			return cmdDump([]string{"-format", "md", "-mode", "clean", "-resources", res, "-o", out, src})
		}); err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(filepath.Join(out, "Sty"+wmd.Ext))
		if err != nil {
			t.Fatal(err)
		}
		want := "## apple\n\napple\n\nap‧ple [c](<entry://cooking apple#f>)\n"
		if !strings.HasSuffix(string(b), want) {
			t.Errorf("-resources %s:\n%s\nwant it to end:\n%s", res, b, want)
		}
	}
}
