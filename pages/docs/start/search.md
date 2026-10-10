---
title: Search
description: Search modes, full-text search, lemmatization, result order, links to a search, and search from the terminal.
---

# Search

The main page has a search box, the dictionary picker, and the results: one
collapsible section per dictionary.

## Search modes

Choose the mode in the drop-down next to the search box.

| Mode | Matches | Needs |
| --- | --- | --- |
| **exact** | the headword; if there is none, the headword ignoring case and accents | |
| **prefix** (default) | the headword, then headwords that start with the query; case and accents ignored | |
| **contains** | headwords that contain the query anywhere | contains index |
| **full-text** | words in the article text, ranked by relevance | full-text index |

Exact and prefix work on every dictionary, indexed or not. Contains and
full-text search only dictionaries that have the index; any other dictionary
shows a link that adds it.

To add an index to one dictionary, click its <kbd>contains</kbd> or
<kbd>full-text</kbd> switch in the dictionary panel. To add the full-text index
to all dictionaries: <kbd>☰</kbd> → folder summary →
<kbd>Full-text for every dictionary…</kbd>.

## Case, accents and spelling

All four modes ignore case and accents: `corazon` finds *corazón*, `OXFORD`
finds *Oxford*.

No mode corrects spelling. Contains matches the query as a literal substring:
`orre` finds *correr*, `corer` finds nothing.

A contains query shorter than 3 characters cannot use the index and is
answered by scanning all headwords, which is slower.

## Index sizes

For a dictionary of 40,000 entries:

| Index | Size | Built |
| --- | --- | --- |
| headword | about 2 MB | automatically |
| contains | about 2.4 MB | on request |
| full-text | about 12 MB | on request |

The dictionary panel shows each index's size for your own dictionaries. An
indexed dictionary is usually smaller than its dictionary files, because
article text is stored compressed.

## Full-text search

Full-text search matches words in the article text of dictionaries that have a
full-text index. Its query has its own syntax
([Full-text query syntax](../reference/fts-syntax.md)); in the other modes the
query is literal text.

### Unquoted queries

A query of several words, without quotes or operators, is matched in up to
three stages. Each dictionary stops at the first stage that returns results.

| Stage | Matches | `no pun intended` finds |
| --- | --- | --- |
| **phrase** | the words adjacent, in the given order | *…and, **no pun intended**, he resigned* |
| **proximity** | all words, at most 10 words between them, in any order | *…clearly **intended** as a **pun**…* |
| **anywhere** | all words anywhere in the article | *…**pun** … **intended**…* |

The result header of a dictionary answered at the second or third stage shows
*phrase not found · words proximity* or *phrase not found · words anywhere*.

## Inflected words

When a single-word query returns nothing in a dictionary, wudict looks up the
word's lemma and searches that dictionary again: *knew* → **know**,
*estuviera* → **estar**. A banner names the lemma.

The retry goes only to dictionaries that returned nothing, and only with lemma
data for the dictionary's language: *sale* → **salir** is never searched in an
English dictionary. How the language is detected:
[Configuration → Lemmatization](../reference/configuration.md#lemmatization).

English lemma data is built in. To install another language:

-   <kbd>☰</kbd> → folder summary → <kbd>Lemmatization…</kbd>, or
    <kbd>🔤 Lemmatization</kbd> on the setup page, and tick the language. It
    applies from the next search. This is the only way on Android.
-   In a terminal: [`wudict lemmas download ru`](../reference/cli.md#lemmas).
    A running server uses it after a restart.

Untick a language to delete its lemma data.

A language's lemma data is loaded on the first search that needs it and uses
1 MB (Persian) to 90 MB (Slovak) of memory; the Lemmatization page lists each
figure. At most [`MORPH_CACHE`](../reference/configuration.md#morph_cache)
languages stay loaded: 2 on a desktop, 1 on Android. `MORPH_CACHE = "0"` turns
lemmatization off.

## Result order

Results appear in the order of the dictionary panel. The section that opens
first is placed above dictionaries that have not answered yet. **Open first**
in the dictionary panel decides which section that is: the first dictionary in
your order that has a result (*My order*, the default), or the first to answer
(*Fastest*).

A search of several dictionaries shows up to 5 results per dictionary; a search
of one dictionary, up to 25. <kbd>more…</kbd> in a section header shows up to
100 results from that dictionary.

## Cross-references

A link in an article is looked up in the same dictionary first. If that
dictionary has no such headword, all dictionaries are searched, and a banner
says so.

## Brief and full view

Lingvo DSL dictionaries mark part of an article as **secondary text**: usually
the examples, often the links to sub-entries. It is hidden when an article
opens. The button at the right end of a dictionary's header shows it; its icon
draws the hidden line dotted. Shown, secondary text is grey.

- The next result opens the way you left the last one. The choice is stored
  with your other settings, so every browser and the Android app that use the
  same wudict share it.
- A full-text match inside secondary text, or a link to a place inside it,
  shows it for that result only.
- <kbd>Ctrl</kbd>+<kbd>\*</kbd> shows or hides it in the section you are
  reading (desktop).

## Keys and buttons

| Key or button | Action |
| --- | --- |
| <kbd>/</kbd> | focus the search box |
| <kbd>Esc</kbd> | close the dictionary panel |
| double-click a word in an article | search for it |
| <kbd>⊞</kbd> / <kbd>⊟</kbd> | expand / collapse all results |
| <kbd>⇔</kbd> | wide layout on or off |
| <kbd>◐</kbd> | theme: auto, light, dark |
| <kbd>Ctrl</kbd>+<kbd>\*</kbd> | show or hide secondary text in the section you are reading |

## Dictionary panel

<kbd>☰</kbd> opens it.

| Control | Effect |
| --- | --- |
| <kbd>−</kbd> <kbd>15px</kbd> <kbd>+</kbd> | article text size; click the size to reset |
| <kbd>A–Z</kbd> (beside the find field) | unpin all dictionaries; it asks first. Shown while a dictionary is pinned |
| <kbd>Highlight matches</kbd> | mark the query's words in articles (full-text only); on by default |
| <kbd>Read aloud</kbd> | offer to read selected article text aloud; on by default |
| **Open first**: *My order*, *Fastest* | which result section opens first |
| **Group by** | the groups the picker offers |
| folder summary <kbd>⚙ 2 folders · 105 dictionaries</kbd> | folders, library and config file in effect; <kbd>Edit folders…</kbd>, <kbd>Rescan folders</kbd>, <kbd>Lemmatization…</kbd>, <kbd>Browse A–Z…</kbd>, <kbd>Edit groups…</kbd>, <kbd>Custom styles…</kbd>, <kbd>Full-text for every dictionary…</kbd> |

Each dictionary row:

-   pin: add the dictionary to the pinned ones, after the others. Pinned
    dictionaries come first, in your order; all the others follow A–Z.
    Results and the picker follow this order.
-   to the top (arrow under a bar): make the dictionary the first in the
    list. An unpinned dictionary is pinned.
-   ▲ ▼, or <kbd>⠿</kbd> (drag, desktop): move a pinned dictionary among the
    pinned ones.
-   checkbox: enabled for *All dictionaries* searches. A disabled dictionary
    can still be chosen in the picker.
-   name: search this dictionary only.
-   switches: <kbd>🚀 index</kbd> (then the index size), <kbd>contains</kbd>,
    <kbd>full-text</kbd>, <kbd>media</kbd>. Click to add or remove.
-   file row (e.g. `Oxford.mdx`): the dictionary files, the library folder, and
    <kbd>🗑 Remove…</kbd>.
-   **About this dictionary**: its groups and its own description.

<kbd>Rescan folders</kbd> re-reads the dictionary folders; use it after adding
or deleting dictionary files.

## Link to a search

The address holds the query, the mode and the dictionary selection, so a search
can be bookmarked or opened from a script. Back and Forward step through
searches.

```
http://localhost:6888/?q=serendipity&mode=prefix&dict=all
```

| Parameter | Value |
| --- | --- |
| `q` | the query |
| `mode` | `exact`, `prefix`, `contains` or `fts` |
| `dict` | `all`, a dictionary id ([`/api/dicts`](../reference/api.md) lists them), or a group, e.g. `g:lang:en` |
| `theme` | `auto`, `light` or `dark`; kept for later visits |

On Android, `wudict://lookup?q=serendipity` opens the same search; see
[the Android app](../apps/android.md).

## Search from the terminal

``` sh title="all dictionaries"
wudict searchall phubbing                              # all dictionaries in DICT_DIR
wudict searchall -dict-dir ~/Dictionaries/es phubbing  # one folder
wudict searchall -format raw phubbing                  # the dictionaries' own HTML
wudict searchall -format clean phubbing                # HTML without scripts and styles
```

``` sh title="one dictionary"
wudict lookup   ~/Dictionaries/Oxford.mdx phubbing
wudict prefix   ~/Dictionaries/Oxford.mdx phubb
wudict contains ~/.wudict/db/Oxford phub
wudict fts      ~/.wudict/db/Oxford "sudden fear"
```

`contains` and `fts` need an indexed dictionary: pass its library folder.
`lookup` and `prefix` work on any dictionary file. `searchall` uses exact mode
unless `-mode` says otherwise.

Full-text queries run as in the web UI. The shell removes quotes before wudict
sees them; wrap the query in single quotes to keep double quotes:

``` sh
wudict searchall -mode fts '"faux ami" NOT linguistics'
```

[Command line reference](../reference/cli.md){ .md-button }
