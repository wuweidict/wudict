---
title: Supported Formats
description: Every format WuWeiDict reads, how preparation turns one file into a portable library folder, and how to override or repair broken bundled resources.
tags:
  - Formats
---

# WuWeiDict dictionaries

## Fully supportes formats

| Format         | Files                               | Notes                                                                                                           |
| -------------- | ----------------------------------- | --------------------------------------------------------------------------------------------------------------- |
| **MDict**      | `.mdx` + `.mdd`                     | companion `NAME.mdd`, `NAME.1.mdd`, … resource archives; `.spx` audio transcoded in-process                     |
| **StarDict**   | `.ifo` + `.idx(.gz)` + `.dict(.dz)` | `.syn` synonyms; `res/` folder or `res.zip` resources                                                           |
| **Aard 2**     | `.slob`                             | zlib/bz2/lzma2; images, audio and CSS travel inside                                                             |
| **Lingvo DSL** | `.dsl`, `.dsl.dz`                   | UTF-8/16/32 auto-detected; `NAME.dsl.files.zip` resources; prepared automatically on first open                 |
| **Babylon**    | `.bgl`                              | gzip block stream; source/target charsets auto-detected; resources inside; prepared automatically on first open |
| **WuWeiDict**  | wudict SQLite db (`text.db`)        | the app's own portable library — see below                                                                      |

Source dictionary files are **never modified**: the app reads them as needed, and
generates optimized internal SQLite3 db file to speed up search and enable fuzzy searching (with/without accented characters, etc).

## Preview now, prepared quietly

Every dictionary is searchable the moment it is discovered: exact and
prefix lookups ride the dictionary's own built-in index. Then, on first
use, the app silently builds a small **headword index** in the
background — a couple of megabytes — so accent-insensitive matching
(_corazon_ → _corazón_) behaves as expected. Switch it off
(`AUTO_INDEX=off`) and the dictionary still works, you simply lose the fuzzy search benefits.

The two deeper search modes — **contains** and **full-text** — are
optional per-dictionary switches available in the ☰ panel, each will take more disk space
if enabled, so you can enable it only for the dictionaries where you need these features.

## Internal WuWeiDict SQLite3 format

Background indexing produces the following hierarchy under the db folder (default
`~/.wudict/db`):

``` text
~/.wudict/db/
  Oxford/
    text.db     articles + the headword search indexes
    media.db    audio and images — only created if "pack media" is clicked
    info.txt    what this is, where it came from
    res/        optional — your replacements, see below
```

**A dictionary is one folder, so it moves as one thing.** Copy it, zip
it, hand it over. The other machine drops it into any dictionary folder
and it works on its own — no original source files needed. A `text.db`
without a `media.db` still works fully; audio and images simply fall back
to the source file _if_ one is present.

## Replacing a dictionary's broken files (`res/`)

Dictionaries carry their own stylesheets, scripts and audio, and wudict lets you override any existing file or replace a missing one
by placing files in the dictionary's `res/` subfolder:

``` text
~/.wudict/db/Cambridge English Dictionary Online/
  text.db
  res/
    jquery.js      ← replaces the dictionary's own file
    js/entry.js    ← supplies one it never contained
    css/style.css
```

Mirror the path the article asks for — subfolders matter (`js/…`,
`css/…`). Nothing inside the dictionary is ever edited; delete your file
and the dictionary is exactly as it shipped. (One exception: a `.spx`
file placed in `res/` is served as-is, not transcoded — give it `.mp3`
or `.wav` instead.)
