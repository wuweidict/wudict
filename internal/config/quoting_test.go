// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const winPath = `C:\Users\name\Downloads\mdict`

// The defect: DICT_DIR was written escaped ("C:\\Users\\…") and read back with
// the escaping still in it, so the setup page's input showed a doubled
// backslash for every separator — and on a platform where '\' is not a path
// separator, the doubled path did not exist at all.
func TestWindowsPathRoundTrip(t *testing.T) {
	for _, dirs := range [][]string{
		{winPath},
		{winPath, `D:\Dicts`},
		{`C:\temp\notes`, `C:\new\backup`}, // \t, \n, \b: valid TOML escapes
		{`\\server\share\dicts`},           // UNC: a leading pair that must not collapse
		{"/unix/plain", `/unix/odd\folder`},
		{`C:\Users\Иван\Словари`, `C:\Users\name\辞書`},
	} {
		p := filepath.Join(t.TempDir(), Name)
		if err := os.WriteFile(p, []byte("SERVER_PORT = \"1\"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := SaveKeyRaw(p, "DICT_DIR", FormatList(dirs)); err != nil {
			t.Fatal(err)
		}
		cfg, err := Load(p, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(cfg.DictDirs) != len(dirs) {
			t.Fatalf("%q → %q", dirs, cfg.DictDirs)
		}
		for i := range dirs {
			if cfg.DictDirs[i] != dirs[i] {
				t.Errorf("%q → %q (element %d)", dirs, cfg.DictDirs, i)
			}
		}
		if cfg.Port != "1" {
			t.Errorf("neighbouring key disturbed: %q", cfg.Port)
		}
		// saving twice must be idempotent, not compound the escaping
		if err := SaveKeyRaw(p, "DICT_DIR", FormatList(cfg.DictDirs)); err != nil {
			t.Fatal(err)
		}
		again, err := Load(p, nil)
		if err != nil {
			t.Fatal(err)
		}
		for i := range dirs {
			if again.DictDirs[i] != dirs[i] {
				t.Errorf("second save drifted: %q → %q", dirs, again.DictDirs)
			}
		}
	}
}

// A file written by hand, in every spelling someone might reasonably use.
func TestReadHandWrittenValues(t *testing.T) {
	cases := []struct{ name, line, want string }{
		{"literal string", `DB_DIR = 'C:\Users\me\db'`, `C:\Users\me\db`},
		{"basic, escaped", `DB_DIR = "C:\\Users\\me\\db"`, `C:\Users\me\db`},
		{"basic, typed raw", `DB_DIR = "C:\Users\me\db"`, `C:\Users\me\db`},
		{"basic, raw with \\t", `DB_DIR = "C:\temp\db"`, `C:\temp\db`},
		{"bare", `DB_DIR = /var/db`, `/var/db`},
		{"posix quoted", `DB_DIR = "/var/db"`, `/var/db`},
		{"trailing comment", `DB_DIR = "/var/db"   # where it goes`, `/var/db`},
		{"hash inside the value", `DB_DIR = "/var/vol #2/db"`, `/var/vol #2/db`},
		{"escaped quote", `DB_DIR = "/var/say \"hi\""`, `/var/say "hi"`},
		{"UNC", `DB_DIR = '\\srv\share\db'`, `\\srv\share\db`},
		{"crlf", "DB_DIR = '/var/db'\r", `/var/db`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			vals, _ := parseTOML(c.line + "\n")
			if got := vals["DB_DIR"]; got != c.want {
				t.Errorf("parseTOML(%s) = %q, want %q", c.line, got, c.want)
			}
		})
	}
}

// Arrays: quoting, commas inside a value, and a list spread over several lines.
func TestArrayForms(t *testing.T) {
	cases := []struct {
		name, text string
		want       []string
	}{
		{"single line", `DICT_DIR = ["/a", "/b"]`, []string{"/a", "/b"}},
		{"literal strings", `DICT_DIR = ['C:\a', 'C:\b']`, []string{`C:\a`, `C:\b`}},
		{"escaped basics", `DICT_DIR = ["C:\\a", "C:\\b"]`, []string{`C:\a`, `C:\b`}},
		{"comma in a value", `DICT_DIR = ["/a, b", "/c"]`, []string{"/a, b", "/c"}},
		{"bracket in a value", `DICT_DIR = ["/a[1]", "/b"]`, []string{"/a[1]", "/b"}},
		{"trailing comma", `DICT_DIR = ["/a", "/b",]`, []string{"/a", "/b"}},
		{"comment after", `DICT_DIR = ["/a"]  # two`, []string{"/a"}},
		{"multi line", "DICT_DIR = [\n  \"/a\",\n  \"/b\",\n]\n", []string{"/a", "/b"}},
		{"multi line, comments", "DICT_DIR = [   # folders\n  \"/a\",  # first\n  \"/b\",\n]\n", []string{"/a", "/b"}},
		{"empty", `DICT_DIR = []`, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, lists := parseTOML(c.text + "\n")
			got := lists["DICT_DIR"]
			if len(got) != len(c.want) {
				t.Fatalf("parseTOML(%q) = %q, want %q", c.text, got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("parseTOML(%q) = %q, want %q", c.text, got, c.want)
				}
			}
		})
	}
}

// A raw path arriving from a flag or the environment is NOT TOML and must not
// be decoded: nothing quoted it, so every backslash in it is data.
func TestFlagAndEnvPathsAreNotDecoded(t *testing.T) {
	// A backslash the environment supplies is data, not an escape. The path
	// carries no os.PathListSeparator, because in THIS layer that character
	// does separate folders — on Windows ';', which a path cannot contain.
	const envPath = `C:\Users\name\Downloads\mdict`
	p := filepath.Join(t.TempDir(), Name)
	if err := os.WriteFile(p, []byte("SERVER_PORT = \"1\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		t.Setenv("DICT_DIR", envPath)
		cfg, err := Load(p, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(cfg.DictDirs) != 1 || cfg.DictDirs[0] != envPath {
			t.Errorf("env path mangled: %q", cfg.DictDirs)
		}
		if cfg.Origin("DICT_DIR") != OriginEnv {
			t.Errorf("origin: %q", cfg.Origin("DICT_DIR"))
		}
	}
	// a flag outranks the file, and is equally raw
	flagPath := `/from/flag/odd\folder`
	if runtime.GOOS == "windows" {
		flagPath = `D:\from\flag`
	}
	cfg, err := Load(p, map[string]string{"DICT_DIR": flagPath})
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.DictDirs) != 1 || cfg.DictDirs[0] != flagPath {
		t.Errorf("flag path mangled: %q", cfg.DictDirs)
	}
	if cfg.Origin("DICT_DIR") != OriginFlag {
		t.Errorf("origin: %q", cfg.Origin("DICT_DIR"))
	}
}

// A drive letter is followed by the character that separates folders in the
// environment. The FILE layer must not split on it — a single folder there is
// one folder, whatever it contains, on every host.
func TestFileScalarIsOneFolder(t *testing.T) {
	for _, dir := range []string{winPath, `\\srv\share\dicts`, "/mnt/vol:1/dicts"} {
		p := filepath.Join(t.TempDir(), Name)
		if err := os.WriteFile(p, []byte("SERVER_PORT = \"1\"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := SaveKey(p, "DICT_DIR", dir); err != nil {
			t.Fatal(err)
		}
		cfg, err := Load(p, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(cfg.DictDirs) != 1 || cfg.DictDirs[0] != dir {
			t.Errorf("%q → %q", dir, cfg.DictDirs)
		}
		if cfg.Origin("DICT_DIR") != OriginFile {
			t.Errorf("origin: %q", cfg.Origin("DICT_DIR"))
		}
	}
}

// A Windows path goes in as a literal string, so the file stays readable and
// nothing has to be unescaped on the way back out.
func TestQuoteTOML(t *testing.T) {
	cases := []struct{ in, want string }{
		{winPath, `'` + winPath + `'`},
		{`\\srv\share`, `'\\srv\share'`},
		{"/data/dicts", `"/data/dicts"`},
		{"/home/me/Словари", `"/home/me/Словари"`},
		{`C:\it's\odd`, `"C:\\it's\\odd"`}, // a quote rules the literal form out
		{"a\tb", `"a\tb"`},                 // so does a control character
	}
	for _, c := range cases {
		if got := QuoteTOML(c.in); got != c.want {
			t.Errorf("QuoteTOML(%q) = %s, want %s", c.in, got, c.want)
		}
		if got := decodeString(QuoteTOML(c.in)); got != c.in && !strings.ContainsRune(c.in, '\t') {
			t.Errorf("round trip %q → %s → %q", c.in, QuoteTOML(c.in), got)
		}
	}
}

// Replacing a key whose value spans several lines must take the whole value,
// not leave its tail behind as lines that parse as nothing.
func TestSaveKeyReplacesMultiLineValue(t *testing.T) {
	p := filepath.Join(t.TempDir(), Name)
	body := "# header\nDICT_DIR = [\n  \"/old/a\",\n  \"/old/b\",\n]\nSERVER_PORT = \"1\"\n"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SaveKeyRaw(p, "DICT_DIR", FormatList([]string{winPath})); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(p)
	s := string(data)
	if strings.Contains(s, "/old/") {
		t.Errorf("orphan lines left behind:\n%s", s)
	}
	if !strings.Contains(s, "# header") || !strings.Contains(s, "SERVER_PORT = \"1\"") {
		t.Errorf("unrelated lines lost:\n%s", s)
	}
	cfg, err := Load(p, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.DictDirs) != 1 || cfg.DictDirs[0] != winPath {
		t.Errorf("after replace: %q", cfg.DictDirs)
	}
	if cfg.Port != "1" {
		t.Errorf("neighbouring key disturbed: %q", cfg.Port)
	}
}

// The shipped template must still parse to nothing but defaults: every line in
// it is commented, and the new scanner must not start seeing values in it.
func TestTemplateStaysInert(t *testing.T) {
	vals, lists := parseTOML(configTemplate)
	if len(vals) != 0 || len(lists) != 0 {
		t.Errorf("template yielded %v / %v", vals, lists)
	}
}
