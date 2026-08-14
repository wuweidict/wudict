---
title: WuDict Hover
description: Look up any word on any page of your browser — hover, read, move on. The WuWeiDict extension for Chrome and Firefox.
tags:
  - Browser
status: new
---

# WuDict Hover - Chrome/Firefox extension

With the WuWeiDict extension for **Chrome and Firefox** you get a quick definition for any word in your browser — no need to copy the word and switch to another app to look it up.

The WuWeiDict server [must be running](run.md) for the extension to work.

## Install

=== "Chrome"

    **Chrome Web Store.**

    [Install from the Chrome Web Store](https://FIXME.TODO.com/chrome-webstore/wudict){ .md-button .md-button--primary }

    !!! warning "This link is a placeholder"

        `https://FIXME.TODO.com/…` is a stand-in while the store listing
        is being prepared. The day the extension ships, this button
        becomes the real listing. Found your way here before then? Manual
        install below works today.

    **Install via developer mode**

    ``` sh title="1. download the extension and unpack it"
    # the built extension bundle (source is in the repository)
    curl -L -o wudict-extension.zip https://FIXME.TODO.com/wudict-extension.zip
    unzip wudict-extension.zip -d wudict-extension
    ```

    1. Open `chrome://extensions`
    2. Enable the **Developer mode** switch
    3. Click **Load unpacked** and select the `wudict-extension` folder

    The extension is live, and stays live across browser restarts.

=== "Firefox"

    **Firefox Add-on**

    [Get it on Firefox Add-ons](https://FIXME.TODO.com/amo/wudict){ .md-button .md-button--primary }

    !!! warning "This link is a placeholder"

        `https://FIXME.TODO.com/…` names a slot, not a listing yet. When
        the add-on is published, this button points at the real page.

    **The developer way — install a preview build now.**

    ``` sh title="1. download the extension bundle (.xpi)"
    curl -L -o wudict-extension.xpi https://FIXME.TODO.com/wudict-extension.xpi
    ```

    1. Open `about:addons`
    2. Gear menu → **Install Add-on From File** → choose
       `wudict-extension.xpi`

    Temporary loading works too: `about:debugging#/runtime/this-firefox`
    → **Load Temporary Add-on** → pick the unpacked folder's
    `manifest.json`. Temporary add-ons vanish on restart — permanent
    installs do not.

## Use

Hover any word that looks unfamiliar. The popup shows the entry — the
way your dictionaries phrase it, not a machine paraphrase:

- **Exact lookups only.** The popup asks for the word itself, never for
  "everything starting with" it — hovering _run_ should not offer _runny_.
  One or two results per dictionary, enough to read, not a scroll marathon.
- **Audio plays** — Speex transcoded for you on the server.
- **"Open in WuWeiDict"** — the _full_ entry, in the dictionary's own
  styling, in your own full-page reader. One consistent rule: the popup
  is a glance; the app is the reading.
- **Cross-references stay out of the popup.** Clicking a link inside the
  popup opens the target in the real app, scoped to the dictionary the
  link came from. The popup never becomes a browser.

Everything above adapts in the **options page**: server host and port
(default `127.0.0.1:6888`), which dictionaries answer, popup size and
theme, and an optional **modifier key** (say, <kbd>Alt</kbd>) so hover
stays silent until you ask for it.

``` text title="Options — the defaults"
Server          http://127.0.0.1:6888      (set once, remembered)
Dictionaries    All enabled                 (or pick a short list)
Trigger         Hover, or Alt + hover
Payload         Clean HTML — structure only, no scripts (fast & safe)
Cache           500 lookups, per-word, in the background worker
```

## How it stays fast — and safe

Three engineering truths shaped the popup, all inherited from the
server's public contract:

- **One request per lookup.** The server answers several dictionaries in
  a single concurrent call, so the extension never fans out requests.
- **Reduced payloads.** The popup asks for _clean_ articles — markup
  reduced by half, every stylesheet and script request dropped. Reading a
  glance costs one small request, and repeat lookups never reach the
  network twice (worker cache).
- **The worker talks, the page doesn't.** Browser security forbids an
  ordinary page from calling your server — so the extension's background
  worker carries that permission and relays results to the popup. The
  server's loopback binding stays intact, and your dictionaries stay
  unexposed to the internet.

## Requirements

- **wuWeiDict running** on the machine the browser runs on — the
  extension reaches `127.0.0.1:6888`, nothing else.
- Modern Chrome or Firefox (Manifest V3). No other extensions required,
  and none of your browsing data requests is ever read.

!!! info inline end "Status"

    Preview builds circulate manually. The Chrome Web Store and Firefox
    Add-ons listings are in preparation; every `FIXME.TODO.com` link on
    this page becomes a live store page the day they are approved.

WuWeiDict reads your dictionaries. The extension carries that reading to
every page you visit — without ever leaving home.