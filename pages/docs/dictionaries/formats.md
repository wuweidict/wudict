---
title: Formats
description: Every dictionary format wuDict reads, the files each one needs, and what it does with media.
---

# Formats

wuDict reads six common dictionary formats plus its own SQLite3-based files.

| Format         | Extensions                                                  | Media                                                                                         |
|----------------|-------------------------------------------------------------|-----------------------------------------------------------------------------------------------|
| **MDict**      | `.mdx`                                                      | companion `dict.mdd`, `dict.1.mdd`, … archives; `.spx` audio is auto-converted                |
| **StarDict**   | `.ifo` plus `.idx` or `.idx.gz`, plus `.dict` or `.dict.dz` | `.syn` synonyms; a `res/` folder or a `res.zip`                                               |
| **Aard 2**     | `.slob`                                                     | standalone binary with text, images, audio and CSS, etc using zlib, bz2 and lzma2 compression |
| **Lingvo DSL** | `.dsl` or `.dsl.dz`                                         | `dict.dsl.files.zip`; UTF-8, UTF-16 and UTF-32 are detected automatically                     |
| **Babylon**    | `.bgl`                                                      | resources are inside the file; source and target character sets are detected automatically    |
| **ZIM**        | `.zim`                                                      | everything is inside the file: articles, images, audio and CSS (Kiwix, Wikipedia, Wiktionary)  |
| **wuDict library** | `text.db`                                                   | optional `media.db` in the same folder                                                        |

## How wuDict works

A dictionary is available for search the moment wuDict finds it. It reads the
dictionary's own index for exact and prefix lookups.

Afterwards wuDict **indexes** the dictionary in the background: it copies the
articles and builds its own indexes into a library folder. Prepared
dictionaries search faster, support more modes and use much less memory.

DSL and BGL are prepared as soon as they are opened. Neither format has an
index of its own, so there is nothing to search until wuDict has indexed it.

ZIM is the opposite case and is **never** indexed automatically. Its own index
already answers exact and prefix searches straight from the file, using almost
no memory even for a whole Wiktionary, and a ZIM is packed far more tightly than
a library folder can be - indexing one would take several times the disk the
file itself uses. Index it from the dictionary panel when you want *contains*,
full-text or packed media; nothing else changes.

[wuDict's internal library](library.md){ .md-button }

## Audio

Most pronunciation audio is `.mp3`, `.ogg`, or `.wav`, and plays directly in the browser.

Speex audio (`.spx`) cannot be played by browsers, so wuDict automatically converts it to
WAV format. The `-cgo` builds do this without external dependencies. The
`-purego` flavours need the external `speexdec` cli utility, which must be installed.

[How to fix audio not working](../help/troubleshooting.md#audio){ .md-button }
