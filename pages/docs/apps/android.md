---
title: Android app
description: wuDict for Android - the two builds, adding dictionaries, looking up a word from another app, settings, battery and memory.
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

wuDict searches `.mdx`, `.ifo`, `.slob`, `.dsl`, `.bgl`, `.zim` and `.wudict.md`
dictionaries on the phone. The app runs the wudict server on the phone's
loopback address and shows its pages; network use is listed in the
[Privacy Policy](../privacy_policy.md#network-requests).

Requires Android 8.0 or later on a 64-bit ARM (`arm64`) device.

## Builds

| | FOSS build (GitHub, Obtainium) | Google Play build |
| --- | --- | --- |
| Dictionary folder | *Internal storage → Dictionaries* | none: dictionaries are imported into the app |
| Storage permission | *All files access* | none |
| Reachable from other devices (testing) | optional, in Settings | no |

## Install the FOSS build

1.  Download **`wudict-android-arm64-foss-<version>.apk`** from
    [the releases page](https://github.com/wuweidict/wudict/releases).
2.  Open it from the file manager or the download notification. Android asks
    once to allow *Install unknown apps* for that app.
3.  On first start, choose <kbd>Grant access</kbd>, then turn on
    <kbd>All files access</kbd>. With <kbd>Later</kbd> the app starts without
    dictionaries and asks again on the next start.

## Add dictionaries

### FOSS build

Copy the dictionary files to *Internal storage → Dictionaries* with a file
manager, over USB, or with `adb push`. Subfolders are read. Then
<kbd>☰</kbd> → folder summary → <kbd>Rescan folders</kbd>.

Copy every file of a dictionary:

| Format | Files |
| --- | --- |
| MDict | `*.mdx`, and `*.mdd` if present |
| StarDict | `*.ifo`, `*.idx`, `*.dict` or `*.dict.dz`, `*.syn` if present |
| Aard 2 | `*.slob` |
| Lingvo DSL | `*.dsl` or `*.dsl.dz`, and `*.dsl.files.zip` if present |
| Babylon | `*.bgl` |
| ZIM | `*.zim`, or every part of a split archive: `*.zimaa`, `*.zimab`, … |

A subfolder named by a language code, such as `es/`, sets the language of the
dictionaries in it, which lemmatization needs when a dictionary declares none
([language detection](../reference/configuration.md#lemmatization)).

### Google Play build

On first start, <kbd>Import dictionaries</kbd> → <kbd>Choose folder</kbd>
copies a folder's dictionaries into the app, then offers to delete the
originals. Dictionaries in `Android/data/com.legbehindneck.wudict/files/Dictionaries`
on a microSD card are read without importing.

### Both builds

Share a dictionary file, an archive or a link to wuDict, or open one with
wuDict: the **Add dictionaries** dialog lists what it contains and installs the
ones you tick. The setup page's **Add dictionaries** does the same for a file
or a pasted link.

## Look up a word from another app

-   **Text selection menu**: select a word; the menu shows **wuDict** next to
    *Copy*.
-   **Share menu**: <kbd>Share</kbd> → <kbd>wuDict</kbd>, for apps without the
    selection menu entry or for a longer passage.
-   **A reading app's dictionary button**: see the table below.
-   **A `wudict://lookup?q=word` link**: from automation apps, note apps and
    scripts.

The lookup opens in a floating window. Back, or a tap outside it, closes it;
it is not listed in Recents. Settings → *Look up in the full app* opens the
full app instead, per source.

``` sh title="from Termux or Tasker"
am start -a android.intent.action.VIEW -d "wudict://lookup?q=phubbing"
```

The link also takes `mode=exact|prefix|fts|wildcard`, `dict=<name>`,
and `full=1` (full app) or `full=0` (floating window). In a wildcard query,
write `?` as `%3F`.

## Reading apps

| Reader | What to choose |
| --- | --- |
| **Moon+ Reader** | *ColorDict3* (listed as ColorDict/BlueDict/GoldenDict), *Lingvo*, *Fora* or *YunCi*; or *Customized*, with the URL `wudict://lookup?q=%s`; or wuDict from its list of installed apps |
| **ReadEra** | wuDict, from its list of dictionary and translator apps |
| **Librera Reader** | wuDict, from its dictionary list; it is listed more than once, and any of the entries works |
| **FBReader** | *ColorDict 3*, *ABBYY Lingvo* or *Dictan* |
| **KnownReader** | *ColorDict new / GoldenDict (minicard)* for the popup, *ColorDict new / GoldenDict* for the full app; *Aard 2* and *Dictan* also work, even when listed as not installed |
| **Prestigio eReader** | *ColorDict* or *ABBYY Lingvo*, then wuDict |
| **Readest, Book's Story, Lithium, Aldiko** | wuDict; these list the apps of the selection menu |
| **KOReader** | see below |
| **CoolReader** | *ColorDict new / GoldenDict*, *Aard 2 Dictionary* or *Dictan* |
| **Kindle, Google Play Books, Kobo** | none: they offer no outside dictionary |

If the app a reader names (GoldenDict, Aard2, Lingvo, Fora, QuickDic, YunCi,
Dictan) is also installed, Android asks which app to use the first time;
choose wuDict and *Always*.

**KOReader** reads its list of outside dictionaries from a file you can
replace. Create `koreader/dictionaries.lua` in internal storage:

``` lua title="koreader/dictionaries.lua"
return {
    { "wudict", "wuDict", false, "com.legbehindneck.wudict", "search" },
}
```

The file replaces KOReader's built-in list, so add back any dictionary app you
still use. Then, in KOReader: *Dictionary settings* → check *Use external
dictionary* → *Dictionary: wuDict*.

## Settings

Long-press the app icon → **Settings**.

| Section | Settings |
| --- | --- |
| Look up in the full app | per source: text selection menu, share menu, a reading app's dictionary button, links and automation |
| Screen | what fills the screen edges (margin colour), which system bars hide while you read |
| Access | *Only this app may change your dictionaries*: other apps on the phone can look words up but not import, index, remove or change settings |
| Advanced | article compression, memory one search may use, memory for dictionaries not indexed, dictionaries indexed at once, server port; FOSS build: *Reachable from other devices (testing only)* |

## Files

| File types                                           | Location                                      |
|------------------------------------------------------|-----------------------------------------------|
| Dictionaries, FOSS build                             | *Internal storage → Dictionaries*             |
| Imported dictionaries, settings, library, lemma data | `Android/data/com.legbehindneck.wudict/files` |

Uninstalling the app deletes `Android/data/com.legbehindneck.wudict`;
*Internal storage → Dictionaries* remains. Android 11 and later hide
`Android/data` from other file managers: reach it over USB (*File transfer*)
or with `adb pull` and `adb push`. The dictionary panel and Settings cover the
usual settings, so `wudict.toml` rarely needs editing; all keys are in
[Configuration](../reference/configuration.md).

## Remove dictionaries

<kbd>☰</kbd> → the dictionary's file row (e.g. `Oxford.mdx`) →
<kbd>🗑 Remove…</kbd>. The file row shows the disk space the dictionary uses.
See [Remove a dictionary](../dictionaries/library.md#remove-a-dictionary).

## Battery and memory

-   While the app is not visible, the server uses one CPU core, starts no new
    indexing, and closes the dictionaries it can reopen; indexed dictionaries
    stay open. Indexing, downloads and rebuilds already running continue in a
    foreground service with a notification.
-   Android memory defaults are lower than on a desktop:
    [`MEMORY_LIMIT`](../reference/configuration.md#memory_limit) is 1/16 of the
    device's RAM (192–384 MiB),
    [`PREVIEW_MEMORY`](../reference/configuration.md#preview_memory) a third of
    it, [`SEARCH_MEMORY`](../reference/configuration.md#search_memory) equal to
    it, and [`MORPH_CACHE`](../reference/configuration.md#morph_cache) `1`.
-   The keyboard hides when you scroll an article.

## Differences from the desktop

| | Desktop | Android |
| --- | --- | --- |
| Dictionary folders | any, set on the setup page | FOSS: *Internal storage → Dictionaries*; Play: imported |
| Speex `.spx` audio | `-cgo` builds; `-purego` with `speexdec` | built in |
| Browser extension | yes | no; the text selection menu replaces it |
| Command line | yes | no |
| Lemma data | Lemmatization page or `wudict lemmas` | Lemmatization page |

## Next

[Search](../start/search.md){ .md-button }
