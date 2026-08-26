---
title: macOS app
description: wuDict.app is native macOS app bundle which gives you a menu-bar icon, no Dock icon, no terminal.
---

# macOS app

**Goal:** run WuWeiDict from the Applications folder, without a terminal.

`wuDict.app` is the same server as the command-line build, packed into a macOS app bundle. Unlike the CLI version, it adds a **menu-bar icon**, so you can see when it is running and quit via the icon.

## Install

1.  Download **`wudict-macos-app.zip`** from
    [the releases page](https://github.com/wuweidict/wudict/releases).
    It is universal — one file for Apple Silicon and Intel.
2.  Unzip it. Drag **wuDict.app** to *Applications*.
3.  Control-click the app and choose **Open**. Confirm **Open** in the dialog.
4.  If you get a warning that Apple cannot verify this app, and you should move it to trash, 
    then retry step 3 after removing the extended attribute quarantine bits:

```sh  title="stripping macOS extended attributes"
/usr/bin/xattr -cr /Applications/wudict.app

# or if you copied the bundle to user's Application folder:
/usr/bin/xattr -cr ~/Applications/wudict.app
```

The app is signed with an ad-hoc certificate, so macOS treats it as
software from an unidentified developer. A normal double-click is refused, and
the Control-click menu is the workaround.

**Verify:** the menu bar gains a small dictionary icon, and your browser opens
at [localhost:6888](http://localhost:6888).

## The menu-bar icon

The icon is the whole interface. Click it:

| Entry | Does |
| --- | --- |
| *wuDict `<version>`* | nothing; it tells you the server runs, and which build |
| **Open wuDict** | opens the page in your browser |
| **Rescan dictionaries** | re-reads your dictionary folders, for files added since |
| **Open dictionary folder** | reveals that folder in Finder |
| **Quit wuDict** | stops the server |

There is no Dock icon and no window of its own. Closing the browser tab leaves
the server running; **Quit** is what stops it.

The app prints nothing. Its log is `~/Library/Logs/wudict.log`.

## Opening it a second time

Opening the app while it already runs does not start a second server. It opens
the page in your browser and exits.

## App, or LaunchAgent?

Both keep WuWeiDict available. They differ in when it runs.

| | `wuDict.app` | LaunchAgent |
| --- | --- | --- |
| Starts | when you open it | at every login |
| Visible | menu-bar icon | nothing |
| Stops | **Quit** | `make mac-agent-stop`, or logout |

Use the app if you want a visible switch. Use
[a LaunchAgent](../running.md) if you want the server to always be available even after reboot. 
Do not use both: the second one to start finds the port taken.

## Using the same binary from a terminal

The real executable sits inside the bundle, so a shell can call it:

``` sh title="the app's own binary, from the command line"
/Applications/wuDict.app/Contents/MacOS/wudict --version
/Applications/wuDict.app/Contents/MacOS/wudict lookup serendipity
```

Started that way it behaves as the command-line build: it prints, and it shows
no menu-bar icon. The icon appears only when macOS launches the bundle.

## Uninstall

1.  **Quit wuDict** from the menu bar.
2.  Move the app to the Bin.
3.  Optional: delete `~/.wudict` (config, and the prepared library unless you
    moved it) and `~/Library/Logs/wudict.log`.

## Next

[Quick Start: Configure WuWeiDict dictionary folders](../start/first-run.md){ .md-button }
