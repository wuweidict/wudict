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
| Installed lemma data | `~/.wudict/lemmas` | `%USERPROFILE%\.wudict\lemmas` | `Android/data/com.legbehindneck.wudict/files/lemmas` |
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
    | Default | `1GB`; on Android a third of [`MEMORY_LIMIT`](#memory_limit), which is `64MB` on a small device and `128MB` on a large one |

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

## Lemmatization

When a dictionary comes back with nothing, WuWeiDict looks the word's
dictionary form up and asks that dictionary again: `knew` finds *know*,
`fuiste` finds *ser*, `идет` finds *идти*. It fires only on an empty answer,
and only for a single word, so nothing it produces can push a real hit down the
page.

It is decided one dictionary at a time. A glossary that happens to list
`understood` itself answers straight away; the dictionaries beside it that index
only *understand* are still asked for *understand*, instead of being silenced by their
neighbour's hit.

### MORPH_CACHE

How many languages of lemma data stay in memory.

??? info "Values, default, and what a language costs"

    | | |
    | --- | --- |
    | Flag | none, environment and file only |
    | Values | a count, or `0` to switch lemmatization off entirely |
    | Default | `2`; `1` on Android |

    English is built in. Every other language is a file you install — see
    [`LEMMA_DIR`](#lemma_dir). Nothing is loaded at startup: a language is read
    on the first search that needs it, and the least recently used is dropped
    above this count.

    A loaded language costs from 7 MB (English) to 65 MB (Russian). `0` never
    loads any and never lemmatizes, which is also what you want if you would
    rather a failed search simply stay failed.

    A dictionary is only offered its own language's lemma — Spanish `sale` →
    *salar* is never asked of an English dictionary. WuWeiDict works the
    language out from what the dictionary declares, then its file name, its
    folders and its title; one it cannot place is searched as English.

### LEMMA_DIR

The folder every language but English is loaded from.

??? info "Values, default, and the file format"

    | | |
    | --- | --- |
    | Flag | none, environment and file only |
    | Values | a folder path |
    | Default | `~/.wudict/lemmas` |

    Spanish, Russian, German, French, Italian and anything else is installed
    here rather than shipped inside WuWeiDict — the six languages that used to
    be built in were 9 MB of a 20 MB program, carried by everyone who never
    searched in them.

    You do not have to fill this folder by hand. Open **🔤 Lemmatization** on
    the settings page (or in the ⚙ panel beside the search box) and tick a
    language: it downloads, installs, and takes effect immediately — no
    restart. That page is the only route on Android, which has no shell. From
    a terminal, `wudict lemmas download ru` does the same; see
    [the `lemmas` command](cli.md#lemmas). Everything below is what those
    write, and what to write yourself for a language neither offers.

    One file per language, named after the language: `pl.txt`, `pol.tsv`,
    `polish.txt.gz`. The extension must be `.txt` or `.tsv`, optionally
    `.gz`-compressed; anything else in the folder is ignored. The folder does
    not have to exist.

    Each line is a lemma followed by its forms, separated by tabs and written
    in lower case:

    ```
    kot	kota	kotu	kotem	koty	kotów
    pies	psa	psu	psem	psy	psów
    ```

    The lists at
    [michmech/lemmatization-lists](https://github.com/michmech/lemmatization-lists)
    are already in this shape — one `lemma`/form pair per line — and work as
    downloaded. Blank or malformed lines are skipped rather than failing the
    file.

    An `en.txt` replaces the English WuWeiDict ships, if you have a better
    list than the one built in.

    The folder is indexed at startup and again whenever the lemmatization page
    installs or removes something, so a language obtained there is searchable
    at once; a file you drop in by hand is picked up at the next start. A
    language is loaded under the same [`MORPH_CACHE`](#morph_cache) budget as a
    built-in one — so `MORPH_CACHE = "0"` ignores the folder entirely, and the
    lemmatization page says so rather than letting you download data nothing
    will read.

### LEMMA_URL

The catalogue `wudict lemmas` installs languages from.

??? info "Values, default, and using your own"

    | | |
    | --- | --- |
    | Flag | `-url` on the `lemmas` commands |
    | Used by | `wudict lemmas`, and the 🔤 Lemmatization page |
    | Values | a URL, or the path of a `manifest.json` on disk |
    | Default | the published WuWeiDict lemma catalogue |

    You will not normally set this. It exists for two cases: an installation
    with no route to the internet, and a mirror of your own.

    For the first, copy the published folder — the `.tsv.gz` files and the
    `manifest.json` beside them — onto the machine and point at it:

    ``` sh
    wudict lemmas download -url /media/usb/lemmas/manifest.json ru pl
    ```

    Everything else is unchanged: each file is still checked against the
    SHA-256 digest the manifest publishes, which is what makes the source
    interchangeable. WuWeiDict pins no host and never takes a file name from
    the catalogue — a language installs as `<code>.tsv.gz` and nowhere else.

    The lemma data itself is published under the
    [ODbL](https://opendatacommons.org/licenses/odbl/1-0/) and derived from
    [michmech/lemmatization-lists](https://github.com/michmech/lemmatization-lists);
    an `ATTRIBUTION.txt` sits beside the files.

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

### WEB_ORIGINS

Which **web pages** may look words up in this server with JavaScript.

| | |
| --- | --- |
| Flag | none, environment and file only |
| Default | blank: no web page may |

``` toml title="two pages of your own"
WEB_ORIGINS = ["http://localhost:3000", "https://notes.example.com"]
```

The defaults of the two keys are opposite, on purpose. An extension is
something you chose to install, so `BROWSER_EXTENSIONS` starts open and you
narrow it. A web page is whatever you happened to open, so `WEB_ORIGINS` starts
closed and you widen it, one origin at a time.

An allowed page reaches the same three read-only endpoints an extension does -
`/api/dicts`, `/api/search` and `/res/` - and nothing else. It cannot read your
settings, your preferences or your library, and it cannot prepare, remove or
rescan a dictionary.

#### Writing an origin

An origin is a scheme, a host and a port. Nothing else.

``` toml
WEB_ORIGINS = ["http://localhost:3000"]     # correct
WEB_ORIGINS = ["http://localhost:3000/app"] # wrong: a path is not part of an origin
WEB_ORIGINS = ["localhost:3000"]            # wrong: no scheme
```

`http://` and `https://` are the only schemes accepted. A different port is a
different origin, and so is a different scheme: `http://localhost:3000` does not
allow `https://localhost:3000`. The default port may be written or left out -
`https://x.example` and `https://x.example:443` mean the same thing, because
that is what the browser means by them.

`null` is never allowed. It is what a `file://` page and a sandboxed iframe
send, and every one of them sends the same string, so it names nobody.

#### The wildcard

``` toml
WEB_ORIGINS = "*"
```

Every site you visit may then read every dictionary you have, for as long as
WuWeiDict is running. That is useful while you are developing a page and know
what is open in the browser. It is a standing invitation otherwise.

The server prints the setting on every start, so an allowlist you left behind
is visible rather than forgotten:

``` text
  address       http://127.0.0.1:6888
  web origins   any website may read your dictionaries (WEB_ORIGINS = "*")
```

#### Chrome and private addresses

`127.0.0.1` is a private address. Chrome sends a preflight before any request to
one from a page that is not itself local, and drops the request unless the
answer opts in. WuWeiDict answers that preflight for origins you allowed, so
this needs nothing from you - but it is why an allowed page may still be blocked
by a Chrome policy or an extension that suppresses local network access.

#### What this does not cover

Anything that is not a browser page ignores all of it. `curl`, a Node or Python
script, an Electron main process, a native app: none of them send an `Origin`
header, none of them enforce CORS, and all of them can already reach every
endpoint. CORS is a rule browsers apply to pages, not a lock on the server. The
lock is [`SERVER_IP`](#server_ip-and-server_port), which keeps the server on the
loopback address.

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
WEB_ORIGINS = ["http://localhost:3000"]
```
