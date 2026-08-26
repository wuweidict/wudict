---
title: WuDict Hover chrome/firefox extension
description: Hover any word in chrome/firefox to get definition from the wuDict server.
status: new
---

# Browser extension

With the wuDict extension you can quickly get a definition for any word in your browser 
by hovering (with an optional key) or via the right-click context menu - no need to copy-paste words into another app.

The extension needs the wuDict server to be running on the same computer or another computer in your local network.

!!! info "Not published yet"

    The Chrome Web Store and Firefox Add-ons listings are in preparation.
    Preview builds circulate directly. This page describes how the extension
    behaves and how to configure it.

## Preconditions

- **wuDict must be running** on the same machine as the browser. The extension talks to
  `127.0.0.1:6888`. You can also configure it to use a wudict server on another computer in your local network, and then set the IP address in the extension settings.
- **Chrome or Firefox**, current version, Manifest V3.


The server must be running for the popup to answer.
[Keep it running](running.md){ .md-button }

## Install a build by hand

=== "Chrome"

    1. Unzip the extension folder.
    2. Open `chrome://extensions`.
    3. Switch **Developer mode** on.
    4. Click **Load unpacked** and select the folder.

    The extension stays installed across browser restarts.

=== "Firefox"

    1. Open `about:addons`.
    2. Use the gear menu, then **Install Add-on From File**.
    3. Choose the `.xpi` file.

    A temporary install also works: open
    `about:debugging#/runtime/this-firefox`, click **Load Temporary Add-on**,
    and pick `manifest.json` inside the unpacked folder. Temporary add-ons
    disappear when Firefox restarts.

## Use it

Hover a word. The popup shows the entry, worded by your dictionaries.

- **Exact lookups only.** The popup asks for the word itself, never for every
  word starting with it. Hovering *run* does not offer *runny*. You get one or
  two results per dictionary.
- **Audio plays** in the popup. Speex is converted by the server first.
- **Open in WuWeiDict** shows the full entry in the dictionary's own styling, in
  the main page. The popup is a glance; the app is for reading.
- **Cross-references leave the popup.** Clicking a link inside the popup opens
  the target in the main page, in the dictionary the link came from.

## Options

The options page controls all of it.

``` text title="the defaults"
Server          http://127.0.0.1:6888      set once, remembered
Dictionaries    all enabled                 or pick a short list
Trigger         hover, or a modifier key plus hover
Payload         clean HTML: structure only, no scripts
Cache           500 lookups, held by the background worker
```

Set a modifier key, for example <kbd>Alt</kbd>, and the popup stays silent
until you ask for it.

## Why it is fast, and why it is safe

**One request per lookup.** The server searches several dictionaries in a
single call, so the extension never sends one request per dictionary.

**Small payloads.** The popup asks for `clean` articles. That form is about
half the size of the original markup and drops every stylesheet and script
request. Repeated lookups are answered from the worker's cache and never reach
the network twice.

**The worker talks, the page does not.** A browser forbids an ordinary web page
from calling a server on your machine. The extension's background worker calls
it instead, under the extension's own identity, and passes the result to the
popup. The page you are reading never addresses your machine, so your browser
never asks whether *that site* may reach your local network.

WuWeiDict answers browser extensions on three read-only endpoints only:
`/api/dicts`, `/api/search` and `/res/`. Web pages get nothing. Settings,
preferences and the library are unreachable this way.

To allow named extensions only, list their origins in
[`BROWSER_EXTENSIONS`](reference/configuration.md#browser_extensions).

??? question "The popup says WuWeiDict is not answering extensions"

    Your WuWeiDict is older than this feature. Download the current release and
    replace the binary.

    [Install](start/install.md)
