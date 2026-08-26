---
title: Troubleshooting
description: Symptoms and their causes - missing dictionaries, port already taken, silent audio, slow first searches, disk space.
---

# Troubleshooting

Find your symptom. Each entry names the cause first.

## No dictionaries appear { #no-dictionaries }

Check these four in order.

1. **The folder is not the one WuWeiDict scans.** Startup prints the folder in
   effect. Compare it with where your files are.
2. **The file is not recognised as a dictionary.** A StarDict `.dict.dz` needs
   its `.ifo` file beside it. `.dsl.dz` counts as one whole extension.
   Subfolders are scanned, so depth is not the problem.
3. **You copied files in while WuWeiDict was running.** Open the ☰ panel and click
   **Rescan folders**. No restart is needed.
4. **Windows separators.** In the environment, `DICT_DIR` folders are separated
   by `;` on Windows, not `:`.

``` sh title="ask WuWeiDict what it sees"
wudict list ~/Dictionaries
```

## WuWeiDict refuses to start: the dictionary folder is the library folder

`DICT_DIR` and `DB_DIR` must be different folders. WuWeiDict stops rather than
scan its own output. Normally, you'll only need to set `DICT_DIR`, and let wuDict use its default `DB_DIR`.

Set one of them somewhere else. See
[`DB_DIR`](../reference/configuration.md#db_dir).

## Port 6888 is already in use { #port-taken }

Another program holds the port. Pick a different one.

``` sh title="this session"
wudict --port 9090
```

``` toml title="~/.wudict/wudict.toml, permanently"
SERVER_PORT = 9090
```

Then open `localhost:9090`. WuWeiDict prints a hint when it finds the port busy.

## No browser tab opens { #no-browser }

Open [localhost:6888](http://localhost:6888) yourself.

If the tab should never open, that is a setting, not a fault. See
[`NO_BROWSER`](../reference/configuration.md#no_browser).

On Windows, a double-clicked `wudict.exe` has no console. Look for its **tray
icon** in the notification area. Its log is
`%LOCALAPPDATA%\wudict\wudict.log`. On macOS, wuDict.app puts an icon in the
**menu bar** and logs to `~/Library/Logs/wudict.log`.

## Pronunciation stays silent { #audio }

Most `.mp3` or `.wav` files play directly. The problem is almost always
Speex.

Browsers cannot play `.spx`, so WuWeiDict converts it to WAV first. A `-cgo` build
converts inside the process. A `-purego` build needs the external program:

``` sh title="install the external decoder"
brew install speex     # macOS
apt install speex      # Debian and Ubuntu
```

Point at a specific program with `--speexdec`, or
[`SPEEXDEC`](../reference/configuration.md#speexdec). WuWeiDict prints which
decoder it resolved at startup, or an install hint when it found none.

One exception: a `.spx` file you placed in a `res/` folder is served as it is,
not converted. Supply `.mp3` or `.wav` there instead.

## An article looks broken, or its buttons do nothing

The dictionary might come with a faulty .js or .css file. Its own scripts usually load one library
first, so a single bad file disables everything interactive in every article.

WuWeiDict warns at startup when it serves a text file containing a NUL byte, and
names the path that would replace it.

[Replace the file](../dictionaries/override.md){ .md-button }

## An image or a sound is missing

Open your browser's network panel and look for a failing request to
`/res/<dictionary>/<name>`. A `404` there is the missing file, and `<name>` is
the path you must mirror in the dictionary's `res/` folder.

## The first searches are slow, and the machine is busy

WuWeiDict is preparing your dictionaries. That happens once per dictionary,
one at a time, in the background.

Preparing one dictionary uses a whole processor core. To let it use more, raise
[`INDEX_WORKERS`](../reference/configuration.md#index_workers). To stop
preparing entirely, set
[`AUTO_INDEX`](../reference/configuration.md#auto_index) to `off`.

## Some dictionaries report "not searched"

The search reached its memory cap and declined to open the rest. They are not
broken. Search the specific dictionary you need from the dropdown and it will work.

This memory limits off by default on the desktop, and on by default on android. See
[`SEARCH_MEMORY`](../reference/configuration.md#search_memory).

## Contains search misses a word it should find

Two possible causes.

- **The word is not a literal piece of the headword.** Contains matches your
  text exactly, ignoring case and accents. It does not correct spelling.
- **The index is old.** When the ☰ panel marks a dictionary's substring index
  as out of date, rebuild it there. It was built with older text folding and
  may miss words whose folded form changed.

## The library uses too much disk

The ☰ panel shows how much space each index takes, per dictionary, and every switch turns
off again.

``` sh title="find leftovers"
wudict clean        # list broken or interrupted items
wudict clean -f     # delete them
```

Removal is offered while the original dictionary file is still on disk. When
the original is gone, the prepared data is the only copy, and its switches
lock.

## The Android app shows no dictionaries { #android-no-dictionaries }

1. **Storage access was refused.** *Settings ▸ Apps ▸ wuDict ▸ Permissions*,
   or Android's *All files access* list. Turn it on, then reopen the app.
2. **The folder name is wrong.** It must be *Internal storage ▸ Dictionaries*,
   at the top level, not inside *Documents* or *Download*.
3. **The files arrived while the app ran.** Open ☰ and tap
   **♻️ Rescan folders**.

An SD card is not scanned. Copy the files to internal storage.

## The Android app pauses while it is not on screen

That is deliberate. With screen off on android the server indexes nothing and closes what it
can, in order to preserve battery life. Prepared dictionaries stay
open, so returning is instant.

Preparing a new dictionary therefore needs the app on screen. Leave it open the
first time you add a large dictionary. See
[the Android app](../apps/android.md).

## I edited wudict.toml and nothing changed

A command-line flag or an environment variable sets the same value, and both
will override the values in `wudict.toml`.

Check the current process: http://localhost:6888/api/config shows the effective settings
and where they came from. The setup page shows the same, and warns when a value
cannot be changed from the file.

[Priority rules](../reference/configuration.md#priority){ .md-button }

## Nothing above matches

Start WuWeiDict with `--verbose`. It then logs every request, dictionary open,
preparation step and audio conversion.

``` sh title="verbose logging"
wudict --verbose
```

Then [open an issue](https://github.com/wuweidict/wudict/issues) with that
output and your `wudict --version`.
