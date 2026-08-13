---
title: Use
description: The four search modes, the shortcuts, the panel — how to read through your whole dictionary wall as if it were one book.
tags:
  - Quickstart
---

# Use: one search box, every dictionary you own

The page that opens is one search box and a wall of results. Everything
else hides until you need it — which is the point. Reading a dictionary
should feel like reading, not like operating software.

## The four modes

Pick a mode from the dropdown next to the search box. Three are free from
day one; two are one click away from becoming free too.

| Mode | What it does | Reach for it when |
|---|---|---|
| **Exact** | the headword itself, accent- and case-insensitive — `corazon` finds `corazón` | you know the word |
| **Prefix** *(default)* | every headword that starts with your text | you half-remember it |
| **Contains** | the term anywhere in the word, typos forgiven | you are missing a letter |
| **Full-text** | words *inside* the definitions, ranked by relevance | you recall the definition, not the word |

Exact and prefix work through the dictionary's own native index — instant,
no setup, even the very first time. Contains and full-text need a small
per-dictionary index; the ☰ panel offers each as a switch **with its real
size attached**, so you see the cost before you pay it. One click builds,
one click removes.

!!! tip "The panel is honest about space"

    On a 40,000-entry dictionary: full-text is about **12 MB**, contains
    about **2.4 MB**, headword indexes about **2 MB** — and the prepared
    library is usually *smaller* than the file it came from. The panel
    shows each dictionary's numbers, not these.

## Results stream in

Dictionaries answer at their own speed, and the page never waits for the
slowest one. Each dictionary bursts open as its results arrive; the first
one with a match opens automatically. A **more…** link expands any single
dictionary — the whole entry, in the dictionary's own layout, its own
fonts, its own scripts. Nothing is reflowed, paraphrased, or prettied up.
The dictionary is shown the way the dictionary wanted to be shown.

## The shortcuts worth knowing

<kbd>/</kbd> focuses the search box — from anywhere on the page. The rest
are one glyph, no chord:

| Key | What it does |
|---|---|
| <kbd>/</kbd> | focus the search box |
| **double-click** any word in an article | look it up |
| <kbd>⊞</kbd> / <kbd>⊟</kbd> | open / close *every* dictionary's results at once |
| <kbd>⇔</kbd> | toggle a wide reading layout |
| <kbd>◐</kbd> | cycle light → dark → automatic theme |
| click any **speaker** | play the pronunciation — `.spx` transcoded on the fly |
| click a **cross-reference** | follow it inside the dictionary you're reading |

Cross-references are the quiet superpower of real dictionaries: link to
another headword, widen to all dictionaries only when the current one has
no such entry. Their spellings — `bword:`, `entry:`, `d:`, `x:` — are
all followed, because every repacker had a different idea of how to write
them.

## The ☰ panel

One panel does the housekeeping:

- **Reorder** dictionaries — drag the ⠿ handle, or nudge with ▲▼⏫⏬.
  Your order is remembered; streaming results arrive in it.
- **Enable / disable** each dictionary for *All*-searches, also remembered.
- **Provenance** — which source file each dictionary came from, and the
  library folder holding its prepared files. Click any path to copy it.
- **Folders & configuration** — the folders being scanned, the library
  location, the config file in effect, and **Rescan folders** when you
  added files behind the server's back.

## This page is a link

Every search is a URL: `?q=word&mode=exact&dict=all`. Bookmark it, share
it, hand it to a friend whose server is running your library — the page
opens on the result. There is no app state that cannot be mailed.

## Speex audio and you

Browsers cannot play Speex, the audio format old MDict dictionaries
prefer. WuWeiDict transcodes `.spx` to WAV **on the fly**, in-process on
the `-cgo` builds, and caches the result — the read never notices. On the
pure-Go build the same work goes to the `speexdec` utility
(`brew install speex`, `apt install speex`; the path is announced at
startup).

## Widen the search when one dictionary is not enough

Searching *All* answers with a few hits per dictionary and a **more…** per
block. The one-click <kbd>⊞</kbd> opens every dictionary at once — for
the current page only, never remembered, because that is how you ask
"who else says this?" without committing to it.

---

Your dictionaries, your layout, your shortcuts. That is the whole manual.