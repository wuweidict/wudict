---
title: Install
description: Get WuWeiDict (wudict) running on macOS, Windows, Linux or Android — one file, no dependencies — or build it from source with Go.
tags:
  - Quickstart
---

# Installing on your OS

WuWeiDict ships as **one executable per platform** — no installers, no
runtimes, no dependencies, nothing to uninstall. Download, rename, run.

Every platform below starts from [the releases page](https://github.com/wuweidict/wudict/releases "Latest binaries and the Android APK — always on GitHub Releases").

=== "macOS"

    Pick the file for your chip. Apple Silicon (`M1`–`M4`) is `arm64`;
    older Intel Macs are `amd64`.

    ``` sh title="Apple Silicon (arm64)" hl_lines="1"
    # 1. download wudict-darwin-arm64-cgo — or the -purego file, either works
    # 2. make it executable and put it where the shell can find it
    chmod +x ~/Downloads/wudict-darwin-arm64-cgo
    mv ~/Downloads/wudict-darwin-arm64-cgo /usr/local/bin/wudict

    # 3. prove it — you should see a short usage summary
    wudict --help
    ```

    ``` sh title="Intel (amd64)"
    chmod +x ~/Downloads/wudict-darwin-amd64-cgo
    mv ~/Downloads/wudict-darwin-amd64-cgo /usr/local/bin/wudict
    wudict --help
    ```

    The first launch opens the system _Gatekeeper_ prompt; click **Open**
    the one time it asks.

    **Or skip the terminal entirely.** The same release carries
    `wudict-macos-app.zip`, which unzips to **wuDict.app** — the identical
    binary in a bundle, universal, so there is no chip to pick. Drag it to
    *Applications* and open it. It puts a **menu-bar icon** up instead of a
    Dock icon, and that icon is the whole interface: it tells you the server
    is running, opens it in the browser, rescans your dictionaries and quits
    it. Nothing is printed anywhere — its log is `~/Library/Logs/wudict.log`.
    From a clone of the source, `make mac-app-install` builds and installs the
    same bundle into `~/Applications`.

=== "Windows"

    Download `wudict-windows-amd64-purego.exe`, rename it to `wudict.exe`,
    and run it from wherever you keep it — a simple folder works fine.

    ``` pwsh title="Windows PowerShell" hl_lines="1"
    # rename the downloaded file
    Rename-Item ~/Downloads/wudict-windows-amd64-purego.exe wudict.exe

    # run it right from that folder, or add the folder to PATH once:
    #   setx PATH "$env:PATH;C:\tools\wudict"
    .\wudict.exe --help
    ```

    Windows Defender may pause on the first run of an unsigned binary;
    choose **More info → Run anyway** once.

    **Or use the installer.** The release includes
    `wudict-windows-x64-setup-<version>.exe`. The setup wizard offers to install
    **for all users** (the default, needs administrator
    permissions), or **for current user only**, which keeps
    everything in the user profile. Install options include: a desktop shortcut, 
    *start at sign-in*, adding `wudict`
    to your `PATH`, and adding wuDict in **Open with** for `.mdx`, `.dsl`,
    `.slob` and `.bgl` files. For an unattended install, `/ALLUSERS` or `/CURRENTUSER` picks the
    mode and skips that page. From the repository root `make win-installer`
    builds it (needs [Inno Setup 6.3+](https://jrsoftware.org/isinfo.php)).

    There is **one** `wudict.exe`, and it behaves differently depending on how
    you start it. When started from PowerShell or `cmd`, it behaves as an
    ordinary command-line program: it prints, it pipes, it returns an exit
    code. When double-clicked, or started from a shortcut or by opening a
    dictionary file, it detaches from the console window and adds a
    **tray icon** in the notification area. Its log then goes to
    `%LOCALAPPDATA%\wudict\wudict.log`.

=== "Linux"

    For Debian/Ubuntu and the like:

    ``` sh title="Linux (amd64)" hl_lines="1"
    chmod +x ~/Downloads/wudict-linux-amd64-purego
    sudo mv ~/Downloads/wudict-linux-amd64-purego /usr/local/bin/wudict
    wudict --help
    ```

    ``` sh title="Linux (Raspberry Pi)"
    # Pi 3/4/5 → wudict-linux-arm64-purego
    # Pi 1/2/Zero  → wudict-linux-arm-v7-purego  (v6 for the first Pis)
    chmod +x ~/Downloads/wudict-linux-arm64-purego
    sudo mv ~/Downloads/wudict-linux-arm64-purego /usr/local/bin/wudict
    wudict --help
    ```

=== "Android"

    Download **`wudict-android-arm64.apk`** from the releases page and
    install it with your file manager (approve _install unknown apps_). 
    It is a dependency-free WebView shell with the
    server compiled in — it starts the server and shows the app in one.

!!! tip "Two flavours on the releases page"

    Every desktop platform offers a **`-cgo`** and a **`-purego`** binary,
    and both are full products — they differ only in two internals:

    |               | `-cgo`                 | `-purego`               |
    | ------------- | ---------------------- | ----------------------- |
    | SQLite driver | mattn (C, fastest)     | modernc (pure Go)       |
    | `.spx` audio  | decoded **in-process** | via external `speexdec` |
    | C toolchain   | —                      | —                       |

    The recommenced flavour is the `-cgo` build when it exists for your platform (macOS and
    Linux have it). The `-purego` variant is a fallback that does not use C-compiled extensions 
    — it offers the same functionality, but with slightly slower SQLite3 layer (purego)
    and without the native `.spx` audio decoding,  so you'll need the external `speexdec` utility to be installed (`brew install
    speex`, `apt install speex`).

---

## Usage

Once you downloaded `wudict` see:

[How to use](run.md){ .md-button .md-button--primary }

---

## Build from source

You need [Go](https://go.dev/doc/install) and, for the fastest flavour, a
C compiler (Xcode Command Line Tools on macOS, `build-essential` on
Debian/Ubuntu, MSYS2/MinGW on Windows).

=== "With a C compiler — recommended"

    ``` sh title="go install — cgo sqlite + in-process speex" hl_lines="1"
    go install -tags sqlite_fts5 github.com/wuweidict/wudict@latest
    ```

    That is the same recipe as `make build`. From a clone of the
    repository, the Makefile does the rest:

    ``` sh
    make build    # native build → ./wudict
    make install  # into GOBIN
    make check    # tidy + vet + all tests
    make help     # every available target
    ```

=== "Without a C compiler"

    ``` sh title="go install — pure-Go sqlite"
    go install github.com/wuweidict/wudict@latest
    ```

    Both commands produce the same `wudict`. If you pass the `-tags
    sqlite_fts5` recipe on a machine without a C toolchain, the build
    quietly falls back to the pure-Go driver instead of failing — the tag
    only chooses the SQLite engine.

=== "Cross-platform release builds"

    ``` sh
    make cross   # every release platform, from one machine
    ```

    Produces the pure-Go binaries for macOS, Linux and Windows — the same
    set the CI builds. To build Android app use `make android-go` +
    `make apk` (see the [Android guide](service.md#android-nothing-to-run)).

## What "no dependencies" means

`wudict --help`, then `wudict` — and the app answers at
[localhost:6888](http://localhost:6888). There is no database server to configure.
No Node, no Python, no plugins. The only thing `wudict` needs on your machine is
a spare port and a folder to watch.