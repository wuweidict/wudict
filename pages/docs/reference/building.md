---
title: Build from source
description: Build wudict yourself with Go, with or without a C compiler, including cross-compiled releases and the Android app.
---

# Build from source

**You need:** [Go](https://go.dev/doc/install).

For the faster `-cgo` flavour you also need a C compiler: Xcode Command Line
Tools on macOS, `build-essential` on Debian and Ubuntu, MSYS2 or MinGW on
Windows.

## Install the command

=== "With a C compiler"

    ``` sh title="cgo SQLite plus the built-in Speex decoder"
    go install -tags sqlite_fts5 github.com/wuweidict/wudict@latest
    ```

=== "Without a C compiler"

    ``` sh title="pure-Go SQLite"
    go install github.com/wuweidict/wudict@latest
    ```

Both commands produce a `wudict` that works. The tag chooses the SQLite engine
and the Speex decoder, nothing else.

On a machine without a C toolchain, the `-tags sqlite_fts5` command does not
fail. It falls back to the pure-Go driver.

## From a clone

The Makefile is the interface. Every action has a target.

``` sh title="the four you need"
make build     # native build, cgo flavour, into ./wudict
make install   # into GOBIN
make check     # tidy, vet and all tests
make help      # every target, with a description
```

Run `make check` before you propose a change.

## Release builds

``` sh title="every release platform from one machine"
make cross
```

This produces the pure-Go binaries for macOS, Linux and Windows into `dist/` -
the same set the automated builds produce.

``` sh title="platform packages"
make mac-app          # wuDict.app into dist/
make mac-app-install  # and into ~/Applications
make win-installer    # the Windows installer (needs Inno Setup 6.3 or newer)
```

## Android

``` sh title="the Go side, then the app"
make android-go     # cross-compile the server into the app (needs the Android NDK)
make apk            # build the APK (needs the Android SDK)
```

`make android-go` builds the cgo flavour, which needs the NDK. Without it, use
`make android-go-purego`. That fallback has no Speex audio on the device.

The Android app is a WebView shell that runs the same server binary. It adds no
Go code and no build tags.

### Two flavours

One source tree builds two APKs. They differ only in how a dictionary reaches
the device.

| Flavour | Storage | Built by |
| --- | --- | --- |
| `foss` | All-files access; reads *Internal storage ▸ Dictionaries* | `make apk`, `make apk-foss-release` |
| `play` | no storage permission; files are imported into the app's own folder | `make apk-play-debug`, `make apk-play-release` |

The released `wudict-android-arm64.apk` is the `foss` flavour, which is what
[the Android app](../apps/android.md) describes. `make apk-verify` asserts that
the `play` build still declares no storage permission.

## Verify

``` sh title="the version you just built"
./wudict --version
```
