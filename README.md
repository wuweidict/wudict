# WuWeiDict

Fast, _native_, self-contained, multi-format dictionary server that runs in your
browser at [http://localhost:6888](http://localhost:6888). One native binary, no
dependencies; set the folders with your .mdx/.slob/.bgl/.zim/.ifo dictionaries, and search them all at
once.

Runs natively on android, mac, windows, linux and even raspberry pi.

**Supported formats**

| Format | Files                               | Notes |
|---|-------------------------------------|---|
| MDict | `.mdx` + `.mdd`                     | companion `*.mdd`, `*.1.mdd`, … resource archives; built-in `.spx` audio decoding |
| StarDict | `.ifo` + `.idx(.gz)` + `.dict(.dz)` | `.syn` synonyms, `res/` folder or `res.zip` resources |
| Aard2 | `.slob`                             | zlib/bz2/lzma2; embedded images/audio/css |
| Lingvo DSL | `.dsl`, `.dsl.dz`                   | UTF-8/16/32 auto-detected; `*.dsl.files.zip` resources; auto-indexed |
| Babylon | `.bgl`                              | gzip block stream; source/target charset auto-detected (Latin / Cyrillic / CJK code pages); embedded images; indexed automatically on first open |
| ZIM | `.zim`                              | Kiwix/Wikimedia offline archives; see https://library.kiwix.org
| WuWeiDict | cache folder (`text.db`)            | wuDict's own SQLite-based format (see *Sharing*, below) |

## Quick start

1. Download the binary for your OS from
   [releases](https://github.com/wuweidict/wudict/releases), rename to `wudict`, 
   `chmod +x wudict` (macOS/Linux) and move to a folder in `$PATH`, e.g. `/usr/local/bin`.
2. Run `wudict` or `./wudict` if the file is in the current folder. On windows use the installer [`wudict-windows-x64-setup-<x.y.z>.exe`](https://github.com/wuweidict/wudict/releases/latest) or download the standalone executable `wudict-windows-amd64-cgo.exe` and then double-click to run. For macOS an app bundle is provided too (it is not signed with a commercial Apple Developer Certificate, will be flagged by the system as unverified by Apple and requires additional steps to de-quarantine the app as described in the [manual](https://wuweidict.github.io/wudict/apps/macos/)).
3. By default `wudict` searches for dictionaries under `~/Dictionaries` (including subfolders); 
   if the dictionary folder is missing or
   empty, a setup page opens where you can configure your dictionary folders.

## Adding dictionaries 
With wuDict running dictionary folders can be configured from [http://localhost:6888/setup](http://localhost:6888/setup). The browser setup page is a convenience 
for writing `DICT_DIR` in the configuration file at `~/.wudict/wudict.toml` and other actions.

### Multiple dictionary folders

You can configure the dictionary folders from
the console via cli args, env vars or by directly editing the config file at `~/.wudict/wudict.toml` (recommended):
```sh
# as one or more CLI args:
wudict --dict-dir ~/Dictionaries --dict-dir /Volumes/Data/Dicts   # repeat the flag

# or via an env var:
DICT_DIR="~/Dictionaries:/Volumes/Data/Dicts" wudict              # separate multiple folders with ":" on linux/mac and ";" on Windows
```

in `wudict.toml` (the recommended way):
```toml
DICT_DIR = ["~/Dictionaries", "/Volumes/Data/Dicts"]
```

## Searching

| Mode | What it does | Needs indexing? |
|---|---|---|
| **starts with** | exact matches, else headwords starting with the term (accent/case-insensitive) | no |
| **exact** | exact headword (accent/case-fold fallback: `corazon` → `corazón`) | no |
| **contains** | substring / typo-tolerant headword match, anywhere in the word (FTS5 trigram) | ad-hoc |
| **full-text** | search inside article text, ranked by relevance | yes |

Every dictionary works immediately for starts-with/exact lookups using
the native index. The first time you search **a small headword index
is prepared in the background** — a couple of MB — so accent-insensitive
lookups (*corazon* → *corazón*) work seamlessly (disable with `AUTO_INDEX=off`).

***Full-text*** (searching inside article text) and ***contains*** 
 (substring search) are not enabled by default as they consume more disk space. 
Click the ☰ button and enable them as needed.
For each dictionary the index size is displayed; *⚡ index all* adds full-text for all dictionaries
at once, and `wudict ingest [-contains] <file-or-folder>` does the
same from the command line.

Results stream live as each dictionary responds — the top one opens
automatically. In the ☰ panel you can **reorder** dictionaries (drag the
⠿ handle or use the ▲▼⏫⏬ buttons) to set your preferred result order, and
**enable/disable** each one (the switch) to include or exclude it from
*All dictionaries* searches; both are remembered.
A dictionary that was disabled for *All dictionaries* searches can still 
be searched by selecting it in the dictionary dropdown.

Tips: `/` focuses the search box; double-click any word in an article to
look it up; click links inside articles to follow cross-references;
audio plays on click; ⊞ expands all results (⊟ closes
them again — for the current page only, never remembered);
⇔ toggles a wide layout; ◐ cycles auto/light/dark
theme. Search URLs are bookmarkable.

## Run as an app (macOS)

For macOS you can either run the `wudict` binary from a terminal, or as an 
alternative use the wudict-macos-app.zip from [releases](https://github.com/wuweidict/wudict/releases) 
which wraps `wudict` into an macOS app bundle.

### Building the macOS bundle from source

Run `make mac-app-install` from project root to build **wuDict.app** and install it to
`~/Applications` (no sudo, no admin prompt):

```sh
make mac-app            # dist/wuDict.app — universal-ready, ad-hoc signed
make mac-app-install    # copy it to ~/Applications (APP_DEST= to relocate)
```

The bundle is the same binary as console `wudict` which which spawns no terminal window, and 
additionally puts  a **menu-bar icon** up for common actions. See more about [running on macOS](https://wuweidict.github.io/wudict/apps/macos/).

## Run as an app (Windows)

In windows `wudict` runs from `cmd` or PowerShell as an ordinary
command-line program. When double-clicked, started from a shortcut, or by double-clicking 
a dictionary file, it hides the  console window, and shows a **tray icon** instead, logging to
`%LOCALAPPDATA%\wudict\wudict.log`. See also [running on windows](https://wuweidict.github.io/wudict/apps/windows/).


## Run wudict as a service (macOS)

`wudict` can be installed as a `launchctl` LaunchAgent using Makefile targets:

```sh
# from project root
make mac-agent-install   # generate the plist from launchctl/*.plist.in, then:
make mac-agent-start     # launchctl bootstrap gui/$UID <plist>
make mac-agent-stop      # launchctl bootout   gui/$UID/com.legbehindneck.wudict
make mac-agent-restart   # rebuild, then launchctl kickstart -k gui/$UID/<label>
make mac-agent-status    # launchctl print    gui/$UID/<label>
make mac-agent-uninstall # stop it and delete the plist
```

## Run as a service (Linux)

On linux `wudict` can be installed as a **systemd user unit** — only copying the binary into
`/usr/local/bin` needs sudo, the service itself runs with user permissions and start/stop does not require sudo.

```sh
# from project root
make linux-service-install   # sudo-installs /usr/local/bin/wudict, then writes the user unit
make linux-service-start     # systemctl --user enable --now wudict.service
make linux-service-stop
make linux-service-restart   # rebuild, reinstall the binary, restart
make linux-service-status
make linux-service-uninstall # disable + remove the unit (keeps the binary)

make linux-install     # just the binary  (PREFIX=/opt/foo to relocate)
make linux-uninstall
```

The systemd unit expects the executable to be at `/usr/local/bin/wudict`.

To keep the service running when you are not logged in:

```sh
sudo loginctl enable-linger "$(id -un)"
```

## Configuration

Priority: **CLI flag > environment variable > wudict.toml > default**.
A commented `~/.wudict/wudict.toml` is generated on first run, and the
config file path is printed on startup.

| Flag | env / toml key | Default                       |
|---|---|-------------------------------|
| `--dict-dir` | `DICT_DIR` | `~/Dictionaries`              |
| `--db-dir` | `DB_DIR` | `~/.wudict/db`                |
| `--ip` | `SERVER_IP` | `127.0.0.1`                   |
| `--port` | `SERVER_PORT` | `6888`                        |
| `--config` | `CONFIG_PATH` | auto-detect                   |
| `--no-browser` | `NO_BROWSER=1` | open browser                  |
| `--verbose` | `VERBOSE=1` | detailed logging              |
| `--speexdec` | `SPEEXDEC` | found on `PATH`               |
| `--use-cached` | `USE_CACHED` | off                           |
| — | `AUTO_INDEX` | `on` (`off` to disable)       |
| `--no-compress` | `NO_COMPRESS` | off (article text compressed) |
| — | `BROWSER_EXTENSIONS` | any extension may look words up |



**`BROWSER_EXTENSIONS`** sets which browser extensions may use the `wudict` server. 
Blank (the default) lets any installed  extension reach the read-only dictionary API — `/api/dicts`, `/api/search`,
`/res/`.

Set it to allow only specific extensions:

```toml
BROWSER_EXTENSIONS = ["chrome-extension://abcdefghijklmnopabcdefghijklmnop"]
```

(Firefox generates a fresh `moz-extension://` id for every installation, so
there is no stable origin to pin there.)

See also: [chrome/firefox browser extension](https://wuweidict.github.io/wudict/extension/)

Config file search order: `--config` / `CONFIG_PATH`, then
`<exe-dir>/wudict.toml`, `~/.wudict/wudict.toml`,
`/etc/wudict/wudict.toml`.

**Portable mode.** A `wudict.toml` can also be placed in the same folder as the executable.

**`~/.wudict/state.json`** stores dictionary search order and enabled/disable state. 

## Command line

Run `wudict --help` for the full reference. Highlights:

```sh
wudict                                   # start the server (default command)

# start server with custom options
wudict --dict-dir ~/Dicts --port 9090    
wudict --dict-dir ~/Dicts --dict-dir /Volumes/Ext/Dicts   # several folders

# search for a word in a specific dictionary; plain text to stdout 
# optional, request specific format with: "-format=raw|clean|text"
wudict lookup ~/Dicts/Oxford.mdx water

# search ALL dictionaries in folder; plain text to stdout
wudict searchall -dict-dir /path/to/dicts flight

# index every dictionary in a given folder
wudict ingest ~/Dicts                    

# index + pack media
wudict ingest -full ~/Dicts/Oxford.mdx   

# clean leftover files
wudict clean

# list removable library items (-f deletes)

# list headwords (or keys) in a specific dictionary
wudict keys ~/Dicts/Oxford.mdx
wudict keys ~/Dicts/Oxford.mdd

# extract a resource (pass the key shown by `wudict keys ...`)
wudict res ~/Dicts/Oxford.mdd audio/a.mp3
```

For more on command line usage see the manual:
- [wuDict Command Line](https://wudict.legbehindneck.com/reference/cli/)

## Sharing dictionaries (one folder each)

Indexing a dictionary creates a corresponding folder under
`~/.wudict/db/` — the **library**:

```
~/.wudict/db/
  Oxford/
    text.db     articles + search indexes
    media.db    audio/images (only after "pack media")
    info.txt    what this is, where it came from
    res/        optional — files that replace the dictionary's own
```

A dictionary is one folder, so it moves as one thing: **copy, move or zip
it and share**. On the other machine, drop it into a dictionary
folder and it works — no original source files needed. a `text.db`
without its `media.db` still works (with no media). 
To generate the media pack click the <kbd>media</kbd> 
in the dictionary panel.

The ☰ panel shows each dictionary's provenance: the source file it came
from, and — expanded — the library folder holding its SQLite database files.
Click a path to copy it to clipboard.

At the foot of the panel, **Folders & configuration** shows which folders
are being scanned (with per-folder counts), where indexed dictionaries
are located, and which `wudict.toml` is in effect — with *Reveal in Finder* /
*Show in File Explorer* / *Open Containing Folder*, depending on your
system. **Edit folders…** opens the dictionary folders editor.

## Patching dictionary's files

Dictionaries carry their own stylesheets, scripts, images and audio
inside the dictionary file. You can provide your own 'patched' versions 
for example to fix broken or missing resources in the `res/` subfolder in 
wuDict's DB folder at under `~/.wudict/db/<some-dict-name/res`. Any
file in the `./res` folder is served **instead of** the original.

```
~/.wudict/db/Cambridge English Dictionary Online/
  text.db
  res/
    jquery.js          ← replaces the dictionary's own (damaged) copy
    js/entry.js        ← supplies one the dictionary never contained
    css/style.css
```

Subfolders work, and they matter: articles routinely reference
`js/…` and `css/…`, so mirror whatever path the article is referencing.

This works for any file and any dictionary, and it is also how you'd
patch a CSS stylesheet you want to modify or swap an icon. Nothing is modified
inside the dictionary itself — remove the file and you're back to the original.

One exception: a `.spx` audio file placed in `res/` is served as-is,
**not** transcoded to WAV the way a `.spx` inside the dictionary is. Supply `.mp3` or `.wav` instead.

## Custom styles

`res/` patches one file of one dictionary. To restyle **everything** — wuDict
itself and every article — write your own CSS in two optional files beside the
`wudict.toml` in effect, usually `~/.wudict/style/`:

```
~/.wudict/style/
  app.css       wuDict itself — its colours, its own layout
  article.css   what dictionaries render, in every article
```

The ☰ panel's **Custom styles…** opens an editor for app and article styles, docked at the
bottom of the page so you get live preview of your CSS changes as you type. 
Several presets are provided — a compact mobile view, sepia, high contrast, true-black OLED, 
a wider column, justified text, normalize tables.


```css
/* app.css — sepia in light mode, page and definitions together */
html:not([data-dark]){
  --bg:#f4ecd8; --bg-card:#faf3e3;
  --fg:#3b3229; --line:#e0d5bd;
  --wd-article-bg:#faf3e3; --wd-article-fg:#3b3229;
}
```

```css
/* article.css — reclaim the side space a desktop dictionary reserves */
@media (max-width: 700px){
  :host, :root > body         { margin-inline:0 !important; padding-inline:0 !important }
  :host > *, :root > body > * { margin-inline:0 !important; padding-inline:0 !important }
}
```

Undoing is the same three moves everywhere else: <kbd>⌘</kbd> + <kbd>Z</kbd> 
(on windows <kbd>Ctrl</kbd> + <kbd>Z</kbd>) lets you undo changes.
A dot on a tab means that box has unsaved changes. If a rule ever hides the app, apend `/?style=off` in the URL
and the page is served with default styles.

## Disk use

A prepared dictionary is usually **smaller than the file it came from**:
article text is compressed, and full-text search and contains indexes are only built when you ask for it.

| what | cost (40k-entry dictionary, 45.6 MB source) |
|---|---|
| finding a headword — exact, prefix, accent-insensitive | ~2 MB, always on |
| full-text search | ~12 MB, one click |
| contains (substring) | ~2.4 MB, one click |
| packed media | as large as the images/audio |

The ☰ panel shows these as switches per dictionary, with their real sizes —
click to add, click again to remove. Removing is offered only while the
original file is still on disk, since that is what makes it reversible; a
dictionary whose source is gone shows its switches locked, because the
prepared data is then the only copy.

`NO_COMPRESS = "1"` (or `--no-compress`) stores article text verbatim:
roughly 3x larger databases, marginally faster reads.

## Speex audio (.spx)

Browsers cannot play Speex. wuDict internally transcodes
`.spx` resources to WAV on the fly and caches the result. 
If wuDict was built without the internal speex decoder (the purego flavours) 
then the external `speexdec` utility can be used (for mac: `brew install speex`, 
linux: `apt install speex`, etc).

## Build from source

Requires [Go](https://go.dev/doc/install) (and a C compiler for the
default cgo build):

```sh
make build          # native build (cgo sqlite, fastest) → ./wudict
make install        # install the binary into GOBIN
make check          # tidy + vet + tests
make cross          # all release platforms (pure-Go sqlite, no C toolchain)
make help           # every available target
```

Or with the Go toolchain alone:

```sh
# recommended — fastest (cgo sqlite + built-in speex), needs a C compiler
go install -tags sqlite_fts5 github.com/wuweidict/wudict@latest

# no C compiler? drop the tag — pure-Go sqlite, .spx audio via external speexdec
go install github.com/wuweidict/wudict@latest
```

Both produce a working `wudict`; the tag only chooses the SQLite driver.
Passing `-tags sqlite_fts5` on a machine without a C toolchain quietly
falls back to the pure-Go build rather than failing.

CI builds are generated with github actions — `.github/workflows/build-cgo.yml`
for the cgo flavour with internal speex decoder and optimized sqlite3, and
`.github/workflows/build-purego.yml` for purego builds.
Supported OS's: macOS (arm64/amd64), Linux (amd64/arm64/armv7/armv6) and Windows
(amd64/arm64).
## Acknowledgements

Almost nothing here was invented by this project. The formats it reads are
closed, and they are readable at all because other people spent years working
them out and then wrote down what they found.

### Prior art and format knowledge

- **[pyglossary](https://github.com/ilius/pyglossary)** — the reference this
  project was built against. Its plugins are the clearest working description of
  MDX/MDD, StarDict, Slob, DSL and BGL that exists anywhere, and the BGL parser
  here is ported from its `babylon_bgl` plugin. Thanks to
  **[@ilius](https://github.com/ilius)** and pyglossary's contributors for
  sustained, meticulous work on formats nobody else kept maintaining.
- **[GoldenDict](http://goldendict.org/)** and the actively developed fork
  **[goldendict-ng](https://github.com/xiaoyifang/goldendict-ng)** — the program
  that made these dictionaries worth owning, and the model for the BGL reader's
  streaming decompression.
- **[medict](https://github.com/terasum/medict)** by Quan Chen (formerly
  `go-mdict`) — the MDX/MDD parser in `internal/gomdict` is derived from it.
- **Raul Fernandes** and **Karl Grill** — the original reverse engineering of the
  Babylon BGL format.
- **[slob](https://github.com/itkach/slob)** and Aard 2 by Igor Tkach,
  **[StarDict](https://github.com/huzheng001/stardict-3)**, ABBYY Lingvo's DSL,
  and **[Kiwix](https://kiwix.org/)**'s ZIM.

### Libraries and components

- **[SQLite](https://sqlite.org/)** and its
  **[FTS5](https://sqlite.org/fts5.html)** extension — the entire prepared
  library: storage, headword index, and full-text search across a hundred
  dictionaries at once. **FTS5** made this project possible. 
- Public domain, and maintained at that standard for twenty-five years.
- **[mattn/go-sqlite3](https://github.com/mattn/go-sqlite3)** — cgo SQLite
  driver; the default optimized build.
- **[modernc.org/sqlite](https://gitlab.com/cznic/sqlite)** — SQLite translated
  to pure Go, so releases build for every platform without a C toolchain. Both
  drivers are first-class.
- **[Speex](https://www.speex.org/)** — Jean-Marc Valin and the
  [Xiph.Org Foundation](https://xiph.org/). `internal/speex` vendors the
  reference decoder so `.spx` pronunciations play without an external tool, and
  it includes **[kiss_fft](https://github.com/mborgerding/kissfft)** by Mark
  Borgerding.
- **[anchore/go-lzo](https://github.com/anchore/go-lzo)** — LZO1X
  decompression for MDX record blocks, written from the kernel's format
  documentation and **[lzokay](https://github.com/AxioDL/lzokay)** by Jack
  Andersen.
- **[c0mm4nd/go-ripemd](https://github.com/c0mm4nd/go-ripemd)** — RIPEMD, for
  MDX key-block decryption.
- **[cespare/xxhash](https://github.com/cespare/xxhash)** — MDX v3 checksums.
- **[klauspost/compress](https://github.com/klauspost/compress)** — zstd and
  deflate, for ZIM clusters and Slob bins.
- **[ulikunitz/xz](https://github.com/ulikunitz/xz)** — LZMA, for Slob and ZIM
  content.
- **[aaaton/golem](https://github.com/aaaton/golem)** — English lemmatiser, so
  that *understood* finds *understand*.
- **[golang.org/x/net](https://pkg.go.dev/golang.org/x/net)** — HTML tokeniser,
  used to rewrite article markup and resolve dictionary resources.
- **[gogpu/systray](https://github.com/gogpu/systray)** and
  **[godbus/dbus](https://github.com/godbus/dbus)** — the tray icon, and its
  Linux desktop integration.
- **[Inno Setup](https://jrsoftware.org/isinfo.php)** by Jordan Russell — the
  Windows installer: small, scriptable, and free for as long as Windows has had
  installers.
- **[Go](https://go.dev/)** — the stack that lets us cross-compiles a static binary for
  eight platforms from one machine, including android.

Not affiliated with, or endorsed by, any of the above.

## License

GPL-3.0-or-later — see [LICENSE](LICENSE).

`wudict licenses` prints the full third-party notices from inside the binary;
the same text is in [THIRD-PARTY-NOTICES.md](THIRD-PARTY-NOTICES.md).

Third-party code included in this repository:

| Component | Origin | License |
|---|---|---|
| `internal/gomdict` (MDX/MDD parser) | [medict](https://github.com/terasum/medict), © 2023 Quan Chen | GPL-3.0-or-later |
| `internal/format/bgl` (BGL parser) | ported from [pyglossary](https://github.com/ilius/pyglossary)'s `babylon_bgl` | GPL-3.0-or-later |
| `internal/speex/clib` (Speex decoder) | [Speex](https://www.speex.org/), © Jean-Marc Valin / Xiph.Org, Analog Devices | BSD-3-Clause |
| `internal/speex/clib` (`kiss_fft`) | [kissfft](https://github.com/mborgerding/kissfft), © Mark Borgerding | BSD-3-Clause |

Dependencies fetched at build time keep their own licenses; every one of them,
with its license text, is listed in
[THIRD-PARTY-NOTICES.md](THIRD-PARTY-NOTICES.md) (regenerate with
`make notices`).
