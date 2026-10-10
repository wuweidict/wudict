# wudict — Architecture Spec (v0.2, Phase 0)

## 1. Core idea

**Dual-backend** (Decision D1, user-directed): every dictionary is served through one `Dictionary` interface with two interchangeable backends:

1. **Direct backend (default, implicit)** — reads the native format at runtime using its built-in index. Zero import step, original files are the only storage. Capabilities: exact + prefix headword lookup, article render, resources.
2. **Prepared backend (the "native" mode — D15)** — canonical SQLite databases built from the source, in the dictionary's library folder (D20). Always: headwords, aliases and articles in `text.db`, which every headword mode needs. Opt-in per dictionary (D24): the contains (trigram) index, the full-text (FTS5) index, and the binary resources packed into `media.db` (§3); without `media.db`, resources stay lazy from the source.

```
config: AUTO_INDEX = on | off            (prepare headwords on first search, D13)
        per dictionary: contains · full-text · media

              runtime
  ┌ Direct backend ──── format reader (built-in index) ─┐
  │                                                     ├─ unified Dictionary API ─ server/UI
  └ Ingested backend ── <db dir>/<name>/text.db [+ media.db]┘
              resources: media.db if present, else lazy from source file
```

- **Capability tiers**: backends advertise `Caps.Exact/Prefix/Contains/FullText` (D16 renamed `Fuzzy`→`Contains`). the panel shows contains/full-text/media as per-dictionary switches with their measured sizes (D24), and a search mode a dictionary lacks offers to add it inline; the headword index is prepared on its own (D13, `AUTO_INDEX = on|off`).
- **D15 (2026-07-25)**: the ingested pair is the *native* mode and direct readers are *preview*. Read D15 before optimizing anything on the direct side.
- **DSL/BGL/wudict markdown exception**: no native index ⇒ opening one prepares its library folder first (`store.OpenSelfPrepared`) — with the same cheap plan as any other format (headwords + article text stored so it is readable at all; full-text and contains stay opt-in, D24). Its resources (`.files.zip`) stay lazy.
- Sources are never *modified*: nothing writes into a dictionary file. They can be **deleted**, but only by an explicit removal (D63 amended: `DELETE /api/library`, `wudict rm`) — never as a side effect of ingest, search or cleanup. `meta` stores source size/mtime/hash; changed source ⇒ stale-DB prompt (re-ingest or fall back to direct).

### Direct-backend feasibility notes (why this is OK)
- MDX/MDD: solved — `gomdict` already does indexed lookup incl. MDD resources.
- StarDict: load `.idx`(.gz) into memory (few MB), binary search with StarDict's documented ordering (`g_ascii_strcasecmp`, then `strcmp`); articles via dictzip random access. ~600–900 LOC.
- Slob: refs loaded into memory at open; lookup via normalized comparison using `golang.org/x/text/collate`/`unicode` normalization instead of ICU UCA binary search (we search our own in-memory copy, so we don't need byte-exact ICU ordering). Risk: RAM for huge slobs (Wikipedia-scale, millions of refs) — mitigate later with a sidecar key cache if it bites.
- Contains/full-text is structurally impossible over native indexes — that's what the ingest tier is for.
- Since D15/P11 the direct headword maps (exact **and** accent-folded) are built **lazily on first use** (`sync.Once`), not at Open: resource-only, ingest-scan and dropdown opens build none. Reference runtime readers (aard2 `Slob.java`, GoldenDict) keep no in-memory index at all; pyglossary does only because it is a converter.

## 2. Canonical text DB (`<library folder>/text.db`, wudict format v1)

```sql
PRAGMA user_version = 1;
CREATE TABLE meta(key TEXT PRIMARY KEY, value TEXT);
  -- dict_uuid, name, format(mdx|stardict|slob|dsl|bgl|zim|wmd), source_path, source_size, source_mtime,
  -- source_sha256_1M, entry_count, lang_from, lang_to, description, css, js, ingest_level,
  -- ingest_version, reader_version (D151: which code built it; absent = 1)
CREATE TABLE entry(id INTEGER PRIMARY KEY, w TEXT NOT NULL, m TEXT NOT NULL);
  -- w = display headword; m = article HTML (converted from native markup at ingest)
CREATE INDEX idx_entry_w ON entry(w COLLATE NOCASE);
CREATE TABLE alias(w TEXT NOT NULL, entry_id INTEGER NOT NULL REFERENCES entry(id));
  -- StarDict .syn, MDX @@@LINK, Slob aliases, DSL variant/sub-headwords
CREATE INDEX idx_alias_w ON alias(w COLLATE NOCASE);
CREATE VIRTUAL TABLE entry_fts USING fts5(
  w, txt,                       -- txt = tag-stripped plain text of m (NOT raw HTML — FTS-audit #1)
  content='', columnsize=0,     -- contentless; rowid = entry.id
  tokenize='unicode61 remove_diacritics 2'
);
CREATE VIRTUAL TABLE entry_trigram USING fts5(   -- D16: "contains" substring mode
  w, content='', columnsize=0, tokenize='trigram'
);                              -- w stored dict.Fold-ed (accent/case-insensitive)
```

`entry_trigram` was added without a `user_version` bump: it is **feature-detected at Open** (`SELECT … FROM sqlite_master`) and flagged in `meta.has_trigram=1` for the cheap list path, so pre-D16 `.text.db` files keep opening and merely lack contains until re-ingested.

## 3. Media DB (`<library folder>/media.db`, only at ingest=full)

Separate file by design (user-directed): text DBs stay small and exchangeable without dragging multi-GB audio packs; media DB is optional at runtime.

```sql
PRAGMA user_version = 1;
CREATE TABLE meta(key TEXT PRIMARY KEY, value TEXT);  -- dict_uuid (MUST match text.db), name, source_*, media_version
CREATE TABLE resource(name TEXT PRIMARY KEY, mime TEXT, data BLOB);
```

- Packing includes `.mdd` resources **plus** the loose sibling files articles reference (`store.ReferencedAssets` scans the prepared bodies for `href/src/data/poster`, dropping schemes, fragments, absolute URLs and anything containing `..`). Lookup falls back to `COLLATE NOCASE`: MDD names are indexed lower-cased while loose files keep their real spelling.
- Pairing: identical `dict_uuid` in both `meta` tables; loader warns on mismatch, refuses cross-dict pairs. A rebuild from the same unchanged source reuses the text.db's `dict_uuid` (`keptUUID`), so its media.db stays paired; a changed source gets a new one and its media is repacked (D151).
- Resolution order at runtime: `media.db` → source file resolver → 404. A text.db shipped alone still works fully for text (graceful degrade); receiver can point it at their own copy of the source for media.
- Naming: D9's `<slug>.text.db`/`.media.db` pair is superseded by D20's folder — `text.db` and `media.db` inside a folder named after the source file. Loose `<name>.text.db` files still open (a database copied out of its folder).
- A prepared **folder** is a valid path argument anywhere a file is (`dict.MainFile`, D90): it is the unit the library lists and the user copies, while dispatch is by base name and extension, which a folder has neither of usefully. `wudict info <db dir>/es_es_DLE_v23.8_1_RealAcademia2026` reported `unsupported dictionary format: .8_1_RealAcademia2026` — `filepath.Ext` reading a version number as an extension. `Open`, `Probe` and `OpenReader` resolve a directory to a registered bundle main file (`text.db`) only after extension dispatch has failed, so an ordinary open costs no `stat`, and a folder holding no main file is still not a dictionary.

### Storage of article bodies (D24)
`entry.m` is DEFLATE-compressed per row, marked by a leading NUL byte (article text never begins with one), so compressed and literal rows coexist and pre-D24 databases open unchanged. Bodies under 120 bytes, and any body compression fails to shrink, are stored literally. `NO_COMPRESS` turns it off for new ingests; reading always understands both. Measured: 3.8x smaller on a 40k-entry dictionary, where article text was 95% of the file.

### What is built, and when (D24)
`store.Plan{FullText, Contains}` decides which indexes exist. Finding a headword — exact, prefix, accent-insensitive — needs no switch (~2 MB, every backend can do it). `entry_trigram` is created only when `Contains` is asked for; `entry_fts` indexes article text only when `FullText` is. The server's `entry.setFeatures` brings a dictionary to a requested state, rebuilding the index when the wanted indexes differ, packing or deleting media, and refusing outright when the source file is gone (the prepared data is then the only copy).

## 4. Query engine (ported from draego, fixed)

Modes (D16): **exact**, **prefix** (default; starts-with, accent-insensitive — the store's internal `fuzzy` is its fold engine), **contains** (substring/typo, `entry_trigram` MATCH on folded `w` for ≥3 chars, raw escaped `LIKE` below that), **full-text** (FTS on `w`+`txt`, BM25, read as a phrase, then near, then the words anywhere — `internal/ftsq`), each × single-dict / all-dicts. `fuzzy` survives only as a legacy `search.ParseMode` alias → prefix. All-dicts fans out concurrently (bounded goroutines) across *both* backend types — direct dicts contribute exact/prefix results, prepared ones the full set; grouped per dictionary in the accordion. Prefix falls back to a diacritic-folded FTS prefix when the raw `LIKE` finds nothing (accent-insensitive prefix parity with the direct backends). **Streaming (P8, D12)**: `search.StreamOpen` emits each dictionary's `Hit` as it completes (serialized), so the server can flush results progressively in the caller's preference order instead of waiting for the whole fan-out. `search.Query` is the same per-dictionary query for one dictionary, which is what the CLI's `lookup`/`prefix`/`contains`/`fts` run, so the terminal and the app cannot answer one question differently.

**Cancellation (D158).** A prepared database answers through `dict.Searcher`, whose every query takes the request's context: the UI abandons a search on every keystroke, and a cancelled context interrupts the query inside SQLite (both drivers map `sqlite3_interrupt` to `ctx.Err()`). A direct backend has nothing to interrupt; its query is abandoned, not waited for.

### Lemmatization — the second wave (D82, O3-M1)

**Every dictionary that returns nothing** is retried once with the query's dictionary form:
`knew`→`know`, `fuiste`→`ser`, `идет`→`идти`. Nothing else triggers it — a dictionary that hit,
or that was deferred, errored or lacks the index this mode needs, is left out, so a dictionary
sends either a first-wave hit or a lemma hit and never both. That is the whole simplification:
within a dictionary there is no result set to rank against, so no derived hit can displace an
exact one and no rank field has to be threaded through the NDJSON path; and a misdetected
language costs one failed index probe on a dictionary that had already failed, because every
candidate is validated by the real headword index.

The gate is per dictionary rather than per search (D89). It was per search, and that made
lemmatization depend on which unrelated dictionaries happened to be installed: a Babylon glossary
carrying `estuviera` as a hand-written alias of `estar` answered the first wave, and every proper
Spanish dictionary beside it — each indexing lemmas only, each of which would have answered
`estar` — was never asked. The cost is that a pack can now load on a search that already partly
succeeded; it stays bounded by `MORPH_CACHE`, and the retry still reaches only the dictionaries
that returned nothing.

**Language attribution** (`internal/lang`) is the only input, resolved per dictionary in one order:
what the dictionary itself declares (`Meta.IndexLang` — DSL's `#INDEX_LANGUAGE`, BGL's source
language; persisted at ingest as `meta[index_lang]`), else a **prefix** of the file stem
(`en-es-apresyan.mdx`, `eng_eng_x.mdx`, `russian-apresyan.mdx`), else an **exact, case-insensitive**
ancestor folder name up to the configured dictionary root (`Spanish/`, `ru/`), else the
dictionary's own **title**, else **English**.

The title is free text, so it is read by different rules than a file name (`lang.FromTitle`): the
**first** English language name written as a whole word (`Dahl's Russian Dictionary` → ru,
`Larousse Compact English-Spanish` → en, `JM Latin-English Dictionary` → la), or the first language
code the title marks *as* a code — one half of a pair or alone in brackets (`(Ru-Ru)`, `(rus-rus)`,
`(En-Ru)` → the first half; `[ru]`). A loose two- or three-letter token is never read as a code,
because `is it no be am he la id ta pa ne ms ka ha my` are all ISO codes and ordinary English words
(`A la carte` is not Latin). Failing all of that, a title with more Cyrillic letters than Latin
ones is Russian (`Брокгауз и Ефрон`) — script is consulted last and for one language only, so
`Apresyan (En-Ru)` is still English.

English is the one language assumed without evidence — the smallest pack by a wide margin, and
wrong costs one probe; every other language must be stated, so a rename is the fix. Path-derived
answers are never persisted, so a rename takes effect at the next open with no re-ingest.

Candidates come from `internal/morph` (`aaaton/golem/v4`, pure Go), and are
offered **only to the dictionaries of that language**, which is what stops Spanish `sale`→`salar`
being asked of an English dictionary. Packs are loaded on first use and held in a count-based LRU
sized by `MORPH_CACHE` (desktop 2, Android 1, `0` disables the feature); a pack costs 7 MB (en) to
65 MB (ru), so nothing is loaded at startup. Russian additionally retries one `е`→`ё`
substitution, because golem's ru pack is keyed on the `ё` spellings.

**Only English is compiled in** (D87, `internal/morph/file.go`). Every other language is a
file in `LEMMA_DIR` (default `~/.wudict/lemmas`) named after its language — `pl.txt`, `pol.tsv`,
`polish.txt.gz`, resolved through `lang.Normalize` — loaded through the same LRU; an `en` file
replaces the built-in English rather than merging with it. golem takes a `LanguagePack` interface,
so the backing is a file instead of a generated Go const and nothing else changes. English is the
exception because it is the language assumed when a dictionary declares none, so it must answer
with an empty folder; dropping the other five took the binary from 29.3 MB to 20.4 MB. The
on-disk format is golem's own (lemma first, tab-separated forms, lower case), which is also the
shape of the raw lists at michmech/lemmatization-lists, so those load as downloaded. Two
things are done to the bytes on the way in,
because golem is stricter than a hand-edited file can be trusted to be: they are lower-cased (only
`LemmaLower` is ever called) and lines with fewer than two fields are dropped (**one** such line
otherwise fails the entire load, costing a language). Decompressed size is capped at 64 MB — five
times the largest language golem publishes — since golem's map costs 6-10x its input in heap and a
compression bomb
in that folder would otherwise be an OOM with no diagnostic.

`wudict lemmas list | download | remove` is how a user obtains those files (D88,
`internal/lemmas`, `internal/cli/lemmas.go`). `make lemma-files` is not: it needs a Go toolchain
and a populated module cache, and someone who downloaded a release binary has neither. The client
knows exactly one URL — `LEMMA_URL`, a `manifest.json` listing each language's file name, byte
size, sha256 and *measured* heap cost — and every asset sits beside it, named by a bare file name.
sha256 is the entire trust model, so the transport is interchangeable: a mirror, a proxy or a
folder on a USB stick all work, and nothing pins a host. Every manifest field is treated as
hostile — the code must survive `lang.Normalize`, the file name must be a plain base name with a
known extension, the declared size must be within `morph.MaxPackBytes`, the manifest body is read
through a 1 MB limit, and the **local** name is constructed as `<code><ext>` rather than taken
from the catalogue, so a broken or malicious manifest cannot choose where wudict writes. A
download streams to a dot-prefixed temporary in the destination folder — a name `scanDir` cannot
match — and is renamed only after its length and digest both agree, so an interrupted install is
invisible rather than half-applied. Enumerating a repository through a code-hosting API was
rejected: the unauthenticated GitHub API is 60 requests per hour *per IP*, which fails for
everyone behind one NAT, and a static file has no such limit.

`tools/lemmafiles` builds that catalogue, from michmech/lemmatization-lists rather than golem's Go
modules: 24 languages instead of 8, no module cache, no coupling to generated Go source. It
groups the pair-per-line lists the way golem's own `cmd/simplify` does, loads each result through
golem as an acceptance test, measures its heap with `runtime.ReadMemStats`, and writes
`<code>.tsv.gz`, `manifest.json` and the ODbL `ATTRIBUTION.txt`. `internal/morph.Cache.Rescan()`
re-indexes the folder so a language installed under a running process becomes searchable without
a restart; packs already loaded are left alone. The CLI does not call it — it is a different
process from a running server, and says so.

The stream carries `{"t":"morph","from":…,"to":…,"lang":…}` before that language's hits, and the
hits are **buffered** until at least one is non-empty — "showing results for X" over an empty list
would be worse than silence. Multi-word queries are never lemmatized.

### FTS-audit — bugs found in draego/drae.go; fix when porting
1. FTS indexes raw HTML ⇒ markup tokens pollute matches/rank. Index stripped text.
2. `sanitizeFts` only escapes `"` — `-`, `*`, `^`, `:`, parens, `OR/AND/NOT/NEAR` reach MATCH ⇒ syntax errors/operator surprises. Wrap every token as `"tok"*`.
3. `ensureFtsIndex` swallows CREATE errors (`return false, nil`); population non-transactional; index built lazily on first request (stall). Build only at ingest time, in a transaction.
4. Fuzzy `ORDER BY w` discards rank — keep for headword mode, use `ORDER BY rank` for full-text (deliberate, documented).
5. `LIKE` with raw user input — escape `%`/`_` + `ESCAPE '\'`.
6. Per-request `sql.Open`+PRAGMA churn (CGI artifact). Persistent server, pooled read-only handles.
7. `max` unbounded — clamp.
8. Headword printed unescaped in single-dict mode — escape all reflected text.

## 4b. Intake — an archive becomes dictionaries (D128/D129, `internal/intake`)

Discovery (§1) starts at *files already sitting in a scanned folder*. Intake is the stage
in front of it: arbitrary bytes — an uploaded or shared `.zip` or `.7z`, a plain `.mdx` /
`.ifo` / `.dsl` / `.slob` / `.bgl`, or any of those downloaded from a link — become
installed dictionaries that discovery then finds, with no file manager and no unpacking by
hand. It stops at rescan; preparation and indexing downstream are untouched.

**Stages.** Acquire → **Sniff** → *user confirms* → Extract → rescan → Dispose.

A 7z is read **strictly in archive order on one goroutine**. That is a format fact, not a
style: a 7z *folder* is one compressed stream holding several files end to end, so reading
them in stored order is linear, and reading them in any other order means decompressing
the folder again from its start for each file. Extraction therefore sorts a candidate's
files by their position in the archive; a zip is indifferent to the order, so one rule
serves both. Peak memory is one LZMA2 dictionary: when extraction leaves a folder that
holds more than one file, the archive is reopened, which is what drops the decompressor
the reader pooled for it.

- **Sniff decompresses nothing.** A zip answers "what is in here" from its central
  directory (a seek to the tail) and a 7z from its header, so listing a 4 GB archive
  costs no decompressor and no memory surprise. A 7z declares no per-file *compressed*
  size — only a packed size per folder — so the ratio test that catches a zip bomb has
  no input there; what bounds a 7z instead is the write-time check every format gets,
  which reads one byte past the declared size and refuses the entry that exceeds it. The grouping rule is `internal/dict`'s, not a second implementation of
  it: a **candidate** is one main file plus every companion sharing its stem *in the same
  archive directory*, with a StarDict `res/` subtree handed to the sole dictionary in its
  folder (the archive reading of `soleIfoInDir`). A candidate missing a required companion
  — a lone `.ifo` without its `.idx`/`.dict` — is reported **incomplete and not
  installable**, never imported into a broken state. `.mdd` and `_abrv.dsl` travel as
  payload and are never offered as dictionaries of their own.
- **A loose dictionary file is an archive with no compression** (D135). These formats are
  not always *handed over* as bundles: a link points at `oxford.mdx`, a share sheet carries
  one file, a download folder holds the `.mdx` and the `.mdd` because the site offered them
  as two links. Rather than a second pipeline, such a file is presented through the same
  `Archive` interface as "this file plus the same-stem siblings in its directory" — which is
  dict's folder rule — so grouping, completeness, the already-installed check, staging and
  the atomic rename are all the code that was already there. The file may be the main one or
  a companion: downloading `oxford.mdd` after `oxford.mdx` is handing over the second half of
  the same dictionary. A file belonging to no dictionary is refused as unreadable, not
  reported as an archive holding nothing. Because the **name is the dictionary's name**, a
  streamed upload is spooled into a per-upload `incoming-*` directory under its real name
  rather than a temporary one — which also keeps one upload's sibling scan out of another's —
  and disposal removes what was actually consumed: "delete the source afterwards" takes the
  `.mdd` along with the `.mdx`, and takes the whole spool directory for an upload.
- **Every input is hostile.** Names that are absolute, climbing, null-bearing,
  drive-lettered or too deeply nested are rejected *at sniff*, so a hostile entry is never
  even listed to the user; extraction re-checks the canonical destination before it writes.
  Entry count, per-candidate size and compression ratio are capped, and each entry is
  written through an `io.LimitReader` of its declared size so a lying header truncates
  instead of filling the disk.
- **Staging is `<dict dir>/.wudict-intake/<jobid>/`** — inside the destination on purpose,
  so installing is an atomic `os.Rename` and not a second full copy. It is deliberately not
  `TMPDIR`: on Android that is the cache partition, which would make a 2 GB extraction both
  a cross-device copy and a partition overflow. Discovery skips hidden subtrees, so a
  racing scan cannot see a half-extracted dictionary.
- **One job at a time, globally**, so extraction and the indexing it triggers stay
  sequential by construction and peak RSS is bounded by one dictionary.
- **Cleanup has three layers**: the job directory unwinds through one removal on success,
  failure and cancel alike; the staging root goes last and only if empty; and
  `intake.Sweep(DictDirs)` at startup removes what a kill left behind.
- **A URL is acquired by the server, not the caller** (D132). `POST ?url=` claims the job,
  answers 202 and downloads on a goroutine, so the phone's page may die on rotation while
  the transfer continues; `downloading` is the one state in the machine that exists because
  the work is minutes long and somebody is watching a number. Policy is `IMPORT_URL_HOSTS`
  (empty by default, so any site: the link is one the user handed over deliberately, and a
  list of the community sites was answering a question nobody asked - D139. Set, a host
  stands for itself and its subdomains) plus https-only and a refusal to reach loopback, private,
  link-local or CGNAT addresses — the latter judged in `net.Dialer.Control`, at connect
  time, which is where a name that resolves differently on the second lookup cannot slip
  past. Every redirect hop is re-validated. `IMPORT_INSECURE=1` lifts both refusals as one
  question. The download lands in `<dict dir>/Downloads/`, **never in the staging area**: it
  is not an intermediary but the only copy of a file that took minutes to fetch, and its
  fate has to be the user's `IMPORT_KEEP` choice rather than the job's teardown. That
  folder is visible on purpose — somebody who fetched two gigabytes has to be able to find
  them — and is *not* part of the library: discovery skips it where intake writes it,
  directly inside a scan root, so one import cannot list the same dictionary twice, once
  from the library and once from the shelf it arrived on. A `Downloads` folder further
  down the tree is somebody's own and is scanned like any other. An
  unfinished transfer keeps its `.part` plus a `.part.meta` validator sidecar, so the next
  attempt resumes under `If-Range` — and a server that answers 200 restarts it rather than
  splicing two different files into one corrupt archive. Whether the link is something we read at
  all is decided from the **response headers**, before a byte of the body is taken — and for
  a loose file that judgement is the *name*, because these formats have no registered MIME
  type and every one of them arrives as `application/octet-stream`. A declared `text/html`
  or `application/xhtml+xml` is refused however perfect the name, because that is the login
  page the link redirected to wearing the file's name; `text/plain` is deliberately not
  refused, because a DSL dictionary is text.
- **A downloaded main file has its companions looked for, and offered** (D135). After a
  loose file lands, the siblings dict's tables say it can have are probed at the **same
  location** — `oxford.mdd`, then `oxford.1.mdd`, `oxford.2.mdd` stopping at the first gap;
  `oxford.css` and `oxford.js`, which an MDX repack ships loose beside the `.mdx` and which
  the reader already serves from there; a StarDict `.idx`/`.dict` in each of their spellings,
  first hit wins; `res.zip`. Two rules
  keep this from being a crawler: nothing is guessed that the format tables do not already
  name, and the budget is capped. A probe is a `HEAD`, falling back to a one-byte ranged
  `GET` for the hosts that answer `HEAD` with 403/405/501, so nothing but headers crosses the
  wire; a file already in the download folder is not probed at all, and does not end the
  numbered run. **Nothing is fetched on the strength of a probe.** What a probe produces is
  a list reported as `extras` and shown on the same screen that asks which dictionaries to
  install, ticked but not taken — a companion can be hundreds of megabytes of somebody's
  phone data. Confirming with extras runs in two phases: download, **re-sniff**, then
  install, because a `.ifo` is incomplete before its `.idx` lands and complete after; the
  picked candidates are re-matched **by name**, and the completeness check runs again on what
  actually arrived, so a download that failed still refuses honestly instead of installing
  half a dictionary. The derived URL is re-checked against the same host policy as the link
  the user gave, and is never reported: it is machinery, and a link may carry a session
  token.
- **A dictionary the library already holds is updated, not duplicated** (D134). Sniff
  compares each candidate against the folder an install would create — re-rooting its files
  exactly as extraction does, and comparing **declared size against what is on disk**, the
  same size-first test `store.SourceChanged` makes before it re-indexes anything; no
  checksum, because hashing a two-gigabyte bundle on a phone answers a question a `stat`
  answers. The candidate carries `existing` (the folder) and `unchanged` (same files, same
  sizes), so the user is told *before* choosing; the tick is the decision — ticked
  **overwrites**, unticked **skips** — and an unchanged one starts unticked. **Nothing is
  ever installed under a numbered name, and there is no "second copy"** (D155 Am. 4; the
  old `copy=1` opt-out is gone): two of one dictionary is never the outcome of a choice
  nobody made. Confirming **replaces the folder whole**,
  by renaming the old one aside into the stage, renaming the new one in, and restoring it if
  that fails — so the library is never without the dictionary. **Media survives a replacement
  that brings none** (`keepMedia`): a user who unticks a gigabyte of `.mdd` on a row that
  replaces their copy saved a download and did not ask to lose the installed one, so the old
  folder's `dict.CompanionMedia` moves into the new one. A replacement with media of its own
  keeps only its own, because two editions' parts spliced together are neither. The question is re-asked at
  write time rather than trusted from the sniff, because minutes of user and extraction time
  sit between the two. **Beyond the import folder, the whole library is checked by name** (D155
  Am. 2): a candidate with no folder of its own there is looked up among every dictionary
  the registry knows (`Manager.Library` ← `Registry.SourcePaths`), in any configured folder
  and any format, by main-file stem, case-folded. It reports `elsewhere` (that folder's name),
  with `unchanged` when the files beside it match by name and size. Ticked, it is
  **overwritten in place** (`replaceIn`), "replaces your copy in <folder>": only the files the
  new copy brings are written, each old one first renamed aside in its own folder (a
  same-filesystem rename, undone if any file fails), and nothing else in that folder — other
  dictionaries, media the new copy lacks — is touched, because the folder is the user's own
  arrangement. Unticked, it is skipped.
- **Each download is kept under its own link** (`LinkDir`): `Downloads/<host>/<path
  folders>/<file>`, sanitised, at most 8 folders deep. Two different links therefore never
  share a file name, and the only file a download can land on is an earlier revision of the
  same place — which it **replaces**. That is not a doubt, so it is not a question; the
  dictionaries themselves are the user's decision, taken on the confirm screen. The folder
  is made only once there are bytes to write, and the sweep removes the ones left empty.
- **A link already downloaded is not downloaded again.** A completed fetch leaves a `.done`
  sidecar beside the archive recording the URL, the server's validator and the size; the
  next fetch of the same URL sends `If-None-Match`/`If-Modified-Since`, and a **304 reuses
  the file with no body transferred at all**. With no validator to send, an equal
  `Content-Length` ends the transfer at the headers. A file that genuinely changed is
  fetched and **replaces its older revision** — the old rule "a download is never
  overwritten" produced `oxford (2).mdx`, which installed as a second dictionary called
  `oxford (2)` instead of updating `oxford`. A sidecar whose file has gone is swept.
- **One link can name a collection** (D155, `collection.go`). When the download path refuses
  a link as not a dictionary, the page itself is read once, and a **web folder page** (every
  `<a href>` on it) or a **`.txt` list** yields the files it names. A **Nextcloud public share**
  (`…/index.php/s/<token>?dir=…`, a page drawn by script) is listed instead through its public
  WebDAV, `PROPFIND Depth: 1` on `…/public.php/dav/files/<token><dir>/` (Nextcloud 29+;
  `nextcloud.go`), and its files download from their WebDAV URLs. A list is one file name
  (resolved against the list's own URL after redirects) or link per line; the first `# `
  line is the title, and everything else is ignored. Several links **pasted at once** are the
  same thing with nothing to fetch first, and `https://legbehindneck.com/wudict#<link>`, the
  **share link**, stands for the link after `#` (`#/dict/x/`, a single leading slash, is short for a path on
  legbehindneck.com itself; `//host` is refused as a host in disguise), or for several links after `#`
  (`UnwrapAll`: split where `http(s)://` follows a comma, semicolon, bar or whitespace, plain or %-escaped;
  a link inside another's query stays whole). **Google Drive** file links (`/file/d/<id>`, `open?id=`, `uc?id=`)
  are rewritten in `Fetcher.Check` to `drive.usercontent.google.com/download?id=…&export=download&confirm=t`
  (`drive.go`); a Drive file, whose URL names no file, is named by the HEAD's Content-Disposition, its `.part`/`.done` keyed
  `gdrive-<id>`, and no siblings are probed.
  A Drive page where the file should be is `ErrDriveRefused`. Drive folders are not supported (only the Drive API lists them). The page at the share address is only what a
  browser shows when Android did not open the app, and it never sees the fragment. There is
  no manifest format. Only names a dictionary, companion or archive could have are kept
  (dict's tables, `SupportedArchive`), each is re-checked against the host policy, and each
  is described by a `HEAD` (4 at a time; ≤ 500 files; page ≤ 1 MiB). A file the site no
  longer has is not offered. **Nothing is downloaded before the user chooses.** The files
  are presented to `Sniff` as an archive whose directory is the listing, so grouping,
  completeness and `markExisting` are the archive rules unchanged. "Already installed /
  replaces your copy" therefore comes from names and sizes (D134), with no stored
  provenance; the site's `Last-Modified` is shown as `date`. Each dictionary's **media**
  (`.mdd`, `.files.zip`, `res.zip`) is moved into `extras`, **ticked by default**. It is
  marked before the move, so a grown `.mdd` is a change. For a list, not a folder page,
  each main file also gets the single-link companion probe, so a list of `.mdx` links
  installs with their `.mdd` (the probes run 4 at a time, as the `HEAD`s do). Pasted links
  are checked against the host policy **before** a job is claimed, as one link is. Confirming
  downloads and installs **one dictionary at a
  time** through the ordinary `Fetch` → `sniffFile` → extract path. A failing dictionary is
  named in `error` and the rest still install (`done` + `error`). So is a ticked file that
  did not arrive beside a dictionary that did. A row's files are put back on **one stem**
  after download (`renameDownload`), because Fetch may save one under another name (a
  `Content-Disposition`), and OpenPlain groups by
  stem. A row with nothing left to install still disposes of its download. An archive row installs
  every whole dictionary inside it except those the library holds unchanged. Downloads are
  named after the link given, not the redirect target, so a "latest" permalink (Kiwix)
  installs into the same folder every time and a new edition replaces the old.
- **Disposition**: `IMPORT_KEEP = ask|keep|delete` decides what becomes of the source
  archive after a successful install, and the configured answer beats the request. **A
  failed import always keeps the source, and that is not a setting** (D129) — deleting the
  user's only copy after a failure is unrecoverable.

**API** (`/api/intake`, not CORS — it writes to the library, outside the D69 allowlist):
`POST` starts a job from an uploaded body, a local `path` or a `url`, and `POST ?confirm=1` accepts
the picked candidates — and the picked `extras` — and returns 202; `GET` polls status, the sniff manifest and progress;
`DELETE` cancels and cleans up. Poll-based, matching `/api/ingest`. The UI is the shared
embedded setup page, so **desktop gets drop-an-archive at no extra cost**; per D102 the
staging directory, the central-directory trick and the job id surface nowhere — progress is
one line, and while downloading it names the **host** rather than the link, since a shared
URL may carry a session token and the file has no name until the site answers.

## 5. Ingest pipeline

```go
type Entry struct { Headwords []string; Body string; Kind BodyKind; LinkTo string } // HTML|Text|XDXF|DSL
type Reader interface { Meta() Meta; Next() (Entry, error); Close() error }        // sequential scan
type Dictionary interface { Exact(…); Prefix(…); Keywords(…); Resource(name) …}   // runtime, every backend
type Searcher interface { …Context(ctx, …); FullTextMatch(ctx, …) }               // prepared backend only
```
- Ingester (`store.IngestPlan`): consumes `Reader`, normalizes body → HTML, strips text for FTS, batch inserts (5000 rows per progress step), then FTS `optimize`, `ANALYZE`, `PRAGMA optimize`. Media packing (`store.IngestMedia`) writes `media.db`.
- **One way to change prepared data (D157)**: `store.Reconcile(src, Target, Hooks)`. Every caller — the panel's switches (`setFeatures`), the background indexer (`ensureBaseIndex`), the abbreviation sweep, the Rebuild job (`refresh`), `wudict ingest`, `wudict reindex`, and the self-preparing formats (`store.OpenSelfPrepared`) — states the result it wants, and what differs between them is the Target: `FullText`/`Contains` (nil keeps what is built; a dictionary never prepared starts from headwords only, D24 — `wudict ingest` included, D152 Am. 1), `Media` (leave · keep · repack · on · off), `Rebuild` (which outdated reasons rebuild usable data; nil never — D151), `Dir`/`Out`. A missing, unusable or source-changed database is always rebuilt. The text rebuild never opens the direct backend (PERF M3); only a media pack needs one.
- **One read per prepared database (D157)**: `store.Inspect(textDB)` reads the meta once and answers usability, plan (`PlanFromMeta`, the one reader of `ingest_level`/`has_trigram`), staleness (`Stale`/`TextStale`) and media pairing from it; `FindPrepared` is `PreparedFor` with that read kept.
- Triggered: CLI `wudict ingest [-full] [-fulltext] [-contains] [<path|dir>…]` (no path: the configured `DICT_DIR`; a flag left out keeps the prepared plan, D152); web UI per-dict switches / "full-text for every dictionary" with SSE progress; headword-level indexing happens silently on first search (D13).
- Durability (P10): an upgrade no longer deletes the existing `.text.db` first — the atomic temp+rename overwrites it, so an interrupted "index all" leaves the old index intact; the finished temp is `fsync`ed before the rename (inserts still run `synchronous=OFF`; only the commit boundary syncs).
- Direct and prepared backends share the format packages: each format implements `Dictionary` (runtime) + `Reader` (ingest scan) so parsing code is written once.

## 6. Server & UI

- Persistent HTTP server (D3), config layering (flag > env > toml > default) reused from mdict-go-web, browser auto-open.
- Endpoints: the route table in `internal/server/routes.go`, published as `web/openapi.yaml` (`/api/openapi.yaml`); `openapi_test.go` holds the two in step. Client contract: `docs/CLIENT-API.md`.
- **Switches**: every boolean query parameter is read by one function (`queryFlag`): off for `0`, `false`, `no`, `off` or an empty value, on for anything else; a caller that keeps a state the request did not mention asks whether it was sent at all.
- **Served assets (D160)**: the embedded pages, scripts and stylesheets are served without their whole-line comments, stripped once at start from the embedded bytes (`internal/server/webasset.go`) — no second file, no build step. Scripts are lexed just enough to leave template-literal content alone (the Custom styles presets carry CSS comments that are content); licence notices stay. `webasset_test.go` checks placeholders survive and every script still parses under node.
- **URL convention (D14, root-only)**: wudict is served at the **site root** by design. Article resource refs are rewritten to **root-absolute** `/res/{dictID}/…` by `RewriteEntryHTML` (idempotent; skips both `/res/{d}/` and a stray relative `res/{d}/`), and the client `resURL` fallback + the srcdoc `<script src="/assets/frame.js">` use the same absolute form. Rationale: articles render in three contexts — main page, Shadow-DOM, and **srcdoc iframe** — and a *relative* ref resolves against each context's base URL, which differs for the iframe and which third-party dictionary scripts re-resolve against the wrong base, producing the `res/{d}/res/{d}/…` doubling 404 (seen twice: OED `OED4.js`, AHD5 `wavs/*`). Absolute `/res/` is context-insensitive and resolves to the same origin URL everywhere. Subpath mounting was **explicitly dropped** as an always-on requirement; if ever needed it returns as a deliberate `<base href>` + server-known-prefix feature, not implicit relative URLs. (Client `fetch`/`EventSource`/asset refs are all root-absolute too.) `RewriteEntryHTML` coverage: `src/href/data/poster` (and prefixed variants like `xlink:href`, `data-src`) via a `\b`-anchored regex that does **not** corrupt attribute-name substrings (`metadata=` is safe), `srcset` candidate lists, `url(...)` inside inline `style=` attrs and `<style>` blocks (external stylesheets need none — their `url()` resolves against the sheet's own `/res/` URL), and it **strips `<base>`** (a scraping leftover that would hijack all relative resolution).
- **Browser history**: the URL carries `?q=&mode=&dict=`, and *how* it is recorded depends on the action. Typing uses `replaceState` (the search is debounced, so pushing would put `c`, `ca`, `cas`, `casa` on the stack and Back would walk letter by letter); following a cross-reference — `lookupWord`, i.e. a `bword:`/`entry:` link, a double-click lookup, or the iframe bridge — uses `pushState`, so Back returns to the previous entry. A `popstate` restores `q`/`mode`/`dict` from the URL and re-runs the search recording nothing; popping to a URL without `q` aborts any in-flight search and clears the results rather than leaving stale ones on screen. Identical consecutive URLs are not pushed twice.
- **Cross-reference links**: `entry://` is the canonical lookup link (D153) and the only one wudict emits; `bword:` (Babylon Ltd.'s proprietary scheme from BGL / Babylon Builder, late 1990s, no public spec — parsed, never emitted: `htmlref.CanonRef` respells a dictionary's own `bword:` links to `entry://` on every way out) and `entry:` (with or without `//`), plus the `d:`/`x:` shorthands, are accepted as equivalents. `RewriteEntryHTML` never turns them into resources — they are navigation — and only respells them (`bword:` → `entry://`, `entry://@x` → `entry:@x`); the client resolves them: `wordFromHref` in `index.html` for the main page and Shadow-DOM articles, the same logic in `frame.js` for sandboxed iframes (kept in step deliberately; one shared table, `internal/server/parseref_test.go`, runs both copies under node and skips without it). The target is trimmed of spaces and tabs only, and the `@` sub-entry test runs on the undecoded target, so the headword `@home` (written `entry://%40home`) stays a lookup. Every emitter builds lookup links with `htmlref.EntryHref`, which percent-encodes only `%`, `#`, controls and a leading `@`. A trailing `#fragment` is dropped before decoding (it addresses a place inside the target article, never part of the headword, so an encoded `%23` still survives), and a link naming no word swallows the click instead of searching for nothing.
- **Panel provenance**: the summary line (format, source *file name*, `+ n mdd`; full path in its tooltip) is a teaser and the disclosure toggle — one affordance for the whole row, nothing inside it styled as a link; the expanded body is the single place every path appears as a click-to-copy row, including the source itself.
- **In-article clicks are intercepted in the CAPTURE phase** (`frame.js`). Dictionary scripts install their own handlers inside the article — LDOCE6's `entry.js` calls `stopPropagation()` on a speaker `<img>` so that playing a sound does not also toggle the accordion around it — and a bubble-phase listener on `document` never sees those clicks. The browser then followed the link and replaced the article with a bare media player. Capture runs on the way down, before any in-article handler can stop it, and only acts on anchors we recognise, so the dictionary's own toggles keep working.
- **Article sandboxing (hybrid)**: script-free articles render in a Shadow DOM (`:host{all:initial}`) — cheap, total CSS isolation both ways. Script-bearing articles (custom tooltip JS, MathJax) auto-detect into a **sandboxed iframe** with a bridge script (`/assets/frame.js`): auto-height via ResizeObserver+postMessage, entry:// and dblclick lookups, theme sync. Rationale: innerHTML never executes `<script>` in a shadow root and dictionary JS cannot see shadow content — iframes are the only correct primitive for third-party HTML+JS.
- Layout: draego chrome (sticky autohide bar, accordion, dl/dt/dd) with φ-scale spacing; dark mode per mdict-go-web/notes.
- **Brief and full view**: Lingvo DSL's secondary zone `[*]` (`wu-sec`: examples, links to sub-entries) is hidden until the reader shows it, as in Lingvo. A switch at the right end of a section header — last in the row, the one place the match walker and the count never move it — flips that section; the press is also stored as `ui.full` in `/api/prefs` (absent: brief) and decides how the next section opens, while sections already on screen keep theirs. Present only when the section's articles hold a `wu-sec` (D102); Ctrl+\* switches the section being read, from the page or from inside a frame (`frame.js` forwards it). One attribute on the shadow host (`:host([data-brief]) .wu-sec{display:none}`) does the hiding, so nothing re-renders; the line under the sticky header is measured before and restored after, by walking text nodes (a DSL line without `[m]` is a bare text node, which `ShadowRoot.elementFromPoint` answers with the host). A full-text match or a `#fragment` target inside the zone opens the section full without touching the stored choice. Format side: `docs/DSL.md` §6.6.

## 6b. Later additions (implemented)

- **Plans**: `store.Plan{FullText, Contains}` (§3, D24). `/api/ingest` takes `contains`, `fts`, `media` as states; a request naming none prepares an unprepared dictionary at the cheapest level (the panel's index chip).
- **SQLite drivers**: build-tag pair — default `mattn/go-sqlite3` (cgo, `-tags sqlite_fts5`, D4) and `-tags purego` → `modernc.org/sqlite` (pure Go, FTS5 included) used by release cross-builds so one ubuntu runner builds all platforms (D4 amendment).
- **First-run setup**: missing/empty dict dir → `/` serves a setup page; `/api/setup` validates live, switches the registry without restart, persists `DICT_DIR` via comment-preserving `config.SaveKey`.
- **.spx audio (D18)**: transcoded to WAV at `/res` time (cache under db-dir/spxcache, single-flight per cache key). cgo builds decode **in-process** — `internal/speex` (in-house Ogg demuxer) over a vendored decode-only libspeex in `internal/speex/clib` — and never look for an external binary. `SPEEX_BACKEND=external` forces the `speexdec` CLI; purego builds fall back to it automatically, resolving it **once at launch** (`SPEEXDEC` override → next to the executable → `$PATH`) and printing the resolved path or an install hint.
- **Library hygiene**: pre-D20 flat databases are adopted into folders at startup (`store.AdoptLoose`, rename only). `wudict clean` lists/deletes incomplete folders (no `text.db`), unreadable databases, interrupted ingest temps, and loose databases that survived adoption because the same dictionary already has a folder (true duplicates). A healthy prepared dictionary is never listed among these — not when its source changed (D20: re-indexing overwrites in place, so nothing is superseded). One whose source is **gone** is an *orphan* (D156, `store/orphans.go`): listed separately, deleted only by `clean -f -orphans`, and offered in the panel after **Rescan folders** (`GET/POST /api/orphans`: every row pre-ticked; unticked rows get `keep = standalone` in `info.txt` and are never offered again; "Not now" records nothing). No reason is inferred — an unmounted drive and a deleted folder are the same case, and the user decides. `Remove` with source only (D63) and `rm -keep-index` write the same keep marker. Before any of that, `store.Relink` (every rescan, and `clean`) re-points a folder whose source vanished at a discovered file with the same name, size and first-MiB hash — a move, not a loss; only the `info.txt` claim changes, nothing is re-indexed.
- **Outdated prepared data (D151)**: `store.Stale(textDB, src)` is the one judge of whether a prepared dictionary is what this build would prepare from its source now — reasons `schema` (unreadable/other schema: not prepared at all), `source`, `abbrev`, `ingest`, `reader`, `markup`, `fold`, `media`. The code-version reasons come from stamps: `ingest_version` (`store.IngestVersion`), `reader_version` (per format, `dict.RegisterReaderVersion`), `media_version` (`store.MediaVersion`); an absent stamp reads as 1. Any change to what an ingest writes bumps the matching version in the same commit; per-format golden tests (`internal/store/goldentest`) fail until it does (MDX has none — no in-code writer). Detection never rebuilds: `/api/dicts` reports `outdated` per row, the panel offers one Rebuild line when any are, and `POST/GET/DELETE /api/reindex` runs that job on the background lane, one dictionary at a time, cancellable between dictionaries. CLI: `wudict reindex [-all] [<name|path>…]` (outdated only unless `-all`; hands off to a running server on the same library). `wudict ingest` (without `-o`), `rm -f` and `clean` refuse while a server on the same library answers at the configured address: it holds those files open, and Windows refuses to rename or delete an open file. Every rebuild that is not a user plan change keeps the plan (`store.KeptPlan`) and repacks media that was packed before. Rescan re-stats each source, so an edit in place is seen without a restart.
- **Hardening**: `dict.Open`/`OpenReader` convert parser panics on corrupt files into errors; server has a recover middleware; graceful SIGINT shutdown; port-in-use hint.
- **Article formats (D61)**: `/api/search?format=raw|clean|text`. `raw` (default, unchanged) is the dictionary's HTML verbatim, which the desktop UI needs. `clean` is allowlisted structure — no `<script>`/`<style>`/`<link>`/`<object>`/`<iframe>` and their content, no `on*`/`class`/`style`, unknown elements unwrapped, `javascript:`/`vbscript:`/non-image `data:`/protocol-relative URLs rejected, root-absolute `/res/…` absolutised against the request's own origin, and DSL's `<object type="audio/*">` pronunciation rewritten to `<audio>`. `text` is markup-free. Implemented by `htmlref.Sanitize`/`htmlref.Text`, a sibling of `Rewriter` rather than more hooks on it (opposite contracts: byte-preservation vs reconstruction). Measured over 748 KB of real articles: clean 53% of raw, text 39%. **Client contract: `docs/CLIENT-API.md`.**
- **Resource overrides + damage reporting (D59)**: `/res/{dict}/{name}` first looks for `<library folder>/res/{name}` on disk and serves it when present (`Cache-Control: no-cache` via `http.ServeContent`, so an edit is picked up on reload), falling back to the blob inside the dictionary. `{name}` is `path.Clean`ed on a rooted copy and bounded by `within`. Deliberately general — any resource of any dictionary — because the motivating case is a dictionary that ships a **damaged** file: a slob whose bundled `jquery.js` carries 12,186 NUL bytes, unparseable, which kills its own tab script. On the way out, `/res/` also flags a NUL byte in a source-text type (`.js .mjs .css .html .htm .xhtml .json .xml .svg .txt`) — impossible in those formats, so proof the stored copy is broken — and logs once per dictionary+resource, naming the override path. Bytes are never altered. **User docs: README "Replacing a file a dictionary ships (`res/`)" and the CLI help's LIBRARY FOLDER section.**
- **Global user styling (D108)**: two optional CSS files the user owns, `<dir of the wudict.toml in effect>/style/{app.css,article.css}` (`internal/server/style.go`; the CLI resolves the parent via `userDir`, the same way it resolves `StateFile`, so a portable install keeps them with its config). Complementary to D59, not a variant of it: `res/` *replaces* one file of one dictionary, this is *added* after everything, globally, and ordered last so it wins ties. **Two files, not one**, because `body{}`/`p{}`/`a{}`/`table{}` are precisely what an article-minded author writes and every one of them would also hit the chrome; the split is stated as "App is the page, Article is what dictionaries render" and does not fragment the sepia case, because `:root` custom properties set in `app.css` inherit into shadow roots for free. Delivery, one mechanism per surface: chrome via a server-injected `<link rel=stylesheet href="/style/app.css?v=<assetTag>">` at the `{{USERCSS}}` placeholder last in `<head>` (a `<link>`, not an inline `<style>`: no `</style>` escaping question, and render-blocking so a sepia reader gets no white flash) — `basePage()` caches the version/`{{FRAMEJS}}` substitution once, `pageFor(tag)` is a one-entry cache keyed on the app.css hash so the page **and its ETag** move when the stylesheet does; shadow articles via one module-level constructed `CSSStyleSheet` in `root.adoptedStyleSheets` (adopted sheets order after the root's own, so user CSS beats dictionary CSS; one `replaceSync` restyles every article already on screen); srcdoc frames via `<style id="wd-user">` last in `<body>` plus a `{t:"css"}` bridge message, exactly the two-step `{t:"fs"}` uses. Two new tokens `--wd-article-bg`/`--wd-article-fg` replace the hardcoded `#fff`/`#1a1a1a` on `.article`, `iframe.article`, `shadowBase` and `frameDoc`; frames cannot inherit them, so `frameCSS()` prepends the resolved pair to the text it ships across. Dark is one spelling: `syncAutoDark()` stamps a **resolved** `data-dark` on `<html>`, `frame.js` mirrors it beside `class="dark"`, and shadow hosts already carried it — so `html[data-dark]{…}` (App) and `:host([data-dark]), html[data-dark]{…}` (Article) work everywhere, and the three internal spellings (`[data-theme]`, `prefers-color-scheme`, `.dark`) never surface (D102). `GET`/`PUT /api/style` is `{app, article, dir, writable}` with **pointer** fields — absent ≠ empty, the same trap `/api/prefs` has with `ui`; empty *removes* the file; 256 KiB per file; 409 when there is no config directory. `/style/{app,article}.css` is exact-path (no `filepath.Base` aliasing), `no-cache` like a `res/` override, and 200-with-empty-body rather than 404. **Escape hatch, mandatory**: `app.css` can hide its own editor, so `?style=off` omits the `<link>` server-side and the client skips the article sheet. Editor is a docked bottom sheet (`#styler`), not the panel — the panel is `min(420px,92vw)` and would cover the article being tuned; presets **insert text** rather than being toggles, so there is one stored artifact and nothing to reconcile. No user JS, no file watching. **User docs: README "Custom styles", `pages/docs/dictionaries/styles.md`, and the CLI help's CUSTOM STYLES section.**
- **Resource Content-Type**: `/res/` serves from an authoritative `webMIME` extension→type map (`server.go`), overriding both `mime.TypeByExtension` (returns `text/plain` for `.css`/`.js` on some OSes) and stale `media.db` mime rows — otherwise browsers reject stylesheets/scripts under strict MIME checking.
- **Open latency & progressive UX (P8, D12)**: `dict.Probe` (header-only metadata, per-format prober; `dict.HasProber`) + lazy `upgraded.src` + `Registry.Warm()` remove the heavy full-index build from page load and from ingested-dict opens (the double-open is gone; the embedded Store's auto-attached media.db serves resources first, source opened only on a miss). `/api/search` is NDJSON-streamed (`begin`/`hit`/`end`, per-hit preference slot index); the client paints each accordion's summary on arrival, auto-opens the reader's count of sections (Open, D142), and drives an enabled+ordered dictionary list from the panel (one order for the panel, the picker and the search, D166: pinned dictionaries first in the user's order — pin toggle (adds after the pinned), to-top (first; pins), ▲▼ and a desktop drag gripper among the pinned — then every other one A–Z; enable/disable; persisted in `state.json` through `/api/prefs`; All-dicts queries fewer results/dict with per-dict "more…"). **A section's articles are rendered the first time it opens (D159)**, not when its results arrive: a section the Open choice opens is rendered in the same step as it opens, and so is every other way of opening one (a click, Open all ⊞, `more…`, a deferred load, an `entry://word#frag` anchor) — a closed section costs its summary line, not a shadow root or an iframe per article. Ingest temp files are per-call unique (`store` `tempDBName`) so concurrent ingests (warm vs on-demand) can't clobber.
- **Open-order strategy (D142)**: which accordion sections auto-expand is the reader's choice, stored as `UIPrefs.fastFirst`, `openN` and `openAll` in `state.json` and surfaced as one `Open` menu in the ☰ panel: `1, first to show a result` · `1`–`5` · `All` (a larger `openN` set by hand is shown as its own choice; there is no ceiling). The menu writes all three fields at once. **My order** (every count; 1 is the default since D144, and every field's zero value) opens the first `openN` dictionaries in the user's own order that have results, or all of them: slot `i` may open only once every slot above it has reported, and the hold lasts until the count is reached. Only the first section that opens gets the full-text mark walk and the jump to the first mark. **Fastest** (`1, first to show a result`) is D73's latch unchanged, and only ever one section: the first dictionary to *answer* takes the open slot and keeps it. Purely client-side: `/api/search` is untouched, because the slot layout is already emitted in preference order on `begin` before any dictionary is opened, so what changes is which section unfolds, never what is searched or in what order. Both strategies are one predicate (`settle()` in `index.html`) over a per-generation resolution frontier, so they cannot drift apart; `deferred`, errored, skipped and empty slots *resolve* the frontier without ever becoming the section that opens. The intrinsic cost — time-to-open is `max()` over the prefix rather than `min()` over everything — is bounded by a 700 ms deadline (`OPEN_BARRIER_MS`) armed on `begin`: on expiry the best answer already in hand opens and the Fastest rule resumes, so the worst case is Fastest plus 700 ms. On a prepared library the frontier resolves in milliseconds and it never fires. D73's invariant is untouched under both: nothing that opens ever closes, moves or reopens under a reader.
- **Picker groups (D149, D162)**: each `/api/dicts` row carries `groups` (`internal/facet.Derive`, pure, per row): Language and Language pair derived in code, then every section of `groups.ini` — `[Section]` a facet (≤ 32, names ≤ 64 bytes; `lang`/`pair`/`Language`/`Language pair` refused), `Name = text, text` a group (`:` works too; a line before any section joins `[My groups]`). A plain item has at least 2 characters (shorter is reported and skipped) and matches as a case-insensitive substring of the title or of the file name (`Input.File`: the base name on disk, extension included; a standalone `text.db` gives its library folder's name). An item in backticks is a Go RE2 regex, `(?i)`, read verbatim (`,` `;` `#` inside are the pattern's), tested against title and file name separately. Outside backticks `;`/`#` start a comment, with no escaping. Ids are the names trimmed and lower-cased. Rank (`fo`): a section the built-in list does not have — the user's own, `[My groups]` included — comes first, above Language, in file order (`foMine`, negative); built-in sections follow Language pair. The file lives beside `wudict.toml`; while absent, the embedded `internal/facet/groups.ini` is in effect, and once saved it is the whole list — no merge. `GET/PUT/DELETE /api/groups` read, replace (a body equal to the default removes the file; an existing file that cannot be read is replaced only with `?replace=1`) and reset it, writing through `writeFileAtomic` (fsync, mode and symlink kept). The server caches the parse keyed on the file's text, re-read at most once a second (`groupsNow`); PUT/DELETE install what they wrote. Bad lines and items — an invalid regex included — are reported, never refused (at most 100 listed, the rest counted; text and message clipped to 200 bytes; at most 500 regexes per file; only a regular file of at most 256 KB is read); their count rides on `/api/dicts`' `begin` (`groupsProblems`) so the ⚙ drawer and "Edit groups…" show ⚠. Edited on the `/groups` page (`setup.css` family). The client hides only a group holding every dictionary (it is "All dictionaries") and a facet left empty; a group of one is a choice.
- **Shared plumbing (D163)**: files a user can edit are read with `fsx.ReadBounded` (regular files only, capped: groups.ini and stylesheets 256 KB, wudict.toml 1 MB, state.json 4 MB, token 4 KB) and written with `fsx.WriteAtomic` (temp + fsync + rename, mode kept with owner rw, symlinks written through). groups.ini, app.css, article.css and state.json are each an `ownedFile[T]` (`internal/server/ownedfile.go`): content-keyed cache, serialised saves that install their result, an unreadable file replaced only with `replace=1`; state.json re-reads before every save, so a hand edit survives, and `PUT /api/prefs` changes only the `ui` keys it is sent and keeps the dictionary list when `dicts` is absent (the page sends only the keys, and the list only if it differs from what it last read or wrote), so two pages on one server cannot carry stale copies of each other's settings back. Their folder is one `UserDir` beside wudict.toml. wudict.toml lines that set nothing (`config.Config.Problems`: no `=`, unknown key, open list or string) are printed at startup and shown on the setup page. Every API error is `{"error": …}` (`httpErr`; `decodeJSON` answers 413/400); every stream (NDJSON, SSE) goes through `respStream` (no-cache, `X-Accel-Buffering: no`). Long-running work is a job (`jobs.go`): keyed, one per key, owned by the server and holding the active-power reference - the Rebuild, lemma downloads, and each dictionary's index change, which `/api/ingest` follows and `/api/dicts` rows report as `job` so a reloaded page re-attaches. Downloads (imports and lemma data) share `intake.Fetcher`'s client policy. Names derived from files use `dict.Name`; sizes shown to people use `logx.Size` (decimal) and the same rule in the pages.
- **Dictionary list, kept and revalidated**: every `/api/dicts` row is keyed by stat calls alone (`rowKey`, `internal/server/dictrows.go`): the source and its folder, the abbreviation file, the library folder `LookupDir` resolves with its text.db and media.db (journal_mode=OFF, so every write moves the .db), the entry's `gen` (bumped by `entry.reconcile`, D157, when it changed the prepared data or the served backend, and by `revalidate` when a rescan finds the backend or prepared folder a row was derived from changed), plus the folders, groups.ini, reader versions and the build. The keys make the list validator, sent on `end` as `etag` (never a header: an error row, a running job, or a row whose key moved while it was derived makes an answer unkeepable, known only after the last row); `If-None-Match` with it is a 304 before any row is derived. Rows whose key is unchanged come from an in-memory row cache (a row is kept only if its key held across its derivation, which a self-preparing open breaks; error rows are not cached; `builtin`, `job` and the media-empty override are applied fresh); a rescan flushes it and ignores `If-None-Match`. The page keeps the last keepable answer in localStorage (`wudict_dicts`), draws it at once and revalidates in the background; a 200 replaces the list in one step and re-runs a search over "all" or a group only if ids or caps changed. A link naming one dictionary (`from=`, or a single-id `dict=`) is answered before the list without a kept copy; an id the server does not know falls back to "all"; `/browse` links use `from=`. A page load never rescans the folders: new files arrive by "Rescan folders" (`/api/rescan`, which also empties the row cache) or a server start.
- **Browsing (D147)**: the dictionary as an ordered list of its own headwords, read a page at a time — `GET /api/browse?dict=&p=|at=` plus its own page `/browse` (`internal/server/web/browse.html`, self-contained, no `setup.css`). Index-backed only, via the optional `dict.Browser` capability that **only** `*store.Store` implements (`internal/store/browse.go`): order, offsets and the letter strip are three reads of `idx_entry_w`, which is covering for `SELECT w … ORDER BY w COLLATE NOCASE`, so a jump to `M` in a 2.9 M-entry dictionary is an index seek and deep `OFFSET` is an index walk — a direct backend would have to sort its whole headword list in memory, which is what preparation exists to remove, so an unprepared dictionary gets `409 {prepared:false, indexing}` (checked from `validPrepared` **before** `entry.open()`, or the answer "not yet" would cost a gigabyte of headword map). Browse order is the index's, not the file's — `wudict keys` is the sequential-source tool. The strip is built by a **seek-and-count walk** (one prepared `MIN(w) WHERE w >= ?` per initial plus one `COUNT(*)` over its span), not `GROUP BY substr(w,1,1)`, which SQLite cannot serve from an index and would answer with a temp b-tree over every row; it is computed once per open handle (`sync.Once` on the `Store`) and shipped with every page, so a page turn is one request. NOCASE folds **ASCII only**, so the upper bound of `Z` is `{`, not `[`; non-contiguous runs of the same initial (`É` … `é`) merge into one chip by label, first offset wins; CJK collapses to one chip per script and Hangul syllables to their 19 leading jamo, or the strip would be the dictionary — and a collapsed chip is then **cut**: a run longer than `max(3000, total/32)` becomes pieces of that span labelled by the headword each opens on (`漢 一 丁 三 …`, first piece keeps the run's label), so the strip stays a thumb index of ~30–90 chips at any size instead of one tab holding 78,000 words. Each boundary is a `LIMIT 1 OFFSET span` walk on from the previous one, never an `OFFSET` from the start, so cutting costs at most a second pass over the runs that are too long. The walk jumps a whole collapsed script in one step (`initialRunEnd`: a Han run ends where its Unicode block does, a Hangul run where its leading jamo changes) — per-code-point stepping would be 30,000 queries on a Chinese dictionary and 11,172 on a Korean one. Korean is read **before** `dict.Fold`, which normalises to NFD and decomposes a precomposed syllable into conjoining jamo; both spellings map to the compatibility jamo (`ㄱ`), the only form that renders on its own in a tab. Chips merge, so the dictionary's total is the **sum** of the chip counts, never `offset+count` of the last chip — the last chip in strip order is not always the one the last headword falls under. Headwords are decoded for display only (`unesc()` in browse.html: `DOMParser`, never `innerHTML`, skipped when the headword contains `<`, bounded at two passes like `dict.DisplayText`) — some dictionaries store `&#x3B1;-松油烯` as the headword, and the raw string is still what the `?q=` link carries. The response carries `at`, the row that was ASKED for: a chip click and a typed jump both snap DOWN to the page grid, so the page holding the first `K` opens in the tail of `J` — the strip highlights by `at` (the chip clicked, or the located row), falling back to the page start on a plain page turn, or clicking K lights up J. `size` is a server constant (300): the page number is the whole address of a browse view, so `?p=37` must mean the same place tomorrow. Never CORS (D69) — the extension grant is lookup, and this dumps a dictionary a page at a time.

## 6c. Native-ingested model (D15, P10–P11) & the library (D19/D20, P12)

- **The library** is `store.DefaultDBDir()` (DB_DIR): wudict's own working area, holding one **folder per prepared dictionary** — `<db dir>/<source file name>/{text.db, media.db, info.txt}` (D20). A dictionary is one folder: copyable, zippable, transferable; dropped into a dictionary folder it is discovered by its `text.db`.
- **Dictionary folders (D21)**: `DICT_DIR` holds one *or several* roots — repeatable flag, `os.PathListSeparator` in the environment, TOML array in the file. `dict.DiscoverAll` walks them in order, dedupes by canonical path (overlapping roots are normal), and reports per root what it contributed vs. what it holds; a missing root is skipped, not fatal. `Discover` resolves a symlinked root before walking (`WalkDir` lstats, so a symlinked folder used to yield nothing).
- **It is not a discovery root** (D19). `DICT_DIR == DB_DIR` is a startup error and `dict.ExcludeDir(DB_DIR)` keeps discovery out of it wherever it sits. Only `USE_CACHED` (default off, set by "Use these dictionaries" on the setup page) lists prepared dictionaries; when on, one is hidden only if its source is among the discovered files (dedup by source path). `server.native` wraps a prepared dictionary with no live source; `upgraded` wraps one whose source is still on disk (so resources fall back to it).
- **Folder resolution**: `store.LookupDir` (read-only, ownership-verified) on every read path; `store.ClaimDir` (atomic `os.Mkdir` + immediate `source` claim) only when ingesting. `store.PreparedFor` = LookupDir + `SourceChanged`, so an edited source falls back to the direct backend and is re-indexed in place.
- **Direct = preview**: direct backends need to be correct and acceptable, not fast (D15). Their headword indexes are lazy (§1), gomdict caches 8 decompressed record blocks (FIFO, open-per-miss to respect the `.mdd` fd budget), and no binary-search/collation work is taken on (silent-wrong-results risk).
- **Formats**: mdx/mdd, stardict, slob, dsl, **bgl** (`internal/format/bgl`, P10 — streaming reader: metadata pass then lazy per-block second pass, tolerates BGL's absent/zero CRC; type-2 resources scanned lazily; DSL-style auto-ingest on first open), **zim** (`internal/format/zim`, P92 — the file's own path pointer list is sorted in raw byte order, so exact and prefix lookup are a binary search over the FILE with no resident index; auto-ingest is declined, D96/O9).
- **Extension matching**: `dict.Registry` matches the **longest registered suffix** (`matchKey`), so `.dsl.dz` beats `.dz` and an unregistered multi-part companion like `dict.dz` is neither discovered nor opened by mistake (it is reached through its `.ifo`).

## 6c-bis. Seeing and editing the configuration (D22, P16)

- `GET /api/config` — folders with per-root status, library dir + prepared count, config file path, the origin of `DICT_DIR` (flag/env/file/default) and whether saving to the file can take effect, the platform reveal label, and whether the caller is on loopback.
- `GET /setup` — the folder editor, always reachable, pre-filled, with Cancel when dictionaries are already in use. `/` still serves it automatically while the registry is empty.
- `GET /api/reveal?path=` — opens the platform file manager (`open -R` / `explorer /select,` / `xdg-open <dir>`). Refused unless the path is one the UI already displays **and** the request comes from loopback.
- Panel: a collapsed "Folders & configuration" section carries all of the above plus **Rescan folders**, which was moved out of the panel header — it re-walks every folder and re-opens every dictionary, too expensive to be a one-click curiosity.

## 6d. Terminal output (P13)

House style lives in the `internal/logx` package doc and is enforced by keeping printing OUT of library packages:
- **Every message about a dictionary names it** — `"Espasa Calpe": 2 entries indexed in 0.4s`. Unattributed lines (`ingest: 6931 unresolved link targets`) are useless when 55 dictionaries are being prepared at once. `logx.Dict(name)` builds the prefix.
- **Libraries return, callers print.** `store.Reconcile` returns an `Outcome` (what it rebuilt and why, the ingest `Report`, what it packed) instead of writing to stderr; the CLI prints it, the server logs it verbosely — because only they know whether an ingest is a foreground command or one of fifty silent background builds (D13).
- **Levels**: `logx.V` verbose detail · `logx.Warn` real degradation · `logx.Status` progress on a slow foreground step · `logx.Progress`/`ClearLine` in-place counters, **suppressed when stderr is not a terminal** (they are for a human watching a wait, and their carriage returns collide with results on stdout).
- **Startup prints the effective configuration** — dictionary folder, library (DB_DIR), config file in use, address, .spx decoder, indexing mode, and what is being served — each folder counted by what *it* contributed, then a single next-step line when there is something to do.

## 6e. wudict markdown (D154)

- **The wudict howto** (`internal/howto`, D154 Am. 5): the app's guide, shipped as a wudict markdown dictionary
  embedded in the binary.
  - At start the CLI writes it to `<config dir>/builtin/`, rewriting only a file whose content differs, and
    passes it to the registry (`server.WithBuiltin`).
  - It is listed after the folder scan, while its file exists, under the fixed id `wudict-howto`, so
    `/browse?dict=wudict-howto` works everywhere, and it is flagged `builtin` in `/api/dicts`. Its library folder is
    never listed as imported.
  - Removing it (the panel's Remove…, whole only) deletes its file, images and index, and leaves the marker
    `wudict-howto.removed` beside it: the app no longer writes it back at start. `/api/config` then reports
    `howtoRemoved`, and the setup page's "Bring back the wudict howto" (`POST /api/howto?restore=1`) undoes it.
  - It stands down while a dictionary folder holds a file of its name: the user's copy, which
    `POST /api/howto` (the setup page's link) writes into the import folder. The copy takes over the fixed id, so
    the guide's own links still reach it. It is not flagged `builtin`; it is the user's file.
  - Counts that mean "the user's library" exclude it (`UserCount`). The index page serves the app, not the setup
    page, when it is all there is, and opens it on `wudict welcome` under an "Add dictionaries" line.
  - Every headword starts with `wudict `, so it never answers a lookup meant for the user's own dictionaries.

- `.wudict.md` or a plain `.md`, either one a dictionary only when its line 2 is `wudict: <version>` (the gate,
  whatever the name, in folder scans and intake alike): one UTF-8 markdown file per dictionary, also read as
  `.wudict.md.gz` or `.wudict.md.dz`. It is read as a format (id `wmd`) and written by
  `wudict dump -format md -mode html|clean [-compress gz]`. Normative spec: `docs/WUDICT-MARKDOWN.md`.
- Standard CommonMark + GFM tables, parsed by a stock parser.
  - `# title`, `wudict: 1` and `key: value` header lines.
  - Entries are the top-level `##` headings; adjacent `##` lines are one entry (headword, then aliases).
  - `see: target` is a redirect to a target outside the dictionary.
  - Links are standard `[t](entry://…)`.
- The writer has two modes, chosen per dump:
  - `html` (default): each body verbatim as one HTML block; nothing is lost.
  - `clean`: a destructive cleanup to markdown primitives; if cleanup is not enough, it aborts with a hint to use
    `html`.
- There is no lossless-round-trip machinery: `html` keeps the HTML, `clean` is lossy by design.

## 7. Non-goals (v1)
Writing/exporting formats other than wudict markdown (D153, the one sanctioned writer); entry editing; draego's `.db` as input (one-off migration script if ever needed); EPWING/etc. (BGL moved *into* scope in P10.)
