# WuWeiDict

Fast, zero-effort, self-contained, multi-format dictionary server that runs in your
browser at [http://localhost:8808](http://localhost:8808). One binary, no
dependencies; drop your dictionaries in a folder and search them all at
once.

The command is **`wudict`**. 無為 *wúwéi* — effortless action: preparation
happens in the background, on one thread by default, and gets out of your
way.

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
   [releases](https://github.com/legbehindneck/wuweidict/releases),
   `chmod +x` it (macOS/Linux).
2. Put some dictionaries in `~/Dictionaries` (or anywhere — subfolders
   are scanned too).
3. Run `wudict`. A browser tab opens; if the folder is missing or
   empty, a setup page lets you pick one or more folders with dictionaries.

## Searching

| Mode | What it does | Needs indexing? |
|---|---|---|
| **starts with** | exact matches, else headwords starting with the term (accent/case-insensitive) | no |
| **exact** | exact headword (accent/case-fold fallback: `corazon` → `corazón`) | no |
| **contains** | substring / typo-tolerant headword match, anywhere in the word (FTS5 trigram) | automatic |
| **full-text** | search inside article text, ranked by relevance | yes |

Every dictionary works immediately for starts-with/exact lookups using
its native index. The first time you search **a small headword index
is prepared in the background** — a couple of MB — so accent-insensitive
lookups (*corazon* → *corazón*) work from the next query on (disable with
`AUTO_INDEX=off`).

***Full-text*** (searching inside article text) and ***contains*** 
 (substring search) are not enabled by default as they consume more disk space. 
Click the ☰ button and enable them as needed.
Each shows its actual size; *⚡ index all* adds full-text everywhere
at once, and `wudict ingest [-contains] <file-or-folder>` does the
same from the command line.

Results stream in as each dictionary responds — the top one opens
automatically. In the ☰ panel you can **reorder** dictionaries (drag the
⠿ handle or use the ▲▼⏫⏬ buttons) to set your preferred result order, and
**enable/disable** each one (the switch) to include or exclude it from
*All dictionaries* searches; both are remembered for the current browser. Searching *All* shows a
few hits per dictionary with a **more…** link to expand any one.

Tips: `/` focuses the search box; double-click any word in an article to
look it up; click links inside articles to follow cross-references;
audio plays on click; ⇔ toggles a wide layout; ◐ cycles auto/light/dark
theme. Searches produce shareable URLs.

## Run as a service (macOS)

With a LaunchAgent at
`~/Library/LaunchAgents/com.legbehindneck.wudict.plist`
(modern `launchctl` syntax, wrapped as Makefile targets):

```sh
make mac-agent-install   # generate the plist from launchctl/*.plist.in, then:
make mac-agent-start     # launchctl bootstrap gui/$UID <plist>
make mac-agent-stop      # launchctl bootout   gui/$UID/com.legbehindneck.wudict
make mac-agent-restart   # rebuild, then launchctl kickstart -k gui/$UID/<label>
make mac-agent-status    # launchctl print    gui/$UID/<label>
make mac-agent-uninstall # stop it and delete the plist
```

The plist is **generated, not shipped**: launchd expands nothing in
`ProgramArguments` — no `~`, no `$HOME`, no `PATH` lookup — so it needs a
literal absolute path to the binary, and any committed plist would be
correct only on the machine that wrote it. `mac-agent-install` points the
agent at this checkout's `./wudict` so that `make mac-agent-restart` rebuilds
and relaunches the thing you are working on. To pin the installed copy
instead, so that moving or renaming the checkout doesn't break the agent:

```sh
make mac-agent-install AGENT_BIN="$(go env GOPATH)/bin/wudict"
```

Logs go to `~/Library/Logs/wudict.log`. `KeepAlive` restarts the agent
only after a *failed* exit — see the template for why an unconditional
`KeepAlive` and the handover behaviour would fight each other.

## Run as a service (Linux)

A **systemd user unit** — the analogue of the LaunchAgent above. WuWeiDict
reads `~/Dictionaries`, writes `~/.wudict` and binds `127.0.0.1`, so it
belongs to your session, not to root; only copying the binary into
`/usr/local/bin` needs sudo.

```sh
make linux-service-install   # sudo-installs /usr/local/bin/wudict, then writes the unit
make linux-service-start     # systemctl --user enable --now wudict.service
make linux-service-stop
make linux-service-restart   # rebuild, reinstall the binary, restart
make linux-service-status
make linux-service-uninstall # disable + remove the unit (keeps the binary)

make linux-install     # just the binary  (PREFIX=/opt/foo to relocate)
make linux-uninstall
```

The unit runs `/usr/local/bin/wudict` — the installed copy, never the one
in your checkout — and is generated for the same reason as the plist:
`ExecStart` needs an absolute path and systemd expands no `~` there. It is
written to `${XDG_CONFIG_HOME:-~/.config}/systemd/user/wudict.service`.

Logs go to the journal: `journalctl --user -u wudict -f`. `Restart=on-failure`
(not `always`) for the same reason macOS uses `SuccessfulExit=false`. To keep
the service running when you are not logged in:

```sh
sudo loginctl enable-linger "$(id -un)"
```

## Configuration

Priority: **CLI flag > environment variable > config.toml > default**.
A commented `config.toml` is generated next to the binary on first run
(or in `~/.wudict/` if that location is read-only).

| Flag | env / toml key | Default |
|---|---|---|
| `--dict-dir` | `DICT_DIR` | `~/Dictionaries` |
| `--db-dir` | `DB_DIR` | `~/.wudict/db` |
| `--ip` | `SERVER_IP` | `127.0.0.1` |
| `--port` | `SERVER_PORT` | `8808` |
| `--config` | `CONFIG_PATH` | auto-detect |
| `--no-browser` | `NO_BROWSER=1` | open browser |
| `--verbose` | `VERBOSE=1` | quiet |
| `--speexdec` | `SPEEXDEC` | found on `PATH` |
| `--use-cached` | `USE_CACHED` | off |
| — | `AUTO_INDEX` | `on` (`off` to disable) |
| `--no-compress` | `NO_COMPRESS` | off (article text compressed) |

### Several dictionary folders

`DICT_DIR` takes more than one folder — an external drive, a shared
folder, a second collection:

```sh
wudict --dict-dir ~/Dictionaries --dict-dir /Volumes/Ext/Dicts   # repeat the flag
DICT_DIR="~/Dictionaries:/Volumes/Ext/Dicts" wudict              # ":" — ";" on Windows
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
`<exe-dir>/config.toml`, `~/.wudict/config.toml`,
`/etc/wudict/config.toml`, `./config.toml`.

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
live, and which `config.toml` is in effect — with *Reveal in Finder* /
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
make install        # install both binaries into GOBIN
make check          # tidy + vet + tests
make cross          # all release platforms (pure-Go sqlite, no C toolchain)
make help           # every available target
```

Or with the Go toolchain alone. Go names an installed binary after the
last element of its package path, so there are two:

```sh
go install github.com/legbehindneck/wuweidict@latest             # → wuweidict
go install github.com/legbehindneck/wuweidict/cmd/wudict@latest  # → wudict
```

Same program either way; `wudict` is the canonical, short name.

CI builts are generated with github actions — `.github/workflows/build-release.yml` 
for the cgo flavour with internal speex decoder and optimized sqlite3 and 
`.github/workflows/build-release.yml` for purego builds.
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
