# Lingvo DSL — working reference

**Status markers**

| Marker | Meaning |
| --- | --- |
| `[V]` | VERIFIED — read in our code, or observed in a real dictionary/run in this repo. |
| `[D]` | DOCUMENTED, NOT TESTED HERE — stated by a Lingvo reference; no fixture exercises it. |
| `[I]` | INFERRED — reasoned from the format or from adjacent behaviour. Treat as a hypothesis. |

**Sources cited**
- https://github.com/yozhic/DSL-Reference 
- http://lingvo.helpmax.net/en/troubleshooting/dsl-compiler/inserting-pictures-and-sounds/

---

## 1. Containers and companions

| File | Role | wudict |
| --- | --- | --- |
| `<name>.dsl` | main text | `[V]` opened by `dsl.Open` / `dsl.NewReader` |
| `<name>.dsl.dz` | dictzip-compressed main text | `[V]` sequential gunzip via `compress/gzip` (`Reader.init`) |
| `<name>.dsl.files.zip` | GoldenDict's resource archive | `[V]` first source in `MediaSources` |
| `<name>.dsl.files/` | resource folder (also `<name>.files/` for a `.dz`) | `[V]` second source, walked and indexed |
| beside the `.dsl` | loose resources | `[V]` last source, `NewDirExact` — exact paths only, never walked or listed (the folder is not the dictionary's own; other dictionaries live there) |
| `<name>.ann` | annotation/about text, `#LANGUAGE`-partitioned | `[V]` **read live** for the About surface (§1.2); never ingested |
| `<name>_abrv.dsl` | abbreviations shown on hover | `[V]` **absorbed** into the parent at ingest (§1.1) and hidden from discovery |
| `<name>.bmp` | dictionary icon | `[V]` **ignored** (`#ICON_FILE` likewise parsed into the header map and unused) |
| `<name>.lsd` | Lingvo's compiled binary form | `[V]` not supported, not planned |

Resolution order is `MediaSources(srcPath)` in `internal/format/dsl/dsl.go`, and it
is the O8 provider registered for the format, so a *prepared* dictionary reaches its
media from the recorded source path without reopening/reparsing the `.dsl`. `[V]`

A `.dsl.dz` names its resources after either the compressed file or the `.dsl`
inside it; both spellings exist in the wild and both are tried. `[V]`

### 1.1 The `_abrv` companion `[V]`

In Lingvo a `<name>_abrv.dsl` is not a dictionary: it is the glossary that
supplies the expansion shown on hover over a `[p]…[/p]` label ("plural" over
`pl`). wudict follows Lingvo and never lists it.

- **Pairing** — `dict.AbbrevCompanion` / `dict.IsAbbrevCompanion`
  (`internal/dict/companions.go`), name-based, case-insensitive, both `.dsl`
  and `.dsl.dz` spellings. **Orphan rule:** a `*_abrv.dsl` is a companion only
  when a sibling `<stem>.dsl{,.dz}` exists, so a genuine standalone
  abbreviation dictionary keeps working. An explicit path
  (`wudict lookup x_abrv.dsl`) always opens, like an `.mdd`.
- **Hiding** — `dict.Discover` skips companions, and the server's
  `libraryPaths` skips a library folder whose recorded source is one, which
  retires folders prepared by an older build (the folders stay on disk; removal
  is the user's call through the panel).
- **Absorbing** — `internal/format/dsl/abbrev.go` loads the companion with the
  same reader (headword → plain-text expansion, exact key plus a case-folded
  fallback) and `closeLabel` bakes a hit into the article as
  `<abbr class="wudict-abbr" title="…">`. Baking at ingest is what makes the
  tooltip free everywhere: shadow DOM, sandboxed iframe, the Android WebView,
  `-format clean` (both the element and `title=` survive) and `wudict dump`,
  with no client code. A miss emits exactly the pre-existing bytes.
- **Bounds** — companion over 8 MiB ignored; 20 000 keys; 200 runes per
  expansion; self-referential and empty entries dropped. A malformed companion
  is a `-v` note, never an ingest failure.
- **Staleness** — the parent records `abbrev_path`/`_size`/`_mtime`/`_count` in
  `meta` (via the reader's optional `ExtraMeta`), and `store.AbbrevChanged`
  compares them, treating present↔absent as changed. The server re-indexes on a
  mismatch through the background index lane, one dictionary at a time.

### 1.2 The `.ann` annotation `[V]`

A `<name>.ann` beside the dictionary is its editorial blurb — publisher,
edition, copyright, often in several languages at once. It is *display text*:
it is part of no article, so unlike the `_abrv` companion (§1.1) it is **never
ingested, never baked and never a staleness input**. It is read live whenever
someone asks for it, which costs nothing until they do.

- **Path** — chop `.dsl` / `.dsl.dz`, append `.ann`; `.ANN` is tried too, for a
  Windows-authored set on a case-sensitive filesystem. `internal/format/dsl/ann.go`,
  registered as a `dict.RegisterAbout("dsl", …)` provider — a path-only,
  per-format registry (`internal/dict/about.go`) modelled on `resource.Register`.
  An `_abrv.ann` is never read: the companion is not a dictionary, so nothing
  asks it for an About.
- **Encoding** — `decodedScanner` + `detectEncoding`, shared with the
  dictionary reader (§2). Every `.ann` in the reference corpus is UTF-16LE with
  a BOM; CRLF is stripped. Gzip is sniffed from the magic bytes, not the name.
- **`#LANGUAGE` sections — all of them, in file order.** When line 1 starts with
  `#LANGUAGE`, each such line opens a section whose quoted name becomes a
  heading; anywhere else the line is prose, which is how goldendict reads it
  too. Unlike goldendict, **no section is selected by system locale and none is
  dropped**: picking one hides the Russian annotation of a Ru-Ru dictionary from
  a reader running an English UI.
- **Bounds** — 256 KiB cap, truncated with `…` rather than refused; a missing,
  unreadable, empty or heading-only file is simply "no annotation", never an
  error.
- **Where it surfaces** — `GET /api/about?dict=<id>` (same-origin only; not in
  the D69 CORS allowlist, because the response names a file on the user's
  disk), the panel card's **About** disclosure, and `wudict info`. The
  `#INDEX_LANGUAGE → #CONTENTS_LANGUAGE` string the header synthesises
  (`reader.go`) is the **fallback**, shown when a dictionary ships no `.ann`.

## 2. Encodings

`detectEncoding` (`reader.go`), BOM first: `[V]`

| BOM | Encoding |
| --- | --- |
| `FF FE 00 00` | UTF-32LE |
| `00 00 FE FF` | UTF-32BE |
| `FF FE` | UTF-16LE |
| `FE FF` | UTF-16BE |
| `EF BB BF` | UTF-8 |
| none, sample is valid UTF-8 | UTF-8 |
| none, sample is not valid UTF-8 | **UTF-16LE** (the common BOM-less Lingvo export) |

`#SOURCE_CODE_PAGE` names a Windows ANSI code page for files saved as ANSI, and is
required only then; Lingvo ignores it for Unicode files. `[D]` (lingvo-ref
`#SOURCE_CODE_PAGE`) We parse the directive into the header map and **do
not honour it** — an ANSI DSL in a non-Latin code page will decode as mojibake.
`[V]` Fixing it means selecting a `charmap` from the value before building the
scanner in `Reader.init`; the name is case-sensitive in Lingvo ("English", not
"english"). `[D]`

Line ends: `\r` is trimmed per line, a leading `﻿` is trimmed from the first
header line. `[V]`

## 3. Header directives

Syntax: `#` in column 0, key, whitespace (**space or tab** — Lingvo's own samples use
a tab, and splitting on `" "` alone dropped the line), value optionally in `"` or `'`.
`[V]` (`Reader.init`) Directives must sit in the first lines of the file; the first
non-`#`, non-blank line starts the entries. `[D]`/`[V]`

| Directive | Lingvo meaning | wudict |
| --- | --- | --- |
| `#NAME` | dictionary title | `[V]` → `Meta.Name`; falls back to the file base name |
| `#INDEX_LANGUAGE` | language of the **headwords** | `[V]` → `Meta.IndexLang` via `lang.FromDeclared` (absorbs collation names like `SpanishModernSort`), and into `Description` |
| `#CONTENTS_LANGUAGE` | language of the definitions | `[V]` → `Description` only |
| `#SOURCE_CODE_PAGE` | ANSI code page | `[V]` parsed, **not honoured** (§2) |
| `#INCLUDE "path"` | splice another `.dsl` at compile time; `\\` doubled in the path; absolute or relative | `[D]` `[V]` **not supported** — the directive line is swallowed as a header key and its file is never read, so those entries are silently missing |
| `#ICON_FILE` | icon path (undocumented by ABBYY) | `[V]` parsed, unused |
| `#LANGUAGE` | only in `.ann` files, partitions the annotation by UI language | `[V]` every section is kept and shown, none selected by locale (§1.2) |

## 4. Entry-block grammar

`Reader.Next` / `parseBlock`: `[V]`

```
headword line          ← column 0 (no leading space/tab)
headword line          ← consecutive col-0 lines = MORE HEADWORDS FOR THE SAME CARD
<TAB>body line
<TAB>body line
<blank>                ← blank lines are skipped, they do not terminate a block
headword line          ← a col-0 line AFTER body lines starts the next block
```

- Blank lines never separate blocks; a col-0 line after at least one body line does.
  A pushback buffer (`r.buffered`) holds that line for the next `Next()`. `[V]`
- Every headword of a block becomes a lookup key; `terms[0]` is the one `~`
  substitutes in the body. `[V]`
- Scanner limit: 16 MiB per line, 1 MiB start buffer. A longer line fails the read. `[V]`

### Sub-entries (`@`)

Line shape (`atSignHeading`, `reader.go`; mirrors `isAtSignFirst` in
goldendict-ng `src/dict/dsl_details.cc`): `[V]`

```
^[ \t]*(?:\[[^\]]+\][ \t]*)*@
```

- The `@` must be first on the line **after** leading whitespace **and after any
  leading DSL tags** — `[m1]@ heading` and `[m3]@` are legal and appear in real
  dictionaries. Those tags are discarded; they do not become part of the heading.
- The space after `@` is **optional**: `@heading` == `@ heading`.
- Heading text = everything after the `@`, whitespace-trimmed.
- An **empty** heading (`@` alone, tags aside) closes the current sub-card.
- `\@` is not a sub-card line (the regexp cannot reach a `\`), which is also how a
  literal `@` is written inside a body. An unescaped `@` that is *not* first on its
  line is a compile error in Lingvo; GoldenDict warns and keeps it as text; we keep
  it as text silently. `[D]`/`[V]`

**Piled headings.** Consecutive `@` lines with no body line between them are several
headings for **one** card — the discriminator is "have any body lines been seen since
the card opened", not the heading count (`linesInCard`, and `linesInsideCard` in
goldendict-ng `src/dict/dsl.cc`). `[V]`

```
@ dictionary making        ← opens a card
@ dictionary compiling     ← same card, second heading
[m1]body                   ← the card's body
@ next                     ← body seen ⇒ closes the previous card, opens a new one
```

Each sub-entry becomes a **separate `dict.Entry`** carrying every heading's every key
as a headword (heading order, `Full` before `Alt`), and the parent gets one
back-reference line **per key**: `[V]`

```
\t[m2]- [ref]<escaped key>[/ref][/m]
```

The leading `- ` is deliberate: Lingvo and GoldenDict both draw a hyphen before each
sub-card link. `[V]` (screenshots of both, same source) A heading with an optional
part therefore produces **two** links, one per variant — GoldenDict does the same via
`expandOptionalParts` in `ArticleDom` (`dsl_details.cc`), and `{...}` unsorted parts
are stripped from the key by `processUnsortedParts` there and by `transformTitle`
here. `[V]`

Lingvo itself renders the sub-card inline-collapsed and lists its title in the
headword list. `[D]` Same user-visible outcome (title in the index, click to read),
different mechanism.

Not implemented: `~` expansion inside a sub-card heading (GoldenDict calls
`expandTildes(headword, parent)`); ours leaves the `~` literal. `[V]` gap

## 5. Headword grammar

`transformTitle` (`title.go`) returns three strings: `[V]`

| Construct | Full (key) | Alt (key) | Display (HTML) |
| --- | --- | --- | --- |
| plain text | kept | kept | escaped |
| `(...)` optional part | contents kept, brackets dropped | **omitted entirely** | brackets kept, as Lingvo renders it |
| `{...}` unsorted part | omitted | omitted | rendered as DSL markup (a `[']` stress mark, `[s]`, `[br]`…) |
| `{{...}}` comment | omitted | omitted | omitted |
| `\x` | literal `x` | literal `x` | literal `x` |

- `(` does not nest; a second one is literal. `)` outside a paren is literal. `[V]`
- `{...}` may sit **inside** `(...)` and vice versa — one flat loop with an `inParen`
  flag, not a paren scanner, precisely because `(слов{[']}а{[/']}рной)` exists. `[V]`
- Keys get interior whitespace collapsed (`collapseSpace`); Display does not — the
  removal of an unsorted part otherwise leaves a key with a double space nobody can
  type. `[V]`
- Keys are stored **raw**, not XML-escaped (a deliberate deviation from pyglossary);
  only Display is escaped. `[V]`
- When Display differs from the escaped Full, it is prepended to the body as
  `<b>…</b>`, several titles joined by `<br/>`. `[V]`
- `<<…>>` link targets: a target headword's `(...)` part must be given *resolved*
  (`<<вдохновить>>`, not `<<вдохновить(ся)>>`), escaped parens must be reproduced, an
  unsorted `{...}` part must be omitted, and matching is **case-sensitive** in Lingvo.
  `[D]` Ours resolves through the store's own headword lookup, so it is
  case-insensitive and more forgiving. `[V]`

## 6. Tag table

Columns: Lingvo semantics `[D]` unless noted → GoldenDict divergence → our output
(`transform.go`, `processTag`/`closeTag`) → status.

| Tag | Lingvo | wudict output | Status |
| --- | --- | --- | --- |
| `[b]` | bold | `<b>` / `</b>` | `[V]` |
| `[i]` | italic | `<i>` / `</i>` | `[V]` |
| `[u]` | underline | `<u>` / `</u>` | `[V]` |
| `[']` | stress mark on the next vowel | `<u class="accent">` / `</u>` | `[V]` |
| `[c]`, `[c colour]` | colour, default green; bare attribute is the colour name | `<font color="…">` / `</font>`, default `green` | `[V]` |
| `[sup]` `[sub]` | super/subscript | `<sup>` `<sub>` | `[V]` |
| `[m]`, `[m0]`…`[m9]` | left margin, N ems; bare `[m]` = default indent | `<p style="padding-left:Nem;margin:0">`, bare = `0.3em`; `[/m]` → `</p>` | `[V]` |
| `[br]` | line break, **no closing tag**, x5+ | **unknown tag → dropped**; no `<br/>` emitted | `[V]` gap |
| `^` | invert the case of the next character ("перевёртыш"), typically `^~` | not implemented; `^` is literal text | `[V]` gap |
| `[p]` | grammatical/usage label | buffered, then `<i class="p"><font color="green">…</font></i>` at `[/p]` | `[V]` |
| `[t]` | phonetic transcription | `<font face="Helvetica" class="dsl_t">` / `</font>` | `[V]` |
| `[*]` | secondary/optional zone, hidden or shown on demand, grey | `<span class="sec">` / `</span>` (always shown) | `[V]` |
| `@` | sub-card | see §4 | `[V]` |
| `[ex]` | example — search-processing group | `<span class="ex"><font color="steelblue">` / `</font></span>` | `[V]` |
| `[com]` | comment zone | wrapper stripped, content kept | `[V]` |
| `[trn]` `[!trn]` `[trs]` `[!trs]` | include/exclude from translation/transcription indexing | wrapper stripped, content kept | `[V]` |
| `[trn1]` | x5 variant of `[trn]` | unknown tag → dropped, content kept (same visible result) | `[V]` |
| `[lang]`, `[lang id=…]`, `[lang name="…"]` | mark a language span | wrapper stripped, attributes ignored | `[V]` |
| `[s]` | **multimedia zone** — image, sound or video | see §7 | `[V]` |
| `[video]` | undocumented x5 synonym of `[s]` | identical to `[s]` | `[V]` |
| `[preview]` | undocumented, legal only inside `[s]`/`[video]`, no effect | consumed inside the media zone, dropped outside | `[V]` |
| `[ref]`, `[ref target=…]` | link to another headword **in this dictionary** | `<a href="bword://target">text</a>` | `[V]` |
| `<<…>>` | same as `[ref]`, inline form | same as `[ref]` | `[V]` |
| `[url]` | external link | `<a href="…">`, `http://` prefixed when the value has no `://` | `[V]` |
| `{{…}}` | comment, removed before rendering | stripped by `stripComments` before lexing; a comment alone on a line takes the line with it | `[V]` |
| unknown tag | compile error in Lingvo | **dropped, content kept** — pyglossary logs a warning, we do not | `[V]` |

Nesting is by output only: we emit open and close markup as tags arrive and never
build a tree, so unbalanced DSL yields unbalanced HTML rather than an error. `[V]`
The renderers parse into a shadow root or an iframe, where the browser closes it.

### Character-level rules (`transformer.run`) `[V]`

| Input | Result |
| --- | --- |
| `\x` | literal `x` (escapes `[`, `]`, `\`, `~`, `@`, `(`, `)`, `{`, `}`, `<`, `>`, `#`) |
| `\ ` (backslash-space) | `&nbsp;` |
| `\<\<` / `\>\>` | literal `<<` / `>>`, escaped for HTML |
| trailing lone `\` at EOF | literal backslash |
| `~` | the block's first headword, HTML-escaped |
| `]` with no opening `[` | passed through as-is (pyglossary parity) |
| `[[` | literal `[` |
| `[` never closed | the rest of the input as literal text |
| `[]`, `[ ]`, `[/]` | literal text (real articles contain `([ ])`) |
| newline | leading spaces/tabs of the next line are skipped, then `<br/>` — **unless** the next thing is `[m`, whose `<p>` provides the break |
| `<` not followed by `<` | `&lt;` |

Attribute lexing accepts quoted (`'`/`"`) and unquoted values, backslash escapes
inside them, and is EOF-tolerant. A bare attribute with no `=` is recorded with an
empty value — which is how `[c red]` finds its colour. `[V]`

## 7. The media zone in depth

Syntax rules, all `[D]` from lingvo-ref `[s]···[/s]` unless marked:

- The content is **one bare file name with an extension**. Absolute or relative paths
  do not work. The name must fill the whole zone: **no spaces** between the name and
  the delimiters.
- **No other tag may appear inside** — except undocumented `[preview]`, which the x5
  compiler accepts and which does nothing.
- Usable in a headword, but the whole `[s]…[/s]` must then be wrapped in an unsorted
  `{…}` part so the tag does not appear in the headword list.
- Content of media (and link) tags is **excluded from Lingvo's search index**.
- Lingvo packs the files into the compiled `.lsd`; GoldenDict instead reads
  `<name>.dsl.files.zip`.

Formats Lingvo itself supports:

| Kind | Extensions | Notes |
| --- | --- | --- |
| image | `bmp` `jpg`/`jpeg` `tif`/`tiff` `pcx` `dcx` (6.5+), `png` `gif` `wmf` `emf` (x5+) | ≤200 px shown full size, larger ones as a 200 px thumbnail opening in a window; 96 dpi recommended (72 dpi renders a third too large, 300 dpi three times too small) |
| sound | `wav` (the only one ABBYY documents; AC3-compressed allowed), `wav` written by WaveMP3 (stripped MP3, no Unicode meta tags), `asf` (hands off to the system player) | shown as a speaker icon |
| video | `avi` only — **MP4 must be renamed to `.avi`**; GoldenDict also plays FLV renamed `.avi` and WMV as `.wmv` | shown as a camera icon |

### What wudict emits `[V]`

`mediaExt` + `lexTagS` in `internal/format/dsl/transform.go`. The extension is
lower-cased through `path.Ext`; the four kinds are exhaustive — **every payload
renders something**.

| Kind | Extensions | HTML |
| --- | --- | --- |
| audio | `wav mp3 ogg spx m4a` | `<a class="wudict-audio" href="NAME">🔊</a>` (`&#128266;`) |
| image | `bmp gif ico jpeg jpg png svg tif tiff webp avif` | `<img align="top" src="NAME" alt="NAME" />` |
| video | `mp4 webm ogv mov m4v 3gp` | `<video class="wudict-video" controls preload="none" src="NAME"></video>` |
| file | **everything else**, extension-less names included | `<a class="wudict-file" href="file://NAME">📄 NAME</a>` (`&#128196;`) |

Design points that are not obvious and should not be "simplified" away:

- The video list is **browser-playable formats only**. Lingvo's `avi` and
  GoldenDict's `wmv`/`flv`, plus `mkv mpg mpeg asf`, take the file link: an inline
  `<video>` for a codec no browser decodes is a permanently broken player, whereas a
  link reaches the system player — which is what Lingvo does with them anyway.
  Same reasoning puts Lingvo's `pcx dcx wmf emf` images in `file`, not `<img>`.
- `preload="none"` — a card may hold several tens-of-megabytes clips; nothing is
  fetched until the reader presses play.
- The file link uses the **`file://` pseudo-scheme**, and only there. The article
  rewriter treats `sound://`/`file://` as "the author naming their own file" and
  rewrites regardless of extension (§9), so a container-owned `.pdf` is reachable
  **without** widening `dict.IsAssetName`, which is also the allowlist for files
  lying loose beside an `.mdx`. Keep those two concerns apart.
- Audio is an `<a>`, not GoldenDict's `<object type="audio/x-wav">`: the anchor
  survives `format=clean`, is understood by both renderers, and needs no inline
  handler.
- The name goes through `quoteAttr` in attributes and `escape` in text; a name
  containing `"` or `&` cannot break out. Covered by `TestTransformMediaKinds`.
- An empty zone (`[s][/s]`) emits nothing and records no resource. `[V]`

## 8. Escaping and metacharacters — deviations we keep

| Case | pyglossary | wudict | Why                                                                             |
| --- | --- | --- |---------------------------------------------------------------------------------|
| malformed/empty tag (`[ ]`) | drops the entry | literal text | real articles contain `([ ])`                                                   |
| headword variants | XML-escaped | raw | they are lookup keys; escaping breaks matching                                  |
| unknown tag | warning logged | silently unwrapped | a warning per article is noise at 100+ dictionaries scale                       |
| unterminated `{{` | — | literal text | a stray `{{` in a body is a typo, not a request to delete the rest of the entry |

`dslEscape` (`reader.go`) escapes `\ [ ] ~ < > @` when a sub-entry key is embedded
back into generated DSL. It was written with doubled raw strings (`"["` → `` `\\[` ``)
and therefore emitted **two** backslashes, so any sub-headword containing one of those
characters produced a corrupted back-reference
(`dslEscape("a[b]~c")` = `a\\[b\\]\\~c` → `<a href="bword://a\">a\</a>\Kc`).
Fixed; `TestDslEscapeRoundTrip` pins the round trip. `[V]`

## 9. Pipeline map

```
.dsl bytes
  └ internal/format/dsl/reader.go   detectEncoding → header → blocks → parseBlock
      └ title.go       transformTitle   headword → Full / Alt / Display
      └ transform.go   transformBody    body → HTML   (+ resFiles: names referenced)
          └ store.IngestPlan → <db dir>/<name>/text.db      (headwords only by default, D24)
               └ media pack (opt-in) → media.db             (store/media.go, IngestMedia)
  └ query: store.Store lookup → entry HTML
      └ internal/server/rewrite.go  RewriteEntryHTML(html, dictID)
          · htmlref tokenizer walks every reference site
          · fetch sites (img/video/audio/source src, link href) → always rewritten
          · <a href> → rewritten only when isResourceRef: sound:// or file://
            pseudo-scheme, or dict.IsAssetName(ref) by extension
          · resURL strips the pseudo-scheme → /res/{dictID}/{name}
      └ internal/server/articleformat.go   format=clean|text (raw is the default and
        what the built-in UI requests; clean keeps <video src|controls|preload> but
        drops class attributes, so class-keyed renderer branches do not apply there)
  └ GET /res/{dict}/{name}  internal/server/server.go handleResource
      · serveOverride first  (<library folder>/res/<name>, user replacements)
      · d.Resource(name) → format backend or media.db
      · .spx → transcoded to WAV in-process (D18)
      · webMIME override table, then the backend's own MIME
      · io.ReadSeeker → http.ServeContent (Range, 206, Accept-Ranges)
        otherwise → io.Copy through nulWatcher (text resources; damaged-blob warning)
  └ renderers
      · internal/server/web/index.html — shadow DOM, document click dispatch:
        parseRef → audio extensions → <img> link → .wudict-file → http(s) → "#" → bare word
      · internal/server/web/frame.js  — sandboxed srcdoc iframe, capture-phase click:
        same order; .wudict-file and external links post {t:"open", url} to the parent,
        which opens a tab for http(s) and for /res/ paths
```

Seekability matters end to end: `resource.Dir` returns an `*os.File` (seekable), a zip
entry is **not** seekable, and `store.Media.Resource` returns
`readSeekNopCloser{*bytes.Reader}` — `io.NopCloser` would hide `Seek` and silently
disable ranges for every packed dictionary. `[V]`

## 10. Resource-name matching (`internal/resource`)

The article's name is text; the container's name is bytes. So each stored entry is
indexed under **every plausible reading** of its bytes and the article selects one —
no guess about the container's code page is made. `[V]`

- `Key(name)` = `Clean` (backslashes → `/`, no leading `/` or `./`, no `..` escape) +
  NFC + lower case. Case and normalization are folded there and nowhere else.
- `readings(raw, utf8Declared)` adds a key per legacy code page that decodes without
  U+FFFD (cp1251, cp1252, cp866, KOI8-R, cp1250, cp1253–1258, ISO-8859-2/5/7, cp437,
  cp850). Display name is chosen by `score`, which judges whole words, not runes
  ("café" vs "ÊÓÁÎÊ").
- `Index.Lookup` falls back to the **basename** when the full path misses, unless that
  basename is ambiguous (`dupe`) — this is what lets an article say `кубок.jpg` when
  the zip stores `files/кубок.jpg`.
- `Dir.Open` tries the original spelling first (case-sensitive filesystems), then the
  index. `NewDirExact` (loose files beside the `.dsl`) skips the index entirely.
- `IsJunk` removes `.DS_Store`, `._*` AppleDouble shadows, `__MACOSX/`, `Thumbs.db`
  at any path depth, case-insensitively.
- `store.Media.Resource` does its own folding, since media.db is queried by SQL:
  exact → `COLLATE NOCASE` → NFC → NFD. macOS hands out NFD filenames while the
  article says NFC; `COLLATE NOCASE` folds case only. `[V]`

## 11. Discovered while fixing

Fixed (sub-card pass):

 A. `@` was recognised only as the exact line `@` or the prefix `@ ` **after**
    `TrimSpace`. Consequences, all three reported from real DSL: `@heading` (no space)
    was swallowed as body text; `[m1]@ heading` was not a sub-card at all; and a pile
    of `@` lines produced one **empty** card per heading but the last. `[V]`
 B. Piled headings now share one card, keyed on "body lines seen since the card
    opened". `[V]`
 C. One `- [ref]` line per expanded key, not one per card: a heading with an optional
    part contributes both variants, matching Lingvo and GoldenDict. `[V]`
 D. `dslEscape` double-escaping (§8). `[V]`

Fixed (media pass):

1. `[s]` rendered **only** audio and browser images; every other extension fell
   through the switch and emitted **nothing** — the name was recorded in `resFiles`
   and the reader saw a blank gap. `[s]video.mp4[/s]` and `[s]español.pdf[/s]` were
   the reported symptom. `[V]`
2. `[video]` and `[preview]` were unknown tags: `[video]x.mp4[/video]` printed the
   file name as prose, `[s][preview]x.avi[/preview][/s]` would have asked the
   container for a file named `[preview]x.avi`. `[V]`
3. `dict.IsAssetName` had no `.pdf`, `.avi`, `.wmv`, `.mkv`, `.mpg`, `.asf`, `.flv`,
   `.pcx`, `.dcx`, `.wmf`, `.emf`, `.mov`, `.m4v`, `.3gp`, so `isResourceRef` read
   `<a href="español.pdf">` as a **cross-reference** and never mapped it to `/res/`.
   Extensions added; the `file://` scheme covers the rest without touching the MDX
   loose-file boundary. `[V]`
4. `/res/` streamed with `io.Copy`: no `Accept-Ranges`, no 206. A 13 MB MP4 could not
   be seeked and Safari/iOS refuse to start playback at all without ranges.
   `http.ServeContent` on the seekable path. `[V]` (verified: `206` +
   `Content-Range: bytes 0-1023/13681040`)
5. `store.Media.Resource` returned `io.NopCloser`, hiding `Seek` from the type
   assertion — packed dictionaries would have kept the no-range behaviour. `[V]`
6. `media.db` name lookup folded case but not Unicode normalization: `español.pdf`
   is stored NFD (macOS filesystem) and referenced NFC (the article). `[V]`
7. `webMIME` had no entry for the containers now served (`avi wmv mkv m4v flv asf
   pcx dcx wmf emf`). `[V]`
8. The parent page's `{t:"open"}` handler accepted only `http(s)` URLs, so a file
   link posted from the sandboxed iframe went nowhere; `/res/` paths are now admitted
   (path-absolute, fixed first segment — no scheme to smuggle, `//host` cannot match).
   `[V]`

Open, **not** fixed here — each with what correct behaviour would be:

| Gap | Correct behaviour |
| --- | --- |
| `[br]` | emit `<br/>`; one `case tag == "br"` in `processTag`, no close tag. `[V]` gap |
| `^` command | invert the case of the next character; matters mostly as `^~` (mirrored headword at the start of a sentence). Needs a rune-aware branch in `run`, not a byte one. `[V]` gap |
| `#INCLUDE` | read the referenced `.dsl` (relative to the including file, `\\` unescaped, `\` → `/`) and continue the block stream through it; guard against cycles and absolute Windows paths. Entries are currently missing with no diagnostic. `[V]` gap |
| `#SOURCE_CODE_PAGE` | select a `charmap` decoder for BOM-less ANSI files (§2). `[V]` gap |
| `[lang id=…]` | could become `<span lang="…">` for hyphenation/voice selection; attributes are currently dropped. `[V]` gap |
| `[trn1]` | behaves as an unwrapped unknown tag; harmless today, but it should be listed with the other search-processing wrappers so the intent is explicit. `[V]` gap |
| `~` in a sub-card heading | expand to the parent's first headword before `transformTitle` (goldendict-ng `expandTildes`); currently literal. `[V]` gap |
| `[*]` secondary zone | always rendered; Lingvo hides it behind a toggle. A `details`-like control would match the format's intent. `[V]` gap |


