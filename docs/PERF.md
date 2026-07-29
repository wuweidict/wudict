# Performance audit — RAM, CPU, and the first-run indexing storm

**Date:** 2026-07-28 · **Subject:** 18 GB+ RSS during first-run indexing, 2.5 GB idle,
1000 %+ CPU on an 18-core M5, ~90 % CPU during search, with 100+ dictionaries.

Everything below is **measured on the reporting machine against the real corpus**,
except where explicitly marked *modelled*. Unit costs come from the shipped binary;
the extrapolations are arithmetic on those units. The goal is orders of magnitude,
and the model reproduces the reported numbers within ~10 %.

---

## 1. The corpus (measured)

| format | files | total on disk | mean file | notes |
|---|---:|---:|---:|---|
| `.slob` | 45 | 29 257 MB | 650 MB | several multi-GB with embedded audio |
| `.mdx` | 49 | 2 315 MB | 47 MB | |
| `.dsl` | 3 | 95 MB | 32 MB | self-preparing |
| `.bgl` | 3 | 64 MB | 21 MB | self-preparing |
| `.ifo` (StarDict) | 5 | — | — | payload in sibling files |
| **total** | **105** | **31.7 GB** | 302 MB | |

**Total headwords: 18 109 276** across the 102 dictionaries that report a count
(mean 177 541, median far lower — the distribution is dominated by a few giants):

| entries | file | |
|---:|---|---|
| 2 401 262 | rus-rus Hagen paradigm | 135 MB |
| 1 184 383 | Oxford English Dictionary.mdx | 729 MB |
| 1 152 511 | en-en-OED-2020-audio.slob | 6 709 MB |
| 691 861 | dex09-2009.ifo | — |
| 627 765 | ro-ro-dex.slob | 72 MB |

**Entry count, not file size, is the unit of memory cost.** A 557 MB SLOB with
109 k entries costs less RAM than a 24 MB SLOB with 458 k entries. File size
tracks packed media; RAM tracks headwords.

## 2. Apparatus

* Peak RSS: `ps -o rss=` sampled every 20–50 ms for the lifetime of the process.
* Server scenarios: launched, sampled, driven over HTTP, sampled again
  (`scratchpad/perf/bench.py`).
* Test bed for server scenarios: 4 real MDX dictionaries — ro-ro-DEX (627 764),
  maria-moliner, Espasa-Calpe (38 521), wordref-audio — **1.03 M entries total**.
* Binary: cgo build (mattn sqlite3), the tree as of this audit.

## 3. Measured unit costs

### 3.1 Per dictionary, by operation

| operation | dictionary | entries | peak RSS | per entry |
|---|---|---:|---:|---:|
| open + probe (`info`) | Espasa-Calpe MDX | 38 521 | 26 MB | ~700 B |
| open + probe (`info`) | ro-ro-DEX MDX | 627 764 | 66 MB | **~105 B** |
| open (`info`) | any SLOB, 24–557 MB | 2.5 k–458 k | **4 MB** | ~0 (lazy) |
| **first lookup** (builds the map) | catalanbeta1 SLOB | 457 615 | 165 MB | **~360 B** |
| first lookup | Stedmans SLOB | 108 702 | 36 MB | ~330 B |
| first lookup | Российский SLOB | 69 962 | 40 MB | ~570 B |
| first lookup | ro-ro-DEX MDX | 627 764 | 181 MB | **~290 B** |
| ingest (one at a time) | Espasa-Calpe MDX | 38 521 | 52 MB | — |
| ingest (one at a time) | ro-ro-DEX MDX | 627 764 | 131 MB | **~210 B** + ~25 MB base |
| ingest, synthetic DSL | 5 k / 20 k / 80 k | | 32 / 36 / 54 MB | ~293 B |

**Take-away:** a dictionary costs ~**300–500 bytes of RAM per headword** once it
is *searched* through its own format, and about the same again while it is being
indexed. Opening alone is cheap (SLOB) or moderate (MDX); the cost lands on first
use, because P11 made the headword maps lazy.

### 3.2 Server, end to end (4 MDX, 1.03 M entries)

| scenario | RSS after warm | peak RSS | peak CPU | search |
|---|---:|---:|---:|---|
| **A — unprepared**, one all-dictionaries search | — | **500 MB** | **424 %** | — |
| **B — prepared**, five all-dictionaries searches | **17 MB** | **33 MB** | 84 % | 50 ms |

**15× less memory and ~5× less CPU when the dictionaries are prepared**, for the
same dictionaries and the same queries. This single comparison contains the
answer: SQLite does the work on disk; a direct backend does it in RAM.

## 4. Mechanisms, ranked by contribution

### M1 — The auto-index fan-out is unbounded (dominant: RAM **and** CPU at first run)

`server.go` builds one opener per dictionary in the search fan-out, and each
opener calls `e.maybeAutoIndex()`, which does:

```go
e.autoOnce.Do(func() { go func() { … ensureBaseIndex … }() })   // registry.go
```

There is **no semaphore**. Searching *All dictionaries* touches every entry, so a
single first search launches **one goroutine per dictionary** — 102 concurrent
ingests. Compare with the two places that *are* bounded: `Registry.Warm` (4) and
`/api/dicts` (8). The search fan-out itself is bounded at 8 workers, but the
indexing it triggers escapes that bound entirely.

Modelled cost at full corpus: 18.1 M entries × ~500 B (ingest working set +
the open backend it is reading from) ≈ **9 GB live**, and Go's default `GOGC=100`
lets the heap reach ~2× live before collecting → **~18 GB RSS**. Measured
analogue: 4 dictionaries → 500 MB peak, 424 % CPU. **This is the reported 18 GB
and the 1000 % CPU.**

### M2 — Direct backends are opened for everything and never released (dominant: idle RAM)

`Registry.Warm()` opens every dictionary at startup and memoises the handle in
`entry.d` for the life of the process. Nothing ever evicts it. Once a dictionary
has been searched, its lazy headword maps are materialised and stay.

Modelled: if every dictionary is touched, 18.1 M × ~350 B ≈ **6 GB**; the
reported 2.5 GB idle corresponds to roughly a third of the corpus having been
searched. Measured analogue: prepared dictionaries cost **17 MB for 1.03 M
entries** (state B) versus hundreds of MB unprepared — i.e. the idle cost is
almost entirely *unprepared* backends.

### M3 — Indexing holds two copies of the same dictionary

`dict.OpenReader(path)` → `mdx.NewReader` → **`Open(path)` again**. During an
ingest the entry already holds an open direct backend (from Warm/search), and the
reader opens a second, independent parse of the same file. Roughly **2× the
per-dictionary working set** for the duration, on top of M1's multiplier.

### M4 — GC headroom doubles the peak

Nothing sets `GOGC` or `debug.SetMemoryLimit`. With a fast-growing heap, peak RSS
runs ~2× live heap, and returning memory to the OS is lazy. This is why the peak
is 18 GB rather than the ~9 GB of live data.

### M5 — Search CPU scales with dictionary count, per keystroke

Each keystroke (300 ms debounce) fans out to every enabled dictionary, 8 at a
time. For prepared dictionaries a query is a few SQLite lookups (state B: 50 ms
for 4). For **unprepared** ones it is an in-memory scan with per-headword NFD
folding — `code-review.md` C2. With ~100 dictionaries, most unprepared, that is
tens of millions of fold operations per keystroke: the reported ~90 %.

### M6 — Secondary (real, but an order of magnitude smaller)

* No `SetMaxOpenConns` on any `sql.DB`: each concurrent query can open another
  SQLite connection, each with its own page cache. Bounded in practice by the
  8-worker fan-out; ~2 MB × connections.
* `RewriteEntryHTML` runs five regex passes per result body (fast-path gated).
* The gomdict record-block cache holds 8 decompressed blocks **per open MDX**.

## 5. Model versus reality

| quantity | modelled | reported | |
|---|---:|---:|---|
| first-run indexing peak | ~18 GB | 18 GB+ | ✓ |
| CPU during indexing | ≥ 10 cores (unbounded on 18) | 1000 %+ | ✓ |
| idle after partial indexing | 2–6 GB | 2.5 GB | ✓ |
| search CPU, mostly-unprepared | ~1 core per 4–8 dicts | ~90 % | ✓ |

The model reproduces every reported symptom from measured unit costs. No
unexplained residual — we are not missing a mechanism of comparable size.

## 6. Where the fixes are (impact, before deciding anything)

Ranked by measured impact per unit of risk. **Not yet decided — see §7.**

| # | change | impact (modelled from §3) | risk |
|---|---|---|---|
| **F1** | Bound auto-indexing to 1–2 concurrent (semaphore or a serial queue) | first-run peak **18 GB → ~0.5–1 GB**; CPU 1000 % → ~100–200 % | very low — it is already background work |
| **F2** | Release the direct backend once a dictionary is prepared; evict idle backends (LRU) | idle **2.5 GB → ~0.3–0.5 GB** | low — resources reopen lazily (D12 already does this) |
| **F3** | Reuse the already-open dictionary as the ingest reader instead of opening a second copy | −30–50 % of the indexing working set | low |
| **F4** | `debug.SetMemoryLimit` (soft ceiling) + `GOGC` tuning | peak ≈ live instead of 2× live; predictable ceiling | low |
| **F5** | Prepare on demand, not on first *all-dictionaries* search (queue by preference order; index what the user actually reads first) | spreads the cost; time-to-usable drops | medium — changes when work happens |
| **F6** | Make unprepared prefix/exact cheap (binary search over a sorted headword slice instead of scan+fold per query) | search CPU during the unprepared window | **declined by D15** — preview may be slow; not worth the collation risk |

**The single highest-value change is F1**, and it is nearly free: one semaphore.
It converts a 102-way parallel storm into a 1–2-way stream, which is what the
`Warm` path already does for opening.

## 6b. Applied 2026-07-28 — F1–F4 (measured after)

Same scenario, same four dictionaries, same binary except the fixes:

| | before | after | |
|---|---:|---:|---|
| peak CPU (first-search storm) | **424 %** | **154 %** | F1: bounded to INDEX_WORKERS (+1 foreground slot) |
| RSS left resident after the storm | **474 MB** | **172 MB** | F2: superseded backends closed + `FreeOSMemory` |
| RSS after Warm, before any search | 17 MB | **13 MB** | F2: Warm no longer opens *unprepared* dictionaries |
| peak RSS during the storm | 500 MB | 477 MB | see the residual below |
| prepared steady state | 17 MB idle / 33 MB peak | unchanged | already optimal |

Concurrency is enforced and tested (`TestIndexingConcurrencyIsBounded`:
INDEX_WORKERS=1 → 1 concurrent ingest, =3 → 3).

**Modelled effect on the full corpus** (18.1 M entries): the indexing storm
falls from ~102 concurrent ingests to 1–2, i.e. **~18 GB → ~0.3–0.5 GB** for the
indexing component, and CPU from 1000 %+ to ~100–150 %.

### The residual, now dominant: search materialises unprepared backends

Peak barely moved in the 4-dictionary test because with only four dictionaries
the storm was never wide. What remains is **M2 in a new place**: an *All
dictionaries* search opens every unprepared dictionary's headword map (8 workers
at a time) and holds it until that dictionary is prepared and swapped out.

For the full corpus that is a transient of ~18.1 M × ~350 B ≈ **6 GB** during
the first minutes, decaying to a few hundred MB as preparation completes. The
indexing storm is fixed; this is the next-largest term and it is *not* fixed.

**Resolved 2026-07-28 by S2** (see §6c). Two candidates were considered:

* **S1 — skip unprepared dictionaries in search while auto-indexing is on.**
  The UI already renders a "preparing…" state for a skipped dictionary, and D13
  promises results "on the next query". No direct backend is ever materialised
  by search; only the indexer touches them, one at a time. Transient becomes
  ~200 MB. Cost: a dictionary is missing from results for a minute or two after
  first launch, which is visible.
* **S2 — bound and evict open direct backends (LRU by count or by entries).**
  Keeps results complete but adds a policy, and evicted backends must be
  reopened at real cost (measured 0.2–1.1 s each).

## 6c. Applied 2026-07-28 — S2, bounded preview memory with LRU eviction

Chosen over S1 (skip-while-preparing) because it keeps search results complete.

* **Budget in memory, not in count.** Dictionaries differ by two orders of
  magnitude in headwords, so "keep N open" would mean 50 MB or 3 GB depending
  which N. `PREVIEW_MEMORY` (default **1 GB**) caps what *unprepared*
  dictionaries may hold; each is weighed at `EntryCount × 350 B`, the middle of
  the measured 290–570 B range (§3.1).
* **Prepared dictionaries weigh zero and are never evicted** — they answer from
  SQLite, so there is nothing to reclaim and reopening would only cost. The
  predicate is "answers from a prepared database" (`SourcePath()`), which also
  covers DSL and BGL: those embed a `*store.Store` without a wrapper, and a
  type-switch on the wrappers alone had counted them as if they held a RAM
  index.
* **Eviction never runs on the request path.** A janitor sweeps every 20 s and
  will not touch a backend used in the last 45 s. Without that rule, a search
  fanning out over a hundred dictionaries would evict the very backends it is
  about to reuse, and each reopen costs 0.2–1.1 s — turning a memory fix into a
  latency bug.
* **Evicted means reopenable.** `entry.open` moved from `sync.Once` to
  double-checked locking under a dedicated mutex, so a backend can be dropped
  and rebuilt on next use; the close itself waits `closeGrace` (10 s) so
  in-flight reads finish, then `FreeOSMemory` returns the pages.

Verified live (4 real MDX, `PREVIEW_MEMORY=150MB`):

```
evicted preview backend ro-ro-DEX.mdx (~209 MB)
preview budget: reclaimed 209 MB (was 259 MB, budget 150 MB)
```

and by test (`TestPreviewEviction`): in-use backends survive any budget, idle
ones over budget are evicted, an evicted dictionary reopens and still searches,
and a prepared dictionary weighs 0 and is never a candidate.

**Modelled on the reference corpus:** the ~6 GB transient of §6b is now capped
at `PREVIEW_MEMORY` + whatever is in active use — **~1–1.5 GB**, decaying to a
few hundred MB as preparation completes. Combined with F1–F4, the first-run
peak goes from a measured 18 GB to a bounded ~1.5 GB, and idle from 2.5 GB to
~0.3–0.5 GB.

### Resource handles — also budgeted (2026-07-28)

`upgraded.src` — the direct backend a *prepared* dictionary opens when a
resource misses its `media.db` — holds the same ~350 B per headword and was
never released. It is now a reclaimable of its own: weighed on open, timestamped
on every resource fetch, and released by the same janitor under the same idle
rule. Releasing it costs nothing visible — text still comes from SQLite, and the
next resource request reopens it.

The sweep works over a single list of `reclaimable{used, bytes, free}` so both
kinds compete on one LRU: an unprepared dictionary's whole backend, and a
prepared one's resource handle. They differ only in what stops working
meanwhile (everything, versus unpacked media until the next fetch reopens it).

Verified live: `released resource handle for es-en-wordref-audio.mdx (~9 MB)`,
and by `TestResourceHandleIsEvictable` — nothing held before the first resource
request, weighed after it, never released while in use whatever the budget,
released when idle and over budget, text lookups unaffected, handle reopened by
the next resource request.

**One bug this found:** `reopen()` swapped in the prepared view but left the
*replaced* backend's weight recorded on the entry, so a freshly prepared
dictionary looked RAM-heavy and could have been evicted — closing a Store that
costs nothing to keep. Weight now follows the view at every assignment.

## 7. Open questions for the decision rounds

0. ~~**S1 vs S2**~~ — decided: **S2**, shipped (§6c), including the `upgraded.src` handle.
1. ~~**Concurrency for background indexing: 1, 2, or `NumCPU/4`?**~~ — decided: **1** by default, `INDEX_WORKERS` to raise it, `auto` = every core. Wall-clock for
   the full corpus at 1× is roughly the sum of per-dictionary ingest times;
   measured, that is ~2 s per 100 k entries, so ~6 minutes for 18.1 M entries —
   background, but not instant.
2. **Should preparation be automatic at all for a 100-dictionary library?**
   D13 says yes (silently). At this scale, an explicit "prepare all (~6 min)"
   with progress may be more honest than an invisible six-minute storm.
3. **Eviction policy for direct backends**: never-evict (today), LRU by count,
   or "close after preparing". The last is nearly free given D12/D20.
4. **Do we want a memory ceiling** (`SetMemoryLimit`) as a safety net, and at
   what value — fixed, or a fraction of system RAM?

## Appendix — reproducing

```sh
# unit cost of one ingest
scratchpad/perf/peak.sh gonow-dict ingest -headwords <file>
# server scenarios
python3 scratchpad/perf/bench.py unprepared    # A
python3 scratchpad/perf/bench.py prepared      # B
# corpus inventory
find ~/Downloads/Language -type f \( -name '*.mdx' -o -name '*.slob' … \)
```
