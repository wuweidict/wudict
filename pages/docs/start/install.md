---
title: Install
description: Download and run WuWeiDict on macOS, Linux, Windows or Android. One executable, no dependencies.
---

# Install

WuWeiDict is one executable per platform. It needs no runtime, no database
server and (almost) zero configuration. Download the file, make it executable, start it.

Optionally, for macOS you can download the macOS bundle, for Windows an install wizard is available.

[Download page](https://github.com/wuweidict/wudict/releases/latest){ .md-button .md-button--primary }

## Pick your system

=== "macOS"

    Apple Silicon Macs (M1 to M4) use `arm64`. Older Intel Macs use `amd64`.

    ``` sh title="Apple Silicon (arm64)"
    # Assuming file was downloaded to ~/Downloads

    chmod +x ~/Downloads/wudict-darwin-arm64-cgo
    mv ~/Downloads/wudict-darwin-arm64-cgo /usr/local/bin/wudict
    wudict
    ```

    ``` sh title="Intel (amd64)"
    chmod +x ~/Downloads/wudict-darwin-amd64-cgo
    mv ~/Downloads/wudict-darwin-amd64-cgo /usr/local/bin/wudict
    wudict
    ```

    macOS shows a Gatekeeper warning the first time. Click **Open** once.

    ??? tip "Looking for a macOS app bundle?"

        The same release contains `wudict-macos-app.zip`. It unzips to
        **wuDict.app**: the identical server, built universal, with a menu-bar
        icon instead of a terminal. See [the macOS app](../apps/macos.md).

=== "Linux"

    ``` sh title="Linux (amd64)"
    chmod +x wudict-linux-amd64-cgo
    sudo mv wudict-linux-amd64-cgo /usr/local/bin/wudict
    wudict
    ```

    ``` sh title="Raspberry Pi"
    # Pi 3, 4 and 5      -> wudict-linux-arm64-cgo
    # Pi 1, 2 and Zero   -> wudict-linux-arm-v7-purego  (v6 for the first models)
    chmod +x wudict-linux-arm64-cgo
    sudo mv wudict-linux-arm64-cgo /usr/local/bin/wudict
    wudict
    ```

=== "Windows"
    ??? tip "Prefer an installer?"

        The release includes `wudict-windows-x64-setup-<version>.exe`. It adds a
        Start-menu entry, an uninstaller, `PATH`, and *Open with* for dictionary
        files. See [the Windows installer](../apps/windows.md).

    ``` cmd title="Windows CMD"
    ren wudict-windows-amd64-cgo.exe wudict.exe
    
    # show help
    wudict.exe --help

    # run it
    wudict.exe
    ```
    ``` pwsh title="Windows PowerShell"
    Rename-Item wudict-windows-amd64-cgo.exe wudict.exe
    wudict.exe
    ```

    Any folder works. On Arm-based Windows PCs take
    `wudict-windows-arm64-purego.exe` instead.

    Windows Defender stops an unsigned program on its first run. Choose
    **More info**, then **Run anyway**, once.


    ??? info "One executable, two behaviours"

        There is one `wudict.exe`, and how you start it decides what it does.

        Started from PowerShell or `cmd`, it is an ordinary command-line
        program: it prints, it pipes, it returns an exit code.

        Double-clicked, started from a shortcut, or started by opening a
        dictionary file, it detaches from the console and adds a **tray icon**
        in the notification area. In this scenario instead of the stdout it logs to
        `%LOCALAPPDATA%\wudict\wudict.log`.

=== "Android"

    Download **`wudict-android-arm64.apk`** and install it with your file
    manager. Android asks you to allow *install unknown apps* once.

    The app then asks for storage access, and reads your dictionaries from
    *Internal storage ▸ Dictionaries*. It can also look up a word you selected
    in any other app.

    [The Android app: storage, lookup, battery](../apps/android.md){ .md-button }

## Command line help

``` sh title="wudict cli help"
wudict --help
```

## Verify

``` sh title="the version, then the server"
wudict --version
wudict
```

`wudict` prints the address it listens on and opens your browser at
[localhost:6888](http://localhost:6888).

Windows users who double-click the file see the tray icon instead of the
printed lines. Android users see the app window.

## Which flavour to download

Most downloads come as a **`-cgo`** file and a **`-purego`** file. Both are
complete products. They differ in two internals only.

| | `-cgo`                                      | `-purego` |
| --- |---------------------------------------------| --- |
| SQLite driver | mattn, written in C, fastest                | modernc, pure Go |
| `.spx` audio | built-in native decoding | needs the external `speexdec` program |

Take **`-cgo`** where it exists: macOS, Linux on amd64 and arm64, and Windows
on x64. Take **`-purego`** for the rest — 32-bit Raspberry Pi boards, and
Windows on Arm. `-purego` searches slightly slower and needs `speexdec`
installed for Speex audio (`brew install speex`, or `apt install speex`).

A C compiler is only needed to
[build the `-cgo` flavour yourself](../reference/building.md).

## Update

Download the new file and put it over the old one. Stop the server first on
Windows, which refuses to replace a running program.

Nothing else changes: your config file, your prepared library and your
dictionary files stay where they are, and the new binary reads them.

The packaged apps update themselves the same way — a new
[installer](../apps/windows.md) over the old install, a new
[`wuDict.app`](../apps/macos.md) over the old app, a new
[APK](../apps/android.md) over the old app.

## Uninstall

Delete the executable. To remove all the data and configurations delete
`~/.wudict` — the config file, the state file and the indexed library.

> Next

[Set the dictionary folders](first-run.md){ .md-button .md-button--primary }
