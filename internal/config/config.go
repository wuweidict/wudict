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
	DictDirs  []string // DICT_DIR: one or more folders scanned for dictionaries
	DBDir     string   // DB_DIR: cache dir for generated .text.db/.media.db
	IP        string   // SERVER_IP
	Port      string   // SERVER_PORT
	NoBrowser bool     // NO_BROWSER=1: do not open a browser tab

	// Tray is tri-state, so it is a pointer rather than a bool: unset means
	// "decide from how the app was launched" - a GUI launch (a macOS .app, the
	// double-clicked wudict.exe) gets an icon, a terminal or a service does not.
	// TRAY=1 or TRAY=0 overrides that decision in either direction (D74).
	Tray          *bool  // TRAY
	Verbose       bool   // VERBOSE=1: verbose logging
	Speexdec      string // SPEEXDEC: path to the external speexdec binary (.spx audio)
	SpeexBackend  string // SPEEX_BACKEND: internal (in-process libspeex, default) | external (speexdec binary)
	AutoIndex     string // AUTO_INDEX: on|off - prepare a dictionary's index on first search
	UseCached     bool   // USE_CACHED=1: also list previously imported dictionaries from the db dir
	NoCompress    bool   // NO_COMPRESS=1: store article text verbatim (bigger databases)
	IndexWorkers  int    // INDEX_WORKERS: how many dictionaries may be indexed at once
	MemoryLimit   int64  // MEMORY_LIMIT: soft heap ceiling in bytes (0 = none)
	PreviewMemory int64  // PREVIEW_MEMORY: cap on RAM held by unprepared dictionaries (0 = unlimited)
	SearchMemory  int64  // SEARCH_MEMORY: cap on RAM ONE search may materialise (0 = uncapped)
	Source        string // path of the wudict.toml that was loaded ("" if none)

	// BrowserExtensions (BROWSER_EXTENSIONS) pins which browser-extension
	// origins may read /api/dicts, /api/search and /res/ cross-origin. Empty =
	// any chrome-extension:// or moz-extension:// origin. See D69.
	BrowserExtensions []string

	// Portable reports that Source sits next to the executable: the user put
	// it there, so that is where saves go too (D32).
	Portable bool

	// Shadowed lists config files that exist further down the search order and
	// therefore did nothing. Saying so out loud is the whole cure for "I
	// edited it and nothing changed" - two binaries, two files, one silent winner.
	Shadowed []string

	// Origins records which layer supplied each key: "flag", "env", "file" or
	// "default". With four layers, "I edited wudict.toml and nothing changed"
	// is the classic confusion - a flag or an environment variable outranks
	// the file - so the UI can say where a value actually came from, and
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
		// platform's opinion of us rather than its capability - see tuning.go
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
	fileVals, fileLists := r.vals, r.lists
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
	// getList is get for a key naming several folders. The file layer is the
	// one that can spell it as a TOML array, and that array is taken as it was
	// parsed rather than re-serialised and split again - see resolved.lists.
	// The other layers hand over one string, which ParseList splits as before.
	getList := func(key string) []string {
		if v, ok := flags[key]; ok && v != "" {
			cfg.Origins[key] = OriginFlag
			return ParseList(v)
		}
		if v := os.Getenv(key); v != "" {
			cfg.Origins[key] = OriginEnv
			return ParseList(v)
		}
		if l, ok := fileLists[key]; ok {
			cfg.Origins[key] = OriginFile
			return expandAll(l) // an explicit [] means "this file sets nothing"
		}
		if v := fileVals[key]; v != "" {
			cfg.Origins[key] = OriginFile
			// ONE folder, exactly as written. A file spells several folders as
			// an array - that is what FormatList writes and what the template
			// documents; the separator convention belongs to the environment,
			// where it follows $PATH. Splitting here instead cut
			// "C:\Users\me\Dicts" in half at the drive letter on any host
			// whose separator is ':' , and cut a Unix folder whose name
			// contains one anywhere.
			return expandAll([]string{v})
		}
		cfg.Origins[key] = OriginDefault
		return nil
	}
	if dirs := getList("DICT_DIR"); len(dirs) > 0 {
		cfg.DictDirs = dirs
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
	if v := get("TRAY"); v != "" {
		on := v != "0"
		cfg.Tray = &on
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
// to be "config.toml" - the most generic filename there is, shared with Rust,
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
// exist but lost - reported so a shadowed file can be named instead of silently
// ignored.
type resolved struct {
	// vals holds every key as a string: a scalar fully decoded, an array as
	// the raw TOML text it was written with (the keys still parsed from text -
	// BROWSER_EXTENSIONS - read it, DICT_DIR does not).
	vals map[string]string
	// lists holds the keys written as a TOML array, already split and decoded.
	// A list of paths must never be flattened back into one string and
	// re-split: on Windows a path contains the characters that would be split
	// on, and it is the round trip through text that doubled every backslash.
	lists    map[string][]string
	path     string
	portable bool
	shadowed []string
}

// loadFile reads the first wudict.toml found. An explicit path is taken as
// given and must exist - a typo there must fail loudly rather than fall back to
// a different file. Missing candidates are not an error.
func loadFile(explicit string) (resolved, error) {
	if explicit != "" {
		data, err := os.ReadFile(explicit)
		if err != nil {
			return resolved{}, fmt.Errorf("config %s: %w", explicit, err)
		}
		vals, lists := parseTOML(string(data))
		return resolved{vals: vals, lists: lists, path: explicit}, nil
	}
	r := resolved{vals: map[string]string{}, lists: map[string][]string{}}
	for _, p := range candidates() {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		if r.path != "" {
			r.shadowed = append(r.shadowed, p)
			continue
		}
		r.vals, r.lists = parseTOML(string(data))
		r.path = p
	}
	if d := exeDir(); d != "" && r.path != "" && r.path == filepath.Join(d, Name) {
		r.portable = true
	}
	return r, nil
}

const configTemplate = `# wudict configuration  (~/.wudict/wudict.toml)
# Priority: CLI flag > environment variable > this file > built-in default.
# All keys are optional - uncomment a line to override its default.

# DICT_DIR    = "~/Dictionaries"      # folder with dictionaries (.mdx, .ifo, .slob, .dsl, .bgl)
#                                     # several folders: ["~/Dictionaries", "/Volumes/Ext/Dicts"]
#                                     # (in the environment separate them with ":", ";" on Windows)
#                                     # none of them may be the DB_DIR folder
# DB_DIR      = "~/.wudict/db"    # library of prepared dictionaries (one folder each)
# SERVER_IP   = "127.0.0.1"           # listen address (0.0.0.0 = all interfaces)
# SERVER_PORT = "6888"
# NO_BROWSER  = "0"                   # "1" = do not open a browser tab on startup
# TRAY        = ""                    # "1" = system tray icon, "0" = never; unset = only when launched from a desktop
# VERBOSE     = "0"                   # "1" = verbose logging for debugging
# SPEEX_BACKEND = "internal"          # ".spx" audio decoder: "internal" (built-in libspeex) or "external" (speexdec)
# SPEEXDEC    = "/usr/bin/speexdec"   # external speexdec path; blank = auto-detect (next to the executable, then $PATH)
# AUTO_INDEX  = "on"                  # "off" = never prepare an index on its own; searching then
#                                     #         uses the dictionary's own format directly
# INDEX_WORKERS = "1"                 # how many dictionaries may be prepared at once. Each one
#                                     # saturates a core and holds a few hundred bytes per headword,
#                                     # so the default is one - the machine stays usable.
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
# MEMORY_LIMIT  = "0"                 # soft ceiling, e.g. "4GB": Go collects harder - and sheds its
#                                     # caches - rather than growing past it. "0" = no ceiling.
#                                     # Android defaults to a fraction of the device's RAM.
# NO_COMPRESS = "0"                   # "1" = store article text uncompressed (databases roughly 3x larger,
#                                     #       marginally faster reads; only worth it with disk to spare)
# USE_CACHED  = "0"                   # "1" = also list previously imported dictionaries kept in DB_DIR
#                                     #       (set from the setup page: "Use these dictionaries")
# BROWSER_EXTENSIONS = []             # which browser extensions may read this server from a web page
#                                     # they run in. Blank = any installed extension may look words up
#                                     # (it can read dictionaries and nothing else - never your
#                                     # settings or library). List origins to allow only those:
#                                     # ["chrome-extension://abcdefghijklmnopabcdefghijklmnop"]
`

// EnsureConfigFile makes sure a config file exists, generating the fully
// commented template on first run. It writes exactly one place -
// ~/.wudict/wudict.toml - and never beside the executable.
//
// It used to prefer the executable's directory and fall back to the home
// directory "if that is not writable". Writability was answering a question it
// cannot answer: it was being read as "this directory is ours". It is not.
// `go install` lands in ~/go/bin and Homebrew in /opt/homebrew/bin - both
// user-writable, both shared with every other program on the machine - so the
// probe succeeded exactly where it should have failed, and the fallback never
// fired on the two most common installs. Guessing harder (matching /opt, /usr,
// …) only lengthens a denylist that is incomplete by construction.
//
// So the rule is inverted, and now complete: portable mode is something the
// user DECLARES, by putting a wudict.toml next to the binary. When they have,
// this function is never reached - the search found it and saves go there (D32).
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
	return SaveKeyRaw(path, key, QuoteTOML(value))
}

// QuoteTOML renders one value as TOML.
//
// A value containing a backslash is written as a LITERAL string ('…'), in
// which backslashes have no meaning. That is where a Windows path belongs in a
// TOML file: it is what someone opening wudict.toml expects to read and what
// they would type by hand, and it takes escaping out of the round trip
// altogether. Go's %q - a TOML basic string - would write
// "C:\\Users\\me\\Dicts", and did.
//
// The literal form cannot hold a single quote (a literal string has no escapes
// at all) or a control character (neither single-line form can), so those fall
// back to %q, which decodeString reads back.
func QuoteTOML(v string) string {
	if strings.ContainsRune(v, '\\') && !strings.ContainsRune(v, '\'') && !hasControl(v) {
		return "'" + v + "'"
	}
	return fmt.Sprintf("%q", v)
}

func hasControl(v string) bool {
	for _, r := range v {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

// SaveKeyRaw is SaveKey for a value that is already TOML syntax - an array
// from FormatList, say - so lists round-trip through the same
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
		// The pattern is line-anchored, but an array may be spread over
		// several lines. Replacing only the first would leave its tail behind
		// as orphan lines, so follow the value to wherever it closes.
		end := loc[1]
		if _, val, ok := strings.Cut(s[loc[0]:end], "="); ok {
			_, depth := scanValue(strings.TrimSpace(val), 0)
			for depth > 0 && end < len(s) && s[end] == '\n' {
				stop := len(s)
				if nl := strings.IndexByte(s[end+1:], '\n'); nl >= 0 {
					stop = end + 1 + nl
				}
				_, depth = scanValue(strings.TrimSpace(s[end+1:stop]), depth)
				end = stop
			}
		}
		s = s[:loc[0]] + line + s[end:]
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

// wudict.toml holds nothing but flat `KEY = value` lines, so this package
// reads it itself rather than taking a TOML dependency. The one thing such a
// reader must get right is quoting, because the character TOML gives a meaning
// to - the backslash - is the character a Windows path is spelled with. This
// used to strip the quotes and stop, so "C:\\Users\\me" (which is how the
// writer, correctly, escapes C:\Users\me) was read back with both backslashes
// still there.

// scanValue returns the text of the value starting at s, with any trailing
// comment removed, and the bracket depth left open at the end of it. Quotes
// are tracked, so a '#' inside a string is data - a folder called "vol #2"
// survives - and a depth above zero means an array continues on a later line.
func scanValue(s string, depth int) (val string, newDepth int) {
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\'':
			j := strings.IndexByte(s[i+1:], '\'')
			if j < 0 {
				return strings.TrimRight(s, " \t"), depth // unterminated: as written
			}
			i += j + 1
		case '"':
			i++
			for i < len(s) && s[i] != '"' {
				if s[i] == '\\' {
					i++
				}
				i++
			}
			if i >= len(s) {
				return strings.TrimRight(s, " \t"), depth
			}
		case '[':
			depth++
		case ']':
			if depth > 0 {
				depth--
			}
		case '#':
			return strings.TrimRight(s[:i], " \t"), depth
		}
	}
	return strings.TrimRight(s, " \t"), depth
}

// decodeString turns one quoted TOML string into the text it denotes.
//
// A literal string ('…') is taken verbatim; that is the whole point of it, and
// it is what QuoteTOML now writes for anything holding a backslash.
//
// In a basic string ("…") exactly two escapes are honoured, \\ and \", and
// every other backslash is kept as written. That is deliberately not full
// TOML. It reads back everything this package emits, since %q produces no
// other escape for a path; and it is safe for a file edited by hand, where a
// backslash is nearly always a Windows separator typed literally. A strict
// reader turns "C:\temp\notes" into a tab and a newline and loses the folder,
// which is a far worse failure than declining to interpret an escape nobody
// meant to write.
func decodeString(s string) string {
	if len(s) >= 2 && s[0] == '\'' && s[len(s)-1] == '\'' {
		return s[1 : len(s)-1]
	}
	if len(s) < 2 || s[0] != '"' || s[len(s)-1] != '"' {
		return s // bare: a number, a bool, or an unquoted path
	}
	body := s[1 : len(s)-1]
	if !strings.Contains(body, `\`) {
		return body
	}
	var b strings.Builder
	b.Grow(len(body))
	for i := 0; i < len(body); i++ {
		if body[i] == '\\' && i+1 < len(body) && (body[i+1] == '\\' || body[i+1] == '"') {
			b.WriteByte(body[i+1])
			i++
			continue
		}
		b.WriteByte(body[i])
	}
	return b.String()
}

// decodeArray reads a TOML array of strings, splitting on the commas that are
// OUTSIDE quotes so that a folder whose name contains one survives. It reports
// false for anything that is not an array.
func decodeArray(s string) ([]string, bool) {
	s = strings.TrimSpace(s)
	if len(s) < 2 || s[0] != '[' || s[len(s)-1] != ']' {
		return nil, false
	}
	body := s[1 : len(s)-1]
	var out []string
	start := 0
	flush := func(end int) {
		if e := strings.TrimSpace(body[start:end]); e != "" {
			out = append(out, decodeString(e))
		}
	}
	for i := 0; i < len(body); i++ {
		switch body[i] {
		case '\'':
			if j := strings.IndexByte(body[i+1:], '\''); j >= 0 {
				i += j + 1
			}
		case '"':
			i++
			for i < len(body) && body[i] != '"' {
				if body[i] == '\\' {
					i++
				}
				i++
			}
		case ',':
			flush(i)
			start = i + 1
		}
	}
	flush(len(body))
	return out, true
}

// parseTOML reads the flat `KEY = value` subset our config files use. It
// returns scalars decoded, and separately the keys written as an array -
// already split, so no caller has to reconstruct one from text. An array whose
// bracket closes on a later line is gathered rather than ignored.
func parseTOML(s string) (map[string]string, map[string][]string) {
	vals := map[string]string{}
	lists := map[string][]string{}
	lines := strings.Split(s, "\n")
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(strings.TrimSuffix(lines[i], "\r"))
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "[") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		raw, depth := scanValue(strings.TrimSpace(v), 0)
		for depth > 0 && i+1 < len(lines) {
			i++
			var next string
			next, depth = scanValue(strings.TrimSpace(strings.TrimSuffix(lines[i], "\r")), depth)
			raw = strings.TrimRight(raw+" "+next, " \t")
		}
		key := strings.TrimSpace(k)
		if l, isArray := decodeArray(raw); isArray {
			lists[key] = l
			vals[key] = raw // the raw array, for the keys still parsed from text
			continue
		}
		vals[key] = decodeString(raw)
	}
	return vals, lists
}

// expandAll ~-expands folders and drops blanks, preserving order.
func expandAll(in []string) []string {
	out := make([]string, 0, len(in))
	for _, p := range in {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, ExpandHome(p))
		}
	}
	return out
}

// ParseList reads a DICT_DIR value in any of the forms the layers produce:
//
//	["~/Dicts", "/Volumes/Ext/Dicts"]   wudict.toml array
//	~/Dicts:/Volumes/Ext/Dicts          environment (os.PathListSeparator,
//	                                    ';' on Windows - ':' would collide
//	                                    with drive letters, and this is a path
//	                                    list, so it follows $PATH convention)
//	~/Dicts                             a single folder, exactly as before
//
// Entries are ~-expanded and blanks dropped; order is preserved (it decides
// nothing but which root wins a tie - result ranking belongs to the panel).
func ParseList(v string) []string {
	v = strings.TrimSpace(v)
	// An array is decoded, never split by hand: quoting and commas belong to
	// one scanner. Everything else is a separator-joined list from a flag or
	// the environment, where the value is a raw path and there is nothing to
	// unquote - decoding it would eat the backslashes of C:\temp.
	if strings.HasPrefix(v, "[") {
		parts, _ := decodeArray(v)
		return expandAll(parts)
	}
	return expandAll(strings.Split(v, string(os.PathListSeparator)))
}

// ParseOrigins reads BROWSER_EXTENSIONS: a list of browser-extension origins,
// written as a TOML array or separated by commas, semicolons or whitespace.
//
//	BROWSER_EXTENSIONS = ["chrome-extension://abc…", "moz-extension://1f2e…"]
//	BROWSER_EXTENSIONS=chrome-extension://abc…,moz-extension://1f2e…
//
// It deliberately does NOT split on os.PathListSeparator the way ParseList does:
// that is ':' on Unix, and every value here contains one. A trailing '/' is
// dropped - an origin has no path, but it is the obvious thing to type.
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
		return QuoteTOML(dirs[0])
	}
	quoted := make([]string, len(dirs))
	for i, d := range dirs {
		quoted[i] = QuoteTOML(d)
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

// AUTO_INDEX values. "fuzzy" is the pre-D16 spelling of "on" - the mode it
// named was retired, the setting was not - and is still accepted so an
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
// hundred bytes per headword, so the default is ONE - a background convenience
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
