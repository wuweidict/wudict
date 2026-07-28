// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Package config implements gonow-dict's configuration with the layering
// borrowed from mdict-go-web: CLI flag > environment variable >
// config.toml > built-in default.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Config holds all server settings.
type Config struct {
	DictDirs     []string // DICT_DIR: one or more folders scanned for dictionaries
	DBDir        string   // DB_DIR: cache dir for generated .text.db/.media.db
	IP           string   // SERVER_IP
	Port         string   // SERVER_PORT
	NoBrowser    bool     // NO_BROWSER=1: do not open a browser tab
	Verbose      bool     // VERBOSE=1: verbose logging
	Speexdec     string   // SPEEXDEC: path to the external speexdec binary (.spx audio)
	SpeexBackend string   // SPEEX_BACKEND: internal (in-process libspeex, default) | external (speexdec binary)
	AutoIndex    string   // AUTO_INDEX: on|off — prepare a dictionary's index on first search
	UseCached    bool     // USE_CACHED=1: also list previously imported dictionaries from the db dir
	NoCompress   bool     // NO_COMPRESS=1: store article text verbatim (bigger databases)
	Source       string   // path of the config.toml that was loaded ("" if none)

	// Origins records which layer supplied each key: "flag", "env", "file" or
	// "default". With four layers, "I edited config.toml and nothing changed"
	// is the classic confusion — a flag or an environment variable outranks
	// the file — so the UI can say where a value actually came from, and
	// refuse to pretend that saving it will take effect.
	Origins map[string]string
}

// Origin layers, in priority order.
const (
	OriginFlag    = "flag"
	OriginEnv     = "env"
	OriginFile    = "file"
	OriginDefault = "default"
)

func defaults() Config {
	home, _ := os.UserHomeDir()
	return Config{
		DictDirs: []string{filepath.Join(home, "Dictionaries")},
		DBDir:    "", // empty = store.DefaultDBDir()
		IP:       "127.0.0.1",
		Port:     "8808",
		// Speexdec "" = auto-detect at launch (next to the executable, then
		// $PATH); SPEEXDEC overrides. See resolveSpeexdec in the CLI.
		Speexdec:     "",
		SpeexBackend: "internal",  // in-process libspeex (cgo); "external" = speexdec binary
		AutoIndex:    AutoIndexOn, // opt-out: prepare an index on first use
	}
}

// Load builds the effective config. flags maps key -> value for
// CLI-provided values (highest priority); configPath overrides the
// config.toml search order when non-empty.
func Load(configPath string, flags map[string]string) (Config, error) {
	cfg := defaults()

	fileVals, source, err := loadFile(configPath)
	if err != nil {
		return cfg, err
	}
	cfg.Source = source
	cfg.Origins = map[string]string{}
	get := func(key string) string {
		if v, ok := flags[key]; ok && v != "" {
			cfg.Origins[key] = OriginFlag
			return v
		}
		if v := os.Getenv(key); v != "" {
			cfg.Origins[key] = OriginEnv
			return v
		}
		if v := fileVals[key]; v != "" {
			cfg.Origins[key] = OriginFile
			return v
		}
		cfg.Origins[key] = OriginDefault
		return ""
	}
	if v := get("DICT_DIR"); v != "" {
		if dirs := ParseList(v); len(dirs) > 0 {
			cfg.DictDirs = dirs
		}
	}
	if v := get("DB_DIR"); v != "" {
		cfg.DBDir = ExpandHome(v)
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
	if v := get("VERBOSE"); v != "" && v != "0" {
		cfg.Verbose = true
	}
	if v := get("SPEEXDEC"); v != "" {
		cfg.Speexdec = ExpandHome(v)
	}
	if v := get("SPEEX_BACKEND"); v != "" {
		cfg.SpeexBackend = strings.ToLower(v)
	}
	if v := get("AUTO_INDEX"); v != "" {
		cfg.AutoIndex = normalizeAutoIndex(v)
	}
	if v := get("USE_CACHED"); v != "" && v != "0" && !strings.EqualFold(v, "false") {
		cfg.UseCached = true
	}
	if v := get("NO_COMPRESS"); v != "" && v != "0" && !strings.EqualFold(v, "false") {
		cfg.NoCompress = true
	}
	return cfg, nil
}

// candidates returns the config.toml search order (mdict-go-web parity):
// <exe-dir>, ~/.gonow-dict, /etc/gonow-dict, ./ .
func candidates() []string {
	var out []string
	if exe, err := os.Executable(); err == nil {
		out = append(out, filepath.Join(filepath.Dir(exe), "config.toml"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		out = append(out, filepath.Join(home, ".gonow-dict", "config.toml"))
	}
	out = append(out, "/etc/gonow-dict/config.toml", "config.toml")
	return out
}

// loadFile reads the first config.toml found (explicit path first when
// given). Missing files are not an error; it reports which file loaded.
func loadFile(explicit string) (map[string]string, string, error) {
	paths := candidates()
	if explicit != "" {
		paths = []string{explicit}
	}
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			if explicit != "" {
				return nil, "", fmt.Errorf("config %s: %w", p, err)
			}
			continue
		}
		return parseTOML(string(data)), p, nil
	}
	return map[string]string{}, "", nil
}

const configTemplate = `# gonow-dict configuration
# Priority: CLI flag > environment variable > this file > built-in default.
# All keys are optional — uncomment a line to override its default.

# DICT_DIR    = "~/Dictionaries"      # folder with dictionaries (.mdx, .ifo, .slob, .dsl, .bgl)
#                                     # several folders: ["~/Dictionaries", "/Volumes/Ext/Dicts"]
#                                     # (in the environment separate them with ":", ";" on Windows)
#                                     # none of them may be the DB_DIR folder
# DB_DIR      = "~/.gonow-dict/db"    # library of prepared dictionaries (one folder each)
# SERVER_IP   = "127.0.0.1"           # listen address (0.0.0.0 = all interfaces)
# SERVER_PORT = "8808"
# NO_BROWSER  = "0"                   # "1" = do not open a browser tab on startup
# VERBOSE     = "0"                   # "1" = verbose logging for debugging
# SPEEX_BACKEND = "internal"          # ".spx" audio decoder: "internal" (built-in libspeex) or "external" (speexdec)
# SPEEXDEC    = "/usr/bin/speexdec"   # external speexdec path; blank = auto-detect (next to the executable, then $PATH)
# AUTO_INDEX  = "on"                  # "off" = never prepare an index on its own; searching then
#                                     #         uses the dictionary's own format directly
# NO_COMPRESS = "0"                   # "1" = store article text uncompressed (databases roughly 3x larger,
#                                     #       marginally faster reads; only worth it with disk to spare)
# USE_CACHED  = "0"                   # "1" = also list previously imported dictionaries kept in DB_DIR
#                                     #       (set from the setup page: "Use these dictionaries")
`

// EnsureConfigFile makes sure a config.toml exists somewhere in the
// search order, generating a fully commented template on first run.
// Preferred location is next to the executable; if that is not writable
// (Homebrew, /usr/local/bin, …) it falls back to ~/.gonow-dict/.
// Returns the path and whether it was created now.
func EnsureConfigFile() (path string, created bool, err error) {
	for _, p := range candidates() {
		if _, err := os.Stat(p); err == nil {
			return p, false, nil
		}
	}
	var targets []string
	if exe, err := os.Executable(); err == nil {
		targets = append(targets, filepath.Join(filepath.Dir(exe), "config.toml"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		targets = append(targets, filepath.Join(home, ".gonow-dict", "config.toml"))
	}
	var lastErr error
	for _, p := range targets {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			lastErr = err
			continue
		}
		if err := os.WriteFile(p, []byte(configTemplate), 0o644); err != nil {
			lastErr = err
			continue
		}
		return p, true, nil
	}
	return "", false, lastErr
}

// SaveKey sets key = "value" in the config file at path, uncommenting or
// replacing an existing `KEY =` line (commented or not) and preserving
// the rest of the file; the key is appended when absent.
func SaveKey(path, key, value string) error {
	return SaveKeyRaw(path, key, fmt.Sprintf("%q", value))
}

// SaveKeyRaw is SaveKey for a value that is already TOML syntax — an array
// from FormatList, say — so lists round-trip through the same
// uncomment-in-place, comments-preserved edit as scalars.
func SaveKeyRaw(path, key, raw string) error {
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	line := fmt.Sprintf("%s = %s", key, raw)
	re := regexp.MustCompile(`(?m)^[ \t]*#?[ \t]*` + regexp.QuoteMeta(key) + `[ \t]*=.*$`)
	s := string(data)
	if loc := re.FindStringIndex(s); loc != nil {
		s = s[:loc[0]] + line + s[loc[1]:]
	} else {
		if s != "" && !strings.HasSuffix(s, "\n") {
			s += "\n"
		}
		s += line + "\n"
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(s), 0o644)
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

// ParseList reads a DICT_DIR value in any of the forms the layers produce:
//
//	["~/Dicts", "/Volumes/Ext/Dicts"]   config.toml array
//	~/Dicts:/Volumes/Ext/Dicts          environment (os.PathListSeparator,
//	                                    ';' on Windows — ':' would collide
//	                                    with drive letters, and this is a path
//	                                    list, so it follows $PATH convention)
//	~/Dicts                             a single folder, exactly as before
//
// Entries are ~-expanded and blanks dropped; order is preserved (it decides
// nothing but which root wins a tie — result ranking belongs to the panel).
func ParseList(v string) []string {
	v = strings.TrimSpace(v)
	var parts []string
	if strings.HasPrefix(v, "[") {
		for _, raw := range strings.Split(strings.Trim(v, "[]"), ",") {
			parts = append(parts, strings.Trim(strings.TrimSpace(raw), "\"'`"))
		}
	} else {
		parts = strings.Split(v, string(os.PathListSeparator))
	}
	var out []string
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, ExpandHome(p))
		}
	}
	return out
}

// FormatList renders folders back into config.toml syntax: a bare string for
// one folder (the common case stays readable), an array for several.
func FormatList(dirs []string) string {
	if len(dirs) == 1 {
		return fmt.Sprintf("%q", dirs[0])
	}
	quoted := make([]string, len(dirs))
	for i, d := range dirs {
		quoted[i] = fmt.Sprintf("%q", d)
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

// ExpandHome expands a leading ~ to the user home directory.
func ExpandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(p, "~"), "/"))
		}
	}
	return p
}

// AUTO_INDEX values. "fuzzy" is the pre-D16 spelling of "on" — the mode it
// named was retired, the setting was not — and is still accepted so an
// existing config.toml keeps working.
const (
	AutoIndexOn  = "on"
	AutoIndexOff = "off"
)

// normalizeAutoIndex maps what a user may have written onto on/off.
func normalizeAutoIndex(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "off", "0", "false", "no", "none":
		return AutoIndexOff
	default: // "on", "1", "true", and the legacy "fuzzy"
		return AutoIndexOn
	}
}

// AutoIndexEnabled reports whether dictionaries index themselves on first use.
func (c Config) AutoIndexEnabled() bool { return c.AutoIndex != AutoIndexOff }

// Origin reports which layer supplied key ("default" when nothing did).
func (c Config) Origin(key string) string {
	if o, ok := c.Origins[key]; ok {
		return o
	}
	return OriginDefault
}

// EditableInFile reports whether saving key to config.toml would actually take
// effect, i.e. no higher-priority layer is currently setting it.
func (c Config) EditableInFile(key string) bool {
	o := c.Origin(key)
	return o != OriginFlag && o != OriginEnv
}

// Addr returns the listen address.
func (c Config) Addr() string { return c.IP + ":" + c.Port }
