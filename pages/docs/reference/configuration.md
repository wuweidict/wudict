---
title: Configuration
description: Every WuWeiDict setting - the flag, the environment variable, the TOML key, and the default.
---

# Configuration

Every setting has up to three spellings: a command-line flag, an environment
variable, and a key in `wudict.toml`. The name is the same in the environment
and in the file.

## Priority

**Flag beats environment. Environment beats the config file. The file beats the
built-in default.**

This is the answer to "I edited `wudict.toml` and nothing changed": something
above it sets the same value. WuWeiDict knows where each value came from, and the
setup page refuses to pretend that saving to the file will take effect.

## Where the config file lives

WuWeiDict reads the first file it finds.

1. the `--config` flag, or the `CONFIG_PATH` environment variable
2. `wudict.toml` next to the executable - see [portable mode](#portable-mode)
3. `~/.wudict/wudict.toml`
4. `/etc/wudict/wudict.toml`

The first `serve` run creates `~/.wudict/wudict.toml`, fully commented. Every
start prints the file in effect.

`~/.wudict/state.json` holds your dictionary order and your on/off switches.
That file is written by the app, not by you.

### Every file WuWeiDict owns

| What | macOS and Linux | Windows | Android |
| --- | --- | --- | --- |
| Config | `~/.wudict/wudict.toml` | `%USERPROFILE%\.wudict\wudict.toml` | `Android/data/com.legbehindneck.wudict/files/wudict.toml` |
| Prepared library | `~/.wudict/db` | `%USERPROFILE%\.wudict\db` | `Android/data/com.legbehindneck.wudict/files/db` |
| State (order, switches) | `~/.wudict/state.json` | `%USERPROFILE%\.wudict\state.json` | as above |
| Log, when there is no console | `~/Library/Logs/wudict.log` on macOS, `~/.wudict/wudict.log` on Linux | `%LOCALAPPDATA%\wudict\wudict.log` | Android's own log |

Your dictionary files are not in this list. WuWeiDict reads them where they
are and never writes to them. [`DB_DIR`](#db_dir) moves the library; the rest
of the table follows the config file.

### Portable mode

Put a `wudict.toml` next to the executable. It then wins over every user
location, and settings are saved back to it. Use this for a USB stick or a
self-contained folder.

WuWeiDict never creates that file by itself. An executable's folder usually
belongs to someone else, such as `~/go/bin` or `/opt/homebrew/bin`.

## Everyday settings

### DICT_DIR

Folders holding your dictionary files, scanned including every subfolder.

| | |
| --- | --- |
| Flag | `--dict-dir <path>`, repeat for several folders |
| Default | `~/Dictionaries` |

``` sh title="three ways to name two folders"
wudict --dict-dir ~/Dictionaries --dict-dir /Volumes/Ext/Dicts
DICT_DIR="~/Dictionaries:/Volumes/Ext/Dicts" wudict     # ";" on Windows
```

``` toml title="~/.wudict/wudict.toml"
DICT_DIR = ["~/Dictionaries", "/Volumes/Ext/Dicts"]
```

The config file spells several folders as an array. The `:` and `;` separators
belong to the environment only, where they follow the convention of `PATH`.

A dictionary found in two folders is listed once, and the first folder wins. A
missing folder is reported, and the others still work.

### DB_DIR

The library folder: one subfolder per prepared dictionary.

| | |
| --- | --- |
| Flag | `--db-dir <path>` |
| Default | `~/.wudict/db` |

It must not be the same folder as `DICT_DIR`. WuWeiDict refuses to start when they
are the same.

### SERVER_IP and SERVER_PORT

| | |
| --- | --- |
| Flags | `--ip <address>`, `--port <port>` |
| Defaults | `127.0.0.1`, `6888` |

`127.0.0.1` is the loopback address, reachable from your machine only. Set
`0.0.0.0` to accept connections from your network. Only do that on a network
you trust; WuWeiDict has no login.

### NO_BROWSER

Do not open a browser tab at startup.

| | |
| --- | --- |
| Flag | `--no-browser` |
| Default | off, a tab opens |

### AUTO_INDEX

Prepare each dictionary in the background on its first search.

| | |
| --- | --- |
| Flag | none, environment and file only |
| Values | `on`, `off` |
| Default | `on` |

`off` leaves every dictionary in preview, searched through its own format.
Contains, full-text and media stay per-dictionary choices either way.

[What preparation does](../dictionaries/library.md){ .md-button }

### USE_CACHED

Also list prepared dictionaries whose original files are gone.

| | |
| --- | --- |
| Flag | `--use-cached` |
| Default | off |

The setup page sets this when you click **Use these dictionaries**.

### VERBOSE

Log requests, dictionary opens, preparation and audio conversion.

| | |
| --- | --- |
| Flag | `--verbose` |
| Default | off |

`--verbose` works for every command, not only `serve`. The short `-v` means
`--version`.

## Audio

### SPEEX_BACKEND

Which decoder converts `.spx` audio to WAV.

| | |
| --- | --- |
| Flag | none, environment and file only |
| Values | `internal` (built-in libspeex), `external` (the `speexdec` program) |
| Default | `internal` |

`internal` needs a `-cgo` build. A `-purego` build always uses the external
program.

### SPEEXDEC

Path to the external `speexdec` program.

| | |
| --- | --- |
| Flag | `--speexdec <path>` |
| Default | found next to the executable, then on `PATH` |

WuWeiDict prints which decoder it resolved at startup, or an install hint when it
found none.

## Desktop integration

### TRAY

Show a tray icon on Windows, or a menu-bar icon on macOS.

| | |
| --- | --- |
| Flags | `--tray`, `--no-tray` |
| Values | `1` always, `0` never, unset for automatic |
| Default | unset: an icon appears only when started from the desktop |

Given both flags, `--no-tray` wins. Between two contradictory instructions, the
one that changes nothing about the process is the safe reading.

## Tuning

These change speed and memory, never results - with one marked exception. The
defaults are right for a desktop. Open a section only if you want to change it.

### INDEX_WORKERS

How many dictionaries may be prepared at once.

??? info "Values, default, and when to raise it"

    | | |
    | --- | --- |
    | Flag | `--index-workers <n>` |
    | Values | a number, or `auto` (also `0`) for every core |
    | Default | `1` |

    Preparing one dictionary saturates one core and holds a few hundred bytes
    per headword. The default is one, so background work never takes the
    machine away from you.

    Raise it when you want a large collection prepared quickly and do not mind
    the machine being slow while it happens.

### PREVIEW_MEMORY

How much RAM dictionaries that are not yet prepared may hold open.

??? info "Values, default, and what it does not apply to"

    | | |
    | --- | --- |
    | Flag | none, environment and file only |
    | Values | a size such as `1GB`, or `0` for no limit |
    | Default | `1GB`, and `64MB` on Android |

    Each dictionary in preview holds about 350 bytes per headword open. Above
    this limit, the least recently used are closed.

    Prepared dictionaries answer from disk. They cost nothing here and are
    never closed for this reason.

### SEARCH_MEMORY

How much RAM one search may claim by opening dictionaries that are not yet
prepared.

??? info "Values, default, and the results it can change"

    | | |
    | --- | --- |
    | Flag | none, environment and file only |
    | Values | a size such as `512MB`, or `0` for no cap |
    | Default | no cap; on Android, the value of `MEMORY_LIMIT` |

    This is the one setting here that changes what a search returns. Past the
    cap, the remaining dictionaries are reported as not searched instead of
    being opened. They are not errors, and asking for one of them by itself
    still answers.

    It never applies to prepared dictionaries, which cost nothing to search,
    and never to a search naming a single dictionary.

### MEMORY_LIMIT

A soft ceiling for the whole process.

??? info "Values and default"

    | | |
    | --- | --- |
    | Flag | none, environment and file only |
    | Values | a size such as `4GB`, or `0` for none |
    | Default | none; on Android, a fraction of the device's RAM |

    Go collects more often, and drops its caches, rather than growing past
    this. It is a ceiling, not a hard limit.

### NO_COMPRESS

Store article text plain instead of compressed.

??? info "Values and what it costs"

    | | |
    | --- | --- |
    | Flag | `--no-compress` |
    | Default | off, text is compressed |

    Prepared databases become roughly three times larger. Reads get marginally
    faster. Worth it only with disk to spare.

## Access

### BROWSER_EXTENSIONS

Which browser extensions may look words up in this server.

| | |
| --- | --- |
| Flag | none, environment and file only |
| Default | blank: any installed extension |

``` toml title="allow two named extensions only"
BROWSER_EXTENSIONS = ["chrome-extension://abcdefghijklmnopabcdefghijklmnop"]
```

An extension reaches three read-only endpoints: `/api/dicts`, `/api/search` and
`/res/`. It never reaches your settings, your preferences or your library. Web
pages reach none of them.

Firefox generates a new `moz-extension://` origin per installation, so there is
no stable origin to list there.

## CONFIG_PATH

The config file to read, instead of searching the four locations.

| | |
| --- | --- |
| Flag | `--config <path>` |
| Default | unset |

This one exists as a flag and an environment variable only. A config file
cannot name itself.

## A complete example

``` toml title="~/.wudict/wudict.toml"
DICT_DIR    = ["/data/dicts", "~/Dictionaries"]
DB_DIR      = "~/.wudict/db"
SERVER_IP   = "127.0.0.1"
SERVER_PORT = "9000"
NO_BROWSER  = "1"
AUTO_INDEX  = "on"
```
