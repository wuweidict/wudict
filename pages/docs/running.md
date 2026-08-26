---
title: Run at startup
description: Start WuWeiDict automatically when you log in - a macOS LaunchAgent, a Linux systemd user unit, or the Windows Startup folder.
---

# Run at startup

**Goal:** WuWeiDict answers at [localhost:6888](http://localhost:6888) from the
moment you log in, without you starting it.

All this is optional and is just convenience to have `wudict` running as service even after you reboot,
so that you don't need to start it manually every time you need it.

=== "macOS"

    ## LaunchAgent

    macOS starts a LaunchAgent when you log in. The Makefile in a source clone
    generates and manages it.

    | Command | What it does |
    | --- | --- |
    | `make mac-agent-install` | generate the plist and register the agent |
    | `make mac-agent-start` | start it now |
    | `make mac-agent-stop` | stop it |
    | `make mac-agent-restart` | rebuild the binary, then restart |
    | `make mac-agent-status` | show its state, including the process id |
    | `make mac-agent-uninstall` | stop it and delete the plist |

    ### Verify

    ``` sh title="the agent's state"
    make mac-agent-status
    ```

    Then open [localhost:6888](http://localhost:6888).

    ## Or use the app instead

    **wuDict.app** does the same job on demand. It shows a menu-bar icon that
    reports whether the server runs, opens it, rescans dictionaries and quits
    it. The LaunchAgent shows nothing.

    Choose the agent to have WuWeiDict always there. Choose the app to start and
    stop it yourself.

    [The macOS app](apps/macos.md){ .md-button }

=== "Linux"

    ## systemd user unit

    The service runs as your user. Only the binary needs `sudo`, because it
    goes to `/usr/local/bin`.

    | Command | What it does |
    | --- | --- |
    | `make linux-service-install` | install the binary with sudo, then create the user unit |
    | `make linux-service-start` | enable and start it now |
    | `make linux-service-stop` | disable and stop it |
    | `make linux-service-restart` | rebuild, reinstall, restart |
    | `make linux-service-status` | show the unit's state |
    | `make linux-service-uninstall` | remove the unit and keep the binary |

    The unit expects the binary at `/usr/local/bin/wudict`.

    To install only the binary, run `make linux-install`. Set
    `PREFIX=/opt/foo` to put it elsewhere.

    ### Keep it running after you log out

    ``` sh title="allow your services to outlive the session"
    sudo loginctl enable-linger "$(id -un)"
    ```

    ### Verify

    ``` sh title="state now, logs live"
    make linux-service-status
    journalctl --user -u wudict -f
    ```

=== "Windows"

    ## The installer does it

    The setup wizard has a **start at sign-in** option. Switch it on during
    installation and it places the launcher in your **Startup** folder, with
    `--no-browser` already set. See
    [the Windows installer](apps/windows.md).

    ## By hand

    1. Put `wudict.exe` in a folder of your choice, for example
       `C:\tools\wudict\`.
    2. Press <kbd>Win</kbd>+<kbd>R</kbd> and enter `shell:startup`.
    3. Put a shortcut to `wudict.exe` in the folder that opens.

    Add `--no-browser` to the shortcut's command line if you do not want a
    browser tab at every sign-in.

    ### What you get

    Started by Windows or from Explorer, `wudict.exe` shows a **tray icon** in
    the notification area instead of a console window with log messages. It logs to
    `%LOCALAPPDATA%\wudict\wudict.log`.

    ### Verify

    Sign out and back in, then open
    [localhost:6888](http://localhost:6888).

## Useful settings for a background server

| Setting | Why |
| --- | --- |
| [`NO_BROWSER`](reference/configuration.md#no_browser) | do not open a browser tab at every start |
| [`TRAY`](reference/configuration.md#tray) | force the tray or menu-bar icon on or off |
| [`VERBOSE`](reference/configuration.md#verbose) | log requests and dictionary opens while you diagnose |
