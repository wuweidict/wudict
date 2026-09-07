---
title: WuDict Hover chrome/firefox extension
description: Hover any word in chrome/firefox for instant definitions from the wuDict server.
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


## Preconditions

- a running **wuDict** server, by default `127.0.0.1:6888`. You can also configure the wudict server to run 
on another computer in your local network, and then set the IP address in the extension settings.
- **Chrome or Firefox**, current version, Manifest V3.


Without a running wudDict server the browser extension cannot work.
[Keep it running](running.md){ .md-button }

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

## How does it work?

**Small payloads.** The popup asks for `clean` articles. That form is about
half the size of the original markup and drops every stylesheet and script
request. Repeated lookups are answered from the worker's cache and never reach
the network twice.

**The worker** Modern browsers do not allow ordinary web pages
to access a server running on your machine. The extension uses a service worker 
to talk to the wudict server, and passes the result to the
popup.

The wudict server only exposes three endpoints to browser extensions:
`/api/dicts`, `/api/search` and `/res/`. Settings, preferences and the library
are unreachable this way.

To allow named extensions only, list their origins in
[`BROWSER_EXTENSIONS`](reference/configuration.md#browser_extensions).

Regular web pages are now allowed to access the `wudict` server, but you can explicitly 
whitelist the origins that you control and trust via
[`WEB_ORIGINS`](reference/configuration.md#web_origins).

??? question "The popup says WuWeiDict is not answering extensions"

    Your wudict server might be older than this feature. Download the current release and
    replace the binary.

    [Install](start/install.md)

## Install the wuDict Hover extension in development mode

!!! info "Note"

    The steps below only apply to local development and debugging, or when you want to get the latest unpublished
    features that have not yet been published in the Chrome/Firefox stores.


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
