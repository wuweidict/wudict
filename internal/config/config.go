// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Package config implements wudict's configuration with the layering
// borrowed from mdict-go-web: CLI flag > environment variable >
// wudict.toml > built-in default.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

// Config holds all server settings.
type Config struct {
	DictDirs      []string // DICT_DIR: one or more folders scanned for dictionaries
	DBDir         string   // DB_DIR: cache dir for generated .text.db/.media.db
	IP            string   // SERVER_IP
	Port          string   // SERVER_PORT
	NoBrowser     bool     // NO_BROWSER=1: do not open a browser tab
	Verbose       bool     // VERBOSE=1: verbose logging
	Speexdec      string   // SPEEXDEC: path to the external speexdec binary (.spx audio)
	SpeexBackend  string   // SPEEX_BACKEND: internal (in-process libspeex, default) | external (speexdec binary)
	AutoIndex     string   // AUTO_INDEX: on|off — prepare a dictionary's index on first search
	UseCached     bool     // USE_CACHED=1: also list previously imported dictionaries from the db dir
	NoCompress    bool     // NO_COMPRESS=1: store article text verbatim (bigger databases)
	IndexWorkers  int      // INDEX_WORKERS: how many dictionaries may be indexed at once
	MemoryLimit   int64    // MEMORY_LIMIT: soft heap ceiling in bytes (0 = none)
	PreviewMemory int64    // PREVIEW_MEMORY: cap on RAM held by unprepared dictionaries (0 = unlimited)
	SearchMemory  int64    // SEARCH_MEMORY: cap on RAM ONE search may materialise (0 = uncapped)
	Source        string   // path of the wudict.toml that was loaded ("" if none)

	// BrowserExtensions (BROWSER_EXTENSIONS) pins which browser-extension
	// origins may read /api/dicts, /api/search and /res/ cross-origin. Empty =
	// any chrome-extension:// or moz-extension:// origin. See D69.
	BrowserExtensions []string

	// Portable reports that Source sits next to the executable: the user put
	// it there, so that is where saves go too (D32).
	Portable bool

	// Shadowed lists config files that exist further down the search order and
	// therefore did nothing. Saying so out loud is the whole cure for "I
	// edited it and nothing changed" — two binaries, two files, one silent winner.
	Shadowed []string

	// Origins records which layer supplied each key: "flag", "env", "file" or
	// "default". With four layers, "I edited wudict.toml and nothing changed"
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
		Port:     "6888",
		// Speexdec "" = auto-detect at launch (next to the executable, then
		// $PATH); SPEEXDEC overrides. See resolveSpeexdec in the CLI.
		Speexdec:     "",
		SpeexBackend: "internal",  // in-process libspeex (cgo); "external" = speexdec binary
		AutoIndex:    AutoIndexOn, // opt-out: prepare an index on first use
		IndexWorkers: 1,           // one dictionary at a time: the machine has other work to do
		// both are platform-dependent, and for reasons that are about the
		// platform's opinion of us rather than its capability — see tuning.go
		PreviewMemory: previewMemoryDefault(),
		MemoryLimit:   memoryLimitDefault(),
		SearchMemory:  searchMemoryDefault(),
	}
}

// Load builds the effective config. flags maps key -> value for
// CLI-provided values (highest priority); configPath overrides the
// wudict.toml search order when non-empty.
func Load(configPath string, flags map[string]string) (Config, error) {
	cfg := defaults()

	r, err := loadFile(configPath)
	if err != nil {
		return cfg, err
	}
	fileVals := r.vals
	cfg.Source, cfg.Portable, cfg.Shadowed = r.path, r.portable, r.shadowed
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
	if v := get("INDEX_WORKERS"); v != "" {
		cfg.IndexWorkers = ParseWorkers(v)
	}
	if v := get("MEMORY_LIMIT"); v != "" {
		cfg.MemoryLimit = ParseSize(v)
	}
	if v := get("PREVIEW_MEMORY"); v != "" {
		cfg.PreviewMemory = ParseSize(v)
	}
	if v := get("SEARCH_MEMORY"); v != "" {
		cfg.SearchMemory = ParseSize(v)
	}
	if v := get("BROWSER_EXTENSIONS"); v != "" {
		cfg.BrowserExtensions = ParseOrigins(v)
	}
	return cfg, nil
}

// Name is the configuration file, spelled the same in every location. It used
// to be "config.toml" — the most generic filename there is, shared with Rust,
// Hugo and half the checkouts on a developer's disk. The old bare "./config.toml"
// candidate turned that collision into a live defect: running wudict from such
// a directory parsed a stranger's file, suppressed creation of our own, and
// pointed the setup page's SaveKey at it. A name nobody else uses cannot be
// mistaken for anything, wherever it is copied to (D32).
const Name = "wudict.toml"

// systemDir is the machine-wide location; a variable so tests can point it
// somewhere harmless rather than depend on what is really in /etc.
var systemDir = filepath.Join("/etc", "wudict")

// Seams for the two directories the search is anchored to. Tests replace them;
// both return "" when the OS cannot say, which simply drops that candidate.
var (
	exeDir = func() string {
		p, err := os.Executable()
		if err != nil {
			return ""
		}
		return filepath.Dir(p)
	}
	homeDir = func() string {
		p, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		return p
	}
)

// HomeConfig is the only file wudict ever creates: ~/.wudict/wudict.toml,
// beside the library that directory already holds.
func HomeConfig() string {
	if h := homeDir(); h != "" {
		return filepath.Join(h, ".wudict", Name)
	}
	return ""
}

// candidates returns the search order: next to the executable (portable mode,
// see EnsureConfigFile), then the user's own directory, then the machine's.
func candidates() []string {
	var out []string
	if d := exeDir(); d != "" {
		out = append(out, filepath.Join(d, Name))
	}
	if p := HomeConfig(); p != "" {
		out = append(out, p)
	}
	return append(out, filepath.Join(systemDir, Name))
}

// resolved is the outcome of the search: the file that was read, whether it is
// the portable one next to the executable, and which lower-priority candidates
// exist but lost — reported so a shadowed file can be named instead of silently
// ignored.
type resolved struct {
	vals     map[string]string
	path     string
	portable bool
	shadowed []string
}

// loadFile reads the first wudict.toml found. An explicit path is taken as
// given and must exist — a typo there must fail loudly rather than fall back to
// a different file. Missing candidates are not an error.
func loadFile(explicit string) (resolved, error) {
	if explicit != "" {
		data, err := os.ReadFile(explicit)
		if err != nil {
			return resolved{}, fmt.Errorf("config %s: %w", explicit, err)
		}
		return resolved{vals: parseTOML(string(data)), path: explicit}, nil
	}
	r := resolved{vals: map[string]string{}}
	for _, p := range candidates() {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		if r.path != "" {
			r.shadowed = append(r.shadowed, p)
			continue
		}
		r.vals, r.path = parseTOML(string(data)), p
	}
	if d := exeDir(); d != "" && r.path != "" && r.path == filepath.Join(d, Name) {
		r.portable = true
	}
	return r, nil
}

const configTemplate = `# wudict configuration  (~/.wudict/wudict.toml)
# Priority: CLI flag > environment variable > this file > built-in default.
# All keys are optional — uncomment a line to override its default.

# DICT_DIR    = "~/Dictionaries"      # folder with dictionaries (.mdx, .ifo, .slob, .dsl, .bgl)
#                                     # several folders: ["~/Dictionaries", "/Volumes/Ext/Dicts"]
#                                     # (in the environment separate them with ":", ";" on Windows)
#                                     # none of them may be the DB_DIR folder
# DB_DIR      = "~/.wudict/db"    # library of prepared dictionaries (one folder each)
# SERVER_IP   = "127.0.0.1"           # listen address (0.0.0.0 = all interfaces)
# SERVER_PORT = "6888"
# NO_BROWSER  = "0"                   # "1" = do not open a browser tab on startup
# VERBOSE     = "0"                   # "1" = verbose logging for debugging
# SPEEX_BACKEND = "internal"          # ".spx" audio decoder: "internal" (built-in libspeex) or "external" (speexdec)
# SPEEXDEC    = "/usr/bin/speexdec"   # external speexdec path; blank = auto-detect (next to the executable, then $PATH)
# AUTO_INDEX  = "on"                  # "off" = never prepare an index on its own; searching then
#                                     #         uses the dictionary's own format directly
# INDEX_WORKERS = "1"                 # how many dictionaries may be prepared at once. Each one
#                                     # saturates a core and holds a few hundred bytes per headword,
#                                     # so the default is one — the machine stays usable.
#                                     # "auto" (or 0) = every core.
# PREVIEW_MEMORY = "1GB"              # how much RAM dictionaries that are NOT yet prepared may hold
#                                     # open. Each costs ~350 bytes per headword; the least recently
#                                     # used are closed above this. Prepared ones answer from disk and
#                                     # are never evicted. "0" = no limit. Android defaults to 64MB.
# SEARCH_MEMORY = "0"                 # how much RAM ONE search may bring in by opening dictionaries
#                                     # that are not yet prepared. Past it the rest are reported as
#                                     # not searched rather than opened. Prepared dictionaries are
#                                     # never capped. "0" = no cap. Android defaults to the memory
#                                     # limit, where one query could otherwise get the app killed.
# MEMORY_LIMIT  = "0"                 # soft ceiling, e.g. "4GB": Go collects harder — and sheds its
#                                     # caches — rather than growing past it. "0" = no ceiling.
#                                     # Android defaults to a fraction of the device's RAM.
# NO_COMPRESS = "0"                   # "1" = store article text uncompressed (databases roughly 3x larger,
#                                     #       marginally faster reads; only worth it with disk to spare)
# USE_CACHED  = "0"                   # "1" = also list previously imported dictionaries kept in DB_DIR
#                                     #       (set from the setup page: "Use these dictionaries")
# BROWSER_EXTENSIONS = []             # which browser extensions may read this server from a web page
#                                     # they run in. Blank = any installed extension may look words up
#                                     # (it can read dictionaries and nothing else — never your
#                                     # settings or library). List origins to allow only those:
#                                     # ["chrome-extension://abcdefghijklmnopabcdefghijklmnop"]
`

// EnsureConfigFile makes sure a config file exists, generating the fully
// commented template on first run. It writes exactly one place —
// ~/.wudict/wudict.toml — and never beside the executable.
//
// It used to prefer the executable's directory and fall back to the home
// directory "if that is not writable". Writability was answering a question it
// cannot answer: it was being read as "this directory is ours". It is not.
// `go install` lands in ~/go/bin and Homebrew in /opt/homebrew/bin — both
// user-writable, both shared with every other program on the machine — so the
// probe succeeded exactly where it should have failed, and the fallback never
// fired on the two most common installs. Guessing harder (matching /opt, /usr,
// …) only lengthens a denylist that is incomplete by construction.
//
// So the rule is inverted, and now complete: portable mode is something the
// user DECLARES, by putting a wudict.toml next to the binary. When they have,
// this function is never reached — the search found it and saves go there (D32).
//
// Returns the path and whether it was created now.
func EnsureConfigFile() (path string, created bool, err error) {
	for _, p := range candidates() {
		if _, err := os.Stat(p); err == nil {
			return p, false, nil
		}
	}
	p := HomeConfig()
	if p == "" {
		return "", false, fmt.Errorf("no home directory to create %s in", Name)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return "", false, err
	}
	if err := os.WriteFile(p, []byte(configTemplate), 0o644); err != nil {
		return "", false, err
	}
	return p, true, nil
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
//	["~/Dicts", "/Volumes/Ext/Dicts"]   wudict.toml array
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

// ParseOrigins reads BROWSER_EXTENSIONS: a list of browser-extension origins,
// written as a TOML array or separated by commas, semicolons or whitespace.
//
//	BROWSER_EXTENSIONS = ["chrome-extension://abc…", "moz-extension://1f2e…"]
//	BROWSER_EXTENSIONS=chrome-extension://abc…,moz-extension://1f2e…
//
// It deliberately does NOT split on os.PathListSeparator the way ParseList does:
// that is ':' on Unix, and every value here contains one. A trailing '/' is
// dropped — an origin has no path, but it is the obvious thing to type.
func ParseOrigins(v string) []string {
	v = strings.TrimSpace(v)
	if strings.HasPrefix(v, "[") {
		v = strings.Trim(v, "[]")
	}
	var out []string
	for _, raw := range strings.FieldsFunc(v, func(r rune) bool {
		return r == ',' || r == ';' || r == ' ' || r == '\t' || r == '\n' || r == '\r'
	}) {
		if s := strings.TrimRight(strings.Trim(raw, "\"'`"), "/"); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// FormatList renders folders back into wudict.toml syntax: a bare string for
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
// existing wudict.toml keeps working.
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

// ParseWorkers reads INDEX_WORKERS: a count, or "auto"/"all"/"max"/"0"/"-1"
// for every core. Preparing a dictionary saturates a core and allocates a few
// hundred bytes per headword, so the default is ONE — a background convenience
// must not take the machine away from the person using it. The result is
// clamped to [1, NumCPU].
func ParseWorkers(v string) int {
	v = strings.ToLower(strings.TrimSpace(v))
	switch v {
	case "auto", "all", "max", "cpu", "0", "-1":
		return runtime.NumCPU()
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 1
	}
	if n < 1 {
		return runtime.NumCPU()
	}
	if n > runtime.NumCPU() {
		return runtime.NumCPU()
	}
	return n
}

// ParseSize reads a byte size: plain bytes, or with a K/M/G suffix
// ("2GB", "1500MB", "512M"). Returns 0 for "off"/"none"/unparseable.
func ParseSize(v string) int64 {
	v = strings.ToLower(strings.TrimSpace(strings.ReplaceAll(v, " ", "")))
	if v == "" || v == "off" || v == "none" || v == "0" {
		return 0
	}
	v = strings.TrimSuffix(v, "b")
	mult := int64(1)
	switch {
	case strings.HasSuffix(v, "k"):
		mult, v = 1<<10, strings.TrimSuffix(v, "k")
	case strings.HasSuffix(v, "m"):
		mult, v = 1<<20, strings.TrimSuffix(v, "m")
	case strings.HasSuffix(v, "g"):
		mult, v = 1<<30, strings.TrimSuffix(v, "g")
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || f <= 0 {
		return 0
	}
	return int64(f * float64(mult))
}

// Origin reports which layer supplied key ("default" when nothing did).
func (c Config) Origin(key string) string {
	if o, ok := c.Origins[key]; ok {
		return o
	}
	return OriginDefault
}

// EditableInFile reports whether saving key to wudict.toml would actually take
// effect, i.e. no higher-priority layer is currently setting it.
func (c Config) EditableInFile(key string) bool {
	o := c.Origin(key)
	return o != OriginFlag && o != OriginEnv
}

// Addr returns the listen address.
func (c Config) Addr() string { return c.IP + ":" + c.Port }
