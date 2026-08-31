---
title: Command line
description: Every wudict command, flags and args.
---

# Command line

``` text title="the shape of every invocation"
wudict [command] [flags] [args]
```

`wudict` with no command, or with flags only, starts the server. 
Same as `wudict serve`.

``` sh title="the three ways to start the server"
wudict
wudict serve --port 9090 --no-browser
wudict ~/Dicts/Oxford.mdx     # serve that file's folder and open it
```

The third form is what double-clicking a dictionary file does from 
a file explorer when file extensions are associated with wuDict.
This lets you start wudict with a custom dictionary that is outside 
your currently configured `DICT_DIR` without having to change your configs.

`wudict --help` prints the whole reference. `wudict --version` prints the
version.

## Commands

| Command | What it does                                                       |
| --- |--------------------------------------------------------------------|
| [`serve`](#serve) | start the HTTP server; the default                                 |
| [`lookup`](#search-commands) | exact lookup                                                       |
| [`prefix`](#search-commands) | exact, then prefix lookup                                          |
| [`contains`](#search-commands) | substring headword search, needs the contain index                 |
| [`searchall`](#searchall) | search every dictionary in a folder at once (default -format=text) |
| [`list`](#list) | show the dictionaries under one or more folders                    |
| [`info`](#info) | show one dictionary's metadata and capabilities                    |
| [`fts`](#search-commands) | full-text search, needs the FTS index                              |
| [`keys`](#keys) | list headwords, or the contents of an `.mdd`                       |
| [`res`](#res) | extract one resource file                                          |
| [`ingest`](#ingest) | prepare dictionaries into the library                              |
| [`clean`](#clean) | list or delete broken library items                                |
| [`rm`](#rm) | remove a dictionary                                                |

`--verbose` works with every command.

## serve

``` sh title="serve"
wudict serve [flags]
```

Every flag has a corresponding environment variable and a `wudict.toml` key.

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

Three settings have no flag, because they are not things you change per run:
[`BROWSER_EXTENSIONS`](configuration.md#browser_extensions) and
[`WEB_ORIGINS`](configuration.md#web_origins), which decide who may call the
[HTTP API](api.md) from a browser, and
[`AUTO_INDEX`](configuration.md#auto_index).

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

By default, each prints article HTML to standard output. 
You can pass `-format=text` to remove all tags. `-n` limits the number of results.

`contains` and `fts` need a prepared dictionary. `lookup` and `prefix` work on
any supported file.

### Article format

All four commands — lookup, prefix, contains, fts — accept the `-format` flag,
the same three forms the [HTTP API](api.md#article-formats) offers.

``` sh title="the dictionary's markup, reduced, or gone"
wudict lookup -format raw   <dictfile> <word>   # raw HTML (the default)
wudict lookup -format clean <dictfile> <word>   # structure, emphasis and media only
wudict lookup -format text  <dictfile> <word>   # no markup at all
```

`clean` drops scripts, stylesheets and presentation and keeps semantic formatting; 
`text` keeps text only. Both read the dictionary's own stylesheet to
tell a sense boundary from a span the dictionary hides, so their output follows
the dictionary rather than its tag skeleton.

`-base` prefixes the `/res/…` references generated by `-format=clean`, 
so an article can point at a running server:

``` sh title="a page whose images and audio resolve"
wudict lookup -format clean -base http://127.0.0.1:6888 ~/Dicts/Oxford.mdx flight > flight.html
```


[What each mode matches](../start/search.md){ .md-button }

## searchall

``` sh title="search all dictionaries, live streaming"
wudict searchall [-mode m] [-n perDict] [-format f] [-dict-dir /path/to/dicts] <term>
```

`-mode` takes `exact` (the default), `prefix`, `contains` or `fts`. `-n` limits
results per dictionary.

**Where it searches.** If `-dict-dir` is not specified
the currently configured `DICT_DIR` is used, resolved through the same chain: `--dict-dir` (repeatable), then the
`DICT_DIR` environment variable, then `wudict.toml`.
The folder searched is printed on standard error.

**What it prints.** The definitions, as plain text - `-format text` is the
default. `clean` and `raw` print them as markup instead, in the
[same forms](#article-format) `lookup` produces, and `-base` applies to both.
`-format list` is the fourth value: one headword per line under each dictionary,
for when the question really is which dictionaries hold the word.

Results are streaming live as they are available, so
`| head` and a piped reader work on a large library. Dictionaries are opened
inside the search and closed as soon as they have answered, so the cost is the
few being read at once rather than the whole library at once.

Exit status is 1 when no dictionary matched, as with `lookup`.

!!! note "It searches the files, not the library"

    `searchall` reads each dictionary in its original format (`.mdx`, `.slob`, `.bgl`, etc)
    and does not use the SQLite indexes, so `-mode contains` and
    `-mode fts` are skipped by dictionaries that only support them once
    indexes are generated. The [web UI](../start/search.md) and the HTTP API use the
    indexed library and do not have this limit.

## keys

``` sh title="list headwords, or an archive's contents"
wudict keys [-offset N] [-n count] <dictfile>
```

On a dictionary, `keys` lists headwords, all of them by default.

On an `.mdd` file, it lists the files that archive holds. The format is the
same, so the command is the same, and no `.mdx` is needed in order to see 
what's inside an `.mdd`.

## res

``` sh title="pull one file out of a dictionary"
wudict res [-o out] [-f] <dictfile> <name>

# Example:
wudict res ~/Dicts/Oxford.mdd audio/word.mp3
```

`<name>` is any name that `keys` printed. An `.mdd` file is accepted directly.

Where the binary output goes depends on how the command is invoked.

- Piped or redirected: to standard output.
- On a terminal: to a file named after the resource. Use `-f` overwrite a file.
- `-o` sets a custom file name (can contain slashes, missing intermediate folders will be created); 
a folder; or `-` to dump the binary output to standard output.

## ingest

``` sh title="prepare dictionaries without a browser"
wudict ingest [-full] [-headwords] [-contains] <dictfile|folder> …
```

Creates `<db-dir>/<dictionary name>/text.db` and `info.txt`. Given a folder, it
prepares everything inside and skips what is already indexed.

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
from an older layout. It is a dry run until you add `-f` (to force).

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
