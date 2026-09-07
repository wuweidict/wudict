---
title: Search
description: The four search modes, streaming results, and search configs.
---

# Search

wuDict's main page consists of a search box and a list of results, one accordion section per dictionary.

## The four modes

There are four search type available to pick from the dropdown.

| Mode | It matches | Use it when |
| --- | --- | --- |
| **Exact** | the headword itself, ignoring case and accents | you know the word |
| **Prefix** *(default)* | every headword that starts with your text | you know how it begins |
| **Contains** | your text anywhere inside a headword | you know the middle, not the start |
| **Full-text** | words inside the article text, ranked by relevance | you remember the meaning, not the word |

Exact and prefix work immediately, through the dictionary's own index.

Contains and full-text index are not generated automatically. 
You can enable them for specific dictionaries via
the <kbd>☰</kbd> panel, or generate full-text-search indexes for all dictionaries by clicking 
the cogwheel <kbd>⚙️</kbd> in the dictionary panel and then in the expanded box clicking 
<kbd>**⚡️ Enable full-text search for every dictionary…**</kbd>.

!!! info "Accents and case are ignored, spelling mistakes are not"

    `corazon` finds `corazón`, and `OXFORD` finds `Oxford`, in every mode.

    No mode corrects a typo. Contains looks for your text as a literal piece of
    a headword: `orre` finds *correr*, `corer` finds nothing.

??? info "Approximate index size for on a 40,000-entry dictionary"

    | Index | Size | Enables |
    | --- | --- | --- |
    | Headword | about 2 MB | exact and prefix, accent-insensitive |
    | Contains | about 2.4 MB | substring headword search |
    | Full-text | about 12 MB | search inside article text |

    The headword index is built automatically. `Contain` and `Full-text` are optional,
    per dictionary. The <kbd>☰</kbd> panel shows the real numbers for your own files, not
    these examples.

    An indexed wuDict dictionary is usually **smaller** than the file it came from,
    because article text is compressed.

??? info "How contains works, and its one limit"

    Contains is an FTS5 trigram index over folded headwords. A trigram index
    needs at least three characters. wuDict searches shorter text with a plain
    scan, which is correct but slower.

    Folded means lower case with accents removed. The index stores the folded
    form, so searching for words with accented characters works as expected.

## Inflected words

A search that finds nothing anywhere is not over. wuDict looks the word up in
its list of word forms and searches again for what it finds — *knew* becomes
**know**, *estuviera* becomes **estar** — and a banner says which word it
switched to.

The retry is per dictionary and per language. A dictionary that already
answered is never second-guessed, and a dictionary is only offered its own
language's word forms: Spanish *sale* → **salir** is never asked of an English
dictionary.

**Only English is built in.** Every other language is a small data file you
install, and there are two ways to do it:

-   Click <kbd>**🔤 Lemmatization**</kbd> — in the ⚙ box of the <kbd>☰</kbd> dictionary panel, or on
    the settings page — tick a language, and it downloads. It works in the next
    search; nothing needs restarting. This is the only route on Android.
-   From a terminal, [`wudict lemmas download ru`](../reference/cli.md#lemmas).

??? info "What a language costs, and how to switch this off"

    The page lists each language's download size (a few hundred KB to 2 MB) and
    the memory it uses while loaded (7 MB for English, 90 MB for Slovak) —
    measured, not estimated. Nothing is loaded until a search needs it, and
    only [`MORPH_CACHE`](../reference/configuration.md#morph_cache) languages
    stay in memory at once — 2 on a desktop, 1 on Android.

    `MORPH_CACHE = "0"` switches the whole thing off, which is what you want if
    you would rather a failed search simply stay failed. The lemmatization page
    tells you when it is off rather than letting you download data nothing will
    read.

    Untick a language to delete its file.

## Dictionary search order

The results appear in the order you have defined in the dictionary panel
with the exception that the first dictionary to return a result always maintains 
its first position in the result list.

A **more…** link expands one dictionary to the full entry, with the
dictionary's own layout, fonts, styles and scripts. Nothing is reflowed or
rewritten.

## Cross-references

Click a link inside an article to follow it. wuDict looks in the dictionary you
are reading first, and widens the search to all dictionaries only when that
dictionary has no such entry.


## Shortcuts

| Key or click | Action                                        |
| --- |-----------------------------------------------|
| <kbd>/</kbd> | focus the search box, from anywhere on the page |
| <kbd>Esc</kbd> | close the <kbd>☰</kbd> panel                              |
| double-click a word in an article | look up selected word                         |
| <kbd>⊞</kbd> | expand / collapse results                     |
| <kbd>⇔</kbd> | switch to a wide layout                 |
| <kbd>◐</kbd> | cycle light, dark and automatic theme         |

## The <kbd>☰</kbd> dictionary panel

This is where you configure your dictionaries.

- **Sort** your dictionaries by dragging the <kbd>⠿</kbd> handle, or with the ▲ ▼ ⏫ ⏬
  buttons. Results stream in that order, and the order is remembered.
- **Enable or disable** a dictionary to be included in the 'All dictionaries' search. 
If you disable a dictionary you can still use it by selecting it from the dropdown. 
- **Toggle** `contains` or `full-text` per dictionary, and pack media.
- **Rescan folders** re-reads every dictionary folder without a restart. Use it
  after adding new dictionaries.

## Link straight to a search

The address bar URL includes the search term and search mode, so any result is a link you can bookmark,
share on the same machine, or open from a script.

```
http://localhost:6888/?q=serendipity&mode=prefix&dict=<dictionary_hash>
```

| Parameter | Value |
| --- | --- |
| `q` | the word to search |
| `mode` | `exact`, `prefix`, `contains` or `fts` |
| `dict` | dictionary id, or `all` — [/api/dicts](../reference/api.md) lists the ids |
| `theme` | `light` or `dark`, remembered from then on |

Back and Forward work as expected and preserve navigation history. On Android, the same link works
as `wudict://lookup?q=serendipity` — see [the Android app](../apps/android.md).

## Searching from the console


``` sh title="search in ALL dictionaries"
wudict searchall phubbing

# search in a custom folder
wudict searchall -dict-dir=/another/dir/Babylon phubbing

# return full raw HTML
wudict searchall -format=raw phubbing

# return cleaned-up HTML
wudict searchall -format=clean phubbing
```

``` sh title="search in a single dictionary in the terminal"
wudict lookup   ~/Dicts/Oxford.mdx phubbing
wudict prefix   ~/Dicts/Oxford.mdx phubb
wudict contains ~/Dicts/Oxford.mdx phub
wudict fts      ~/Dicts/Oxford.mdx "sudden fear"
```

`contains` and `fts` need a prepared dictionary. `lookup` and `prefix` work on
any file.

[All commands](../reference/cli.md){ .md-button }
