---
title: Command line
description: Every wudict command, its flags and its arguments.
---

# Command line

``` text title="the shape of every invocation"
wudict [command] [flags] [arguments]
```

`wudict` with no command, or with flags only, starts the server. That is the
same as `wudict serve`.

``` sh title="the three ways to start the server"
wudict
wudict serve --port 9090 --no-browser
wudict ~/Dicts/Oxford.mdx     # serve that file's folder and open it
```

The third form is what double-clicking a dictionary file does.

`wudict --help` prints the whole reference. `wudict --version` prints the
version.

## Commands

| Command | What it does |
| --- | --- |
| [`serve`](#serve) | start the HTTP server; the default |
| [`list`](#list) | show the dictionaries under one or more folders |
| [`info`](#info) | show one dictionary's metadata and capabilities |
| [`lookup`](#search-commands) | exact lookup |
| [`prefix`](#search-commands) | exact, then prefix lookup |
| [`contains`](#search-commands) | substring headword search |
| [`fts`](#search-commands) | full-text search |
| [`searchall`](#searchall) | search every dictionary in a folder at once |
| [`keys`](#keys) | list headwords, or the contents of an `.mdd` |
| [`res`](#res) | extract one resource file |
| [`ingest`](#ingest) | prepare dictionaries into the library |
| [`clean`](#clean) | list or delete broken library items |
| [`rm`](#rm) | remove a dictionary |

`--verbose` works with every command.

## serve

``` sh title="serve"
wudict serve [flags]
```

Every flag maps to an environment variable and a `wudict.toml` key.

| Flag | Setting |
| --- | --- |
| `--dict-dir <path>` | [`DICT_DIR`](configuration.md#dict_dir), repeatable |
| `--db-dir <path>` | [`DB_DIR`](configuration.md#db_dir) |
| `--ip <address>` | [`SERVER_IP`](configuration.md#server_ip-and-server_port) |
| `--port <port>` | [`SERVER_PORT`](configuration.md#server_ip-and-server_port) |
| `--no-browser` | [`NO_BROWSER`](configuration.md#no_browser) |
| `--use-cached` | [`USE_CACHED`](configuration.md#use_cached) |
| `--no-compress` | [`NO_COMPRESS`](configuration.md#no_compress) |
| `--index-workers <n>` | [`INDEX_WORKERS`](configuration.md#index_workers) |
| `--speexdec <path>` | [`SPEEXDEC`](configuration.md#speexdec) |
| `--tray`, `--no-tray` | [`TRAY`](configuration.md#tray) |
| `--config <path>` | [`CONFIG_PATH`](configuration.md#config_path) |
| `--verbose` | [`VERBOSE`](configuration.md#verbose) |

[All settings, with defaults](configuration.md){ .md-button }

## list

``` sh title="what WuWeiDict can read in these folders"
wudict list ~/Dictionaries /Volumes/Ext/Dicts
```

One line per dictionary found. Use it to check a folder before starting the
server.

## info

``` sh title="metadata and capabilities of one dictionary"
wudict info ~/Dicts/Oxford.mdx
```

Shows the name, format, entry count and which search modes that dictionary
currently supports.

## Search commands

The four commands match the four modes in the app.

``` sh title="the four modes"
wudict lookup   [-n max] <dictfile> <word>     # exact, with an accent-folded fallback
wudict prefix   [-n max] <dictfile> <word>     # exact, else prefix; accent-insensitive
wudict contains [-n max] <dictfile> <word>     # substring headword search
wudict fts      [-n max] <dictfile> <query>    # full text of the articles
```

Each prints article HTML to standard output. `-n` limits the number of results.

`contains` and `fts` need a prepared dictionary. `lookup` and `prefix` work on
any supported file.

[What each mode matches](../start/search.md){ .md-button }

## searchall

``` sh title="every dictionary in a folder, concurrently"
wudict searchall [-mode m] [-n perDict] <dir> <term>
```

`-mode` takes `exact`, `prefix`, `contains` or `fts`. `-n` limits results per
dictionary. `<dir>` may be a list in the same form as `DICT_DIR`.

## keys

``` sh title="list headwords, or an archive's contents"
wudict keys [-offset N] [-n count] <dictfile>
```

On a dictionary, `keys` lists headwords, all of them by default.

On an `.mdd` file, it lists the files that archive holds. The format is the
same, so the command is the same, and no `.mdx` is needed.

## res

``` sh title="pull one file out of a dictionary"
wudict res [-o out] [-f] <dictfile> <name>
wudict res ~/Dicts/Oxford.mdd audio/word.mp3
```

`<name>` is any name that `keys` printed. An `.mdd` file is accepted directly.

Where the bytes go depends on where you send them.

- Piped or redirected: to standard output.
- On a terminal: to a file named after the resource. `-f` overwrites it.
- `-o` names the output: a path, whose parent folders are created; a folder; or
  `-` for standard output.

## ingest

``` sh title="prepare dictionaries without a browser"
wudict ingest [-full] [-headwords] [-contains] <dictfile|folder> …
```

Creates `<db-dir>/<dictionary name>/text.db` and `info.txt`. Given a folder, it
prepares everything inside and skips what is already done.

| Flag | Effect |
| --- | --- |
| none | headword and full-text indexes |
| `-headwords` | headword index only, much smaller |
| `-contains` | add the substring index, roughly doubling a headwords-only database |
| `-full` | also pack media into `media.db` |

## clean

``` sh title="find and remove library leftovers"
wudict clean        # list what could be removed
wudict clean -f     # remove it
```

Lists incomplete or unreadable folders, interrupted preparations, and leftovers
from an older layout. It is a dry run until you add `-f`.

A prepared dictionary is never listed, even when its original file is gone or
has changed.

## rm

``` sh title="remove one dictionary"
wudict rm [-f] [-keep-source | -keep-index] <name|path>
```

By default this deletes **both** the prepared folder in the library and the
original dictionary files.

The argument may be a library name, a folder, a `text.db`, or the path of an
original dictionary file.

| Flag | Effect |
| --- | --- |
| `-keep-source` | delete only the prepared folder |
| `-keep-index` | delete only the original files |
| `-f` | actually delete; without it, `rm` only lists |

With `-keep-source`, the dictionary is prepared again on the next search if its
original file is still in a scanned folder. `-keep-index` refuses to run while
media is unpacked.

## Examples

``` sh title="everyday commands"
wudict --dict-dir ~/Books/Dicts --port 9090 --no-browser
wudict lookup ~/Dicts/Oxford.mdx serendipity
wudict ingest -full ~/Dicts/Oxford.mdx
SERVER_PORT=9000 wudict
```
