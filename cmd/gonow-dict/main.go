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
	"github.com/glowinthedark/gonow-dict/internal/store"

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
  list   <dir>                            Discover dictionaries under a folder
  info   <dictfile>                       Show dictionary metadata and capabilities
  lookup [-n max] <dictfile> <word>       Exact lookup (accent-fold fallback); HTML to stdout
  prefix [-n max] <dictfile> <word>       Exact-else-prefix lookup; HTML to stdout
  fuzzy  [-n max] <dictfile> <word>       FTS5 fuzzy headword search (ingested dicts only)
  fts    [-n max] <dictfile> <query>      FTS5 full-text search (ingested dicts only)
  keys   [-offset N] [-n max] <dictfile>  List headwords
  res    [-o out] <dictfile> <name>       Extract one resource (e.g. "audio/word.mp3")
  ingest [-full] [-fuzzy-only] <dictfile|folder>
                                          Build <slug>.text.db enabling fuzzy & full-text search
                                          (a folder ingests every dictionary in it, skipping
                                          already-ingested ones); -full also packs media into
                                          <slug>.media.db; -fuzzy-only indexes headwords only
                                          (smaller db, fuzzy search but no full-text search)
  searchall [-mode m] [-n perDict] <dir> <term>
                                          Concurrent search across all dictionaries in a folder
  clean  [-f]                             List orphaned cache databases (deleted/changed
                                          sources); -f deletes them. Dry run by default.

SERVE FLAGS
  --dict-dir     <path>   Folder with dictionary files (scanned recursively)
                          env: DICT_DIR       toml: DICT_DIR
                          default: ~/Dictionaries

  --db-dir       <path>   Cache folder for generated .text.db / .media.db files
                          env: DB_DIR         toml: DB_DIR
                          default: ~/.gonow-dict/db

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
	case "lookup", "prefix", "fuzzy", "fts":
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
	if len(args) != 1 {
		return fmt.Errorf("usage: gonow-dict list <dir>")
	}
	paths, err := dict.Discover(args[0])
	if err != nil {
		return err
	}
	for _, p := range paths {
		fmt.Println(p)
	}
	if len(paths) == 0 {
		fmt.Fprintln(os.Stderr, "no dictionaries found")
	}
	return nil
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
	fmt.Printf("name:        %s\nformat:      %s\npath:        %s\nentries:     %d\ncapabilities: exact=%v prefix=%v fuzzy=%v fts=%v\n",
		m.Name, m.Format, m.Path, m.EntryCount, c.Exact, c.Prefix, c.Fuzzy, c.FTS)
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
	case "fuzzy":
		f, ok := d.(dict.FuzzySearcher)
		if !ok {
			return dict.ErrUnsupported
		}
		results, err = f.Fuzzy(fs.Arg(1), *n)
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
	out := fs.String("o", "", "output .db path (default: <cache>/<slug>-<hash8>.text.db)")
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
	if st, err := os.Stat(srcPath); err == nil && st.IsDir() {
		if *out != "" {
			return fmt.Errorf("-o cannot be used with a folder")
		}
		paths, err := dict.Discover(srcPath)
		if err != nil {
			return err
		}
		if len(paths) == 0 {
			return fmt.Errorf("no dictionaries found under %s", srcPath)
		}
		var failed int
		for i, p := range paths {
			fmt.Fprintf(os.Stderr, "[%d/%d] %s\n", i+1, len(paths), p)
			if err := ingestOne(p, "", *full, level); err != nil {
				fmt.Fprintf(os.Stderr, "  FAILED: %v\n", err)
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

	dbPath := out
	if dbPath == "" {
		dbPath = store.CacheBase(srcPath, r.Meta().Name) + ".text.db"
	}
	if _, err := os.Stat(dbPath); err == nil && out == "" {
		cl, _ := store.ReadMetaValue(dbPath, "ingest_level")
		if store.Level(cl) == level || level == store.LevelHeadwords {
			fmt.Printf("%s (already ingested, skipped)\n", dbPath)
			return maybePackMedia(srcPath, dbPath, full)
		}
		// headwords-only db upgraded to full text
		_ = os.Remove(dbPath)
	}
	progress := func(done, total int) {
		if total > 0 {
			fmt.Fprintf(os.Stderr, "\r%d/%d", done, total)
		} else {
			fmt.Fprintf(os.Stderr, "\r%d", done)
		}
	}
	start := time.Now()
	if err := store.IngestLevel(r, dbPath, level, progress); err != nil {
		fmt.Fprintln(os.Stderr)
		return err
	}
	fmt.Fprintln(os.Stderr)
	fmt.Printf("%s (%.1fs)\n", dbPath, time.Since(start).Seconds())
	return maybePackMedia(srcPath, dbPath, full)
}

func maybePackMedia(srcPath, dbPath string, full bool) error {

	if !full {
		return nil
	}
	mediaPath := strings.TrimSuffix(dbPath, ".text.db") + ".media.db"
	if _, err := os.Stat(mediaPath); err == nil {
		fmt.Printf("%s (already packed, skipped)\n", mediaPath)
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
		fmt.Fprintln(os.Stderr, "no resources to pack")
		return nil
	}
	uuid, err := store.ReadMetaValue(dbPath, "dict_uuid")
	if err != nil {
		return err
	}
	progress := func(done, total int) { fmt.Fprintf(os.Stderr, "\r%d/%d", done, total) }
	start := time.Now()
	if err := store.IngestMedia(d, names, mediaPath, uuid, progress); err != nil {
		fmt.Fprintln(os.Stderr)
		return err
	}
	fmt.Fprintln(os.Stderr)
	fmt.Printf("%s (%d resources, %.1fs)\n", mediaPath, len(names), time.Since(start).Seconds())
	return nil
}

func cmdServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	dictDir := fs.String("dict-dir", "", "directory with dictionaries (env/toml: DICT_DIR)")
	dbDir := fs.String("db-dir", "", "cache dir for generated databases (env/toml: DB_DIR)")
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
		"DICT_DIR": *dictDir, "DB_DIR": *dbDir,
		"SERVER_IP": *ip, "SERVER_PORT": *port,
		"SPEEXDEC": *speexdec,
	}
	if *noBrowser {
		flagVals["NO_BROWSER"] = "1"
	}
	if *verbose {
		flagVals["VERBOSE"] = "1"
	}
	cfg, err := config.Load(*configPath, flagVals)
	if err != nil {
		return err
	}
	if cfg.Verbose {
		logx.Enabled = true
	}
	logx.V("config: source=%q dictDir=%s dbDir=%q addr=%s speexdec=%s",
		cfg.Source, cfg.DictDir, cfg.DBDir, cfg.Addr(), cfg.Speexdec)
	if cfg.DBDir != "" {
		os.Setenv("GONOW_DB_DIR", cfg.DBDir) // store + auto-ingest honor this
	}

	// first run: generate a commented config.toml so users can discover
	// the knobs; an existing file anywhere in the search order wins
	cfgFile := cfg.Source
	if cfgFile == "" && *configPath == "" {
		p, created, err := config.EnsureConfigFile()
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not create config.toml: %v\n", err)
		} else {
			cfgFile = p
			if created {
				fmt.Fprintf(os.Stderr, "created default config: %s\n", p)
			}
		}
	}

	reg, err := server.NewRegistry(cfg.DictDir)
	if err != nil {
		return fmt.Errorf("scanning %s: %w", cfg.DictDir, err)
	}
	srv := server.New(reg)
	srv.ConfigPath = cfgFile
	srv.Speexdec = cfg.Speexdec
	srv.AutoIndex = cfg.AutoIndex != "off"

	url := "http://" + cfg.Addr() + "/"
	switch {
	case !dirExists(cfg.DictDir):
		fmt.Fprintf(os.Stderr, "gonow-dict %s listening on %s\n", version, url)
		fmt.Fprintf(os.Stderr, "note: dictionary folder %s does not exist — open %s to choose one\n", cfg.DictDir, url)
	case reg.Count() == 0:
		fmt.Fprintf(os.Stderr, "gonow-dict %s listening on %s\n", version, url)
		fmt.Fprintf(os.Stderr, "note: no dictionaries found in %s — open %s to choose another folder\n", cfg.DictDir, url)
	default:
		fmt.Fprintf(os.Stderr, "gonow-dict %s serving %d dictionaries from %s on %s\n",
			version, reg.Count(), cfg.DictDir, url)
	}
	if !cfg.NoBrowser {
		go openBrowser(url)
	}

	httpSrv := &http.Server{Addr: cfg.Addr(), Handler: srv}
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
	modeStr := fs.String("mode", "prefix", "exact|prefix|fuzzy|fts")
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
	case "fuzzy":
		mode = search.Fuzzy
	case "fts":
		mode = search.FullText
	default:
		return fmt.Errorf("unknown mode %q", *modeStr)
	}
	paths, err := dict.Discover(fs.Arg(0))
	if err != nil {
		return err
	}
	var dicts []dict.Dictionary
	for _, p := range paths {
		d, err := dict.Open(p)
		if err != nil {
			fmt.Fprintf(os.Stderr, "skip %s: %v\n", p, err)
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
			fmt.Fprintf(os.Stderr, "%s: %v\n", h.Meta.Name, h.Err)
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
		fmt.Println("cache is clean — no orphaned databases")
		return nil
	}
	var total int64
	for _, o := range orphans {
		total += o.Size
		fmt.Printf("%s  (%.1f MB)\n  %s\n", o.Path, float64(o.Size)/(1<<20), o.Reason)
	}
	fmt.Printf("%d orphaned files, %.1f MB total\n", len(orphans), float64(total)/(1<<20))
	if !*force {
		fmt.Println("dry run — re-run with -f to delete")
		return nil
	}
	var failed int
	for _, o := range orphans {
		if err := os.Remove(o.Path); err != nil {
			fmt.Fprintf(os.Stderr, "delete %s: %v\n", o.Path, err)
			failed++
		}
	}
	fmt.Printf("deleted %d files\n", len(orphans)-failed)
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
