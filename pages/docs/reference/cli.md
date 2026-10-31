---
title: Command line
description: Every wudict command, its flags and arguments.
---

# Command line

``` text
wudict [command] [flags] [args]
```

`wudict --help` prints the full reference; `wudict --version` (`-v`) prints the
version. `--verbose` applies to every command. Flags may be written with one
or two dashes.

## Commands

| Command | Action |
| --- | --- |
| [`serve`](#serve) | start the server (the default command) |
| [`list`](#list) | list the dictionaries in one or more folders |
| [`info`](#info) | show a dictionary's metadata, search modes and files |
| [`lookup`, `prefix`, `fts`, `wildcard`](#lookup-prefix-fts-wildcard) | search one dictionary |
| [`searchall`](#searchall) | search all dictionaries in the dictionary folders |
| [`keys`](#keys) | list headwords, or the files in an `.mdd` |
| [`res`](#res) | extract resources |
| [`dump`](#dump) | export a dictionary to CSV or wudict markdown |
| [`ingest`](#ingest) | index dictionaries |
| [`reindex`](#reindex) | rebuild outdated indexes |
| [`lemmas`](#lemmas) | list, install and remove lemma data |
| [`clean`](#clean) | list or delete broken library folders and orphans |
| [`rm`](#rm) | remove a dictionary |
| [`token`](#token) | print the access-key link, or replace the key |
| [`licenses`](#licenses) | print the licence and third-party notices |

A command that takes a dictionary path also accepts a library folder:
`wudict info ~/.wudict/db/Oxford` and `wudict info ~/.wudict/db/Oxford/text.db`
are equivalent.

## serve

``` sh
wudict
wudict serve --port 9090 --no-browser
wudict ~/Dictionaries/Oxford.mdx     # serve that file's folder and open it
```

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
| `--allow-remote-delete <0\|1>` | [`ALLOW_REMOTE_DELETE`](configuration.md#allow_remote_delete) |
| `--speexdec <path>` | [`SPEEXDEC`](configuration.md#speexdec) |
| `--tray`, `--no-tray` | [`TRAY`](configuration.md#tray) |
| `--config <path>` | [`CONFIG_PATH`](configuration.md#config_path) |
| `--verbose` | [`VERBOSE`](configuration.md#verbose) |

The other settings have no flag; they are set in the environment or in
`wudict.toml`. [Configuration](configuration.md) lists them all.

## list

``` sh
wudict list ~/Dictionaries /Volumes/Ext/Dicts
```

Prints one line per dictionary found in the folders, subfolders included.

## info

``` sh
wudict info ~/Dictionaries/Oxford.mdx
```

Prints the name, format, number of entries, search modes, language and its
source, description, and the files with their sizes: the dictionary files and
the library folder.

``` text
files:
  original  ~/Dictionaries/Oxford.mdx    41.2 MB
  original  ~/Dictionaries/Oxford.mdd   301.7 MB
  prepared  ~/.wudict/db/Oxford          63.4 MB
  total                                 406.3 MB
```

## lookup, prefix, fts, wildcard

``` sh
wudict lookup   [-n max] <dictfile> <word>    # exact; case and accents ignored, the word as typed first
wudict prefix   [-n max] <dictfile> <word>    # the word, then headwords it starts; case and accents ignored
wudict fts      [-n max] <dictfile> <query>   # full-text query, as in the app
wudict wildcard [-n max] <dictfile> <pattern> # headwords that fit <pattern>
```

| Flag | Effect |
| --- | --- |
| `-n` | maximum number of results; default `20` |
| `-format` | `raw` (default): the dictionary's HTML; `clean`: structure, emphasis and media, without scripts, stylesheets and presentation; `text`: no markup |
| `-base <url>` | prefix for the `/res/…` references in `clean` output |

`fts` and `wildcard` need an indexed dictionary: pass its library folder.
`fts` reads the query as the app does
([Full-text query syntax](fts-syntax.md)), and so does `wildcard`
([Wildcard syntax](wildcard-syntax.md)). Put a wildcard pattern in single
quotes, so that the shell does not expand `*` and `?`. The exit status is `1`
when nothing matches.

``` sh title="an article as an HTML file whose images load from the server"
wudict lookup -format clean -base http://127.0.0.1:6888 ~/Dictionaries/Oxford.mdx flight > flight.html
```

## searchall

``` sh
wudict searchall [-mode m] [-n perDict] [-format f] [-dict-dir <dir>] [<dir>] <term>
```

Searches every dictionary in the folders and prints each dictionary's results
as it answers, so `| head` works on a large library. Each dictionary is opened
for the search and closed when it has answered.

| Flag | Effect |
| --- | --- |
| `-mode` | `exact` (default), `prefix`, `fts`, `wildcard` |
| `-n` | results per dictionary; default `10` |
| `-format` | `text` (default), `clean`, `raw` as for `lookup`; `list`: headwords only |
| `-base <url>` | as for `lookup` |
| `-dict-dir <dir>` | folder to search, repeatable; or a `<dir>` argument, which may be a `:`-separated list. Default: `DICT_DIR` |
| `-config <file>` | the `wudict.toml` to read `DICT_DIR` from |

`searchall` opens each dictionary through its own format. Full-text and
wildcard therefore return results only for dictionaries that index themselves
(Lingvo DSL, Babylon, wudict markdown) and for library folders in the searched
folders. The exit status is `1` when no dictionary matches.

## keys

``` sh
wudict keys [-offset N] [-n count] <dictfile>
```

Lists the headwords, all by default. For an `.mdd` file, lists the files it
contains.

## res

``` sh
wudict res [-o out] [-f] [-dry-run] <dictfile> <name|pattern>…
wudict res ~/Dictionaries/Oxford.mdd audio/word.mp3
wudict res -o ~/oxford-audio ~/Dictionaries/Oxford.mdx 'audio/US/'
```

Extracts resources. A `<name>` is a name `keys` prints; an `.mdd` can be given
directly.

One name writes one resource:

| Output | Where |
| --- | --- |
| piped or redirected | standard output |
| on a terminal | a file named after the resource; `-f` overwrites |
| `-o <path>` | that file (missing folders are created), that folder, or `-` for standard output |

A pattern, or more than one name, writes files. The files go in the `-o`
folder, or in the current folder when there is no `-o`. Each file keeps the
folders of its resource name: `audio/US/ab/1.ogg` is written to
`<out>/audio/US/ab/1.ogg`. Standard output is not used.

| Pattern | Selects |
| --- | --- |
| `*` | any number of characters in one folder or file name |
| `?` | one character |
| `**` | any number of folders, also none: `audio/**/1.ogg` |
| `audio/` | everything in the folder `audio`, in all its folders |
| `*.ogg` | a pattern without `/`: every `.ogg` file, in any folder |
| `audio/*.ogg` | a pattern with `/`: from the top, `audio/1.ogg` but not `audio/US/1.ogg` |

Case is ignored; accents are not. Put the pattern in quotes, or the shell
expands it first. A name that exists is read as a name, also when it contains
`*` or `?`.

| Option | Effect |
| --- | --- |
| `-f` | overwrite existing files. Without `-f`, if one file exists, no file is written |
| `-dry-run` | write nothing: print each resource name and its file, separated by a tab |

A resource name that is invalid on the file system is normalized. A resource
that cannot be written is reported and skipped:

- two names that become the same file name, also when they differ only in case;
- a file with the name of a folder that holds other resources.

## dump

``` sh
wudict dump [-format csv|md] [-mode html|clean] [-compress gz] [-resources all|text|none]
            [-include pattern]… [-exclude pattern]… [-articles=false] -o <outdir> <dictfile>
wudict dump -articles=false -include 'audio/' -exclude '*.wav' -o ~/exports ~/Dictionaries/Oxford.mdx
```

### CSV

``` sh
wudict dump -o ~/exports ~/Dictionaries/Oxford.mdx
```

Writes `<outdir>/<name>.csv` in the layout [pyglossary](https://github.com/ilius/pyglossary)
reads and writes (RFC 4180). The first rows are `"#key","value"` metadata; each
following row is one entry: headword, article, alternative headwords.

``` csv
"#name","Cambridge English Dictionary"
"#sourceLang","en"
"#description","18th Edition"
"aardvark","medium-sized, burrowing, nocturnal mammal","ant bear,earth pig"
```

Resources are written to `<name>.csv_res` beside the CSV, with their folder
structure, so `src="audio/word.mp3"` still resolves. A resource name that is
invalid on the file system is normalized; a resource that cannot be normalized
or read is reported and skipped.

An MDX `@@@LINK` redirect is written as its own row pointing at
`entry://target` when dumping the dictionary file, and as an alternative
headword of the target when dumping the library folder. pyglossary accepts
both.

### wudict markdown

``` sh
wudict dump -format md -o ~/exports ~/Dictionaries/Oxford.mdx
wudict dump -format md -mode clean -compress gz -o ~/exports ~/Dictionaries/Oxford.mdx
```

Writes `<outdir>/<name>.wudict.md` ([wudict markdown](../dictionaries/formats.md#wudict-markdown)),
resources in `<name>.wudict.files`.

| Flag | Effect |
| --- | --- |
| `-mode html` (default) | each article's HTML, unchanged |
| `-mode clean` | only what markdown expresses: paragraphs, bold, italic, links, images, lists, tables, headings. Stops at the first article it cannot convert and prints the `-mode html` command |
| `-compress gz` | write `<name>.wudict.md.gz` |

### Both formats

| Flag | Effect |
| --- | --- |
| `-resources all` (default) | every resource |
| `-resources text` | `.css`, `.js`, `.mjs`, `.json`, `.html`, `.htm`, `.xml`, `.txt` only |
| `-resources none` | no resource folder |
| `-include <pattern>` | only the resources that match the pattern; repeat for more patterns |
| `-exclude <pattern>` | not the resources that match the pattern; repeat for more patterns |
| `-articles=false` | no articles: the resource folder only |
| `-o`, `-output` | the output folder |

`-include` and `-exclude` take the patterns of [`res`](#res): `'*.ogg'` is every
`.ogg` file in any folder, `'audio/'` everything in the folder `audio`. Put the
pattern in quotes. A resource is written when it passes `-resources`, matches
one `-include` (if there is one) and matches no `-exclude`. An `-include` that
matches no resource stops the dump before anything is written.

`-articles=false` writes the resources to the same folder as a full dump. A
later dump with `-resources none` adds the articles beside them.

If the output folder exists, `dump` prints which files it overwrites, then
overwrites them. Files left there by an earlier dump under other names are not
deleted. A resource that cannot be written is reported and skipped: two names
that become the same file name, or a file with the name of a folder that holds
other resources. The library's SQLite schema
is documented in [The text.db format](text-db.md).

## ingest

``` sh
wudict ingest [-full] [-fulltext] [<dictfile|folder> …]
```

Indexes dictionaries into `<DB_DIR>/<name>/` (`text.db`, `info.txt`). Without a
path, the dictionary folders (`DICT_DIR`); with a folder, everything in it.
Dictionaries already indexed are skipped.

| Flag | Effect |
| --- | --- |
| none | a new dictionary gets the headword index, as in the app |
| `-fulltext` | add the full-text index |
| `-fulltext=false`, `-headwords` | remove the full-text index |
| `-full` | also add the media pack (`media.db`) |
| `-o <file>` | write one dictionary's `text.db` to this path instead of the library |
| `-config <file>` | the `wudict.toml` to read `DICT_DIR` from |

A flag left out keeps what each dictionary has.

## reindex

``` sh
wudict reindex [-all] [<name|path> …]
```

Rebuilds outdated indexes: built by an older version of wudict, from dictionary
files that changed since, or unreadable. Each dictionary keeps its indexes and
media pack. Names limit the rebuild to those dictionaries; `-all` rebuilds
current ones too. A dictionary whose files are gone is reported and kept. While
a server uses the library, `reindex` has the server run the rebuild and shows
its progress.

## lemmas

``` sh
wudict lemmas list                  # installed and available languages
wudict lemmas download hu fr pt ru  # install, by code or English name
wudict lemmas remove ru             # delete
```

```
Lemma files in ~/.wudict/lemmas
1 installed, 24 available

  [x]  en   English          187 kB  ~7 MB RAM  built in
  [ ]  fr   French           661 kB  ~27 MB RAM
  [x]  hu   Hungarian        187 kB  ~5 MB RAM
  [ ]  ru   Russian          1.9 MB  ~63 MB RAM
  ...

  wudict lemmas download ru sk    install       [x] ready   [ ] not installed
  wudict lemmas remove sk         delete        [!] installed, differs from the catalogue
```

The RAM column is the memory a language uses while loaded; at most
[`MORPH_CACHE`](configuration.md#morph_cache) languages are loaded, and only
when a search needs them.

`download` accepts codes or English names (`ru`, `russian`) and checks every
argument before downloading, so one invalid name fails the whole command.
`-all` installs every language; `-f` downloads a language again although it is
current. Each file is checked against its SHA-256 digest in the catalogue and
written atomically. Files go to [`LEMMA_DIR`](configuration.md#lemma_dir); the
catalogue is [`LEMMA_URL`](configuration.md#lemma_url), which may be a local
`manifest.json`. `list` works offline and reports why the catalogue could not
be read.

A running server reads languages installed this way after a restart. The
Lemmatization page (<kbd>☰</kbd> → folder summary → <kbd>Lemmatization…</kbd>)
installs from the same catalogue without a restart, and is the only way on
Android.

## clean

``` sh
wudict clean                # list
wudict clean -f             # delete broken library folders
wudict clean -f -orphans    # and orphans
```

Lists incomplete or unreadable library folders, interrupted indexing, and
leftovers of an older library layout; `-f` deletes them. Also lists
[orphans](../dictionaries/library.md#orphans), library folders whose dictionary
files are gone; `-f -orphans` deletes those too. A dictionary moved within the
dictionary folders is not an orphan: its library folder is linked to the new
location. A library folder kept with *dictionary files only* or
`rm -keep-index` is not listed.

## rm

``` sh
wudict rm [-f] [-keep-source | -keep-index] <name|path>
```

Removes one dictionary: its library folder and its dictionary files. The
argument is a library name, a library folder, a `text.db`, or a dictionary
file.

| Flag | Effect |
| --- | --- |
| `-f` | delete; without it, `rm` only lists |
| `-keep-source` | delete only the library folder |
| `-keep-index` | delete only the dictionary files; refused while the dictionary has media not in a media pack |

After `-keep-source`, a dictionary still in a dictionary folder is indexed
again on its next search.

## token

``` sh
wudict token           # print the link that carries the access key
wudict token -rotate   # replace the key
```

A browser on another device opens the link once when the server listens on the
network. After `-rotate`, every browser and script holding the old key needs
the new link. See [`AUTH`](configuration.md#auth).

## licenses

Prints the program's licence and the notices for the third-party code built
into it.

## Examples

``` sh
wudict --dict-dir ~/Books/Dicts --port 9090 --no-browser
wudict lookup ~/Dictionaries/Oxford.mdx serendipity
wudict ingest -full ~/Dictionaries/Oxford.mdx
SERVER_PORT=9000 wudict
```
