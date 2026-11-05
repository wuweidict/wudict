---
title: Configuration
description: Every wudict setting - flag, environment variable, wudict.toml key, values and default.
---

# Configuration

Each setting can be given as a command-line flag, an environment variable or a
key in `wudict.toml`. The variable and the key have the same name.

## Priority

Highest first: command-line flag, environment variable, `wudict.toml`, built-in
default. The setup page shows which values a flag or variable overrides.

## Config file

wudict reads the first of:

1. `--config` or `CONFIG_PATH`
2. `wudict.toml` next to the executable ([portable mode](#portable-mode))
3. `wudict.toml` in the config folder ([Files](#files))
4. Linux and BSD: `wudict/wudict.toml` in each folder of `$XDG_CONFIG_DIRS`
   (default `/etc/xdg`)
5. `/etc/wudict/wudict.toml`

The first `wudict serve` creates `wudict.toml` in the config folder with every
setting commented out. Each start prints the file in effect. An unknown key is reported
at startup and on the setup page.

### Portable mode

A `wudict.toml` next to the executable takes precedence over the one in the
config folder and receives the settings saved from the setup page. wudict never
creates it. `state.json`, `groups.ini`, `style/`, the word lists and the howto
are then kept beside it too.

## Files

On macOS, Windows and Android wudict keeps its files in one folder:

| Platform | Folder |
| --- | --- |
| macOS | `~/.wudict` |
| Windows | `%USERPROFILE%\.wudict` |
| Android | `Android/data/com.legbehindneck.wudict/files` |

On Linux and BSD the files follow the
[XDG Base Directory](https://specifications.freedesktop.org/basedir/latest/)
folders. `$XDG_CONFIG_HOME`, `$XDG_DATA_HOME` and `$XDG_STATE_HOME` replace
the defaults:

| Folder | Default | Contents |
| --- | --- | --- |
| config | `~/.config/wudict` | `wudict.toml` (settings), `groups.ini` (picker groups, once edited), `style/` (custom styles) |
| data | `~/.local/share/wudict` | `db/` (the library, [`DB_DIR`](#db_dir)), `lemmas/` (installed lemma data, [`LEMMA_DIR`](#lemma_dir)), `favourites.txt`, `builtin/` (the howto) |
| state | `~/.local/state/wudict` | `state.json` (dictionary order, enabled dictionaries, display preferences), `history.txt`, `token` (the access key), `wudict.log` |

A `~/.wudict` from an earlier version moves to these folders on the next
`wudict serve`, which lists every moved file. If `DB_DIR` or `LEMMA_DIR` points
inside `~/.wudict`, that folder stays where it is.

Log when there is no console: macOS `~/Library/Logs/wudict.log`, Linux
`~/.local/state/wudict/wudict.log`, Windows `%LOCALAPPDATA%\wudict\wudict.log`,
Android logcat.

wudict does not modify dictionary files. Only <kbd>🗑 Remove…</kbd> and
`wudict rm` delete them.

## Everyday settings

### DICT_DIR

Folders with dictionary files, subfolders included.

| | |
| --- | --- |
| Flag | `--dict-dir <path>`, repeated for several folders |
| Default | `~/Dictionaries` |

``` sh title="two folders, from the command line and the environment"
wudict --dict-dir ~/Dictionaries --dict-dir /Volumes/Ext/Dicts
DICT_DIR="~/Dictionaries:/Volumes/Ext/Dicts" wudict     # ";" on Windows
```

``` toml title="wudict.toml"
DICT_DIR = ["~/Dictionaries", "/Volumes/Ext/Dicts"]
```

In `wudict.toml` several folders are an array; in the environment they are
separated by `:` (`;` on Windows). A dictionary found in two folders is listed
once, from the first. A missing folder is reported; the others are still read.

### DB_DIR

The library: one library folder per indexed dictionary.

| | |
| --- | --- |
| Flag | `--db-dir <path>` |
| Default | `db/` in the data folder ([Files](#files)) |

Must not be a `DICT_DIR` folder; wudict does not start if it is.

### SERVER_IP and SERVER_PORT

| | |
| --- | --- |
| Flags | `--ip <address>`, `--port <port>` |
| Defaults | `127.0.0.1`, `6888` |

`127.0.0.1` is the loopback address, reachable from your machine only. Set
`0.0.0.0` to accept connections from your network. Requests from the network
then need the access key ([`AUTH`](#auth)), and deleting a dictionary from
another host also needs [`ALLOW_REMOTE_DELETE`](#allow_remote_delete).

### NO_BROWSER

Do not open a browser tab at startup.

| | |
| --- | --- |
| Flag | `--no-browser` |
| Default | off: a tab opens |

### AUTO_INDEX

Index each dictionary's headwords in the background on its first search.

| | |
| --- | --- |
| Flag | none, environment and file only |
| Values | `on`, `off` |
| Default | `on` |

`off`: dictionaries stay not indexed and are searched through their own format.
The full-text index and media pack are per-dictionary choices either way.

[The library](../dictionaries/library.md){ .md-button }

### USE_CACHED

Also list indexed dictionaries whose dictionary files are gone.

| | |
| --- | --- |
| Flag | `--use-cached` |
| Default | off |

The setup page sets it with <kbd>Use these dictionaries</kbd>.

### ALLOW_REMOTE_DELETE

Allows clients on other hosts to delete dictionaries (<kbd>🗑 Remove…</kbd>,
`DELETE /api/library`).

| | |
| --- | --- |
| Flag | `--allow-remote-delete <0\|1>` |
| Default | `0` |

Requests from the loopback address can always delete.

!!! warning "Deletion is permanent"

    Deleted files are unlinked, not moved to the Trash or the Recycle Bin.
    `wudict rm` without `-f` lists what it would delete.

### VERBOSE

Log requests, dictionary opens, indexing and audio conversion.

| | |
| --- | --- |
| Flag | `--verbose` |
| Default | off |

Applies to every command. `-v` is `--version`.

## Audio

### SPEEX_BACKEND

Which decoder converts `.spx` audio to WAV.

| | |
| --- | --- |
| Flag | none, environment and file only |
| Values | `internal` (built-in libspeex), `external` (the `speexdec` program) |
| Default | `internal` |

`internal` needs a `-cgo` build; a `-purego` build always uses `external`.

### SPEEXDEC

Path to the `speexdec` program.

| | |
| --- | --- |
| Flag | `--speexdec <path>` |
| Default | next to the executable, then on `PATH` |

The decoder in use is printed at startup.

## Desktop integration

### TRAY

Tray icon (Windows) or menu-bar icon (macOS).

| | |
| --- | --- |
| Flags | `--tray`, `--no-tray` |
| Values | `1` always, `0` never, unset for automatic |
| Default | unset: shown only when started from the desktop |

Given both flags, `--no-tray` applies.

## Tuning

These settings change speed and memory use. Only `SEARCH_MEMORY` changes search
results.

### INDEX_WORKERS

How many dictionaries are indexed at the same time.

| | |
| --- | --- |
| Flag | `--index-workers <n>` |
| Values | a number; `auto` or `0`: one per CPU core |
| Default | `1` |

Indexing one dictionary fully uses one CPU core and a few hundred bytes of
memory per headword.

### PREVIEW_MEMORY

Memory that dictionaries not yet indexed may hold open.

| | |
| --- | --- |
| Flag | none, environment and file only |
| Values | a size such as `1GB`; `0`: no limit |
| Default | `1GB`; Android: a third of [`MEMORY_LIMIT`](#memory_limit) |

An open dictionary that is not indexed holds about 350 bytes per headword.
Above the limit, the least recently used are closed. Indexed dictionaries are
read from disk and do not count.

### SEARCH_MEMORY

Memory one search may use to open dictionaries that are not indexed.

| | |
| --- | --- |
| Flag | none, environment and file only |
| Values | a size such as `512MB`; `0`: no limit |
| Default | `0`; Android: the value of `MEMORY_LIMIT` |

Past the limit, the remaining dictionaries are reported as *not searched*.
Indexed dictionaries and a search of one dictionary are not limited.

### MEMORY_LIMIT

A soft memory limit for the whole process.

| | |
| --- | --- |
| Flag | none, environment and file only |
| Values | a size such as `4GB`; `0`: none |
| Default | none; Android: 1/16 of the device's RAM, between 192 MiB and 384 MiB |

Near the limit, the Go runtime collects garbage more often and wudict drops its
caches; the process can still exceed it.

### COMPRESSION

How article text is stored when a dictionary is indexed.

| | |
| --- | --- |
| Flag | `--compression` (`serve` only; `ingest` and `reindex` read the environment and the file) |
| Values | `zstd`, `deflate` or `none`, optionally with a level: `zstd-1` to `zstd-22`, `deflate-1` to `deflate-9` |
| Default | `zstd` (level 1) |

- `zstd`: the smallest dictionaries. Each dictionary stores one zstd dictionary
  made from its own articles, and every article is compressed with it.
- `deflate`: dictionaries 1.3 to 3 times as large as with `zstd`. Use it for a
  program that reads only DEFLATE, or for a wudict version before zstd, which
  does not open a `zstd` dictionary.
- `none`: dictionaries 2 to 7 times as large as with `zstd`.

A bare `deflate` is level 6, and level 1 on Android. zstd has four levels:
`zstd-1` and `zstd-2` are level 1, `zstd-3` to `zstd-5` are level 3, `zstd-6`
to `zstd-9` are level 6, and `zstd-10` and above are level 10. Levels above 1
make dictionaries a few per cent smaller and take up to twice as long to index.

The setting applies to dictionaries indexed after the change. A dictionary
already indexed with another codec keeps working and is shown as
[outdated](../dictionaries/library.md#outdated-indexes): <kbd>Rebuild</kbd>, or
`wudict reindex`, converts it. A different level of the same codec is not
outdated. Reading handles
every codec and level. A value that is not one of these is reported at start-up
and on the setup page, and the default applies.

`NO_COMPRESS = "1"` and `--no-compress` are earlier spellings of
`COMPRESSION = "none"`, and `COMPRESSION = "fast"` and `"small"` are earlier
spellings of `"zstd"`. wudict still reads them and reports the new spelling.

```toml
COMPRESSION = "deflate"
```

## Lemmatization

With lemma data for a language, a search for an inflected form finds the lemma:
`knew` → *know*, `fuiste` → *ser*, `идет` → *идти*. See
[Search → Inflected words](../start/search.md#inflected-words).

**Lemmatization needs the dictionary's language.** wudict takes the first it
finds of:

1. the language the dictionary declares: Lingvo DSL (`#INDEX_LANGUAGE`),
   Babylon, ZIM, wudict markdown;
2. a language code or English language name at the start of the file name:
   `es-en-collins.mdx`, `spa-eng-oxford.mdx`, `spanish-collins.mdx`;
3. a folder inside a dictionary folder whose whole name is a language code or
   English language name: `es/`, `Spanish/`;
4. a language named in the dictionary's title: *Dahl's Russian Dictionary*,
   *(Ru-Ru)*;
5. otherwise English.

English dictionaries therefore need no renaming.

### MORPH_CACHE

How many languages of lemma data stay in memory.

| | |
| --- | --- |
| Flag | none, environment and file only |
| Values | a number; `0` turns lemmatization off |
| Default | `2`; Android: `1` |

A language is loaded on the first search that needs it; above the limit the
least recently used is unloaded. A loaded language uses 1 MB (Persian) to 90 MB
(Slovak); English 7 MB. `wudict lemmas list` shows each.

### LEMMA_DIR

The folder of installed lemma data. English is built in.

| | |
| --- | --- |
| Flag | none, environment and file only |
| Values | a folder |
| Default | `lemmas/` in the data folder ([Files](#files)) |

Install languages with <kbd>Lemmatization…</kbd> (dictionary panel → folder
summary) or [`wudict lemmas download`](cli.md#lemmas). The folder is read at
startup and after each install or removal on the Lemmatization page; a file
added by hand or with `wudict lemmas` is read after a restart. The folder also
holds `.catalogue.json`, the list of languages the Lemmatization page read
last, which it shows before the network answers.

??? info "Lemma data file format"

    One file per language, named by its code or English name: `pl.txt`,
    `pol.tsv`, `polish.txt.gz`. Extension `.txt` or `.tsv`, optionally
    gzip-compressed; other files are ignored. Each line is a lemma followed by
    its forms, tab-separated, in lower case:

    ```
    kot	kota	kotu	kotem	koty	kotów
    pies	psa	psu	psem	psy	psów
    ```

    The lists of
    [michmech/lemmatization-lists](https://github.com/michmech/lemmatization-lists)
    (one lemma–form pair per line) work as downloaded. Malformed lines are
    skipped. An `en` file replaces the built-in English.

### LEMMA_URL

The catalogue lemma data is installed from.

| | |
| --- | --- |
| Flag | `-url` on the `wudict lemmas` commands |
| Values | a URL, or the path of a `manifest.json` |
| Default | `https://raw.githubusercontent.com/wuweidict/lemmas/main/manifest.json` |

Used by `wudict lemmas` and the Lemmatization page. A local path installs
without a network, e.g. from a copy of the catalogue repository:

``` sh
wudict lemmas download -url /media/usb/lemmas/manifest.json ru sk
```

Each file is checked against the SHA-256 digest in the catalogue.

The lemma data is published under the
[ODbL](https://opendatacommons.org/licenses/odbl/1-0/) and derived from
[michmech/lemmatization-lists](https://github.com/michmech/lemmatization-lists);
see `ATTRIBUTION.txt` in the catalogue repository.

## Import

Settings for installing dictionaries from a file or a link on the setup page.

### IMPORT_KEEP

What happens to an archive after its dictionaries are installed.

| | |
| --- | --- |
| Flag | none, environment and file only |
| Values | `ask`, `keep`, `delete` |
| Default | `ask` |

A failed import always keeps the archive.

### IMPORT_URL_HOSTS

The hosts an archive may be downloaded from. A host matches itself and its
subdomains.

| | |
| --- | --- |
| Flag | none, environment and file only |
| Default | blank: any host |

``` toml
IMPORT_URL_HOSTS = ["example.org", "cloud.example.net"]
```

In the environment the hosts are comma-separated.

Set it when the server listens on the network: otherwise a client there can
make this machine download from any address it can reach.

### IMPORT_INSECURE

Also allow plain `http://` links and hosts on the local network.

| | |
| --- | --- |
| Flag | none, environment and file only |
| Values | `0`, `1` |
| Default | `0`: `https://` only, public hosts only |

## Access

### AUTH

Whether a request must carry the access key.

| | |
| --- | --- |
| Flag | none, environment and file only |
| Values | `auto`, `on`, `off` |
| Default | `auto`: required when [`SERVER_IP`](#server_ip-and-server_port) is not `127.0.0.1` |

The key is a random string stored in `token` ([Files](#files)), readable only by your
user. `wudict token` prints a link that carries it
(`http://<address>:<port>/?k=<key>`); opening the link once stores the key in
that browser as a cookie. Scripts send `Authorization: Bearer <key>`.
`wudict token -rotate` replaces the key, and every browser and script holding
the old one must be given the new link.

The three endpoints open to browser extensions (`/api/dicts`, `/api/search`,
`/res/`) do not need the key.

`off` on a network address gives anyone who can reach the port your library,
your settings and your folder names.

### AUTH_TOKEN

The access key itself, when it comes from elsewhere: a password manager, a
container secret. Overrides the `token` file and is never written to it.

| | |
| --- | --- |
| Flag | none, environment and file only |
| Default | the contents of the `token` file |

### TRUSTED_HOSTS

Host names the server answers to, in addition to `localhost` and a host name set
as `SERVER_IP`. Requests to an IP address are always accepted.

| | |
| --- | --- |
| Flag | none, environment and file only |
| Default | blank: IP addresses and `localhost` only |

``` toml
TRUSTED_HOSTS = ["wudict.lan"]
```

A request naming any other host is refused with `421 Misdirected Request`. This
blocks DNS rebinding: a web page that points its own domain at your machine
cannot read the server through it. Set it for a reverse proxy or a host name
you gave the machine; `"*"` turns the check off.

### BROWSER_EXTENSIONS

Browser extensions allowed to call the server.

| | |
| --- | --- |
| Flag | none, environment and file only |
| Default | blank: any installed extension |

``` toml title="allow one extension only"
BROWSER_EXTENSIONS = ["chrome-extension://abcdefghijklmnopabcdefghijklmnop"]
```

An extension can call three read-only endpoints: `/api/dicts`, `/api/search`
and `/res/`. Firefox assigns a new `moz-extension://` origin to each
installation, so a list cannot name a Firefox extension in advance.

### WEB_ORIGINS

Web page origins allowed to call the server from JavaScript.

| | |
| --- | --- |
| Flag | none, environment and file only |
| Default | blank: none |

``` toml title="allow two origins"
WEB_ORIGINS = ["http://localhost:3000", "https://notes.example.com"]
```

A listed origin can call the same three endpoints as an extension.

An origin is a scheme (`http` or `https`), a host and a port, without a path:
`http://localhost:3000`, not `http://localhost:3000/app` or `localhost:3000`. A
different scheme or port is a different origin. A default port may be omitted:
`https://x.example` equals `https://x.example:443`.

`"*"` allows every origin: any page you visit can read your dictionaries while
the server runs. Use it only while developing; the startup output shows it:

``` text
  web origins   any website may read your dictionaries (WEB_ORIGINS = "*")
```

Programs outside a browser (curl, scripts, applications) are not subject to
this setting. What limits them is [`SERVER_IP`](#server_ip-and-server_port) and
the access key ([`AUTH`](#auth)).

## Safety limits

These settings bound what one damaged or hostile file can make wudict use:
memory for a block it unpacks, disk for an import. A real dictionary stays far
below each default.

Change a limit only when a dictionary you trust does not open and the message
names the limit, for example:

``` text
cluster 0: the unpacked cluster exceeds the MAX_ZIM_CLUSTER limit of 256MB
```

A higher limit lets a damaged or hostile file use that much memory or disk. A
limit below what your dictionaries need stops them from opening. A value that is
outside a key's range is moved to the nearest end of it, and a value that is not
a positive size leaves the default in effect; the startup output names the key
in either case. The ranges keep a typo from disabling a guard or stopping
ordinary dictionaries from opening.

Each key is also a flag of `wudict serve` and `wudict ingest`: `MAX_ZIM_CLUSTER`
is `--max-zim-cluster`.

### MAX_ZIM_CLUSTER

One ZIM cluster, unpacked.

| | |
| --- | --- |
| Flag | `--max-zim-cluster` (`serve`, `ingest`) |
| Values | a size from `16MB` to `4GB` |
| Default | `256MB` |

### MAX_SLOB_BIN

One Aard 2 (`.slob`) bin, unpacked.

| | |
| --- | --- |
| Flag | `--max-slob-bin` (`serve`, `ingest`) |
| Values | a size from `16MB` to `4GB` |
| Default | `256MB` |

### MAX_MDICT_BLOCK

One MDict (`.mdx`, `.mdd`) block, unpacked.

| | |
| --- | --- |
| Flag | `--max-mdict-block` (`serve`, `ingest`) |
| Values | a size from `16MB` to `4GB` |
| Default | `256MB` |

### MAX_STARDICT_INDEX

A compressed StarDict index (`.idx.gz`), unpacked; 16 times the size of the file is always allowed.

| | |
| --- | --- |
| Flag | `--max-stardict-index` (`serve`, `ingest`) |
| Values | a size from `16MB` to `4GB` |
| Default | `256MB` |

### MAX_BGL_BLOCK

One Babylon (`.bgl`) block.

| | |
| --- | --- |
| Flag | `--max-bgl-block` (`serve`, `ingest`) |
| Values | a size from `8MB` to `4GB` |
| Default | `64MB` |

### MAX_LEMMA_SIZE

One lemma data file, unpacked.

| | |
| --- | --- |
| Flag | `--max-lemma-size` (`serve`, `ingest`) |
| Values | a size from `16MB` to `1GB` |
| Default | `64MB` |

### MAX_IMPORT_SIZE

One dictionary taken out of an archive, or downloaded.

| | |
| --- | --- |
| Flag | `--max-import-size` (`serve`, `ingest`) |
| Values | a size from `1GB` to `64TB` |
| Default | `256GB` |

### MAX_IMPORT_RATIO

How many times its packed size one file in an archive may unpack to.

| | |
| --- | --- |
| Flag | `--max-import-ratio` (`serve`, `ingest`) |
| Values | a number from `100` to `100000` |
| Default | `2000` |

### MAX_UPLOAD_SIZE

One archive sent to the server from a browser or the app.

| | |
| --- | --- |
| Flag | `--max-upload-size` (`serve`, `ingest`) |
| Values | a size from `1GB` to `64TB` |
| Default | `16GB` |

## CONFIG_PATH

The config file to read, instead of the search order under
[Config file](#config-file).

| | |
| --- | --- |
| Flag | `--config <path>` |
| Default | unset |

A flag and an environment variable only; not a key in `wudict.toml`.

## Example

``` toml title="wudict.toml"
DICT_DIR      = ["~/Dictionaries", "/data/dicts"]
SERVER_IP     = "0.0.0.0"          # listen on the network: AUTH applies
SERVER_PORT   = "9000"
NO_BROWSER    = "1"
TRUSTED_HOSTS = ["wudict.lan"]
WEB_ORIGINS   = ["http://localhost:3000"]
```
