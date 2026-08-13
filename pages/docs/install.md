---
title: Install
description: Get WuWeiDict (wudict) running on macOS, Windows, Linux or Android — one file, no dependencies — or build it from source with Go.
tags:
  - Quickstart
---

# Installing on your OS

WuWeiDict ships as **one executable per platform** — no installers, no
runtimes, no dependencies, nothing to uninstall. Download, rename, run.

Every platform below starts from [the releases page](https://github.com/legbehindneck/wudict/releases "Latest binaries and the Android APK — always on GitHub Releases").

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

=== "Linux"

    One downloaded file degrades to nothing. For Debian/Ubuntu and friends:

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

    Grab **`wudict-android-arm64.apk`** from the releases page and
    install it with your file manager (approve _install unknown apps_ for
    that one source once). It is a dependency-free WebView shell with the
    server compiled in — it starts the server and shows the app in one.

!!! tip "Two flavours on the releases page"

    Every desktop platform offers a **`-cgo`** and a **`-purego`** binary,
    and both are full products — they differ only in two internals:

    |               | `-cgo`                 | `-purego`               |
    | ------------- | ---------------------- | ----------------------- |
    | SQLite driver | mattn (C, fastest)     | modernc (pure Go)       |
    | `.spx` audio  | decoded **in-process** | via external `speexdec` |
    | C toolchain   | —                      | —                       |

    Take the `-cgo` build when it exists for your platform (macOS and
    Linux have it). Take `-purego` when it does not, or when you would
    rather keep everything pure Go — it runs the same, and still plays
    `.spx` audio when the `speexdec` utility is installed (`brew install
    speex`, `apt install speex`).

---

## Usage

You have the binary. The next page takes you from **run** to _first
dictionary found_ in under a minute:

[Run it in 60 seconds](run.md){ .md-button .md-button--primary }

---

## Build from source

You need [Go](https://go.dev/doc/install) and, for the fastest flavour, a
C compiler (Xcode Command Line Tools on macOS, `build-essential` on
Debian/Ubuntu, MSYS2/MinGW on Windows).

=== "With a C compiler — recommended"

    ``` sh title="go install — cgo sqlite + in-process speex" hl_lines="1"
    go install -tags sqlite_fts5 github.com/legbehindneck/wudict@latest
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
    go install github.com/legbehindneck/wudict@latest
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
    set the CI builds. The Android app builds with `make android-go` +
    `make apk` (see the [Android guide](service.md#android-nothing-to-run)).

## What "no dependencies" means

`wudict --help`, then `wudict` — and the app answers at
[localhost:6888](http://localhost:6888). No database server to configure.
No Node, no Python, no plugins. The only thing it asks of your machine is
a spare port and a folder to watch.