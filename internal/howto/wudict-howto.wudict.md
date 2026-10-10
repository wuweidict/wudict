# wudict howto
wudict: 1
from: en
to: en

Type `wudict help` in the search box for quick access, and [browse](/browse?dict=wudict-howto).

## wudict welcome
## wudict intro
## wudict 1st run
## wudict howto
## wudict help

<em>💡Tip</em>: you can return to this article at any time by searching for `wudict welcome` or `wudict intro`

> *Quick actions*

- [Download free libre dictionaria](https://legbehindneck.com/wudict)
- [Browse the wudict repertorium](/browse?dict=wudict-howto) all entries in this guide
- [Add local existing dictionaria](/setup)
- Add/remove [morphology files](/lemmas)
- Explore the [dictionary panel <kbd>☰</kbd>](<entry://wudict panel>)
- Discover [wudict markdown](<entry://wudict markdown>) (this howto is written in [↗ it](https://github.com/wuweidict/wudict/blob/master/internal/howto/wudict-howto.wudict.md))

> *wudict features*

- Formats: MDict (`.mdx/.mdd`), StarDict (`.ifo/.idx/.dict/.dz`), Aard2 (`.slob`), Lingvo DSL (`.dsl/.dsl.dz`), Babylon (`.bgl`), ZIM (`.zim`), and wudict markdown (`.wudict.md`)
- Instant search across all dictionaries with four [search modes](<entry://wudict search>): prefix, exact, contains, and full-text search
- Dictionaries are automatically grouped by language, language pair, content and publisher; add your own groups in <kbd>☰</kbd> → ![](cog.svg) → [Edit groups](/groups).
- Double-tap any word in a wudict dictionary article to instantly look up its definition
- Adjust the font size via the <kbd>☰</kbd> → <kbd>−</kbd> <kbd>+</kbd> widget
- [Lemmatization](<entry://wudict lemmatization>) for 24 languages: <kbd>☰</kbd> → ![](cog.svg) → <kbd>Lemmatization…</kbd>; English is built in
- Android: full screen, hiding the system bars while you read and drawing content edge-to-edge, into the camera cutout: long-tap the wudict icon in the launcher → **Settings** → **Screen**
- Browse **all headwords** in any dictionary via <kbd>☰</kbd> → ![](cog.svg) → <kbd>Browse A–Z…</kbd>
- Select text in any app and pick → <kbd>**wuDict**</kbd> from the context menu
- Apply custom CSS styles and fonts via the embedded Styler: <kbd>☰</kbd> → ![](cog.svg) → <kbd>Custom styles…</kbd>
- Import dictionaries into wudict directly from your file manager: tap a `.mdx/.dsl/.bgl/.zim/.slob/.zip/.7z/.wudict.md` file → <kbd>Share</kbd> → <kbd>**wuDict**</kbd>
- Long-tap a dictionary download link on any web page (`.mdx/.slob/.dsl/.zip/.7z`) and pick <kbd>Share</kbd> → <kbd>**wuDict**</kbd> to install it locally
- Share and install single dictionaries and dictionary collections via specially crafted [🔗wudict URLs](https://legbehindneck.com/wudict)

📱 Android only · 💻 desktop only · everything else works on both.

| Control | What it does |
| --- | --- |
| <kbd>prefix</kbd> | the [search mode](<entry://wudict search>): prefix, exact, contains, full-text |
| <kbd>All dictionaries</kbd> | search everything, a specific dictionary, a language or language pair, a publisher |
| <kbd>◐</kbd> | theme: light, dark, automatic |
| <kbd>⇔</kbd> | wide layout |
| <kbd>⊞</kbd> | open all results |
| <kbd>☰</kbd> | the [panel](<entry://wudict panel>): your dictionaries and settings |

### Quick Start

1. [Add dictionaries](<entry://wudict add dictionaries>): MDict, StarDict, Slob, DSL, Babylon, ZIM, and [more](<entry://wudict formats>).
2. Search. Double-click a word in an article (📱 double-tap) to look it up.
3. In <kbd>☰</kbd>, pin ![](pin.svg) the dictionaries you use most. Pinned dictionaries come first in the results and in the picker.
4. Install morphology files for your languages: ![](lemmas.svg) [Lemmatization](/lemmas). English is built in.

### Options

At the top of the dictionary panel <kbd>☰</kbd>:

- **Text size**: <kbd>−</kbd> <kbd>15px</kbd> <kbd>+</kbd>. Click the number to reset.
- **Find a dictionary**: type part of its title or group. Only the dictionaries that match stay in the list, and the switch above the list changes only these dictionaries. When you close the panel, the field becomes empty.
- <kbd>A–Z</kbd>, beside the field: unpin all dictionaries, so that the whole list is A–Z. It asks first. It shows while a dictionary is pinned and the field is empty.

In <kbd>☰</kbd> → ![](cog.svg):

- **Open** sets the number of dictionaries that open automatically. They open in the sequence of your list and only when they have a result. <kbd>1, first to show a result</kbd> opens the dictionary that shows a result first. <kbd>All</kbd> opens all dictionaries that have a result; many open dictionaries can make the page slow.
- ![](rescan.svg) <kbd>Rescan folders</kbd> can be used to refresh the dictionary folders after adding or removing items.

More: [search](<entry://wudict search>) · [full-text](<entry://wudict full-text>) · [panel](<entry://wudict panel>) · [index](<entry://wudict index>) · [styles](<entry://wudict styles>) · [links](<entry://wudict links>) · [📱 Android](<entry://wudict android>) · [💻 desktop](<entry://wudict desktop>) · [FAQ](<entry://wudict FAQ>)

## wudict search
## wudict search modes
## wudict modes

**Pick a mode in the bar; accents and case never matter**: `corazon` finds *corazón*, `OXFORD` finds *Oxford*. Exact and prefix work at once; contains and full-text need an optional [index](<entry://wudict index>).

| Mode | Finds | Use it when |
| --- | --- | --- |
| **prefix** | headwords that start with your text | you know how the word begins (the default) |
| **exact** | the headword itself | you know the word |
| **contains** | your text anywhere in a headword | you know the middle |
| **full-text** | words inside article text, best first | you remember the meaning, not the word |

wudict keeps the mode that you select. A word from a link or from a different app does not use full-text: wudict uses prefix for that word.

[base form lemmas](<entry://wudict lemmatization>) let you find **know** by typing *knew*.

***

- [index](/browse?dict=wudict-howto)

## wudict full-text
## wudict fts
## wudict full-text syntax

**Several words are a phrase; quotes make it exact**. `no pun intended` finds the phrase, then, only if nothing matches, the words in proximity, then the words anywhere, and the section header indicates which stage answered. Full-text searches only dictionaries whose full-text [index](<entry://wudict index>) is on. Dictionaries without a FTS are offered for indexing.

| You type | You get |
| --- | --- |
| `pun` | words starting with *pun* |
| `"pun"` | the word *pun* only |
| `"no pun intended"` | exactly that phrase, never widened |
| `pun OR joke` | either |
| `pun NOT punctuation` | *pun*, without *punctuation* |
| `NEAR("bank" "river", 4)` | both words, with at most 4 words between them |
| `(pun OR joke) AND intended` | grouping |

Operators count only in capitals: `wage not minimum` is three words. A query never fails; what cannot be parsed is read as plain words. ![](highlight.svg) <kbd>Highlight matches</kbd> in <kbd>☰</kbd> → ![](cog.svg) marks the words found; step through them with the chevrons (💻 <kbd>←</kbd> <kbd>→</kbd>, <kbd>F3</kbd>).

- [index](/browse?dict=wudict-howto)

## wudict lemmatization
## wudict word forms
## wudict inflected words
## wudict lemmas

**Lemmatization is applied when a single word finds nothing in a dictionary**: *knew* finds **know**, *estuviera* finds **estar**. 24 languages: English is built in, the others install in one tap from ![](lemmas.svg) [Lemmatization](/lemmas) (also in <kbd>☰</kbd> → ![](cog.svg)).

Each language is a small download (33 kB to 2.5 MB) and is used only for dictionaries in that language: Spanish *sale* → **salir** is never asked of an English dictionary. Untick a language to delete it.

**IMPORTANT**

> ❗️ For lemmatization to work, wuDict needs to know the dictionary language! Babylon (`.bgl`), Lingvo (`.dsl`), ZIM and wudict markdown declare the headword language. Dictionary formats like `.mdx` have no language metadata, and wuDict infers the language in this order of priority:

- the dictionary file name starts with a language code or English language name, e.g. `es-es` or `spa-eng` (for example, spa-eng-oxford.mdx): the first language is used
- a folder inside a dictionary folder is named by a language code or English language name, such as `es`, `spa` or `Spanish`: it applies to the dictionaries in it
- the dictionary **title** names a language, e.g. *Dahl's Russian Dictionary*
- if none of the previous checks found a language, English is used, which implies that for English dictionaries you do not need to rename your files or the parent subfolders to match `en` or `eng`.

***

- [index](/browse?dict=wudict-howto)

## wudict panel
## wudict ☰
## wudict settings
## wudict dictionary list

<kbd>☰</kbd> **holds your dictionaries and every setting**. The top rows set the text size and find a dictionary by title or group; the ![](cog.svg) row holds the settings; the list below is your dictionaries, in the order results appear.

***

- [index](/browse?dict=wudict-howto)

### Display options

In <kbd>☰</kbd> → ![](cog.svg):

- ![](highlight.svg) <kbd>Highlight matches</kbd>: mark the words a [full-text](<entry://wudict full-text>) search found.
- ![](speak.svg) <kbd>Read aloud</kbd>: select text in an article to speak it using the OS's Text-to-speech engine. You can pick your preferred voice via the chevron next to the speaker icon (when the OS provides multiple voices for the detected language), see more details under [wudict Text-to-speech](<entry://wudict Text-to-speech>)
- **Open**: see [wudict welcome](<entry://wudict welcome>).
- **Group by**: which groups the dictionary picker offers (language, language pair, content, publisher). [Edit groups](/groups) changes them or adds your own: one line per group, `MyGroup = text, other text`, and a dictionary joins it when one of the texts is anywhere in its title or file name. Between backticks, a regular expression: `` MyGroup = `^the\b` ``.

***

- [index](/browse?dict=wudict-howto)

### Settings ![](cog.svg)

- ![](folder.svg) <kbd>Edit folders…</kbd>: [add dictionaries](<entry://wudict add dictionaries>).
- ![](rescan.svg) <kbd>Rescan folders</kbd>: find dictionaries added or removed since. If a dictionary's files are gone, it offers to delete its library folder.
- ![](lemmas.svg) <kbd>Lemmatization…</kbd>: [lemma data](<entry://wudict lemmatization>).
- ![](browse.svg) <kbd>Browse A–Z…</kbd>: [read a dictionary page by page](<entry://wudict browse>).
- ![](styles.svg) <kbd>Custom styles…</kbd>: [your own look](<entry://wudict styles>).
- <kbd>Full-text for every dictionary…</kbd>: build every full-text [index](<entry://wudict index>) at once.

***

- [index](/browse?dict=wudict-howto)

### Per dictionary

- ![](pin.svg) pin: add the dictionary to the pinned ones, after the others. Pinned dictionaries come first, in your order; all the others follow A–Z. The results and the picker use this order.
- ![](top.svg) to the top: make the dictionary the first in the list. An unpinned dictionary is pinned.
- <kbd>▲</kbd> <kbd>▼</kbd>, or 💻 drag <kbd>⠿</kbd>: move a pinned dictionary among the pinned ones.
- the checkbox: include/exclude from *All dictionaries* search, even when <kbd>OFF</kbd> a dictionary is still searchable when selected explicitly in the combobox.
- click or tap the dictionary name: search only this dictionary.
- <kbd>contains</kbd> <kbd>full-text</kbd> <kbd>media</kbd>: optional [indexes](<entry://wudict index>), with their size.
- ![](browse.svg) <kbd>Browse</kbd>: [A–Z](<entry://wudict browse>). **About this dictionary**: its details.
- the file row: its files, and 🗑 <kbd>Remove…</kbd> ([remove](<entry://wudict remove>)).

## wudict text size
## wudict font size
## wudict zoom

In <kbd>☰</kbd>, at the top of the panel: <kbd>−</kbd> <kbd>15px</kbd> <kbd>+</kbd>; click the number to reset. It sets the size of article text; the app keeps it.

## wudict add dictionaries
## wudict add
## wudict folders
## wudict setup
## wudict import

**Put dictionary files in a folder wudict reads, or import them**. They are searchable at once, and indexed in the background. [Setup](/setup) lists the folders and imports files.

- **Import**: on [Setup](/setup), drop or choose a file, or paste a download link, then <kbd>Install</kbd>. An archive (`.zip`, `.7z`) is unpacked; the parts of a dictionary (an `.mdd` beside its `.mdx`) are found for you.
- 📱 Open a dictionary file with wuDict from your file manager, or share it to wuDict. Long-tap a download link on a web page → Share → wuDict.
- 📱 The wuDict build from GitHub also reads *Internal storage ▸ Dictionaries* once you allow *All files access*.
- 💻 **Folders**: on [Setup](/setup), paste the path of any folder; subfolders are read too. The default is `~/Dictionaries`.

After copying files into a folder, press ![](rescan.svg) <kbd>Rescan folders</kbd> in <kbd>☰</kbd> → ![](cog.svg). Copy every part of a dictionary: see [wudict formats](<entry://wudict formats>).

***

- [index](/browse?dict=wudict-howto)

## wudict formats
## wudict dictionary formats
## wudict files

**wudict reads these formats**.

| Format | Files |
| --- | --- |
| MDict | `.mdx`, and its `.mdd` resources |
| StarDict | `.ifo` with `.idx` and `.dict` (or `.dict.dz`), `.syn` |
| Aard 2 | `.slob` |
| Lingvo DSL | `.dsl` or `.dsl.dz`, and its `.files.zip` |
| Babylon | `.bgl` |
| ZIM | `.zim` (Kiwix, Wikipedia, Wiktionary) |
| wudict markdown | `.wudict.md`, a [text file you can write](<entry://wudict markdown>) |

Speex `.spx` audio auto-converted for browser playback. A folder named after a language (`es/`) can be used to signal to wudict the language of a dictionary. The preferred way is for dictionary files to have a file prefix with a two character or three character language codes, e.g. `fr-es-oxford.mdx` or `fra-spa-oxford.mdx`.

***

- [index](/browse?dict=wudict-howto)

## wudict index
## wudict indexing
## wudict prepare

**wudict indexes each dictionary in the background**. You can search a dictionary even if it has no headword index, but the index makes it faster and more efficient for RAM/CPU consumption as well as battery life. In the dictionary panel <kbd>☰</kbd> you can also optionally enable <kbd>contains</kbd> and <kbd>full-text</kbd> indexes — these can be additional GB for large dictionaries, are optional and can be enabled on-demand.

- <kbd>media</kbd> packs a dictionary's pictures and sounds into its index, so it keeps working if you delete the original files, and also makes access faster to assets such as images and audio.
- <kbd>Full-text for every dictionary…</kbd> in the ![](cog.svg) row will generate full-text indexes for all dictionaries — IMPORTANT: depending on the size of your dictionary collection this can take GB of space and take hours.
- ⟳ on a switch, or a **Rebuild** line: an outdated index, made by an older wudict or before the dictionary files changed; one click rebuilds it.
- DSL, Babylon and wudict markdown are indexed as soon as they are opened; ZIM only when you ask, since this format already has its own index and wudict can use it.

An index usually takes less space than the dictionary file.

***

- [index](/browse?dict=wudict-howto)

## wudict browse
## wudict all headwords

**Read a dictionary word by word, A to Z**: ![](browse.svg) <kbd>Browse</kbd> on a dictionary in <kbd>☰</kbd>, or ![](browse.svg) <kbd>Browse A–Z…</kbd> in the ![](cog.svg) row. [Try it on this guide](/browse?dict=wudict-howto).

Tap a letter to jump; type the start of a word to go there; tap a headword to read it. 💻 The <kbd>←</kbd> <kbd>→</kbd> keys turn pages, <kbd>Home</kbd> <kbd>End</kbd> jump to the first and last page.

***

- [index](/browse?dict=wudict-howto)

## wudict links
## wudict cross-references
## wudict double-click
## wudict double-tap

**Click a link in an article to follow it; double-click any word (📱 double-tap) to look it up**. A link is looked up in the dictionary you are reading first, then in all of them. Back returns to where you were.

Every search has its own address, so you can bookmark it: `/?q=word&mode=exact`. 📱 `wudict://lookup?q=word` opens a lookup from other apps and scripts.

***

- [index](/browse?dict=wudict-howto)

## wudict examples
## wudict brief view
## wudict full view
## wudict secondary text

**Press ![](full.svg) at the right of a dictionary's header to show its examples**; press again to hide them. Lingvo DSL dictionaries mark examples, sub-entry links and other secondary text, and an article opens with it hidden: the dotted line in the icon is the hidden text. Shown, it is grey. The next result opens the way you left the last one. A [full-text](<entry://wudict full-text>) match inside secondary text shows it for that result. 💻 <kbd>Ctrl</kbd>+<kbd>\*</kbd> switches the section you are reading.

***

- [index](/browse?dict=wudict-howto)

## wudict read aloud
## wudict speak
## wudict pronunciation
## wudict TTS
## wudict Text-to-speech

**Select text in an article and press the speaker icon that appears to hear it** in a system voice for the article's language. The TTS feature can be disabled via the ![](speak.svg) <kbd>Read aloud</kbd> in <kbd>☰</kbd> → ![](cog.svg). A dictionary's own recordings play with a click on their speaker icon.

To see in action, select the text below, and then click the speaker icon to hear it read aloud by the system Text-to-speech engine:

> For a moment, nothing happened. Then, after a second or so, nothing continued to happen.

***

- [index](/browse?dict=wudict-howto)

## wudict styles
## wudict custom styles
## wudict css
## wudict theme

**Use custom display styles**: ![](styles.svg) <kbd>Custom styles…</kbd> in <kbd>☰</kbd>. <kbd>App</kbd> styles the page, <kbd>Article</kbd> every dictionary article, use <kbd>Files</kbd> to add fonts and images and then <kbd>Insert</kbd> to add them into the CSS. Pick a preset, edit, <kbd>Save</kbd> — you can see a live preview of the changes as you type.

<kbd>◐</kbd> switches light, dark and automatic theme. <kbd>Clear</kbd> then <kbd>Save</kbd> removes a style. If a custom style caused the entire page to disappear, append `?style=off` to the URL (💻 desktop only).

***

- [index](/browse?dict=wudict-howto)

## wudict remove
## wudict delete

In <kbd>☰</kbd> → the dictionary's file row → 🗑 <kbd>Remove…</kbd>, then choose what to delete: everything, only the index, or (once its media is packed) only the dictionary files. ❗️ There is no undo!

To exclude a dictionary from *All dictionaries* searches, just untick its checkmark in the dictionary panel <kbd>☰</kbd>.

***

- [index](/browse?dict=wudict-howto)

## wudict android
## wudict mobile

📱 **Look up a word from any app**: select it and pick <kbd>wuDict</kbd> in the selection menu, or share it to <kbd>wuDict</kbd>; the wuDict definition floats in a popup over the original app. To discard it, press **Back** or tap anywhere outside the popup.

> NOTE: If, instead of a popup, you'd rather have wudict open in a full window, then long press the wudict icon in the launcher and select <kbd>wuDict Settings</kbd> and check the corresponding checkbox under **Look up in the full app**.

- **Add dictionaries** by opening or sharing a file to wuDict: see [wudict add dictionaries](<entry://wudict add dictionaries>).
- **Full screen**: long-tap the wuDict icon on the home screen → <kbd>Settings</kbd> → **Screen**: *Hide while you read* hides the system bars, *Edges of the screen* draws edge to edge, into the camera cutout.
- The keyboard hides when you scroll an article. Off screen, wuDict uses one core, to avoid draining the battery.
- **Reading apps**: in the reader's dictionary settings, choose wuDict, or a dictionary wuDict answers for: *ColorDict*/*GoldenDict*, *Aard 2*, *Lingvo*, *Fora* or *Dictan*. In Moon+ Reader, a *Customized* dictionary with the URL `wudict://lookup?q=%s` also works.
- `wudict://lookup?q=word` opens a lookup from automation apps and scripts.

***

- [index](/browse?dict=wudict-howto)

## wudict desktop
## wudict computer

💻 **wudict runs in your browser**, at `localhost:6888`, as a small program on your computer: a menu-bar icon on macOS, a tray icon on Windows. Nothing leaves the machine.

| Key | Action |
| --- | --- |
| <kbd>/</kbd> | go to the search box; typing anywhere does too |
| <kbd>Esc</kbd> | in <kbd>☰</kbd>: the first press makes the find field empty, the next press closes the panel |
| <kbd>←</kbd> <kbd>→</kbd> · <kbd>F3</kbd> · <kbd>Ctrl</kbd> <kbd>G</kbd> | step through [full-text](<entry://wudict full-text>) matches |

- **wuDict Hover** (Chrome, Firefox): hover a word on any web page (optionally holding <kbd>Alt</kbd>) to see its definition in a popup.
- **Command line**: `wudict searchall word` searches from a terminal; `wudict dump -format md` writes any dictionary as [wudict markdown](<entry://wudict markdown>); `wudict help` lists the rest.

***

- [index](/browse?dict=wudict-howto)

## wudict markdown
## wudict md
## wudict md format

**A wudict markdown file is a dictionary you can write in any text editor — this guide was written in wudict markdown**. Line 1 is `#` and the title, line 2 is `wudict: 1`; each entry is a `## headword` followed by its text. Save it as `name.wudict.md` in a dictionary folder.

<details>
<summary>An example</summary>

***

- [↗ wudict markdown specification](https://github.com/wuweidict/wudict/blob/master/docs/WUDICT-MARKDOWN.md)
- [index](/browse?dict=wudict-howto)

```markdown
# My Glossary
wudict: 1

## colour
## color

The property of an object that depends on the light it reflects. See [hue](entry://hue).
```

</details>

More `##` lines right under the first are other spellings. Links like `[hue](entry://hue)` trigger a lookup. Tables, lists, images and the rest of standard markdown work. 💻 Running `wudict dump -format md` from a console lets you export any existing dictionary to wudict markdown. To edit this guide, put a copy in your folder: [Setup](/setup) → **Put the wudict howto in this folder**.

***

- [index](/browse?dict=wudict-howto)

## wudict install dictionaries
## wudict shareable URLs

wudict lets you install dictionaries and dictionary collections via custom shareable URLs which open directly in wudict and allow you to pick which items from the collections you want to install.

Some examples are listed on the wudict URL sharing page:

- [🔗wudict URL sharing](https://legbehindneck.com/wudict)

More ways to install dictionaries:

- android: short or long tap on a `.mdx/.dsl/.bgl/.zim/.slob/.zip/.7z/.wudict.md` from a file manager → <kbd>**Share**</kbd> → <kbd>**wuDict**</kbd>
- android: long tap a dictionary download link on any webpage → <kbd>**Share**</kbd> → <kbd>**wuDict**</kbd>
- desktop: <kbd>☰</kbd> → ![](cog.svg) → <strong>Edit folders…</strong> and drag and drop a dictionary onto the drop zone (<em>"drop one here"</em>)

***

- [index](/browse?dict=wudict-howto)

## wudict FAQ

<details>
<summary>wudict is using too much disk space — how do I claim it back?</summary>

For every dictionary in the panel <kbd>☰</kbd> the size of headword, contains, and full-text search indexes are displayed. Also for dictionaries with assets (e.g. `.mdd`) resources also might be imported into a wudict optimized sqlite-based storage. You can reclaim space by deleting the *contains*, and *full-text search* indexes for dictionaries for which you don't need them - just tap the corresponding badge in the panel. Clicking the dictionary source file in the card, located below the <kbd>Browse</kbd> button, will display a delete option for deleting both the indexes and/or the source dictionaries (use with caution!)

</details>

<details>
<summary>Why does searching estuviera find nothing?</summary>

Because lemma data for Spanish is not installed. wudict retries a failed search with the word's lemma — *knew* → **know** — but only English is built into the program; every other language is a small file you install.

Click <kbd>☰</kbd> → ![](cog.svg) → <kbd>Lemmatization…</kbd> (or <kbd>🔤 Lemmatization</kbd> on the setup page), tick Spanish, and search again — it works immediately, with no restart. On a desktop, [`wudict lemmas download es`](/lemmas) does the same.

💡 **IMPORTANT**: Dictionary formats like `.mdx`, `.slob` and others contain *no metadata* about the headwords language. Lemmatization will only work for these dictionaries if their filename starts with a two or three character language code prefix e.g. with `es-es`, `fr-en` (will be detected as French). Or, as an alternative, you can place all the dictionaries for a specific source language under a subfolder that must match exactly the language code, e.g. `de` or `it`.

For English language dictionaries the `en-en` prefix is optional, since wudict will by default use English as the fallback lemmatization language if it cannot be detected from the dictionary metadata, the file name, the subfolder name or the title. A title that names a language, such as Spanish or German, is used only when the file and folder names give none.

</details>

<details>
<summary>What is the format used for this dictionary and where can I see its source code?</summary>

The wudict howto dictionary uses **wudict markdown** format, and you can build your own markdown dictionary using the example source at [↗ wudict-howto.wudict.md](https://github.com/wuweidict/wudict/blob/master/internal/howto/wudict-howto.wudict.md)

also see [<strong>`wudict markdown`</strong>](<entry://wudict markdown>)

</details>

<details>
<summary>Can I convert any dictionary in my collection to wudict markdown?</summary>

The `wudict` binary on the desktop (windows/linux/mac) provides a `dump` command that can be used to export any dictionary to the **wudict markdown** format:

```sh
# with -mode html you get maximum fidelity with complex HTML rendered as markdown HTML blocks
wudict dump -format md -mode html -o my-output-folder ldoce6.mdx

# with -mode clean complex HTML is reduced to the subset that is allowd in markdown
wudict dump -format md -mode clean -o my-output-folder ldoce6.mdx
```

By default all resources (media, .js, .css) are included in the export. You can control what gets included with the `-resources` flag. See `wudict dump --help` for details:

```
dump --help
Usage of dump:
  -compress string
    	md only: gz
  -format string
    	csv, or md (wudict markdown) (default "csv")
  -mode string
    	md only: html (each article's HTML preserved) or clean (markdown only, lossy) (default "html")
  -o string
    	output folder for the dump and its resources (created if missing)
  -output string
    	long form of -o
  -resources string
    	all, text (only .css, .js and other text files), or none (default "all")
```

also see [<strong>`wudict markdown`</strong>](<entry://wudict markdown>)

</details>

***

- [index](/browse?dict=wudict-howto)
