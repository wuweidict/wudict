---
title: The library
description: Indexing, library folders, moving a dictionary, outdated indexes, and removing dictionaries, indexes and orphans.
---

# The library

The **library** (`~/.wudict/db`, [`DB_DIR`](../reference/configuration.md#db_dir))
holds wudict's indexed copy of each dictionary: one library folder per
dictionary.

## Indexing

| State | Searched via               | Modes |
| --- |----------------------------| --- |
| not indexed | the dictionary files       | exact, prefix |
| indexed | `text.db` (headword index) | exact, prefix, wildcard; faster, less memory |
| + full-text index | the same `text.db`         | + full-text |

A dictionary is searchable as soon as wudict finds it. On its first search
wudict indexes its headwords in the background
([`AUTO_INDEX`](../reference/configuration.md#auto_index)), one dictionary at a
time ([`INDEX_WORKERS`](../reference/configuration.md#index_workers)). With
`AUTO_INDEX = off`, dictionaries stay not indexed and use more memory and CPU.

Lingvo DSL, Babylon and wudict markdown are indexed when first opened: they
have no index of their own. ZIM is never indexed automatically: its own index
answers exact and prefix search with little memory, and an indexed copy is
several times the size of the file. Index a ZIM from the dictionary panel to
use wildcard, full-text or a media pack.

The full-text index and the media pack are added per dictionary with its
switches in the dictionary panel, or with
[`wudict ingest`](#index-from-the-terminal).

## Library folder

``` text title="~/.wudict/db/Oxford/"
text.db     articles and indexes
media.db    media pack, if added
info.txt    source and build details
res/        resource overrides (optional)
```

`text.db` alone is a complete dictionary. Without `media.db`, images and audio
are read from the dictionary files, if present. Both are SQLite databases;
[The text.db format](../reference/text-db.md) documents the schema.

Article text is stored compressed with zstd, so an indexed dictionary is
usually smaller than its dictionary files.
[`COMPRESSION`](../reference/configuration.md#compression) chooses DEFLATE
instead, 1.3 to 3 times as large, for programs and earlier wudict versions that
read only DEFLATE; `none` stores article text uncompressed, 2 to 7 times as
large.

## Move a dictionary to another machine

1. Copy the library folder, e.g. `~/.wudict/db/Oxford`.
2. Put it in the other machine's library, or in one of its dictionary folders.
3. Search. The dictionary files are not needed; without `media.db` there are no
   images or audio.

## Index from the terminal

``` sh
wudict ingest                    # the dictionary folders (DICT_DIR)
wudict ingest ~/Dictionaries/es  # one folder
```

`ingest` skips dictionaries already indexed and gives a new dictionary the
headword index only, as the app does. `-fulltext` adds the full-text index,
`-full` also adds the media pack, and `-fulltext=false` removes the full-text
index. A flag left out keeps what each dictionary has.
See [`ingest`](../reference/cli.md#ingest).

## Outdated indexes

An index is outdated when an older version of wudict built it, when the
dictionary files changed since, or when its article text is stored with another
compression than [`COMPRESSION`](../reference/configuration.md#compression) -
which, after an upgrade, is every dictionary indexed before zstd. The dictionary panel then shows *N dictionaries have outdated indexes* and <kbd>Rebuild</kbd>;
[`wudict reindex`](../reference/cli.md#reindex) does the same from a terminal.
A rebuild keeps each dictionary's indexes and media pack. <kbd>Stop</kbd> ends
it after the current dictionary.

## Remove a dictionary

<kbd>☰</kbd> → the dictionary's file row (e.g. `oald10.mdx`) →
<kbd>🗑 Remove…</kbd>. The confirmation offers:

| Choice | Deletes |
| --- | --- |
| <kbd>💥 delete everything</kbd> | the library folder and the dictionary files |
| <kbd>index only</kbd> | the library folder; the dictionary files stay |
| <kbd>dictionary files only</kbd> | the dictionary files; offered when the library folder is complete (media pack added, or no media) |

After *index only*, a dictionary still in a dictionary folder is indexed again
on its next search ([`AUTO_INDEX`](../reference/configuration.md#auto_index)).

!!! warning "Deletion is permanent"

    Files are unlinked, not moved to the Trash or the Recycle Bin.

When running wudict on a LAN, from another host, removal needs
[`ALLOW_REMOTE_DELETE`](../reference/configuration.md#allow_remote_delete).

## Remove an index

Click the corresponding switch in the dictionary panel. When the dictionary files are gone,
the switches are locked: the library folder is the only copy.

## Orphans

An **orphan** is a library folder whose original dictionary files are gone.
<kbd>Rescan folders</kbd> lists orphans with their sizes: ticked ones are
deleted, unticked ones are kept and not listed again. A dictionary file moved
within the dictionary folders is not an orphan; its library folder follows it.
A library folder kept with *dictionary files only* is never listed.
[`USE_CACHED`](../reference/configuration.md#use_cached) lists orphans as
ordinary dictionaries.

## From the terminal

``` sh
wudict clean               # list broken or incomplete library folders, and orphans
wudict clean -f            # delete the broken ones
wudict clean -f -orphans   # and the orphans
wudict rm Oxford           # list what removing Oxford would delete
wudict rm -f Oxford        # delete the library folder and the dictionary files
```

`rm -keep-source` deletes only the library folder, `rm -keep-index` only the
dictionary files. Neither command deletes anything without `-f`.

[Command line reference](../reference/cli.md){ .md-button }
