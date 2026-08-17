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

func TestLayering(t *testing.T) {
	dir := t.TempDir()
	toml := filepath.Join(dir, Name)
	os.WriteFile(toml, []byte(`
# comment
DICT_DIR = "/from/toml"
SERVER_PORT = "9999"
NO_BROWSER = "1"
`), 0o644)

	t.Setenv("SERVER_PORT", "7777") // env beats toml
	cfg, err := Load(toml, map[string]string{"SERVER_IP": "0.0.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.DictDirs) != 1 || cfg.DictDirs[0] != "/from/toml" {
		t.Errorf("toml DICT_DIR: %q", cfg.DictDirs)
	}
	if cfg.Port != "7777" {
		t.Errorf("env should beat toml: %q", cfg.Port)
	}
	if cfg.IP != "0.0.0.0" {
		t.Errorf("flag should win: %q", cfg.IP)
	}
	if !cfg.NoBrowser {
		t.Error("NO_BROWSER not applied")
	}
	if cfg.Addr() != "0.0.0.0:7777" {
		t.Errorf("Addr: %q", cfg.Addr())
	}
}

func TestDefaultsAndMissingFile(t *testing.T) {
	isolate(t) // no real ~/.wudict/wudict.toml may leak into the defaults
	t.Setenv("SERVER_PORT", "")
	t.Setenv("DICT_DIR", "")
	cfg, err := Load("", nil) // no explicit file; defaults apply
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Port != "6888" || cfg.IP != "127.0.0.1" {
		t.Errorf("defaults: %+v", cfg)
	}
	if _, err := Load(filepath.Join(t.TempDir(), "nope.toml"), nil); err == nil {
		t.Error("explicit missing config must error")
	}
}

func TestSaveKey(t *testing.T) {
	p := filepath.Join(t.TempDir(), Name)
	os.WriteFile(p, []byte("# header\n# DICT_DIR    = \"~/Dictionaries\"      # comment\n# SERVER_PORT = \"6888\"\n"), 0o644)
	if err := SaveKey(p, "DICT_DIR", "/data/dicts"); err != nil {
		t.Fatal(err)
	}
	if err := SaveKey(p, "NEW_KEY", "v"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(p)
	s := string(data)
	if !strings.Contains(s, "DICT_DIR = \"/data/dicts\"") {
		t.Errorf("commented key not uncommented: %q", s)
	}
	if strings.Contains(s, "# DICT_DIR") {
		t.Errorf("old commented line kept: %q", s)
	}
	if !strings.Contains(s, "# header") || !strings.Contains(s, "# SERVER_PORT") {
		t.Errorf("unrelated lines lost: %q", s)
	}
	if !strings.Contains(s, "NEW_KEY = \"v\"") {
		t.Errorf("append failed: %q", s)
	}
	// round-trip: parse sees the saved value
	r, err := loadFile(p)
	if err != nil || r.path != p || r.vals["DICT_DIR"] != "/data/dicts" {
		t.Errorf("round-trip: %v %q %v", r.vals, r.path, err)
	}
}

func TestExpandHome(t *testing.T) {
	home, _ := os.UserHomeDir()
	if got := ExpandHome("~/x"); got != filepath.Join(home, "x") {
		t.Errorf("ExpandHome: %q", got)
	}
	if got := ExpandHome("/abs"); got != "/abs" {
		t.Errorf("abs untouched: %q", got)
	}
}

// DICT_DIR accepts one folder or several, in every layer's own spelling.
func TestDictDirList(t *testing.T) {
	sep := string(os.PathListSeparator)
	home, _ := os.UserHomeDir()
	cases := []struct {
		name, value string
		want        []string
	}{
		{"single", "/a", []string{"/a"}},
		{"toml array", `["/a", "/b"]`, []string{"/a", "/b"}},
		{"toml array, single entry", `["/a"]`, []string{"/a"}},
		{"env separated", "/a" + sep + "/b", []string{"/a", "/b"}},
		{"blanks dropped", "/a" + sep + sep + " /b ", []string{"/a", "/b"}},
		{"tilde expanded", "~/d", []string{filepath.Join(home, "d")}},
		{"order preserved", `["/b","/a"]`, []string{"/b", "/a"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ParseList(c.value)
			if len(got) != len(c.want) {
				t.Fatalf("ParseList(%q) = %q, want %q", c.value, got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("ParseList(%q) = %q, want %q", c.value, got, c.want)
				}
			}
		})
	}
	// and it round-trips through the config file in both shapes
	dir := t.TempDir()
	for _, dirs := range [][]string{{"/only"}, {"/a", "/b b"}} {
		p := filepath.Join(dir, Name)
		if err := os.WriteFile(p, []byte("# DICT_DIR = \"~/Dictionaries\"\nSERVER_PORT = \"1\"\n"), 0o644); err != nil {
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
			t.Fatalf("round-trip %q → %q", dirs, cfg.DictDirs)
		}
		for i := range dirs {
			if cfg.DictDirs[i] != dirs[i] {
				t.Fatalf("round-trip %q → %q", dirs, cfg.DictDirs)
			}
		}
		if cfg.Port != "1" {
			t.Errorf("saving DICT_DIR disturbed another key: %q", cfg.Port)
		}
	}
}

// BROWSER_EXTENSIONS holds origins, not paths: every value contains the ':'
// that ParseList splits on, so it needs its own parser (D69).
func TestParseOrigins(t *testing.T) {
	const chrome = "chrome-extension://abcdefghijklmnopabcdefghijklmnop"
	const fx = "moz-extension://5b1f2e3c-0a4d-4c7e-9f21-2a6c8d1e7b40"
	cases := []struct {
		name, value string
		want        []string
	}{
		{"single", chrome, []string{chrome}},
		{"toml array", `["` + chrome + `", "` + fx + `"]`, []string{chrome, fx}},
		{"comma separated", chrome + "," + fx, []string{chrome, fx}},
		{"space separated", chrome + " " + fx, []string{chrome, fx}},
		{"the scheme's colon is not a separator", chrome, []string{chrome}},
		{"trailing slash dropped", chrome + "/", []string{chrome}},
		{"blanks dropped", " , " + chrome + " , ", []string{chrome}},
		{"empty", "", nil},
		{"empty array", "[]", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ParseOrigins(c.value)
			if len(got) != len(c.want) {
				t.Fatalf("ParseOrigins(%q) = %q, want %q", c.value, got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("ParseOrigins(%q) = %q, want %q", c.value, got, c.want)
				}
			}
		})
	}
	// …and it survives the config file, whose parser strips quotes and a
	// trailing " #" comment before this ever sees the value.
	p := filepath.Join(t.TempDir(), Name)
	if err := os.WriteFile(p, []byte("BROWSER_EXTENSIONS = [\""+chrome+"\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(p, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.BrowserExtensions) != 1 || cfg.BrowserExtensions[0] != chrome {
		t.Errorf("BROWSER_EXTENSIONS from file = %q, want [%q]", cfg.BrowserExtensions, chrome)
	}
	blank := filepath.Join(t.TempDir(), Name)
	if err := os.WriteFile(blank, []byte("SERVER_PORT = \"1\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if cfg2, _ := Load(blank, nil); len(cfg2.BrowserExtensions) != 0 {
		t.Errorf("unset BROWSER_EXTENSIONS = %q, want empty (any extension)", cfg2.BrowserExtensions)
	}
}

// AUTO_INDEX is on|off since the "fuzzy" search mode it was named after was
// retired — but an existing wudict.toml saying "fuzzy" must keep working.
func TestAutoIndexValues(t *testing.T) {
	for _, c := range []struct {
		in   string
		want string
		on   bool
	}{
		{"", AutoIndexOn, true}, // unset → default
		{"on", AutoIndexOn, true},
		{"ON", AutoIndexOn, true},
		{"1", AutoIndexOn, true},
		{"true", AutoIndexOn, true},
		{"fuzzy", AutoIndexOn, true}, // the legacy spelling
		{"off", AutoIndexOff, false},
		{"OFF", AutoIndexOff, false},
		{"0", AutoIndexOff, false},
		{"false", AutoIndexOff, false},
		{"no", AutoIndexOff, false},
	} {
		dir := t.TempDir()
		p := filepath.Join(dir, Name)
		body := ""
		if c.in != "" {
			body = "AUTO_INDEX = \"" + c.in + "\"\n"
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		cfg, err := Load(p, nil)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.AutoIndex != c.want || cfg.AutoIndexEnabled() != c.on {
			t.Errorf("AUTO_INDEX=%q → %q (enabled=%v), want %q (%v)",
				c.in, cfg.AutoIndex, cfg.AutoIndexEnabled(), c.want, c.on)
		}
	}
}

func TestParseWorkersAndSize(t *testing.T) {
	cpu := runtime.NumCPU()
	for in, want := range map[string]int{
		"1": 1, "2": 2, "auto": cpu, "all": cpu, "max": cpu, "0": cpu, "-1": cpu,
		"": 1, "nonsense": 1, "99999": cpu,
	} {
		if got := ParseWorkers(in); got != want {
			t.Errorf("ParseWorkers(%q) = %d, want %d", in, got, want)
		}
	}
	for in, want := range map[string]int64{
		"4GB": 4 << 30, "512M": 512 << 20, "1500MB": 1500 << 20, "2048": 2048,
		"1.5GB": int64(1.5 * float64(int64(1)<<30)), "0": 0, "off": 0, "": 0, "junk": 0,
	} {
		if got := ParseSize(in); got != want {
			t.Errorf("ParseSize(%q) = %d, want %d", in, got, want)
		}
	}
	// default is one dictionary at a time: background work must not take the machine
	cfg, err := Load(filepath.Join(t.TempDir(), "none.toml"), nil)
	if err == nil && cfg.IndexWorkers != 1 {
		t.Errorf("default INDEX_WORKERS = %d, want 1", cfg.IndexWorkers)
	}
}

// isolate points the three search anchors at empty temporary directories, so a
// test sees only the files it creates — never the developer's real ~/.wudict
// or /etc. Returns the executable and home directories, in search order.
func isolate(t *testing.T) (exe, home string) {
	t.Helper()
	root := t.TempDir()
	exe, home, etc := filepath.Join(root, "bin"), filepath.Join(root, "home"), filepath.Join(root, "etc")
	for _, d := range []string{exe, home, etc} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	oldExe, oldHome, oldSys := exeDir, homeDir, systemDir
	exeDir = func() string { return exe }
	homeDir = func() string { return home }
	systemDir = etc
	t.Cleanup(func() { exeDir, homeDir, systemDir = oldExe, oldHome, oldSys })
	return exe, home
}

// Where the config file is looked for, which one wins, and — the point of D32 —
// where a new one is written.
func TestConfigLocation(t *testing.T) {
	write := func(t *testing.T, path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("created in the home directory, never beside the executable", func(t *testing.T) {
		exe, home := isolate(t)
		p, created, err := EnsureConfigFile()
		if err != nil || !created {
			t.Fatalf("EnsureConfigFile() = %q, %v, %v", p, created, err)
		}
		if want := filepath.Join(home, ".wudict", Name); p != want {
			t.Errorf("created %q, want %q", p, want)
		}
		// the executable's directory belongs to whoever installed us
		if _, err := os.Stat(filepath.Join(exe, Name)); !os.IsNotExist(err) {
			t.Errorf("wrote next to the executable: %v", err)
		}
		// idempotent: the second run finds the first one
		p2, created2, err := EnsureConfigFile()
		if err != nil || created2 || p2 != p {
			t.Errorf("second call = %q, %v, %v; want %q, false", p2, created2, err, p)
		}
	})

	t.Run("an existing file is never overwritten", func(t *testing.T) {
		exe, _ := isolate(t)
		portable := filepath.Join(exe, Name)
		write(t, portable, `SERVER_PORT = "1234"`)
		p, created, err := EnsureConfigFile()
		if err != nil || created || p != portable {
			t.Fatalf("EnsureConfigFile() = %q, %v, %v; want %q, false", p, created, err, portable)
		}
	})

	t.Run("portable beats home, and says so", func(t *testing.T) {
		exe, home := isolate(t)
		write(t, filepath.Join(exe, Name), `SERVER_PORT = "1111"`)
		write(t, filepath.Join(home, ".wudict", Name), `SERVER_PORT = "2222"`)
		cfg, err := Load("", nil)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Port != "1111" {
			t.Errorf("port = %q, want the portable file's 1111", cfg.Port)
		}
		if !cfg.Portable {
			t.Error("Portable not reported for a file beside the executable")
		}
		if len(cfg.Shadowed) != 1 || cfg.Shadowed[0] != filepath.Join(home, ".wudict", Name) {
			t.Errorf("Shadowed = %q, want the home file", cfg.Shadowed)
		}
	})

	t.Run("home beats system and is not portable", func(t *testing.T) {
		_, home := isolate(t)
		write(t, filepath.Join(home, ".wudict", Name), `SERVER_PORT = "2222"`)
		write(t, filepath.Join(systemDir, Name), `SERVER_PORT = "3333"`)
		cfg, err := Load("", nil)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Port != "2222" || cfg.Portable {
			t.Errorf("port = %q portable = %v, want 2222 and not portable", cfg.Port, cfg.Portable)
		}
		if len(cfg.Shadowed) != 1 {
			t.Errorf("Shadowed = %q, want the system file", cfg.Shadowed)
		}
	})

	t.Run("the working directory is not searched", func(t *testing.T) {
		isolate(t)
		dir := t.TempDir()
		write(t, filepath.Join(dir, Name), `SERVER_PORT = "9999"`)
		write(t, filepath.Join(dir, "config.toml"), `SERVER_PORT = "9999"`)
		wd, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Chdir(dir); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Chdir(wd) })
		cfg, err := Load("", nil)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Source != "" || cfg.Port == "9999" {
			t.Errorf("picked up a file from the working directory: %q", cfg.Source)
		}
	})

	t.Run("an explicit path must exist", func(t *testing.T) {
		exe, _ := isolate(t)
		write(t, filepath.Join(exe, Name), `SERVER_PORT = "1111"`)
		// silently falling back to the portable file would hide the typo
		if _, err := Load(filepath.Join(t.TempDir(), "typo.toml"), nil); err == nil {
			t.Error("explicit missing config must error")
		}
	})
}
