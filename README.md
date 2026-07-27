# gonow-dict

Fast, self-contained, multi-format dictionary server that runs in your
browser at [http://localhost:8808](http://localhost:8808). One binary, no
dependencies; drop your dictionaries in a folder and search them all at
once.

**Supported formats**

| Format | Files | Notes |
|---|---|---|
| MDict | `.mdx` + `.mdd` | companion `NAME.mdd`, `NAME.1.mdd`, … resource archives; `.spx` audio via `speexdec` |
| StarDict | `.ifo` + `.idx(.gz)` + `.dict(.dz)` | `.syn` synonyms, `res/` folder or `res.zip` resources |
| Aard2 | `.slob` | zlib/bz2/lzma2; embedded images/audio/css |
| Lingvo DSL | `.dsl`, `.dsl.dz` | UTF-8/16/32 auto-detected; `NAME.dsl.files.zip` resources; indexed automatically on first open |
| Babylon | `.bgl` | gzip block stream; source/target charset auto-detected (Latin / Cyrillic / CJK code pages); embedded images; indexed automatically on first open |
| gonow | prepared folder (`text.db`) | this app's own portable format (see *Sharing*, below) |

## Quick start

1. Download the binary for your platform from
   [releases](https://github.com/glowinthedark/gonow-dict/releases),
   `chmod +x` it (macOS/Linux).
2. Put some dictionaries in `~/Dictionaries` (or anywhere — subfolders
   are scanned too).
3. Run `gonow-dict`. A browser tab opens; if the folder is missing or
   empty, a setup page lets you pick one — validated live and saved.

## Searching

| Mode | What it does | Needs indexing? |
|---|---|---|
| **starts with** | exact matches, else headwords starting with the term (accent/case-insensitive) | no |
| **exact** | exact headword (accent/case-fold fallback: `corazon` → `corazón`) | no |
| **contains** | substring / typo-tolerant headword match, anywhere in the word (FTS5 trigram) | automatic |
| **full-text** | search inside article text, ranked by relevance | yes |

Every dictionary works immediately for starts-with/exact lookups using
its native index. **The *contains* index is prepared automatically** the
first time you search a dictionary — a small headword index builds
quietly in the background, so *contains* is ready on your next query
(disable with `AUTO_INDEX=off`). **Full-text search** (searching inside
article text) is the one deliberate step: click *full-text search* on a
dictionary (or *⚡ index all* in the ☰ panel), or run
`gonow-dict ingest <file-or-folder>`.

Results stream in as each dictionary responds — the top one opens
automatically. In the ☰ panel you can **reorder** dictionaries (drag the
⠿ handle or use the ▲▼⏫⏬ buttons) to set your preferred result order, and
**enable/disable** each one (the switch) to include or exclude it from
*All dictionaries* searches; both are remembered. Searching *All* shows a
few hits per dictionary with a **more…** link to expand any one.

Tips: `/` focuses the search box; double-click any word in an article to
look it up; click links inside articles to follow cross-references;
audio plays on click; ⇔ toggles a wide layout; ◐ cycles auto/light/dark
theme. Searches produce shareable URLs.

## Run as a service (macOS)

With a `KeepAlive` LaunchAgent at
`~/Library/LaunchAgents/com.glowinthedark.gonow-dict.plist`
(modern `launchctl` syntax, wrapped as Makefile targets):

```sh
make agent-start     # launchctl bootstrap gui/$UID <plist>
make agent-stop      # launchctl bootout   gui/$UID/com.glowinthedark.gonow-dict
make agent-restart   # rebuild, then launchctl kickstart -k gui/$UID/<label>
make agent-status    # launchctl print    gui/$UID/<label>
```

## Configuration

Priority: **CLI flag > environment variable > config.toml > default**.
A commented `config.toml` is generated next to the binary on first run
(or in `~/.gonow-dict/` if that location is read-only).

| Flag | env / toml key | Default |
|---|---|---|
| `--dict-dir` | `DICT_DIR` | `~/Dictionaries` |
| `--db-dir` | `DB_DIR` | `~/.gonow-dict/db` |
| `--ip` | `SERVER_IP` | `127.0.0.1` |
| `--port` | `SERVER_PORT` | `8808` |
| `--config` | `CONFIG_PATH` | auto-detect |
| `--no-browser` | `NO_BROWSER=1` | open browser |
| `--verbose` | `VERBOSE=1` | quiet |
| `--speexdec` | `SPEEXDEC` | found on `PATH` |
| `--use-cached` | `USE_CACHED` | off |
| — | `AUTO_INDEX` | `fuzzy` (`off` to disable) |

### Several dictionary folders

`DICT_DIR` takes more than one folder — an external drive, a shared
folder, a second collection:

```sh
gonow-dict --dict-dir ~/Dictionaries --dict-dir /Volumes/Ext/Dicts   # repeat the flag
DICT_DIR="~/Dictionaries:/Volumes/Ext/Dicts" gonow-dict              # ":" — ";" on Windows
```
```toml
DICT_DIR = ["~/Dictionaries", "/Volumes/Ext/Dicts"]
```

The setup page has an **+ add another folder** row for the same thing, and
saves your list back to `config.toml`. Folders may overlap freely: a
dictionary reachable from two of them is listed once (the folder listed
first wins), and a folder that is missing — an unplugged drive — is
reported at startup while the rest keep working.

Config file search order: `--config` / `CONFIG_PATH`, then
`<exe-dir>/config.toml`, `~/.gonow-dict/config.toml`,
`/etc/gonow-dict/config.toml`, `./config.toml`.

## Command line

Run `gonow-dict --help` for the full reference. Highlights:

```sh
gonow-dict                                   # start the server (default command)
gonow-dict --dict-dir ~/Dicts --port 9090    # server with options
gonow-dict --dict-dir ~/Dicts --dict-dir /Volumes/Ext/Dicts   # several folders
gonow-dict lookup ~/Dicts/Oxford.mdx word    # exact lookup, HTML to stdout
gonow-dict ingest ~/Dicts                    # index every dictionary in a folder
gonow-dict ingest -full ~/Dicts/Oxford.mdx   # index + pack media into the same folder
gonow-dict clean                             # list removable library items (-f deletes)
```

## Sharing dictionaries (one folder each)

Indexing a dictionary creates a folder named after it under
`~/.gonow-dict/db/` — the **library**:

```
~/.gonow-dict/db/
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
dictionary folder is empty, the setup page lists everything already
prepared under *Previously imported dictionaries* with a **Use these
dictionaries** button; that choice is remembered (`USE_CACHED = "1"`, or
`--use-cached`). Your dictionary folder must not be the db folder —
gonow-dict refuses to start if they are the same.

The ☰ panel shows each dictionary's provenance: the source file it came
from, and — expanded — the library folder holding its prepared files.
Click any path to copy.

## Speex audio (.spx)

Browsers cannot play Speex. When `speexdec` is installed
(`brew install speex` / `apt install speex`), gonow-dict transcodes
`.spx` resources to WAV on the fly and caches the result. Without it,
spx audio is unavailable (everything else works).

## Build from source

Requires [Go](https://go.dev/doc/install) (and a C compiler for the
default build):

```sh
make build          # native build (cgo sqlite, fastest)
make check          # tidy + vet + tests
make cross          # all release platforms (pure-Go sqlite, no C toolchain)
make help           # every available target
```

Releases are built by the GitHub workflow (`.github/workflows/build-release.yml`)
for macOS (arm64/amd64), Linux (amd64/arm64/armv7/armv6) and Windows
(amd64/arm64).

## License

GPL-3.0-or-later — see [LICENSE](LICENSE). Includes code derived from
[go-mdict](https://github.com/terasum/go-mdict) (GPL-3) and format
knowledge from [pyglossary](https://github.com/ilius/pyglossary) (the BGL
parser is ported from its `babylon_bgl` plugin, with streaming modeled on
[GoldenDict](https://github.com/xiaoyifang/goldendict-ng); both trace to
the reverse engineering by Raul Fernandes and Karl Grill);
[mark.js](https://markjs.io/) (MIT) is bundled for highlighting.
