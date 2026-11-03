---
title: FAQ
description: Privacy, working offline, licence, getting dictionaries, http:// links, files on disk, moving the library, updating, disk space, the terminal, Android builds and requirements, export, Termux, searching several dictionaries, custom styles, network access, lemmatization, read aloud.
---

# FAQ

For problems, see [Troubleshooting](troubleshooting.md).

??? question "Is any of my data sent anywhere?"

    No. wudict listens on the loopback address `127.0.0.1` and has no account,
    no telemetry, no analytics or crash reporting. It connects to the internet only to
    download what you ask for: lemma data, or a dictionary from a link you
    provide. See the [Privacy Policy](../privacy_policy.md).

??? question "Does wudict work offline?"

    Yes. Search, lookup and indexing need no network. This is also true for
    the Android app. Only these need the network:

    -   A download of lemma data. English lemma data is built in.
    -   An import from a link.
    -   An article that refers to an image, font or script at an `http://` or
        `https://` address. Most dictionaries include their resources.
    -   <kbd>Read aloud</kbd> with a voice that the browser gets from the
        network. The voices of the operating system need no network.

??? question "Why two names, WuWeiDict and wudict?"

    *Wú wéi* (無為) is the classical Chinese term for effortless action, or
    non-action. `wudict` is the short form, used for the program.

??? question "Is wudict free?"

    Yes. wudict is free software under the GNU General Public License,
    version 3 or later. You can use, change and redistribute it under that
    licence. The source code is on
    [GitHub](https://github.com/wuweidict/wudict). See [Licence](license.md).

    The licence applies to wudict only. Each dictionary has its own licence.

??? question "Where can I get dictionaries?"

    Use your own dictionary files, or get free dictionaries:

    -   [legbehindneck.com/wudict](https://legbehindneck.com/wudict/): free
        dictionaries as [share links](../dictionaries/add.md#share-links). On
        Android, a share link starts the import in wuDict. On a desktop, paste
        the link on the setup page.
    -   [Kiwix](https://library.kiwix.org): Wikipedia, Wiktionary and other
        reference works as `.zim` files.
    -   Your own dictionary: write it as a
        [wudict markdown](../dictionaries/formats.md#wudict-markdown) file in
        a text editor.

    wudict reads MDict, StarDict, Aard 2, Lingvo DSL, Babylon, ZIM and wudict
    markdown: see [Formats](../dictionaries/formats.md). To install the files:
    [Add dictionaries](../dictionaries/add.md).

??? question "Why does an import from a link fail with “only https downloads are allowed”?"

    The full message is:

    ```
    only https downloads are allowed: set IMPORT_INSECURE=1 to allow http:// links
    ```

    wudict downloads only from `https://` links to public hosts. To allow an
    `http://` link, set
    [`IMPORT_INSECURE`](../reference/configuration.md#import_insecure) to `1`
    in `wudict.toml` or in the environment, then restart wudict:

    ``` toml
    IMPORT_INSECURE = 1
    ```

    The same setting allows hosts on the local network, which are otherwise
    refused with “that address is on the local network”. When
    [`SERVER_IP`](../reference/configuration.md#server_ip-and-server_port) is
    not `127.0.0.1`, a client on the network can then make this machine
    download from any address it can reach: restrict the hosts with
    [`IMPORT_URL_HOSTS`](../reference/configuration.md#import_url_hosts).

    Without the hint, the message means the link is not `http://` or
    `https://` at all; no setting allows those.

??? question "Where are wudict's files?"

    ``` text title="macOS"
    ~/.wudict/
      wudict.toml     settings
      state.json      dictionary order, enabled dictionaries, display preferences
      groups.ini      picker groups, only created after manually edited
      style/          custom styles
      lemmas/         installed lemma data
      db/             the library; db/spxcache/ holds converted Speex audio
    ~/Dictionaries    the default dictionary folder
    ```

    ``` text title="Linux"
    ~/.config/wudict/        wudict.toml, groups.ini, style/
    ~/.local/share/wudict/   db/ (the library), lemmas/, favourites.txt
    ~/.local/state/wudict/   state.json, history.txt, token
    ~/Dictionaries           the default dictionary folder
    ```

    Windows, Android and the `$XDG_*` variables: [Configuration → Files](../reference/configuration.md#files).

??? question "How do I update wudict?"

    Replace the executable, or run the new installer, `wuDict.app` or APK over
    the old one. Settings and the library are kept. Indexes built by an older
    version are reported as [outdated](../dictionaries/library.md#outdated-indexes)
    and rebuilt with <kbd>Rebuild</kbd> or `wudict reindex`. 
    Use [Obtainium](https://obtainium.imranr.dev/) to automatically update the Android FOSS version of wudict. 

??? question "How do I free the disk space an index uses?"

    Click the index's switch in the dictionary panel; the switch shows its size.
    The switches are locked when the dictionary files are gone, because the
    library folder is then the only copy; <kbd>🗑 Remove…</kbd> still deletes
    it. <kbd>Rescan folders</kbd> lists [orphans](../dictionaries/library.md#orphans)
    and deletes the ones you tick.

??? question "Can I use wudict without a browser?"

    Yes, you can use wudict in the terminal:

    ``` sh
    wudict lookup -format text Oxford.mdx flight     # show definitions as plain text
    wudict keys   ~/Dictionaries/Oxford.mdx          # list all headwords
    wudict ingest                                    # index the dictionary folders
    wudict clean                                     # list removable library folders
    ```

    See `wudict --help` in terminal.

    [Command line](../reference/cli.md)

??? question "Is there a phone app?"

    On Android: the app runs the same server and works offline, and looks up a
    word from the text selection menu of other apps. There is no iOS app.

    [Android app](../apps/android.md)

??? question "What's the difference between the Android FOSS build and the Google Play build?"

    **Short answer.** Both builds are compiled from the same open-source repository and provide the same search, lookup, and indexing functionality. The primary difference is how they access dictionary files.
    
    **FOSS build.** Choose this build if you need unrestricted access to dictionary files in device storage. It allows you to select dictionary folders from locations of your choice, subject to Android's storage permissions and restrictions.
    
    **Google Play build.** Choose this build if you prefer distribution through Google Play and automatic app updates, or if you want to avoid granting the app broad storage access. It uses Android's Storage Access Framework (SAF), which lets you grant access to specific folders without granting general access to device storage.
    
    The Google Play build stores its app-managed dictionary data in:
    
    `/sdcard/Android/data/com.legbehindneck.wudict/files/Dictionaries`
    
    On recent Android versions, this directory is generally inaccessible through standard file managers. This can make it more difficult to manage or transfer dictionary files manually.

    **Differences.**

    | | FOSS build | Google Play build |
    | --- | --- | --- |
    | Source | GitHub, Obtainium | Google Play |
    | Dictionary files | *Internal storage → Dictionaries*, a folder you manage | `Android/data/com.legbehindneck.wudict/files/Dictionaries`; you import the files |
    | Storage permission | *All files access* | none |
    | Updates | Obtainium, or a download from GitHub | Google Play |
    | Signing certificate | the wudict developers | Google |
    | Reachable from other devices (testing) | optional toggle in wudict settings, OFF by default | no |
    | Install and crash data to Google | no | yes |

    **Disk space.**

    -   FOSS build: the dictionary files stay where you put them. One copy of
        each dictionary exists.
    -   Google Play build: an import copies the dictionary files into the app.
        Two copies exist until the import ends. The import does not start if
        the free space is less than the size of the files.
    -   Google Play build: after the import, the app offers to delete the
        originals. If you keep them, the dictionaries use two times the disk
        space.
    -   Google Play build: dictionary files in
        `Android/data/com.legbehindneck.wudict/files/Dictionaries` on a microSD
        card are read there. They use no internal storage and need no import.
    -   Both builds: the library adds the indexes and the media packs. It is in
        `Android/data/com.legbehindneck.wudict/files/db`.

    **Uninstall.** Android deletes `Android/data/com.legbehindneck.wudict`:
    the library, the settings and the lemma data.

    -   FOSS build: *Internal storage → Dictionaries* stays. After a new
        install, the indexes are built again.
    -   Google Play build: the imported dictionary files are in
        `Android/data`. Android deletes them too.

    **Advantages of the FOSS build.**

    -   The dictionary folder is a usual folder. A file manager or a computer
        can add, change and delete files in it.
    -   To add a dictionary, copy its files. There is no import and no second
        copy.
    -   An uninstall does not delete your dictionary files.
    -   Google gets no install or crash data.
    -   Other devices on your network can connect, for testing:
        [Access](../access.md#android).

    **Disadvantages of the Google Play build.**

    -   Each dictionary must be imported. During the import, it needs two times
        its size in free space.
    -   Android 11 and later hide `Android/data` from file managers. To get the
        imported files, use USB (*File transfer*), `adb pull` or `adb push`.
    -   An uninstall deletes the imported dictionary files. Copy them before
        you uninstall.
    -   Google collects install and crash data:
        [Privacy Policy](../privacy_policy.md#data-collected-by-google-play).
    -   Other devices cannot connect.

    The Google Play build has these advantages: it asks for no storage
    permission, and Google Play installs the updates.

    **Signing certificate collision.** The two builds have the same package
    name, `com.legbehindneck.wudict`, and different signing certificates.
    Android does not install an update with a different certificate:

    -   You cannot install the two builds on one device at the same time.
    -   You cannot install one build over the other. Android shows an error.
    -   Obtainium cannot update the Google Play build. Google Play cannot
        update the FOSS build.

    To change to the other build:

    1.  From Google Play to FOSS only: copy
        `Android/data/com.legbehindneck.wudict/files/Dictionaries` to a
        computer, over USB or with `adb pull`.
    2.  Uninstall the current build. Android deletes the library, the settings
        and the lemma data.
    3.  Install the other build.
    4.  FOSS build: put the dictionary files in *Internal storage →
        Dictionaries*. Google Play build: <kbd>Import dictionaries</kbd> →
        <kbd>Choose folder</kbd>.
    5.  Download the lemma data again. Make your settings again.

??? question "Why can't I install the Android app?"

    The app needs:

    -   Android 8.0 or later.
    -   A 64-bit ARM (`arm64`) processor. There is no build for 32-bit ARM or
        x86 devices.

    FOSS build, other causes:

    -   Android blocks the APK. Allow *Install unknown apps* for the app that
        opens the APK: the browser or the file manager.
    -   The Google Play build is installed. Android cannot install one build
        over the other: see the question on the two builds above.

??? question "Can other computers on my network use it?"

    Set [`SERVER_IP`](../reference/configuration.md#server_ip-and-server_port)
    to `0.0.0.0`. Requests from the network then need the access key
    ([`AUTH`](../reference/configuration.md#auth)): `wudict token` prints a link
    that carries it; open it once in the browser on the other device. Deleting
    dictionaries from another device also needs
    [`ALLOW_REMOTE_DELETE`](../reference/configuration.md#allow_remote_delete).

??? question "Does wudict change my dictionary files?"

    No. Indexes go into the library. A replacement for a broken resource goes
    into the dictionary's library folder as a
    [resource override](../dictionaries/override.md). Only
    <kbd>🗑 Remove…</kbd> and `wudict rm` delete dictionary files.

??? question "Can I convert or export a dictionary?"

    Yes, from the command line. `wudict dump` can export a dictionary as CSV or as
    [wudict markdown](../dictionaries/formats.md#wudict-markdown):

    ``` sh
    wudict dump -o ~/exports ~/Dictionaries/Oxford.mdx              # CSV
    wudict dump -format md -o ~/exports ~/Dictionaries/Oxford.mdx   # wudict markdown
    ```

    For more details see: `wudict dump --help`

    The images, audio and other resources are exported to a folder next to the output.
    The CSV is compatible with [pyglossary](https://github.com/ilius/pyglossary),
    and thus can be converted to other formats, such as StarDict.
    See [wudict dump](../reference/cli.md#dump).

    The Android app has no command line, but it is possible to build and run 
    the desktop version of wudict on Android in the Termux environment.
    See *Can I use wudict in Termux?* below.

??? question "Can I use wudict in Termux?"

    Yes, you can build wudict from the source code in
    [Termux](https://termux.dev). The server and all commands work, as on a
    desktop.

    1.  Install Termux from F-Droid or from
        [GitHub](https://github.com/termux/termux-app/releases).
    2.  Install the build tools:

        ``` sh
        pkg upgrade
        pkg install golang clang git make
        ```

        `go version` must show 1.26.5 or later.

    3.  Get the source code and build wudict:

        ``` sh
        git clone https://github.com/wuweidict/wudict
        cd wudict
        make build                            # cgo: fast SQLite, built-in Speex decoder
        install -m 755 wudict $PREFIX/bin/
        ```

        The first build compiles SQLite. This can take some minutes.

        Without `clang`, use `make build-purego` in place of `make build`.
        SQLite is then slower, and `.spx` audio needs the `speexdec` program.

    4.  Give Termux access to the shared storage:

        ``` sh
        termux-setup-storage
        ```

        *Internal storage* is then `~/storage/shared`.

    5.  Write the settings:

        ``` sh
        mkdir -p ~/.wudict
        cat >> ~/.wudict/wudict.toml <<'EOF'
        DICT_DIR    = "~/storage/shared/Dictionaries"
        SERVER_PORT = "6889"
        NO_BROWSER  = "1"
        EOF
        ```

        `~/storage/shared/Dictionaries` is also the folder of the wuDict FOSS
        build. The two use the same dictionary files. Each has its own library.
        The wuDict app uses port 6888, so wudict in Termux uses 6889.

    6.  Start the server:

        ``` sh
        wudict
        ```

    7.  Open `http://127.0.0.1:6889` in a browser on the phone.
    8.  To keep the server running when the screen is off:

        ``` sh
        termux-wake-lock
        ```

    To update: in the `wudict` folder, run `git pull`, `make build` and the `install` command again.

??? question "Can I search several dictionaries at once?"

    Yes. *All dictionaries* in the dictionary picker searches every enabled
    dictionary.

    -   To remove a dictionary from *All dictionaries*, clear its checkbox in
        the dictionary panel.
    -   To search one dictionary, choose it in the picker.
    -   To search a set of dictionaries, choose a group in the picker: a
        language, a language pair, or your own group.
    -   To change the order of the results, drag <kbd>⠿</kbd> in the
        dictionary panel.

    See [Picker groups](../dictionaries/groups.md) and
    [Dictionary panel](../start/search.md#dictionary-panel).

??? question "Can I change the look of wudict and of the articles?"

    Yes.

    -   Theme: <kbd>◐</kbd> chooses auto, light or dark.
    -   Text size: <kbd>☰</kbd> → <kbd>−</kbd> <kbd>15px</kbd> <kbd>+</kbd>.
    -   Your own CSS: <kbd>☰</kbd> → folder summary →
        <kbd>Custom styles…</kbd>. **App** changes the wudict page. **Article**
        changes every dictionary article. <kbd>Examples…</kbd> inserts ready
        styles, such as sepia, true black, a serif font, and a compact layout
        for phones. **Files** adds fonts and images.
    -   One dictionary only: a [resource override](../dictionaries/override.md).

    If a rule hides the page, open `http://127.0.0.1:6888/?style=off`.
    See [Custom styles](../dictionaries/styles.md).

??? question "Why does *estuviera* find nothing?"

    Spanish lemma data is not installed, or wudict cannot tell that the
    dictionary is Spanish.

    Install it: <kbd>☰</kbd> → folder summary → <kbd>Lemmatization…</kbd> →
    tick Spanish. It applies from the next search. In a terminal:
    `wudict lemmas download es`.

    Most `.mdx` and `.slob` dictionaries do not declare their language. Name the
    file with a language code first (`es-en-collins.mdx`), or put it in a folder
    named by the code (`es/`). English is assumed when nothing else is found.
    See [language detection](../reference/configuration.md#lemmatization).

??? question "Why is selected text read aloud in the wrong language?"

    wudict reads a selection in one of the dictionary's two languages: the
    language of its headwords or the language of its articles. The script of
    the selection selects between them. If both languages use the same script,
    as German and English do, wudict reads the article language.

    If wudict finds no language of the dictionary in the script of the
    selection, it uses the language of the default voice. You set the default
    voice in the text-to-speech settings of the system. Most `.mdx` and
    `.slob` dictionaries do not declare their languages.

    -   The default voice must use the script of the selection.
    -   In a browser, its language must also be one of the languages that the
        browser prefers.

    If not, wudict reads Latin-script text in the language of the browser or
    of the phone, or in English.

    The <kbd>▾</kbd> button next to the speaker opens a menu with:

    -   <kbd>Read as German</kbd>: the other language of the dictionary, or
        the language that the default voice replaced.
    -   The voices installed for the language that wudict reads. wudict keeps
        your choice for that language.
    -   <kbd>Other languages…</kbd>: all the other languages that have a voice
        on the device and use the script of the selection.
    -   <kbd>Speech settings…</kbd>: Android app only. It opens the
        text-to-speech settings of the system, where you install voices.

    wudict keeps the language you select for that dictionary and that script.
    To go back, select the original language with <kbd>Read as</kbd>.
    <kbd>▾</kbd> is shown only when the menu has a choice.

    The menu shows only the languages that use the script of the selection.
    If a language is not in the menu, the device has no voice for it. Install
    one in the speech settings of the operating system.

    To give a dictionary its languages:

    1.  Rename the file. Put the language pair first, headword language
        first: `de-en-collins.mdx`. For a dictionary in one language, put one
        code: `de-duden.mdx`. You can also put the file in a folder named by
        the code: `de/`.
    2.  <kbd>☰</kbd> → folder summary → <kbd>Rescan folders</kbd>.

??? question "What's the difference between -cgo and -purego flavours and which should I choose?"

    Prefer `-cgo` for platform where it is available: you get a faster SQLite engine, and the built-in Speex decoder.
    `-purego` is the fallback mode for environments where C-code cannot be compiled; A `-purego` build needs the external command line `speexdec` utility for Speex audio decoding.
    See [-cgo or -purego](../start/install.md#-cgo-or-purego).

??? question "How do I stop the browser tab opening at startup?"

    `wudict --no-browser`, or `NO_BROWSER = "1"` in `wudict.toml`.

??? question "How do I build this documentation site?"

    With [Zensical](https://zensical.org/), which needs Python 3:

    ``` sh
    cd pages
    pip install zensical
    zensical serve      # localhost:8000, rebuilt on change
    zensical build      # static HTML in pages/site
    ```
