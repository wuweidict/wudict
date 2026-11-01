# WuDict - desktop and mobile dictionary app

WuDict (`wudict`) searches all your local dictionaries at once and shows
the results in the browser at [localhost:6888](http://localhost:6888). One
executable, no dependencies; runs on macOS, Linux (including Raspberry Pi),
Windows and Android.

📖 User Guide: [wuweidict.github.io/wudict](https://wuweidict.github.io/wudict)

## Android

<div class="badges">
<a href="https://github.com/wuweidict/wudict/releases/latest"><img src="https://raw.githubusercontent.com/Kunzisoft/Github-badge/master/get-it-on-github.png" alt="Get it on GitHub" height="80"></a> <a href="https://apps.obtainium.imranr.dev/redirect?r=obtainium://app/%7B%22id%22:%22com.legbehindneck.wudict%22,%22url%22:%22https://github.com/wuweidict/wudict%22,%22author%22:%22wuweidict%22,%22name%22:%22wudict%22%7D"><img src="https://raw.githubusercontent.com/ImranR98/Obtainium/main/assets/graphics/badge_obtainium.png" alt="Get it on Obtainium" height="80"></a> <a href="https://play.google.com/store/apps/details?id=com.legbehindneck.wudict"><img src="https://play.google.com/intl/en_us/badges/static/images/badges/en_badge_web_generic.png" alt="Get it on Google Play" height="80"></a>
</div>

## Desktop

1.  Download the binary for your system from
    [releases](https://github.com/wuweidict/wudict/releases/latest), rename it
    to `wudict`, make it executable (`chmod +x wudict`) and move it to a folder
    in `PATH`, e.g. `/usr/local/bin`.
    -   Windows: the installer `wudict-windows-x64-setup-<version>.exe`, or
        `wudict-windows-amd64-cgo.exe`.
    -   macOS: `wudict-macos-universal-app-<version>.zip` contains
        **wuDict.app**, with a menu-bar icon. It is signed ad hoc: see
        [macOS app](https://wudict.legbehindneck.com/apps/macos/) for the first launch.
2.  Run `wudict`. The browser opens at [localhost:6888](http://localhost:6888).
3.  The default dictionary folder is `~/Dictionaries`. If it is missing or
    empty, the setup page ([localhost:6888/setup](http://localhost:6888/setup))
    sets the folders, and imports dictionaries from a file or a link.

> [!TIP]
> <sup>If you don't have yet a local dictionary collection you can download some FOSS dictionaries from the [links here](https://legbehindneck.com/wudict/).</sup>

## Features
- Formats: MDict, StarDict, Aard2, DSL, Babylon, ZIM, wudict markdown (a plain-text dictionary you can write in a text editor)
- Instant search across all dictionaries with four modes: prefix, exact, full-text, and [wildcard](https://wudict.legbehindneck.com/reference/wildcard-syntax/) search (`c?t`, `*ology`, `*rain*`)
- Dictionaries are automatically grouped by language, publisher, direction, or [your own custom groups](https://wudict.legbehindneck.com/dictionaries/groups/).
- Adjust the font size via the <kbd>☰</kbd> → <kbd><b>+/-</b></kbd> widget
- [Custom lemmatization](https://wudict.legbehindneck.com/start/search/#inflected-words) (word-form morphology) for 24 languages, configurable via <kbd>☰</kbd> → <kbd>⚙️</kbd> → <kbd>**Lemmatization**</kbd>; English lemmatization comes pre-installed
- Browse **all headwords** in a dictionary via <kbd>☰</kbd> → <kbd>**Browse**</kbd>
- Apply custom CSS styles and fonts via the [embedded Styler](https://wudict.legbehindneck.com/dictionaries/styles/#custom-styles): <kbd>☰</kbd> → <kbd>⚙️</kbd> → <kbd>**Custom Style**</kbd>
- 📱 Double-tap / 💻 double-click any word in a wudict dictionary article to instantly look up its definition
- 📱 Immersive mode (full screen), with an option to draw content edge-to-edge, including the camera cutout area; enable by long-tapping the wudict icon in the launcher → <kbd>**Settings**</kbd>
- 📱 Select text in any app and pick → <kbd>wuDict</kbd> from the context menu
- 📱 Import dictionaries into wudict directly from your file manager: tap a .mdx/.dsl/.bgl/.zim/.slob/.zip/.7z file → <kbd>**Share**</kbd> → <kbd>**wuDict**</kbd>
- 📱 Long-tap a dictionary download link on any web page (.mdx/.slob/.dsl/.zip/.7z) and pick Share → <kbd>**wuDict**</kbd> to install it locally
- Install multiple dictionaries via [wudict magic links 🔗](https://legbehindneck.com/wudict/) 

# Supported formats

| Format | Files | Notes                                                          |
|---|---|----------------------------------------------------------------|
| MDict | `.mdx` + `.mdd` | `.mdd`, `.1.mdd`, … resource archives; Speex audio decoded     |
| StarDict | `.ifo` + `.idx(.gz)` + `.dict(.dz)` | `.syn` synonyms; `res/` folder or `res.zip`                    |
| Aard 2 | `.slob` | zlib, bz2, LZMA2                                               |
| Lingvo DSL | `.dsl`, `.dsl.dz` | `.dsl.files.zip` resources; UTF-8/16/32 detected               |
| Babylon | `.bgl` | character sets detected                                        |
| ZIM | `.zim`, or `.zimaa` + `.zimab` + … | Kiwix and Wikimedia archives; the parts of a split archive are read as one file |
| wudict markdown | `.wudict.md`, `.wudict.md.gz`, `.md` | Markdown (CommonMark) as generated by `wudict dump -format md` |
| wudict library | `text.db` | wudict's SQLite format, one folder per dictionary              |

## Search

| Mode | Matches | Needs |
|---|---|---|
| **exact** | the headword, ignoring case and accents | |
| **prefix** | headwords that start with the query | |
| **wildcard** | headwords that fit a pattern: `?` is one character, `*` any number of characters; `*rain*` finds headwords that contain *rain*; a capital letter or an accent puts the headwords that have it first | headword index |
| **full-text** | words in the article text, ranked by relevance; phrases, `AND`, `OR`, `NOT`, `NEAR` | full-text index |

Each dictionary's headwords are indexed in the background on its first search.
The full-text index is added per dictionary in the dictionary panel
(<kbd>☰</kbd>), or with `wudict ingest -fulltext`. With lemma
data, an inflected form finds its lemma: *understood* → **understand**.

> [!TIP]
> - <kbd>/</kbd> focuses the search box
> - double-click a word in an article to search for it
> - <kbd>⊞</kbd> expands all results, <kbd>⇔</kbd> widens the layout, <kbd>◐</kbd> cycles the theme
> - the address bar holds the search, so a search can be bookmarked

## Documentation

| | |
|---|---|
| [Install](https://wudict.legbehindneck.com/start/install/), [First run](https://wudict.legbehindneck.com/start/first-run/) | download, folders |
| [Search](https://wudict.legbehindneck.com/start/search/), [Full-text query syntax](https://wudict.legbehindneck.com/reference/fts-syntax/), [Wildcard syntax](https://wudict.legbehindneck.com/reference/wildcard-syntax/) | modes, lemmatization, the dictionary panel |
| [Add dictionaries](https://wudict.legbehindneck.com/dictionaries/add/), [The library](https://wudict.legbehindneck.com/dictionaries/library/) | import, indexing, removal |
| [Picker groups](https://wudict.legbehindneck.com/dictionaries/groups/), [Custom styles](https://wudict.legbehindneck.com/dictionaries/styles/), [Resource overrides](https://wudict.legbehindneck.com/dictionaries/override/) | customization |
| [Android](https://wudict.legbehindneck.com/apps/android/), [macOS](https://wudict.legbehindneck.com/apps/macos/), [Windows](https://wudict.legbehindneck.com/apps/windows/), [Run at startup](https://wudict.legbehindneck.com/running/) | platforms |
| [Access from other devices](https://wudict.legbehindneck.com/access/), [Browser extension](https://wudict.legbehindneck.com/extension/) | network |
| [Configuration](https://wudict.legbehindneck.com/reference/configuration/), [Command line](https://wudict.legbehindneck.com/reference/cli/), [HTTP API](https://wudict.legbehindneck.com/reference/api/), [text.db](https://wudict.legbehindneck.com/reference/text-db/) | reference |

## Build from source

Requires [Go](https://go.dev/doc/install); the default `-cgo` build also needs
a C compiler.

``` sh
make build          # ./wudict (cgo SQLite, built-in Speex decoder)
make check          # tidy, vet, tests
make cross          # -purego binaries for every release platform
make help           # every target
```

Without `make`:

``` sh
go install -tags sqlite_fts5 github.com/wuweidict/wudict@latest   # cgo
go install github.com/wuweidict/wudict@latest                     # pure Go
```

On Windows, `make` is available as `make-<version>-without-guile-w32-bin.zip`
from [ezwinports](https://sourceforge.net/projects/ezwinports/files/); put
`make.exe` in a folder in `PATH`. More: [Building](https://wudict.legbehindneck.com/reference/building/).

## Acknowledgements

Almost nothing here was invented by this project. Some of the formats `wudict` reads are open-source, others are
closed source, and they are available because other people spent years working
on dictionaries and tools to read, write and convert data in these formats.

### Prior art and format knowledge

- **[pyglossary](https://github.com/ilius/pyglossary)** — its plugins are the clearest working description of
  MDX/MDD, StarDict, Slob, DSL and BGL that exists anywhere, and the BGL parser
  in `wudict` is ported from `pyglossary`'s `babylon_bgl` plugin. Thanks to
  **[@ilius](https://github.com/ilius)** and pyglossary's contributors for
  sustained, meticulous work on formats nobody else kept maintaining.
- **[GoldenDict](http://goldendict.org/)** and the actively developed fork
  **[goldendict-ng](https://github.com/xiaoyifang/goldendict-ng)** — the program
  that made these dictionaries worth owning, and the model for the BGL reader's
  streaming decompression.
- **[medict](https://github.com/terasum/medict)** by Quan Chen (formerly
  `go-mdict`) — the MDX/MDD parser in `internal/gomdict` is derived from it.
- **Raul Fernandes** and **Karl Grill** — the original reverse engineering of the
  Babylon BGL format.
- **[slob](https://github.com/itkach/slob)** and Aard 2 by Igor Tkach,
  **[StarDict](https://github.com/huzheng001/stardict-3)**, ABBYY Lingvo's DSL,
  and **[Kiwix](https://kiwix.org/)**'s ZIM.

### Libraries and components

- **[SQLite](https://sqlite.org/)** and its
  **[FTS5](https://sqlite.org/fts5.html)** extension — the entire prepared
  library: storage, headword index, and full-text search across a hundred
  dictionaries at once. Public domain.
- **[mattn/go-sqlite3](https://github.com/mattn/go-sqlite3)** — cgo SQLite
  driver; the default optimized build.
- **[modernc.org/sqlite](https://gitlab.com/cznic/sqlite)** — SQLite translated
  to pure Go, so releases build for every platform without a C toolchain. Both
  drivers are first-class.
- **[Speex](https://www.speex.org/)** — Jean-Marc Valin and the
  [Xiph.Org Foundation](https://xiph.org/). `internal/speex` vendors the
  reference decoder so `.spx` pronunciations play without an external tool, and
  it includes **[kiss_fft](https://github.com/mborgerding/kissfft)** by Mark
  Borgerding.
- **[anchore/go-lzo](https://github.com/anchore/go-lzo)** — LZO1X
  decompression for MDX v1.2 key and record blocks, written from the kernel's format
  documentation and **[lzokay](https://github.com/AxioDL/lzokay)** by Jack
  Andersen.
- **[c0mm4nd/go-ripemd](https://github.com/c0mm4nd/go-ripemd)** — RIPEMD, for
  MDX decryption keys.
- **[cespare/xxhash](https://github.com/cespare/xxhash)** — the MDX v3
  decryption key.
- **[klauspost/compress](https://github.com/klauspost/compress)** — zstd and
  deflate, for ZIM clusters, Slob bins and MDX blocks.
- **[pierrec/lz4](https://github.com/pierrec/lz4)** — LZ4, for MDX blocks.
- **[ulikunitz/xz](https://github.com/ulikunitz/xz)** — LZMA, for Slob and ZIM
  content.
- **[aaaton/golem](https://github.com/aaaton/golem)** — English lemmatiser, so
  that *understood* finds *understand*.
- **[golang.org/x/net](https://pkg.go.dev/golang.org/x/net)** — HTML tokeniser,
  used to rewrite article markup and resolve dictionary resources.
- **[gogpu/systray](https://github.com/gogpu/systray)** and
  **[godbus/dbus](https://github.com/godbus/dbus)** — the tray icon, and its
  Linux desktop integration.
- **[Inno Setup](https://jrsoftware.org/isinfo.php)** by Jordan Russell — the
  Windows installer: small, scriptable, and free for as long as Windows has had
  installers.
- **[Go](https://go.dev/)** — cross-compiles a static binary for eight platforms, Android
  included, from one machine.

Not affiliated with, or endorsed by, any of the above.

## Licence

GPL-3.0-or-later — see [LICENSE](LICENSE).

`wudict licenses` prints the full third-party notices from inside the binary;
the same text is in [THIRD-PARTY-NOTICES.md](THIRD-PARTY-NOTICES.md).

Third-party code included in this repository:

| Component | Origin | Licence |
|---|---|---|
| `internal/gomdict` (MDX/MDD parser) | [medict](https://github.com/terasum/medict), © 2023 Quan Chen | GPL-3.0-or-later |
| `internal/format/bgl` (BGL parser) | ported from [pyglossary](https://github.com/ilius/pyglossary)'s `babylon_bgl` | GPL-3.0-or-later |
| `internal/speex/clib` (Speex decoder) | [Speex](https://www.speex.org/), © Jean-Marc Valin / Xiph.Org, Analog Devices | BSD-3-Clause |
| `internal/speex/clib` (`kiss_fft`) | [kissfft](https://github.com/mborgerding/kissfft), © Mark Borgerding | BSD-3-Clause |

Dependencies fetched at build time keep their own licenses; every one of them,
with its license text, is listed in
[THIRD-PARTY-NOTICES.md](THIRD-PARTY-NOTICES.md) (regenerate with
`make notices`).
