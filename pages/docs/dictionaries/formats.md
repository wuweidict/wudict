---
title: Formats
description: The dictionary formats wudict reads, the files each needs, and how each is indexed.
---

# Formats

| Format | Files | Resources and notes                                                     |
| --- | --- |-------------------------------------------------------------------------|
| **MDict** | `.mdx` | `.mdd`, `.1.mdd`, … archives; files beside the `.mdx` replace their `.mdd` copy ([Resource overrides](override.md#files-beside-an-mdict-dictionary)); `.spx` audio auto-converted to WAV |
| **StarDict** | `.ifo` + `.idx` or `.idx.gz` + `.dict` or `.dict.dz` | `.syn` synonyms; `res/` folder or `res.zip`                             |
| **Aard 2** | `.slob` | everything in the file; zlib, bz2 and LZMA2 compression                 |
| **Lingvo DSL** | `.dsl` or `.dsl.dz` | `.dsl.files.zip`; `.ann` annotations; UTF-8, UTF-16 and UTF-32 detected |
| **Babylon** | `.bgl` | everything in the file; source and target character sets detected       |
| **ZIM** | `.zim`, or a split archive: `.zimaa`, `.zimab`, … | everything in the file (Kiwix, Wikipedia, Wiktionary); the parts of a split archive are read as one file |
| **wudict markdown** | `.wudict.md`, `.wudict.md.gz`, `.wudict.md.dz`, or `.md` | `<name>.wudict.files` folder or `<name>.wudict.files.zip`               |
| **wudict library** | `text.db` | `media.db` in the same folder                                           |

## Indexing

Every dictionary is searchable as soon as wudict finds it: exact and prefix
search use the dictionary's own index. wudict then indexes it in the
background into the library, which is faster, uses less memory, and enables
_wildcard_ search and the optional _full-text_ search.

- **Lingvo DSL, Babylon, wudict markdown** are indexed when first opened: they
  have no index of their own.
- **ZIM** is never indexed automatically. Its own index answers exact and
  prefix search with little memory, and an indexed copy is several times the
  size of the file. Index it from the dictionary panel for _wildcard_ or
  _full-text_ search, or for a media pack.

[The library](library.md){ .md-button }

## wudict markdown

A dictionary as one [Markdown (CommonMark)](https://commonmark.org) file. The first line
is `# ` which is the title; the second line is `wudict: 1`, which is a marker for a
wudict dictionary, so other `.md` files in a dictionary folder are ignored. Each
entry is a `## headword` heading followed by its text; further `##` headings
directly below it are alternative headwords.

``` markdown
# My Glossary
wudict: 1

## colour
## color

The property of an object that depends on the light it reflects.
```

`wudict dump -format md` writes any dictionary readable by wudict in this format.

[Specification](https://github.com/wuweidict/wudict/blob/master/docs/WUDICT-MARKDOWN.md){ .md-button }
[wudict dump](../reference/cli.md#dump){ .md-button }

## Audio

`.mp3`, `.ogg` and `.wav` play in the browser. Browsers cannot play Speex
(`.spx`), so wudict converts it to WAV: `-cgo` builds and the Android app with
a built-in decoder, `-purego` builds with the external `speexdec` program.

[No sound](../help/troubleshooting.md#audio){ .md-button }
