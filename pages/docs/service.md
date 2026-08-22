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

The Makefile generates a `launchctl` LaunchAgent from
`launchctl/com.legbehindneck.wudict.in` and manages it for you:

| Command | What it does                                      |
|---|---------------------------------------------------|
| `make mac-agent-install` | generate the plist, register the agent            |
| `make mac-agent-start` | `launchctl bootstrap gui/$UID <plist>`            |
| `make mac-agent-stop` | `launchctl bootout gui/$UID/com.legbehindneck.wudict` |
| `make mac-agent-restart` | rebuild the binary, then `kickstart -k`           |
| `make mac-agent-status` | `launchctl print gui/$UID/<label>`                |
| `make mac-agent-uninstall` | stop it and delete the plist                      |

The agent launches when you log in. Search is available the moment the
desktop is.

### …or the app, if you'd rather see it

`make mac-app-install` builds **wuDict.app** into `~/Applications` (the
release carries a prebuilt universal copy as `wudict-macos-app.zip`). It is
the same server, started by double-clicking instead of by launchd, and it
shows a **menu-bar icon** — running/not running, open, rescan, quit — where
the agent shows nothing at all.

The two are alternatives, not layers: both bind the same port, so the second
one to start just opens the browser at the first and exits — so if the agent
already holds the port, the app opens a tab and quits without ever showing an
icon. Pick the agent if
you want it up before you ask for it; pick the app if you want to see it and
be able to stop it.

## Linux — a systemd user unit

Only the binary install needs `sudo`; the service runs with your
permissions, and start/stop never escalate:

| Command | What it does |
|---|---|
| `make linux-service-install` | install `/usr/local/bin/wudict` (sudo), write the user unit |
| `make linux-service-start` | `systemctl --user enable --now wudict.service` |
| `make linux-service-stop` | disable + stop |
| `make linux-service-restart` | rebuild, reinstall, restart |
| `make linux-service-status` | the unit's state |
| `make linux-service-uninstall` | remove the unit (keeps the binary) |

The unit expects the binary at `/usr/local/bin/wudict`. To keep the
service alive after you log out:

```sh
sudo loginctl enable-linger "$(id -un)"
```

Just the binary, without the unit: `make linux-install`
(`PREFIX=/opt/foo` relocates it).

## Windows — the honest path

No launchd or systemd here; the dependable pattern is the **Startup
folder**. The installer offers it as a checkbox — *Start wuDict when I
sign in* — and that is all the checkbox does: it drops a shortcut there,
so you can remove it later without a tool.

By hand, if you did not use the installer:

1. Put `wudict.exe` wherever you keep it, e.g. `C:\tools\wudict\`
2. `Win+R` → `shell:startup` → 
3. Drop a shortcut to `wudict.exe` there (add `--no-browser` to its
   command line unless you want a tab at every sign-in)

One logon later the dictionary wall is up, silently — and *silently* is
literal: started from a shortcut rather than a terminal, `wudict.exe`
closes the console window Windows gave it and shows a **tray icon**
instead, so there is no black box left on your desktop and still
something to click when you want to stop it. Its log goes to
`%LOCALAPPDATA%\wudict\wudict.log`.

Task Scheduler does the same with more ceremony and a "run at startup"
trigger.

## Android — nothing to run

The APK starts the server itself when the app opens. There is no
background service to configure; the app *is* the server, sleeping when
you do. Rebuilding the APK from source is the usual Makefile business:

```sh title="from a clone of the repository"
make android-go          # compile the server → android/app/src/main/jniLibs/
make apk                 # assemble + sign the APK → android/app/build/outputs/
```

`make android-go-purego` is the NDK-less fallback. CI signs via
repository secrets, so your local `make apk` produces either a debug APK
or a release one already configured for your own keystore.

---

One process, one port, your dictionaries. Start it once; forget it the
way you forget a library.