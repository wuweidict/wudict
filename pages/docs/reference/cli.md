---
title: Command line
description: Every wudict command, flags and args.
---

# Command line

``` text title="the command flow"
wudict [command] [flags] [args]
```

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
| [`dump`](#dump) | export a dictionary to a pyglossary-compatible CSV file            |
| [`ingest`](#ingest) | trigger indexing of dictionaries                                   |
| [`lemmas`](#lemmas) | list, install and remove lemmatization data                        |
| [`clean`](#clean) | list or delete broken library items                                |
| [`rm`](#rm) | remove a dictionary                                                |

`--verbose` works with every command.

## serve

`serve` is the default command, so `wudict` and `wudict serve` do exactly the same.

``` sh title="starting the wudict server"
wudict
wudict serve --port 9090 --no-browser
wudict ~/Dicts/Oxford.mdx     # serve that file's folder and open it
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

Shows name, format, entry count and supported search modes, and occupied disk space (`.mdd` media,
`.files.zip`, `.idx`/`.dict.dz`), and the index folder.

``` title="the file list"
files:
  original  /Dicts/Oxford.mdx        41.2 MB
  original  /Dicts/Oxford.mdd       301.7 MB
  prepared  ~/.wudict/db/Oxford      63.4 MB
  total                             406.3 MB
```

Every command that takes a dictionary path accepts a **prepared folder** as
readily as a file — `wudict info ~/.wudict/db/Oxford` and `wudict info
~/.wudict/db/Oxford/text.db` are the same request. The folder is the unit you
copy, move and zip, so it is the name the library lists, and it is a name you
can type.


## Search commands

The four commands match the four modes in the app.

``` sh title="the four modes"
wudict lookup   [-n max] <dictfile> <word>     # exact, with an accent-folded fallback
wudict prefix   [-n max] <dictfile> <word>     # exact, else prefix; accent-insensitive
wudict contains [-n max] <dictfile> <word>     # substring headword search
wudict fts      [-n max] <dictfile> <query>    # full text of the articles
```

By default, each prints the raw article HTML to standard output. 
You can pass `-format=text` to print a plain text version. `-n` limits the number of results.


### Article format

`lookup`, `prefix`, `contains`, `fts` all accept the `-format` flag,
same as the [REST API](api.md#article-formats).

``` sh title="available CLI output formats"
wudict lookup -format raw   <dictfile> <word>   # raw HTML (the default)
wudict lookup -format clean <dictfile> <word>   # structure, emphasis and media only
wudict lookup -format text  <dictfile> <word>   # no markup at all
```

`clean` drops scripts, stylesheets and presentation and keeps semantic formatting; 
`text` keeps text only.

`-base` prefixes the `/res/…` references generated by `-format=clean`, 
so an article can point at a running server:

``` sh title="dump an article to and HTML file with links working"
wudict lookup -format clean -base http://127.0.0.1:6888 ~/Dicts/Oxford.mdx flight > flight.html
```

[What each mode matches](../start/search.md){ .md-button }

## searchall

``` sh title="search all dictionaries, live streaming"
wudict searchall [-mode m] [-n perDict] [-format f] [-dict-dir /path/to/dicts] <term>
```

`-mode` takes `exact` (the default), `prefix`, `contains` or `fts`. `-n` limits
results per dictionary.

**Search folders** If `-dict-dir` is not specified
the currently configured `DICT_DIR` is used, resolved through the same chain: `--dict-dir` (repeatable), then the
`DICT_DIR` environment variable, then `wudict.toml`.

**Output modes** The definitions, as plain text - `-format text` is the
default. `clean` and `raw` print them as markup, in the
[same forms](#article-format) `lookup` produces, and `-base` applies to both.
`-format list` only lists the dictionaries that contain the search term _without_ the definitions.

Results are streaming live as they are available, so
`| head` and a piped reader work on a large library. Dictionaries are opened
inside the search and closed as soon as they have answered, so the cost is the
few being read at once rather than the whole library at once.

Exit status is 1 when no dictionary matched, as with `lookup`.

!!! note "CLI search mode"

    `searchall` reads each dictionary in its original format (`.mdx`, `.slob`, `.bgl`, etc)
    and does not use the SQLite indexes.

## keys

``` sh title="list headwords, or an archive's contents"
wudict keys [-offset N] [-n count] <dictfile>
```

On a dictionary, `keys` lists the headwords.

On an `.mdd` file, it lists the contained file names.

## res

``` sh title="pull one file out of a dictionary"
wudict res [-o out] [-f] <dictfile> <name>

# Example:
wudict res ~/Dicts/Oxford.mdd audio/word.mp3
```

where `<name>` is the name that was printed with the `keys` command. 

Where the binary output goes depends on how the command is invoked.

- Piped or redirected: to standard output.
- On a terminal: to a file named after the resource. Use `-f` overwrite a file.
- `-o` sets a custom file name (can contain slashes, missing intermediate folders will be created); 
a folder; the special placeholder `-` (`-o -`) to dump the file output to standard output.

## dump

``` sh title="export a dictionary to CSV"
wudict dump -o <outdir> <dictfile>

# Example:
wudict dump -o ~/exports ~/Dicts/Oxford.mdx
```

Writes `<outdir>/<name>.csv` in 
[pyglossary](https://github.com/ilius/pyglossary)-compatible .CSV format:

The CSV file is RFC 4180 compliant. The leading rows are `"#key","value"` metadata, and
every row after them is one entry:

``` csv
"#name","Cambridge English Dictionary"
"#sourceLang","en"
"#description","18th Edition"
"aardvark","medium-sized, burrowing, nocturnal mammal","ant bear,earth pig"
```

**Resources.** If the dictionary has images, audio, .css, .js files, they are
unpacked into `<name>.csv_res` beside the CSV, derived from the file's name. 
The folder structure inside the
container is preserved, so an article's `src="audio/word.mp3"` still resolves
after the conversion. Resource file names that are invalid for the filesystem are normalized;
if normalization fails the resource is skipped and reported, and so is a resource that fails to read, rather
than aborting the operation.

**Indexed dictionaries vs. source dictoinaries.** A dictionary that has been indexed in the library
can be dumped either way, and the two differ in one respect: dumping the
source writes an MDX `@@@LINK` redirect as its own row pointing at
`bword://target`, while dumping the prepared folder writes it as an alternate
headword on the target's row, because that is what ingest resolved it into.
Both are valid and accepted by [pyglossary](https://github.com/ilius/pyglossary).

If the output folder already exists, `dump` warns before overwritting. 
Files left by an earlier dump under a different name are **not** deleted.

## ingest

``` sh title="prepare dictionaries from CLI"
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

## lemmas

``` sh title="install lemma data for another language"
wudict lemmas list                  # what is installed, and what is available
wudict lemmas download hu fr pt ru  # install, by code or by name
wudict lemmas remove ru             # delete
```

WuWeiDict recognises inflected forms — searching *knew* finds **know** — and
only English is built into the program. Every other language is a data file,
and this is how you get one:

```
Lemma files in ~/.wudict/lemmas
1 installed, 24 available

  [x] en  English                    ~7 MB RAM   built in
  [ ] fr  French       1.1 MB       ~28 MB RAM
  [x] hu  Hungarian     238 KB       ~6 MB RAM
  [ ] ru  Russian       1.9 MB      ~65 MB RAM
  ...

  wudict lemmas download pl ru    install       [x] ready   [ ] not installed
  wudict lemmas remove pl         delete        [!] installed, differs from the catalogue
```

The **RAM** column is what that language costs while it is loaded, measured
rather than estimated. Only [`MORPH_CACHE`](configuration.md#morph_cache)
languages are held at once, and none is loaded until a search needs it.

`download` takes codes or English names — `ru` and `russian` are the same
request — and checks every argument before it downloads anything, so a typo
fails the whole command instead of installing three languages out of four.
`-all` installs the entire catalogue; there is no way to do that by accident.
`-f` re-downloads a language that is already up to date.

Every file is verified against a SHA-256 digest published in the catalogue and
written atomically, so an interrupted download leaves nothing behind. Files land
in [`LEMMA_DIR`](configuration.md#lemma_dir); the catalogue they come from is
[`LEMMA_URL`](configuration.md#lemma_url), which may also be a folder on disk
for an installation with no network.

`list` still works offline — it prints what is installed and says why it could
not reach the catalogue.

A server that is already running keeps the languages it started with; restart it
to pick up one installed this way.

!!! tip "The same thing without a terminal"

    **🔤 Lemmatization**, in the ⚙ box of the ☰ dictionary panel and on the
    settings page, lists the same catalogue with a checkbox per language. It
    installs into the same folder from the same catalogue, and the running
    server picks a language up immediately — it re-reads the folder itself,
    so there is no restart. It is the only route on Android, which has no
    shell.

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
