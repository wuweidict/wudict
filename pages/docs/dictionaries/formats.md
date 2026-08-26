---
title: Formats
description: Every dictionary format wuDict reads, the files each one needs, and what it does with media.
---

# Formats

wuDict reads five common dictionary formats plus its own SQLite3-based files.

| Format             | Extensions                                                  | Media                                                                                         |
|--------------------|-------------------------------------------------------------|-----------------------------------------------------------------------------------------------|
| **MDict**          | `.mdx`                                                      | companion `dict.mdd`, `dict.1.mdd`, … archives; `.spx` audio is auto-converted                |
| **StarDict**       | `.ifo` plus `.idx` or `.idx.gz`, plus `.dict` or `.dict.dz` | `.syn` synonyms; a `res/` folder or a `res.zip`                                               |
| **Aard 2**         | `.slob`                                                     | standalone binary with text, images, audio and CSS, etc using zlib, bz2 and lzma2 compression |
| **Lingvo DSL**     | `.dsl` or `.dsl.dz`                                         | `dict.dsl.files.zip`; UTF-8, UTF-16 and UTF-32 are detected automatically                     |
| **Babylon**        | `.bgl`                                                      | resources are inside the file; source and target character sets are detected automatically    |
| **wuDict library** | `text.db`                                                   | optional `media.db` in the same folder                                                        |

## How wuDict works

A dictionary is available for search the moment wuDict finds it. It reads the
dictionary's own index for exact and prefix lookups.

Afterwards wuDict **indexes** the dictionary in the background: it copies the
articles and builds its own indexes into a library folder. Prepared
dictionaries search faster, support more modes and use much less memory.

DSL and BGL are prepared as soon as they are opened. Neither format has an
index of its own, so there is nothing to search until wuDict has indexed it.

[wuDict's internal library](library.md){ .md-button }

## Audio

Most pronunciation audio is `.mp3`, `.ogg`, or `.wav`, and plays directly in the browser.

Speex audio (`.spx`) cannot be played by browsers, so wuDict automatically converts it to
WAV format. The `-cgo` builds do this without external dependencies. The
`-purego` flavours need the external `speexdec` cli utility, which must be installed.

[How to fix audio not working](../help/troubleshooting.md#audio){ .md-button }
