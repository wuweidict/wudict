# WuWeiDict

Fast, self-contained, multi-format dictionary server that runs in your
browser at [http://localhost:6888](http://localhost:6888). One binary, no
dependencies; drop your dictionaries in a folder and search them all at
once.

**Supported formats**

| Format | Files                               | Notes |
|---|-------------------------------------|---|
| MDict | `.mdx` + `.mdd`                     | companion `NAME.mdd`, `NAME.1.mdd`, … resource archives; `.spx` audio via `speexdec` |
| StarDict | `.ifo` + `.idx(.gz)` + `.dict(.dz)` | `.syn` synonyms, `res/` folder or `res.zip` resources |
| Aard2 | `.slob`                             | zlib/bz2/lzma2; embedded images/audio/css |
| Lingvo DSL | `.dsl`, `.dsl.dz`                   | UTF-8/16/32 auto-detected; `NAME.dsl.files.zip` resources; indexed automatically on first open |
| Babylon | `.bgl`                              | gzip block stream; source/target charset auto-detected (Latin / Cyrillic / CJK code pages); embedded images; indexed automatically on first open |
| WuWeiDict | cache folder (`text.db`)            | this app's own portable format (see *Sharing*, below) |

## Quick start

1. Download the binary for your platform from
   [releases](https://github.com/legbehindneck/wudict/releases), rename to `wudict`, 
   `chmod +x wudict` (macOS/Linux) and move to a folder in `$PATH`, e.g. `/usr/local/bin`.
2. Put some dictionaries in `~/Dictionaries` (or anywhere — subfolders
   are scanned too).
3. Run `wudict` or `./wudict` if the file is in the current folder. 
A browser tab opens; if the dictionary folder is missing or
   empty, a setup page lets you pick one or more folders with dictionaries.

## Adding dictionaries 
Dictionary folders can be configured in the browser via the [browser setup page](http://localhost:6888/setup). 

### Several dictionary folders

`DICT_DIR` accepts more than one folder:

```sh
# as one or more CLI args:
wudict --dict-dir ~/Dictionaries --dict-dir /Volumes/Ext/Dicts   # repeat the flag

# or via an env var:
DICT_DIR="~/Dictionaries:/Volumes/Ext/Dicts" wudict              # ":" — ";" on Windows
```

in `wudict.toml`:
```toml
DICT_DIR = ["~/Dictionaries", "/Volumes/Ext/Dicts"]
```



## Searching

| Mode | What it does | Needs indexing? |
|---|---|---|
| **starts with** | exact matches, else headwords starting with the term (accent/case-insensitive) | no |
| **exact** | exact headword (accent/case-fold fallback: `corazon` → `corazón`) | no |
| **contains** | substring / typo-tolerant headword match, anywhere in the word (FTS5 trigram) | automatic |
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

Results stream in as each dictionary responds — the top one opens
automatically. In the ☰ panel you can **reorder** dictionaries (drag the
⠿ handle or use the ▲▼⏫⏬ buttons) to set your preferred result order, and
**enable/disable** each one (the switch) to include or exclude it from
*All dictionaries* searches; both are remembered. Searching *All* shows a
few hits per dictionary with a **more…** link to expand any one.

Tips: `/` focuses the search box; double-click any word in an article to
look it up; click links inside articles to follow cross-references —
these stay inside the dictionary you are reading, and widen to all of them
only if that dictionary has no such entry;
audio plays on click; ⊞ opens every dictionary's results at once (⊟ closes
them again — for the current page only, never remembered);
⇔ toggles a wide layout; ◐ cycles auto/light/dark
theme. Search URLs are bookmarkable.

## Run as a service (macOS)

`wudict` can be installed as a `launchctl` LaunchAgent using Makefile targets:

```sh
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
make linux-service-install   # sudo-installs /usr/local/bin/wudict, then writes the user unit
make linux-service-start     # systemctl --user enable --now wudict.service
make linux-service-stop
make linux-service-restart   # rebuild, reinstall the binary, restart
make linux-service-status
make linux-service-uninstall # disable + remove the unit (keeps the binary)

make linux-install     # just the binary  (PREFIX=/opt/foo to relocate)
make linux-uninstall
```

The ststemd unit expects the executable to be at `/usr/local/bin/wudict`.

To keep  the service running when you are not logged in:

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



Config file search order: `--config` / `CONFIG_PATH`, then
`<exe-dir>/wudict.toml`, `~/.wudict/wudict.toml`,
`/etc/wudict/wudict.toml`.

**Portable mode.** A `wudict.toml` can also be placed in the same folder as the executable.

**`~/.wudict/state.json`** stores dictionary search order and enabled/disable state. 

## Command line

Run `wudict --help` for the full reference. Highlights:

```sh
wudict                                   # start the server (default command)
wudict --dict-dir ~/Dicts --port 9090    # server with options
wudict --dict-dir ~/Dicts --dict-dir /Volumes/Ext/Dicts   # several folders
wudict lookup ~/Dicts/Oxford.mdx word    # exact lookup, HTML to stdout
wudict ingest ~/Dicts                    # index every dictionary in a folder
wudict ingest -full ~/Dicts/Oxford.mdx   # index + pack media into the same folder
wudict clean                             # list removable library items (-f deletes)
```

## Sharing dictionaries (one folder each)

Indexing a dictionary creates a folder named after it under
`~/.wudict/db/` — the **library**:

```
~/.wudict/db/
  Oxford/
    text.db     articles + search indexes
    media.db    audio/images (only after "pack media")
    info.txt    what this is, where it came from
```

A dictionary is one folder, so it moves as one thing: **copy, move or zip
it and hand it over**. On the other machine, drop it into a dictionary
folder and it works — no original source files needed (a `text.db`
without its `media.db` still works; audio and images just fall back to
the source file when one is present).

Databases from earlier versions (the old flat `<name>-<hash>.text.db`
files) are moved into folders automatically on startup — a rename, never
a re-index, and nothing is deleted.

Your own library is used only if you say so. On first run, when the
dictionary folder is empty, the setup page lists previously cached
sources under *Previously imported dictionaries* with a **Use these
dictionaries** button; that choice is remembered (`USE_CACHED = "1"`, or
`--use-cached`). Your dictionary folder must not be the db folder —
wudict refuses to start if they are the same.

The ☰ panel shows each dictionary's provenance: the source file it came
from, and — expanded — the library folder holding its prepared files.
Click any path to copy.

At the foot of the panel, **Folders & configuration** shows which folders
are being scanned (with per-folder counts), where prepared dictionaries
live, and which `wudict.toml` is in effect — with *Reveal in Finder* /
*Show in File Explorer* / *Open Containing Folder*, depending on your
system. **Edit folders…** opens the folder editor (also at `/setup`) at
any time. If a folder came from `--dict-dir` or `DICT_DIR`, the editor
says so: saving to the config file cannot override them.

## Disk use

A prepared dictionary is usually **smaller than the file it came from**:
article text is compressed, and only the indexes you ask for are built.

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

Browsers cannot play Speex. WuWeiDict internally transcodes
`.spx` resources to WAV on the fly and caches the result. 
If WuWeiDict was built without the internal speex decoder then 
the external `speexdec` utility can be used (for mac: `brew install speex`, linux: `apt install speex`, etc).

## Build from source

Requires [Go](https://go.dev/doc/install) (and a C compiler for the
default build):

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
go install -tags sqlite_fts5 github.com/legbehindneck/wudict@latest

# no C compiler? drop the tag — pure-Go sqlite, .spx audio via external speexdec
go install github.com/legbehindneck/wudict@latest
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
the reverse engineering by Raul Fernandes and Karl Grill);
[mark.js](https://markjs.io/) (MIT) is bundled for highlighting.
