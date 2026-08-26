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

    `.slob`. Images, audio and styles are stored inside the one file.

-   :fontawesome-solid-language:{ .lg .middle } **Lingvo DSL**

    ---

    `.dsl` and `.dsl.dz`. Old, and still widely used in Eastern Europe.

-   :fontawesome-solid-earth-americas:{ .lg .middle } **Babylon**

    ---

    `.bgl`. Character sets are detected automatically, resources are read from
    inside the file.

-   :fontawesome-solid-box-archive:{ .lg .middle } **wuDict library**

    ---

    `text.db`. wuDict's own SQLite3-based format: one folder per dictionary, which you can
    copy share.

</div>

[All format details](dictionaries/formats.md){ .md-button }

---

## How you search

wuDict searches all your dictionaries at once. Results appear per dictionary as
each one answers, so you read the first hit before the slowest dictionary has
finished.

| Mode | It finds | Available |
| --- | --- | --- |
| **Exact** | the headword itself, ignoring case and accents - `corazon` finds `corazón` | always |
| **Prefix** | every headword that starts with your text | always |
| **Contains** | your text anywhere inside a headword | switch per dictionary |
| **Full-text** | words inside the article text, ranked by relevance | switch per dictionary |

Exact and prefix work the moment a dictionary is found. Contains and full-text
need an extra index. The ☰ panel offers each one as a switch and shows what it
costs in megabytes first.

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
- It does not change your dictionary files. It only reads them.
