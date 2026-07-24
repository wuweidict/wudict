package config

import (
	"os"
	"path/filepath"
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

func TestExpandHome(t *testing.T) {
	home, _ := os.UserHomeDir()
	if got := expandHome("~/x"); got != filepath.Join(home, "x") {
		t.Errorf("expandHome: %q", got)
	}
	if got := expandHome("/abs"); got != "/abs" {
		t.Errorf("abs untouched: %q", got)
	}
}
