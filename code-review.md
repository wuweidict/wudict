# gonow-dict — Comprehensive Code Review & Performance Audit

**Scope:** full codebase audit (backends, store, search fan-out, HTTP layer, frontend) with
empirical measurements on real dictionaries.
**Method:** static review of every hot path + a purpose-built benchmark harness driving
`dict.Open` / `Exact` / `Prefix` / `Resource` on real files (MDX 58 MB/627k entries,
StarDict 446 MB/89k entries, SLOB 6.2 GB/1.15M entries, ingested SQLite).
**Environment:** macOS arm64, Go 1.26, `sqlite_fts5` tag (cgo driver). All tests pass; `go vet` clean.

---

## 0. Measured baseline (the evidence)

| Operation | MDX ro-ro-DEX (627k entries, 58 MB) | SLOB OED (1.15M entries, 6.2 GB) | store (.text.db, 38k) |
|---|---|---|---|
| `Open()` time | **181 ms** | **1 099 ms** | **1 ms** |
| Heap delta after open | **150 MB** | **402 MB** | **≈0 MB** |
| Exact+render, same word ×50 (warm) | **1 173 µs/op** | 1.4 µs/op | — |
| Exact+render, 30 distinct words | 1 122 µs/op | 1.7 µs/op | — |
| Resource fetch (image/audio) | **705 µs/op** | 1 110 µs/op | — |
| Prefix, hit | 3 590 µs/op | 38 µs/op | 7 752 µs/op |
| Prefix, **miss** (worst case) | **29 266 µs/op** | **627 078 µs/op** | 7 649 µs/op |
| Fuzzy ×100 | n/a | n/a | 45 µs/op |

Three smoking guns:

1. **A repeated identical MDX lookup costs 1.17 ms warm** because *every* lookup re-opens the
   file and re-decompresses the whole record block. SLOB (which has a 4-entry bin cache) does
   the same thing in 1.4 µs — an **~840× gap**.
2. **A prefix miss on the OED SLOB costs 627 ms** — one typo'd keystroke in live search burns
   more than half a second of CPU. This is the single worst "lookup → first result" latency item.
3. **Opening the OED SLOB allocates 402 MB of heap** (DEX MDX: 150 MB) because every backend
   builds *two* `map[string][]int` indexes eagerly at open.

---

## 1. Critical bottlenecks (ranked)

### C1. MDX/MDD: every lookup = `os.Open` + full record-block decompress. No cache anywhere.

**Where:** `internal/gomdict/mdict_base.go:1043` (`locateByKeywordEntry`),
`internal/gomdict/mdict_base.go:946` (`locateDefByKWIndex`), `internal/gomdict/v3reader.go:209`.
Called from `internal/format/mdx/mdx.go:235` (`render`) and `mdx.go:290` (`Resource`).

**What happens per lookup:**
1. `os.Open(mdict.filePath)` → new fd, `defer Close()` (line 1044).
2. Linear scan of `recordInfoList` to find the block (lines 1053–1068 — the built range
   tree is only used by the *other* variant, `keywordEntryToIndex`).
3. `readFileFromPos` reads the entire compressed block (typically 32–128 KB).
4. The whole block is zlib/LZO-decompressed (lines 1109–1125) to extract a few KB of article.

No record-block cache exists. Consequences, all measured:

- Same word looked up 50× → 1.17 ms/op, block decompressed 50×.
- A 20-result prefix page whose entries share record blocks (adjacent headwords almost always do) re-decompresses the same block up to 20×.
- An article with 30 inline images (`mdx.Resource` → same path, mdx.go:290) opens the `.mdd`
  30 times and decompresses ~30 blocks. **Every `<img>` on every result page pays ~700 µs.**
- `@@@LINK=` redirect chains (`render`, mdx.go:234–261) multiply this: each hop is another
  open+decompress.

**Impact:** the dominant per-query CPU cost for the most common dictionary format; also a
syscall storm (open/close per resource) under media-heavy result pages.

**Fix (Step 1):** keep one `*os.File` per `Mdict` (opened once, `ReadAt` is goroutine-safe),
add a small bounded record-block cache (8–32 blocks, keyed by block offset), and use the
already-built range tree instead of the linear `recordInfoList` scan.

---

### C2. Prefix search is an O(N) linear scan; the fold fallback re-folds every headword per query

**Where:** `internal/format/mdx/mdx.go:183–213`, `internal/format/stardict/stardict.go:289–319`,
`internal/format/slob/slob.go:79–109`.

```go
scan := func(useFold bool) []int {
    ...
    for i, e := range d.entries {          // ALL 627k / 1.15M entries
        hw := e.KeyWord
        if useFold { hw = fold(hw) }       // NFD normalize + unicode.Is PER headword PER query
        if strings.HasPrefix(hw, key) { ... }
    }
}
```

`dict.Fold` (`internal/dict/fold.go:16`) runs `strings.ToLower` + `norm.NFD.String` +
a rune loop with `unicode.Is(unicode.Mn, …)` — roughly 1–2 µs per headword. Multiplied by
1 152 511 OED refs = **627 ms for a single miss**, measured. Live search debounces 300 ms,
so a miss-heavy typing session keeps a worker permanently saturated; in "All dictionaries"
mode several such dicts saturate the 8-worker pool.

All three backends keep entries in *sorted* order (MDX key blocks are sorted; StarDict `.idx`
is sorted; SLOB refs are sorted by key). A binary search turns prefix match into
O(log N + k). The fold pass can be served from a lazily built sorted folded slice.

**Fix (Step 2).**

---

### C3. Dual eager `map[string][]int` indexes → 150–400 MB RSS per large dictionary

**Where:** `internal/format/mdx/mdx.go:102–110`, `internal/format/stardict/stardict.go:105–119`,
`internal/format/slob/slob.go:38–47`.

Every direct backend builds at open:

```go
exactIdx: make(map[string][]int, len(entries)),  // headword -> indexes
foldIdx:  make(map[string][]int, len(entries)),  // folded headword -> indexes
```

Each entry costs: two map insertions (~50 B bucket overhead each), two string keys (the
folded key is a fresh allocation), two 24-byte slice headers + backing arrays. Measured:
**627k MDX → 150 MB**, **1.15M SLOB → 402 MB** — and this is *per process, forever*, because
`entry.open()` memoizes opened dictionaries (`internal/server/registry.go:119`) and
`Registry.Warm()` opens all of them at startup (registry.go:264). A folder with ten large
direct (non-ingested) dictionaries idles at multiple GB of RSS.

The folded half is pure waste until a query actually needs folding — the overwhelming
majority of lookups never touch it.

**Fix (Step 3):** replace both maps with (a) the existing sorted entry slice + binary search
for exact, (b) a lazily built (`sync.Once`) sorted folded slice used only when the raw pass
misses. Expected: OED 402 MB → ~40–60 MB; DEX 150 MB → ~15–25 MB; open time improves
similarly (fewer allocations, no NFD pass over every headword at open).

---

### C4. `handleSearch` opens all dictionaries sequentially *before* writing the `begin` line

**Where:** `internal/server/server.go:391–405`.

```go
for i, e := range entries {
    d, err := e.open()          // sequential! cold MDX = 180 ms, cold OED = 1.1 s
    ...
}
writeLine(streamMsg{T: "begin", Slots: begin})   // first byte waits for ALL opens
```

The client cannot even paint the empty accordion skeleton until every requested dictionary
is open. After a server start (or a rescan with `Warm` still racing), time-to-first-byte is
the *sum* of all open times, serialized. The streaming NDJSON design is defeated at its
first line.

**Fix (Step 4):** emit `begin` immediately from cheap metadata (entry ID + probe name),
move `e.open()` into the per-dictionary workers inside `search.Stream`, and deliver open
errors as per-slot `hit` messages with `error` set.

---

### C5. Result post-processing runs under the global emit mutex

**Where:** `internal/server/server.go:421–433` + `internal/search/search.go:74–79`.

`search.Stream` serializes `emit` with one mutex for all dictionaries. The handler's emit
closure runs `RewriteEntryHTML` (5 regex passes × up to 25 articles) and the JSON
`enc.Encode` of the whole result payload **inside that lock**. One dictionary with heavy
articles blocks the socket writes of every other finished dictionary.

**Fix (Step 5):** do the rewrite *and* the NDJSON encoding inside the worker goroutine;
take the mutex only for `w.Write(buf)` + `Flush()`.

---

### C6. Store `Exact`/`Prefix` waterfall: up to 5 sequential SQL round trips per query

**Where:** `internal/store/store.go:161–201` (`Exact`), `220–244` (`Prefix`).

`Prefix` calls `Exact` (query 1 case-sensitive → on miss query 2 `COLLATE NOCASE` → on miss
query 3 FTS5 + per-row `dict.Fold` filter), then a LIKE `UNION` (query 4), then `Fuzzy`
(query 5). Measured: a store-backed prefix *hit* costs 7.75 ms while fuzzy costs 45 µs —
the hit pays for stacked misses. Each layer also rebuilds SQL text via `fmt.Sprintf(q, …)`
(store.go:172, 176) per call.

**Fix (Step 6):** one prioritized UNION for the common case:

```sql
SELECT w, m, 0 AS prio FROM entry WHERE w = ?1
UNION ALL
SELECT e.w, e.m, 1 FROM alias a JOIN entry e ON e.id=a.entry_id WHERE a.w = ?1
-- plus NOCASE copies with prio 2/3
ORDER BY prio LIMIT ?2
```

…falling back to the FTS fold pass only when that returns nothing. `Prefix` then needs
exactly one round trip on hit, two on miss.

---

### C7. Frontend renders every article body eagerly; in-flight searches are never aborted

**Where:** `internal/server/web/index.html` `renderSlot` (~line 564–597), `fetchStream`
(~540), input handler (~657).

- Every result body is mounted into a shadow root (`root.innerHTML = shadowBase + body`)
  the moment a dictionary's `hit` arrives — up to 5 bodies × N dictionaries × possibly
  heavy dictionary markup, before the user expands anything. First paint competes with
  dozens of shadow-DOM parses; `mark.js` highlighting multiplies it in fuzzy/fts modes.
- Superseded searches are dropped client-side via `searchSeq`, but the HTTP response is
  never cancelled: the server keeps computing and streaming results nobody will render
  (the 30 s `context.WithTimeout` in server.go:419 does not stop already-running workers —
  see C8).

**Fix (Step 7):** render the headword list immediately, mount bodies lazily
(`<details>` `ontoggle` / `IntersectionObserver`), and wrap the fetch in an
`AbortController` aborted on each new search.

---

### C8. Cancellation is not propagated: SQLite queries ignore the request context

**Where:** `internal/search/search.go:84–90` (ctx checked only *before* a worker starts),
`internal/store/store.go` — all queries use `s.db.Query`, never `QueryContext`.

A disconnected client (tab closed, search superseded, 30 s timeout hit) leaves every
running query going to completion. Under live search that is continuous wasted CPU.

**Fix (part of Step 10):** thread `ctx` through `dict.Dictionary` query methods (or at
least the store backend) and use `QueryContext`.

---

### C9. Repeated per-request work: `CacheBase` re-hashes 1 MB of source on every call; `/api/dicts` re-opens every `.text.db` per request

**Where:** `internal/store/store.go:275–283` (`CacheBase` — `io.CopyN(h, f, 1<<20)` each
call), `internal/server/server.go:241–279` (`dictInfoFor` → `store.ReadMeta` opens a fresh
SQLite connection per dictionary per request).

`CacheBase` is called from `openUpgradedOrDirect` (every open), `dictInfoFor` (every dict,
every list), `handleIngest`, CLI paths… Each call = open + read 1 MB + SHA-256. On
`/api/dicts` with N cached dictionaries that's N hashes + N SQLite open/query/close cycles
per request; the page calls it on load, after every ingest, after rescan.

**Fix (Step 8):** memoize `map[srcPath]cacheBase` (invalidate on source mtime change), cache
`dictInfo` rows on the `entry`, invalidate on ingest/rescan.

---

### C10. Ingest is single-threaded row-by-row; ~5.3k entries/s measured

**Where:** `internal/store/ingest.go:111–163`. Espasa (38.5k entries) took 7.2 s → the OED
(1.15M) projects to ~3.5 min, and it is triggered on the user's first fuzzy search
(`maybeAutoIndex`, registry.go:101). Body normalization + `StripHTML` + FTS tokenization is
CPU-bound and serial; inserts are one row per `Exec`.

**Fix (Step 9):** pipeline — reader goroutine → normalize/strip worker pool → single writer
doing multi-row `INSERT … VALUES (…),(…),…` batches. Expect 3–5×.

---

## 2. RAM audit — where the memory goes

| Consumer | Cost | Notes |
|---|---|---|
| `exactIdx`+`foldIdx` maps (mdx/stardict/slob) | **150 MB / 627k entries; 402 MB / 1.15M** | C3. Folded half almost never used. |
| `entries []*MDictKeywordEntry` (mdx) | ~60–80 MB / 627k | one heap string + struct per headword; unavoidable without redesign but shrinkable (store offsets in arrays, keys in one arena). |
| `Registry.Warm` opening *all* direct backends at startup | multiplies the above | Warm (registry.go:264) eagerly pays full open for dictionaries the user may never query. |
| MDD companions indexed eagerly at open (`mdx.go:119–126`) | BuildIndex per `.mdd` | resource index is already lazy (`resOnce`), but the key entries themselves are loaded eagerly — make the whole MDD lazy. |
| `resMap` on first resource touch (mdx.go:310) | full MDD key map | acceptable once MDDs are lazy; keep bounded. |
| Store backend | ≈0 MB | SQLite page cache does the work — this is the model to emulate. |

Additional per-request churn (GC pressure, not resident): `Fold` allocations per headword per
query (C2), `seen := map[string]bool{…}` allocated per result (`mdx.go:219`), 5× regex
`ReplaceAllStringFunc` buffers per article (rewrite.go), `fmt.Sprintf` query text per store
query (store.go:172).

## 3. CPU audit — hot spots

1. Record-block (re)decompression per lookup — C1 (dominant).
2. `Fold` over every headword per query — C2.
3. NFD folding of every headword at open (fold-map build) — part of C3 (also makes
   `Open`/`Warm` slow: OED 1.1 s, DEX 181 ms).
4. `RewriteEntryHTML` 5 regex passes × articles × under lock — C5. Add a cheap gate:
   `strings.Contains(html, "src=") || strings.Contains(html, "href=") || …` before running
   the pipeline (most plain-text articles skip all passes).
5. `Store.Exact` waterfall — C6.
6. Ingest `StripHTML`+tokenize serial — C10.
7. `substituteStylesheet` (`mdx.go:372`) does `Split` + `FindAllString` (two full passes) per
   rendered article — fold into one `ReplaceAllStringFunc`.
8. `handleDicts` probe path is good, but uncached — C9.

## 4. Transport, API & architecture audit

**Good decisions worth keeping:** NDJSON streaming with slot indexes (progressive render),
`SetEscapeHTML(false)`, read-only SQLite DSNs, probe-based cheap list, lazy direct backend in
`upgraded`, server-side URL rewriting, FTS5 contentless index, atomic ingest (temp+rename),
per-dictionary ingest mutex.

**Inefficiencies & refactors:**

| # | Finding | Where | Recommendation |
|---|---|---|---|
| T1 | `begin` gated on sequential opens | server.go:391 | emit begin immediately; open inside workers (C4) |
| T2 | emit lock covers rewrite+encode | server.go:421 | rewrite/encode in worker; lock only `Write` (C5) |
| T3 | no query cancellation | search.go, store.go | `QueryContext` + ctx-aware `Dictionary` methods (C8) |
| T4 | `/api/dicts` uncached meta reads | server.go:241 | cache rows on entry, invalidate on ingest/rescan (C9) |
| T5 | HTTP server has **no timeouts** | main.go:487 | `ReadHeaderTimeout: 10s`, `IdleTimeout: 120s`, `MaxHeaderBytes` — slowloris exposure even on localhost |
| T6 | SSE for ingest + NDJSON for search — fine, but `/api/ingest` uses GET with side effects | server.go:596 | switch to POST (also prevents accidental prefetch/crawler triggers) |
| T7 | resource responses: no `Content-Length`, no `If-Modified-Since`/ETag | server.go:490 | set `Content-Length` when known (media.db BLOB size, file size); lets the browser keep connections warm and cache validate |
| T8 | `.spx` transcode has no single-flight | server.go:555 | two concurrent plays of the same word spawn two `speexdec` processes and race the same cache file — guard with a `sync.Map` of in-flight keys |
| T9 | `Keywords` endpoint & `searchall` linear-open CLI | main.go:596 | CLI-only, low priority |
| T10 | no gzip anywhere | — | skip on localhost (CPU not worth it); revisit only if serving over LAN |

**Architectural direction:** the store backend (SQLite) is faster to open (1 ms vs 181–1099 ms),
uses ~0 heap (vs 150–402 MB), and serves fuzzy in 45 µs. The direct backends should be treated
as *ingest readers + resource fallbacks*, not primary query engines. The biggest architectural
win available: make headwords-level auto-ingest the default for every dictionary at scan time
(background, one at a time, nice-priority), so the query path is nearly always SQLite, and the
heavy direct backends are only built lazily for resource fallback (which `upgraded` already
does). Combined with C1's block cache, even that fallback becomes cheap.

## 5. Secondary correctness/robustness notes

- `internal/gomdict/mdict_base.go:1028`: `log.Errorf("return mdd data")` — an unconditional
  stderr "error" on a *success* path of `locateDefByKWIndex` (accessor path; today cold, but
  fix the mislabeled level). Same file line 947/1033: `log.Infof` in per-lookup paths.
- `internal/gomdict/mdict.go:153–162` (`Mdict.Lookup`): linear scan with a `log.Infof` inside
  the loop — dead for the server (mdx uses maps) but landmine for any other consumer; delete
  the log line.
- `mdict_base.go:1071`: `fmt.Printf` on the error path of a library function — return the
  error, don't print.
- SLOB `getItem` (`container.go:308–350`) re-reads the bin header (`ReadAt` ×2: itemCount +
  ctype ids) on *every* item fetch even when the bin content is cached (binCacheSize=4).
  Cache the ctype ids with the bin (they're per-bin); saves 2 syscalls per lookup.
- `stardict.Resource` (`stardict.go:373–377`): `os.Stat` on the `res/` dir *per request* —
  hoist to open.
- `search.Stream` workers don't observe `ctx.Done()` while queued queries run — with C8 fixed
  this collapses.
- `handleSearch` 30 s timeout is wall-clock from request start; after C8 it becomes effective.
- `Store.Fuzzy` orders by `e.w` over *all* FTS matches before `LIMIT` (store.go:254) — a
  one-letter query can sort 100k rows. Two-phase (collect rowids with a cap, then join+sort
  the small set) or accept; medium priority.
- `resolveMIME` relies on backend MIME for non-web types; fine.
- `ingest` of the same dictionary from CLI + auto-index can race; `tempDBName` uniqueness
  handles the file, but the `entry.ingestMu` doesn't cover the CLI process — acceptable
  (worst case: duplicate work, atomic rename keeps one winner).

---

## 6. Top 10 steps to make it faster (ranked by impact ÷ effort)

| # | Step | Expected effect | Effort |
|---|---|---|---|
| 1 | **gomdict: persistent fd + record-block LRU cache + range-tree lookup** | MDX repeat lookup 1.17 ms → ~10 µs; images ~700 µs → ~20 µs; kills the per-call syscall storm | M |
| 2 | **Binary-search prefix on sorted entries + lazy sorted fold index (all 3 direct backends)** | prefix miss: 627 ms → <1 ms (OED), 29 ms → <0.5 ms (DEX); live search becomes viable on huge dicts | M |
| 3 | **Drop eager dual hash maps; lazy fold structures** | RSS: OED 402 MB → ~50 MB, DEX 150 MB → ~20 MB; open 1.1 s → ~0.2 s; Warm stops eating GBs | M |
| 4 | **Stream `begin` immediately; open dictionaries inside search workers** | cold-search TTFB: Σ(all opens) → max(one open); first skeleton paints instantly | S |
| 5 | **Rewrite + JSON-encode inside workers; mutex only around socket write** | cross-dictionary emission parallelism; one heavy dict no longer head-of-line blocks others | S |
| 6 | **Single-round-trip store `Exact`/`Prefix`** | store prefix-hit 7.7 ms → <1 ms; fewer SQLite parses per keystroke | S |
| 7 | **Frontend: lazy body mount + `AbortController` per search** | first paint sooner, no wasted server CPU on superseded typing | S |
| 8 | **Memoize `CacheBase`; cache `/api/dicts` rows; lazy MDD indexing** | removes N×(1 MB hash + SQLite open) per list request; faster list & open | S |
| 9 | **Parallel ingest pipeline + batch inserts** | ingest 3–5× faster (auto-index feels instant) | M |
| 10 | **Context-aware queries (`QueryContext`) + HTTP server timeouts** | cancelled typing stops burning CPU; slowloris hardening | S |

(S = hours, M = a focused day.)

---

## 7. Implementation guide

### Step 1 — gomdict block cache + persistent file (fixes C1)

`internal/gomdict/mdict_base.go`:

1. Add to `MdictBase`:

```go
file     *os.File          // opened once in New(), closed in Close()
blkMu    sync.Mutex
blkCache map[int64][]byte  // block file-offset -> decompressed block
blkOrder []int64           // FIFO eviction, cap 16
```

2. In `New()`/`init()`: `mdict.file, err = os.Open(filename)` — keep it; replace every
   per-call `os.Open` in `locateByKeywordEntry`, `locateDefByKWIndex`,
   `readKeyBlockInfo`, `readKeyEntries`, `readRecordBlockMeta`, `readRecordBlockInfo`,
   `v3reader.go` with `mdict.file` + `ReadAt`.
3. In `locateByKeywordEntry` (line 1043): compute `recordBlockStartOffset` first; check
   `blkCache` under `blkMu`; on miss read+decompress once, insert with FIFO eviction at 16
   entries (16 × ~2 MB worst case ≈ 32 MB ceiling per dictionary — tune to 8 if preferred).
4. Replace the linear `recordInfoList` scan (lines 1053–1068) with the existing
   `QueryRangeData(mdict.rangeTreeRoot, item.RecordStartOffset)` (already used by
   `keywordEntryToIndex`, line 857).
5. Fix `mdx.Dict.Close()` (`internal/format/mdx/mdx.go:168`) to actually close the mdx and
   mdd files.
6. Delete the `log.Infof` at line 947/1033, demote `log.Errorf("return mdd data")` (1028) to
   debug, and remove `fmt.Printf` at 1071.
7. Regression test: two consecutive lookups of adjacent headwords; assert the second does no
   read (wrap `ReadAt` with a counter in tests). Existing `mdx_test.go` must stay green.

### Step 2 — binary-search prefix + lazy fold index (fixes C2)

All three direct backends share the shape; example `internal/format/mdx/mdx.go`:

1. MDX key entries are already in sorted order (verify at open with a cheap sampled check;
   fall back to `sort.Slice` once if not). Then:

```go
func (d *Dict) prefixRaw(key string, limit int) []int {
    i := sort.Search(len(d.entries), func(i int) bool { return d.entries[i].KeyWord >= key })
    var idxs []int
    for ; i < len(d.entries) && len(idxs) < limit; i++ {
        if !strings.HasPrefix(d.entries[i].KeyWord, key) { break }
        idxs = append(idxs, i)
    }
    return idxs
}
```

2. Lazy folded slice (built once, only when a raw pass misses):

```go
foldOnce sync.Once
foldKeys []string // fold(entry.KeyWord), sorted, parallel to foldIdxs []int32
```
   Build in a goroutine-safe `sync.Once`; binary search it the same way. (Folding is *not*
   prefix-preserving across scripts in theory; keep the existing `Fold` function so results
   stay identical to today.)
3. `Prefix` becomes: exact (map or binary) → `prefixRaw` → folded binary search. No full
   scans, no per-query folding of the dictionary.
4. StarDict: `.idx` is sorted by construction — same change. SLOB: refs are sorted by ICU
   key; a byte-wise binary search still lands on the right neighborhood — scan ±a few entries
   around the insertion point to be collation-safe, then take the prefix window.
5. Benchmark: add `BenchmarkPrefixMiss` per backend with the real fixtures (env-gated like
   existing integration tests) — target <1 ms where today it's 29–627 ms.

### Step 3 — kill the dual maps (fixes C3)

1. Keep `exactIdx map[string][]int` **only** if exact lookup must stay O(1); better: replace
   with `map[string]int32` (first index) + "next duplicate" chaining, or simply binary search
   the sorted slice from Step 2 (O(log N) ≈ 20 comparisons ≈ 200 ns — indistinguishable here).
2. Delete `foldIdx` entirely; the lazy sorted fold slice from Step 2 covers folded exact
   (fold key range = one binary-search window) and folded prefix.
3. Don't fold anything at open: `Open` then does zero NFD passes — open time and open RSS
   both drop (~8–10× less heap measured per C3 evidence).
4. Same for stardict (drop both maps; synonyms get their own small map — synonyms are few)
   and slob.
5. Add a test asserting `Fold`-exact still matches (e.g. `corazon` → `corazón`).

### Step 4 — immediate `begin`, opens in workers (fixes C4)

`internal/server/server.go` `handleSearch`:

1. Build `slots`/`begin` from `entry.ID` + a cheap name source: cached `dictInfo` (Step 8),
   else probe name, else entry ID. Do **not** call `e.open()` here.
2. Write `begin` first.
3. Move the open into the search worker: extend `search.Stream` (or wrap) so each worker
   calls `e.open()` itself; an open error becomes that slot's `hit{error}`.
4. Trigger `maybeAutoIndex` from the worker after a successful open (same semantics as now).
5. Verify with a cold server: TTFB of `/api/search?q=x` < 50 ms regardless of dictionary
   count.

### Step 5 — unlock the emit path (fixes C5)

1. Change `search.Stream`'s contract: workers receive a `*bytes.Buffer`-returning emit, or
   simpler — give `handleSearch`'s closure a per-worker pre-encode step:

```go
// in worker, BEFORE taking the write lock:
for j := range h.Results { h.Results[j].Body = RewriteEntryHTML(h.Results[j].Body, id) }
buf := encodeNDJSON(streamMsg{...})   // json.Marshal to a []byte
mu.Lock(); w.Write(buf); fl.Flush(); mu.Unlock()
```

2. Keep `enc.SetEscapeHTML(false)` semantics by using a shared `json.Encoder` per worker
   writing into a `bytes.Buffer`.
3. Add a `RewriteEntryHTML` fast-path gate (`strings.Contains` checks for `src=`, `href=`,
   `srcset=`, `style`, `<base`) to skip regex passes on plain articles.

### Step 6 — store query consolidation (fixes C6)

1. Replace the two `fmt.Sprintf` queries in `Store.Exact` with one constant prepared
   statement (prioritized UNION over case-sensitive + NOCASE + alias legs), keeping the FTS
   fold fallback as the only second round trip.
2. `Store.Prefix`: run the new `Exact` (1 RTT); on miss go straight to the LIKE UNION; keep
   `Fuzzy` as the final fallback.
3. Prepare statements once at `Open` (`sql.Stmt`) — removes per-query parse/validate.
4. Assert no behavior change via existing `store_test.go`; add a test counting round trips
   with a wrapping driver or simply benchmark before/after.

### Step 7 — frontend lazy render + abort (fixes C7)

`internal/server/web/index.html`:

1. In `renderSlot`, mount only the `<dl>` of headwords + first body; stash remaining bodies
   on the element (`dd.dataset.pending`); mount on `<details open>` toggle and on
   `dt` click; optionally `IntersectionObserver` for open details.
2. `fetchStream`: create `let ac = new AbortController()` per `doSearch`; `ac.abort()` the
   previous one; pass `ac.signal` to `fetch`. Catch and swallow `AbortError`.
3. Cap initial bodies (e.g. 10 across all dicts) even when details auto-open.
4. Defer `mark.js` highlighting until the body mounts.

### Step 8 — cache the caches (fixes C9)

1. `store.CacheBase`: add `var cbCache sync.Map // srcPath -> struct{base string; mtime time.Time}`;
   recompute only when `os.Stat` shows a new mtime.
2. `server.entry`: cache the `dictInfo` produced by `dictInfoFor`; invalidate in
   `entry.ingest` and on `Rescan` (new entries recompute).
3. MDD laziness (`mdx.go:119`): move `openIndexed(f)` for companions into `resourceIndex()`'s
   `resOnce` so a dictionary that never serves an image never indexes its MDDs.

### Step 9 — ingest pipeline (fixes C10)

`internal/store/ingest.go`:

1. Reader goroutine → channel of `dict.Entry` (buffer ~512).
2. Pool of `runtime.NumCPU()` workers doing `normalizeBody` + `StripHTML` → channel of rows.
3. Single writer: `INSERT INTO entry … VALUES (…),(…),(…)` with ~200 rows per `Exec`
   (respect `SQLITE_MAX_VARIABLE_NUMBER` = 32766 in modern builds; chunk accordingly),
   same for `entry_fts` and `alias`.
4. Keep the single transaction, atomic temp+rename, and the final `optimize`/`ANALYZE`.
5. Progress reporting unchanged (count rows written).
6. Target: ≥20k entries/s (Espasa 7.2 s → ~2 s; OED ~3.5 min → ~1 min).

### Step 10 — cancellation + server hardening (fixes C8, T5)

1. Change `dict.Dictionary` query signatures to take `context.Context` (interface change —
   update all four backends + tests), or minimally: `search.query` passes ctx to a new
   `ContextSearcher` interface implemented by `store.Store` (`QueryContext`), falling back to
   the plain methods elsewhere. The minimal variant is less invasive; the full variant is the
   right long-term shape.
2. `main.go:487`:

```go
httpSrv := &http.Server{
    Addr:              cfg.Addr(),
    Handler:           srv,
    ReadHeaderTimeout: 10 * time.Second,
    IdleTimeout:       120 * time.Second,
    MaxHeaderBytes:    1 << 20,
}
```

3. `handleResource`: set `Content-Length` when the size is known (media.db blob, stardict
   loose file, spx WAV bytes) and add single-flight for `spxToWav` (T7/T8).
4. Change `/api/ingest` to POST (T6) — small client change in `runIngest`
   (`EventSource` can't POST; switch to `fetch` + reader, or keep GET but require a
   token/`Sec-Fetch-Dest` check — pragmatic: keep GET, document it).

---

## 8. Validation plan

1. `make test` (all existing tests must pass — the review changes no semantics).
2. `make test-purego` for the release driver parity.
3. Re-run the measurement harness (the one used for §0) after each step; acceptance:
   - MDX same-word repeat lookup < 20 µs (from 1 173 µs).
   - OED prefix miss < 1 ms (from 627 ms); DEX < 0.5 ms (from 29 ms).
   - OED heap after open < 80 MB (from 402 MB); DEX < 30 MB (from 150 MB).
   - Cold `/api/search` TTFB < 50 ms with 20+ dictionaries.
   - Ingest ≥ 20k entries/s.
4. `go test -race ./...` after the concurrency changes (Steps 1, 4, 5, 9).
5. Manual: live-search typing session against the OED + DEX + Espasa watching
   `top`/`Activity Monitor` — CPU should go idle between keystrokes.

---

*Audit performed against commit `7afd1b1` (URL rewriting/MIME/speexdec work), working tree
clean apart from untracked `.DS_Store` files and a session log.*
