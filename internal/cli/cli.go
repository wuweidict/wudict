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
	"io"
	"net"
	"net/http"
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
	"github.com/wuweidict/wudict/internal/logx"
	"github.com/wuweidict/wudict/internal/morph"
	"github.com/wuweidict/wudict/internal/resource"
	"github.com/wuweidict/wudict/internal/search"
	"github.com/wuweidict/wudict/internal/server"
	"github.com/wuweidict/wudict/internal/speex"
	"github.com/wuweidict/wudict/internal/store"
	"github.com/wuweidict/wudict/internal/tray"

	_ "github.com/wuweidict/wudict/internal/format/bgl"      // register .bgl
	_ "github.com/wuweidict/wudict/internal/format/dsl"      // register .dsl(.dz)
	_ "github.com/wuweidict/wudict/internal/format/mdx"      // register .mdx
	_ "github.com/wuweidict/wudict/internal/format/slob"     // register .slob
	_ "github.com/wuweidict/wudict/internal/format/stardict" // register .ifo
	_ "github.com/wuweidict/wudict/internal/format/zim"      // register .zim
)

// Version is stamped by the Makefile via
// -ldflags "-X github.com/wuweidict/wudict/internal/cli.Version=…".
// It lives here rather than in main because this package, not the one-line
// shim at the module root, is where the program is.
var Version = "dev"

// Product identity, in one place. The binary is `wudict`; the name people
// read is WuWeiDict. Anything user-facing - CLI banner, web UI
// About box, setup page - sources its wording from here or mirrors it.
const (
	ProductName = "wuDict"
	Tagline     = "Search every dictionary you own, from one browser tab."
	SiteURL     = "https://wuweidict.github.io/wudict"
	RepoURL     = "https://github.com/wuweidict/wudict"
)

func usage() string {
	return fmt.Sprintf(`WuWeiDict %s - multi-format dictionary server
MDict (.mdx/.mdd) · StarDict (.ifo) · Aard2 (.slob) · Lingvo DSL (.dsl/.dsl.dz) · openZIM (.zim) · WuWeiDict (.text.db)

USAGE
  wudict [command] [flags] [args]

  Running wudict with no arguments (or with only flags) starts the
  HTTP server - the same as the "serve" command.

  wudict <dictfile>                       Serve the folder that holds that file
                                          and open it in the browser. This is what
                                          double-clicking a dictionary does.

COMMANDS
  serve                                   Start the HTTP server (the default command)
  list   <dir> [dir…]                     Discover dictionaries under one or more folders
  info   <dictfile>                       Show dictionary metadata, capabilities, and every file
                                          on disk that belongs to it, with sizes
  lookup [-n max] <dictfile> <word>       Exact lookup (accent-fold fallback); HTML to stdout
                                          By default uses -format=raw, pass -format=text for plain text
  prefix [-n max] <dictfile> <word>       Exact-else-prefix lookup (accent-insensitive); HTML to stdout
  contains [-n max] <dictfile> <word>     Substring headword search (FTS5 trigram; ingested dicts only)
  fts    [-n max] <dictfile> <query>      FTS5 full-text search (ingested dicts only)
                                          All four take -format raw|clean|text, the same
                                          three the HTTP API offers: raw is the dictionary's
                                          own HTML (the default), clean drops scripts, styles
                                          and presentation, text drops all markup.
  keys   [-offset N] [-n count] <dictfile>  List headwords (all by default).
                                          On an .mdd, lists the files it holds -
                                          the same key/value format, so the same
                                          command. No .mdx needed.
  res    [-o out] [-f] <dictfile> <name>  Extract one resource (e.g. "audio/word.mp3").
                                          Takes an .mdd directly too; any name
                                          that keys printed is one this accepts.
                                          Piped or redirected, the bytes go to stdout.
                                          On a terminal they are written to a file
                                          named after the resource instead (-f to
                                          overwrite). -o names the output: a path
                                          (parents created), a directory, or "-"
                                          for stdout.
  dump   -o <outdir> <dictfile>           Write the whole dictionary out as CSV in
                                          pyglossary's import/export layout, so any
                                          format its converter writes is reachable
                                          from here. Resources are unpacked beside
                                          it into <name>.csv_res. -output is the
                                          long form of -o.
  ingest [-full] [-headwords] [-contains] <dictfile|folder…>
                                          Prepare a dictionary into the library:
                                          <db-dir>/<dictionary name>/text.db (+ info.txt).
                                          A folder prepares everything in it, skipping what
                                          is already done. By default this builds headword
                                          and full-text indexes; -headwords skips full text
                                          (much smaller), -contains adds the substring index
                                          (roughly doubles a headwords-only db), and -full
                                          also packs media.db into the same folder.
  searchall [-mode m] [-n perDict] [-format f] [<dir>] <term>
                                          Concurrent search across every dictionary, printed as
                                          each one answers. Without <dir> it searches the
                                          configured DICT_DIR (--dict-dir, env, or wudict.toml);
                                          <dir> may be a "a:b" list, as in DICT_DIR.
                                          -format text (default) prints the definitions;
                                          clean|raw print them as markup, as in lookup;
                                          list prints headwords only.
  lemmas list | download <lang…> | remove <lang…>
                                          Manage the lemma data that lets a search for an
                                          inflected word find its dictionary form ("knew" ->
                                          "know"). English is built in; every other language
                                          is a file in LEMMA_DIR. "list" marks the installed
                                          ones; "download pl ru" or "download polish russian"
                                          installs them from LEMMA_URL; "remove pl" deletes.
  clean  [-f]                             List removable items in the library: incomplete or
                                          unreadable folders, interrupted ingests, leftovers
                                          from the old flat layout. A cached dictionary is
                                          never listed, even if its source is gone or changed.
                                          -f deletes them. Dry run by default.
  rm [-f] [-keep-source|-keep-index] <name|path>
                                          Remove one dictionary: its prepared folder in the
                                          library AND its original files. The argument is a
                                          library name, a folder, a text.db or the path of an
                                          original dictionary file. -keep-source deletes only
                                          the prepared folder (it will be prepared again on
                                          the next search if the original is still in a
                                          scanned folder); -keep-index deletes only the
                                          originals, and refuses while media is unpacked.
                                          Lists what it would delete; -f deletes it.

SERVE FLAGS
  --dict-dir     <path>   Folder with dictionary files (scanned recursively).
                          Repeat the flag for several folders:
                            --dict-dir ~/Dictionaries --dict-dir /Volumes/Ext/Dicts
                          env: DICT_DIR       toml: DICT_DIR
                            DICT_DIR="~/Dictionaries:/Volumes/Ext/Dicts"   (";" on Windows)
                            DICT_DIR = ["~/Dictionaries", "/Volumes/Ext/Dicts"]
                          A dictionary found in two folders is listed once (the
                          first folder wins); a missing folder is reported and
                          the others still work.
                          default: ~/Dictionaries

  --db-dir       <path>   Library folder: one subfolder per cached dictionary,
                          holding text.db (+ media.db, info.txt). Must not be the
                          same folder as --dict-dir.
                          env: DB_DIR         toml: DB_DIR
                          default: ~/.wudict/db

  (env/toml only)
  AUTO_INDEX = "on"       On first search, a dictionary prepares its own headword
                          index in the background (exact, prefix, accent-insensitive;
                          a couple of MB). "off" leaves dictionaries to be searched
                          through their own format. Contains, full-text and media
                          stay per-dictionary choices either way.
                          env: AUTO_INDEX     toml: AUTO_INDEX
                          default: on

  --index-workers <n>     How many dictionaries may be prepared at once. Preparing
                          one saturates a core and holds a few hundred bytes per
                          headword, so the default is 1 - background work must not
                          take the machine away from you. "auto" (or 0) = every core.
                          env: INDEX_WORKERS  toml: INDEX_WORKERS
                          default: 1

  (env/toml only)
  PREVIEW_MEMORY = "1GB"  How much RAM dictionaries that are not yet prepared may
                          hold open (~350 bytes per headword each). The least
                          recently used are closed above this; prepared ones
                          answer from disk and are never evicted. "0" = no limit.
                          env: PREVIEW_MEMORY toml: PREVIEW_MEMORY
                          default: 1GB (Android: a third of MEMORY_LIMIT,
                          64-128MB by device RAM)

  SEARCH_MEMORY = "512MB" How much RAM ONE search may bring into memory by opening
                          dictionaries that are not yet prepared. Past it, the
                          remaining ones are reported as not searched instead of
                          opened - the only setting here that changes what a
                          search returns, and it never applies to prepared
                          dictionaries, which cost nothing to search. "0" = no cap.
                          env: SEARCH_MEMORY  toml: SEARCH_MEMORY
                          default: none (Android: the memory limit)

  MORPH_CACHE = "2"       How many lemma packs stay in memory. When a search finds
                          nothing in any dictionary, wudict looks the word's
                          dictionary form up instead - "knew" finds "know" - in
                          dictionaries whose language it can tell. English is
                          built in; every other language is a file you install
                          (see LEMMA_DIR). Each loaded language costs 7-65 MB;
                          the least recently used is dropped above this.
                          "0" = never lemmatize and load nothing.
                          env: MORPH_CACHE    toml: MORPH_CACHE
                          default: 2 (Android: 1)

  LEMMA_DIR = "~/.wudict/lemmas"
                          Folder of installed lemma files. Every language except
                          English comes from here - Spanish, Russian, Polish and
                          the rest are downloaded, not built in. One file per
                          language, named after it: "pl.txt", "pol.tsv",
                          "polish.txt.gz". Each line is
                          a lemma followed by its forms, tab-separated and lower
                          case - the format of the lists at
                          github.com/michmech/lemmatization-lists, which work as
                          downloaded. An "en" file replaces the built-in English.
                          Read once at startup.
                          env: LEMMA_DIR      toml: LEMMA_DIR
                          default: ~/.wudict/lemmas

  LEMMA_URL = "https://…/manifest.json"
                          Where "wudict lemmas" looks for installable languages.
                          A static catalogue naming each language, its size and
                          its sha256 - not a code-hosting API, whose 60-requests-
                          per-hour-per-IP limit would break "lemmas list" for
                          anyone behind a shared address. May be a path to a
                          manifest.json on disk instead, to install with no
                          network at all; every check still applies, because the
                          hashes are what is trusted, not the transport.
                          env: LEMMA_URL      toml: LEMMA_URL
                          default: the published catalogue

  MEMORY_LIMIT = "4GB"    Soft heap ceiling: Go collects harder - and drops its
                          caches - rather than growing past it. "0" = none.
                          env: MEMORY_LIMIT   toml: MEMORY_LIMIT
                          default: none (Android: a fraction of device RAM)

  --no-compress           Store article text uncompressed. Prepared databases are
                          roughly 3x larger; reads are marginally faster. Only
                          worth it with plenty of disk to spare.
                          env: NO_COMPRESS    toml: NO_COMPRESS
                          default: off (article text is compressed)

  --use-cached            Also serve the previously imported dictionaries kept in
                          the library, whether or not their original files are still
                          present. Normally set by clicking "Use these dictionaries"
                          on the first-run setup page.
                          env: USE_CACHED     toml: USE_CACHED
                          default: off

  (env/toml only)
  BROWSER_EXTENSIONS      Which browser extensions may look words up in this
                          server from the pages they run in. Blank = any
                          installed extension; it reaches the dictionary API
                          (/api/dicts, /api/search, /res/) and nothing else -
                          never your settings, preferences or library. Pin it
                          to exact origins to allow only those:
                            BROWSER_EXTENSIONS = ["chrome-extension://abc…"]
                          (Firefox regenerates moz-extension:// per install,
                          so there is no stable origin to pin there.)
                          env: BROWSER_EXTENSIONS  toml: BROWSER_EXTENSIONS
                          default: any extension

  WEB_ORIGINS             Which web pages may look words up in this server with
                          JavaScript. Blank = none, and a page in a browser
                          cannot reach wudict at all. List the exact origins of
                          your own pages - scheme, host and port, no path - to
                          let them read the same three endpoints an extension
                          reaches, and nothing else:
                            WEB_ORIGINS = ["http://localhost:3000"]
                          "*" allows every site you visit: useful while
                          developing, a standing invitation otherwise.
                          (Scripts outside a browser - curl, Node, your own
                          program - are not affected by any of this.)
                          env: WEB_ORIGINS    toml: WEB_ORIGINS
                          default: no web page

  --ip           <addr>   Listen IP address
                          env: SERVER_IP      toml: SERVER_IP
                          default: 127.0.0.1

  --port         <port>   Listen port
                          env: SERVER_PORT    toml: SERVER_PORT
                          default: 6888

  --config       <path>   Path to wudict.toml (overrides auto-detect)
                          env: CONFIG_PATH

  --no-browser            Do not open a browser tab on startup
                          env/toml: NO_BROWSER=1

  --verbose               Verbose logging (requests, dictionary opens,
                          ingest, transcodes) for easy debugging
                          env/toml: VERBOSE=1  (works for all commands)

  --speexdec     <path>   Path to speexdec binary (.spx audio is
                          transcoded to WAV - browsers cannot play Speex)
                          env: SPEEXDEC       toml: SPEEXDEC
                          default: speexdec found on PATH, else /usr/bin/speexdec

  -v, --version           Show version and exit  (verbose is --verbose)
  -h, --help              Show this help and exit

CONFIG FILE SEARCH ORDER
  1. --config flag / CONFIG_PATH env var
  2. <executable-dir>/wudict.toml   (portable mode - see below)
  3. ~/.wudict/wudict.toml
  4. /etc/wudict/wudict.toml
  On the first "serve" run a fully commented ~/.wudict/wudict.toml is
  generated, and the file in effect is printed at every startup.

PORTABLE MODE
  Put a wudict.toml next to the executable and it wins, and is where
  settings are saved - for a USB stick or a self-contained folder.
  wudict never creates that file on its own: an executable directory is
  usually somebody else's (~/go/bin, /opt/homebrew/bin), not ours.

PRIORITY (highest → lowest)
  CLI flag  >  environment variable  >  wudict.toml  >  built-in default

FIRST RUN
  If the dictionary folder is missing or empty, the web UI shows a setup
  page: paste a folder path, it is validated live, and the choice is
  saved to wudict.toml - no restart needed.

LIBRARY FOLDER
  Preparing a dictionary creates <DB_DIR>/<name>/ holding text.db,
  media.db and info.txt - one folder per dictionary, the unit you copy
  or hand over.

  A res/ subfolder there replaces - or supplies - the files a dictionary
  ships. Articles load their stylesheets, scripts and media from inside
  the dictionary file; res/ is consulted FIRST, so a file there is used
  whether or not the dictionary has one of that name:

    <DB_DIR>/Cambridge English Dictionary Online/res/jquery.js
    <DB_DIR>/Stanford Encyclopedia/res/js/entry.js

  Subfolders work, and articles routinely use them (js/…, css/…), so
  mirror the path the article asks for. That path is the one in the
  /res/<id>/<name> URL seen in the browser's network panel - a 404 there
  is the "missing resource" case. Overrides are served uncached, so a
  reload picks up an edit. Nothing inside the dictionary is modified;
  delete the file to go back. One exception: a .spx in res/ is served
  as-is rather than transcoded to WAV, so supply .mp3 or .wav instead.

  This exists because a dictionary can ship a DAMAGED file, and since
  its own scripts usually load a library first, one bad file can
  silently disable every interactive part of its articles. wudict warns
  when it serves a .js/.css/.html/.json/.xml/.svg/.txt containing a NUL
  byte - impossible in those formats, so proof the stored copy is
  broken - and names the res/ path that would override it. The bytes
  themselves are always served exactly as stored.

EXAMPLE wudict.toml
  DICT_DIR    = "/data/dicts"
  DB_DIR      = "~/.wudict/db"
  SERVER_IP   = "0.0.0.0"
  SERVER_PORT = "9000"
  NO_BROWSER  = "1"

EXAMPLES
  wudict
  wudict --dict-dir ~/Books/Dicts --port 9090 --no-browser
  wudict lookup ~/Dicts/Oxford.mdx serendipity
  wudict ingest -full ~/Dicts/Oxford.mdx
  SERVER_PORT=9000 wudict

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
	case "lookup", "prefix", "contains", "fts":
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
	case "clean":
		err = cmdClean(args)
	case "rm", "remove":
		err = cmdRemove(args)
	case "-h", "--help", "help":
		fmt.Print(usage())
	case "-v", "--version", "version":
		fmt.Println(ProductName, Version)
		fmt.Println(RepoURL)
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
	fmt.Printf("name:        %s\nformat:      %s\npath:        %s\nentries:     %d\ncapabilities: exact=%v prefix=%v contains=%v fts=%v\n",
		m.Name, m.Format, m.Path, m.EntryCount, c.Exact, c.Prefix, c.Contains, c.FTS)
	if m.IndexLang != "" {
		// Only when the dictionary DECLARED it. A language derived from the
		// file name is the search path's fallback, not a fact about the file,
		// and printing it here would make a guess look like metadata.
		fmt.Printf("language:    %s (declared)\n", m.IndexLang)
	}
	if m.Description != "" {
		fmt.Printf("description: %s\n", m.Description)
	}
	printDictFiles(files)
	return nil
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
// types, and without this it fell through to the "original" branch and was
// reported as one opaque file.
func dictFiles(arg string) []dictFile {
	abs, err := filepath.Abs(arg)
	if err != nil {
		abs = arg
	}
	abs = dict.MainFile(abs)
	var out []dictFile
	add := func(kind, p string) {
		if n := store.TreeSize(p); n > 0 || fileExists(p) {
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
		fmt.Printf("  %-8s  %-*s  %10s\n", f.kind, w, f.path, humanSize(f.size))
	}
	fmt.Printf("  %-8s  %-*s  %10s\n", "total", w, "", humanSize(total))
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
	d, err := dict.Open(fs.Arg(0))
	if err != nil {
		return err
	}
	defer d.Close()
	var results []dict.Result
	switch mode {
	case "lookup":
		results, err = d.Exact(fs.Arg(1), *n)
	case "prefix":
		results, err = d.Prefix(fs.Arg(1), *n)
	case "contains":
		f, ok := d.(dict.ContainsSearcher)
		if !ok {
			return dict.ErrUnsupported
		}
		results, err = f.Contains(fs.Arg(1), *n)
	case "fts":
		f, ok := d.(dict.FullTextSearcher)
		if !ok {
			return dict.ErrUnsupported
		}
		results, err = f.FullText(fs.Arg(1), *n)
	}
	if err != nil {
		return err
	}
	if len(results) == 0 {
		return dict.ErrNotFound
	}
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
	out := fs.String("o", "", "output .db path (default: <db-dir>/<dictionary name>/text.db)")
	full := fs.Bool("full", false, "also pack binary resources into a companion .media.db")
	headwords := fs.Bool("headwords", false, "index headwords only: smaller db, no full-text search")
	fuzzyOnly := fs.Bool("fuzzy-only", false, "deprecated spelling of -headwords")
	contains := fs.Bool("contains", false, "also build the substring (contains) index - roughly doubles a headword-only index")
	fs.Parse(args)
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: wudict ingest [-o out.db] [-full] [-headwords] [-contains] <dictfile|folder…>")
	}
	level := store.LevelText
	if *headwords || *fuzzyOnly {
		level = store.LevelHeadwords
	}
	plan := store.Plan{FullText: level == store.LevelText, Contains: *contains}
	srcPath := fs.Arg(0)

	// folder: ingest every dictionary found under it
	if st, err := os.Stat(folderArgs([]string{srcPath})[0]); err == nil && st.IsDir() {
		if *out != "" {
			return fmt.Errorf("-o cannot be used with a folder")
		}
		paths, _, err := dict.DiscoverAll(folderArgs(fs.Args()))
		if err != nil {
			return err
		}
		if len(paths) == 0 {
			return fmt.Errorf("no dictionaries found under %s", srcPath)
		}
		var failed int
		for i, p := range paths {
			fmt.Fprintf(os.Stderr, "[%d/%d] %s\n", i+1, len(paths), filepath.Base(p))
			if err := ingestOne(p, "", *full, plan); err != nil {
				fmt.Fprintf(os.Stderr, "  failed: %v\n", err)
				failed++
			}
		}
		if failed > 0 {
			return fmt.Errorf("%d of %d dictionaries failed", failed, len(paths))
		}
		return nil
	}
	return ingestOne(srcPath, *out, *full, plan)
}

func ingestOne(srcPath, out string, full bool, plan store.Plan) error {
	r, err := dict.OpenReader(srcPath)
	if err != nil {
		return err
	}
	defer r.Close()

	name := r.Meta().Name
	if name == "" {
		name = filepath.Base(srcPath)
	}
	dbPath := out
	if dbPath == "" {
		// claim this source's library folder: <db dir>/<source name>/text.db
		dbPath, err = store.PrepareTarget(srcPath)
		if err != nil {
			return err
		}
	}
	if _, err := os.Stat(dbPath); err == nil && out == "" {
		m, _ := store.ReadMeta(dbPath)
		have := store.Plan{
			FullText: m["ingest_level"] != string(store.LevelHeadwords),
			Contains: m["has_trigram"] == "1",
		}
		fresh := !store.SourceChanged(dbPath, srcPath)
		if fresh && have == plan {
			fmt.Printf("%salready prepared in %s - skipped\n", logx.Dict(name), filepath.Dir(dbPath))
			return maybePackMedia(srcPath, dbPath, name, full)
		}
		// a different plan or a changed source: IngestPlan overwrites the
		// existing database atomically, so nothing is deleted up front.
	}
	progress := func(done, total int) {
		if total > 0 {
			logx.Progress("  %d/%d entries", done, total)
		} else {
			logx.Progress("  %d entries", done)
		}
	}
	start := time.Now()
	rep, err := store.IngestPlan(r, dbPath, plan, progress)
	logx.ClearLine()
	if err != nil {
		return fmt.Errorf("preparing %q: %w", name, err)
	}
	fmt.Printf("%s%s indexed in %.1fs → %s\n",
		logx.Dict(name), plural(rep.Entries, "entry", "entries"), time.Since(start).Seconds(), filepath.Dir(dbPath))
	if rep.UnresolvedLinks > 0 {
		fmt.Printf("%s%s pointed at headwords not present in the source (skipped)\n",
			logx.Dict(name), plural(rep.UnresolvedLinks, "redirect", "redirects"))
	}
	return maybePackMedia(srcPath, dbPath, name, full)
}

func maybePackMedia(srcPath, dbPath, name string, full bool) error {
	if !full {
		return nil
	}
	mediaPath := store.MediaSibling(dbPath)
	if _, err := os.Stat(mediaPath); err == nil {
		fmt.Printf("%smedia already packed - skipped\n", logx.Dict(name))
		return nil
	}
	d, err := dict.Open(srcPath)
	if err != nil {
		return err
	}
	defer d.Close()
	lister, ok := d.(dict.ResourceLister)
	if !ok {
		return fmt.Errorf("format cannot enumerate resources")
	}
	names := resource.Filter(lister.Resources())
	if len(names) == 0 {
		fmt.Printf("%sno media to pack\n", logx.Dict(name))
		return nil
	}
	uuid, err := store.ReadMetaValue(dbPath, "dict_uuid")
	if err != nil {
		return err
	}
	progress := func(done, total int) { logx.Progress("  %d/%d media files", done, total) }
	start := time.Now()
	if err := store.IngestMedia(d, names, mediaPath, uuid, progress); err != nil {
		logx.ClearLine()
		return fmt.Errorf("packing media for %q: %w", name, err)
	}
	logx.ClearLine()
	fmt.Printf("%s%s packed in %.1fs\n",
		logx.Dict(name), plural(len(names), "media file", "media files"), time.Since(start).Seconds())
	return nil
}

func cmdServe(args []string) (err error) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	var dictDirs multiFlag
	fs.Var(&dictDirs, "dict-dir", "folder with dictionaries; repeat for several (env/toml: DICT_DIR)")
	dbDir := fs.String("db-dir", "", "library dir for cached dictionaries (env/toml: DB_DIR)")
	useCached := fs.Bool("use-cached", false, "also serve previously imported dictionaries from the library (env/toml: USE_CACHED)")
	noCompress := fs.Bool("no-compress", false, "store article text uncompressed - larger databases (env/toml: NO_COMPRESS)")
	indexWorkers := fs.String("index-workers", "", "dictionaries to prepare at once; \"auto\" = every core (env/toml: INDEX_WORKERS)")
	ip := fs.String("ip", "", "listen IP (env/toml: SERVER_IP)")
	port := fs.String("port", "", "listen port (env/toml: SERVER_PORT)")
	configPath := fs.String("config", "", "path to wudict.toml (env: CONFIG_PATH)")
	noBrowser := fs.Bool("no-browser", false, "do not open a browser tab (env/toml: NO_BROWSER)")
	verbose := fs.Bool("verbose", false, "verbose logging (env/toml: VERBOSE)")
	speexdec := fs.String("speexdec", "", "path to speexdec for .spx audio (env/toml: SPEEXDEC)")
	trayOn := fs.Bool("tray", false, "show a system tray icon (env/toml: TRAY)")
	trayOff := fs.Bool("no-tray", false, "never show a system tray icon (env/toml: TRAY=0)")
	fs.Parse(args)

	if *configPath == "" {
		*configPath = os.Getenv("CONFIG_PATH")
	}
	flagVals := map[string]string{
		"DICT_DIR": dictDirs.String(), "DB_DIR": *dbDir,
		"SERVER_IP": *ip, "SERVER_PORT": *port,
		"SPEEXDEC": *speexdec,
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
	if cfg.Verbose {
		logx.Enabled = true
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
	if cfg.DBDir != "" {
		os.Setenv("WUDICT_DB_DIR", cfg.DBDir) // store + auto-ingest honor this
	}

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
	url := "http://" + cfg.Addr() + "/"
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
				browserCmd(url)
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
	reg, err := server.NewRegistry(cfg.DictDirs, cfg.UseCached,
		server.WithPrefs(server.LoadPrefs(statePath(cfgFile))))
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
	store.SetCompressBodies(!cfg.NoCompress)
	server.SetIndexWorkers(cfg.IndexWorkers)
	if cfg.MemoryLimit > 0 {
		// A soft ceiling: Go collects harder instead of growing past it. Set it
		// through the server rather than debug directly, because a ceiling on
		// its own only buys more GC - the registry needs the same number to
		// know when to shed caches instead, which is what turns pressure into
		// freed memory rather than a collector spinning on a live set (D64).
		server.SetMemoryLimit(cfg.MemoryLimit)
		logx.V("memory limit: %d MB", cfg.MemoryLimit>>20)
	}
	srv.Version = Version
	srv.DictDirOrigin = cfg.Origin("DICT_DIR")
	srv.DictDirEditable = cfg.EditableInFile("DICT_DIR")
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
	srv.BrowserExtensions = cfg.BrowserExtensions
	srv.WebOrigins = cfg.WebOrigins

	lib, _ := store.Library()
	inFolder, fromLib := reg.Counts()
	printStartup(cfg, startupInfo{
		roots:    reg.Roots(),
		inFolder: inFolder, fromLibrary: fromLib, prepared: len(lib),
		total: reg.Count(), libDir: libDir, url: url,
		speex: speexSummary(useExternalSpeex, sxPath, sxSource),
	})
	if !cfg.NoBrowser {
		go openBrowser(url)
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
		err := httpSrv.Serve(ln)
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
		Open:     func() { browserCmd(url) },
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
// settings the server uses. They previously read only the raw WUDICT_DB_DIR
// environment variable, so `wudict ingest` ignored a DB_DIR set in
// wudict.toml and wrote somewhere the server would never look.
func applyLibrarySettings() {
	cfg, err := config.Load("", nil)
	if err != nil {
		return // a broken config must not stop a local command
	}
	if cfg.DBDir != "" {
		os.Setenv("WUDICT_DB_DIR", cfg.DBDir)
	}
	store.SetCompressBodies(!cfg.NoCompress)
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

func dirExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
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
// startupInfo is what the startup summary describes: each folder counted by
// what IT contributed (a blended total next to the dictionary folder made an
// empty folder look full when the library was in use).
type startupInfo struct {
	roots       []server.Root // dictionary folders, each with its own status
	inFolder    int           // dictionaries discovered across those folders
	fromLibrary int           // cached dictionaries actually serving (USE_CACHED)
	prepared    int           // cached dictionaries that exist, in use or not
	total       int           // what is being served
	libDir      string
	url         string
	speex       string
}

// statePath places state.json next to the wudict.toml that is in effect. When
// no config file exists at all it falls back to the home location - the same
// directory EnsureConfigFile would have used - and when even the home
// directory is unknown it returns "", which makes the state in-memory rather
// than scattering a file somewhere nobody asked for.
func statePath(cfgFile string) string {
	if cfgFile != "" {
		return filepath.Join(filepath.Dir(cfgFile), server.StateFile)
	}
	if p := config.HomeConfig(); p != "" {
		return filepath.Join(filepath.Dir(p), server.StateFile)
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
		note := plural(root.Count, "dictionary", "dictionaries")
		switch {
		case !root.Exists:
			note = "folder not found"
		case root.Total == 0:
			note = "no dictionaries found"
		case root.Count == 0:
			// overlapping folders: saying "none found" here would be a lie
			note = plural(root.Total, "dictionary", "dictionaries") + ", already listed above"
		case root.Total > root.Count:
			note = fmt.Sprintf("%s (+%d already listed above)",
				plural(root.Count, "dictionary", "dictionaries"), root.Total-root.Count)
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
	// place is invisible otherwise, and that was the whole bug behind D32.
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
	fmt.Fprintf(out, "  address       %s\n", in.url)
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
		plural(cfg.IndexWorkers, "dictionary at a time", "dictionaries at a time"),
		memLimitNote(cfg.MemoryLimit))
	fmt.Fprintf(out, "  preview memory %s%s\n", budgetNote(cfg.PreviewMemory), searchBudgetNote(cfg.SearchMemory))
	fmt.Fprintf(out, "  serving       %s\n", plural(in.total, "dictionary", "dictionaries"))

	// what to do next, when there is something to do
	switch {
	case in.total == 0:
		if in.prepared > 0 && !cfg.UseCached {
			fmt.Fprintf(out, "\nopen %s - choose a dictionary folder, or use the %s already prepared\n",
				in.url, plural(in.prepared, "dictionary", "dictionaries"))
		} else {
			fmt.Fprintf(out, "\nopen %s to choose your dictionary folder\n", in.url)
		}
	case in.prepared > 0 && !cfg.UseCached:
		fmt.Fprintf(out, "\n%s previously imported - enable with --use-cached (or on the setup page)\n",
			plural(in.prepared, "dictionary", "dictionaries"))
	}
	if in.speex == "" {
		fmt.Fprintf(out, "\nnote: no .spx decoder available - Speex audio will not play.\n"+
			"  Build with cgo for the built-in decoder, or install speex / set SPEEXDEC=/path/to/speexdec:\n"+
			"    macOS:    brew install speex\n"+
			"    Linux:    sudo apt install speex   (or your distro's speex package)\n"+
			"    Windows:  https://www.speex.org/downloads/\n")
	}
}

// multiFlag collects a repeatable path flag. Repetition rather than a
// separator inside one value: nothing to escape and it reads the same on every
// platform (`--dict-dir A --dict-dir B`). It joins with os.PathListSeparator
// so the value travels through the same string-keyed config layering as the
// environment variable, which follows the $PATH convention.
type multiFlag []string

func (m *multiFlag) String() string { return strings.Join(*m, string(os.PathListSeparator)) }
func (m *multiFlag) Set(v string) error {
	*m = append(*m, v)
	return nil
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

// plural renders a count with the right noun: "1 dictionary", "55 dictionaries".
func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

func indexingSummary(autoIndex string) string {
	if autoIndex == config.AutoIndexOff {
		return "off - dictionaries are searched through their own format (AUTO_INDEX=off)"
	}
	return "on - a headword index is prepared on first search; contains, full-text and media on request"
}

func openBrowser(url string) {
	time.Sleep(300 * time.Millisecond) // let our own server finish binding
	browserCmd(url)
}

func browserCmd(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}

func cmdSearchAll(args []string) error {
	applyLibrarySettings()
	fs := flag.NewFlagSet("searchall", flag.ExitOnError)
	// exact by default: the term a person types on this command line is the
	// word they want, and prefix over a whole library answers a query nobody
	// asked - 97 dictionaries offering `Casablanca` for `casa`.
	modeStr := fs.String("mode", "exact", "exact|prefix|contains|fts")
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

	var mode search.Mode
	switch *modeStr {
	case "exact":
		mode = search.Exact
	case "prefix":
		mode = search.Prefix
	case "contains":
		mode = search.Contains
	case "fts":
		mode = search.FullText
	default:
		return fmt.Errorf("unknown mode %q", *modeStr)
	}
	// A search that prints only headwords answers "does this word exist", which
	// is not what anyone runs a dictionary for. `text` is the default because
	// it is the readable one, and because it is the smallest: raw markup is
	// 2.6x its size (D61) and a terminal cannot render it anyway. `list` keeps
	// the old headword listing for the times the question really is "which
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
	// wudict.toml, else the default folder (config.Load applies that chain).
	// Before this, `searchall` was the one command that could not find the
	// library the rest of the program is configured with.
	if *configPath == "" {
		*configPath = os.Getenv("CONFIG_PATH")
	}
	cfg, err := config.Load(*configPath, map[string]string{"DICT_DIR": dictDirs.String()})
	if err != nil {
		return err
	}
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
		plural(len(paths), "dictionary", "dictionaries"), strings.Join(dirs, ", "), origin)

	// Opened inside the worker, closed as soon as that dictionary has answered.
	// The previous version opened all of them up front and held every one until
	// the command exited - for a large library that is the whole corpus
	// materialised at once (docs.local/PERF.md §8.7). Peak is now the worker
	// count, and the first dictionary's results print while the rest are still
	// opening.
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
	fs.Parse(args)
	orphans, err := store.FindOrphans()
	if err != nil {
		return err
	}
	if len(orphans) == 0 {
		fmt.Println("library is clean - nothing to remove")
		return nil
	}
	var total int64
	for _, o := range orphans {
		total += o.Size
		kind := "file"
		if o.IsDir {
			kind = "folder"
		}
		fmt.Printf("%s  (%s, %s)\n  %s\n", o.Path, kind, humanSize(o.Size), o.Reason)
	}
	fmt.Printf("%d items, %s total\n", len(orphans), humanSize(total))
	if !*force {
		fmt.Println("dry run - re-run with -f to delete")
		return nil
	}
	var failed int
	for _, o := range orphans {
		var err error
		if o.IsDir {
			err = os.RemoveAll(o.Path)
		} else {
			err = os.Remove(o.Path)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "delete %s: %v\n", o.Path, err)
			failed++
		}
	}
	fmt.Printf("deleted %d items\n", len(orphans)-failed)
	if failed > 0 {
		return fmt.Errorf("%d deletions failed", failed)
	}
	return nil
}

func cmdRes(args []string) error {
	fs := flag.NewFlagSet("res", flag.ExitOnError)
	out := fs.String("o", "", `output file; "-" is stdout, a directory means "in there", empty writes a file when stdout is a terminal and stdout otherwise`)
	force := fs.Bool("f", false, "overwrite an existing file whose name was derived from the resource")
	fs.Parse(args)
	if fs.NArg() != 2 {
		return fmt.Errorf("usage: wudict res [-o out] [-f] <dictfile> <name>")
	}
	d, err := dict.Open(fs.Arg(0))
	if err != nil {
		return err
	}
	defer d.Close()
	name := fs.Arg(1)
	rc, mimeType, err := d.Resource(name)
	if err != nil {
		return err
	}
	defer rc.Close()

	dest, derived, err := resDest(*out, name, os.Stdout, os.Stat)
	if err != nil {
		return err
	}
	if dest == "" { // stdout
		if _, err := io.Copy(os.Stdout, rc); err != nil {
			return err
		}
		if mimeType != "" {
			fmt.Fprintln(os.Stderr, "mime:", mimeType)
		}
		return nil
	}
	// A derived name is a guess, so it never destroys anything without -f; a
	// name the user typed is an instruction, and truncating it is what every
	// other tool does with an output path.
	if derived && !*force {
		if _, err := os.Stat(dest); err == nil {
			return fmt.Errorf("%s already exists: pass -f to overwrite, or -o to name the output", dest)
		}
	}
	n, err := writeFileAtomic(dest, rc)
	if err != nil {
		return err
	}
	// stdout stays free of anything that is not the resource itself, so this
	// remains usable in a pipeline that also asked for a file.
	if mimeType != "" {
		fmt.Fprintf(os.Stderr, "wrote %s (%d bytes, %s)\n", dest, n, mimeType)
	} else {
		fmt.Fprintf(os.Stderr, "wrote %s (%d bytes)\n", dest, n)
	}
	return nil
}

// resDest decides where `res` writes. It returns "" for stdout, and reports
// whether the path was DERIVED from the resource name (the caller then refuses
// to overwrite) rather than given by the user.
//
// The guard is "stdout is a terminal", NOT "stdout". Binary on a terminal is
// the accident; binary down a pipe or a redirect is the command working as
// intended, and `wudict res d.mdx img.png > img.png` and `… | file -` must keep
// meaning what they always meant. `-o -` is an explicit instruction and beats
// the terminal check - naming stdout IS the consent.
//
// stat is injected so the directory case is testable without touching disk.
func resDest(out, name string, stdout *os.File, stat func(string) (os.FileInfo, error)) (dest string, derived bool, err error) {
	if out == "-" {
		return "", false, nil
	}
	if out != "" {
		// An existing directory means "put it in there"; anything else is the
		// file name itself, parents included (mkdir -p is done at write time).
		if fi, err := stat(out); err == nil && fi.IsDir() {
			base := resBasename(name)
			if base == "" {
				return "", false, fmt.Errorf("resource %q has no usable file name: name the output file with -o", name)
			}
			return filepath.Join(out, base), false, nil
		}
		return out, false, nil
	}
	if !isTerminal(stdout) {
		return "", false, nil
	}
	base := resBasename(name)
	if base == "" {
		return "", false, fmt.Errorf("resource %q has no usable file name: pass -o to name the output, or -o - for stdout", name)
	}
	return base, true, nil
}

// resBasename reduces a resource name to a single, safe file name in the
// current directory. It returns "" when nothing usable is left.
//
// The name comes from the DICTIONARY, not from the user: MDX stores paths as
// `\audio\x.spx`, and a hostile or merely broken container can hold "..", an
// absolute path, a Windows drive letter, or a NUL. So the whole path is
// discarded and only the last element kept - the flat file the user asked for.
// Recreating the container's directory tree from those strings is what turns
// this into zip-slip, and is why -o exists for anyone who wants a specific
// place. The server solved the same problem the same way (D59).
func resBasename(name string) string {
	// Both separators, whatever the host: an .mdd written on Windows is read
	// on Linux, where a backslash is an ordinary character and would otherwise
	// survive into the file name.
	name = strings.ReplaceAll(name, "\\", "/")
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		name = name[i+1:]
	}
	// A drive-relative name ("c:x.png") keeps a colon that means something on
	// Windows and nothing here; take what follows it.
	if i := strings.LastIndexByte(name, ':'); i >= 0 {
		name = name[i+1:]
	}
	name = strings.TrimSpace(name)
	// "." and ".." are not file names, and a control byte in one is a
	// terminal-escape vector as much as a filesystem problem.
	if name == "." || name == ".." || strings.ContainsAny(name, "\x00") {
		return ""
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return ""
		}
	}
	return name
}

// writeFileAtomic copies src to path via a sibling temp file, creating parent
// directories first. Nothing lands at the final name until the copy has fully
// succeeded: a resource that fails to decompress halfway through must not
// leave a truncated file that looks like a complete one.
func writeFileAtomic(path string, src io.Reader) (int64, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return 0, err
		}
	}
	// Same directory as the destination, so the rename is on one filesystem.
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".part*")
	if err != nil {
		return 0, err
	}
	tmpName := tmp.Name()
	defer func() {
		tmp.Close()        // no-op after a successful Close below
		os.Remove(tmpName) // no-op after a successful Rename
	}()
	n, err := io.Copy(tmp, src)
	if err != nil {
		return n, err
	}
	if err := tmp.Close(); err != nil {
		return n, err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return n, err
	}
	return n, nil
}

// isTerminal reports whether f is a character device - the same test logx uses
// for stderr, kept here rather than shared because logx is about progress
// output and this is about not vomiting a PNG into someone's shell.
func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}
