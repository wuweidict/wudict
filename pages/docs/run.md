---
title: Run
description: Run wudict for the first time — point it at your dictionary folders, watch it serve your library at localhost:6888, and configure it your way.
tags:
  - Quickstart
---

# Usage

## Windows

Download the corresponding **`wudict.exe`** file.

## Linux/macOS

Download the `wudict` binary, make it executable (`chmod +x wudict`), and move to a folder in your `$PATH`, such as `/usr/local/bin`; now it can be started with:

``` sh
wudict
```

To start the file from the current folder, for example after building locally with `make build` start with:

``` sh
./wudict
```

## `DICT_DIR` — the folder(s) with dictionaries

By default `wudict` assumes your dictionaries are in **`~/Dictionaries`** (including subfolders).
If this folder does not exist or if it is empty, then a setup page will open at [http://localhost:6888/setup](http://localhost:6888/setup) where you can
add the folders with dictionaries.

The setup page will add the configurations to `~/.wudict/wudict.toml` — you can also edit this file directly.

### DICT_DIR configuration

=== "Command line"

    ``` sh title="repeated flag — anything after the first is extra"
    wudict --dict-dir ~/Dictionaries --dict-dir /Volumes/Data/Dicts
    ```

=== "Environment variable"

    ``` sh title="folders separated by :  (use ; on Windows)"
    DICT_DIR="~/Dictionaries:/Volumes/Data/Dicts" wudict
    ```

=== "Config file"

    ``` toml title="~/.wudict/wudict.toml"
    DICT_DIR = ["~/Dictionaries", "/Volumes/Data/Dicts"]
    ```

    The file is generated for you on first run — commented, complete, and
    printed to the terminal so you always know which one is in effect.

## Configuration flags

| Setting                                                 | CLI Flag                  | env var / `wudict.toml`     | Default               |
| ------------------------------------------------------- | ------------------------- | --------------------------- | --------------------- |
| Dictionary folders                                      | `--dict-dir` (repeatable) | `DICT_DIR`                  | `~/Dictionaries`      |
| Library location                                        | `--db-dir`                | `DB_DIR`                    | `~/.wudict/db`        |
| Address                                                 | `--ip` / `--port`         | `SERVER_IP` / `SERVER_PORT` | `127.0.0.1` / `6888`  |
| Don't open a browser                                    | `--no-browser`            | `NO_BROWSER=1`              | open browser          |
| Build headword indexes                                  | —                         | `AUTO_INDEX`                | `on` (`off` disables) |
| Store text uncompressed                                 | `--no-compress`           | `NO_COMPRESS=1`             | compressed            |
| External Speex decoder \ (not required for cgo version) | `--speexdec`              | `SPEEXDEC`                  | found on `PATH`       |
| Use cached libraries                                    | `--use-cached`            | `USE_CACHED=1`              | off                   |

Precedence order: **flag > environment > config file >
default**. If a setting was given on the command line, the config file
cannot override it, and the setup page will display a corresponding warning.

## Where the config files live

A commented `~/.wudict/wudict.toml` is created on first run; `~/.wudict/state.json`
stores per-dictionary order and on/off settings.

=== "Config search order"

    1. `--config` / `CONFIG_PATH`
    2. `<exe-dir>/wudict.toml` — the _portable mode_: keep the binary and
       its config in one folder, carry them together
    3. `~/.wudict/wudict.toml`
    4. `/etc/wudict/wudict.toml`

=== "Portable mode"

    A `wudict.toml` in the same folder as the executable is honored before
    everything user-specific.

## `wudict` command line usage

`wudict` without arguments serves the web interface. `wudict --help` shows the full
reference. The subcommands can be used to do CLI dictionary lookups:

``` sh title="Lookups without opening a browser" hl_lines="1"
wudict lookup ~/Dicts/Oxford.mdx flight     # (1)!
wudict keys   ~/Dicts/Oxford.mdx            # list headwords
wudict keys   ~/Dicts/Oxford.mdd            # list the archive's contents
wudict res    ~/Dicts/Oxford.mdd audio/a.mp3  # pull one file out

wudict ingest ~/Dicts                       # (2)!
wudict clean                                # list removable library items
```

1. The entry, as HTML, to stdout. Pipe it to `less` or write it to a file.
2. Prepares indexes for every dictionary in the folder — runs once, then
   the app is silent. No browser needed. The `--full` flag packs media
   into the library too.

Terminal people rarely open a browser for a single word again.

---

Next: learn the four search modes and the shortcuts that make the app
feel like a part of your hands.

[Usage](use.md){ .md-button .md-button--primary }