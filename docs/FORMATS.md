# Format notes & reference-code pointers

Read only the section you are working on.

The reference projects cited below (`pyglossary/…`, `mdict-go-web/…`, `draego/…`) are **no longer on disk** — they were removed once the formats were implemented. Their file and function names are kept as provenance: they say where a parsing rule came from and what to search for upstream if a format ever misbehaves, not paths to open.

## MDX / MDD (Octopus MDict)
- Binary, block-compressed (zlib/LZO), optionally encrypted (Salsa20/8 variant), v1.2–v3.0. Has built-in **headword index** (key blocks) — no full-text index. MDD = same container holding resources keyed by `\path\name`.
- Go reader EXISTS and works: `mdict-go-web/internal/gomdict/` — entry points `mdict.go` (`New`, `BuildIndex`, `Lookup`, `Keys`), v3 support in `v3reader.go/v3block.go/v3crypto.go`.
- Plan: inline `gomdict` into the new module. Importer = iterate `Keys()` + locate/decode each record. Resource resolver = existing MDD lookup path. `@@@LINK=` entries become `alias` rows. Embedded `Description`/`StyleSheet` header fields → `meta`.
- Python cross-check: `pyglossary/pyglossary/plugins/octopus_mdict_new/reader.py` (thin; real logic in `pyglossary/plugin_lib/readmdict.py`).

- **Loose sibling assets**: MDict also serves files sitting next to the `.mdx`, which is how repacks ship their stylesheet and scripts (LDOCE6 keeps `LDOCE6.css` and `entry.js` loose, with no `.mdd` at all). `Dict.Resource` falls back to the `.mdx`'s own directory **on a packed miss** — the packed path never touches the disk, and a stat costs ~1 µs against a ~100 µs HTTP round trip, so it is free where it matters. Guards: the ORIGINAL name is used (the index key is lower-cased; `LDOCE6.css` ≠ `ldoce6.css` on a case-sensitive filesystem), the path must stay inside the dictionary's folder, and only asset extensions a dictionary legitimately loads are served — article HTML is third-party, so a stray `href="secrets.env"` must not turn `/res` into a file browser. Loose files are not in `Resources()` (that lists the `.mdd`), so **pack media** adds the ones articles actually *reference*, via `store.ReferencedAssets` — reading the prepared text.db rather than re-parsing the source, and referenced-only because dictionary folders commonly hold several dictionaries and sweeping would pack a neighbour's assets. A packed dictionary therefore keeps its stylesheet and scripts after the source is gone.

## StarDict
- File family: `.ifo` (ini-ish metadata; `sametypesequence`), `.idx`/`.idx.gz` (sorted: word\0 + 32/64-bit offset+size BE), `.dict`/`.dict.dz` (concatenated articles; dictzip = gzip with random-access extra field), `.syn` (synonym → idx-entry index), optional `res/` dir or `res.zip`.
- Article types by `sametypesequence` char: `m` text, `h` HTML, `x` XDXF, `g` Pango, etc. Importer must convert `m`→escaped `<p>`, `x`→HTML (see `pyglossary/pyglossary/xdxf/`), pass `h` through.
- Reference: `pyglossary/pyglossary/plugins/stardict/reader.py` (idx/syn/dict.dz parsing, sametypesequence handling).
- Go effort if done directly at runtime: ~600–900 LOC + dictzip random access. Under hybrid plan only the sequential import path is needed (simpler: gunzip whole .dict.dz streamwise). Resource resolver: plain files in `res/` or zip entries.

## Aard2 Slob
- Single `.slob` file: header + tags + content-type list + sorted **ref** list (key → bin/item) + LZMA2(default)/zlib-compressed **bins** of items. Has built-in headword index; correct binary search requires ICU UCA collation (pyglossary depends on pyicu — a Go runtime port of that lookup is the riskiest piece of any "direct" plan).
- Reference: `pyglossary/pyglossary/slob.py` or `pyglossary/pyglossary/slob/` (container format), `pyglossary/pyglossary/plugins/aard2_slob/reader.py` (tags, `<a href>` rewriting).
- Hybrid plan: importer walks refs sequentially (no collation needed), decompresses bins once (`ulikunitz/xz` for LZMA2). Resource resolver: keep slob open, direct item fetch by stored bin/item ids (store them in `alias`/aux table or resource name map at import).

## Lingvo DSL
- Plain text, UTF-8/UTF-16(+BOM)/UTF-32; optional `.dsl.dz` (dictzip); optional `.ann`, `.bmp`, and `_abrv.dsl`; resources in `<name>.dsl.files.zip` (plain zip). NO index of any kind ⇒ always fully ingested.
- Syntax: headword line(s) at col 0 (multiple consecutive = same card; `{...}` unsorted parts; `(...)` optional-variant expansion; `~` = headword ref; `<<link>>`), body lines indented; tags `[b][i][c][p][m1..9][s]media[/s][ref][url]...`.
- Reference: `pyglossary/pyglossary/plugins/dsl/` — `lex.py` (tag lexer), `transform.py` (→HTML), `title.py` (headword variant expansion), `reader.py` (encoding detect, .dz handling).
- Go: write lexer/transformer fresh, port rules from the above. dictzip read = sequential gunzip (stdlib `compress/gzip` handles dictzip files sequentially). Resource resolver: `archive/zip` over `.files.zip`.
- IMPLEMENTED (P4) in `internal/format/dsl/`. Deviations from pyglossary: malformed/empty tags degrade to literal text instead of dropping the entry (real data contains raw `([ ])`); headword variants are stored raw, not XML-escaped. NOT yet supported: `#INCLUDE` directives, `_abrv.dsl` hover abbreviations, `.ann` annotations. Prepared into a library folder named after the source file (D20); a changed source is detected from the recorded size/mtime/hash and re-indexed in place. `WUDICT_DB_DIR` overrides the library location.

## Babylon BGL (added P10)
- Single `.bgl` file: 6-byte header (`\x12\x34\x00\x0{1,2}` + gzip payload offset), then a **gzip stream with an absent/zero CRC trailer** (treat `ErrChecksum`/short trailer as clean EOF). Payload = variable-length blocks: high nibble = type, low nibble = inline length or the byte-count of a multi-byte length; type 4 = end marker, type 2 = embedded resource, types 1/7/10/13 = standard entries, type 11 = 5-byte-length entry layout.
- Encoding is a resolution chain, not a field: UTF-8 flag → charset code → source/target language code → cp1252 fallback; `<charset c=X>` tags switch encoding *within* a string (incl. Babylon hex references). Definitions carry `\x14`-delimited fields (part of speech, title, transcriptions). Codecs via `golang.org/x/text` (cp125x, cp874, ShiftJIS, GBK, Big5, EUC-KR), lenient decode.
- Reference: `pyglossary/pyglossary/plugins/babylon_bgl/` (most complete parser); GoldenDict for the **streaming** model — never `io.ReadAll` a BGL.
- IMPLEMENTED in `internal/format/bgl/` (`reader.go` streaming core, `decode.go` key/definition processing, `text.go` cleanup helpers, `tables.go` charset/language/POS tables, `bgl.go` backend). No native index ⇒ DSL-style auto-ingest on first open; type-2 resources served from a lazily-scanned map (direct) or packed into `media.db` (native path).

## draego SQLite (legacy, NOT a supported input)
- `word(w TEXT, m TEXT)` + optional `word_fts`. If ever needed: one-off migration SQL into wudict schema. Query-logic audit lives in SPEC §FTS-audit.
