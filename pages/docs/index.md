---
title: Home
description: wuDict — instant search in MDict (.mdx/.mdd), AARD2 (.slob), Stardict (.ifo), Babylon (.bgl), ZIM (.zim) dictionaries in your browser.
---

# All your dictionaries, in your browser

**WuWeiDict** (the program is called `wudict`) searches all your local dictionaries using your browser. 
It runs on at [localhost:6888](http://localhost:6888).

`wudict` is a single standalone executable file. Native, no dependencies. 

[Install](start/install.md){ .md-button .md-button--primary }
[Quick Start](start/first-run.md){ .md-button }
[Chrome/Firefox Extension](extension.md){ .md-button }

---

## Supported dictionary formats

<div class="grid cards" markdown>

-   :fontawesome-solid-file-zipper:{ .lg .middle } **MDict**

    ---

    `.mdx` plus `.mdd`. A widely used format, with articles in HTML, and resources (audio, .css, .js) in .mdd packs.

-   :fontawesome-solid-book-bookmark:{ .lg .middle } **StarDict**

    ---

    `.ifo`, `.idx`, `.dict`. Synonym files, images and dictzip compression are
    all read in place.

-   :fontawesome-solid-layer-group:{ .lg .middle } **Aard 2**

    ---

    `.slob`. Single binary with text, images, audio, scripts and styling.

-   :fontawesome-solid-language:{ .lg .middle } **Lingvo DSL**

    ---

    `.dsl` and `.dsl.dz`. Legacy, still common in Eastern Europe.

-   :fontawesome-solid-earth-americas:{ .lg .middle } **Babylon**

    ---

    `.bgl`. Character sets are detected automatically, resources are read from
    inside the file.

-   :fontawesome-solid-globe:{ .lg .middle } **ZIM**

    ---

    `.zim`. Kiwix and Wikimedia archives, searched straight from the file.

-   :fontawesome-solid-box-archive:{ .lg .middle } **wuDict library**

    ---

    `text.db`. wuDict's own SQLite-based format: one folder per dictionary with optional `media.db`, portable across machines.

</div>

[All format details](dictionaries/formats.md){ .md-button }

---

## How you search

`wuDict` searches all your dictionaries at once. Results are streamed as soon as they are available, so you get the first hit
while the other dictionaries are still being searched.

| Mode | Results | Available |
| --- | --- | --- |
| **Exact** | the headword itself, ignoring case and accents - `corazon` finds `corazón` | always |
| **Prefix** | every headword that starts with your text | always |
| **Contains** | your text anywhere inside a headword | on-demand per dictionary |
| **Full-text** | words inside the article text, ranked by relevance | on-demand per dictionary |

`exact` and `prefix` modes work instantly. Contains and full-text need an extra index which can be 
enabled on a per-dictionary basis. 
The <kbd>☰</kbd> panel offers each one as a switch and shows the estimated index size, e.g. `42MB`.

Lemmatization (morphology) allows `wudict` to find *understand*
when you search for **understood**, searching for *estuviera* will land on **estar**. English is built in; other
languages can be installed via <kbd>☰</kbd> → <kbd>⚙️</kbd>  → <kbd>Lemmatization…</kbd> (see also [Lemmas](reference/configuration/#lemmatization))

[How search works](start/search.md){ .md-button }

---

## Supported Platforms

| |                                                                                                     |
| --- |-----------------------------------------------------------------------------------------------------|
| **macOS** | CLI and macOS app bundle [wuDict.app](apps/macos.md) with a menu-bar icon                           |
| **Windows** | CLI and [GUI Inno Installer](apps/windows.md) with a tray icon                                      |
| **Linux** | CLI with optional systemd unit for [startup](running.md)                                            |
| **Android** | [Android APK](apps/android.md) with extra intents for Share, text selection context menus, and more |

---

## Privacy

- No data is sent anywhere, no account, no cookies, no profiling, no telemetry, no analytics and no crash reporting.
