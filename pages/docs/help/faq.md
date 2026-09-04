---
title: FAQ
description: Questions people ask about privacy, disk space, moving a library, updating, and using WuWeiDict without a browser.
---

# FAQ

Questions, not symptoms. For a symptom, see
[troubleshooting](troubleshooting.md).

??? question "Is any of my data sent anywhere?"

    No. WuWeiDict binds to the loopback address `127.0.0.1` on one port. It has no
    account system, no telemetry, no analytics and no crash reporting.

    Your dictionaries are read from your disk, indexed on your disk, and served
    to your own browser.

??? question "Why two names, WuWeiDict and wudict?"

    *Wú wéi* (無為) is the classical Chinese term for effortless action, or no action, depending on whom you ask. 
    The project took this name, because good tools should not get in the way (no pun intended).

    The program is called `wudict` because you type it more often than you read
    it.

??? question "Where is everything on disk?"

    ``` text title="the default layout"
    ~/.wudict/
      wudict.toml     your settings; created and commented on first run
      state.json      dictionary order and on/off switches
      db/             the library: one folder per prepared dictionary
      spxcache/       converted Speex audio
    ~/Dictionaries    the default dictionary folder - change as needed in wudict.toml 
    ```

    Windows and Android put the same tree elsewhere. See
    [every file WuWeiDict owns](../reference/configuration.md#every-file-wudict-owns).

??? question "Can I move my library to another machine?"

    Yes. One dictionary is one folder under `~/.wudict/db`. Copy or zip the
    folder and put it in the other machine's library folder.

    `text.db` carries all the search. Media either travels with the folder in
    `media.db`, or falls back to the original file when one is present.

    [More about the library](../dictionaries/library.md)

??? question "How do I update WuWeiDict?"

    Download the new binary and replace the old one. That is the whole
    procedure. For windows, download the wudict setup wizard and rerun it.

    Your prepared libraries stay. WuWeiDict checks what each database supports
    when it opens it, so an older `text.db` keeps working, without the newest
    index until you rebuild it.

    `wudict --version` prints the version you are running.

??? question "Can I take back the disk space an index uses?"

    Yes. Every switch in the ☰ panel turns off again, and shows the size in
    megabytes before you click.

    Removal is offered while the original dictionary file is still on disk,
    because that is what makes it reversible. A dictionary whose original file
    is gone keeps its prepared data - that data is now the only copy - and its
    switches lock.

??? question "Can I use WuWeiDict without a browser?"

    Yes. Every search mode has a CLI equivalent command.

    ``` sh title="the terminal, no server needed"
    wudict lookup ~/Dicts/Oxford.mdx flight   # print the entry as HTML
    wudict keys   ~/Dicts/Oxford.mdx          # list every headword
    wudict ingest ~/Dicts                     # prepare a whole folder
    wudict clean                              # list removable library items
    ```

    [All commands](../reference/cli.md)

??? question "Can I use it on my phone?"

    Yes, on Android. The app uses the same wudict server codebase and works offline; your
    dictionaries go in *Internal storage ▸ Dictionaries*. It also adds a
    **wuDict** entry to the text-selection toolbar, so you can look a word up
    without leaving the app you are reading.

    There is no iOS app, and not planned, unless sufficient interest arises.

    [The Android app](../apps/android.md)

??? question "Can other computers on my network use it?"

    Set [`SERVER_IP`](../reference/configuration.md#server_ip-and-server_port)
    to `0.0.0.0` and WuWeiDict accepts connections from your network.

    Do this only on a network you trust. WuWeiDict has no login and no access
    control.

??? question "Does WuWeiDict change my dictionary files?"

    Never. It reads them. Everything it builds goes into the library folder,
    which is a different folder by design.

    To fix a broken file inside a dictionary, put a replacement in that
    dictionary's [`res/` folder](../dictionaries/override.md). Delete your file
    and the dictionary is exactly as it shipped.

??? question "Why does searching *estuviera* find nothing?"

    Because word-form data for Spanish is not installed. WuWeiDict retries a
    failed search with the word's dictionary form — *knew* → **know** — but
    only English is built into the program; every other language is a small
    file you install.

    Click **🔤 Lemmatization** in the ⚙ box of the ☰ panel (or on the settings
    page), tick Spanish, and search again — it works immediately, with no
    restart. On a desktop,
    [`wudict lemmas download es`](../reference/cli.md#lemmas) does the same.

    [Inflected words](../start/search.md#inflected-words)

    ** 💡 IMPORTANT**: Dictionary formats like `.mdx`, `.slob` and others contain
    NO DATA about the actual language of the headwords in the dictionary.
    Lemmatization will only work for these dictionaries if their filename starts 
    e.g. with `es-es`,  `fr-en` (will be detected as French). 
    Or, as an alterntive, you can place all the dictionaries for a specic language 
    under a sobfolder that must match exactly the language code, e.g. `de` or `it`.

    For English language dictionaries the `en-en` prefix is optional, since wudict
    will by default use English as the fallback lemmatization language if it cannot be detected
    from the dictionary metadata or from the filename or subfolder name.

??? question "Which download should I take, -cgo or -purego?"

    Take `-cgo` for OS's for which it exists. It uses the faster SQLite driver and converts
    Speex audio internally without requiring external decoders.

    `-purego` is the fallback. It is slightly slower and needs the external
    `speexdec` program for Speex audio.

    [Both flavours, compared](../start/install.md#which-flavour-to-download)

??? question "How do I stop the browser tab opening at every start?"

    Start WuWeiDict with `--no-browser`, or put `NO_BROWSER = "1"` in
    `~/.wudict/wudict.toml`.

??? question "How can I build this documentation site?"

    The site is built with [Zensical](https://zensical.org/) and needs Python 3.

    ``` sh title="from the repository"
    cd pages
    pip install zensical
    zensical serve      # watch it live at localhost:8000
    zensical build      # static HTML into pages/site
    ```
