---
title: FAQ
description: The questions that actually come up — privacy, ports, missing dictionaries, Speex audio, disk space, updates, and where everything lives on disk.
---

# FAQ

The questions that actually come up, answered in the order they usually
do.

??? question "Is my data sent anywhere?"

    No. wudict binds to your loopback interface (default
    `127.0.0.1:6888`), holds no account, and does not include any telemetry, tracking or analytics.
    Dictionaries are read from your disk, indexed on your disk, served
    to your browser.

??? question "Why the name WuWeiDict? And why `wudict`?"

    _Wú wéi_ — 無為 — is the classical Chinese idea of effortless action:
    the current does the work, the boat does not strain. The product is
    named WuWeiDict because it disappears when it works; the binary is
    named `wudict` because a dictionary in your terminal should take less
    time to type than to start reading about.

??? question "The port 6888 is taken. What now?"

    Pick another:

    ``` sh
    wudict --port 9090            # this session
    SERVER_PORT=9090 wudict       # this machine, this shell
    ```

    In `~/.wudict/wudict.toml`:

    ``` toml
    SERVER_PORT = 9090
    ```

    The URL you open changes too — `localhost:9090`. The server also
    prints a friendly hint when it finds the port busy, instead of a
    stack trace.

??? question "My dictionaries don't show up"

    In order, the usual suspects:

    1. Is the folder the one the server scans? Startup prints the
       effective dictionary folder — compare.
    2. Is the file inside a subfolder? wudict walks recursively; if it
       still does not appear, check the extension: `.dsl.dz` is matched
       as a whole, and a lone `dict.dz` without its `.ifo` is not a
       dictionary.
    3. Did you just copy files in? The ☰ panel's **Rescan folders**
       re-walks everything without a restart.
    4. Windows users: separators in `DICT_DIR` are `;`, not `:`.

    One further safety: the dictionary folder may not be the db folder —
    wudict refuses to start when `DICT_DIR` and `DB_DIR` are the same.

??? question "Speex audio plays nothing"

    `.spx` is transcoded in-process on `-cgo` builds; the pure-Go build
    routes it to the `speexdec` utility. Not installed?

    ``` sh
    brew install speex     # macOS
    apt install speex      # Debian / Ubuntu
    ```

    Point at a specific binary with `--speexdec` / `SPEEXDEC`. The
    resolved path — or an install hint — is printed at startup, so you
    always know which side is responsible. Note that `/res/` serves
    `.spx` as `audio/wav` content type since it is transcoded to wav on-the-fly.

??? question "Contains and full-text take disk space. Can I take the indexes back?"

    Yes — the switches in the ☰ panel flip both ways, with the real size
    in megabytes before you click. The one honest condition: removal is
    offered while the original source file is still on disk, because that
    is what makes it reversible. A dictionary whose source is gone keeps
    its prepared data — it is then the only copy — and its switches lock.

??? question "Can I move my whole library to another machine?"

    One dictionary is one folder under the db dir (default
    `~/.wudict/db`). Copy the folder, zip it, move it; on the other
    machine it opens wherever a dictionary folder points at it. A
    `text.db` carries all the search, and media either comes along in
    the folder or falls back to the source file when one is present.

??? question "How do I update?"

    Download the new binary and replace the old one — that is the entire
    procedure; the prepared libraries stay put. Databases are
    feature-detected at open, so an older `text.db` keeps working and
    simply lacks the newest index until you re-indexed. Use `wudict
    --version` to see the current version.

??? question "Where is everything on disk?"

    ``` text
    ~/.wudict/
      wudict.toml     the config file — generated commented on first run
      state.json      your dictionary order and on/off switches
      db/             the library: one folder per prepared dictionary
      spxcache/       transcode cache for .spx audio
    ~/Dictionaries    the default dictionary folder
    ```

??? question "Can I use it without a browser?"

    `wudict` works in CLI mode too:

    ``` sh
    wudict lookup ~/Dicts/Oxford.mdx flight    # dump raw entry to stdout
    wudict keys   ~/Dicts/Oxford.mdx           # list headwords
    wudict ingest ~/Dicts                      # prepare indexes headlessly
    wudict clean                               # list removable library items
    ```

??? question "How to supress browser auto-open?"

    Start `wudict` with `--no-browser` or in `~/.wudict/wudict.toml` add `NO_BROWSER = "1"`.

??? question "How can I build this site on my machine?"

    Assuming you have Python3 installed you can install
    **[Zensical](https://zensical.org/)** and build the markdown pages under `pages/` with:

    ``` sh
    cd pages
    pip install zensical
    zensical serve      # watch it live at localhost:8000
    zensical build      # static HTML into pages/site
    ```
