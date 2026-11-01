# MDict semantics

What the engine does with a decoded container: how it finds keys, what it shows, where it
gets resources. Byte layout is in `FORMAT.md`.

## 1. Redirects (`@@@LINK=`)

A record is a redirect when it **begins** with `@@@LINK=` (in the record encoding: as
UTF-16 code units for a UTF-16 dictionary) and is at least 8 units long. 
The target is the rest of the record with trailing CR/LF stripped
(`StripReturn`). Inner text is kept as it is: no entity decoding, no trimming of spaces.
The target is looked up like a typed key (§2, exact spelling preferred among equal folds)
and followed recursively. A redirect to itself fails (error 15); a missing target fails
(error 13). Only one target is followed.

The MdxBuilder sample writes the record as the bare line `@@@LINK=graphic`.

## 2. Keys: folding and order

The key blocks are sorted, and MDict binary-searches them: first among the key-block info
records' first/last keys, then inside the block.

**v1/v2** compare folded strings:

- `StripKey` on: keep only characters that are `iswalpha`, `iswdigit` or above U+007F;
  every ASCII space, punctuation mark and control character is dropped.
  `ice-cream`, `ice cream` and `icecream` are one key.
- case-insensitive (`KeyCaseSensitive` off): lowercase each character (`FastLower`).

Among the keys that fold equal, the one whose raw spelling equals the query exactly is
preferred.

**v3** compares with an ICU collator built from `KeyData@locale` (or the header's
`DefaultSortingLocale`). The corpus uses `es-u-ks-level1-ka-shifted`: strength 1 ignores
case and accents, and `alternate=shifted` ignores punctuation and spaces. MdxBuilder's
default is
`zh-u-co-pinyin-ks-level1-ka-shifted-kr-space-punct-symbol-digit-en-others-hani`.

So a dictionary's sort order is defined by MDict's own comparator. A reader that
binary-searches the key blocks with any other comparison returns wrong or missing results
for keys whose order differs.

## 3. What a record shows (`MdxDict::getDictText`)

The record bytes are decoded from the dictionary encoding (`FORMAT.md` §2.1, §4.2), then:

- **ContentType Text** (an MDX whose header has `Format="Text"` or no `Format` at all;
  v3 `ContentType="Text"`): HTML-escaped by `HtmlEscapeMDXText`. `&` `<` `>` `"` become
  entities, LF becomes `<br>`, the two characters `\n` become `<br>`, and a backslash followed
  by any other character is dropped together with that character.
- **Html**, with `Compact` on: StyleSheet substitution (below).
- **Html**, `Compact` off: used as is. The StyleSheet is not applied even when present.

### 3.1 StyleSheet (`MdxDict::parseFormat`, `MdxDict::reFormat`)

The `StyleSheet` attribute (from MdxBuilder's "style format file") is lines separated by
LF, in triplets:

    token        decimal, 1–255 (atoi)
    begin string may be empty
    end string   may be empty

Every line, the last included, must end with LF, and a token above 255 invalidates the
table. An invalid table is logged ("Invalid style sheet") and the dictionary still opens.
Tokens without a definition have empty begin/end strings.

In the record, `` `N` `` (a backtick, decimal digits only, a backtick) switches the
current style. The text from one marker up to the next marker, or to the end, is emitted
as `begin(N) + text + end(N)`. Text before the first marker gets style 0, whose strings are
empty. A token with no definition therefore keeps its text and loses only the marker.
Whitespace is not touched. *readmdict differs*: it moves a trailing newline outside the end
string as CRLF, and it fails on an undefined token. A literal backtick in a compact source
is written `&#96;` (MdxBuilder manual).

MdxBuilder manual example: with style 1 = `<font size=5>`/`</font>`, 2 = `<br>`/``,
3 = `<font face="Kingsoft Phonetic Plain, Tahoma">`/`</font>`, the source
`` `1`whole&#96;s`2`<p>`3`(hol,hJl; houl) `` renders as
`<font size=5>whole&#96;s</font><br><p><font face="…">(hol,hJl; houl)</font>`.

## 4. Resources (`MdxDict::getDictData`)

A resource reference is normalised twice: to `/dir/name` (backslashes become slashes, a
leading slash is added) and to `\dir\name`. Then:

1. **The loose file `<mdx folder>/dir/name` is tried first, before any `.mdd`**, and if it
   exists it wins. This is how users override a packed stylesheet: drop a corrected `.css`
   beside the `.mdx`. Any file is served; there is no extension allowlist.
2. Otherwise each `.mdd` is searched in load order and the first hit wins. v1/v2 `.mdd`
   keys have the form `\dir\name`; v3 `.mdd` keys have the form `/dir/name`. Lookup is the
   §2 comparison under the `.mdd`'s own header: a v1/v2 `.mdd` is always case-insensitive,
   and `StripKey` defaults to off for `.mdd`.

**Load order** (`MdxDict::setup`): `NAME.mdd`, then `NAME.1.mdd`, `NAME.2.mdd`, …
`NAME.99.mdd`, stopping at the first that is missing or fails to open. The numbered parts
are tried only when `NAME.mdd` itself opened. Lower-case `.mdd` only.

With the app's CSS isolation on, `.css` resources are rewritten under a per-dictionary
prefix (`IsolateCSS`).

## 5. Link schemes in article HTML (`RebuildTagLink`, `ParseMdxUrl`)

Before a link is classified, its control characters (< U+0020) are removed and it is
percent-decoded in the dictionary encoding (`DecodeUrl`).

| Reference | Meaning |
|---|---|
| `#name` | in-page anchor; left alone |
| no scheme, or `file://` | resource (§4); one leading `/` or `\` dropped |
| `entry://key` / `entry://key#section` / `entry://#section` | look up `key` in the same dictionary; `#section` jumps to `<a name>` |
| `entryx://…` | entry link carrying an explicit dictionary/entry id (internal form) |
| `sound://name` | play the resource `name` |
| `source://key` | show the HTML source of `key`'s entry (`source://#` is the current one) |

`bword://` is **not** an MDict scheme; it is Babylon's, kept by GoldenDict for
compatibility. MdxBuilder's sample uses `entry://`, `sound://`, `source://`, `/name` and
`file://name`, with spaces and apostrophes in file names (`sound://sound with's.spx`,
`/1 .jpg`).

## 6. Companion files

| File | Role |
|---|---|
| `NAME.mdd`, `NAME.N.mdd` | resources (§4) |
| loose files beside the `.mdx` | resources, preferred over packed ones (§4) |
| `NAME.mdt` | per-dictionary HTML conversion rules (XML: `UpdateTag`, `InsertTag`, `ConvertAttr`, `ConvertText`, `Char` maps), applied to every article (`HtmlConverter::initWithFile`) |
| `NAME.key` | registration code for a bit-0 encrypted dictionary (`FORMAT.md` §6) |

## 7. Reserved keys

`:About`, `:Title`, `:Info`, `:Verify` and `:LogLevel=…` are engine commands, never looked
up (`IsReservedKeyWord`). `:Info` shows the device id a registration code is issued for.
