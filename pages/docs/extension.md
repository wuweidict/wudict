---
title: WuDict Hover chrome/firefox extension
description: Hover any word in chrome/firefox to get definition from the wuDict server.
status: new
---
<style>
.md-typeset .badges{display:flex;flex-wrap:wrap;gap:.6rem;align-items:center;justify-content:center}
.md-typeset .badges img{height:80px;width:auto;max-width:none}
</style>
# wuDict Hover Chrome/Firefox Extension


## Install

Click a badge below to install:

<div class="badges">
  <a href="https://chromewebstore.google.com/detail/bknaaoffefipfnpefmkbipcdemljbhjh">
    <img alt="Available in the Chrome Web Store" height="80" src="assets/chrome-web-store-badge.png" />
  </a>
  <a href="https://addons.mozilla.org/firefox/addon/wudict-hover/">
    <img alt="Get the Add-on for Firefox" height="80" src="assets/firefox-get-the-addon.svg" />
  </a>
</div>

---

With the wuDict Hover extension you can quickly get a definition for any word in Chrome/Firefox 
by hovering (with an optional key) or via the right-click context menu — no need to copy-paste words into another app.

The extension needs the [wuDict](index.md) server to be running on localhost or another computer in your local network.

<!--
!!! info "Not published yet"

    The Chrome Web Store and Firefox Add-ons listings are in preparation.
    Preview builds circulate directly. This page describes how the extension
    behaves and how to configure it.
-->
## Preconditions

- a running **wuDict** server, by default `127.0.0.1:6888`. You can also configure the wudict server to run 
on another computer in your local network, and then set the IP address in the extension settings.
- **Chrome or Firefox**, current version, Manifest V3.


Without a running wudDict server the browser extension cannot work.
[Keep it running](running.md){ .md-button }

## Install the wuDict Hover extension in development mode 

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

## Using wuDict Hover

Hover a word while holding the <kbd>Alt</kbd> key, or without a key, depending on what you set in <kbd>Options</kbd>. 
The definitions from wuDict will show in a popup below your mouse.

- **Exact lookups only.** The popup asks for the word itself, never for every
  word starting with it. Hovering *run* will not return *running*. You get one or
  two results per dictionary.
- **Audio plays** in the popup when available.
- **Look up selection in wuDict** is an alternative way to show definitions. Whether the results open in the 
popup or in the main wuDict page is controlled by <kbd>Options</kbd> > <kbd>Options</kbd> > <kbd>Search opens</kbd>: popup/full wudict page. 
- **Cross-references leave the popup.** Clicking a link inside the popup opens
  the target in the main wuDict page.

## Options

wuDict Hover options

``` text title="the defaults"
Server          http://127.0.0.1:6888      set once, remembered
Dictionaries    all enabled                 or pick a short list
Trigger         hover, or a modifier key plus hover
Payload         clean HTML: structure only, no scripts
Cache           500 lookups, held by the background worker
```

Set a modifier key, for example <kbd>Alt</kbd>, and the popup stays silent
until you ask for it.

## wuDict Hover Source Code Repository

- https://github.com/wuweidict/wudict-browser-extension

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
`/api/dicts`, `/api/search` and `/res/`. Settings, preferences and the library
are unreachable this way.

To allow named extensions only, list their origins in
[`BROWSER_EXTENSIONS`](reference/configuration.md#browser_extensions).

Web pages get nothing here. A page of your own can be allowed the same three
endpoints by naming its origin in
[`WEB_ORIGINS`](reference/configuration.md#web_origins), which is unset by
default.

??? question "The popup says WuWeiDict is not answering extensions"

    Your WuWeiDict is older than this feature. Download the current release and
    replace the binary.

    [Install](start/install.md)
