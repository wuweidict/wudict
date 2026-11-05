// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Package config implements wudict's configuration with the layering
// borrowed from mdict-go-web: CLI flag > environment variable >
// wudict.toml > built-in default.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"math"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"

	"github.com/wuweidict/wudict/internal/basedir"
	"github.com/wuweidict/wudict/internal/fsx"
	"github.com/wuweidict/wudict/internal/limits"
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
	Tray         *bool  // TRAY
	Verbose      bool   // VERBOSE=1: verbose logging
	Speexdec     string // SPEEXDEC: path to the external speexdec binary (.spx audio)
	SpeexBackend string // SPEEX_BACKEND: internal (in-process libspeex, default) | external (speexdec binary)
	AutoIndex    string // AUTO_INDEX: on|off - prepare a dictionary's index on first search

	// ImportKeep (IMPORT_KEEP) is what becomes of the archive an import came
	// from once its dictionaries are installed: "ask" puts the choice in front
	// of the user each time, "keep" and "delete" answer it for them. It has no
	// bearing on a FAILED import, where the source is always kept - deleting
	// somebody's only copy of a dictionary after an import that did not work
	// is unrecoverable, so it is not something a setting may authorise.
	ImportKeep string // IMPORT_KEEP: ask|keep|delete

	// ImportURLHosts (IMPORT_URL_HOSTS) restricts which sites an archive may
	// be DOWNLOADED from. A host matches itself and its subdomains; an EMPTY
	// list - the DEFAULT - is no restriction at all.
	//
	// Opt-in, because an allowlist of community sites answers a question
	// nobody asked (D139). A link reaches this server only because the user shared, pasted
	// or tapped it: there is no path by which one arrives unbidden, so a list
	// deciding which of the user's OWN links are permissible is not protecting
	// them from anything - it is refusing the dictionary they went and found
	// on the fourth site. What does protect the machine is unrelated to the
	// host's NAME and is not controlled here: plain http and addresses on the
	// local network are refused by the fetcher itself, and only ImportInsecure
	// lifts those.
	//
	// A setting for the one case it is right for: a wudict reachable from a
	// LAN, where an unauthenticated caller could
	// otherwise point the download anywhere the host machine can reach.
	ImportURLHosts []string // IMPORT_URL_HOSTS: comma-separated, or a TOML array
	// ImportInsecure (IMPORT_INSECURE=1) lifts the two refusals a download
	// makes on its own: plain http, and a host that resolves to a private,
	// loopback or link-local address. The second matters because this server
	// may be listening on a LAN: without it, a browser on another machine
	// could use the download as a way to reach hosts only this machine can
	// see. One key for both because they are one question - "I trust what I
	// am pointing this at" - and two would invite turning off the wrong one.
	ImportInsecure bool // IMPORT_INSECURE
	UseCached      bool // USE_CACHED=1: also list previously imported dictionaries from the db dir

	// AllowRemoteDelete (ALLOW_REMOTE_DELETE) governs whether a browser on
	// ANOTHER machine may delete a dictionary. Removal from the machine
	// running wudict is always allowed - this is a personal app managing its
	// own library, and the user at the keyboard owns those files. The default
	// is false: the server has no authentication, so a caller that is not this
	// machine has proved nothing, and the one irreversible operation is the
	// wrong place to be generous. Set it to "1" when the server is bound to a
	// network address and the LAN is trusted with deletions.
	AllowRemoteDelete bool        // ALLOW_REMOTE_DELETE
	Compression       Compression // COMPRESSION: how article text is stored when a dictionary is indexed (D191)
	IndexWorkers      int         // INDEX_WORKERS: how many dictionaries may be indexed at once
	MemoryLimit       int64       // MEMORY_LIMIT: soft heap ceiling in bytes (0 = none)
	PreviewMemory     int64       // PREVIEW_MEMORY: cap on RAM held by unprepared dictionaries (0 = unlimited)
	SearchMemory      int64       // SEARCH_MEMORY: cap on RAM ONE search may materialise (0 = uncapped)
	MorphCache        int         // MORPH_CACHE: how many lemma packs stay resident (0 = no lemmatization)
	LemmaDir          string      // LEMMA_DIR: folder of installed lemma files (adds/overrides languages)
	LemmaURL          string      // LEMMA_URL: manifest `wudict lemmas` installs languages from
	Source            string      // path of the wudict.toml that was loaded ("" if none)

	// BrowserExtensions (BROWSER_EXTENSIONS) pins which browser-extension
	// origins may read /api/dicts, /api/search and /res/ cross-origin. Empty =
	// any chrome-extension:// or moz-extension:// origin. See D69.
	BrowserExtensions []string

	// WebOrigins (WEB_ORIGINS) lists the http(s) page origins allowed to read
	// those same three endpoints cross-origin. Empty - the default - means no
	// web page may, which is not the same default as BrowserExtensions and is
	// deliberately not: an extension is something the user installed, a web
	// page is whatever they happened to open. "*" allows any origin. See D69.
	WebOrigins []string

	// TrustedHosts (TRUSTED_HOSTS) lists DNS names, beyond "localhost" and a
	// SERVER_IP name (D104 Am. 1), that may appear in a request's Host header. Empty - the default - means
	// none, and nothing legitimate notices: browsers, the Android shell and
	// extensions all address the server by IP. A name is only needed in front
	// of a reverse proxy, and is otherwise the ingredient DNS rebinding
	// requires. "*" disables the check. See internal/server/host.go.
	TrustedHosts []string

	// Auth (AUTH) decides whether a request must carry the capability token:
	// "auto" (the default) requires it whenever the listen address is not
	// loopback, "on" always, "off" never. See token.go and AuthRequired.
	Auth string

	// AuthToken (AUTH_TOKEN) is the token itself, when it is supplied rather
	// than read from the token file - which is how the Android shell, which
	// has no home directory to keep one in, passes the secret it generated.
	// It is never published in /api/config and never a Tunable.
	AuthToken string

	// Portable reports that Source sits next to the executable: the user put
	// it there, so that is where saves go too (D32).
	Portable bool

	// Shadowed lists config files that exist further down the search order and
	// therefore did nothing. Saying so out loud is the whole cure for "I
	// edited it and nothing changed" - two binaries, two files, one silent winner.
	Shadowed []string

	// Limits are the MAX_* guards against corrupt or hostile files
	// (internal/limits); the CLI applies them at startup.
	Limits limits.Limits

	// Problems are lines of the config file in effect that set nothing - not
	// `KEY = value`, a key wudict does not know (a typo is otherwise silent),
	// an array or a string left open. The rest of the file still applies;
	// the CLI prints them at startup and the setup page shows them.
	Problems []string

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
		ImportKeep:   ImportKeepAsk,
		// Nil: any site (D139). Dictionaries come from wherever the user
		// found them, and the link is one they handed over deliberately.
		ImportURLHosts: nil,
		IndexWorkers:   1, // one dictionary at a time: the machine has other work to do
		// both are platform-dependent, and for reasons that are about the
		// platform's opinion of us rather than its capability - see tuning.go
		PreviewMemory: previewMemoryDefault(),
		MemoryLimit:   memoryLimitDefault(),
		SearchMemory:  searchMemoryDefault(),
		MorphCache:    morphCacheDefault(),
		LemmaDir:      join(dirs().Data, "lemmas"),
		// off by default: see the field comment - loopback deletes regardless,
		// so this only ever opens the LAN case, and that is opt-in.
		AllowRemoteDelete: false,
		LemmaURL:          DefaultLemmaURL,
		Limits:            limits.Default,
		Compression:       DefaultCompression(),
	}
}

// LimitKeys are the MAX_* keys, each with the guard it sets, what it bounds
// and the range a value is held to. Ratio keys are a plain number; the others
// a size ParseSize reads ("256MB", "16GB"). The flag of each is its key in
// lower case with '-' for '_' (--max-zim-cluster).
//
// The range is what keeps an edited value from breaking wudict rather than
// tuning it. The floors sit several times above the largest unit measured in
// real files - ZIM clusters 2.3 MB, slob bins 977 kB, MDict and Babylon blocks
// tens of kB to a few MB, the largest lemma data 11.5 MB - so lowering a limit
// cannot stop an ordinary dictionary from opening. The ceilings of the
// per-block keys are what one allocation can be (4 GB, or the address space of
// a 32-bit build); past the others' ceilings a value means nothing a disk
// holds. A value outside the range is moved to the nearest end of it.
var LimitKeys = []struct {
	Key      string
	Ratio    bool
	Min, Max int64
	Help     string
	Field    func(*limits.Limits) *int64
}{
	{"MAX_ZIM_CLUSTER", false, 16 << 20, blockMax, "one ZIM cluster, unpacked", func(l *limits.Limits) *int64 { return &l.ZIMCluster }},
	{"MAX_SLOB_BIN", false, 16 << 20, blockMax, "one Aard 2 (.slob) bin, unpacked", func(l *limits.Limits) *int64 { return &l.SlobBin }},
	{"MAX_MDICT_BLOCK", false, 16 << 20, blockMax, "one MDict (.mdx/.mdd) block, unpacked", func(l *limits.Limits) *int64 { return &l.MDictBlock }},
	{"MAX_STARDICT_INDEX", false, 16 << 20, blockMax, "a compressed StarDict index, unpacked; 16 times the file is always allowed", func(l *limits.Limits) *int64 { return &l.StarDictIndex }},
	{"MAX_BGL_BLOCK", false, 8 << 20, blockMax, "one Babylon (.bgl) block", func(l *limits.Limits) *int64 { return &l.BGLBlock }},
	{"MAX_LEMMA_SIZE", false, 16 << 20, 1 << 30, "one lemma data file, unpacked", func(l *limits.Limits) *int64 { return &l.LemmaSize }},
	{"MAX_IMPORT_SIZE", false, 1 << 30, 64 << 40, "one dictionary taken out of an archive, or downloaded", func(l *limits.Limits) *int64 { return &l.ImportSize }},
	{"MAX_IMPORT_RATIO", true, 100, 100_000, "how many times its packed size one file in an archive may unpack to", func(l *limits.Limits) *int64 { return &l.ImportRatio }},
	{"MAX_UPLOAD_SIZE", false, 1 << 30, 64 << 40, "one archive sent to the server from a browser or the app", func(l *limits.Limits) *int64 { return &l.UploadSize }},
}

// blockMax is the ceiling of a per-block limit: 4 GB, or what one []byte can
// hold on a 32-bit build.
const blockMax = min(4<<30, math.MaxInt)

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
	cfg.Source, cfg.Portable, cfg.Shadowed, cfg.Problems = r.path, r.portable, r.shadowed, r.problems
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
	// The other layers hand over one string, which ParseList splits.
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
	// Every boolean below reads through isOff, and none of them spells the
	// negatives inline. Set-at-all means on, EXCEPT for the negatives a person
	// actually writes in a config file - so TRAY = "no", VERBOSE = "off" and
	// NO_BROWSER = "false" mean what they say instead of the opposite.
	if v := get("NO_BROWSER"); v != "" {
		cfg.NoBrowser = !isOff(v)
	}
	if v := get("TRAY"); v != "" {
		on := !isOff(v)
		cfg.Tray = &on
	}
	if v := get("VERBOSE"); v != "" {
		cfg.Verbose = !isOff(v)
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
	if v := get("IMPORT_KEEP"); v != "" {
		cfg.ImportKeep = normalizeImportKeep(v)
	}
	if v := get("IMPORT_URL_HOSTS"); v != "" {
		// Set-but-empty is meaningful and different from unset: it is how a
		// user removes the restriction, so it must not fall back to the
		// default the way an unset key does.
		cfg.ImportURLHosts = ParseHostList(v)
	}
	if v := get("IMPORT_INSECURE"); v != "" {
		cfg.ImportInsecure = !isOff(v)
	}
	if v := get("USE_CACHED"); v != "" {
		cfg.UseCached = !isOff(v)
	}
	if v := get("ALLOW_REMOTE_DELETE"); v != "" {
		cfg.AllowRemoteDelete = !isOff(v)
	}
	resolveCompression(&cfg, get)
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
	for _, k := range LimitKeys {
		v := get(k.Key)
		if v == "" {
			continue
		}
		n := ParseSize(v)
		if k.Ratio {
			n = int64(ParseCount(v))
		}
		// A guard cannot be switched off, and cannot be set where it breaks
		// dictionaries or overflows (LimitKeys): what applies instead is said
		// where Problems are shown, startup included.
		at := fmt.Sprintf("%s = %q (%s)", k.Key, v, cfg.Origins[k.Key])
		switch {
		case n <= 0:
			cfg.Problems = append(cfg.Problems, fmt.Sprintf("%s is not a positive value: the default %s applies", at, limits.Format(*k.Field(&cfg.Limits))))
		case n < k.Min:
			*k.Field(&cfg.Limits) = k.Min
			cfg.Problems = append(cfg.Problems, fmt.Sprintf("%s is below the lowest allowed value: %s applies", at, limits.Format(k.Min)))
		case n > k.Max:
			*k.Field(&cfg.Limits) = k.Max
			cfg.Problems = append(cfg.Problems, fmt.Sprintf("%s is above the highest allowed value: %s applies", at, limits.Format(k.Max)))
		default:
			*k.Field(&cfg.Limits) = n
		}
	}
	if v := get("MORPH_CACHE"); v != "" {
		cfg.MorphCache = ParseCount(v)
	}
	if v := get("LEMMA_DIR"); v != "" {
		cfg.LemmaDir = ExpandHome(v)
	}
	if v := get("LEMMA_URL"); v != "" {
		cfg.LemmaURL = v
	}
	if v := get("BROWSER_EXTENSIONS"); v != "" {
		cfg.BrowserExtensions = ParseOrigins(v)
	}
	if v := get("WEB_ORIGINS"); v != "" {
		cfg.WebOrigins = ParseOrigins(v)
	}
	if v := get("TRUSTED_HOSTS"); v != "" {
		cfg.TrustedHosts = ParseOrigins(v)
	}
	if v := get("AUTH"); v != "" {
		cfg.Auth = v
	}
	if v := get("AUTH_TOKEN"); v != "" {
		cfg.AuthToken = v
	}
	return cfg, nil
}

// Name is the configuration file, spelled the same in every location. Not
// "config.toml" - the most generic filename there is, shared with Rust, Hugo
// and half the checkouts on a developer's disk, where wudict would parse a
// stranger's file, skip creating its own, and point the setup page's SaveKey
// at it. A name nobody else uses cannot be mistaken for anything, wherever it
// is copied to (D32).
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

// dirs is the user's layout (internal/basedir): ~/.wudict, or on a free Unix
// desktop the XDG base directories.
func dirs() basedir.Dirs { return basedir.Resolve(goos, homeDir(), os.Getenv) }

// goos is runtime.GOOS; tests set it to see another platform's layout.
var goos = runtime.GOOS

// join is filepath.Join that keeps "no such folder" empty.
func join(dir, name string) string {
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, name)
}

// HomeConfig is the only file wudict ever creates: wudict.toml in the user's
// config folder - ~/.wudict, or $XDG_CONFIG_HOME/wudict.
func HomeConfig() string { return join(dirs().Config, Name) }

// TokenPath is where the access key lives (token.go): the state folder,
// because the key belongs to this machine and is not a setting to copy.
// Empty when there is no home directory, in which case a key can still be
// supplied by AUTH_TOKEN.
func TokenPath() string { return join(dirs().State, "token") }

// candidates returns the search order: next to the executable (portable mode,
// see EnsureConfigFile), then the user's own directory, then the machine's -
// $XDG_CONFIG_DIRS where that spec applies, and /etc/wudict.
func candidates() []string {
	var out []string
	if d := exeDir(); d != "" {
		out = append(out, filepath.Join(d, Name))
	}
	if p := HomeConfig(); p != "" {
		out = append(out, p)
	}
	if basedir.XDG(goos) {
		for _, d := range basedir.SystemConfigDirs(os.Getenv) {
			out = append(out, filepath.Join(d, Name))
		}
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
	problems []string
	portable bool
	shadowed []string
}

// maxConfigBytes bounds a wudict.toml, which a user edits by hand: far above
// any real one, and a pipe or a device in its place is refused (fsx).
const maxConfigBytes = 1 << 20

// loadFile reads the first wudict.toml found. An explicit path is taken as
// given and must exist - a typo there must fail loudly rather than fall back to
// a different file. Missing candidates are not an error.
func loadFile(explicit string) (resolved, error) {
	if explicit != "" {
		data, err := fsx.ReadBounded(explicit, maxConfigBytes)
		if err != nil {
			return resolved{}, fmt.Errorf("config %s: %w", explicit, err)
		}
		vals, lists := parseTOML(string(data))
		return resolved{vals: vals, lists: lists, path: explicit, problems: checkTOML(string(data))}, nil
	}
	r := resolved{vals: map[string]string{}, lists: map[string][]string{}}
	for _, p := range candidates() {
		data, err := fsx.ReadBounded(p, maxConfigBytes)
		if err != nil {
			continue
		}
		if r.path != "" {
			r.shadowed = append(r.shadowed, p)
			continue
		}
		r.vals, r.lists = parseTOML(string(data))
		r.path, r.problems = p, checkTOML(string(data))
	}
	if d := exeDir(); d != "" && r.path != "" && r.path == filepath.Join(d, Name) {
		r.portable = true
	}
	return r, nil
}

const configTemplate = `# wudict configuration  (~/.wudict/wudict.toml)
# Priority: CLI flag > environment variable > this file > built-in default.
# All keys are optional - uncomment a line to override its default.

# DICT_DIR    = "~/Dictionaries"      # folder with dictionaries (.mdx, .ifo, .slob, .dsl, .bgl, .zim)
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
# IMPORT_KEEP = "ask"                 # what happens to an archive after its dictionaries are
#                                     # installed: "ask" each time, "keep" it, or "delete" it.
#                                     # An import that FAILED always keeps it, whatever this says.
# IMPORT_URL_HOSTS = ""               # restrict which sites an archive may be downloaded from.
#                                     # A host matches itself and its subdomains. Empty - the
#                                     # default - is any site. Worth setting only if this server
#                                     # is reachable from your network, since a caller there could
#                                     # otherwise aim a download anywhere this machine can reach.
# IMPORT_INSECURE = "0"               # "1" = also allow plain http, and hosts on the local network.
#                                     # Only meaningful if you trust the address you are pointing at.
# INDEX_WORKERS = "1"                 # how many dictionaries may be prepared at once. Each one
#                                     # saturates a core and holds a few hundred bytes per headword,
#                                     # so the default is one - the machine stays usable.
#                                     # "auto" (or 0) = every core.
# PREVIEW_MEMORY = "1GB"              # how much RAM dictionaries that are NOT yet prepared may hold
#                                     # open. Each costs ~350 bytes per headword; the least recently
#                                     # used are closed above this. Prepared ones answer from disk and
#                                     # are never evicted. "0" = no limit. On Android the default is a third of
#                                     # MEMORY_LIMIT (64-128MB, by device RAM).
# SEARCH_MEMORY = "0"                 # how much RAM ONE search may bring in by opening dictionaries
#                                     # that are not yet prepared. Past it the rest are reported as
#                                     # not searched rather than opened. Prepared dictionaries are
#                                     # never capped. "0" = no cap. Android defaults to the memory
#                                     # limit, where one query could otherwise get the app killed.
# MORPH_CACHE = "2"                   # how many LEMMA packs stay in memory. When a search finds
#                                     # nothing at all, wudict tries the word's dictionary form and
#                                     # searches again. English is built in; other languages come
#                                     # from LEMMA_DIR. Each loaded language costs 7-65 MB and is
#                                     # dropped when another needs the slot.
#                                     # "0" = never lemmatize, load nothing.
#                                     # Android defaults to 1.
# LEMMA_DIR   = "~/.wudict/lemmas"    # folder of installed lemma files. Every language but English
#                                     # comes from here - one file per language, named after it
#                                     # ("pl.txt", "polish.tsv.gz"; .txt/.tsv, optionally .gz).
#                                     # Each line is a lemma and its forms, tab-separated, lower
#                                     # case - the format of the lists at
#                                     # github.com/michmech/lemmatization-lists, which load as
#                                     # downloaded. An "en" file replaces the built-in English.
#                                     # Read once at startup.
#                                     # "wudict lemmas list" shows what is installed and what is
#                                     # available; "wudict lemmas download pl" puts a file here.
# LEMMA_URL = ""                      # where "wudict lemmas" looks for installable languages: the
#                                     # URL of a manifest.json, or a path to one on disk for an
#                                     # install with no network. Empty = the published catalogue.
# MEMORY_LIMIT  = "0"                 # soft ceiling, e.g. "4GB": Go collects harder - and sheds its
#                                     # caches - rather than growing past it. "0" = no ceiling.
#                                     # Android defaults to a fraction of the device's RAM.
# COMPRESSION = "zstd"                # how article text is stored in dictionaries indexed after the change:
#                                     # "zstd", "deflate" (1.3 to 3 times as large) or "none" (2 to 7 times);
#                                     # a level may follow: "zstd-3", "deflate-6"
# ALLOW_REMOTE_DELETE = "0"           # may a browser on ANOTHER machine delete a dictionary?
#                                     # Deleting from this machine is always allowed. "1" = a browser
#                                     # on the LAN may too; only meaningful when SERVER_IP is not
#                                     # 127.0.0.1, and there is no undo.
# USE_CACHED  = "0"                   # "1" = also list previously imported dictionaries kept in DB_DIR
#                                     #       (set from the setup page: "Use these dictionaries")
# BROWSER_EXTENSIONS = []             # which browser extensions may read this server from a web page
#                                     # they run in. Blank = any installed extension may look words up
#                                     # (it can read dictionaries and nothing else - never your
#                                     # settings or library). List origins to allow only those:
#                                     # ["chrome-extension://abcdefghijklmnopabcdefghijklmnop"]
# WEB_ORIGINS = []                    # which WEB PAGES may read this server with JavaScript.
#                                     # Blank = none, and that is the safe default: a page you
#                                     # visit cannot read your dictionaries. List the exact
#                                     # origins of your own pages to let them (scheme, host and
#                                     # port, no path):
#                                     # ["http://localhost:3000", "https://notes.example.com"]
#                                     # They reach the same three read-only endpoints an
#                                     # extension does - never your settings or library.
#                                     # "*" allows every site you visit; convenient while
#                                     # developing, a standing invitation otherwise.
# TRUSTED_HOSTS = []                  # which HOST NAMES this server answers to, on top of
#                                     # "localhost" and a SERVER_IP that is a name. An address
#                                     # always works, so you need this only when something
#                                     # reaches wudict through a name - a reverse proxy, a
#                                     # hostname you gave the machine:
#                                     # ["wudict.lan"]
#                                     # A request naming anything else is refused, which is what
#                                     # stops a web page from re-pointing its own domain at your
#                                     # machine and reading the server through it. "*" turns the
#                                     # check off.
# AUTH        = "auto"                # must a request carry the access key?  "auto" = only when this
#                                     # server listens on the network (SERVER_IP is not 127.0.0.1),
#                                     # "on" = always, "off" = never. The key is a random string in
#                                     # ~/.wudict/token: "wudict token" prints the link that carries
#                                     # it, "wudict token --rotate" replaces it.

# ---- Advanced: safety limits ----------------------------------------------
# Ceilings that stop a damaged or malicious file from making wudict use
# unlimited memory or disk. Real dictionaries stay far below each of them.
# Change one only when a dictionary you trust fails with "exceeds the ... limit":
# a higher limit lets a bad file use that much memory or disk, and a limit
# below what your dictionaries need stops them from opening. Each key keeps to
# a range, so a typo cannot disable a guard or break your dictionaries: a value
# outside it is moved to the nearest end (16MB to 4GB for the unpacked blocks,
# 8MB for Babylon; lemma data 16MB to 1GB; imports and uploads 1GB to 64TB;
# the ratio 100 to 100000), and one that is not a positive size leaves the
# default. Startup names any value that was changed.
# MAX_ZIM_CLUSTER    = "256MB"        # one ZIM cluster, unpacked
# MAX_SLOB_BIN       = "256MB"        # one Aard 2 (.slob) bin, unpacked
# MAX_MDICT_BLOCK    = "256MB"        # one MDict (.mdx/.mdd) block, unpacked
# MAX_STARDICT_INDEX = "256MB"        # a compressed StarDict index (.idx.gz), unpacked;
#                                     # 16 times the file's own size is always allowed
# MAX_BGL_BLOCK      = "64MB"         # one Babylon (.bgl) block
# MAX_LEMMA_SIZE     = "64MB"         # one lemma data file, unpacked
# MAX_IMPORT_SIZE    = "256GB"        # one dictionary taken out of an archive, or downloaded
# MAX_IMPORT_RATIO   = "2000"         # how many times larger than its packed size one file
#                                     # in an archive may unpack
# MAX_UPLOAD_SIZE    = "16GB"         # one archive sent to the server from a browser or the app
`

// EnsureConfigFile makes sure a config file exists, generating the fully
// commented template on first run. It writes exactly one place - HomeConfig -
// and never beside the executable.
//
// Not the executable's directory, even when it is writable: writability does
// not mean "this directory is ours". `go install` lands in ~/go/bin and
// Homebrew in /opt/homebrew/bin - both user-writable, both shared with every
// other program on the machine - and guessing harder (matching /opt, /usr, …)
// only lengthens a denylist that is incomplete by construction.
//
// So portable mode is something the user DECLARES, by putting a wudict.toml
// next to the binary. When they have,
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
	if err := fsx.WriteAtomic(p, []byte(template(dirs(), homeDir())), 0o644); err != nil {
		return "", false, err
	}
	return p, true, nil
}

// template is configTemplate naming the folders of layout d, home written
// as ~. configTemplate is written for the ~/.wudict layout, which every
// platform but the XDG ones keeps: there it is used as written (on Windows a
// rewrite would also turn its slashes into backslashes).
func template(d basedir.Dirs, home string) string {
	if !basedir.XDG(goos) {
		return configTemplate
	}
	short := func(dir, name string) string { return basedir.Short(filepath.Join(dir, name), home) }
	return strings.NewReplacer(
		"~/.wudict/wudict.toml", short(d.Config, Name),
		"~/.wudict/db", short(d.Data, "db"),
		"~/.wudict/lemmas", short(d.Data, "lemmas"),
		"~/.wudict/token", short(d.State, "token"),
	).Replace(configTemplate)
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
//
// The whole read-modify-write runs under saveMu: the pages save per key, and
// two overlapping saves that each read the same file would let the later
// write quietly discard the earlier one's key.
func SaveKeyRaw(path, key, raw string) error {
	saveMu.Lock()
	defer saveMu.Unlock()
	data, err := fsx.ReadBounded(path, maxConfigBytes)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
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
	return fsx.WriteAtomic(path, []byte(s), 0o644)
}

// saveMu serialises SaveKeyRaw's read-modify-write (the write itself is
// atomic regardless); writeFileAtomic does not take it, because a nested
// Lock would deadlock the one caller that already holds it.
var saveMu sync.Mutex

// wudict.toml holds nothing but flat `KEY = value` lines, so this package
// reads it itself rather than taking a TOML dependency. The one thing such a
// reader must get right is quoting, because the character TOML gives a meaning
// to - the backslash - is the character a Windows path is spelled with: a
// reader that only stripped the quotes would read "C:\\Users\\me" (how the
// writer, correctly, escapes C:\Users\me) back with both backslashes doubled.

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
// knownKeys are the settings wudict reads: every key configTemplate
// documents (a test holds Load to that), AUTH_TOKEN, which is meant for the
// environment and so is kept out of the template, and NO_COMPRESS, which
// COMPRESSION replaced (D191) and which is still read so that resolveCompression
// can report it.
var knownKeys = func() map[string]bool {
	keys := map[string]bool{"AUTH_TOKEN": true, "NO_COMPRESS": true}
	for _, line := range strings.Split(configTemplate, "\n") {
		k, _, ok := strings.Cut(strings.TrimLeft(line, "# "), "=")
		if k = strings.TrimSpace(k); ok && k != "" && strings.ToUpper(k) == k && !strings.ContainsAny(k, " ()") {
			keys[k] = true
		}
	}
	return keys
}()

// checkTOML lists the lines of a config file that set nothing (Config.Problems),
// reading lines the way parseTOML does.
func checkTOML(s string) []string {
	var out []string
	lines := strings.Split(s, "\n")
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(strings.TrimSuffix(lines[i], "\r"))
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "[") {
			continue
		}
		at := fmt.Sprintf("line %d: ", i+1)
		k, v, ok := strings.Cut(line, "=")
		k = strings.TrimSpace(k)
		switch {
		case !ok:
			out = append(out, at+"not KEY = value: "+clipLine(line))
			continue
		case !knownKeys[k]:
			out = append(out, at+"unknown setting "+clipLine(k))
		}
		raw, depth := scanValue(strings.TrimSpace(v), 0)
		start := i
		for depth > 0 && i+1 < len(lines) {
			i++
			_, depth = scanValue(strings.TrimSpace(strings.TrimSuffix(lines[i], "\r")), depth)
		}
		switch {
		case depth > 0:
			out = append(out, fmt.Sprintf("line %d: the list for %s is not closed with ]", start+1, k))
		case open(raw, '"'), open(raw, '\''):
			out = append(out, at+"the value of "+k+" is not closed with its quote")
		}
	}
	return out
}

// open reports a value that starts with quote q and does not end with it.
func open(raw string, q byte) bool {
	return len(raw) > 0 && raw[0] == q && (len(raw) == 1 || raw[len(raw)-1] != q)
}

func clipLine(s string) string {
	if r := []rune(s); len(r) > 60 {
		return string(r[:60]) + "…"
	}
	return s
}

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

// ParseOrigins reads the two origin lists - BROWSER_EXTENSIONS and WEB_ORIGINS -
// written as a TOML array or separated by commas, semicolons or whitespace.
//
//	BROWSER_EXTENSIONS = ["chrome-extension://abc…", "moz-extension://1f2e…"]
//	BROWSER_EXTENSIONS=chrome-extension://abc…,moz-extension://1f2e…
//	WEB_ORIGINS = ["http://localhost:3000", "https://notes.example.com"]
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

// ExpandHome expands a leading ~ to the user home directory. "~\" counts too
// on Windows, where it is how a path is written - and how the panel itself
// shows one (pathAliases); elsewhere a backslash is part of a file name.
func ExpandHome(p string) string {
	rest, ok := strings.CutPrefix(p, "~")
	if !ok || (rest != "" && rest[0] != '/' && rest[0] != filepath.Separator) {
		return p
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, rest[min(len(rest), 1):])
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

// COMPRESSION codecs (D191): how article text is stored when a dictionary is
// indexed. Reading understands every codec and level whatever the setting.
const (
	CodecZstd    = "zstd"
	CodecDeflate = "deflate"
	CodecNone    = "none"
)

// Compression is a resolved COMPRESSION: a codec and its own level, which is 0
// only for CodecNone.
type Compression struct {
	Codec string
	Level int
}

// String is the spelling COMPRESSION accepts back: "zstd-1", "deflate-6",
// "none".
func (c Compression) String() string {
	if c.Codec == CodecNone {
		return CodecNone
	}
	return c.Codec + "-" + strconv.Itoa(c.Level)
}

// DefaultCompression is what an unset COMPRESSION means: zstd at its fastest
// level, on every platform (D191).
func DefaultCompression() Compression { return Compression{Codec: CodecZstd, Level: 1} }

// ParseCompression reads a COMPRESSION value: a codec, optionally followed by
// "-" and the codec's own level. A bare codec takes its default level: zstd 1,
// deflate the platform's (deflateLevel). A zstd level resolves to the tier the
// encoder actually has (1-2, 3-5, 6-9, 10 and up), so String names what runs.
// ok is false for anything else, the level spellings "fast" and "small"
// included: resolveCompression reports those.
func ParseCompression(v string) (Compression, bool) {
	v = strings.ToLower(strings.TrimSpace(v))
	codec, lvl, hasLevel := strings.Cut(v, "-")
	level := 0
	if hasLevel {
		n, err := strconv.Atoi(lvl)
		if err != nil {
			return Compression{}, false
		}
		level = n
	}
	switch codec {
	case CodecNone:
		if hasLevel {
			return Compression{}, false
		}
		return Compression{Codec: CodecNone}, true
	case CodecDeflate:
		if !hasLevel {
			level = deflateLevel()
		}
		if level < 1 || level > 9 {
			return Compression{}, false
		}
		return Compression{Codec: CodecDeflate, Level: level}, true
	case CodecZstd:
		if !hasLevel {
			level = 1
		}
		if level < 1 || level > 22 {
			return Compression{}, false
		}
		switch {
		case level < 3:
			level = 1
		case level < 6:
			level = 3
		case level < 10:
			level = 6
		default:
			level = 10
		}
		return Compression{Codec: CodecZstd, Level: level}, true
	}
	return Compression{}, false
}

// originRank orders the layers: a higher layer wins.
func originRank(o string) int {
	switch o {
	case OriginFlag:
		return 3
	case OriginEnv:
		return 2
	case OriginFile:
		return 1
	}
	return 0
}

// resolveCompression reads COMPRESSION and NO_COMPRESS, the key it replaced.
// Between a codec and NO_COMPRESS = "1" the higher layer wins, and at one
// layer the codec does. The level spellings "fast" and "small" never beat
// NO_COMPRESS = "1", at any layer: they named how hard to compress, and
// NO_COMPRESS took precedence over them, so a configuration that set both
// keeps storing text uncompressed. Every earlier spelling, and a value
// that is not a compression, is a Problem: the setting is reported, never
// fatal, because a mistyped value must not stop the server (on Android it
// arrives from the settings screen).
func resolveCompression(cfg *Config, get func(string) string) {
	cv := get("COMPRESSION")
	co := cfg.Origins["COMPRESSION"]
	nv := get("NO_COMPRESS")
	no := cfg.Origins["NO_COMPRESS"]
	def := DefaultCompression()

	have, legacy := false, false
	cat := fmt.Sprintf("COMPRESSION = %q (%s)", cv, co)
	if cv != "" {
		switch l := strings.ToLower(strings.TrimSpace(cv)); l {
		case "fast", "small":
			cfg.Compression, have, legacy = def, true, true
		default:
			if c, ok := ParseCompression(cv); ok {
				cfg.Compression, have = c, true
			} else {
				cfg.Origins["COMPRESSION"] = OriginDefault
				cfg.Problems = append(cfg.Problems, fmt.Sprintf("%s is not a compression: the default %s applies. Write zstd, deflate or none, optionally with a level: zstd-3, deflate-6", cat, def))
			}
		}
	}
	nat := fmt.Sprintf("NO_COMPRESS = %q (%s)", nv, no)
	noWins := nv != "" && !isOff(nv) && (!have || legacy || originRank(no) > originRank(co))
	if legacy {
		if noWins {
			cfg.Problems = append(cfg.Problems, fmt.Sprintf("%s is an earlier spelling and has no effect: %s applies", cat, nat))
		} else {
			cfg.Problems = append(cfg.Problems, fmt.Sprintf("%s is an earlier spelling: %s applies. Write COMPRESSION = %q", cat, def, def.Codec))
		}
	}
	switch {
	case nv == "":
	case isOff(nv):
		cfg.Problems = append(cfg.Problems, nat+" is an earlier setting and has no effect. Delete it")
	case noWins:
		cfg.Compression = Compression{Codec: CodecNone}
		cfg.Origins["COMPRESSION"] = no
		cfg.Problems = append(cfg.Problems, nat+` is an earlier setting: none applies. Write COMPRESSION = "none"`)
	default:
		cfg.Problems = append(cfg.Problems, fmt.Sprintf("%s is an earlier setting and has no effect: %s applies", nat, cat))
	}
}

// IMPORT_KEEP values: what becomes of the archive an import came from.
const (
	ImportKeepAsk    = "ask"
	ImportKeepYes    = "keep"
	ImportKeepDelete = "delete"
)

// normalizeImportKeep maps what a user may have written onto the three
// answers. Anything unrecognised means "ask", which is the answer that cannot
// destroy a file by being a typo.
func normalizeImportKeep(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "keep", "1", "true", "yes", "y", "on":
		return ImportKeepYes
	case "delete", "remove", "0", "false", "no", "n", "off":
		return ImportKeepDelete
	default:
		return ImportKeepAsk
	}
}

// ParseHostList splits a host list into the spelling the matcher compares
// against: lower-cased, trimmed, no empty entries, and with any leading dot or
// "*." removed - both are how a person writes "and its subdomains", which is
// what a bare host already means here. It accepts a comma-separated string
// (the environment, or a TOML string) and the TOML array wudict.toml spells
// lists with, which reaches here as its raw text, brackets and quotes
// included - as ParseOrigins does for the origin lists.
func ParseHostList(v string) []string {
	v = strings.TrimSpace(v)
	if strings.HasPrefix(v, "[") {
		v = strings.Trim(v, "[]")
	}
	var out []string
	for _, f := range strings.FieldsFunc(v, func(r rune) bool {
		return r == ',' || r == ';' || r == ' ' || r == '\t' || r == '\n' || r == '\r'
	}) {
		h := strings.ToLower(strings.Trim(f, "\"'`"))
		h = strings.TrimPrefix(h, "*.")
		h = strings.Trim(h, ".")
		// A pasted origin rather than a host: take the host out of it instead
		// of refusing, since the two are indistinguishable to the person
		// typing and one of them is what they meant.
		if i := strings.Index(h, "://"); i >= 0 {
			h = h[i+3:]
		}
		if i := strings.IndexAny(h, "/:"); i >= 0 {
			h = h[:i]
		}
		if h != "" {
			out = append(out, h)
		}
	}
	return out
}

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

// ParseCount reads a small non-negative count. Anything unreadable is 0, which
// for MORPH_CACHE means "off" - a typo in a config file disables a feature
// rather than silently choosing a size nobody asked for.
func ParseCount(v string) int {
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// ParseSize reads a byte size: plain bytes, or with a K/M/G/T suffix
// ("2GB", "1500MB", "512M", "64TB"). Returns 0 for "off"/"none"/unparseable.
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
	case strings.HasSuffix(v, "t"):
		mult, v = 1<<40, strings.TrimSuffix(v, "t")
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || math.IsNaN(f) || f <= 0 {
		return 0
	}
	// "1e30GB" and "inf" are past what int64 holds, and a float conversion
	// past it is undefined - in practice negative, which a caller would read
	// as "no limit". Saturate instead.
	if b := f * float64(mult); b < math.MaxInt64 {
		return int64(b)
	}
	return math.MaxInt64
}

// Setting is one resolved config key: the value that is actually in effect,
// and which layer supplied it.
type Setting struct {
	Value  string `json:"value"`
	Origin string `json:"origin"`
}

// Tunables are the keys a platform shell may override for the device it is
// running on (D101): storage headroom, RAM, cores, and where the server
// listens. They are reported by /api/config so a shell can show what is
// actually in effect - including the defaults this package computes from the
// device itself (tuning.go), which nothing outside Go should ever recompute.
var Tunables = []string{
	"COMPRESSION",
	"SEARCH_MEMORY",
	"PREVIEW_MEMORY",
	"MEMORY_LIMIT",
	"INDEX_WORKERS",
	"SERVER_IP",
	"SERVER_PORT",
}

// Effective reports the resolved value and origin of every key in Tunables.
// The values are the parsed ones, re-rendered in the spelling the config
// accepts back, so what a caller reads is what it would have to write to get
// the same result.
func (c Config) Effective() map[string]Setting {
	out := make(map[string]Setting, len(Tunables))
	val := func(key string) string {
		switch key {
		case "COMPRESSION":
			return c.Compression.String()
		case "SEARCH_MEMORY":
			return FormatSize(c.SearchMemory)
		case "PREVIEW_MEMORY":
			return FormatSize(c.PreviewMemory)
		case "MEMORY_LIMIT":
			return FormatSize(c.MemoryLimit)
		case "INDEX_WORKERS":
			return strconv.Itoa(c.IndexWorkers)
		case "SERVER_IP":
			return c.IP
		case "SERVER_PORT":
			return c.Port
		}
		return ""
	}
	for _, key := range Tunables {
		out[key] = Setting{Value: val(key), Origin: c.Origin(key)}
	}
	return out
}

// FormatSize renders a byte count the way ParseSize reads one, using the
// largest unit that divides it exactly so a value set as "1GB" comes back as
// "1GB" and not "1024MB". Zero is "0", which both keys define as "no limit".
func FormatSize(n int64) string { return limits.Format(n) }

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

// Addr returns the listen address. JoinHostPort, not concatenation: an IPv6
// literal has to be bracketed, and "::1" + ":" + "6888" is ":::6888", which no
// listener parses.
func (c Config) Addr() string { return net.JoinHostPort(c.IP, c.Port) }

// DefaultLemmaURL is the catalogue `wudict lemmas` installs languages from
// (LEMMA_URL). It is a static file, deliberately: enumerating a repository
// through a code-hosting API would put a 60-request-per-hour, per-IP rate
// limit in front of `wudict lemmas list`, and a user sharing an address with
// their office or their carrier would see it fail for no reason they could
// see. Point it at a local manifest.json to install with no network at all.
//
// The host is a repository holding nothing but the catalogue - not the golem
// fork, whose name would credit a lemmatizer library for data it did not
// produce, and not a mirror of michmech/lemmatization-lists either. What is
// published here is DERIVED from those lists (grouped, deduplicated, gzipped)
// and carries digests and measured memory costs the raw lists do not have;
// `tools/lemmafiles` is the only thing that ever reads michmech.
const DefaultLemmaURL = "https://raw.githubusercontent.com/wuweidict/lemmas/main/manifest.json"

// isOff reads a boolean written the way a person writes one in a config file.
// Every boolean key goes through it, so there is exactly one answer to "does
// this value mean no" in the whole configuration.
//
// It recognises the NEGATIVES rather than the positives because the question
// is only ever asked about a value that was actually set: an unset key keeps
// its default and never reaches here, so "set at all" already means yes and
// the only thing left to detect is a user saying no.
func isOff(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "0", "false", "off", "no", "n":
		return true
	}
	return false
}
