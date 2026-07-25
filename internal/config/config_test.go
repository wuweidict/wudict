// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"os"
	"path/filepath"
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
	if cfg.DictDir != "/from/toml" {
		t.Errorf("toml DICT_DIR: %q", cfg.DictDir)
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
