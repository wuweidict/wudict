// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

// gonow-dict CLI — Phase 1 surface: inspect and query dictionaries via
// the direct backends. The HTTP server arrives in a later phase.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/glowinthedark/gonow-dict/internal/config"
	"github.com/glowinthedark/gonow-dict/internal/dict"
	"github.com/glowinthedark/gonow-dict/internal/logx"
	"github.com/glowinthedark/gonow-dict/internal/search"
	"github.com/glowinthedark/gonow-dict/internal/server"
	"github.com/glowinthedark/gonow-dict/internal/speex"
	"github.com/glowinthedark/gonow-dict/internal/store"

	_ "github.com/glowinthedark/gonow-dict/internal/format/bgl"      // register .bgl
	_ "github.com/glowinthedark/gonow-dict/internal/format/dsl"      // register .dsl(.dz)
	_ "github.com/glowinthedark/gonow-dict/internal/format/mdx"      // register .mdx
	_ "github.com/glowinthedark/gonow-dict/internal/format/slob"     // register .slob
	_ "github.com/glowinthedark/gonow-dict/internal/format/stardict" // register .ifo
)

// version is stamped by the Makefile via -ldflags "-X main.version=…".
var version = "dev"

func usage() string {
	return fmt.Sprintf(`gonow-dict %s — multi-format dictionary server
MDict (.mdx/.mdd) · StarDict (.ifo) · Aard2 (.slob) · Lingvo DSL (.dsl/.dsl.dz) · gonow (.text.db)

USAGE
  gonow-dict [command] [flags] [args]

  Running gonow-dict with no arguments (or with only flags) starts the
  HTTP server — the same as the "serve" command.

COMMANDS
  serve                                   Start the HTTP server (the default command)
  list   <dir> [dir…]                     Discover dictionaries under one or more folders
  info   <dictfile>                       Show dictionary metadata and capabilities
  lookup [-n max] <dictfile> <word>       Exact lookup (accent-fold fallback); HTML to stdout
  prefix [-n max] <dictfile> <word>       Exact-else-prefix lookup (accent-insensitive); HTML to stdout
  contains [-n max] <dictfile> <word>     Substring headword search (FTS5 trigram; ingested dicts only)
  fts    [-n max] <dictfile> <query>      FTS5 full-text search (ingested dicts only)
  keys   [-offset N] [-n max] <dictfile>  List headwords
  res    [-o out] <dictfile> <name>       Extract one resource (e.g. "audio/word.mp3")
  ingest [-full] [-fuzzy-only] <dictfile|folder…>
                                          Prepare a dictionary into the library:
                                          <db-dir>/<dictionary name>/text.db (+ info.txt),
                                          enabling contains & full-text search (a folder
                                          prepares every dictionary in it, skipping ones
                                          already done); -full also packs media.db into the
                                          same folder; -fuzzy-only indexes headwords only
                                          (smaller db, no full-text search)
  searchall [-mode m] [-n perDict] <dir> <term>
                                          Concurrent search across all dictionaries in a folder
                                          (<dir> may be a "a:b" list, as in DICT_DIR)
  clean  [-f]                             List removable items in the library: incomplete or
                                          unreadable folders, interrupted ingests, leftovers
                                          from the old flat layout. A prepared dictionary is
                                          never listed, even if its source is gone or changed.
                                          -f deletes them. Dry run by default.

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

  --db-dir       <path>   Library folder: one subfolder per prepared dictionary,
                          holding text.db (+ media.db, info.txt). Must not be the
                          same folder as --dict-dir.
                          env: DB_DIR         toml: DB_DIR
                          default: ~/.gonow-dict/db

  --use-cached            Also serve the previously imported dictionaries kept in
                          the library, whether or not their original files are still
                          present. Normally set by clicking "Use these dictionaries"
                          on the first-run setup page.
                          env: USE_CACHED     toml: USE_CACHED
                          default: off

  --ip           <addr>   Listen IP address
                          env: SERVER_IP      toml: SERVER_IP
                          default: 127.0.0.1

  --port         <port>   Listen port
                          env: SERVER_PORT    toml: SERVER_PORT
                          default: 8808

  --config       <path>   Path to config.toml (overrides auto-detect)
                          env: CONFIG_PATH

  --no-browser            Do not open a browser tab on startup
                          env/toml: NO_BROWSER=1

  --verbose               Verbose logging (requests, dictionary opens,
                          ingest, transcodes) for easy debugging
                          env/toml: VERBOSE=1  (works for all commands)

  --speexdec     <path>   Path to speexdec binary (.spx audio is
                          transcoded to WAV — browsers cannot play Speex)
                          env: SPEEXDEC       toml: SPEEXDEC
                          default: speexdec found on PATH, else /usr/bin/speexdec

  -v, --version           Show version and exit  (verbose is --verbose)
  -h, --help              Show this help and exit

CONFIG FILE SEARCH ORDER
  1. --config flag / CONFIG_PATH env var
  2. <executable-dir>/config.toml
  3. ~/.gonow-dict/config.toml
  4. /etc/gonow-dict/config.toml
  5. ./config.toml
  On the first "serve" run a fully commented config.toml is generated
  next to the executable (or in ~/.gonow-dict/ if that is not writable).

PRIORITY (highest → lowest)
  CLI flag  >  environment variable  >  config.toml  >  built-in default

FIRST RUN
  If the dictionary folder is missing or empty, the web UI shows a setup
  page: paste a folder path, it is validated live, and the choice is
  saved to config.toml — no restart needed.

EXAMPLE config.toml
  DICT_DIR    = "/data/dicts"
  DB_DIR      = "~/.gonow-dict/db"
  SERVER_IP   = "0.0.0.0"
  SERVER_PORT = "9000"
  NO_BROWSER  = "1"

EXAMPLES
  gonow-dict
  gonow-dict --dict-dir ~/Books/Dicts --port 9090 --no-browser
  gonow-dict lookup ~/Dicts/Oxford.mdx serendipity
  gonow-dict ingest -full ~/Dicts/Oxford.mdx
  SERVER_PORT=9000 gonow-dict
`, version)
}

func main() {
	if len(os.Args) < 2 {
		// no arguments: start the server (documented default)
		if err := cmdServe(nil); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
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
	case "clean":
		err = cmdClean(args)
	case "-h", "--help", "help":
		fmt.Print(usage())
	case "-v", "--version", "version":
		fmt.Println("gonow-dict", version)
	default:
		if strings.HasPrefix(cmd, "-") {
			// bare flags (mdict-go-web style): treat as serve flags
			err = cmdServe(os.Args[1:])
			break
		}
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", cmd, usage())
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func cmdList(args []string) error {
	dirs := folderArgs(args)
	if len(dirs) == 0 {
		return fmt.Errorf("usage: gonow-dict list <dir> [dir…]")
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
		return fmt.Errorf("usage: gonow-dict info <dictfile>")
	}
	d, err := dict.Open(args[0])
	if err != nil {
		return err
	}
	defer d.Close()
	m, c := d.Meta(), d.Caps()
	fmt.Printf("name:        %s\nformat:      %s\npath:        %s\nentries:     %d\ncapabilities: exact=%v prefix=%v contains=%v fts=%v\n",
		m.Name, m.Format, m.Path, m.EntryCount, c.Exact, c.Prefix, c.Contains, c.FTS)
	if m.Description != "" {
		fmt.Printf("description: %s\n", m.Description)
	}
	return nil
}

func cmdQuery(mode string, args []string) error {
	fs := flag.NewFlagSet(mode, flag.ExitOnError)
	n := fs.Int("n", 20, "max results")
	fs.Parse(args)
	if fs.NArg() != 2 {
		return fmt.Errorf("usage: gonow-dict %s [-n max] <dictfile> <word>", mode)
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
	for _, r := range results {
		fmt.Printf("<!-- %s -->\n%s\n", r.Headword, r.Body)
	}
	return nil
}

func cmdKeys(args []string) error {
	fs := flag.NewFlagSet("keys", flag.ExitOnError)
	offset := fs.Int("offset", 0, "start offset")
	n := fs.Int("n", 50, "max headwords")
	fs.Parse(args)
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: gonow-dict keys [-offset N] [-n max] <dictfile>")
	}
	d, err := dict.Open(fs.Arg(0))
	if err != nil {
		return err
	}
	defer d.Close()
	for _, w := range d.Keywords(*offset, *n) {
		fmt.Println(w)
	}
	return nil
}

func cmdIngest(args []string) error {
	fs := flag.NewFlagSet("ingest", flag.ExitOnError)
	out := fs.String("o", "", "output .db path (default: <db-dir>/<dictionary name>/text.db)")
	full := fs.Bool("full", false, "also pack binary resources into a companion .media.db")
	fuzzyOnly := fs.Bool("fuzzy-only", false, "index headwords only: fuzzy search, smaller db, no full-text search")
	fs.Parse(args)
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: gonow-dict ingest [-o out.db] [-full] [-fuzzy-only] <dictfile|folder>")
	}
	level := store.LevelText
	if *fuzzyOnly {
		level = store.LevelHeadwords
	}
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
			if err := ingestOne(p, "", *full, level); err != nil {
				fmt.Fprintf(os.Stderr, "  failed: %v\n", err)
				failed++
			}
		}
		if failed > 0 {
			return fmt.Errorf("%d of %d dictionaries failed", failed, len(paths))
		}
		return nil
	}
	return ingestOne(srcPath, *out, *full, level)
}

func ingestOne(srcPath, out string, full bool, level store.Level) error {
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
		cl, _ := store.ReadMetaValue(dbPath, "ingest_level")
		fresh := !store.SourceChanged(dbPath, srcPath)
		if fresh && (store.Level(cl) == level || level == store.LevelHeadwords) {
			fmt.Printf("%salready prepared in %s — skipped\n", logx.Dict(name), filepath.Dir(dbPath))
			return maybePackMedia(srcPath, dbPath, name, full)
		}
		// a level upgrade or a changed source: IngestLevel overwrites the
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
	rep, err := store.IngestLevelReport(r, dbPath, level, progress)
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
		fmt.Printf("%smedia already packed — skipped\n", logx.Dict(name))
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
	names := lister.Resources()
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

func cmdServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	var dictDirs multiFlag
	fs.Var(&dictDirs, "dict-dir", "folder with dictionaries; repeat for several (env/toml: DICT_DIR)")
	dbDir := fs.String("db-dir", "", "library dir for prepared dictionaries (env/toml: DB_DIR)")
	useCached := fs.Bool("use-cached", false, "also serve previously imported dictionaries from the library (env/toml: USE_CACHED)")
	ip := fs.String("ip", "", "listen IP (env/toml: SERVER_IP)")
	port := fs.String("port", "", "listen port (env/toml: SERVER_PORT)")
	configPath := fs.String("config", "", "path to config.toml (env: CONFIG_PATH)")
	noBrowser := fs.Bool("no-browser", false, "do not open a browser tab (env/toml: NO_BROWSER)")
	verbose := fs.Bool("verbose", false, "verbose logging (env/toml: VERBOSE)")
	speexdec := fs.String("speexdec", "", "path to speexdec for .spx audio (env/toml: SPEEXDEC)")
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
	cfg, err := config.Load(*configPath, flagVals)
	if err != nil {
		return err
	}
	if cfg.Verbose {
		logx.Enabled = true
	}
	logx.V("config: source=%q dictDirs=%v dbDir=%q addr=%s speexdec=%s",
		cfg.Source, cfg.DictDirs, cfg.DBDir, cfg.Addr(), cfg.Speexdec)
	if cfg.DBDir != "" {
		os.Setenv("GONOW_DB_DIR", cfg.DBDir) // store + auto-ingest honor this
	}

	// The library (DB_DIR) is gonow-dict's own working area, never a folder of
	// user dictionaries. Using it as DICT_DIR listed every prepared dictionary
	// twice and exposed internal sidecars as phantom dictionaries, so it is a
	// hard error; and wherever it lives, discovery skips it — which also covers
	// the subtler case of a DB_DIR nested inside the dictionary folder.
	libDir := store.DefaultDBDir()
	for _, d := range cfg.DictDirs {
		if !dict.SameDir(d, libDir) {
			continue
		}
		return fmt.Errorf("DICT_DIR and DB_DIR are the same folder:\n"+
			"  %s\n"+
			"  DB_DIR holds the dictionaries gonow-dict has already prepared; DICT_DIR must point at\n"+
			"  the folder with your dictionary files (.mdx, .ifo, .slob, .dsl, .bgl).\n"+
			"  To work from the prepared ones alone, leave DICT_DIR unset and set USE_CACHED = \"1\".", libDir)
	}
	dict.ExcludeDir(libDir)

	// Bring databases from the pre-folder layout into the current one (a
	// rename, never a re-index): data already prepared must stay usable
	// instead of forcing the user to prepare the same dictionary twice.
	if moved, err := store.AdoptLoose(); err != nil {
		logx.Warn("could not tidy the library: %v", err)
	} else if len(moved) > 0 {
		for _, m := range moved {
			fmt.Fprintf(os.Stderr, "library: %s → %s/\n", filepath.Base(m.From), filepath.Base(m.Dir))
		}
	}

	// first run: generate a commented config.toml so users can discover
	// the knobs; an existing file anywhere in the search order wins
	cfgFile := cfg.Source
	if cfgFile == "" && *configPath == "" {
		p, created, err := config.EnsureConfigFile()
		if err != nil {
			logx.Warn("could not create config.toml: %v", err)
		} else {
			cfgFile = p
			if created {
				fmt.Fprintf(os.Stderr, "created default config: %s\n", p)
			}
		}
	}

	reg, err := server.NewRegistry(cfg.DictDirs, cfg.UseCached)
	if err != nil {
		return fmt.Errorf("scanning %s: %w", strings.Join(cfg.DictDirs, ", "), err)
	}
	srv := server.New(reg)
	srv.ConfigPath = cfgFile
	useExternalSpeex := cfg.SpeexBackend == "external"
	srv.UseExternalSpeex = useExternalSpeex
	// Only look for the external speexdec binary when it will actually be used:
	// forced via SPEEX_BACKEND=external, or a purego build without the built-in
	// decoder. A full cgo build trusts its own in-process decoder and never
	// touches speexdec.
	var sxPath, sxSource string
	if useExternalSpeex || !speex.Available {
		sxPath, sxSource = resolveSpeexdec(cfg.Speexdec)
	}
	srv.Speexdec = sxPath
	srv.AutoIndex = cfg.AutoIndex != "off"

	url := "http://" + cfg.Addr() + "/"
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
	// a long time — a write deadline would sever them. ReadHeaderTimeout +
	// IdleTimeout + MaxHeaderBytes harden against slowloris without touching
	// the streaming paths.
	httpSrv := &http.Server{
		Addr:              cfg.Addr(),
		Handler:           srv,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	// graceful shutdown on Ctrl-C / SIGTERM: finish in-flight requests,
	// close database handles cleanly
	idle := make(chan struct{})
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
		<-sig
		fmt.Fprintln(os.Stderr, "\nshutting down…")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(ctx)
		close(idle)
	}()
	err = httpSrv.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		<-idle
		return nil
	}
	if err != nil && strings.Contains(err.Error(), "address already in use") {
		return fmt.Errorf(`%s is already in use — another gonow-dict (or other server) is running.
Hint: pick another port with --port, e.g.:  gonow-dict --port %s
      or find the process using it:        lsof -i :%s`, cfg.Addr(), nextPort(cfg.Port), cfg.Port)
	}
	return err
}

// nextPort suggests port+1 for the port-in-use hint.
func nextPort(p string) string {
	if n, err := strconv.Atoi(p); err == nil {
		return strconv.Itoa(n + 1)
	}
	return "8809"
}

func dirExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// resolveSpeexdec locates the speexdec binary ONCE at startup (never at .spx
// playback time). Precedence: an explicit SPEEXDEC override, then a binary
// sitting next to the gonow-dict executable, then $PATH. Returns the resolved
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
		logx.Warn("SPEEXDEC=%q not found — falling back to auto-detection", override)
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
	return "" // none available — printStartup emits the install hint
}

// printStartup shows the configuration that actually took effect. Every value
// a user might have set somewhere (flag, env, config.toml, default) is listed
// with the folder or address it resolved to, so "why is it not finding my
// dictionaries" is answerable from the first screen.
// startupInfo is what the startup summary describes: each folder counted by
// what IT contributed (a blended total next to the dictionary folder made an
// empty folder look full when the library was in use).
type startupInfo struct {
	roots       []server.Root // dictionary folders, each with its own status
	inFolder    int           // dictionaries discovered across those folders
	fromLibrary int           // prepared dictionaries actually serving (USE_CACHED)
	prepared    int           // prepared dictionaries that exist, in use or not
	total       int           // what is being served
	libDir      string
	url         string
	speex       string
}

func printStartup(cfg config.Config, in startupInfo) {
	out := os.Stderr
	fmt.Fprintf(out, "gonow-dict %s\n", version)

	// one line per folder, aligned in a single column, each with its own
	// status — an unmounted drive must be visible, not silently absent
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

	cfgSrc := cfg.Source
	if cfgSrc == "" {
		cfgSrc = "(none — built-in defaults)"
	}
	fmt.Fprintf(out, "  config        %s\n", cfgSrc)
	fmt.Fprintf(out, "  address       %s\n", in.url)
	if in.speex != "" {
		fmt.Fprintf(out, "  .spx audio    %s\n", in.speex)
	}
	fmt.Fprintf(out, "  indexing      %s\n", indexingSummary(cfg.AutoIndex))
	fmt.Fprintf(out, "  serving       %s\n", plural(in.total, "dictionary", "dictionaries"))

	// what to do next, when there is something to do
	switch {
	case in.total == 0:
		if in.prepared > 0 && !cfg.UseCached {
			fmt.Fprintf(out, "\nopen %s — choose a dictionary folder, or use the %s already prepared\n",
				in.url, plural(in.prepared, "dictionary", "dictionaries"))
		} else {
			fmt.Fprintf(out, "\nopen %s to choose your dictionary folder\n", in.url)
		}
	case in.prepared > 0 && !cfg.UseCached:
		fmt.Fprintf(out, "\n%s previously imported — enable with --use-cached (or on the setup page)\n",
			plural(in.prepared, "dictionary", "dictionaries"))
	}
	if in.speex == "" {
		fmt.Fprintf(out, "\nnote: no .spx decoder available — Speex audio will not play.\n"+
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

// plural renders a count with the right noun: "1 dictionary", "55 dictionaries".
func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

func indexingSummary(autoIndex string) string {
	if autoIndex == "off" {
		return "manual (AUTO_INDEX=off)"
	}
	return "automatic on first search (headwords); full-text on request"
}

func openBrowser(url string) {
	time.Sleep(300 * time.Millisecond)
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
	fs := flag.NewFlagSet("searchall", flag.ExitOnError)
	modeStr := fs.String("mode", "prefix", "exact|prefix|contains|fts")
	n := fs.Int("n", 10, "max results per dictionary")
	fs.Parse(args)
	if fs.NArg() != 2 {
		return fmt.Errorf("usage: gonow-dict searchall [-mode m] [-n perDict] <dir> <term>")
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
	paths, _, err := dict.DiscoverAll(folderArgs([]string{fs.Arg(0)}))
	if err != nil {
		return err
	}
	var dicts []dict.Dictionary
	for _, p := range paths {
		d, err := dict.Open(p)
		if err != nil {
			logx.Warn("%scannot be opened: %v — skipped", logx.Dict(filepath.Base(p)), err)
			continue
		}
		defer d.Close()
		dicts = append(dicts, d)
	}
	for _, h := range search.All(context.Background(), dicts, mode, fs.Arg(1), *n) {
		switch {
		case h.Skipped:
			continue
		case h.Err != nil:
			logx.Warn("%ssearch failed: %v", logx.Dict(h.Meta.Name), h.Err)
		case len(h.Results) > 0:
			fmt.Printf("== %s (%d)\n", h.Meta.Name, len(h.Results))
			for _, r := range h.Results {
				fmt.Printf("  %s\n", r.Headword)
			}
		}
	}
	return nil
}

func cmdClean(args []string) error {
	fs := flag.NewFlagSet("clean", flag.ExitOnError)
	force := fs.Bool("f", false, "actually delete (default: dry run, list only)")
	fs.Parse(args)
	orphans, err := store.FindOrphans()
	if err != nil {
		return err
	}
	if len(orphans) == 0 {
		fmt.Println("library is clean — nothing to remove")
		return nil
	}
	var total int64
	for _, o := range orphans {
		total += o.Size
		kind := "file"
		if o.IsDir {
			kind = "folder"
		}
		fmt.Printf("%s  (%s, %.1f MB)\n  %s\n", o.Path, kind, float64(o.Size)/(1<<20), o.Reason)
	}
	fmt.Printf("%d items, %.1f MB total\n", len(orphans), float64(total)/(1<<20))
	if !*force {
		fmt.Println("dry run — re-run with -f to delete")
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
	out := fs.String("o", "", "output file (default stdout)")
	fs.Parse(args)
	if fs.NArg() != 2 {
		return fmt.Errorf("usage: gonow-dict res [-o out] <dictfile> <name>")
	}
	d, err := dict.Open(fs.Arg(0))
	if err != nil {
		return err
	}
	defer d.Close()
	rc, mimeType, err := d.Resource(fs.Arg(1))
	if err != nil {
		return err
	}
	defer rc.Close()
	w := io.Writer(os.Stdout)
	if *out != "" {
		f, err := os.Create(*out)
		if err != nil {
			return err
		}
		defer f.Close()
		w = f
	}
	if _, err := io.Copy(w, rc); err != nil {
		return err
	}
	if mimeType != "" {
		fmt.Fprintln(os.Stderr, "mime:", mimeType)
	}
	return nil
}
