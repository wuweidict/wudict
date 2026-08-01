# wudict — Architecture Spec (v0.2, Phase 0)

## 1. Core idea

**Dual-backend** (Decision D1, user-directed): every dictionary is served through one `Dictionary` interface with two interchangeable backends:

1. **Direct backend (default, implicit)** — reads the native format at runtime using its built-in index. Zero import step, original files are the only storage. Capabilities: exact + prefix headword lookup, article render, resources.
2. **Ingested backend (the "native" mode — D15)** — canonical SQLite databases built from the source. Adds contains (substring) headword search and full-text search (FTS5), the draego-style features. Two ingest levels:
   - `text` — headwords/aliases/articles → the dictionary's library folder `text.db` (D20); resources still lazy from source.
   - `full` — additionally packs binary resources → `media.db` in the same folder (see §3).

```
config: ingest = off | text | full        (global default, per-dictionary override)

              runtime
  ┌ Direct backend ──── format reader (built-in index) ─┐
  │                                                     ├─ unified Dictionary API ─ server/UI
  └ Ingested backend ── <db dir>/<name>/text.db [+ media.db]┘
              resources: media.db if present, else lazy from source file
```

- **Capability tiers**: backends advertise `Caps.Exact/Prefix/Contains/FullText` (D16 renamed `Fuzzy`→`Contains`). the panel shows contains/full-text/media as per-dictionary switches with their measured sizes (D24), and a search mode a dictionary lacks offers to add it inline; the headword index is prepared on its own (D13, `AUTO_INDEX = on|off`).
- **D15 (2026-07-25)**: the ingested pair is the *native* mode and direct readers are *preview*. Read D15 before optimizing anything on the direct side.
- **DSL/BGL exception**: no native index ⇒ direct mode transparently prepares the library folder on first open — with the same cheap plan as any other format (headwords + article text stored so it is readable at all; full-text and contains stay opt-in, D24) (auto-ingest, no user ceremony). Its resources (`.files.zip`) stay lazy.
- Sources are never modified. `meta` stores source size/mtime/hash; changed source ⇒ stale-DB prompt (re-ingest or fall back to direct).

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
  -- dict_uuid, name, format(mdx|stardict|slob|dsl), source_path, source_size, source_mtime,
  -- source_sha256_1M, entry_count, lang_from, lang_to, description, css, js, ingest_level
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
CREATE TABLE meta(key TEXT PRIMARY KEY, value TEXT);  -- dict_uuid (MUST match text.db), name, source_*
CREATE TABLE resource(name TEXT PRIMARY KEY, mime TEXT, data BLOB);
```

- Packing includes `.mdd` resources **plus** the loose sibling files articles reference (`store.ReferencedAssets` scans the prepared bodies for `href/src/data/poster`, dropping schemes, fragments, absolute URLs and anything containing `..`). Lookup falls back to `COLLATE NOCASE`: MDD names are indexed lower-cased while loose files keep their real spelling.
- Pairing: identical `dict_uuid` in both `meta` tables; loader warns on mismatch, refuses cross-dict pairs.
- Resolution order at runtime: `media.db` → source file resolver → 404. A text.db shipped alone still works fully for text (graceful degrade); receiver can point it at their own copy of the source for media.
- Naming: D9's `<slug>.text.db`/`.media.db` pair is superseded by D20's folder — `text.db` and `media.db` inside a folder named after the source file. Loose `<name>.text.db` files still open (a database copied out of its folder).

### Storage of article bodies (D24)
`entry.m` is DEFLATE-compressed per row, marked by a leading NUL byte (article text never begins with one), so compressed and literal rows coexist and pre-D24 databases open unchanged. Bodies under 120 bytes, and any body compression fails to shrink, are stored literally. `NO_COMPRESS` turns it off for new ingests; reading always understands both. Measured: 3.8x smaller on a 40k-entry dictionary, where article text was 95% of the file.

### What is built, and when (D24)
`store.Plan{FullText, Contains}` decides which indexes exist. Finding a headword — exact, prefix, accent-insensitive — needs no switch (~2 MB, every backend can do it). `entry_trigram` is created only when `Contains` is asked for; `entry_fts` indexes article text only when `FullText` is. The server's `entry.setFeatures` brings a dictionary to a requested state, rebuilding the index when the wanted indexes differ, packing or deleting media, and refusing outright when the source file is gone (the prepared data is then the only copy).

## 4. Query engine (ported from draego, fixed)

Modes (D16): **exact**, **prefix** (default; starts-with, accent-insensitive — `store.Fuzzy` is now just its internal fold engine), **contains** (substring/typo, `entry_trigram` MATCH on folded `w` for ≥3 chars, raw escaped `LIKE` below that), **full-text** (FTS on `w`+`txt`, BM25), each × single-dict / all-dicts. `fuzzy` survives only as a legacy `parseMode` alias → prefix. All-dicts fans out concurrently (bounded goroutines) across *both* backend types — direct dicts contribute exact/prefix results, ingested ones the full set; grouped per dictionary in the accordion. `store.Prefix` falls back to a diacritic-folded FTS prefix when the raw `LIKE` finds nothing (accent-insensitive prefix parity with the direct backends). **Streaming (P8, D12)**: `search.Stream` emits each dictionary's `Hit` as it completes (serialized), so the server can flush results progressively in the caller's preference order instead of waiting for the whole fan-out.

### FTS-audit — bugs found in draego/drae.go; fix when porting
1. FTS indexes raw HTML ⇒ markup tokens pollute matches/rank. Index stripped text.
2. `sanitizeFts` only escapes `"` — `-`, `*`, `^`, `:`, parens, `OR/AND/NOT/NEAR` reach MATCH ⇒ syntax errors/operator surprises. Wrap every token as `"tok"*`.
3. `ensureFtsIndex` swallows CREATE errors (`return false, nil`); population non-transactional; index built lazily on first request (stall). Build only at ingest time, in a transaction.
4. Fuzzy `ORDER BY w` discards rank — keep for headword mode, use `ORDER BY rank` for full-text (deliberate, documented).
5. `LIKE` with raw user input — escape `%`/`_` + `ESCAPE '\'`.
6. Per-request `sql.Open`+PRAGMA churn (CGI artifact). Persistent server, pooled read-only handles.
7. `max` unbounded — clamp.
8. Headword printed unescaped in single-dict mode — escape all reflected text.

## 5. Ingest pipeline

```go
type Entry struct { Headwords []string; Body string; Kind BodyKind } // HTML|Text|DSLMarkup|XDXF…
type Reader interface { Open(path) (Meta, error); Next() (Entry, error); Close() }   // sequential scan
type Lookuper interface { Lookup(word string) ([]Result, error); Resolve(name string) (io.ReadCloser, string, error) } // direct backend
```
- Ingester: consumes `Reader`, normalizes body → HTML, strips text for FTS, batch inserts (10k/tx), then FTS `optimize`, `ANALYZE`, `PRAGMA optimize`. `ingest=full` additionally streams resources into `media.db`.
- Triggered: CLI `wudict ingest [--full] <path|dir>`; web UI per-dict "enable full-text search" / "index all" with SSE progress; headword-level indexing happens silently on first search (D13).
- Durability (P10): an upgrade no longer deletes the existing `.text.db` first — the atomic temp+rename overwrites it, so an interrupted "index all" leaves the old index intact; the finished temp is `fsync`ed before the rename (inserts still run `synchronous=OFF`; only the commit boundary syncs).
- Direct and ingested backends share the format packages: each format implements `Lookuper` (runtime) + `Reader` (ingest scan) so parsing code is written once.

## 6. Server & UI

- Persistent HTTP server (D3), config layering (flag > env > toml > default) reused from mdict-go-web, browser auto-open.
- Endpoints: `/` app, `/api/search`, `/api/entry/{dict}/{id|word}`, `/res/{dict}/{name}`, `/api/ingest` + SSE progress.
- **URL convention (D14, root-only)**: wudict is served at the **site root** by design. Article resource refs are rewritten to **root-absolute** `/res/{dictID}/…` by `RewriteEntryHTML` (idempotent; skips both `/res/{d}/` and a stray relative `res/{d}/`), and the client `resURL` fallback + the srcdoc `<script src="/assets/frame.js">` use the same absolute form. Rationale: articles render in three contexts — main page, Shadow-DOM, and **srcdoc iframe** — and a *relative* ref resolves against each context's base URL, which differs for the iframe and which third-party dictionary scripts re-resolve against the wrong base, producing the `res/{d}/res/{d}/…` doubling 404 (seen twice: OED `OED4.js`, AHD5 `wavs/*`). Absolute `/res/` is context-insensitive and resolves to the same origin URL everywhere. Subpath mounting was **explicitly dropped** as an always-on requirement; if ever needed it returns as a deliberate `<base href>` + server-known-prefix feature, not implicit relative URLs. (Client `fetch`/`EventSource`/asset refs are all root-absolute too.) `RewriteEntryHTML` coverage: `src/href/data/poster` (and prefixed variants like `xlink:href`, `data-src`) via a `\b`-anchored regex that does **not** corrupt attribute-name substrings (`metadata=` is safe), `srcset` candidate lists, `url(...)` inside inline `style=` attrs and `<style>` blocks (external stylesheets need none — their `url()` resolves against the sheet's own `/res/` URL), and it **strips `<base>`** (a scraping leftover that would hijack all relative resolution).
- **Browser history**: the URL carries `?q=&mode=&dict=`, and *how* it is recorded depends on the action. Typing uses `replaceState` (the search is debounced, so pushing would put `c`, `ca`, `cas`, `casa` on the stack and Back would walk letter by letter); following a cross-reference — `lookupWord`, i.e. a `bword:`/`entry:` link, a double-click lookup, or the iframe bridge — uses `pushState`, so Back returns to the previous entry. A `popstate` restores `q`/`mode`/`dict` from the URL and re-runs the search recording nothing; popping to a URL without `q` aborts any in-flight search and clears the results rather than leaving stale ones on screen. Identical consecutive URLs are not pushed twice.
- **Cross-reference links**: `bword:`/`entry:` (with or without `//`), plus the `d:`/`x:` shorthands, are lookup links. `RewriteEntryHTML` leaves every scheme form untouched — they are navigation, not resources — and the client resolves them: `wordFromHref` in `index.html` for the main page and Shadow-DOM articles, the same logic in `frame.js` for sandboxed iframes (kept in step deliberately; one shared table-test runs both). A trailing `#fragment` is dropped before decoding (it addresses a place inside the target article, never part of the headword, so an encoded `%23` still survives), and a link naming no word swallows the click instead of searching for nothing.
- **Panel provenance**: the summary line (format, source *file name*, `+ n mdd`; full path in its tooltip) is a teaser and the disclosure toggle — one affordance for the whole row, nothing inside it styled as a link; the expanded body is the single place every path appears as a click-to-copy row, including the source itself.
- **In-article clicks are intercepted in the CAPTURE phase** (`frame.js`). Dictionary scripts install their own handlers inside the article — LDOCE6's `entry.js` calls `stopPropagation()` on a speaker `<img>` so that playing a sound does not also toggle the accordion around it — and a bubble-phase listener on `document` never sees those clicks. The browser then followed the link and replaced the article with a bare media player. Capture runs on the way down, before any in-article handler can stop it, and only acts on anchors we recognise, so the dictionary's own toggles keep working.
- **Article sandboxing (hybrid)**: script-free articles render in a Shadow DOM (`:host{all:initial}`) — cheap, total CSS isolation both ways. Script-bearing articles (custom tooltip JS, MathJax) auto-detect into a **sandboxed iframe** with a bridge script (`/assets/frame.js`): auto-height via ResizeObserver+postMessage, bword:// and dblclick lookups, theme sync. Rationale: innerHTML never executes `<script>` in a shadow root and dictionary JS cannot see shadow content — iframes are the only correct primitive for third-party HTML+JS.
- Layout: draego chrome (sticky autohide bar, accordion, dl/dt/dd) with φ-scale spacing; mark.js vendored locally (draego pulls from CDN — fix); dark mode per mdict-go-web/notes.

## 6b. Later additions (implemented)

- **Ingest levels**: `store.Level` = `text` (default; contains + full-text) or `headwords` (headword modes only — incl. trigram/contains — much smaller DB; auto-upgraded by rebuild when full text is requested later). API `level=headwords`.
- **SQLite drivers**: build-tag pair — default `mattn/go-sqlite3` (cgo, `-tags sqlite_fts5`, D4) and `-tags purego` → `modernc.org/sqlite` (pure Go, FTS5 included) used by release cross-builds so one ubuntu runner builds all platforms (D4 amendment).
- **First-run setup**: missing/empty dict dir → `/` serves a setup page; `/api/setup` validates live, switches the registry without restart, persists `DICT_DIR` via comment-preserving `config.SaveKey`.
- **.spx audio (D18)**: transcoded to WAV at `/res` time (cache under db-dir/spxcache, single-flight per cache key). cgo builds decode **in-process** — `internal/speex` (in-house Ogg demuxer) over a vendored decode-only libspeex in `internal/speex/clib` — and never look for an external binary. `SPEEX_BACKEND=external` forces the `speexdec` CLI; purego builds fall back to it automatically, resolving it **once at launch** (`SPEEXDEC` override → next to the executable → `$PATH`) and printing the resolved path or an install hint.
- **Library hygiene**: pre-D20 flat databases are adopted into folders at startup (`store.AdoptLoose`, rename only). `wudict clean` lists/deletes incomplete folders (no `text.db`), unreadable databases, interrupted ingest temps, and loose databases that survived adoption because the same dictionary already has a folder (true duplicates). A healthy prepared dictionary is never listed — not when its source is deleted (D17's data-loss guard, kept) and not when its source changed (D20: re-indexing overwrites in place, so nothing is superseded).
- **Hardening**: `dict.Open`/`OpenReader` convert parser panics on corrupt files into errors; server has a recover middleware; graceful SIGINT shutdown; port-in-use hint.
- **Resource Content-Type**: `/res/` serves from an authoritative `webMIME` extension→type map (`server.go`), overriding both `mime.TypeByExtension` (returns `text/plain` for `.css`/`.js` on some OSes) and stale `media.db` mime rows — otherwise browsers reject stylesheets/scripts under strict MIME checking.
- **Open latency & progressive UX (P8, D12)**: `dict.Probe` (header-only metadata, per-format prober; `dict.HasProber`) + lazy `upgraded.src` + `Registry.Warm()` remove the heavy full-index build from page load and from ingested-dict opens (the double-open is gone; the embedded Store's auto-attached media.db serves resources first, source opened only on a miss). `/api/search` is NDJSON-streamed (`begin`/`hit`/`end`, per-hit preference slot index); the client renders each accordion on arrival, auto-opens the top-preference non-empty dict, and drives an enabled+ordered dictionary list from the panel (drag gripper + ▲▼⏫⏬, enable/disable, persisted in localStorage; All-dicts queries fewer results/dict with per-dict "more…"). Ingest temp files are per-call unique (`store` `tempDBName`) so concurrent ingests (warm vs on-demand) can't clobber.

## 6c. Native-ingested model (D15, P10–P11) & the library (D19/D20, P12)

- **The library** is `store.DefaultDBDir()` (DB_DIR): wudict's own working area, holding one **folder per prepared dictionary** — `<db dir>/<source file name>/{text.db, media.db, info.txt}` (D20). A dictionary is one folder: copyable, zippable, transferable; dropped into a dictionary folder it is discovered by its `text.db`.
- **Dictionary folders (D21)**: `DICT_DIR` holds one *or several* roots — repeatable flag, `os.PathListSeparator` in the environment, TOML array in the file. `dict.DiscoverAll` walks them in order, dedupes by canonical path (overlapping roots are normal), and reports per root what it contributed vs. what it holds; a missing root is skipped, not fatal. `Discover` resolves a symlinked root before walking (`WalkDir` lstats, so a symlinked folder used to yield nothing).
- **It is not a discovery root** (D19). `DICT_DIR == DB_DIR` is a startup error and `dict.ExcludeDir(DB_DIR)` keeps discovery out of it wherever it sits. Only `USE_CACHED` (default off, set by "Use these dictionaries" on the setup page) lists prepared dictionaries; when on, one is hidden only if its source is among the discovered files (dedup by source path). `server.native` wraps a prepared dictionary with no live source; `upgraded` wraps one whose source is still on disk (so resources fall back to it).
- **Folder resolution**: `store.LookupDir` (read-only, ownership-verified) on every read path; `store.ClaimDir` (atomic `os.Mkdir` + immediate `source` claim) only when ingesting. `store.PreparedFor` = LookupDir + `SourceChanged`, so an edited source falls back to the direct backend and is re-indexed in place.
- **Direct = preview**: direct backends need to be correct and acceptable, not fast (D15). Their headword indexes are lazy (§1), gomdict caches 8 decompressed record blocks (FIFO, open-per-miss to respect the `.mdd` fd budget), and no binary-search/collation work is taken on (silent-wrong-results risk).
- **Formats**: mdx/mdd, stardict, slob, dsl, **bgl** (`internal/format/bgl`, P10 — streaming reader: metadata pass then lazy per-block second pass, tolerates BGL's absent/zero CRC; type-2 resources scanned lazily; DSL-style auto-ingest on first open).
- **Extension matching**: `dict.Registry` matches the **longest registered suffix** (`matchKey`), so `.dsl.dz` beats `.dz` and an unregistered multi-part companion like `dict.dz` is neither discovered nor opened by mistake (it is reached through its `.ifo`).

## 6c-bis. Seeing and editing the configuration (D22, P16)

- `GET /api/config` — folders with per-root status, library dir + prepared count, config file path, the origin of `DICT_DIR` (flag/env/file/default) and whether saving to the file can take effect, the platform reveal label, and whether the caller is on loopback.
- `GET /setup` — the folder editor, always reachable, pre-filled, with Cancel when dictionaries are already in use. `/` still serves it automatically while the registry is empty.
- `GET /api/reveal?path=` — opens the platform file manager (`open -R` / `explorer /select,` / `xdg-open <dir>`). Refused unless the path is one the UI already displays **and** the request comes from loopback.
- Panel: a collapsed "Folders & configuration" section carries all of the above plus **Rescan folders**, which was moved out of the panel header — it re-walks every folder and re-opens every dictionary, too expensive to be a one-click curiosity.

## 6d. Terminal output (P13)

House style lives in the `internal/logx` package doc and is enforced by keeping printing OUT of library packages:
- **Every message about a dictionary names it** — `"Espasa Calpe": 2 entries indexed in 0.4s`. Unattributed lines (`ingest: 6931 unresolved link targets`) are useless when 55 dictionaries are being prepared at once. `logx.Dict(name)` builds the prefix.
- **Libraries return, callers print.** `store.IngestLevelReport` returns a `Report` (entries, unresolved links) instead of writing to stderr; the CLI prints it, the server logs it verbosely — because only they know whether an ingest is a foreground command or one of fifty silent background builds (D13).
- **Levels**: `logx.V` verbose detail · `logx.Warn` real degradation · `logx.Status` progress on a slow foreground step · `logx.Progress`/`ClearLine` in-place counters, **suppressed when stderr is not a terminal** (they are for a human watching a wait, and their carriage returns collide with results on stdout).
- **Startup prints the effective configuration** — dictionary folder, library (DB_DIR), config file in use, address, .spx decoder, indexing mode, and what is being served — each folder counted by what *it* contributed, then a single next-step line when there is something to do.

## 7. Non-goals (v1)
Writing/exporting formats; entry editing; draego's `.db` as input (one-off migration script if ever needed); EPWING/etc. (BGL moved *into* scope in P10.)
