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
	toml := filepath.Join(dir, "config.toml")
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
	t.Setenv("SERVER_PORT", "")
	t.Setenv("DICT_DIR", "")
	cfg, err := Load("", nil) // no explicit file; defaults apply
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Port != "8808" || cfg.IP != "127.0.0.1" {
		t.Errorf("defaults: %+v", cfg)
	}
	if _, err := Load(filepath.Join(t.TempDir(), "nope.toml"), nil); err == nil {
		t.Error("explicit missing config must error")
	}
}

func TestSaveKey(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	os.WriteFile(p, []byte("# header\n# DICT_DIR    = \"~/Dictionaries\"      # comment\n# SERVER_PORT = \"8808\"\n"), 0o644)
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
	vals, src, err := loadFile(p)
	if err != nil || src != p || vals["DICT_DIR"] != "/data/dicts" {
		t.Errorf("round-trip: %v %q %v", vals, src, err)
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
		p := filepath.Join(dir, "config.toml")
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

// AUTO_INDEX is on|off since the "fuzzy" search mode it was named after was
// retired — but an existing config.toml saying "fuzzy" must keep working.
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
		p := filepath.Join(dir, "config.toml")
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
