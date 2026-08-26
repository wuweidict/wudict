---
title: First run
description: Start wudict and configure folders with dictionaries.
---

# Quick start
1. Download wudict for your OS from https://github.com/wuweidict/wudict/releases/latest


**Goal:** Get wuDict running in your browser at
[localhost:6888](http://localhost:6888).

**You need:** a working `wudict` command, and a folder holding dictionary
files.

## 1. Start the server

``` sh title="start WuWeiDict"
wudict
```

WuWeiDict prints the address it listens on, the dictionary folder it scans, and
the config file in effect. Then it opens your browser.

To start a copy you built in the current folder, run `./wudict`.

## 2. Set the folders with dictionaries

wuDict looks for dictionaries under **`~/Dictionaries`** by default, including subfolders.

If the default folder is missing or empty, the browser opens the setup page at
[localhost:6888/setup](http://localhost:6888/setup) where you can set the custom folders with your dictionaries. 
Paste the folder path to your dictionaries folder (can contain `~`). The page validates the path while you type and
counts displays the number of total dictionaries found.

The setup page writes your choice to `~/.wudict/wudict.toml`. No restart is
needed. To open the setup page from the browser click the hamburger icon and then 
under the cogwheel icon ⚙️ pick  **✏️ Edit folders...**.

To set the folder yourself, pick one of these three ways.


=== "Config file (preferred)"

    ``` toml title="~/.wudict/wudict.toml"
    DICT_DIR = ["~/Dictionaries", "/Volumes/Data/Dicts"]
    ```

=== "Command line"

    ``` sh title="repeat the flag for more folders"
    wudict --dict-dir ~/Dictionaries --dict-dir /Volumes/Data/Dicts
    ```

=== "Environment variable"

    ``` sh title="separate folders with : - use ; on Windows"
    DICT_DIR="~/Dictionaries:/Volumes/Data/Dicts" wudict
    ```


A command-line flag overrides the env var, which in turn overrides the value in `wudict.toml`. If you
edit the file while a flag sets the same value, the setup page will indicate that an override is active.

[All 17 settings](../reference/configuration.md){ .md-button }

## 3. Verify

The page lists one section per dictionary. Type a word you know is in one of
them and press ++enter++.

From the terminal, check the same thing without a browser:

``` sh title="what WuWeiDict sees in a folder"
wudict list ~/Dictionaries
```

Each line is one dictionary that WuWeiDict can read.

## What happens next, on its own

The first search opens each dictionary through its own format. Exact and prefix
lookups work at once.

In the background, WuWeiDict then **prepares** each dictionary: it builds a
headword index of a few megabytes and stores it in the library folder. Prepared
dictionaries search faster and use far less memory.

Preparation runs one dictionary at a time by default, so it never takes the
machine away from you. Search keeps working the whole time.

[What preparation does](../dictionaries/library.md){ .md-button }

## If something went wrong

| What you see | Go to |
| --- | --- |
| No dictionaries listed | [Dictionaries do not appear](../help/troubleshooting.md#no-dictionaries) |
| `address already in use` | [Port 6888 is taken](../help/troubleshooting.md#port-taken) |
| No browser tab opened | [Nothing opens](../help/troubleshooting.md#no-browser) |
| Pronunciation stays silent | [Audio does not play](../help/troubleshooting.md#audio) |

## Next

[Search: the four modes and the shortcuts](search.md){ .md-button .md-button--primary }
