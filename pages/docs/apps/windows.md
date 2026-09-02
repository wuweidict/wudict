---
title: Windows installer
description: What the WuWeiDict installer puts on a Windows PC - install modes, the four optional tasks, the tray icon, silent install and uninstall.
---

# Windows installer

**Goal:** install WuWeiDict on Windows without deciding where to put a file.

The installer is optional. It carries the same single `wudict.exe` you can
download on its own; what it adds is a Start-menu entry, an uninstaller, and
four choices you would otherwise make by hand.

**You need** 64-bit Windows on an x64 processor. The installer refuses to run
on 32-bit Windows and on Arm64 Windows 10, because that binary cannot execute
there. For Arm, use the `wudict-windows-arm64-purego.exe` build.

## Install

1.  Download **`wudict-windows-x64-setup-<version>.exe`** from
    [the releases page](https://github.com/wuweidict/wudict/releases).
2.  Run it. Windows shows *Windows protected your PC*, because the file is not
    signed. Choose **More info**, then **Run anyway**.
3.  Choose an install mode:

    | Mode | Needs | Installs to |
    | --- | --- | --- |
    | **For all users** (preselected) | administrator rights | `C:\Program Files\wuDict` |
    | **For me only** | nothing | `%LOCALAPPDATA%\Programs\wuDict` |

4.  Choose which of the four extras you want (next section).
5.  Finish. Tick **Start wuDict now** to launch it.

**Verify:** the browser opens at [localhost:6888](http://localhost:6888), and a
**wuDict** icon appears in the notification area.

## The four optional tasks

| Task | Default | Effect |
| --- | --- | --- |
| Create a desktop shortcut | off | an icon on the desktop |
| Start wuDict at sign-in | off | a Startup entry that runs it with `--no-browser` |
| Add wuDict to my `PATH` | **on** | `wudict` works in PowerShell and `cmd` |
| Offer wuDict in *Open with* | **on** | for `.mdx`, `.dsl`, `.slob`, `.bgl` and `.zim` files |

Every one of them is reversible. Re-run the installer, or clear it by hand; the
uninstaller removes all four.

!!! tip "Start at sign-in is the whole autostart story on Windows"

    Ticking that box does what the manual Startup-folder shortcut in
    [Run at startup](../running.md) does. Do one or the other, not both.

## One executable, two behaviours

There is one `wudict.exe`, and how you start it decides what it does.

Started from PowerShell or `cmd`, it is an ordinary command-line program. It
prints, it pipes, and it returns an exit code.

Started from the Start menu, a shortcut, the desktop icon, or by opening a
dictionary file, it detaches from the console and shows a **tray icon** in the
notification area instead. Its log then goes to
`%LOCALAPPDATA%\wudict\wudict.log`.

The tray icon opens the page, rescans your dictionary folders, opens that
folder in Explorer, and quits the server.

## Upgrading

Run the newer installer over the old one. It keeps the install mode you chose
the first time, and it asks Windows to close a running `wudict.exe` rather than
failing on a file-in-use error. It does not start the server again afterwards.

Your dictionaries, your config file and the prepared library are untouched by
an upgrade.

## Silent install

``` pwsh title="unattended, for all users"
.\wudict-windows-x64-setup-1.0.0.exe /ALLUSERS /VERYSILENT /NORESTART
```

``` pwsh title="unattended, current user only"
.\wudict-windows-x64-setup-1.0.0.exe /CURRENTUSER /VERYSILENT /NORESTART
```

`/ALLUSERS` and `/CURRENTUSER` skip the mode page. A silent install does not
launch the server at the end.

## Uninstall

Use *Settings ▸ Apps ▸ Installed apps ▸ wuDict ▸ Uninstall*.

It removes the program, the shortcuts, the `PATH` entry and the *Open with*
associations.

It leaves your data: the dictionary files, `wudict.toml`, the prepared library
and the log. Delete `%USERPROFILE%\.wudict` yourself if you want those gone
too.

## Next

[Quick Start: Configure WuWeiDict dictionary folders](../start/first-run.md){ .md-button }
