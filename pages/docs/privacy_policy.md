---
title: Privacy Policy
description: wuDict collects nothing, sends nothing and has no account. This page states exactly what that means, and the one exception.
---

# Privacy Policy

**Effective 30 August 2026.**

This policy covers the **wuDict** Android app (`com.legbehindneck.wudict`) and
the `wudict` program it is built from, on every platform.

## The short version

**wuDict collects no data about you, and sends none anywhere.**

There is no account, no sign-in, no cloud, no analytics, no advertising, no
crash reporting and no third-party SDK of any kind. The app has no server to
talk to. Your dictionaries, your searches and your settings never leave your
device.

This is a property of how the program is built rather than a promise about how
we behave: wuDict runs a small web server *inside the app itself*, bound to
`127.0.0.1`, and shows its pages in a WebView. The `INTERNET` permission you see
in the Play listing exists so the app can reach that server on your own device.
It is the only permission this build declares.

## What the app stores, and where

Everything the app keeps is written to storage that belongs to the app and to no
one else:

| What | Where |
| --- | --- |
| Dictionary files you import | The app's own external files directory |
| The prepared search index built from them | The same place |
| Your settings and search history | The same place |

Nothing is encrypted or uploaded, because nothing leaves the device.

The app declares `allowBackup="false"`, so none of it is copied into Google's
Auto Backup either. **Uninstalling the app deletes all of it.** You can also
clear it at any time from Android's *Storage → Manage space* screen, which the
app implements itself.

## Permissions

This build declares exactly one permission:

- **`INTERNET`** — used solely to connect to the wuDict server running inside
  the app on `127.0.0.1`.

It declares **no storage permission at all**. Dictionaries are imported through
Android's system file picker (the Storage Access Framework), which gives the app
access to the one file you chose and nothing else. The app cannot read, list or
scan your storage.

It requests no location, no contacts, no camera, no microphone, no phone state
and no identifiers. It does not read the clipboard, and it does not use the
Advertising ID.

## The one exception: dictionary content

Dictionary files are authored by third parties, and an article inside one can
contain a link or a reference to something on the internet.

- **Links.** Tapping an external link hands it to your browser. wuDict does
  not open it and does not follow it.
- **Embedded remote resources.** If a dictionary's article references an image,
  font or script by an `http://` or `https://` address instead of bundling it,
  your device will request that address while the article is displayed, and the
  server at the other end will see your IP address the way any web request does.

This is the dictionary's doing, not the app's, and it is uncommon — a
self-contained dictionary file bundles its own media. If it matters to you, the
app works fully with the network turned off; only such embedded references will
fail to load.

## Data collected by Google Play

Distributing through Google Play means Google collects information about
installs, and receives crash and ANR reports from Android itself, under
[Google's own privacy policy](https://policies.google.com/privacy). We can see
that only as aggregate statistics in the Play Console — install counts, device
and country breakdowns, stack traces. It is not collected by the app, we cannot
connect it to you, and it does not include anything you looked up.

If you install the FOSS build from GitHub instead, none of this applies.

## Children

wuDict is a dictionary reader. It collects no data from anyone, of any age,
and contains no advertising and no in-app purchases.

## Your rights

Regulations such as the GDPR and the CCPA give you rights to access, correct,
export and delete the personal data a service holds about you. **We hold none.**
There is no account to close, no profile to export and no record to delete,
because none was ever created. Every byte the app produced is on your device and
under your control.

## Open source

wuDict is free software under the GPL-3.0-or-later. The claims on this page
are checkable: the source is public, and so is the build.

- Source: [github.com/wuweidict/wudict](https://github.com/wuweidict/wudict)

## Changes to this policy

If this policy ever changes, the effective date at the top changes with it and
the previous text stays in the repository's history. A change that affected what
the app collects would arrive with an app update, not quietly.

## Contact

Questions about this policy, or about anything on this page:

- [Open an issue](https://github.com/wuweidict/wudict/issues)
