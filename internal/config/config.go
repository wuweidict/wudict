// Package config implements gonow-dict's configuration with the layering
// borrowed from mdict-go-web: CLI flag > environment variable >
// config.toml > built-in default.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Config holds all server settings.
type Config struct {
	DictDir   string // DICT_DIR: directory scanned for dictionaries
	DBDir     string // DB_DIR: cache dir for generated .text.db/.media.db
	IP        string // SERVER_IP
	Port      string // SERVER_PORT
	NoBrowser bool   // NO_BROWSER=1: do not open a browser tab
}

func defaults() Config {
	home, _ := os.UserHomeDir()
	return Config{
		DictDir: filepath.Join(home, "Dictionaries"),
		DBDir:   "", // empty = store.DefaultDBDir()
		IP:      "127.0.0.1",
		Port:    "8808",
	}
}

// Load builds the effective config. flags maps key -> value for
// CLI-provided values (highest priority); configPath overrides the
// config.toml search order when non-empty.
func Load(configPath string, flags map[string]string) (Config, error) {
	cfg := defaults()

	fileVals, err := loadFile(configPath)
	if err != nil {
		return cfg, err
	}
	get := func(key string) string {
		if v, ok := flags[key]; ok && v != "" {
			return v
		}
		if v := os.Getenv(key); v != "" {
			return v
		}
		return fileVals[key]
	}
	if v := get("DICT_DIR"); v != "" {
		cfg.DictDir = expandHome(v)
	}
	if v := get("DB_DIR"); v != "" {
		cfg.DBDir = expandHome(v)
	}
	if v := get("SERVER_IP"); v != "" {
		cfg.IP = v
	}
	if v := get("SERVER_PORT"); v != "" {
		cfg.Port = v
	}
	if v := get("NO_BROWSER"); v != "" && v != "0" {
		cfg.NoBrowser = true
	}
	return cfg, nil
}

// loadFile reads the first config.toml found in the search order:
// explicit path, <exe-dir>/config.toml, ~/.gonow-dict/config.toml,
// ./config.toml. Missing files are not an error.
func loadFile(explicit string) (map[string]string, error) {
	var candidates []string
	if explicit != "" {
		candidates = []string{explicit}
	} else {
		if exe, err := os.Executable(); err == nil {
			candidates = append(candidates, filepath.Join(filepath.Dir(exe), "config.toml"))
		}
		if home, err := os.UserHomeDir(); err == nil {
			candidates = append(candidates, filepath.Join(home, ".gonow-dict", "config.toml"))
		}
		candidates = append(candidates, "config.toml")
	}
	for _, p := range candidates {
		data, err := os.ReadFile(p)
		if err != nil {
			if explicit != "" {
				return nil, fmt.Errorf("config %s: %w", p, err)
			}
			continue
		}
		return parseTOML(string(data)), nil
	}
	return map[string]string{}, nil
}

// parseTOML handles the flat `KEY = "value"` subset used by our
// config files (comments and bare values included).
func parseTOML(s string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "[") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		if i := strings.Index(v, " #"); i >= 0 {
			v = strings.TrimSpace(v[:i])
		}
		v = strings.Trim(v, `"'`)
		out[strings.TrimSpace(k)] = v
	}
	return out
}

func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(p, "~"), "/"))
		}
	}
	return p
}

// Addr returns the listen address.
func (c Config) Addr() string { return c.IP + ":" + c.Port }
