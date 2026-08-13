---
title: Home
description: WuWeiDict — use every dictionary you own from your browser.
---

# WuWeiDict: all your dictionaries, in your web browser

**WuWeiDict** (`wudict` for short, pronounced _woo-way_) is a golang app
that lets you search all your dictionaries directly from your browser. It runs on
your computer on
[localhost:6888](http://localhost:6888).

[Installation](install.md){ .md-button .md-button--primary }
[Usage](run.md){ .md-button }
[Chrome/Firefox extension](extension.md){ .md-button }

No account. No cloud. No telemetry, no analytics or tracking. **Your dictionaries never leave your
machine.**

---

## Supported formats

<div class="grid cards" markdown>

:fontawesome-solid-file-zipper: **MDict** `.mdx` + `.mdd`

A popuplar HTML-based format with optional media.

:fontawesome-solid-book-bookmark: **StarDict** `.ifo` / `.idx` / `.dict`

The GNU/Linux classic. Synonyms, scanned images, dictzip compression,
all read in place.

:fontawesome-solid-layer-group: **Aard 2** `.slob`

A self-contained format with images, media and styles originally created for Wikipedia dumps.

:fontawesome-solid-language: **Lingvo DSL** `.dsl`, `.dsl.dz`

An old but still widely used format popular in Eastern Europe.

:fontawesome-solid-earth-americas: **Babylon BGL** `.bgl`

Yet another previously popular dictionary format.

:fontawesome-solid-box-archive: **WuWeiDict libraries** `text.db`

WuDict's own internal SQLite3-based format. One folder per dictionary that can
be moved and shared.

</div>

By default wudict searches all your dictionaries, with results displayed as soon as they are retrieved.
You can configure the search priority from the dictionary panel under the hamburger menu (☰).

---

## Search type types

Dictionary headwords are indexed on the fly and results are return even while indexing is still in progress.
Full-text search is optional and can be generated only for specific dictionaries from the dictionary panel (☰),
or for all dictionary using the CLI interface (NOTE: enabling will use additional disk space, up to 3x the dictionary size).

| Search mode   | Availability   | It answers                                                             |
| ------------- | -------------- | ---------------------------------------------------------------------- |
| **Exact**     | out-of-the-box | the headword, accent- and case-insensitive — `corazon` finds `corazón` |
| **Prefix**    | out-of-the-box | every headword that starts with what you typed                         |
| **Contains**  | on demand      | the term anywhere in the word, typos forgiven                          |
| **Full-text** | on demand      | words _inside_ the definitions, ranked by relevance                    |

Exact and prefix work immediately. Contains and
full-text are per-dictionary switches in the ☰ panel, each labelled with its
true size before you commit.

---

#### More

[About](about.md) · [Install](install.md) ·
[Chrome/Firefox extension](extension.md)