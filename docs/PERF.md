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
4. ~~**Do we want a memory ceiling** (`SetMemoryLimit`) as a safety net, and at
   what value?~~ — decided (D64, §8): **yes on Android, no on the desktop, and
   the value must be able to yield.** A fraction of system RAM (RAM/16, floored
   192 MB, capped 384 MB) where the alternative is being killed; unset where the
   OS is better at this than a guess. Measured: no fixed value is safe on its
   own.

## 8. Android — measured 2026-08-14 (D64)

Everything above this section was measured for a desktop, where using the
machine is what a program is for. A phone judges the same behaviour: sustained
CPU is a battery complaint and a thermal event, resident memory above what the
platform thinks reasonable is a kill by the low-memory daemon, and a periodic
timer in a process that outlives its window is a core brought out of idle
forever to find nothing to do. The mechanisms are in D64; what follows is what
was measured, including the part that came out the wrong way.

### 8.1 The memory-ceiling sweep

`internal/server/memlimit_test.go`, env-gated and never part of `make check`.
One **fresh child process per limit** — `debug.SetMemoryLimit` is process-global
and a heap carries its history, so several limits in one process would measure
the order they were tried in. Corpus: the real 130 dictionaries, empty db dir so
every one stays in preview mode (the worst case, and the state a fresh install
is in), `PREVIEW_MEMORY` 64 MB, 60 `dict=all` prefix queries in 30 words across
five scripts, 18 cores, Go 1.26.5 darwin/arm64.

| limit | wall | GC share of CPU | GC cycles | live at end | in-use at end | preview held (open) | peak RSS |
|---|---|---|---|---|---|---|---|
| none | 140 s | 16.6 % | 26 | 5697 MB | 11823 MB | 6333 MB (106) | 7180 MB |
| 96 MB | 1036 s | 44.0 % | 6290 | 1650 MB | 2376 MB | 0 MB (0) | 6253 MB |
| 128 MB | 1069 s | 43.7 % | 6809 | 1083 MB | 1770 MB | 395 MB (1) | 5730 MB |
| 192 MB | 1052 s | 44.0 % | 6502 | 2288 MB | 2985 MB | 395 MB (1) | 5725 MB |
| 256 MB | 1144 s | 46.0 % | 6556 | 637 MB | 985 MB | 0 MB (0) | 5359 MB |
| 384 MB | 1239 s | 44.9 % | 7239 | 662 MB | 1097 MB | 37 MB (1) | 5263 MB |
| 512 MB | 1210 s | 44.9 % | 6836 | 2950 MB | 3830 MB | 0 MB (0) | 5286 MB |

Read the second and third columns together: **no knee, no trend.** Every ceiling
produces the same 44–46 % GC share and the same 7.4–8.8× wall time, because what
is holding the GC share down is Go's own 50 % limiter, not the ceiling. A soft
limit's only lever is to collect harder; below the working set that means
collecting continuously and freeing nothing.

The shedding is not at fault and is visible in the same table: 106 open backends
go to 0–1 and peak RSS falls 7.2 GB → 5.3 GB. But at 512 MB the runtime still
could not get in-use below 3.8 GB, because the remainder is the fan-out's own
working set rather than cache. Hence the conclusion in D64: the ceiling stays,
and `adjustLimit` lets it yield after three consecutive pressured janitor passes
rather than pinning a phone's CPU at 45 % indefinitely.

Two caveats on reading these numbers as phone numbers. This is desktop hardware
and a corpus no phone holds; and the ceilings swept are the *Android* defaults,
applied to a library 130 dictionaries deep. What transfers is the **shape** of
the failure and one fact that does transfer directly: a single unprepared
dictionary here holds a 395 MB headword index, which is larger than the entire
ceiling a phone is given — so "the limit is below one dictionary" is reachable
on a phone with one big file, not only on this desk.

### 8.2 The preview budget does not bind during a fan-out

The same table's first row is the finding: `PREVIEW_MEMORY` was **64 MB** and
6333 MB was held across 106 open backends. `minEvictIdle` (45 s) protects every
backend used recently, and a `dict=all` search touches all of them, so the
budget is only ever enforced *between* bursts of activity. That is what §6c
intended — a reopen costs 0.2–1.1 s and evicting the backends a fan-out is about
to reuse would be self-defeating — but at 130 dictionaries it means the
configured budget is not a bound at all. Only the pressure path (target 0, grace
waived) currently bounds it, and that path exists solely because a limit is set,
which on the desktop it is not.

The honest options were a reference count (evict during use, safely) or a
per-fan-out cap on how much one search may materialise — `search.Workers()`
already bounds concurrent *use* to 8, so the other 98 are cache that the budget
is failing to reclaim.

**Fixed by the cap (D65), and only where the trade is worth taking.**
`SEARCH_MEMORY` gives each query its own budget: an opener is charged what the
last open of that dictionary weighed, and once the budget is gone the remaining
*unprepared* dictionaries are declined with an error the page shows in their
slot rather than opened. It is a second bound, not a replacement — the preview
budget still governs what may stay resident *between* searches, and this one
governs what a single search may create. Prepared dictionaries are never
declined (they weigh nothing, so refusing them costs results and saves
nothing), and the first search of a cold dictionary is never declined either,
because its price is only learned by paying it once. The default is
**0 (uncapped) on the desktop** — where declining results to protect a machine
that is not under threat is a bad trade — and the memory ceiling's value on
**Android**, where the alternative is the 1.71 GB of §8.6.

### 8.3 On device: what shedding actually returns (emulator, API 36 arm64)

One 241 MB MDX (`ODE-Living-Online`, 464,360 headwords) in preview mode
(`AUTO_INDEX = "off"`), reached over `adb forward`, measured at
`/proc/<pid>/status` — **not** `ps -o RSS`, whose Android toybox column is in
4 KB pages and reads a factor of four low; an early round of this measurement
was read as 29 MB when the process held 116 MB.

| stage | VmRSS |
| --- | --- |
| server started, nothing opened | 16.7 MB |
| after one `dict=all` prefix search | 119.5 MB |
| 18 s after HOME (evict + `closeGrace` + reclaim) | **19.9 MB** |

The weight model charges `350 B × EntryCount` = 154 MB for this dictionary.
Measured on the desktop against the same file, the Go heap holds **83 B/entry**
after `Open` (headword entries decoded, no index) and **201 B/entry** once a
search has built the folded index — 93.9 MB, against a whole-process 119.5 MB on
the phone. So the model over-charges a warm MDX by ~1.7×, and the *process*
by ~1.3×. That is the correct direction to be wrong in on Android and the
constant stays, but it is an estimate applied without measuring, not a
measurement.

**The finding that changed code: `GOOS=android` does not get MADV_DONTNEED.**
`runtime1.go:parseRuntimeDebugVars` sets `debug.madvdontneed = 1` only when
`GOOS == "linux"`; on `android` the runtime keeps `MADV_FREE`, so pages the
scavenger has returned stay counted in RSS until the kernel is short of memory.
Before the fix the table's last row read **184 MB** — unchanged across three
search/background cycles, and unchanged by `debug.FreeOSMemory`. Eviction was
not the suspect: driving the same entry through `open → Prefix → evict → wait`
on the desktop takes the heap 93.9 MB → 0.7 MB, so nothing is retained. The
shell now passes `GODEBUG=madvdontneed=1` to the exec'd child
(`ServerProcess.java`), which is what makes the 19.9 MB row above real. On
Android the number *is* the outcome: the low-memory killer, the vendor RAM
watchdogs and the battery screen all read RSS/PSS, so memory released but still
charged is memory not released.

### 8.4 On real hardware — OnePlus PKG110, Android 16 (SDK 36), 8 cores, 11.5 GB

The emulator can show plumbing; it cannot show a vendor's thermal governor or
what a real library costs. This device carried **61 dictionaries**, all in
preview mode (`AUTO_INDEX = "off"` — the worst case a fresh install passes
through, not the steady state), and derived: `GOMAXPROCS=4, search fan-out=4`
(half of 8), memory limit **384 MB** (11.5 GB/16 = 718 MB, capped), preview
budget 64 MB.

Eight `dict=all` prefix queries, back to back:

| | |
| --- | --- |
| wall | 62 s |
| CPU | 130 s (4 threads, so ~2.1 cores held) |
| VmRSS, idle → peak | 288 MB → **4.9 GB** |
| ceiling | raised 384 MB → 3489 → 3740 → 4602 → 5310 → 6312 MB |
| thermal | `active → restricted` **25 s in**, from the platform's own thermal status; battery 40.1 °C |
| lmkd | active throughout (`free 49–57 MB`); the app survived, on a phone with 11.5 GB |

Three things this says that the desktop sweep could not. The relax valve is not
theoretical — it fired five times in one minute on ordinary use of a large
unprepared library, and it is what kept the process out of the 45 %-GC state
§8.1 measured. The thermal path fires on our *own* workload, not on some
external heat source, which is the strongest argument in this document for the
`MaxProcs` halving. And **§8.2's fan-out defect is an Android problem, not only
a desktop one**: 4.9 GB resident on a phone is a kill on any device with 4–6 GB,
and the only reason this one survived is that it has 11.5 GB.

### 8.5 The ceiling that was raised and never handed back

The same run exposed a defect in the relax mechanism itself. `restoreMemoryLimit`
only runs inside a janitor pass, and the janitor blocks when `needsSweep()` is
false — which is exactly the state that follows a relax: the fan-out ends, the
backends are shed, the heap drains, nothing is reclaimable, and no pressure is
reported *because the ceiling is now 6 GB*. Measured: the process sat at the
raised ceiling with the configured 384 MB never restored, for as long as it was
watched. One heavy search would have bought a 6 GB ceiling for the life of the
process — the opposite of what D64 promises.

`needsSweep()` now also returns true while `limitRelaxed()`, and
`restoreMemoryLimit` arms a `scheduleReclaim`. Re-measured on the same device,
same burst, after backgrounding:

| t after HOME | VmRSS | CPU |
| --- | --- | --- |
| +30 s | 4032 MB | 13037 ticks |
| +60 s | **173 MB** | 13143 |
| +90 s … +180 s | 170 MB, flat | 13148 → 13149 (10 ms in three minutes) |

with `memory limit restored to 384 MB` in the log one janitor period after the
last reclaim. Backgrounded, the process is 170 MB resident and costs nothing.

### 8.6 The low-RAM device — Xiaomi MI PAD 4, Android 11 (SDK 30), 6 cores, 3.8 GB

The OnePlus survived §8.4 because it had 11.5 GB. This tablet is the device the
same library is actually dangerous on: **24 dictionaries**, all preview, derived
`GOMAXPROCS=3, search fan-out=3`, memory limit **233 MB**, preview budget 64 MB.
One of the 24 is a Babylon-derived MDX with **2 881 321 headwords** — a single
dictionary the weight model charges at ~961 MB.

Four `dict=all` prefix queries:

| | |
| --- | --- |
| wall / CPU | 177 s / 355 s |
| VmRSS, idle → peak | 7.7 MB → **1.71 GB** on a 3.8 GB device |
| MemAvailable | 2.2 GB → **0.57 GB** |
| latency, per query | 91 s, 36 s, 29 s, 21 s |
| evictions / ceiling raises | 63 / ten (233 MB → 1586 MB) |
| idle recovery | 110 MB, `memory limit restored to 233 MB`, CPU flat |

The recovery is the fix of §8.5 working on a second device and a different
Android generation. The peak is §8.2 as it stood at the time of the run,
before the cap: **45 % of the machine's memory, for one keystroke's worth of
query.** Re-measured on the same tablet under D65 with an uncapped control:
61 s instead of 212 s, 123 s of CPU instead of 438 s, 1.02 GB instead of
1.30 GB — §8.8. Nothing here is a leak — the same run
returned to 54 MB unaided — but a phone does not have to kill a leaking process
to kill this one.

Recovery is also slow, and the slowness is the interesting part: after a burst
the RSS curve stays flat for ~120 s and only then falls (1.28 GB → 610 MB →
54 MB over the next 150 s). `minEvictIdle` (45 s) plus a 20 s janitor period
plus `MADV_DONTNEED` only at reclaim time is a ~4-minute tail. Acceptable when
the user has put the app down; not acceptable as the state a task-switch back
into the app finds.

### 8.7 A search nobody is waiting for still costs everything

The web UI aborts the in-flight fetch on every keystroke (`searchAC` in
`index.html`). Measured on the tablet, three fetches aborted at 4 s: the server
kept working for **90 s each** and drove RSS to **1.0 GB**, because
`StreamOpen` consulted the context only while a worker waited for a semaphore
slot, never after it was admitted. Half the queue could therefore sail past a
context that was already dead — `select` picks randomly when both cases are
ready — and every one of those workers materialised a dictionary's whole
in-memory index for output that had nowhere to go.

`StreamOpen` now re-checks `ctx.Err()` after acquiring the slot and again after
the open. Re-measured, cold-cache aborts at 1 s: **14 of 24** dictionaries
opened, the rest skipped.

What the fix cannot do is the residual, and it is the larger half: **an open
already in flight is uninterruptible.** The 2.88M-headword MDX takes 6–11 s to
open, so any aborted `dict=all` query that admitted it pays for it in full —
the request above still ran 9.9 s after its client had gone. Cancellation
bounds a cancelled fan-out to the opens in flight; the rest is bounded by the
per-search materialisation cap, §8.2 / D65.

### 8.8 The cap, measured — same tablet, same corpus, A/B in one session

D65 re-measured on the MI PAD 4 (24 unprepared dictionaries under
`/sdcard/mdict`, `AUTO_INDEX=off`, `GODEBUG=madvdontneed=1`, derived defaults
`GOMAXPROCS=3` / limit 233 MB / budget **233 MB per query**). The control is the
**same binary** with `SEARCH_MEMORY=0`, run minutes apart on the same files, so
the only variable is the cap. Four cold `dict=all` prefix queries
(`stone water light cat`):

| | uncapped (control) | capped (Android default) |
| --- | --- | --- |
| wall / CPU | 212 s / 438 s | **61 s / 123 s** |
| VmRSS peak | 1.30 GB | **1.02 GB** |
| MemAvailable, low | 1.17 GB | 1.26 GB |
| per-query latency | 99 / 49 / 39 / 25 s | **56 / 1 / 1 / 1 s** |
| dictionaries declined | 0 / 0 / 0 / 0 | 4 / 2 / 3 / 4 of 24 |

**CPU is the headline, not RAM: −72 %.** On a phone that is the number that maps
to battery and heat. The peak fell only 22 %, and the reason is structural and
worth stating: the budget is per *query*, and a dictionary already resident is
free, so four queries inside the 45 s idle grace may each spend a fresh 233 MB
on top of what the last one left. The cap slows accumulation; the janitor, not
the cap, is what bounds the residue. Idle recovery was unaffected and complete —
**34.8 MB** and `memory limit restored to 233 MB` 20 s after the last query,
1 tick (10 ms) of CPU over the following 200 s.

What the cap does absolutely, which the table understates: the corpus's largest
dictionary is a **961 MB** headword index, and after the one open that priced it
it was declined by every later query — `not searched: ~961 MB unprepared`. The
control opened it three times in four queries. One dictionary larger than the
whole machine's comfortable working set is the failure mode §8.2 was about, and
it is now unreachable after the first sight of it.

**The first query cannot be capped, and it costs.** A fan-out is parallel, so
every opener reads "unknown" before any has settled a charge: 56 s and 393 MB
of the capped run is that one query paying to learn 24 prices. Persisting
`lastWeight` across restarts (it is already remembered across eviction) would
make the cap bind from the first query of every run but the first-ever — the
obvious follow-up, not taken here.

Unrelated finding, pre-existing and not from this work: three dictionaries in
this corpus failed to open on-device with deterministic parser panics (`index
out of range [143] with length 143`, `slice bounds out of range [32734:32724]`
×2), recovered and reported per dictionary as intended. Deterministic offsets
meant a parse-path defect rather than memory pressure, and it was one: the panic
came from the companion `.mdd`, not the named `.mdx`, and `splitKeyBlock` was
splitting v3 MDD keys at a UTF-16 stride. Fixed in P73; not a memory finding.

### 8.9 Reproducing

```sh
WUDICT_PERF_CORPUS=~/Downloads/Language WUDICT_PERF_ROUNDS=2 \
  go test ./internal/server -run TestMemoryLimitSweep -v -timeout 240m
```

Two hours for seven children. Rows are printed to stderr as each child finishes,
so an interrupted run keeps what it measured — `testing`'s own log is not
flushed until the test ends, which on the first attempt threw away 40 minutes.

## Appendix — reproducing

```sh
# unit cost of one ingest
scratchpad/perf/peak.sh wudict ingest -headwords <file>
# server scenarios
python3 scratchpad/perf/bench.py unprepared    # A
python3 scratchpad/perf/bench.py prepared      # B
# corpus inventory
find ~/Downloads/Language -type f \( -name '*.mdx' -o -name '*.slob' … \)
```
