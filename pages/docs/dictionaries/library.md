---
title: The library
description: How wuDict prepares a dictionary, what a library folder contains, and how to move, copy or remove one.
---

# The library

**The library** is where wuDict keeps its own indexes.
Its default folder is `~/.wudict/db`.

## The three stages

| Stage | What wuDict uses                       | What works |
| --- |----------------------------------------| --- |
| **Preview** | the dictionary's own file and index    | exact and prefix search |
| **Prepared** | `text.db` in the library folder        | exact and prefix, faster, with far less memory |
| **Extended** | the same `text.db`, with extra indexes | contains and full-text as well |

Preview starts the moment wuDict finds the file. Preparation starts on the
first search and runs in the background. Full-text and `contains` search indexes can be enabled on-demand 
only for the dictionaries where you need them, via the ☰ dictionary panel.

Headword indexing is automatic by default. Set
[`AUTO_INDEX`](../reference/configuration.md#auto_index) to `off` if you want to disable auto-indexing 
(not recommenced — while it might save a few megabytes of disk space this will cause higher CPU and RAM usage).

By default only one dictionary is indexed at a time to preserve RAM and CPU. You can increase the number of
parallel indexing threads via the
[`INDEX_WORKERS`](../reference/configuration.md#index_workers) parameter.

## What a library folder holds

``` text title="~/.wudict/db"
~/.wudict/db/
  Webster/
    text.db     articles and the search indexes
    media.db    audio and images - only if you clicked "pack media"
    info.txt    what this is and where it came from
    res/        optional: your replacement files
```

`text.db` alone is a complete dictionary. Without `media.db`, audio and images
are read from the original files if they are present.

Article text is compressed, so a prepared folder is usually smaller than the
file it came from. Set
[`NO_COMPRESS`](../reference/configuration.md#no_compress) to disable compression
for the definitions stored in the SQLite3 database. That makes the database roughly three times larger.

## Move a dictionary to another machine

1. Copy the whole folder, for example `~/.wudict/db/Oxford`.
2. Put it in the other machine's library folder, or in any folder that machine
   scans.
3. Search. The original dictionary file is not needed.

## Prepare from the terminal

``` sh title="prepare everything in a folder, no browser"
wudict ingest ~/Dicts
```

`ingest` skips what is already prepared. Add `-headwords` for the small index
only, `-contains` for the substring index, and `-full` to pack media as well.

## Removing FTS and contains indexes

Turn an index off with the same ☰ panel switch that turned it on.

``` sh title="two commands that delete"
wudict clean            # list broken or leftover library items
wudict clean -f         # delete them
wudict rm Webster        # list what removing "Webster" would delete
wudict rm -f Webster     # delete the library folder AND the original files
```

Both commands list what they would do and delete nothing until you add `-f`.

`rm` takes `-keep-source` to delete only the prepared folder, or `-keep-index`
to delete only the originals.

[Full CLI reference](../reference/cli.md){ .md-button }

## Dictionaries whose originals are gone

wuDict lists a prepared dictionary only when its original file is still in a
scanned folder. To list every prepared dictionary regardless, switch
[`USE_CACHED`](../reference/configuration.md#use_cached) on, or click **Use
these dictionaries** on the setup page.
