---
title: The text.db format
description: The SQLite format of an indexed wudict dictionary - tables, meta keys, article storage, and reading or writing it from another program.
---

# The `text.db` format

An indexed dictionary is an SQLite 3 database, `text.db`, with an optional
second database for its media pack, `media.db`. This page specifies both, for
programs that read or write them.

``` text title="one library folder"
~/.wudict/db/Webster/
  text.db          articles and indexes
  media.db         media pack: images and audio (optional)
  media.link.db    a cache; can be deleted      (optional)
  info.txt         source and build details
  res/             resource overrides           (optional)
```

`text.db` alone is a complete dictionary; the other files are optional or
derived.

`PRAGMA user_version` is **1** or **2**. A database written with zstd, the
default, is 2: its articles can be zstd rows, and it has a `body_dict` table. A
database written with DEFLATE or without compression is 1. wudict does not open
a database with another value. Tables have been added without changing the
value (see [Compatibility](#compatibility)), so a reader must check that a
table exists before using it.

## Schema

``` sql title="text.db"
PRAGMA user_version = 1;

CREATE TABLE meta(key TEXT PRIMARY KEY, value TEXT);

CREATE TABLE entry(id INTEGER PRIMARY KEY,
                   w  TEXT NOT NULL,          -- display headword
                   m  TEXT NOT NULL);         -- article body (see below)
CREATE INDEX idx_entry_w ON entry(w COLLATE NOCASE);

CREATE TABLE alias(w        TEXT    NOT NULL, -- an alternative spelling
                   entry_id INTEGER NOT NULL REFERENCES entry(id));
CREATE INDEX idx_alias_w ON alias(w COLLATE NOCASE);

CREATE VIRTUAL TABLE entry_fts USING fts5(    -- rowid = entry.id
    w, txt, content='', columnsize=0,
    tokenize='unicode61 remove_diacritics 2');
```

A version-2 database also has the dictionary its zstd rows are compressed with:

``` sql title="text.db, user_version 2"
CREATE TABLE body_dict(id   INTEGER PRIMARY KEY,  -- 0
                       data BLOB NOT NULL);       -- a raw zstd dictionary
```

`entry` and `body_dict` hold the whole dictionary. The other tables are indexes
and can be rebuilt from it.

### Reading articles

`entry.m` is decided per row by its first byte:

| First byte | What the row holds |
| --- | --- |
| `0x00` | raw DEFLATE stream, starting at byte 1 |
| `0x01` | version 2 only: a zstd frame without its 4-byte magic number `28 B5 2F FD`, starting at byte 1 |
| `0x02` to `0x08` | version 2 only: reserved; read as an empty article |
| anything else | the article HTML itself, as text |

In a version-1 database, `0x01` to `0x08` are text. Compressed and literal rows
occur in the same table: short articles, and ones that did not compress, are
stored literally. **A reader must handle every form.**

A zstd row is decoded with the database's dictionary: `body_dict.data` where
`id = 0`, used as **raw content**, not as a zstd dictionary file. The frames
carry no dictionary ID. A version-2 database without a `body_dict` row has no
dictionary, and its zstd rows decode without one.

``` python title="every row, every form (Python 3.14)"
import sqlite3, zlib
from compression import zstd

db = sqlite3.connect("file:text.db?mode=ro", uri=True)
version = db.execute("PRAGMA user_version").fetchone()[0]
raw = db.execute("SELECT data FROM body_dict WHERE id = 0").fetchone() if version >= 2 else None
zdict = zstd.ZstdDict(raw[0], is_raw=True) if raw else None

for w, m in db.execute("SELECT w, m FROM entry ORDER BY id"):
    blob = m if isinstance(m, bytes) else m.encode()
    if blob[:1] == b"\x00":
        html = zlib.decompress(blob[1:], -15).decode()
    elif version >= 2 and blob[:1] == b"\x01":
        html = zstd.decompress(b"\x28\xb5\x2f\xfd" + blob[1:], zstd_dict=zdict).decode()
    elif version >= 2 and blob and blob[0] <= 8:
        html = ""
    else:
        html = blob.decode()
    print(w, html[:80])
```

`-15` is raw DEFLATE with no zlib or gzip header — `zlib.decompress(blob[1:])`
without it will fail. Before Python 3.14, the `zstandard` package reads the same
rows: `ZstdDecompressor(dict_data=ZstdCompressionDict(data,
dict_type=DICT_TYPE_RAWCONTENT))`.

Each row is compressed on its own, so any row can be read alone.
[`COMPRESSION`](configuration.md#compression) sets the codec for dictionaries
indexed afterwards: `zstd` (level 1), `deflate` (level 6, level 1 on Android)
or `none`. Reading handles every codec and level.

### Headwords, aliases and sub-entries

- **`entry.w`** is the headword as it is displayed. Lookups compare
  `COLLATE NOCASE`, which is the collation both indexes are built with.
- **`alias`** holds every other spelling that must find this entry: StarDict
  `.syn` entries, MDX `@@@LINK` redirects, Slob aliases, DSL variant
  headwords. Redirects are resolved during indexing: an alias always
  points at an existing `entry.id`, never at another alias.
- A headword beginning with **`@`** (`@examples_woman`) is an MDict-style
  **sub-entry**: a fragment an article pulls in by link, never a word. It stays
  reachable by exact lookup, is excluded from browsing and wildcard search,
  and is *not* counted in `meta.entry_count` (`meta.sub_entries` counts them).
  Exporters should normally skip `w LIKE '@_%'`.

### The search index

`entry_fts` is **contentless** (`content=''`) and keyed by
`rowid = entry.id`; it holds no text that needs exporting.

| Table | Column | Holds | Present when |
| --- | --- | --- | --- |
| `entry_fts` | `w` | the headword | always |
| `entry_fts` | `txt` | the article with tags stripped | with the full-text index; otherwise `''` |

`entry_fts` exists in every `text.db`: accent-insensitive exact and prefix
lookup use it.

## `meta`

A flat key/value table. Unknown keys must be ignored; absent keys must be
treated as empty rather than as an error.

| Key | Meaning |
| --- | --- |
| `name` | **The title shown to the reader.** |
| `description` | **The text under *About this dictionary*.** Plain text or a small HTML fragment; a tag marks it as HTML. |
| `format` | Where it came from: `mdx`, `stardict`, `slob`, `dsl`, `bgl`, `zim`, … |
| `entry_count` | Entries excluding sub-entries |
| `sub_entries` | How many `@`-prefixed fragments there are |
| `ingest_level` | `text` = article text is indexed, `headwords` = only headwords |
| `body_encoding` | `zstd-1`, `deflate-6`, `plain` and so on — a note for humans; the read path decides per row |
| `body_dict_bytes` | Version 2: the size of the zstd dictionary in `body_dict` |
| `dict_uuid` | 32 hex characters; what pairs this file with its `media.db` |
| `index_lang`, `contents_lang` | Languages, only when the source declared them |
| `source_path`, `source_size`, `source_mtime`, `source_sha256_1M` | The dictionary file it was indexed from, for detecting changes |
| `created` | RFC 3339, UTC |
| `markup_version` | Which article-markup rules built this file |

`name` and `description` are HTML-unescaped on the way out, so a title stored
as `A &amp; B` displays as `A & B`.

## Article HTML

Bodies are stored **as the dictionary wrote them**, converted to HTML where
the source format was not HTML. In particular, references to media are left in
their original spelling:

``` html
<img src="pictures/lion.jpg">
<a href="sound://lion.mp3">🔊</a>
```

wudict rewrites them to `/res/<dictionary id>/…` when it serves an article, not
in the database. An exporter gets the original names and looks each one up in
`media.db` (below) or beside the dictionary file.

## `media.db`

Written when the media pack is added. Same conventions as `text.db`;
`user_version` is 1 whatever the `text.db` beside it has.

``` sql title="media.db"
PRAGMA user_version = 1;
CREATE TABLE meta(key TEXT PRIMARY KEY, value TEXT);   -- dict_uuid, name, format
CREATE TABLE resource(name TEXT PRIMARY KEY, mime TEXT, data BLOB);
```

- `resource.name` is the name as articles reference it. A name is matched
  exactly, then case-insensitively, then in both Unicode normalization forms:
  MDX stores resource names in lower case, loose files keep their spelling, and
  an article may use either.
- `resource.data` is the file's bytes, not compressed by wudict.
- `meta.dict_uuid` **must** equal the `dict_uuid` in the `text.db` beside it.
  A mismatched pair is refused.

`media.link.db` caches where media is located inside the dictionary files. It
is derived, need not be copied, and can be deleted.

## Exporting

Without SQL:

``` sh title="whole dictionary as CSV, pyglossary layout"
wudict dump -o out ~/.wudict/db/Webster
```

This writes `out/Webster.csv`: `"#key","value"` metadata rows, then one row
per entry (headword, article, alternative headwords), with resources in
`Webster.csv_res`. [pyglossary](https://github.com/ilius/pyglossary) converts
this layout to any format it supports.

The complete dictionary, with SQL:

``` sql title="entries with their alternative spellings"
SELECT e.id, e.w, e.m,
       (SELECT group_concat(a.w, char(10)) FROM alias a WHERE a.entry_id = e.id) AS aliases
FROM entry e
WHERE e.w NOT LIKE '@_%'          -- skip sub-entry fragments
ORDER BY e.id;
```

Decode `m` as shown [above](#reading-articles). Open the file read-only
(`file:…?mode=ro`) while wudict may be running.

## Authoring a dictionary directly

A `text.db` written by another program is a wudict dictionary. Put its folder
in the library or in a dictionary folder.

Requirements:

- [x] `PRAGMA user_version = 1`, with DEFLATE or uncompressed bodies. Version 2
      is for zstd rows and needs `body_dict`.
- [x] Create `entry`, `alias` **and** `entry_fts` even for a headwords-only
      dictionary; leave `entry_fts.txt` as `''` when you do not index text.
- [x] Create both indexes exactly as shown, with **`COLLATE NOCASE`**; otherwise
      SQLite does not use them and every lookup scans the table.
- [x] Bodies may be stored uncompressed.
- [x] Set `meta.name` and `meta.description`: they are the title and the
      *About this dictionary* text.
- [x] Set `meta.dict_uuid` (16 random bytes, hex) if you also write a
      `media.db`, and put the same value in both.
- [x] Leave out `meta.source_path`. If it names an existing file, wudict takes
      the About text from that file and ignores `description`.
- [x] Write to a temporary file and rename it into place, so an incomplete
      database is never found.

`meta.format` is a free label. Use your own (`native`), not `dsl` or
`stardict`: wudict treats those as built by itself and may offer to rebuild
them.

## Compatibility

- Version 1 is every `text.db` written before zstd, and every one written with
  `COMPRESSION = "deflate"` or `"none"`; wudict versions before zstd open only
  version 1. A `text.db` written by an older wudict can also hold an
  `entry_trigram` table and the `meta` keys `has_trigram` and `fold_version`.
  wudict no longer reads them.
- Unknown `meta` keys are ignored, so you can add your own.
- A database without an optional index opens with fewer search modes.
- FTS5 is required: an SQLite build without it cannot open a `text.db`.

[The library](../dictionaries/library.md){ .md-button }
[CLI reference](cli.md){ .md-button }
