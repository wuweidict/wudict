// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

// wudict CLI - Phase 1 surface: inspect and query dictionaries via
// the direct backends. The HTTP server arrives in a later phase.
package cli

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	neturl "net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/wuweidict/wudict/internal/config"
	"github.com/wuweidict/wudict/internal/dict"
	"github.com/wuweidict/wudict/internal/fsx"
	"github.com/wuweidict/wudict/internal/howto"
	"github.com/wuweidict/wudict/internal/htmlref"
	"github.com/wuweidict/wudict/internal/intake"
	"github.com/wuweidict/wudict/internal/limits"
	"github.com/wuweidict/wudict/internal/logx"
	"github.com/wuweidict/wudict/internal/morph"
	"github.com/wuweidict/wudict/internal/search"
	"github.com/wuweidict/wudict/internal/server"
	"github.com/wuweidict/wudict/internal/speex"
	"github.com/wuweidict/wudict/internal/store"
	"github.com/wuweidict/wudict/internal/tray"
	"github.com/wuweidict/wudict/internal/wildcard"

	_ "github.com/wuweidict/wudict/internal/format/bgl"      // register .bgl
	_ "github.com/wuweidict/wudict/internal/format/dsl"      // register .dsl(.dz)
	_ "github.com/wuweidict/wudict/internal/format/mdx"      // register .mdx
	_ "github.com/wuweidict/wudict/internal/format/slob"     // register .slob
	_ "github.com/wuweidict/wudict/internal/format/stardict" // register .ifo
	_ "github.com/wuweidict/wudict/internal/format/wmd"      // register .wudict.md (+ .md with a wudict: field)
	_ "github.com/wuweidict/wudict/internal/format/zim"      // register .zim
)

// Version is stamped by the Makefile via
// -ldflags "-X github.com/wuweidict/wudict/internal/cli.Version=…".
// It lives here rather than in main because this package, not the one-line
// shim at the module root, is where the program is.
var Version = "dev"

// Notices is THIRD-PARTY-NOTICES.md, handed over by the root package - the
// only one that can embed a file at the module root. A release is a bare
// executable with nothing beside it, and several of the licences in that file
// require their text to travel with a BINARY, so `wudict licenses` carries it.
// Empty only in a build that skipped the shim (a test binary, say).
var Notices string

// Product identity, in one place. The binary is `wudict`; the name people
// read is WuWeiDict. Anything user-facing - CLI banner, web UI
// About box, setup page - sources its wording from here or mirrors it.
const (
	ProductName = "wuDict"
	Tagline     = "Search every dictionary you own, from one browser tab."
	SiteURL     = "https://wudict.legbehindneck.com"
	RepoURL     = "https://github.com/wuweidict/wudict"
)

func usage() string {
	return fmt.Sprintf(`WuWeiDict %s - dictionary server for the browser
MDict (.mdx/.mdd) · StarDict (.ifo) · Aard 2 (.slob) · Lingvo DSL (.dsl/.dsl.dz) · Babylon (.bgl) · ZIM (.zim, split .zimaa…) · wudict markdown (.wudict.md) · wudict library (text.db)

USAGE
  wudict [command] [flags] [args]

  With no command, or with flags only, wudict starts the server ("serve").

  wudict <dictfile>                       Serve the folder of that file and open it
                                          in the browser; this is what opening a
                                          dictionary file with wudict does.

COMMANDS
  serve                                   Start the server (the default command)
  list   <dir> [dir…]                     List the dictionaries in the folders
  info   <dictfile>                       Show a dictionary's metadata, search modes,
                                          language, and its files with their sizes
  lookup   [-n max] <dictfile> <word>     Exact search; case and accents ignored,
                                          the word as typed first
  prefix   [-n max] <dictfile> <word>     The word, then the headwords it starts;
                                          case and accents ignored
  fts      [-n max] <dictfile> <query>    Full-text query, read as in the app
                                          (full-text index)
  wildcard [-n max] <dictfile> <pattern>  Headwords that fit <pattern>: ? is one
                                          character, * any number; a capital letter
                                          or an accent puts the headwords that have
                                          it first. Quote the pattern: 'b???d'
                                          All four print to standard output and take
                                          -format raw (the dictionary's HTML; default),
                                          clean (no scripts, stylesheets or
                                          presentation) or text (no markup). fts and
                                          wildcard need an indexed dictionary: pass
                                          its library folder.
  keys   [-offset N] [-n count] <dictfile>  List headwords, all by default; for an
                                          .mdd, the files it contains
  res    [-o out] [-f] [-dry-run] <dictfile> <name|pattern>…
                                          Extract resources. One name, e.g.
                                          "audio/word.mp3" (as keys prints it; an
                                          .mdd works directly): piped or redirected,
                                          to standard output; on a terminal, to a
                                          file named after the resource (-f
                                          overwrites); -o: a file (missing folders
                                          are created), a folder, or "-" for
                                          standard output.
                                          A pattern or several names: files in the
                                          -o folder (default: the current folder),
                                          in the resources' own folders. * is any
                                          characters in one name, ? one character,
                                          ** any number of folders; 'audio/' is
                                          everything in audio; '*.ogg' is every
                                          .ogg in any folder. Quote the pattern.
                                          If a file exists, nothing is written (-f
                                          overwrites). -dry-run: print each name
                                          and its file, write nothing.
  dump   [-format csv|md] [-mode html|clean] [-compress gz] [-resources all|text|none]
         [-include pattern]… [-exclude pattern]… [-articles=false] -o <outdir> <dictfile>
                                          Export a dictionary. csv (default):
                                          pyglossary's CSV layout, resources in
                                          <name>.csv_res. md: wudict markdown,
                                          <name>.wudict.md, resources in
                                          <name>.wudict.files; -mode html (default)
                                          keeps each article's HTML, -mode clean
                                          keeps what markdown expresses and stops at
                                          the first article it cannot convert;
                                          -compress gz writes <name>.wudict.md.gz.
                                          -resources text: text files only (.css,
                                          .js, …); none: no resources.
                                          -include: only the resources that match a
                                          pattern (as res reads it); -exclude: not
                                          those. Both repeat; every filter narrows.
                                          Quote the pattern. -articles=false: the
                                          resources only, in the same folder.
                                          -output is the long form of -o.
  ingest [-full] [-fulltext] [<dictfile|folder…>]
                                          Index dictionaries into the library:
                                          <DB_DIR>/<name>/text.db and info.txt.
                                          No path: the dictionary folders (DICT_DIR).
                                          Indexed dictionaries are skipped. A new
                                          dictionary gets the headword index, as in
                                          the app; -fulltext adds the full-text
                                          index, -full also the media pack
                                          (media.db). -fulltext=false (-headwords)
                                          removes the full-text index. A flag left
                                          out keeps what a dictionary has.
  searchall [-mode m] [-n perDict] [-format f] [<dir>] <term>
                                          Search every dictionary in the folders and
                                          print each one's results as it answers.
                                          Default: DICT_DIR; <dir> may be an "a:b"
                                          list. -mode exact (default), prefix, fts
                                          or wildcard. -format text
                                          (default), clean, raw, or list (headwords
                                          only).
  lemmas list | download <lang…> | remove <lang…>
                                          Lemma data, so that an inflected form finds
                                          its lemma ("knew" -> "know"). English is
                                          built in. list: installed and available
                                          languages; "download ru sk" or "download
                                          russian slovak": install from LEMMA_URL;
                                          "remove sk": delete.
  token [-rotate]                         Print the link that carries the access key,
                                          for a browser on another device. -rotate
                                          replaces the key; browsers and scripts
                                          with the old key then need the new link.
  clean  [-f [-orphans]]                  List broken library folders (incomplete,
                                          unreadable, interrupted indexing, an older
                                          layout) and orphans (library folders whose
                                          dictionary files are gone). -f deletes the
                                          broken ones; -f -orphans also the orphans.
                                          A dictionary moved within the dictionary
                                          folders is linked to its new place; one
                                          kept with -keep-index is not listed.
  rm [-f] [-keep-source|-keep-index] <name|path>
                                          Remove a dictionary: its library folder
                                          and its dictionary files. <name|path>: a
                                          library name or folder, a text.db, or a
                                          dictionary file. -keep-source deletes only
                                          the library folder (indexed again on the
                                          next search if the files are still in a
                                          dictionary folder); -keep-index only the
                                          dictionary files, refused while media is
                                          not in a media pack. Without -f, rm lists
                                          what it would delete.
  reindex [-all] [<name|path>…]           Rebuild outdated indexes: built by an older
                                          version of wudict, from dictionary files
                                          changed since, or unreadable. Indexes and
                                          media pack are kept. Names limit it to those
                                          dictionaries; -all rebuilds current ones
                                          too. A dictionary whose files are gone is
                                          reported and kept. While a server uses the
                                          library, the server runs the rebuild.
  licenses                                Print the licence and third-party notices

SETTINGS
  Each setting is a flag, an environment variable, or a wudict.toml key;
  "(env/toml only)" marks those without a flag.

  --dict-dir     <path>   Folder with dictionary files, subfolders included.
                          Repeat the flag for several folders:
                            --dict-dir ~/Dictionaries --dict-dir /Volumes/Ext/Dicts
                          env: DICT_DIR       toml: DICT_DIR
                            DICT_DIR="~/Dictionaries:/Volumes/Ext/Dicts"   (";" on Windows)
                            DICT_DIR = ["~/Dictionaries", "/Volumes/Ext/Dicts"]
                          A dictionary in two folders is listed once, from the
                          first; a missing folder is reported.
                          default: ~/Dictionaries

  --db-dir       <path>   The library: one library folder per indexed dictionary
                          (text.db, media.db, info.txt). Must not be a
                          dictionary folder.
                          env: DB_DIR         toml: DB_DIR
                          default: ~/.wudict/db

  (env/toml only)
  AUTO_INDEX = "on"       Index each dictionary's headwords in the background on
                          its first search. "off": dictionaries stay not indexed
                          and are searched through their own format. Full-text
                          and media pack are per-dictionary choices either
                          way.
                          env: AUTO_INDEX     toml: AUTO_INDEX
                          default: on

  (env/toml only)
  IMPORT_KEEP = "ask"     What happens to an archive after its dictionaries are
                          installed: "ask" each time, "keep", or "delete". A
                          failed import keeps it.
                          env: IMPORT_KEEP    toml: IMPORT_KEEP
                          default: ask

  (env/toml only)
  IMPORT_URL_HOSTS        Hosts an archive may be downloaded from: an array in
                          wudict.toml, comma-separated in the environment; a
                          host matches its subdomains too. Set it
                          when the server listens on the network, or a client
                          there can make this machine download from any address
                          it reaches.
                          env: IMPORT_URL_HOSTS  toml: IMPORT_URL_HOSTS
                          default: any host

  (env/toml only)
  IMPORT_INSECURE = "0"   "1": also plain http:// links and local-network hosts.
                          env: IMPORT_INSECURE  toml: IMPORT_INSECURE
                          default: 0 (https only, public hosts only)

  --index-workers <n>     How many dictionaries are indexed at once. Each uses
                          a CPU core and a few hundred bytes per headword.
                          "auto" (or 0): one per CPU core.
                          env: INDEX_WORKERS  toml: INDEX_WORKERS
                          default: 1

  (env/toml only)
  PREVIEW_MEMORY = "1GB"  Memory that dictionaries not yet indexed may hold open,
                          about 350 bytes per headword. Above it, the least
                          recently used are closed; indexed dictionaries do not
                          count. "0": no limit.
                          env: PREVIEW_MEMORY toml: PREVIEW_MEMORY
                          default: 1GB (Android: a third of MEMORY_LIMIT)

  SEARCH_MEMORY = "512MB" Memory one search may use to open dictionaries that are
                          not indexed. Past it, the rest are reported as not
                          searched. Indexed dictionaries and a search of one
                          dictionary are not limited. "0": no limit.
                          env: SEARCH_MEMORY  toml: SEARCH_MEMORY
                          default: none (Android: MEMORY_LIMIT)

  MORPH_CACHE = "2"       How many languages of lemma data stay in memory. A
                          single-word search that finds nothing in a dictionary
                          is retried there with the lemma ("knew" -> "know"),
                          using lemma data for the dictionary's language. A
                          language uses 1-90 MB. "0": lemmatization off.
                          env: MORPH_CACHE    toml: MORPH_CACHE
                          default: 2 (Android: 1)

  LEMMA_DIR = "~/.wudict/lemmas"
                          Installed lemma data; English is built in. One file
                          per language, named by code or English name: "pl.txt",
                          "pol.tsv", "polish.txt.gz". Each line is a lemma, then
                          its forms, tab-separated, in lower case, as in
                          github.com/michmech/lemmatization-lists. An "en" file
                          replaces the built-in English. Read at startup, and
                          after each install or removal on the Lemmatization
                          page.
                          env: LEMMA_DIR      toml: LEMMA_DIR
                          default: ~/.wudict/lemmas

  LEMMA_URL = "https://…/manifest.json"
                          The lemma data catalogue: languages, sizes, SHA-256
                          digests. A path to a local manifest.json installs
                          without a network.
                          env: LEMMA_URL      toml: LEMMA_URL
                          default: the published catalogue

  MEMORY_LIMIT = "4GB"    Soft memory limit: near it, Go collects garbage more
                          often and wudict drops its caches. "0": none.
                          env: MEMORY_LIMIT   toml: MEMORY_LIMIT
                          default: none (Android: 1/16 of RAM, 192-384 MiB)

  --no-compress           Store article text uncompressed: the library is about
                          3 times larger; reads are slightly faster.
                          env: NO_COMPRESS    toml: NO_COMPRESS
                          default: off (article text is DEFLATE-compressed)

  COMPRESSION = "small"   How hard article text is compressed when a dictionary
                          is indexed. "fast": the quickest indexing. "small": a
                          library 1-2%% smaller; indexing takes up to 1.5 times
                          as long. Applies to dictionaries indexed after the
                          change. NO_COMPRESS takes precedence.
                          env: COMPRESSION    toml: COMPRESSION
                          default: fast

  --use-cached            Also list indexed dictionaries whose dictionary files
                          are gone. "Use these dictionaries" on the setup page
                          sets it.
                          env: USE_CACHED     toml: USE_CACHED
                          default: off

  (env/toml only)
  BROWSER_EXTENSIONS      Browser extensions allowed to call the server. Blank:
                          any installed extension. Extensions reach /api/dicts,
                          /api/search and /res/ only.
                            BROWSER_EXTENSIONS = ["chrome-extension://abc…"]
                          Firefox assigns a new moz-extension:// origin to each
                          installation, so a Firefox extension cannot be listed.
                          env: BROWSER_EXTENSIONS  toml: BROWSER_EXTENSIONS
                          default: any extension

  WEB_ORIGINS             Web page origins allowed to call the server from
                          JavaScript: scheme, host and port, no path. They reach
                          the same three endpoints as extensions.
                            WEB_ORIGINS = ["http://localhost:3000"]
                          "*": every site you visit can read your dictionaries;
                          for development only. Programs outside a browser are
                          not affected.
                          env: WEB_ORIGINS    toml: WEB_ORIGINS
                          default: none

  TRUSTED_HOSTS           Host names the server answers to besides "localhost"
                          and a SERVER_IP that is a name; IP addresses are
                          always accepted. Needed behind a
                          reverse proxy or for a host name you gave the machine:
                            TRUSTED_HOSTS = ["wudict.lan"]
                          Other names are refused, which blocks DNS rebinding.
                          "*" turns the check off.
                          env: TRUSTED_HOSTS  toml: TRUSTED_HOSTS
                          default: IP addresses and localhost

  AUTH                    Whether a request needs the access key. "auto": when
                          the server listens on a network address (not
                          127.0.0.1); "on": always; "off": never. "off" on a
                          network address gives anyone who can reach the port
                          your library, settings and folder names.
                          The key is in ~/.wudict/token, readable only by you;
                          "wudict token" prints the link that carries it.
                          env: AUTH           toml: AUTH
                          default: auto

  AUTH_TOKEN              The access key from elsewhere: a password manager, a
                          container secret. Overrides ~/.wudict/token and is not
                          written to it.
                          env: AUTH_TOKEN     toml: AUTH_TOKEN
                          default: the contents of ~/.wudict/token

  --allow-remote-delete <0|1>
                          Allow clients on other hosts to delete dictionaries.
                          Requests from this machine can always delete.
                          env: ALLOW_REMOTE_DELETE  toml: ALLOW_REMOTE_DELETE
                          default: 0

  --ip           <addr>   Listen address; 0.0.0.0 for the network (AUTH applies)
                          env: SERVER_IP      toml: SERVER_IP
                          default: 127.0.0.1

  --port         <port>   Listen port
                          env: SERVER_PORT    toml: SERVER_PORT
                          default: 6888

  --config       <path>   The wudict.toml to read, instead of the search order
                          env: CONFIG_PATH

  --no-browser            Do not open a browser tab at startup
                          env/toml: NO_BROWSER=1

  --tray, --no-tray       Tray icon (Windows) or menu-bar icon (macOS): always,
                          or never. Default: only when started from the desktop.
                          env/toml: TRAY=1 or TRAY=0

  --verbose               Log requests, dictionary opens, indexing and audio
                          conversion; applies to every command
                          env/toml: VERBOSE=1

  --speexdec     <path>   The speexdec program, which converts Speex (.spx)
                          audio to WAV in -purego builds
                          env: SPEEXDEC       toml: SPEEXDEC
                          default: speexdec on PATH, else /usr/bin/speexdec

  (env/toml only)
  SPEEX_BACKEND           "internal" (built-in decoder, -cgo builds) or
                          "external" (speexdec)
                          env: SPEEX_BACKEND  toml: SPEEX_BACKEND
                          default: internal

  -v, --version           Print the version
  -h, --help              Print this help

SAFETY LIMITS (advanced)
  Ceilings that stop a damaged or malicious file from making wudict use
  unlimited memory or disk. Real dictionaries stay far below each of them.
  Change one only when a dictionary you trust fails with "exceeds the ...
  limit": a higher limit lets a bad file use that much, and a limit below
  what your dictionaries need stops them from opening. A value outside a
  key's range is moved to the nearest end of it; one that is not a positive
  size leaves the default. Startup names any value that was changed.
  Each is a wudict.toml key, an environment variable and a flag of serve and
  ingest: MAX_ZIM_CLUSTER is also --max-zim-cluster.

                      default  range         bounds
  MAX_ZIM_CLUSTER     256MB  16MB-4GB      one ZIM cluster, unpacked
  MAX_SLOB_BIN        256MB  16MB-4GB      one Aard 2 (.slob) bin, unpacked
  MAX_MDICT_BLOCK     256MB  16MB-4GB      one MDict (.mdx/.mdd) block,
                                           unpacked
  MAX_STARDICT_INDEX  256MB  16MB-4GB      a compressed StarDict index
                                           (.idx.gz), unpacked; 16 times the
                                           file's size is always allowed
  MAX_BGL_BLOCK        64MB  8MB-4GB       one Babylon (.bgl) block
  MAX_LEMMA_SIZE       64MB  16MB-1GB      one lemma data file, unpacked
  MAX_IMPORT_SIZE     256GB  1GB-64TB      one dictionary taken out of an
                                           archive, or downloaded
  MAX_IMPORT_RATIO     2000  100-100000    how many times its packed size
                                           one file in an archive may unpack to
  MAX_UPLOAD_SIZE      16GB  1GB-64TB      one archive sent to the server from
                                           a browser or the app

CONFIG FILE
  The first found of:
  1. --config / CONFIG_PATH
  2. wudict.toml next to the executable (portable mode: takes precedence
     and receives the settings saved from the setup page; never created by
     wudict)
  3. ~/.wudict/wudict.toml (created, commented out, by the first "serve")
  4. /etc/wudict/wudict.toml
  Each start prints the file in effect.

  Priority, highest first: flag, environment variable, wudict.toml, default.

SETUP PAGE
  If the dictionary folder is missing or empty, the page links to the
  setup page, which sets the folders (saved to wudict.toml, no restart)
  and imports dictionaries from a file or a link.

LIBRARY
  Indexing creates <DB_DIR>/<name>/ with text.db, media.db (media pack)
  and info.txt: one library folder per dictionary, which can be copied
  to another machine.

  A file in its res/ subfolder overrides the dictionary's resource of
  the same path, or supplies a missing one:

    <DB_DIR>/Cambridge English Dictionary Online/res/jquery.js
    <DB_DIR>/Stanford Encyclopedia/res/js/entry.js

  The path is the one in the /res/<id>/<path> request in the browser's
  network panel. Overrides are served with no-cache. A .spx override is
  not converted to WAV. wudict logs a warning, with the res/ path, when
  it serves a .js/.css/.html/.json/.xml/.svg/.txt resource containing
  NUL bytes.

CUSTOM STYLES
  <config folder>/style/app.css      wudict's page
  <config folder>/style/article.css  every dictionary article

  Custom properties set on :root in app.css also apply to articles:

    html:not([data-dark]){ --bg:#f4ecd8; --wd-article-bg:#faf3e3 }

  article.css is added after each dictionary's own CSS. [data-dark] is
  set on the page, on each article and in each article frame when the
  theme is dark. Both files are served with no-cache. The editor:
  dictionary panel -> folder summary -> "Custom styles…".
  http://<host>:<port>/?style=off loads the page without either file.

EXAMPLE wudict.toml
  DICT_DIR    = ["~/Dictionaries", "/data/dicts"]
  SERVER_IP   = "0.0.0.0"
  SERVER_PORT = "9000"
  NO_BROWSER  = "1"

EXAMPLES
  wudict
  wudict --dict-dir ~/Books/Dicts --port 9090 --no-browser
  wudict lookup ~/Dictionaries/Oxford.mdx serendipity
  wudict ingest -full ~/Dictionaries/Oxford.mdx
  SERVER_PORT=9000 wudict

MANUAL
  https://wudict.legbehindneck.com

ABOUT
  %s - %s
  %s
`, Version, ProductName, Tagline, RepoURL)
}

// Main is the CLI entry point, called by the module-root main package.
// guiDetached records that this process gave up its console (a double-clicked
// wudict.exe, D76) and headlessLog where its output went instead. Set once, in
// cmdServe, and read only by fail: after the detach, stderr is a hole in the
// ground, so a fatal error has to be shown some other way or the user sees a
// window flash and nothing else.
var (
	guiDetached bool
	headlessLog string
)

// applyLimits puts the MAX_* guards (internal/limits) in force from flags, the
// environment and wudict.toml. A config that cannot be read leaves the
// defaults: the command that reads it reports why.
func applyLimits(configPath string, flags map[string]string) {
	if configPath == "" {
		configPath = os.Getenv("CONFIG_PATH")
	}
	if cfg, err := config.Load(configPath, flags); err == nil {
		limits.Set(cfg.Limits)
	}
}

// limitFlags registers one flag per MAX_* key on fs (--max-zim-cluster for
// MAX_ZIM_CLUSTER) and returns a function giving the values that were set,
// keyed as config.Load reads them.
func limitFlags(fs *flag.FlagSet) func() map[string]string {
	vals := map[string]*string{}
	for _, k := range config.LimitKeys {
		name := strings.ToLower(strings.ReplaceAll(k.Key, "_", "-"))
		vals[k.Key] = fs.String(name, "", "advanced: "+k.Help+" (env/toml: "+k.Key+")")
	}
	return func() map[string]string {
		out := map[string]string{}
		for k, v := range vals {
			if *v != "" {
				out[k] = *v
			}
		}
		return out
	}
}

// fail reports a fatal error and exits 1. One place, so the GUI case cannot be
// handled on one path and forgotten on the other.
func fail(err error) {
	if err == nil {
		return
	}
	fmt.Fprintln(os.Stderr, "error:", err)
	if guiDetached {
		msg := err.Error()
		if headlessLog != "" {
			msg += "\n\nDetails: " + headlessLog
		}
		tray.Alert(ProductName, msg)
	}
	os.Exit(1)
}

func Main() {
	// Before any command: every one that opens a dictionary is bounded alike.
	// serve and ingest apply the limits again with their own flags.
	applyLimits("", nil)
	if len(os.Args) < 2 {
		// no arguments: start the server (documented default)
		fail(cmdServe(nil))
		return
	}
	cmd, args := os.Args[1], os.Args[2:]
	var err error
	switch cmd {
	case "list":
		err = cmdList(args)
	case "info":
		err = cmdInfo(args)
	case "lookup", "prefix", "fts", "wildcard":
		err = cmdQuery(cmd, args)
	case "ingest":
		err = cmdIngest(args)
	case "serve":
		err = cmdServe(args)
	case "searchall":
		err = cmdSearchAll(args)
	case "keys":
		err = cmdKeys(args)
	case "res":
		err = cmdRes(args)
	case "dump":
		err = cmdDump(args)
	case "lemmas", "lemma":
		err = cmdLemmas(args)
	case "token":
		err = cmdToken(args)
	case "clean":
		err = cmdClean(args)
	case "rm", "remove":
		err = cmdRemove(args)
	case "reindex":
		err = cmdReindex(args)
	case "-h", "--help", "help":
		fmt.Print(usage())
	case "-v", "--version", "version":
		fmt.Println(ProductName, Version)
		fmt.Println(RepoURL)
	case "licenses", "license", "notices":
		cmdLicenses()
	default:
		if strings.HasPrefix(cmd, "-") {
			// bare flags (mdict-go-web style): treat as serve flags
			err = cmdServe(os.Args[1:])
			break
		}
		if args := openFileArgs(cmd); args != nil {
			// `wudict <dictfile>` - the entry point desktop file associations
			// point at (D76). Tested last, so a typo is still an unknown
			// command rather than a silently misread path.
			err = cmdServe(args)
			break
		}
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", cmd, usage())
		os.Exit(2)
	}
	fail(err)
}

// cmdLicenses prints the program's own licence line and then the embedded
// third-party notices. To stdout, in full: a notice a user has to go find on a
// web page is not one that shipped with the binary.
func cmdLicenses() {
	fmt.Printf("%s %s\n%s\n\n", ProductName, Version, RepoURL)
	fmt.Print("GPL-3.0-or-later. This program comes with ABSOLUTELY NO WARRANTY.\n" +
		"You may redistribute it under the terms of the GNU General Public\n" +
		"License, version 3 or later: <https://www.gnu.org/licenses/gpl-3.0.html>\n\n")
	if Notices == "" {
		fmt.Printf("Third-party notices: %s/blob/master/THIRD-PARTY-NOTICES.md\n", RepoURL)
		return
	}
	fmt.Print(Notices)
}

// openFileArgs turns a lone dictionary path into serve flags, or returns nil
// if the argument is not one. Double-clicking a .mdx serves the FOLDER that
// holds it, not the single file: dictionaries travel with companions (an .mdd
// beside its .mdx, a StarDict .idx beside its .ifo - dict.SourceFiles), the
// discovery path already knows how to pair them, and one folder is the
// smallest unit that cannot be half-opened.
//
// The folder REPLACES the configured library for this run rather than joining
// it: "open this file" should show that file, promptly, and a preview scan of
// one download is D15's cheap path. The configured library is one launch away.
func openFileArgs(arg string) []string {
	if !dict.IsDictionaryFile(arg) {
		return nil
	}
	abs, err := filepath.Abs(arg)
	if err != nil {
		return nil
	}
	if st, err := os.Stat(abs); err != nil || st.IsDir() {
		return nil
	}
	return []string{"--dict-dir", filepath.Dir(abs)}
}

func cmdList(args []string) error {
	dirs := folderArgs(args)
	if len(dirs) == 0 {
		return fmt.Errorf("usage: wudict list <dir> [dir…]")
	}
	paths, _, err := dict.DiscoverAll(dirs)
	if err != nil {
		return err
	}
	for _, p := range paths {
		fmt.Println(p)
	}
	if len(paths) == 0 {
		fmt.Fprintf(os.Stderr, "no dictionaries found in %s\n", strings.Join(dirs, ", "))
	}
	return nil
}

// folderArgs reads folder arguments the same way everywhere in the CLI: one
// per argument, and each argument may itself be a separator-joined list, so
// `list A B`, `list A:B` and `DICT_DIR`-style values all work.
func folderArgs(args []string) []string {
	var out []string
	for _, a := range args {
		out = append(out, config.ParseList(a)...)
	}
	return out
}

func cmdInfo(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: wudict info <dictfile>")
	}
	// Before the open, so the file list is printed even for a dictionary whose
	// header will not parse - which is exactly when knowing what is on disk
	// beside it is most useful.
	applyLibrarySettings()
	files := dictFiles(args[0])

	d, err := dict.Open(args[0])
	if err != nil {
		printDictFiles(files)
		return err
	}
	defer d.Close()
	m, c := d.Meta(), d.Caps()
	fmt.Printf("name:        %s\nformat:      %s\npath:        %s\nentries:     %d\ncapabilities: exact=%v prefix=%v fts=%v wildcard=%v\n",
		m.Name, m.Format, m.Path, m.EntryCount, c.Exact, c.Prefix, c.FTS, c.Wildcard)
	if m.IndexLang != "" {
		// Only when the dictionary DECLARED it. A language derived from the
		// file name is the search path's fallback, not a fact about the file,
		// and printing it here would make a guess look like metadata.
		fmt.Printf("language:    %s (declared)\n", m.IndexLang)
	}
	// What the dictionary says about ITSELF - the sidecar annotation where the
	// format ships one (Lingvo's <stem>.ann), else the header description. Same
	// composition order the About panel uses (internal/server/about.go), so the
	// terminal and the page never disagree about which one wins.
	for i, line := range aboutLines(strings.TrimPrefix(m.Format, "wudict:"), args[0], m.Description) {
		if i == 0 {
			fmt.Printf("description: %s\n", line)
			continue
		}
		fmt.Printf("             %s\n", line)
	}
	// The rest of the header, as the file names it. A value may be markup or
	// span lines like the description; its continuation lines are indented
	// under the value, not under the key.
	for i, f := range m.Header {
		label := "             "
		if i == 0 {
			label = "header:      "
		}
		for j, line := range textLines(f.Value) {
			if j == 0 {
				fmt.Printf("%s%s: %s\n", label, f.Name, line)
				continue
			}
			fmt.Printf("             %*s  %s\n", len(f.Name), "", line)
		}
	}
	printDictFiles(files)
	return nil
}

// textLines flattens one header value for a terminal: markup reduced to its
// text, one trimmed line per line, blank lines dropped.
func textLines(text string) []string {
	if strings.ContainsRune(text, '<') {
		text = htmlref.Text(text, nil)
	}
	var out []string
	for _, line := range strings.Split(text, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, line)
		}
	}
	return out
}

// aboutLines is the annotation, flattened for a terminal: one line per section,
// headed by the language the section declared. Markup is reduced to its text
// rather than printed as tags - an MDX header description is routinely a small
// HTML document.
func aboutLines(format, srcPath, desc string) []string {
	var out []string
	add := func(lang, text string) {
		for _, line := range textLines(text) {
			if lang != "" {
				line = "[" + lang + "] " + line
				lang = ""
			}
			out = append(out, line)
		}
	}
	if a, ok := dict.AboutFor(format, srcPath); ok {
		for _, sec := range a.Sections {
			add(sec.Lang, sec.Text)
		}
		if len(out) > 0 {
			return out
		}
	}
	if strings.TrimSpace(desc) != "" {
		add("", desc)
	}
	return out
}

// dictFile is one thing on disk that belongs to this dictionary: the file
// itself, the .mdd/.files.zip/.idx companions it cannot be read without, and
// the prepared folder if it has one.
type dictFile struct {
	kind string // "original" or "prepared", the words `wudict rm` uses
	path string
	size int64
}

// dictFiles answers the question `wudict rm` answers as a side effect of its
// dry run - what is on disk for this dictionary, and how big - without the
// user having to type a delete command to find out (D63 makes rm dry by
// default precisely so it can be used for looking; this is the looking).
//
// The argument is a path, as everywhere else in the CLI. Two shapes:
// a prepared text.db names its own folder, anything else is an original whose
// companions and prepared folder are looked up. A prepared FOLDER resolves to
// its text.db first (dict.MainFile), the same way the open does - it is the
// name every listing shows for that dictionary, so it is the name a user
// types, and must not fall through to the "original" branch as one opaque
// file.
func dictFiles(arg string) []dictFile {
	abs, err := filepath.Abs(arg)
	if err != nil {
		abs = arg
	}
	abs = dict.MainFile(abs)
	var out []dictFile
	add := func(kind, p string) {
		if n := store.TreeSize(p); n > 0 || fsx.FileExists(p) {
			out = append(out, dictFile{kind, p, n})
		}
	}
	if store.IsTextDB(abs) {
		// The bundle form is a folder and is reported as one, so its info.txt
		// and any media.db are counted whether or not they are named here.
		if strings.EqualFold(filepath.Base(abs), store.TextDBName) {
			add("prepared", filepath.Dir(abs))
			return out
		}
		add("prepared", abs) // loose <name>.text.db, copied out of its folder
		add("prepared", store.MediaSibling(abs))
		return out
	}
	for _, p := range dict.SourceFiles(abs) {
		add("original", p)
	}
	if dir, ok := store.LookupDir(abs); ok {
		add("prepared", dir)
	}
	return out
}

// printDictFiles prints the inventory in `wudict rm`'s vocabulary and order -
// originals first, prepared last, one line each, then the total - so the two
// commands describe the same dictionary in the same words.
func printDictFiles(files []dictFile) {
	if len(files) == 0 {
		return
	}
	w := 0
	for _, f := range files {
		if len(f.path) > w {
			w = len(f.path)
		}
	}
	var total int64
	fmt.Println("files:")
	for _, f := range files {
		total += f.size
		fmt.Printf("  %-8s  %-*s  %10s\n", f.kind, w, f.path, logx.Size(f.size))
	}
	fmt.Printf("  %-8s  %-*s  %10s\n", "total", w, "", logx.Size(total))
}

func cmdQuery(mode string, args []string) error {
	fs := flag.NewFlagSet(mode, flag.ExitOnError)
	n := fs.Int("n", 20, "max results")
	format := fs.String("format", "raw", "article format: raw|clean|text")
	// The base a /res/… reference is resolved against. Empty by default: the
	// CLI is not a server, and inventing an address here would emit links to
	// something that may not be listening. Point it at a running server and the
	// output is a self-contained page.
	base := fs.String("base", "", "prefix for /res/… references (e.g. http://127.0.0.1:6888)")
	fs.Parse(args)
	if fs.NArg() != 2 {
		return fmt.Errorf("usage: wudict %s [-n max] [-format raw|clean|text] <dictfile> <word>", mode)
	}
	// Before the open, so a misspelled format costs nothing.
	f, err := server.ParseArticleFormat(*format)
	if err != nil {
		return err
	}
	m := search.Exact // "lookup"; the other three commands are named for their mode
	if mode != "lookup" {
		if m, err = search.ParseMode(mode); err != nil {
			return err
		}
	}
	d, err := dict.Open(fs.Arg(0))
	if err != nil {
		return err
	}
	defer d.Close()
	// The query the app runs, from the same code: fts reads the words as a
	// phrase, then near each other, then anywhere, exactly as the search box.
	h := search.Query(context.Background(), d, m, fs.Arg(1), *n)
	switch {
	case h.Skipped:
		return dict.ErrUnsupported
	case h.Err != nil:
		return h.Err
	case len(h.Results) == 0:
		return dict.ErrNotFound
	}
	results := h.Results
	// The same reduction /api/search performs (D61), from the same code.
	server.FormatArticles(d, f, strings.TrimSuffix(*base, "/"), results)
	for _, r := range results {
		// One delimiter for all three formats. `text` gains an HTML comment it
		// does not need, which is cheaper than a second line format for
		// anything already parsing this output.
		fmt.Printf("<!-- %s -->\n%s\n", r.Headword, r.Body)
	}
	return nil
}

func cmdKeys(args []string) error {
	fs := flag.NewFlagSet("keys", flag.ExitOnError)
	offset := fs.Int("offset", 0, "start at this headword (0 = the first)")
	// Every headword unless asked otherwise: this command exists to be piped
	// into grep/wc/sort, and a silent default of 50 made it lie about small
	// dictionaries and truncate every large one.
	n := fs.Int("n", 0, "how many headwords (0 = all)")
	fs.Parse(args)
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: wudict keys [-offset N] [-n count] <dictfile>")
	}
	d, err := dict.Open(fs.Arg(0))
	if err != nil {
		return err
	}
	defer d.Close()
	// buffered: a large dictionary is over a million lines, and an unbuffered
	// fmt.Println is one write syscall each
	w := bufio.NewWriter(os.Stdout)
	defer w.Flush()
	for _, k := range d.Keywords(*offset, *n) {
		if _, err := fmt.Fprintln(w, k); err != nil {
			return err // a closed pipe (`| head`) ends the dump, not a panic
		}
	}
	return w.Flush()
}

func cmdIngest(args []string) error {
	applyLibrarySettings()
	fs := flag.NewFlagSet("ingest", flag.ExitOnError)
	out := fs.String("o", "", "output .db path for a single dictionary file (default: <db-dir>/<dictionary name>/text.db)")
	full := fs.Bool("full", false, "also pack binary resources into a companion .media.db")
	fulltext := fs.Bool("fulltext", false, "also index article text for full-text search - the largest index (-fulltext=false drops it)")
	headwords := fs.Bool("headwords", false, "index headwords only: the opposite of -fulltext")
	fuzzyOnly := fs.Bool("fuzzy-only", false, "deprecated spelling of -headwords")
	configPath := fs.String("config", "", "path to wudict.toml (env: CONFIG_PATH)")
	limitVals := limitFlags(fs)
	fs.Parse(args)
	applyLimits(*configPath, limitVals())

	// A flag left out changes nothing about a dictionary already prepared, as
	// a parameter left out of /api/ingest changes nothing; a dictionary never
	// prepared starts from headwords only, as it does in the app (D152, D24).
	var ft *bool
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "fulltext":
			ft = fulltext
		case "headwords", "fuzzy-only":
			v := !(*headwords || *fuzzyOnly)
			ft = &v
		}
	})

	// Where to look, resolved as searchall does: the arguments, else DICT_DIR
	// from --config/CONFIG_PATH, the environment or wudict.toml, else the
	// default folder. The library is already configured, so `ingest` does
	// not make the user spell it out again.
	srcs, origin := fs.Args(), "argument"
	if len(srcs) == 0 {
		if *out != "" {
			return fmt.Errorf("-o needs the one dictionary file to prepare")
		}
		if *configPath == "" {
			*configPath = os.Getenv("CONFIG_PATH")
		}
		cfg, err := config.Load(*configPath, nil)
		if err != nil {
			return err
		}
		srcs, origin = cfg.DictDirs, cfg.Origin("DICT_DIR")
	}
	// a folder argument may be a DICT_DIR-style list (folderArgs); a file is
	// taken whole, so a name with the list separator in it still works
	var split []string
	for _, s := range srcs {
		if st, err := os.Stat(s); err == nil && !st.IsDir() {
			split = append(split, s)
		} else {
			split = append(split, folderArgs([]string{s})...)
		}
	}
	srcs = split
	if len(srcs) == 0 {
		return fmt.Errorf("no dictionary folder is configured (%s) - name one: wudict ingest <dictfile|folder…>", origin)
	}

	if *out != "" {
		if len(srcs) != 1 || fsx.DirExists(srcs[0]) {
			return fmt.Errorf("-o names the output of one dictionary file, not of %s", strings.Join(srcs, ", "))
		}
		if *full && store.MediaSibling(*out) == "" {
			// media pairs with its text by file name; refused before an
			// ingest that could take minutes, not after it
			return fmt.Errorf("-full packs media beside the text, so -o must end in .text.db (or be a text.db)")
		}
		return ingestOne(srcs[0], *out, *full, ft)
	}
	if err := refuseUnderServer("use its dictionary panel"); err != nil {
		return err
	}
	// a single file keeps its own errors, unprefixed by a [1/1] count
	if len(srcs) == 1 && !fsx.DirExists(srcs[0]) {
		return ingestOne(srcs[0], "", *full, ft)
	}

	var paths, dirs []string
	for _, s := range srcs {
		if fsx.DirExists(s) {
			dirs = append(dirs, s)
		} else {
			paths = append(paths, s)
		}
	}
	found, _, err := dict.DiscoverAll(dirs)
	if err != nil {
		return err
	}
	paths = append(paths, found...)
	if len(dirs) > 0 {
		// stderr, as searchall does: once the folders come from a config file
		// they are not on the command line to be seen
		fmt.Fprintf(os.Stderr, "preparing %s from %s (%s)\n",
			logx.Plural(len(found), "dictionary", "dictionaries"), strings.Join(dirs, ", "), origin)
	}
	if len(paths) == 0 {
		return fmt.Errorf("no dictionaries found under %s (%s)", strings.Join(srcs, ", "), origin)
	}
	var failed int
	for i, p := range paths {
		fmt.Fprintf(os.Stderr, "[%d/%d] %s\n", i+1, len(paths), filepath.Base(p))
		if err := ingestOne(p, "", *full, ft); err != nil {
			fmt.Fprintf(os.Stderr, "  failed: %v\n", err)
			failed++
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d dictionaries failed", failed, len(paths))
	}
	return nil
}

// ingestOne prepares one dictionary into its library folder - or, with out,
// into that database - by the rules every preparation follows
// (store.Reconcile): fullText, when given, sets the full-text index, nil
// keeps what is built; outdated data is rebuilt, and full also packs media.
func ingestOne(srcPath, out string, full bool, fullText *bool) error {
	name := displayName(srcPath)
	t := store.Target{FullText: fullText, Rebuild: store.IfOutdated, Out: out}
	if full {
		t.Media = store.MediaOn
	}
	hooks, done := cliHooks(srcPath, &name)
	res, err := store.Reconcile(srcPath, t, hooks)
	done()
	logx.ClearLine()
	dir := filepath.Dir(res.TextDB)
	switch {
	case res.Rebuilt:
		// printed even when the media step after it failed: the text is done
		fmt.Printf("%s%s indexed in %.1fs → %s\n",
			logx.Dict(name), logx.Plural(res.Report.Entries, "entry", "entries"), res.TextTime.Seconds(), dir)
		if res.Report.UnresolvedLinks > 0 {
			fmt.Printf("%s%s pointed at headwords not present in the source (skipped)\n",
				logx.Dict(name), logx.Plural(res.Report.UnresolvedLinks, "redirect", "redirects"))
		}
	case err == nil:
		fmt.Printf("%salready prepared in %s - skipped\n", logx.Dict(name), dir)
	}
	if err != nil {
		return fmt.Errorf("preparing %q: %w", name, err)
	}
	printMedia(name, res)
	return nil
}

// cliHooks reports progress on the terminal and, when the source is read,
// takes the dictionary's own title for the lines that follow. done closes the
// reader, if one was opened.
func cliHooks(srcPath string, name *string) (h store.Hooks, done func()) {
	var r dict.Reader
	done = func() {
		if r != nil {
			r.Close()
		}
	}
	return store.Hooks{
		Reader: func() (dict.Reader, error) {
			rd, err := dict.OpenReader(srcPath)
			if err != nil {
				return nil, err
			}
			r = rd
			if n := r.Meta().Name; n != "" {
				*name = n
			}
			return r, nil
		},
		Progress:      entryProgress,
		MediaProgress: func(n, total int) { logx.Progress("  %d/%d media files", n, total) },
	}, done
}

// printMedia reports what a preparation did with media, when it did anything.
func printMedia(name string, res store.Outcome) {
	switch {
	case res.MediaCurrent:
		fmt.Printf("%smedia already packed - skipped\n", logx.Dict(name))
	case res.MediaEmpty:
		fmt.Printf("%sno media to pack\n", logx.Dict(name))
	case res.Packed > 0:
		fmt.Printf("%s%s packed in %.1fs\n",
			logx.Dict(name), logx.Plural(res.Packed, "media file", "media files"), res.MediaTime.Seconds())
	}
}

// displayName is a dictionary's title for a terminal line, from its header when
// the format can read one cheaply, else its file name.
func displayName(path string) string {
	if dict.HasProber(path) {
		if m, err := dict.Probe(path); err == nil && m.Name != "" {
			return m.Name
		}
	}
	return filepath.Base(path)
}

func cmdServe(args []string) (err error) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	var dictDirs multiFlag
	fs.Var(&dictDirs, "dict-dir", "folder with dictionaries; repeat for several (env/toml: DICT_DIR)")
	dbDir := fs.String("db-dir", "", "library dir for cached dictionaries (env/toml: DB_DIR)")
	useCached := fs.Bool("use-cached", false, "also serve previously imported dictionaries from the library (env/toml: USE_CACHED)")
	noCompress := fs.Bool("no-compress", false, "store article text uncompressed - larger databases (env/toml: NO_COMPRESS)")
	indexWorkers := fs.String("index-workers", "", "dictionaries to prepare at once; \"auto\" = every core (env/toml: INDEX_WORKERS)")
	// A string, not a Bool: "" has to mean "unset, defer to env and toml",
	// which a bool flag cannot express - it would override a file that says
	// "1" with its own false on every run.
	remoteDelete := fs.String("allow-remote-delete", "", "may another machine delete a dictionary: 0 (default) or 1 (env/toml: ALLOW_REMOTE_DELETE)")
	ip := fs.String("ip", "", "listen IP (env/toml: SERVER_IP)")
	auth := fs.String("auth", "", "require the access key: auto (default), on, off (env/toml: AUTH)")
	authToken := fs.String("auth-token", "", "the access key itself, instead of ~/.wudict/token (env/toml: AUTH_TOKEN)")
	port := fs.String("port", "", "listen port (env/toml: SERVER_PORT)")
	configPath := fs.String("config", "", "path to wudict.toml (env: CONFIG_PATH)")
	noBrowser := fs.Bool("no-browser", false, "do not open a browser tab (env/toml: NO_BROWSER)")
	verbose := fs.Bool("verbose", false, "verbose logging (env/toml: VERBOSE)")
	speexdec := fs.String("speexdec", "", "path to speexdec for .spx audio (env/toml: SPEEXDEC)")
	trayOn := fs.Bool("tray", false, "show a system tray icon (env/toml: TRAY)")
	trayOff := fs.Bool("no-tray", false, "never show a system tray icon (env/toml: TRAY=0)")
	limitVals := limitFlags(fs)
	fs.Parse(args)

	if *configPath == "" {
		*configPath = os.Getenv("CONFIG_PATH")
	}
	flagVals := map[string]string{
		"DICT_DIR": dictDirs.String(), "DB_DIR": *dbDir,
		"SERVER_IP": *ip, "SERVER_PORT": *port,
		"SPEEXDEC": *speexdec, "ALLOW_REMOTE_DELETE": *remoteDelete,
		"AUTH": *auth, "AUTH_TOKEN": *authToken,
	}
	if *noBrowser {
		flagVals["NO_BROWSER"] = "1"
	}
	if *verbose {
		flagVals["VERBOSE"] = "1"
	}
	if *useCached {
		flagVals["USE_CACHED"] = "1"
	}
	if *noCompress {
		flagVals["NO_COMPRESS"] = "1"
	}
	if *indexWorkers != "" {
		flagVals["INDEX_WORKERS"] = *indexWorkers
	}
	for k, v := range limitVals() {
		flagVals[k] = v
	}
	// --no-tray is checked second so it wins if both are given: between two
	// contradictory flags, the one that changes nothing about the process is
	// the safe reading.
	if *trayOn {
		flagVals["TRAY"] = "1"
	}
	if *trayOff {
		flagVals["TRAY"] = "0"
	}
	cfg, err := config.Load(*configPath, flagVals)
	if err != nil {
		return err
	}
	limits.Set(cfg.Limits)
	if cfg.Verbose {
		logx.Enabled = true
	}

	// Resolved before anything is served, and a failure here stops the
	// launch: the alternative - warn, then serve the library to the network
	// with no key because the token file could not be written - is the exact
	// outcome the setting exists to prevent.
	var token string
	if config.AuthRequired(cfg.Auth, cfg.IP) {
		if token = cfg.AuthToken; token == "" {
			if token, err = config.LoadToken(false); err != nil {
				return err
			}
		}
	}

	// Machine C (D74). A terminal launch keeps today's behaviour byte for
	// byte; only a GUI launch - a macOS .app, a double-clicked wudict.exe -
	// moves the message channel, because there stderr points at nothing a
	// human will ever read. A console is never taken from a user who has one.
	//
	// Read once and carried: on Windows GUILaunched asks who else is attached
	// to this console, and DetachConsole below makes the same question answer
	// differently (D76).
	gui := tray.GUILaunched()
	trayWanted := gui
	if cfg.Tray != nil {
		trayWanted = *cfg.Tray
	}
	if trayWanted && gui {
		// Order matters. Take the log first and give up the console second: if
		// the log cannot be opened, keeping an ugly black window is worth more
		// than a server that runs in total silence.
		if f, ferr := openHeadlessLog(); ferr == nil {
			logx.SetOutput(f)
			guiDetached, headlessLog = true, f.Name()
			// Whatever ends this run is written to the log while the file is
			// still open - the restore below is what closes it, and Main's
			// stderr is dead by then. The dialog Main raises says where to
			// find this line.
			defer func() {
				if err != nil {
					logx.Warn("%v", err)
				}
				logx.SetOutput(nil)
				f.Close()
			}()
			tray.DetachConsole()
		}
	}
	logx.V("config: source=%q dictDirs=%v dbDir=%q addr=%s speexdec=%s",
		cfg.Source, cfg.DictDirs, cfg.DBDir, cfg.Addr(), cfg.Speexdec)
	store.SetDBDir(cfg.DBDir)

	// The library (DB_DIR) is wudict's own working area, never a folder of
	// user dictionaries. Using it as DICT_DIR is a
	// hard error; and wherever it lives, discovery skips it - which also covers
	// the subtler case of a DB_DIR nested inside the dictionary folder.
	libDir := store.DefaultDBDir()
	for _, d := range cfg.DictDirs {
		if !dict.SameDir(d, libDir) {
			continue
		}
		return fmt.Errorf("DICT_DIR and DB_DIR are the same folder:\n"+
			"  %s\n"+
			"  DB_DIR contains internally cached dictionaries; DICT_DIR must point at\n"+
			"  the folder with your dictionary files (.mdx, .ifo, .slob, .dsl, .bgl, .zim).\n"+
			"  To only use cached dictionaries, leave DICT_DIR unset and set USE_CACHED = \"1\".", libDir)
	}
	dict.ExcludeDir(libDir)

	// Claim the port FIRST. Everything below - adopting library folders,
	// scanning dictionaries, auto-preparing a DSL index - costs time and
	// writes to the library, and a launch that turns out to be a duplicate
	// must do none of it.
	url, moreURLs := serveURLs(cfg.IP, cfg.Port)
	ln, lerr := net.Listen("tcp", cfg.Addr())
	if lerr != nil {
		// Ask who is there rather than classify the error. The test this
		// replaces matched the string "address already in use", which is the
		// POSIX phrasing; Windows says "Only one usage of each socket address
		// ... is normally permitted", so a second launch there never reached
		// this branch - it returned a raw bind error, and after DetachConsole
		// that error had nowhere to appear at all (D76). One HTTP probe on a
		// path that has already failed costs less than a per-OS errno table
		// and answers the question that actually matters.
		if inst, ok := probeRunning(cfg.Addr()); ok {
			announceRunning(inst, url, cfg.DictDirs, !cfg.NoBrowser)
			if !cfg.NoBrowser {
				// With the key, when this launch has one: the instance
				// already running was started from the same configuration,
				// so the key that would have been ours is the key it wants.
				server.OpenURL(browserURL(cfg.Addr(), token))
			}
			return nil
		}
		return fmt.Errorf(`cannot listen on %s: %w
Something else holds the port (it did not answer as a wudict).
Hint: pick another port with --port, e.g.:  wudict --port %s
      or find what holds it:                %s`,
			cfg.Addr(), lerr, nextPort(cfg.Port), portHolderCmd(cfg.Port))
	}
	defer ln.Close()

	// The loopback companion. A bind to one concrete address is a socket on
	// that address and nowhere else, which has two consequences the wildcard
	// default hides. Every client that lives on this machine reaches the
	// server through 127.0.0.1 by construction - the Android shell's WebView
	// origin and its readiness probe, a browser extension, an old bookmark -
	// and none of them can know what the LAN address is today. And a listener
	// bound to a LAN address is destroyed by the kernel when that address
	// leaves the interface: roaming out of range, or a DHCP lease coming back
	// different, makes accept() fail with EINVAL, which Go does not classify
	// as temporary, so Serve returns it and the program ends.
	//
	// So loopback is opened alongside - the one address that cannot be taken
	// away - and it, rather than the address the user asked for, is the one
	// whose loss ends the program. Nothing is added when the main listener
	// already accepts there, which is every default install.
	lns := []net.Listener{ln}
	var alsoURL string
	if lb := loopbackAddr(cfg.IP, cfg.Port); lb != "" {
		if lbLn, lbErr := net.Listen("tcp", lb); lbErr != nil {
			// Not fatal: the server the user asked for is up, and only the
			// clients that would have come in through loopback are affected.
			// Another wudict already holding that port is the likely reason.
			logx.Warn("could not also listen on %s: %v", lb, lbErr)
		} else {
			defer lbLn.Close()
			alsoURL = "http://" + lb + "/"
			// First: from here on this is the essential listener, and the
			// address that can vanish is the expendable one.
			lns = []net.Listener{lbLn, ln}
		}
	}
	// A wildcard bind accepts on loopback already, so it opens no companion;
	// the banner still names loopback once its address line names the LAN.
	if isWildcard(cfg.IP) {
		if lb := "http://" + probeHost(cfg.Addr()) + "/"; lb != url {
			alsoURL = lb
		}
	}

	// An import that was killed - the phone reclaimed the process, the machine
	// lost power - leaves its half-extracted staging directory behind. It is
	// hidden, so discovery never saw it, and nothing else would ever remove
	// it; the only safe moment to do so is before anything is importing.
	intake.Sweep(cfg.DictDirs)

	// migrate old cached dictionaries
	if moved, err := store.AdoptLoose(); err != nil {
		logx.Warn("could not tidy the library: %v", err)
	} else if len(moved) > 0 {
		for _, m := range moved {
			logx.Status("library: %s → %s/", filepath.Base(m.From), filepath.Base(m.Dir))
		}
	}

	// first run: generate the commented ~/.wudict/wudict.toml containing
	// defaults; an existing file anywhere in the search order wins. cfg.Source
	// is updated so the startup summary names the file that is now in effect
	// rather than reporting "none" on the very run that created it.
	cfgFile := cfg.Source
	if cfgFile == "" && *configPath == "" {
		p, created, err := config.EnsureConfigFile()
		if err != nil {
			logx.Warn("could not create %s: %v", config.Name, err)
		} else {
			cfgFile, cfg.Source = p, p
			if created {
				logx.Status("config: created %s", p)
			}
		}
	}

	// Parallelism, before anything starts using it. Zero means this platform
	// keeps the runtime's own default (every core), which is right on a desktop
	// and wrong on a phone - see config.MaxProcs. Handing the number to the
	// server as well is what lets the power states lower it while the app is
	// away and restore it on return (D64).
	if n := config.MaxProcs(); n > 0 {
		server.SetActiveProcs(n)
		search.SetWorkers(n)
		logx.V("parallelism: GOMAXPROCS=%d, search fan-out=%d", n, n)
	}

	// The UI state (which dictionaries are searched, in what order) lives
	// beside the config file in effect, so a portable install carries both and
	// --config re-points both. Loaded BEFORE the registry: Warm skips disabled
	// dictionaries, and it starts inside NewRegistry.
	user := server.UserDir(userDir(cfgFile))
	regOpts := []server.Option{server.WithPrefs(server.LoadPrefs(user.State())),
		server.WithFailures(user.Failures())}
	// The wudict howto, the guide the app ships as a dictionary: written
	// beside the config in effect (rewritten only when this build's guide
	// differs), and listed with the user's dictionaries. Without a place to
	// write it, the app starts without it.
	// Once the user removed it, it is not written back until Setup restores it;
	// the registry lists it only while its file exists either way.
	if howtoDir := user.Builtin(); howtoDir != "" {
		if !howto.IsRemoved(howtoDir) {
			if _, err := howto.Install(howtoDir); err != nil {
				logx.Warn("the wudict howto is not available: %v", err)
			}
		}
		regOpts = append(regOpts, server.WithBuiltin(server.Builtin{ID: howto.ID, Path: filepath.Join(howtoDir, howto.FileName)}))
	}
	// Set before the registry exists: NewRegistry can already start
	// background work on this lane (the abbreviation sweep), which prepares
	// with these - the compression, the memory ceiling the preparation budget
	// follows (D182), the lane. From here on a preparation is urgent only where
	// the server marks it so.
	server.SetIndexWorkers(cfg.IndexWorkers)
	store.SetCompressBodies(!cfg.NoCompress)
	store.SetSmallBodies(cfg.Compression == config.CompressionSmall)
	if cfg.MemoryLimit > 0 {
		// A soft ceiling: Go collects harder instead of growing past it. Set it
		// through the server rather than debug directly, because a ceiling on
		// its own only buys more GC - the registry needs the same number to
		// know when to shed caches instead, which is what turns pressure into
		// freed memory rather than a collector spinning on a live set (D64).
		server.SetMemoryLimit(cfg.MemoryLimit)
		logx.V("memory limit: %d MB", cfg.MemoryLimit>>20)
	}
	dict.SetUrgentDefault(false)
	reg, err := server.NewRegistry(cfg.DictDirs, cfg.UseCached, regOpts...)
	if err != nil {
		return fmt.Errorf("scanning %s: %w", strings.Join(cfg.DictDirs, ", "), err)
	}
	reg.SetPreviewBudget(cfg.PreviewMemory)
	reg.SetSearchBudget(cfg.SearchMemory)
	if cfg.SearchMemory > 0 {
		logx.V("search memory budget: %d MB per query", cfg.SearchMemory>>20)
	}
	srv := server.New(reg)
	srv.ConfigPath = cfgFile
	srv.User = user
	srv.Version = Version
	srv.DictDirOrigin = cfg.Origin("DICT_DIR")
	srv.DictDirEditable = cfg.EditableInFile("DICT_DIR")
	srv.ConfigProblems = cfg.Problems
	// What the tunable keys actually resolved to, for a shell that overrides
	// them per device and needs to show what is in effect (D101).
	srv.Effective = cfg.Effective()
	useExternalSpeex := cfg.SpeexBackend == "external"
	srv.UseExternalSpeex = useExternalSpeex
	// Only look for the external speexdec binary when it will actually be used:
	// forced via SPEEX_BACKEND=external, or a purego build without the built-in
	// decoder. A full cgo build uses its own in-process decoder and
	// does not need the external speexdec.
	var sxPath, sxSource string
	if useExternalSpeex || !speex.Available {
		sxPath, sxSource = resolveSpeexdec(cfg.Speexdec)
	}
	srv.Speexdec = sxPath
	srv.AutoIndex = cfg.AutoIndexEnabled()
	srv.ImportKeep = cfg.ImportKeep
	srv.ImportURLHosts = cfg.ImportURLHosts
	srv.ImportInsecure = cfg.ImportInsecure
	// Lemma packs are loaded on first use and never at startup: a launch must
	// not pay 25-157 ms and tens of megabytes for a language this session may
	// never search in. MORPH_CACHE=0 loads none at all.
	// The folder is indexed here rather than per search: re-reading it on
	// every query that found nothing would put a directory listing on the
	// search path. It is re-read only when something changes it - an install
	// or a removal through /api/lemmas calls Rescan (D91).
	srv.Morph = morph.New(cfg.MorphCache, cfg.LemmaDir)
	// What /api/lemmas installs into, and the catalogue it installs FROM. The
	// URL lives on the Server because the client must never supply one: a
	// caller-chosen address would make the endpoint fetch whatever the host
	// running wudict can reach.
	srv.LemmaDir, srv.LemmaURL = cfg.LemmaDir, cfg.LemmaURL
	useLemmaClient(cfg.LemmaURL, cfg.ImportInsecure)
	srv.AllowRemoteDelete = cfg.AllowRemoteDelete
	srv.BrowserExtensions = cfg.BrowserExtensions
	srv.WebOrigins = cfg.WebOrigins
	srv.TrustedHosts = trustedHosts(cfg.TrustedHosts, cfg.IP)
	srv.AuthToken = token

	lib, _ := store.Library()
	inFolder, fromLib := reg.Counts()
	printStartup(cfg, startupInfo{
		roots:    reg.Roots(),
		inFolder: inFolder, fromLibrary: fromLib, prepared: len(lib),
		total: reg.Count(), libDir: libDir, url: url, moreURLs: moreURLs, alsoURL: alsoURL,
		speex:  speexSummary(useExternalSpeex, sxPath, sxSource),
		keyURL: keyURL(url, token),
	})
	// No wait for the server: every listener is bound above, so the kernel
	// queues the browser's connection until Serve accepts it.
	browse := browserURL(cfg.Addr(), token)
	if !cfg.NoBrowser {
		server.OpenURL(browse)
	}

	// No WriteTimeout: /api/search (NDJSON) and /api/ingest (SSE) stream for
	// a long time - a write deadline would sever them. ReadHeaderTimeout +
	// IdleTimeout + MaxHeaderBytes harden against slowloris without touching
	// the streaming paths.
	httpSrv := &http.Server{
		Addr:              cfg.Addr(),
		Handler:           srv,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	// Graceful shutdown on Ctrl-C / SIGTERM: finish in-flight requests, close
	// database handles cleanly. One shutdown, reachable from two places - a
	// signal and the tray's Quit item - so sync.Once, not a second code path.
	idle := make(chan struct{})
	var stopOnce sync.Once
	stop := func() {
		stopOnce.Do(func() {
			logx.Status("\nshutting down…")
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = httpSrv.Shutdown(ctx)
			close(idle)
		})
	}
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
		<-sig
		stop()
	}()

	serve := func() error {
		// One http.Server across every listener: Serve registers each one, so
		// the single Shutdown above closes them all and every call returns
		// ErrServerClosed. Only lns[0] decides the program's fate. The rest
		// are the addresses a network change can take away, and losing the
		// LAN is not a reason to stop serving the machine itself.
		for _, l := range lns[1:] {
			go func(l net.Listener) {
				err := httpSrv.Serve(l)
				if err == nil || errors.Is(err, http.ErrServerClosed) {
					return
				}
				logx.Warn("stopped listening on %s: %v\n"+
					"  the address is no longer on this machine; restart to share again", l.Addr(), err)
			}(l)
		}
		err := httpSrv.Serve(lns[0])
		if errors.Is(err, http.ErrServerClosed) {
			<-idle
			return nil
		}
		return err
	}

	// The tray wraps the server rather than the other way round: serve() runs
	// to completion whatever the tray does, and with the tray off (every
	// terminal launch, every service unit, Android) Wrap is a pass-through.
	// Name is left empty so the menu uses the package default, WuWeiDict -
	// ProductName still reads "wuDict", which predates D27.
	return tray.Wrap(tray.Config{
		Enabled:  trayWanted,
		Explicit: cfg.Tray != nil,
		GUI:      gui,
		Version:  Version,
		URL:      url,
		// The key too: the cookie lives only as long as the browser session,
		// and a sign-in launch (--no-browser) leaves this item as the only
		// way in, from a GUI with no console to read the key from.
		Open:     func() { server.OpenURL(browse) },
		Rescan:   trayRescan(reg),
		OpenDir:  trayOpenDir(reg.Dirs()),
		Shutdown: stop,
	}, serve)
}

// trayRescan drives the same rescan /api/rescan does. Menu clicks arrive on
// the platform's own thread, so the work runs off it - a rescan of 105
// dictionaries would otherwise freeze the menu - and a second click while one
// is running is dropped rather than queued.
func trayRescan(reg *server.Registry) func() {
	var busy atomic.Bool
	return func() {
		if !busy.CompareAndSwap(false, true) {
			return
		}
		go func() {
			defer busy.Store(false)
			reg.ForgetFailures()
			if err := reg.Rescan(); err != nil {
				logx.Warn("rescan: %v", err)
				return
			}
			reg.Warm()
		}()
	}
}

// trayOpenDir reveals the first dictionary folder, or reports nothing to
// reveal by omitting the menu item entirely (a nil callback).
func trayOpenDir(dirs []string) func() {
	if len(dirs) == 0 {
		return nil
	}
	dir := dirs[0]
	return func() {
		go func() {
			if err := server.Reveal(dir); err != nil {
				logx.Warn("open dictionary folder: %v", err)
			}
		}()
	}
}

// openHeadlessLog is where logx goes when there is no console to write to. The
// destinations are the ones the service templates already chose, so a user who
// switches between a GUI launch and a launchd agent finds one log, not two.
func openHeadlessLog() (*os.File, error) {
	var path string
	switch runtime.GOOS {
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		path = filepath.Join(home, "Library", "Logs", "wudict.log")
	case "windows":
		dir := os.Getenv("LOCALAPPDATA")
		if dir == "" {
			return nil, errors.New("LOCALAPPDATA is not set")
		}
		path = filepath.Join(dir, "wudict", "wudict.log")
	default:
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		path = filepath.Join(home, ".wudict", "wudict.log")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	return os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
}

// applyLibrarySettings gives the subcommands that touch the library the same
// settings the server uses, so `wudict ingest` writes where the server looks:
// DB_DIR from the flag, the environment or wudict.toml alike.
func applyLibrarySettings() {
	cfg, err := config.Load("", nil)
	if err != nil {
		return // a broken config must not stop a local command
	}
	store.SetDBDir(cfg.DBDir)
	store.SetCompressBodies(!cfg.NoCompress)
	store.SetSmallBodies(cfg.Compression == config.CompressionSmall)
	if cfg.Verbose {
		logx.Enabled = true
	}
}

// nextPort suggests port+1 for the port-in-use hint.
func nextPort(p string) string {
	if n, err := strconv.Atoi(p); err == nil {
		return strconv.Itoa(n + 1)
	}
	return "8809"
}

// portHolderCmd is the command that names the process holding a port on this
// OS. Printed, not run: the hint is for a person, and running a diagnostic
// tool on their behalf from a failed startup is not this program's business.
func portHolderCmd(port string) string {
	if runtime.GOOS == "windows" {
		return "netstat -ano | findstr :" + port
	}
	return "lsof -i :" + port
}

// resolveSpeexdec locates the speexdec binary ONCE at startup (never at .spx
// playback time). Precedence: an explicit SPEEXDEC override, then a binary
// sitting next to the wudict executable, then $PATH. Returns the resolved
// path ("" if none found) and a short human-readable source label.
func resolveSpeexdec(override string) (path, source string) {
	name := "speexdec"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if override != "" {
		if isExecFile(override) {
			return override, "SPEEXDEC override"
		}
		if p, err := exec.LookPath(override); err == nil {
			return p, "SPEEXDEC override"
		}
		logx.Warn("SPEEXDEC=%q not found - falling back to auto-detection", override)
	}
	if exe, err := os.Executable(); err == nil {
		if cand := filepath.Join(filepath.Dir(exe), name); isExecFile(cand) {
			return cand, "next to executable"
		}
	}
	if p, err := exec.LookPath(name); err == nil {
		return p, "PATH"
	}
	return "", ""
}

// isExecFile reports whether p is an existing, executable regular file.
func isExecFile(p string) bool {
	fi, err := os.Stat(p)
	if err != nil || fi.IsDir() {
		return false
	}
	if runtime.GOOS == "windows" {
		return true
	}
	return fi.Mode()&0o111 != 0
}

// announceSpeex reports the active .spx audio backend at startup: the built-in
// libspeex decoder (cgo, default), the external speexdec binary (when forced or
// when built without cgo), or a warning with install pointers when neither is
// available.
// speexSummary describes the active .spx decoder in one phrase for the
// startup block; the install hint is printed separately when there is none.
func speexSummary(useExternal bool, path, source string) string {
	if !useExternal && speex.Available {
		if path != "" {
			return "built-in decoder (external speexdec at " + path + " as fallback)"
		}
		return "built-in decoder"
	}
	why := "SPEEX_BACKEND=external"
	if !speex.Available {
		why = "built without cgo"
	}
	if path != "" {
		return "external speexdec at " + path + " (" + why + ")"
	}
	return "" // none available - printStartup emits the install hint
}

// printStartup shows the *resolved* configuration that is in effect. All values
// are listed with the origin, i.e. flag, env, wudict.toml, default.
// loopbackAddr returns the address a loopback companion listener should bind,
// or "" when the main listener already accepts there.
//
// A wildcard covers loopback - Go opens 0.0.0.0 as a dual-stack [::] listen,
// and an empty host likewise - and a loopback bind obviously does. Anything
// ParseIP cannot read is left alone: "localhost" IS loopback, and resolving
// any other name here to decide would repeat a lookup net.Listen has already
// done, with no way to know which of several answers it took.
func loopbackAddr(ip, port string) string {
	p := net.ParseIP(ip)
	if p == nil || p.IsUnspecified() || p.IsLoopback() {
		return ""
	}
	if p.To4() != nil {
		return net.JoinHostPort("127.0.0.1", port)
	}
	return net.JoinHostPort("::1", port)
}

// startupInfo is what the startup summary describes: each folder counted by
// what IT contributed (a blended total next to the dictionary folder would
// make an empty folder look full when the library is in use).
type startupInfo struct {
	roots       []server.Root // dictionary folders, each with its own status
	inFolder    int           // dictionaries discovered across those folders
	fromLibrary int           // cached dictionaries actually serving (USE_CACHED)
	prepared    int           // cached dictionaries that exist, in use or not
	total       int           // what is being served
	libDir      string
	url         string
	moreURLs    []string // a wildcard bind's other LAN addresses, after url
	alsoURL     string   // loopback, "" when url is already loopback
	speex       string
	keyURL      string // url carrying the access key, "" when none is required
}

// userDir is the directory the wudict.toml in effect lives in - wudict's own
// space for everything the user owns but did not put in the config file. When
// no config file exists at all it falls back to the home location, the same
// directory EnsureConfigFile would have used, and when even the home directory
// is unknown it returns "": the caller then keeps its state in memory rather
// than scattering a file somewhere nobody asked for.
func userDir(cfgFile string) string {
	if cfgFile != "" {
		return filepath.Dir(cfgFile)
	}
	if p := config.HomeConfig(); p != "" {
		return filepath.Dir(p)
	}
	return ""
}

func printStartup(cfg config.Config, in startupInfo) {
	// Not os.Stderr: in a GUI launch the whole channel has been moved to a
	// log file, and the banner is the most useful thing in it (D74).
	out := logx.Output()
	fmt.Fprintf(out, "%s %s\n", ProductName, Version)

	// one line per folder, aligned in a single column, each with its own
	// status - an unmounted drive must be visible, not silently absent
	for i, root := range in.roots {
		label := "  dictionaries  "
		if i > 0 {
			label = "                "
		}
		note := logx.Plural(root.Count, "dictionary", "dictionaries")
		switch {
		case !root.Exists:
			note = "folder not found"
		case root.Total == 0:
			note = "no dictionaries found"
		case root.Count == 0:
			// overlapping folders: saying "none found" here would be a lie
			note = logx.Plural(root.Total, "dictionary", "dictionaries") + ", already listed above"
		case root.Total > root.Count:
			note = fmt.Sprintf("%s (+%d already listed above)",
				logx.Plural(root.Count, "dictionary", "dictionaries"), root.Total-root.Count)
		}
		fmt.Fprintf(out, "%s%s  (%s)\n", label, root.Path, note)
	}

	libNote := fmt.Sprintf("%d prepared", in.prepared)
	switch {
	case in.prepared == 0:
		libNote = "empty"
	case cfg.UseCached:
		libNote += fmt.Sprintf(", %d in use", in.fromLibrary)
	default:
		libNote += ", not in use"
	}
	fmt.Fprintf(out, "  library       %s  (%s)\n", in.libDir, libNote)

	// The three paths a user has to know - dictionaries, library, config - are
	// printed on every start, not just the first: a config file in the wrong
	// place is invisible otherwise (D32).
	switch cfgSrc := cfg.Source; {
	case cfgSrc == "":
		fmt.Fprintf(out, "  config        (none - built-in defaults)\n")
	case cfg.Portable:
		fmt.Fprintf(out, "  config        %s  (portable - beside the executable)\n", cfgSrc)
	default:
		fmt.Fprintf(out, "  config        %s\n", cfgSrc)
	}
	for _, p := range cfg.Shadowed {
		fmt.Fprintf(out, "                %s  (ignored - lower priority)\n", p)
	}
	for _, p := range cfg.Problems {
		fmt.Fprintf(out, "                ⚠ %s\n", p)
	}
	fmt.Fprintf(out, "  address       %s\n", in.url)
	// Capped: a machine running containers or VMs has a bridge per network,
	// and the address a person wants is the first one, the default route's.
	const moreShown = 3
	for i, u := range in.moreURLs {
		if i == moreShown {
			fmt.Fprintf(out, "                (+%d more)\n", len(in.moreURLs)-moreShown)
			break
		}
		fmt.Fprintf(out, "                %s\n", u)
	}
	// The second address is not a detail: it is the one that keeps working
	// when the machine changes networks, and the only one a bookmark can name.
	if in.alsoURL != "" {
		fmt.Fprintf(out, "                %s  (from this machine, whatever the network does)\n", in.alsoURL)
	}
	// A wildcard bind is not "the LAN", and the banner is the only place that
	// says so. Go turns 0.0.0.0 into a DUAL-STACK [::] listen, so on a mobile
	// connection - where the IPv6 address is globally routable and has no NAT
	// in front of it - this is a server on the public internet, with no
	// password on it. Printed at every start, because the setting is invisible
	// from inside the app.
	if ip := net.ParseIP(cfg.IP); ip != nil && ip.IsUnspecified() {
		fmt.Fprintf(out, "                open to every network this machine is on, including\n")
		if in.keyURL == "" {
			fmt.Fprintf(out, "                any public IPv6 address, and it asks for no password\n")
		} else {
			fmt.Fprintf(out, "                any public IPv6 address; the access key below is all\n")
			fmt.Fprintf(out, "                that stands in front of it\n")
		}
	}
	// The link, not the key: a key printed on its own is something the user
	// has to paste somewhere, and there is nowhere to paste it. Opened once,
	// the browser holds it in a cookie and the address bar goes back to being
	// the plain URL.
	if in.keyURL != "" {
		fmt.Fprintf(out, "  access key    needed from any browser but the one opened here:\n")
		fmt.Fprintf(out, "                %s\n", in.keyURL)
	}
	// Printed only when set, and always when set: opening the dictionary API
	// to a web page is the one setting here whose effect is invisible from
	// inside the app, so the banner is where it becomes visible again.
	if len(cfg.WebOrigins) > 0 {
		fmt.Fprintf(out, "  web origins   %s\n", webOriginsNote(cfg.WebOrigins))
	}
	if in.speex != "" {
		fmt.Fprintf(out, "  .spx audio    %s\n", in.speex)
	}
	fmt.Fprintf(out, "  indexing      %s\n", indexingSummary(cfg.AutoIndex))
	fmt.Fprintf(out, "  index workers %s%s\n",
		logx.Plural(cfg.IndexWorkers, "dictionary at a time", "dictionaries at a time"),
		memLimitNote(cfg.MemoryLimit))
	fmt.Fprintf(out, "  preview memory %s%s\n", budgetNote(cfg.PreviewMemory), searchBudgetNote(cfg.SearchMemory))
	fmt.Fprintf(out, "  serving       %s\n", logx.Plural(in.total, "dictionary", "dictionaries"))

	// what to do next, when there is something to do
	switch {
	case in.total == 0:
		if in.prepared > 0 && !cfg.UseCached {
			fmt.Fprintf(out, "\nopen %s - choose a dictionary folder, or use the %s already prepared\n",
				in.url, logx.Plural(in.prepared, "dictionary", "dictionaries"))
		} else {
			fmt.Fprintf(out, "\nopen %s to choose your dictionary folder\n", in.url)
		}
	case in.prepared > 0 && !cfg.UseCached:
		fmt.Fprintf(out, "\n%s previously imported - enable with --use-cached (or on the setup page)\n",
			logx.Plural(in.prepared, "dictionary", "dictionaries"))
	}
	if in.speex == "" {
		fmt.Fprintf(out, "\nnote: no .spx decoder available - Speex audio will not play.\n"+
			"  Build with cgo for the built-in decoder, or install speex / set SPEEXDEC=/path/to/speexdec:\n"+
			"    macOS:    brew install speex\n"+
			"    Linux:    sudo apt install speex   (or your distro's speex package)\n"+
			"    Windows:  https://www.speex.org/downloads/\n")
	}
}

// multiFlag collects a repeatable flag: folders (`--dict-dir A --dict-dir B`),
// a dump's -include and -exclude patterns. Repetition rather than a separator
// inside one value: nothing to escape - a resource name can hold a comma - and
// it reads the same on every platform. String joins with
// os.PathListSeparator so a folder list travels through the same string-keyed
// config layering as the environment variable, which follows the $PATH
// convention.
type multiFlag []string

func (m *multiFlag) String() string { return strings.Join(*m, string(os.PathListSeparator)) }
func (m *multiFlag) Set(v string) error {
	*m = append(*m, v)
	return nil
}

// keyURL appends the access key to the server's own URL, producing the one
// string a user can act on: a link that works. Empty in, empty out - no key
// required means there is nothing to show, and a banner line that says
// "access key: (none)" is an invitation to go looking for one.
//
// The key is query-escaped: a generated one is base64url and passes through
// unchanged, but an AUTH_TOKEN is whatever the user typed, and the server
// reads k decoded - a raw '+' would arrive as a space, '&' or '#' would cut
// it short.
func keyURL(base, token string) string {
	if token == "" {
		return ""
	}
	sep := "?"
	if strings.Contains(base, "?") {
		sep = "&"
	}
	return base + sep + "k=" + neturl.QueryEscape(token)
}

// browserURL is the link this machine's own browser is sent to: the address
// probeHost would ask - a wildcard bind becomes loopback, because Windows
// cannot connect to 0.0.0.0 or [::] at all and browsers refuse them - with
// the key when one is required. Never empty, unlike keyURL: no key means
// the plain address, not nothing.
func browserURL(addr, token string) string {
	u := "http://" + probeHost(addr) + "/"
	if token == "" {
		return u
	}
	return keyURL(u, token)
}

// webOriginsNote renders WEB_ORIGINS for the startup block. A wildcard is
// spelled out rather than printed as "*": the difference between one allowed
// site and all of them is the difference worth reading at a glance.
func webOriginsNote(origins []string) string {
	for _, o := range origins {
		if o == "*" {
			return "any website may read your dictionaries (WEB_ORIGINS = \"*\")"
		}
	}
	return strings.Join(origins, ", ") + "  (may read your dictionaries)"
}

// budgetNote describes the preview-memory cap for the startup block.
func budgetNote(b int64) string {
	if b <= 0 {
		return "unlimited - dictionaries stay open once used"
	}
	return fmt.Sprintf("%.1f GB for dictionaries that are not yet prepared", float64(b)/(1<<30))
}

// searchBudgetNote appends the per-search materialisation cap when one is in
// force. Said out loud because it is the one setting here that can change what
// a search *returns* - a dictionary it declines is reported as not searched,
// and a user seeing that deserves to know a number caused it.
func searchBudgetNote(b int64) string {
	if b <= 0 {
		return ""
	}
	return fmt.Sprintf("  ·  %d MB per search", b>>20)
}

// memLimitNote appends the soft heap ceiling to the startup line when set.
func memLimitNote(limit int64) string {
	if limit <= 0 {
		return ""
	}
	return fmt.Sprintf("  ·  memory limit %.1f GB", float64(limit)/(1<<30))
}

func indexingSummary(autoIndex string) string {
	if autoIndex == config.AutoIndexOff {
		return "off - dictionaries are searched through their own format (AUTO_INDEX=off)"
	}
	return "on - a headword index is prepared on first search; full-text and media on request"
}

func cmdSearchAll(args []string) error {
	applyLibrarySettings()
	fs := flag.NewFlagSet("searchall", flag.ExitOnError)
	// exact by default: the term a person types on this command line is the
	// word they want, and prefix over a whole library answers a query nobody
	// asked - 97 dictionaries offering `Casablanca` for `casa`.
	modeStr := fs.String("mode", "exact", "exact|prefix|fts|wildcard")
	n := fs.Int("n", 10, "max results per dictionary")
	format := fs.String("format", "text", "text|clean|raw|list - list prints headwords only")
	base := fs.String("base", "", "prefix for /res/… references (e.g. http://127.0.0.1:6888)")
	var dictDirs multiFlag
	fs.Var(&dictDirs, "dict-dir", "folder to search; repeat for several (env/toml: DICT_DIR)")
	configPath := fs.String("config", "", "path to wudict.toml (env: CONFIG_PATH)")
	fs.Parse(args)

	// The folder is optional now, so the term is the last argument either way.
	// Two arguments still mean the original `searchall <dir> <term>`.
	var dirArg, term string
	switch fs.NArg() {
	case 1:
		term = fs.Arg(0)
	case 2:
		dirArg, term = fs.Arg(0), fs.Arg(1)
	default:
		return fmt.Errorf("usage: wudict searchall [-mode m] [-n perDict] [-format f] [-dict-dir path] [<dir>] <term>")
	}

	mode, err := search.ParseMode(*modeStr)
	if err != nil {
		return err
	}
	// Before any dictionary opens: every one would report the same error.
	if mode == search.Wildcard {
		if _, err := wildcard.Parse(term); err != nil {
			return err
		}
	}
	// A search that prints only headwords answers "does this word exist", which
	// is not what anyone runs a dictionary for. `text` is the default because
	// it is the readable one, and because it is the smallest: raw markup is
	// 2.6x its size (D61) and a terminal cannot render it anyway. `list` prints
	// headwords only, for the times the question really is "which
	// dictionaries have this".
	bodies := *format != "list"
	var articleFormat string
	if bodies {
		f, err := server.ParseArticleFormat(*format)
		if err != nil {
			return fmt.Errorf("%w (or \"list\" for headwords only)", err)
		}
		articleFormat = f
	}

	// Where to search, resolved the way every other setting is: an explicit
	// argument, else --dict-dir, else DICT_DIR from the environment, else
	// wudict.toml, else the default folder (config.Load applies that chain), so
	// `searchall` finds the library the rest of the program is configured with.
	if *configPath == "" {
		*configPath = os.Getenv("CONFIG_PATH")
	}
	cfg, err := config.Load(*configPath, map[string]string{"DICT_DIR": dictDirs.String()})
	if err != nil {
		return err
	}
	limits.Set(cfg.Limits)
	dirs := cfg.DictDirs
	origin := cfg.Origin("DICT_DIR")
	if dirArg != "" {
		dirs, origin = folderArgs([]string{dirArg}), "argument"
	}
	paths, _, err := dict.DiscoverAll(dirs)
	if err != nil {
		return err
	}
	if len(paths) == 0 {
		return fmt.Errorf("no dictionaries found in %s (%s)", strings.Join(dirs, ", "), origin)
	}
	// stderr, so stdout stays exactly the results: the folder searched is not
	// obvious once it comes from a config file rather than the command line.
	fmt.Fprintf(os.Stderr, "searching %s in %s (%s)\n",
		logx.Plural(len(paths), "dictionary", "dictionaries"), strings.Join(dirs, ", "), origin)

	// Opened inside the worker, closed as soon as that dictionary has answered:
	// holding every one until the command exits would materialise a large
	// library's whole corpus at once (docs.local/PERF.md §8.7). Peak is the
	// worker count, and the first dictionary's results print while the rest are
	// still opening.
	//
	// opened[i] is written by worker i and read only by its own emit call,
	// which StreamOpen runs on that same goroutine - one writer, one reader,
	// no sharing.
	opened := make([]dict.Dictionary, len(paths))
	openers := make([]search.Opener, len(paths))
	for i, p := range paths {
		i, p := i, p
		openers[i] = func() (dict.Dictionary, error) {
			d, err := dict.Open(p)
			opened[i] = d
			return d, err
		}
	}

	w := bufio.NewWriter(os.Stdout)
	defer w.Flush()
	var found int
	search.StreamOpen(context.Background(), openers, mode, term, *n, func(i int, h search.Hit) {
		d := opened[i]
		if d != nil {
			defer func() { d.Close(); opened[i] = nil }()
		}
		name := h.Meta.Name
		if name == "" {
			name = filepath.Base(paths[i]) // an open that failed carries no Meta
		}
		switch {
		case h.Skipped:
			return
		case h.Err != nil:
			logx.Warn("%ssearch failed: %v", logx.Dict(name), h.Err)
			return
		case len(h.Results) == 0:
			return
		}
		found++
		if bodies {
			// The same reduction /api/search performs, from the same code.
			server.FormatArticles(d, articleFormat, strings.TrimSuffix(*base, "/"), h.Results)
		}
		fmt.Fprintf(w, "== %s (%d)\n", name, len(h.Results))
		for _, r := range h.Results {
			if bodies {
				fmt.Fprintf(w, "<!-- %s -->\n%s\n", r.Headword, r.Body)
			} else {
				fmt.Fprintf(w, "  %s\n", r.Headword)
			}
		}
		// One flush per dictionary: this is the streaming the API does, and it
		// is what makes `| head` and a piped reader work on a slow library.
		w.Flush()
	})
	if found == 0 {
		return dict.ErrNotFound
	}
	return nil
}

func cmdClean(args []string) error {
	applyLibrarySettings()
	fs := flag.NewFlagSet("clean", flag.ExitOnError)
	force := fs.Bool("f", false, "actually delete (default: dry run, list only)")
	withOrphans := fs.Bool("orphans", false, "with -f, also delete prepared dictionaries whose source file is gone")
	fs.Parse(args)
	// Before the relink below, which moves prepared folders too.
	if err := refuseUnderServer("leave the tidying to it"); err != nil {
		return err
	}

	// A source that was moved inside a dictionary folder takes its prepared
	// folder with it, exactly as a server rescan does - so a move is never
	// reported, let alone deleted, as an orphan (D156).
	if cfg, err := config.Load("", nil); err == nil {
		if found, _, err := dict.DiscoverAll(cfg.DictDirs); err == nil {
			for _, rl := range store.Relink(found) {
				fmt.Printf("relinked  %s\n  moved from %s to %s\n", rl.Dir, rl.From, rl.To)
			}
		}
	}

	leftovers, err := store.FindLeftovers()
	if err != nil {
		return err
	}
	orphans, err := store.FindOrphans(isHowtoSource)
	if err != nil {
		return err
	}
	if len(leftovers) == 0 && len(orphans) == 0 {
		fmt.Println("library is clean - nothing to remove")
		return nil
	}
	var total int64
	for _, o := range leftovers {
		total += o.Size
		kind := "file"
		if o.IsDir {
			kind = "folder"
		}
		fmt.Printf("%s  (%s, %s)\n  %s\n", o.Path, kind, logx.Size(o.Size), o.Reason)
	}
	if len(leftovers) > 0 {
		fmt.Printf("%d items, %s total\n", len(leftovers), logx.Size(total))
	}
	var orphanTotal int64
	if len(orphans) > 0 {
		if len(leftovers) > 0 {
			fmt.Println()
		}
		fmt.Println("orphans - prepared dictionaries whose source file is gone:")
		for _, o := range orphans {
			orphanTotal += o.Size
			fmt.Printf("%s  (%s, %s)\n  source was %s\n", o.Dir, o.Name, logx.Size(o.Size), o.Source)
		}
		fmt.Printf("%d orphans, %s total\n", len(orphans), logx.Size(orphanTotal))
	}
	if !*force {
		switch {
		case len(orphans) == 0:
			fmt.Println("dry run - re-run with -f to delete")
		case len(leftovers) == 0:
			fmt.Println("dry run - re-run with -f -orphans to delete the orphans")
		default:
			fmt.Println("dry run - re-run with -f to delete the items, -f -orphans to delete the orphans as well")
		}
		return nil
	}
	var failed, deleted int
	for _, o := range leftovers {
		var err error
		if o.IsDir {
			err = os.RemoveAll(o.Path)
		} else {
			err = os.Remove(o.Path)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "delete %s: %v\n", o.Path, err)
			failed++
			continue
		}
		deleted++
	}
	if *withOrphans {
		// Re-judged one by one at deletion time (store.RemoveOrphan), so a
		// source that came back since the listing keeps its folder.
		for _, o := range orphans {
			if _, err := store.RemoveOrphan(o.Folder, isHowtoSource); err != nil {
				fmt.Fprintf(os.Stderr, "delete %s: %v\n", o.Dir, err)
				failed++
				continue
			}
			deleted++
		}
	} else if len(orphans) > 0 {
		fmt.Printf("%d orphans left in place - add -orphans to delete them\n", len(orphans))
	}
	fmt.Printf("deleted %d items\n", deleted)
	if failed > 0 {
		return fmt.Errorf("%d deletions failed", failed)
	}
	return nil
}

// isHowtoSource reports whether src is the built-in guide the server writes
// beside its config (<config dir>/builtin/<howto file>). Its source comes and
// goes with the app, not with the user's folders, so it is never an orphan.
func isHowtoSource(src string) bool {
	return filepath.Base(src) == howto.FileName && filepath.Base(filepath.Dir(src)) == "builtin"
}

// cmdToken prints the access key as a link, and rotates it on request.
//
// A link rather than the bare key, for the same reason the startup banner
// prints one: the key is not something a person can use, and the only thing
// they can do with it is open it. Printed for the address the server is
// CONFIGURED to listen on, resolved through the same layering the server
// itself uses, so the link is the one that will actually reach it.
func cmdToken(args []string) error {
	fs := flag.NewFlagSet("token", flag.ExitOnError)
	rotate := fs.Bool("rotate", false, "replace the key: every browser and script holding the old one loses access")
	configPath := fs.String("config", "", "path to wudict.toml (env: CONFIG_PATH)")
	fs.Parse(args)
	if *configPath == "" {
		*configPath = os.Getenv("CONFIG_PATH")
	}
	cfg, err := config.Load(*configPath, nil)
	if err != nil {
		return err
	}
	// A key supplied through the environment or the file is not ours to
	// rotate: the file is where the user put it, and rewriting ~/.wudict/token
	// would silently do nothing to the value actually in force.
	if cfg.AuthToken != "" {
		if *rotate {
			return fmt.Errorf("AUTH_TOKEN is set (%s), so the key comes from there, not from %s:\n"+
				"  change it where it is set, or unset it to use the key file", cfg.Source, config.TokenPath())
		}
		fmt.Println(keyURL("http://"+cfg.Addr()+"/", cfg.AuthToken))
		return nil
	}
	tok, err := config.LoadToken(*rotate)
	if err != nil {
		return err
	}
	fmt.Println(keyURL("http://"+cfg.Addr()+"/", tok))
	if !config.AuthRequired(cfg.Auth, cfg.IP) {
		fmt.Fprintf(os.Stderr, "note: this server does not ask for the key (AUTH is off, or it listens on %s only).\n"+
			"      The link still works; set AUTH = \"on\" to require it.\n", cfg.IP)
	}
	return nil
}
