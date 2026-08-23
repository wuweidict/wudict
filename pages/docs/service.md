---
title: Background service
description: Keep wudict always running — the macOS LaunchAgent, the Linux systemd user unit, and the Windows startup route.
---

# Run it in the background

Reading is quicker when the dictionary is always there. All three desktop
systems have a home for a service like this, and wudict's Makefile
covers them. These setups are optional — the plain `wudict` command is
the product; this page is the convenience.

## macOS — a LaunchAgent

The following `Makefile` goals can be used for managing the wudict `launchctl` LaunchAgent:

| Command | What it does                                      |
|---|---------------------------------------------------|
| `make mac-agent-install` | generate the plist, register the agent            |
| `make mac-agent-start` | run `launchctl bootstrap gui/$UID <plist>`            |
| `make mac-agent-stop` | run `launchctl bootout gui/$UID/com.legbehindneck.wudict` |
| `make mac-agent-restart` | rebuild the binary, then `kickstart -k`           |
| `make mac-agent-status` | run `launchctl print gui/$UID/<label>`                |
| `make mac-agent-uninstall` | stop it and delete the plist                      |

The agent launches when you log in, and `wudict` search is available the moment your
desktop is up.

### Running `wudict` as a macOS app bundle

`make mac-app-install` builds **wuDict.app** into `~/Applications` (the
release carries a prebuilt universal copy as `wudict-macos-app.zip`). It is
the same server, started by double-clicking instead of by launchd, and it
shows a **menu-bar icon** — running/not running, open, rescan, quit — where
the agent shows nothing at all.

Using the agent is recommended if
you want to install it once and always have `wudict` running in the background. Use the app if you want to run `wudict` on demand and be able to stop it via the tray status icon.

## Linux — a systemd user unit

Copying the `wudict` binary to `/usr/local/bin` needs `sudo`, while the service runs with user
permissions, and starting/stopping the service only needs regular user permissions:

| Command | What it does |
|---|---|
| `make linux-service-install` | install `/usr/local/bin/wudict` (sudo) + create the user systemd unit |
| `make linux-service-start` | run `systemctl --user enable --now wudict.service` |
| `make linux-service-stop` | disable + stop |
| `make linux-service-restart` | rebuild, reinstall `wudict`, restart |
| `make linux-service-status` | display the unit's status |
| `make linux-service-uninstall` | remove the unit (keeps the binary) |

The unit expects the binary at `/usr/local/bin/wudict`. To keep the
service alive after you log out:

```sh
sudo loginctl enable-linger "$(id -un)"
```

Install the `wudict` binary, without the systemd unit: `make linux-install`
(`PREFIX=/opt/foo` relocates it).

## Windows

The wudict setup wizard for Windows has an Autostart check box that you can enable during the installation — this will place the launcher in the **Startup** folder..

By hand, if you did not use the installer:

1. Move `wudict.exe` to a folder of your choice, e.g. `C:\tools\wudict\`
2. `Win+R` → `shell:startup` → 
3. Drop a shortcut to `wudict.exe` there (append the `--no-browser` flag to its
   command line if you do not want a new browser tab to appear at every boot)

When launched by the system or from Explorer `wudict.exe` shows a **tray icon**
and the log is written to
`%LOCALAPPDATA%\wudict\wudict.log`.
