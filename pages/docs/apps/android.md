---
title: Android app
description: Install WuWeiDict on Android, point it at your Dictionaries folder, and look up a selected word from inside any other app.
---

# Android app

<style>
.md-typeset .badges{display:flex;flex-wrap:wrap;gap:.6rem;align-items:center;justify-content:center}
.md-typeset .badges img{height:80px;width:auto;max-width:none}
</style>

<div class="badges">

<a href="https://github.com/wuweidict/wudict/releases/latest"><img src="https://raw.githubusercontent.com/Kunzisoft/Github-badge/master/get-it-on-github.png" alt="Get it on GitHub" height="80"></a> <a href="https://apps.obtainium.imranr.dev/redirect?r=obtainium://app/%7B%22id%22:%22com.legbehindneck.wudict%22,%22url%22:%22https://github.com/wuweidict/wudict%22,%22author%22:%22wuweidict%22,%22name%22:%22wudict%22%7D"><img src="https://raw.githubusercontent.com/ImranR98/Obtainium/main/assets/graphics/badge_obtainium.png" alt="Get it on Obtainium" height="80"></a> <a href="https://play.google.com/store/apps/details?id=com.legbehindneck.wudict"><img src="https://play.google.com/intl/en_us/badges/static/images/badges/en_badge_web_generic.png" alt="Get it on Google Play" height="80"></a>

<!--  <img src="https://gitlab.com/IzzyOnDroid/repo/-/raw/master/assets/IzzyOnDroid.png" alt="Get it on IzzyOnDroid" height="80">](https://apt.izzysoft.de/fdroid/index/apk/com.legbehindneck.wudict) [](https://play.google.com/store/apps/details?id=com.legbehindneck.wudict)
-->
<!--
[![F-Droid](https://img.shields.io/f-droid/v/com.legbehindneck.wudict?logo=FDROID)](https://f-droid.org/en/packages/com.legbehindneck.wudict/)
-->
</div>

Search your `.mdx` `.slob` `.bgl` `.dsl` `.ifo` `.zim` dictionaries on the phone.

The app is a small window around the same server the desktop runs. There is no
account, no upload and no network traffic: the server listens on the phone's
own loopback address, and the window reads from there.

**You need** Android 8.0 or newer on a 64-bit (`arm64`) device. Every phone
sold since about 2017 should be able to run wudict.

## Install

1.  Download **`wudict-android-arm64.apk`** from
    [the releases page](https://github.com/wuweidict/wudict/releases).
2.  Open the file in your file manager or in the download notification.
3.  Android asks once to allow *install unknown apps* for that file manager.
    Allow it, then confirm the install.

## Grant the storage access (wudict FOSS version only)

The app asks for storage access on first run. Choose <kbd>**Grant access**</kbd>,
then turn on <kbd>**All files access**</kbd> on the next screen.

The app reads dictionaries from a folder that is not its own — *Internal
storage ▸ Dictionaries* — and Android requires a special 
permission in this case (the google play flavour uses [SAF](https://developer.android.com/guide/topics/providers/document-provider) instead).

Choosing **Later** is safe. The server still starts, and reports an empty
dictionary folder. The app asks again the next time you open it.

## Copy your dictionaries (Android FOSS version only)

Put the dictionary files, for example, in **Internal storage ▸ Dictionaries**. Use a file manager, a USB
cable, or `adb push`.

Subfolders are read too so you can organize the folders according to your needs.

**IMPORTANT**: The folder structure can be used for lemmatization — for dictionary formats that do not specify the headword language
and which do not contain a valid language prefix in their file name, such as e.g. `es-fr-larousse.slob`, a parent folder
such as `es` can serve as a language hint for lemmatization.

Copy every part of a dictionary, not only the main file:

| Format | Copy                                                          |
| --- |---------------------------------------------------------------|
| MDX | `*.mdx`, and `*.mdd` if there is one                          |
| StarDict | `*.ifo`, `*.dict` (or `.dict.dz`), `*.idx`, `*.syn`           |
| Slob | `*.slob`                                                      |
| DSL | `*.dsl` (or `.dsl.dz`), and the `*.files.zip` if there is one |
| BGL | `*.bgl`                                                       |

Open the app, open the <kbd>**☰**</kbd> panel and tap <kbd>**♻️ Rescan folders**</kbd>. New
dictionaries appear in the list.

**Verify:** the dictionary list in the <kbd>☰</kbd> panel names your files..

## Look up a word from another app

You do not have to switch apps to read a definition. Three ways in, all
producing the same floating window over what you were reading:

-   **Select the word you want to look up** — the selection toolbar should have a <kbd>**wuDict**</kbd> entry, next
    to *Copy* and *Translate*.
-   **Share the selection.** Use <kbd>**Share**</kbd> → <kbd>**wuDict**</kbd> when an app hides the
    toolbar, or when the passage spans several paragraphs.
-   **Open a `wudict://lookup?q=word` link.** For automation apps, note apps
    and scripts.

Back, or a tap outside the window, returns to the original screen. The window is not
kept in the recents list.

``` sh title="from Termux or Tasker"
am start -a android.intent.action.VIEW -d "wudict://lookup?q=phubbing"
```

## Where the app keeps its files

| What | Where |
| --- | --- |
| Your dictionaries | *Internal storage ▸ Dictionaries* |
| Config file, prepared library | `Android/data/com.legbehindneck.wudict/files` |

The second folder is app-owned. It survives updates and is deleted when you
uninstall the app; your *Dictionaries* folder stays intact.

??? info "Reaching the app folder to edit `wudict.toml`"

    Android 11 and newer hide `Android/data` from other file managers. Two
    routes still work:

    -   a USB cable, with the phone set to *File transfer*;
    -   `adb pull` and `adb push` over USB debugging.

    Most settings do not require adb. The <kbd>☰</kbd> panel writes what a phone user normally
    changes, and the full key list is in
    [Configuration](../reference/configuration.md).

## Removing dictionaries

Click or tap <kbd>**☰**</kbd>, find the dictionary, and tap its **file row** (e.g. `Oxford.mdx`) to expand it —
this will reveal <kbd>**🗑 Remove…**</kbd>. The panel
shows how much disk space the dictionary takes on your phone. There is no way to undo the <kbd>Delete all</kbd> action! — see
[Removing a dictionary](../dictionaries/library.md#removing-a-dictionary) for details.


## Battery and memory

The app is built to preserve battery and minimize resource usage.

-   Off screen, the server uses one core instead of all of them.
-   [`MEMORY_LIMIT`](../reference/configuration.md#memory_limit) is set on
    Android - a sixteenth of the device's RAM, between 192 MB and 384 MB - and
    unset on a desktop, where the machine manages its own memory.
-   [`PREVIEW_MEMORY`](../reference/configuration.md#preview_memory), what
    dictionaries that are not yet prepared may hold open between searches, is a
    third of that (**64-128 MB**) against 1 GB on a desktop.
-   [`MORPH_CACHE`](../reference/configuration.md#morph_cache) is **1** on
    Android against 2 on a desktop: one language of
    [word-form data](../start/search.md#inflected-words) is held at a time, and
    a second language displaces it rather than adding to it.
-   The keyboard hides as soon as you scroll an article, which gives the
    definition the full screen.


## What is different from the desktop

| | Desktop                                                  | Android |
| --- |----------------------------------------------------------| --- |
| Dictionary folder | anywhere you choose                                      | *Internal storage ▸ Dictionaries* |
| Speex `.spx` audio | works                                                    | works in the released build |
| Browser extension | yes                                                      | no; the selection toolbar replaces it |
| Command line | yes                                                      | no |
| Word forms for other languages | the <kbd>🔤 Lemmatization</kbd> page, or `wudict lemmas` | the page only |

## Next

[Search: search types](../start/search.md){ .md-button }
