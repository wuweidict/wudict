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
| gonow | `.text.db` | this app's own portable database format (see *Sharing*, below) |

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
| **prefix** | exact matches, else headwords starting with the term | no |
| **exact** | exact headword (accent/case-fold fallback: `corazon` → `corazón`) | no |
| **fuzzy** | accent/case-insensitive headword search via FTS5 | automatic |
| **full-text** | search inside article text, ranked by relevance | yes |

Every dictionary works immediately for prefix/exact lookups using its
native index. **Fuzzy search is prepared automatically** the first time
you search a dictionary — a small headword index builds quietly in the
background, so fuzzy is ready on your next query (disable with
`AUTO_INDEX=off`). **Full-text search** (searching inside article text)
is the one deliberate step: click *full-text search* on a dictionary (or
*⚡ enable all* in the ☰ panel), or run `gonow-dict ingest <file-or-folder>`.

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
| — | `AUTO_INDEX` | `fuzzy` (`off` to disable) |

Config file search order: `--config` / `CONFIG_PATH`, then
`<exe-dir>/config.toml`, `~/.gonow-dict/config.toml`,
`/etc/gonow-dict/config.toml`, `./config.toml`.

## Command line

Run `gonow-dict --help` for the full reference. Highlights:

```sh
gonow-dict                                   # start the server (default command)
gonow-dict --dict-dir ~/Dicts --port 9090    # server with options
gonow-dict lookup ~/Dicts/Oxford.mdx word    # exact lookup, HTML to stdout
gonow-dict ingest ~/Dicts                    # index every dictionary in a folder
gonow-dict ingest -full ~/Dicts/Oxford.mdx   # index + pack media into .media.db
gonow-dict clean                             # list stale cache databases (-f deletes)
```

## Sharing dictionaries (.text.db / .media.db)

Indexing a dictionary produces `<name>-<hash>.text.db` (text + search
index) and optionally, with *pack media*, `<name>-<hash>.media.db`
(audio/images) under `~/.gonow-dict/db/`. These are ordinary SQLite
files: **copy them to another machine's db folder and they work as
standalone dictionaries** — no original source files needed (a text.db
without its media.db still works; audio/images just fall back to the
source file if present). The ☰ panel shows each dictionary's db path —
click it to copy.

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
knowledge from [pyglossary](https://github.com/ilius/pyglossary);
[mark.js](https://markjs.io/) (MIT) is bundled for highlighting.
