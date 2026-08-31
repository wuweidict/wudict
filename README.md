# WuWeiDict

Fast, _native_, self-contained, multi-format dictionary server that runs in your
browser at [http://localhost:6888](http://localhost:6888). One native binary, no
dependencies; set the folders with your .mdx/.slob/.bgl/.ifo dictionaries, and search them all at
once.

**Supported formats**

| Format | Files                               | Notes |
|---|-------------------------------------|---|
| MDict | `.mdx` + `.mdd`                     | companion `*.mdd`, `*.1.mdd`, … resource archives; built-in `.spx` audio decoding |
| StarDict | `.ifo` + `.idx(.gz)` + `.dict(.dz)` | `.syn` synonyms, `res/` folder or `res.zip` resources |
| Aard2 | `.slob`                             | zlib/bz2/lzma2; embedded images/audio/css |
| Lingvo DSL | `.dsl`, `.dsl.dz`                   | UTF-8/16/32 auto-detected; `*.dsl.files.zip` resources; auto-indexed |
| Babylon | `.bgl`                              | gzip block stream; source/target charset auto-detected (Latin / Cyrillic / CJK code pages); embedded images; indexed automatically on first open |
| WuWeiDict | cache folder (`text.db`)            | wuDict's own SQLite-based format (see *Sharing*, below) |

## Quick start

1. Download the binary for your platform from
   [releases](https://github.com/wuweidict/wudict/releases), rename to `wudict`, 
   `chmod +x wudict` (macOS/Linux) and move to a folder in `$PATH`, e.g. `/usr/local/bin`.
2. Run `wudict` or `./wudict` if the file is in the current folder. On windows use the installer [`wudict-windows-x64-setup-<x.y.z>.exe`](https://github.com/wuweidict/wudict/releases/latest) or download the standalone executable `wudict-windows-amd64-cgo.exe` and then double-click to run. For macOS an app bundle is provided too (it is not signed with a commercial Apple Developer Certificate, will be flagged by the system as unverified by Apple and requires additional steps to de-quarantine the app as described in the [manual](https://wuweidict.github.io/wudict/apps/macos/)).
3. By default `wudict` searches for dictionaries under `~/Dictionaries` (including subfolders); 
   if the dictionary folder is missing or
   empty, a setup page opens where you can set custom folders with dictionaries.

## Adding dictionaries 
Dictionary folders can be configured in the browser via the [browser setup page http://localhost:6888/setup](http://localhost:6888/setup) when `wudict` is running. The browser setup page is a convenience 
for writing `DICT_DIR` in the configuration file at `~/.wudict/wudict.toml`.

### Multiple dictionary folders

`DICT_DIR` accepts more than one folder:

As an alternative to the [setup page](http://localhost:6888/setup) you can configure the dictionary folders from
the console via cli args, env vars or by directly editing the config file at `~/.wudict/wudict.toml`:
```sh
# as one or more CLI args:
wudict --dict-dir ~/Dictionaries --dict-dir /Volumes/Data/Dicts   # repeat the flag

# or via an env var:
DICT_DIR="~/Dictionaries:/Volumes/Data/Dicts" wudict              # separate with ":" for linux/mac and ";" on Windows
```

in `wudict.toml`:
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
Each shows its actual size; *⚡ index all* adds full-text for all dictionaries
at once, and `wudict ingest [-contains] <file-or-folder>` does the
same from the command line.

Results stream lazily as each dictionary responds — the top one opens
automatically. In the ☰ panel you can **reorder** dictionaries (drag the
⠿ handle or use the ▲▼⏫⏬ buttons) to set your preferred result order, and
**enable/disable** each one (the switch) to include or exclude it from
*All dictionaries* searches; both are remembered. Searching *All* shows a
few hits per dictionary with a **more…** link to expand any one. 
A dictionary that was disabled for *All dictionaries* searches can still 
be searched by selecting it in the dictionary dropdown.

Tips: `/` focuses the search box; double-click any word in an article to
look it up; click links inside articles to follow cross-references —
these stay inside the dictionary you are reading, and widen to all of them
only if that dictionary has no such entry;
audio plays on click; ⊞ expands all results (⊟ closes
them again — for the current page only, never remembered);
⇔ toggles a wide layout; ◐ cycles auto/light/dark
theme. Search URLs are bookmarkable.

## Run as an app (macOS)

`make mac-app-install` builds **wuDict.app** and installs it into
`~/Applications` (no sudo, no admin prompt):

```sh
make mac-app            # dist/wuDict.app — universal-ready, ad-hoc signed
make mac-app-install    # copy it to ~/Applications (APP_DEST= to relocate)
```

The bundle is the same binary; what it changes is the launch. `LSUIElement`
keeps it out of the Dock and it puts a **menu-bar icon** up instead — running
state, open in browser, rescan, open the dictionary folder, quit. That icon is
the only interface, so its log goes to `~/Library/Logs/wudict.log`. Overrides:
`APP_ID=` (bundle identifier), `CODESIGN_ID=` (a Developer ID instead of the
ad-hoc signature), `MACOS_MIN=`. See more about [running on macOS](https://wuweidict.github.io/wudict/apps/macos/).

## Run as an app (Windows)

There is **one** `wudict.exe`. From `cmd` or PowerShell it is an ordinary
command-line program — it prints, pipes and returns an exit code. Double-clicked,
started from a shortcut, or used to open a dictionary file, it releases the
console window, and shows a **tray icon** instead, logging to
`%LOCALAPPDATA%\wudict\wudict.log`. See also [running on windows](https://wuweidict.github.io/wudict/apps/windows/).

`make win-installer` compiles the installer (needs
[Inno Setup 6.3+](https://jrsoftware.org/isinfo.php); github's CI builds it for every
release). The wudict setup wizard has options to add a desktop shortcut,
*start at sign-in*, add to `PATH`, and **Open with → wuDict** for `.mdx`,
`.dsl`, `.slob` and `.bgl`.

Opening a dictionary file — from the installer's association or by hand as
`wudict path\to\some.mdx` — serves the **parent folder** and opens
the browser there.

## Run as a service (macOS)

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



**`BROWSER_EXTENSIONS`** decides which browser extensions may look words up in
your server from the pages they run in. Blank (the default) lets any installed
extension reach the read-only dictionary API — `/api/dicts`, `/api/search`,
`/res/` — and nothing else: never your preferences, your config or your library.
Set it to allow only the extensions you name:

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
To include the media into the bundle the corresponding <kbd>media</kbd> button must be clicked in the dictionary panel.

When present, on first run, when the dictionary folder is empty, 
the setup page lists previously indexed
dictionaries under *Previously imported dictionaries* with a **Use these
dictionaries** button; that choice is remembered (`USE_CACHED = "1"`, or
`--use-cached`). Your dictionary folder must not be the db folder —
wudict refuses to start if they are the same.

The ☰ panel shows each dictionary's provenance: the source file it came
from, and — expanded — the library folder holding its SQLite database files.
Click any path to copy.

At the foot of the panel, **Folders & configuration** shows which folders
are being scanned (with per-folder counts), where indexed dictionaries
are located, and which `wudict.toml` is in effect — with *Reveal in Finder* /
*Show in File Explorer* / *Open Containing Folder*, depending on your
system. **Edit folders…** opens the dictionary folders editor.
If DICT_DIR was set via `--dict-dir` cli flag or `DICT_DIR` env var then the editor cannot override those paths and will show a warning.

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

## License

GPL-3.0-or-later — see [LICENSE](LICENSE). Includes code derived from
[go-mdict](https://github.com/terasum/go-mdict) (GPL-3) and format
knowledge from [pyglossary](https://github.com/ilius/pyglossary) (the BGL
parser is ported from its `babylon_bgl` plugin, with streaming modeled on
[GoldenDict](https://github.com/xiaoyifang/goldendict-ng); both trace to
the reverse engineering by Raul Fernandes and Karl Grill).
