---
title: Home
description: wuDict searches every dictionary you own from your browser. One file to download, no account, no cloud, nothing leaves your machine.
---

# All your dictionaries, in your browser

**WuWeiDict** (the program is called `wudict`) searches every dictionary file you
own. It runs on your own computer and answers in your browser at
[localhost:6888](http://localhost:6888).

One file. No runtime, no database server, no account. Your dictionaries stay
on your disk.

[Install](start/install.md){ .md-button .md-button--primary }
[First run](start/first-run.md){ .md-button }
[Browser extension](extension.md){ .md-button }

---

## Supported dictionary formats

<div class="grid cards" markdown>

-   :fontawesome-solid-file-zipper:{ .lg .middle } **MDict**

    ---

    `.mdx` plus `.mdd`. The most common format. Articles are HTML, media
    travels in companion files.

-   :fontawesome-solid-book-bookmark:{ .lg .middle } **StarDict**

    ---

    `.ifo`, `.idx`, `.dict`. Synonym files, images and dictzip compression are
    all read in place.

-   :fontawesome-solid-layer-group:{ .lg .middle } **Aard 2**

    ---

    `.slob`. Single binary with text, images, audio, scripts and styling.

-   :fontawesome-solid-language:{ .lg .middle } **Lingvo DSL**

    ---

    `.dsl` and `.dsl.dz`. Legacy, still used in Eastern Europe.

-   :fontawesome-solid-earth-americas:{ .lg .middle } **Babylon**

    ---

    `.bgl`. Character sets are detected automatically, resources are read from
    inside the file.

-   :fontawesome-solid-box-archive:{ .lg .middle } **wuDict library**

    ---

    `text.db`. wuDict's own SQLite-based format: one folder per dictionary, which you can
    copy across machines.

</div>

[All format details](dictionaries/formats.md){ .md-button }

---

## How you search

wuDict searches all your dictionaries at once. Results are streamed as soon as they are available, so you get the first hit
while the other dictionaries are still being searched.

| Mode | Results | Available |
| --- | --- | --- |
| **Exact** | the headword itself, ignoring case and accents - `corazon` finds `corazón` | always |
| **Prefix** | every headword that starts with your text | always |
| **Contains** | your text anywhere inside a headword | on-demand per dictionary |
| **Full-text** | words inside the article text, ranked by relevance | on-demand per dictionary |

Exact and prefix work instantly. Contains and full-text need an extra index which can be 
enabled on a per dictionary basis. 
The ☰ panel offers each one as a switch and shows what it costs in megabytes first.

A search that finds nothing is retried with the word's dictionary form — *knew*
finds **know**, *estuviera* finds **estar**. English is built in; other
languages are a small file you install with one click.

[How search works](start/search.md){ .md-button }

---

## Where it runs

| | |
| --- | --- |
| **macOS** | a command, or [wuDict.app](apps/macos.md) with a menu-bar icon |
| **Windows** | a command, or [an installer](apps/windows.md) with a tray icon |
| **Linux** | a command, and a systemd user unit for [startup](running.md) |
| **Android** | [an app](apps/android.md) that looks a word up from inside any other app |

---

## What it does not do

- It does not send anything anywhere. The server listens on the loopback
  address only.
- It has no account, no telemetry, no analytics and no crash reporting.
