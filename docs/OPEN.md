# Open questions — researched, specified, **not scheduled**

Things that have been thought through far enough to cost, and then deliberately parked.
Nothing here is committed to. An item leaves this file by becoming a D-number in
`docs.local/DECISIONS.md`, or by being deleted with a reason.

Status vocabulary: **ON HOLD** (wanted, unscheduled) · **MAYBE** (unconvinced) · **DO ANYWAY**
(stands on its own merits, unrelated to the item it was found under).

---

## O1 — Unicode folding is unversioned — **DONE (D34 / P37)**

*Kept for the reasoning; the implementation notes and the correction are in D34. The blast
radius turned out to be one index, not three — see below.*

`dict.Fold` (`internal/dict/fold.go:16`) is called in two places with **different lifetimes**:

- at **query time**, on what the user typed;
- at **ingest time**, on every headword — and the result is *persisted* into `entry_fts` and
  `entry_trigram` (`internal/store/ingest.go:112,119`).

Nothing records which version of `Fold` produced a stored index. **Any future change to `Fold`
therefore silently desynchronises every existing library folder**: add `ß`→`ss` and a query folds
to `strasse` while an index built last month still holds `straße`. No error, no warning, and
invisible to tests that ingest fresh data every run. The failure mode is a search that quietly
stops finding things it used to find.

This is a live defect today, independent of O2 and O3. It is also the precondition that makes
either of them safe to attempt.

**Correction found while implementing.** The paragraph above overstated the blast radius, and
the audit that produced the real number is the useful part. `Fold`'s output is persisted in
**exactly one place** — `ingest.go:198`, the trigram index behind *contains*. Everything else
that looked folded is not: `entry_fts` stores raw `w`/`txt` and folds with SQLite's own
`unicode61 remove_diacritics 2` tokenizer, `idx_entry_w` is a raw `COLLATE NOCASE` index, and the
direct backends rebuild their fold indexes on every open. So the base headword index needs no
rebuild, full-text needs no rebuild, and a `Fold` change makes exactly one thing stale, only for
dictionaries that opted into *contains*.

**Fix as shipped:** `fold_version` in `meta`, absent ⇒ 1 (grandfathering every existing library),
surfaced as `Store.ContainsStale()` / `containsStale` in `/api/dicts`, and repaired by
re-requesting the feature. Contains is **not** disabled when stale — see D34.

**Cost:** ~half a day, tests included.

---

## O2 — Normalization hardening — **ON HOLD**

### What `Fold` does now

```go
s = strings.ToLower(s)                      // 1. simple lowercase
for _, ru := range norm.NFD.String(s) {     // 2. canonical decomposition
    if unicode.Is(unicode.Mn, ru) { continue }   // 3. drop nonspacing marks
    b.WriteRune(ru)
}
```

### What that already buys (do not "fix" these)

Step 2 does more than its comment claims. `Fold` was written for accent-insensitivity, but
accent-insensitivity **subsumes NFC/NFD-insensitivity** for anything expressible as base +
combining mark — so a dictionary authored in NFC and a query arriving in NFD converge, and the
following all work correctly today:

| Case | Why it already works |
|---|---|
| `café` NFC vs NFD | both → `cafe` |
| Russian `ё`, stress `кни́га` | U+0308 / U+0301 are `Mn` → stripped |
| Korean `한` precomposed vs conjoining jamo | NFD decomposes precomposed syllables **to** jamo, so both sides land on the same three codepoints |
| CJK compatibility ideographs (者 U+FA5B vs U+8005) | most carry *canonical* singleton decompositions, which NFD applies |
| Vietnamese stacked marks | all `Mn` |

The pattern: **NFD covers canonical equivalence.** The gaps are where equivalence is
*compatibility*-only, or is not Unicode's business at all.

### The five real gaps

| # | Class | Concrete miss | Why NFD does not help |
|---|---|---|---|
| 1 | **Full case folding** | `Straße` vs `STRASSE`; Greek final `ς` vs `σ` | `strings.ToLower` is *simple* lowercase; Unicode full casefolding maps ß→ss and ς→σ, `ToLower` maps neither. Fix: `x/text/cases.Fold` |
| 2 | **Compatibility width** | fullwidth `ａ` U+FF41 vs `a`; halfwidth katakana `ｱ` U+FF71 vs `ア` | compatibility (NFKD) decompositions; NFD leaves them alone. Pervasive in CJK dictionary data. Fix: `x/text/width` or targeted NFKC |
| 3 | **Invisible characters** | ZWJ/ZWNJ inside Devanagari and Persian words; soft hyphen, ZWSP, BOM in scraped data | category `Cf`, not `Mn` → kept. Two spellings differ by a character the user cannot see |
| 4 | **Punctuation variants** | `don’t` U+2019 vs `don't` U+0027; `‐` U+2010 vs `-` U+002D | not a Unicode equivalence at all. Probably the **most frequent real-world miss** in English dictionaries |
| 5 | **Over-aggressive `Mn` stripping** | `だ` → NFD → `た` + U+3099 (`Mn`) → **`た`** | inverse of the others: a *wrong merge*, not a missed match. Dakuten/handakuten are phonemic — `だ` and `た` are different words |

Related to (5): Devanagari nukta U+093C is `Mn`, so `क़` currently folds to `क`, merging /q/ into
/k/. For Hindi search that is probably *desirable* — but it is an accident, not a decision, and
should be confirmed either way.

Turkish dotless ı is a sixth, minor gap (`ToLower("I")` → `i`, Turkish wants `ı`); the dotted `İ`
case already works by accident. Not worth doing unless a Turkish dictionary shows up.

### Performance

`Fold` is a hot path — every headword at ingest across the whole corpus, every query at runtime.
`cases.Fold` is slower than `strings.ToLower`; NFKC is slower than NFD. But most headwords in a
Latin-script dictionary are pure ASCII, and an ASCII fast path skips NFD **and** the per-rune
`unicode.Is(unicode.Mn, …)` range-table search that runs unconditionally today:

```go
if isASCII(s) { return strings.ToLower(s) }
```

The hardened version is plausibly *faster* than the current one. Benchmark before and after
(`docs.local/PERF.md` discipline).

**Cost:** 1–2 days for the folding changes (~50 lines plus a punctuation table and a
script-aware `Mn` exception list), on top of O1.

---

## O3 — Morphology (inflected-form lookup) — **ON HOLD**

### Three unrelated problems wearing one name

| | Problem | Shape of the fix |
|---|---|---|
| 1 | **Inflection** — `running`→`run`, `tuviéramos`→`tener`, `книгами`→`книга` | affix rules or a lemma table |
| 2 | **Orthographic variance** — 简体/繁體, okurigana | a *table lookup*, no linguistics |
| 3 | **Segmentation** — no spaces (zh/ja/th), compounds (de/nl/fi) | statistical model or recursive split |

D16's search modes assume a token boundary exists, so (3) is out of scope except for CJK/Thai.
Conflating these three is what makes such projects overrun.

### The finding that kills the "drop Hunspell files in a folder" premise

The Hunspell man page:

> Most widely distributed dictionaries (including the en_US, fr, nl and hu_HU dictionaries shipped
> by Linux distributions and LibreOffice) do not include this metadata, and **analyze(), stem() and
> generate() will return an empty list** for them. This is a property of the dictionary, not a bug
> in the library.

GoldenDict-ng's own documentation corroborates independently, and `goldendict/hunspell.cc` calls
`analyze()`, regex-scrapes `st:` from the output, and wraps **every** Hunspell call in a global
mutex because the library is not reentrant.

So adopting a Hunspell binding buys empty results for most languages plus a per-language hunt for
the few morphology-tailored dictionaries that exist.

### The design that keeps the drop-in story anyway

> Use the `.aff` file purely as a **prefix/suffix rule table**, reverse-apply its `SFX`/`PFX`
> rules to the **query**, and validate every candidate against the headword index we already have.

`analyze()` is never called, so the missing `st:`/`AM` metadata stops mattering. And because
`entry.w` / `alias.w` (with `idx_entry_w COLLATE NOCASE`) is a hard oracle, **over-generation is
free** — a candidate that is not a headword is discarded at zero cost. That turns a precision
problem into a recall problem, which is the benign direction, and it means ~80% of the Hunspell
spec (compounds, `ONLYINCOMPOUND`, `CIRCUMFIX`, `NEEDAFFIX`, flag algebra) can be ignored: we are
not deciding whether a word is spelled correctly, we are proposing lookups.

**Query-time, never index-time:**

```
query → Morph.Candidates(word, lang) → []string (capped)
      → existing Exact() per candidate (idx_entry_w B-tree hit, µs)
      → results tagged rank=derived, sorted strictly below rank=exact
      → UI: "no results for tuviéramos — showing tener"
```

Additive and reversible. The index-time alternative freezes a *guessed* language into every
`text.db` and needs a full re-ingest to correct a mistake.

Two existing assets make this cheaper than it looks: the `alias` table (`Exact` already unions
alias→entry, so derived forms have a home if they ever need persisting), and the fact that the
oracle can be the **union of headwords across the user's whole collection** — one small English
inflections dictionary then improves lookups in every other English dictionary they own.

**Corollary — a stemmer is the wrong tool.** Snowball is reductive and lossy (`happiness`→`happi`);
a stem is not a headword, so it cannot be validated against the index. Using one requires storing
`stem(headword)` as a column and joining stem-to-stem — schema change plus re-ingest of all 105
dictionaries. Rule-based de-affixation with headword validation needs neither. That asymmetry
should decide the architecture.

### Per-script reality

| Script / language | Assessment |
|---|---|
| **Latin (es, en, fr, it, pt, de, nl)** | Concatenative, well served by rule tables. **Spanish is the biggest single win available** — ~50 verb forms each, and `tuviéramos` finds nothing today. German additionally needs compound splitting (recursive split against the headword set with linking `-s-`/`-n-`/`-en-`): a different algorithm. Turkish is agglutinative; a fixed rule list will not work |
| **Cyrillic (ru, uk, bg)** | Highest payoff per language — 6 cases × 2 numbers for nouns, ~24 adjective forms, 60+ verb forms. Russian `.aff`/`.dic` are genuinely stem+flag structured, which is exactly what reverse-application needs |
| **Hanzi (zh)** | **No inflection. Applying a morphology framework here is a category error.** The wins are simplified↔traditional mapping (pure table, OpenCC data) and segmentation |
| **Japanese** | Real heavy inflection (`食べる` → `食べさせられなかった`) plus no spaces plus four scripts. Kagome does it properly but its dictionaries collide with the single-binary story (D28). Correct approach here is the Yomichan/10ten one: a **deconjugation rule table**, few hundred rules, no statistical model, validated against headwords |
| **Korean** | Verb deconjugation comparable to Japanese; jamo composition already handled (O2) |
| **Hindi / Devanagari** | Moderate inflection; the orthography (O2 gaps 3 and 5) is the bigger job. Hunspell `hi_IN` is weak |
| **Arabic / Hebrew** | Hardest by a wide margin: non-concatenative root-and-pattern, clitic stacking (`و+ال+كتاب+ها`), optional diacritics. Realistic scope is strip-diacritics + strip-a-few-clitics + validate. Beyond that is a research project |
| **Thai / Khmer / Lao** | No inflection; a segmentation problem, not a morphology one |

### Go library landscape

| Library | What it gives | Pure Go | Verdict |
|---|---|---|---|
| `client9/gospell` | `.aff`/`.dic` parser, affix expansion, compound rules, iconv. **Spell-check only — no analyze/stem** | ✅ | Best *parser* to borrow from; wrong API |
| `shuLhan/share/lib/hunspell` | `Spell`/`Stem`/`Analyze`; many encodings; ASCII/UTF-8/long/num flags | ✅ | Closest to Hunspell semantics, but v0.x, a sub-package of a large grab-bag module, and inherits the empty-`analyze()` problem |
| `sthorne/go-hunspell` et al. | cgo bindings to libhunspell | ❌ | **Disqualified by D4/D18** — `-tags purego` must build |
| `kljensen/snowball` · `blevesearch/snowballstem` | Snowball stemmers (7 / ~20 languages) | ✅ | See "a stemmer is the wrong tool" above |
| `aaaton/golem` | Dictionary lemmatizer (en, fr, de, es, it, sv, ru); data as separate modules, ODbL | ✅ | Handles irregulars, which rules cannot. Several MB per language |
| `smileart/lemmingo` | Lemmatizer + stemmer fallback | ❌ | cgo (Aspell/Snowball), documented thread-safety caveats |
| `ikawaha/kagome/v2` | Japanese analyzer, embedded IPADIC/UniDic | ✅ | Excellent; dictionary size vs. single binary |
| `go-ego/gse` | zh/ja segmentation | ✅ | Same size objection |
| `liuzl/gocc` · `longbridge/opencc` · `siongui/gojianfan` | OpenCC 簡繁 conversion, `go:embed` data, Apache-2.0 | ✅ | The one to adopt if zh is ever tackled |

### Phasing and cost

| Phase | Scope | Estimate |
|---|---|---|
| **M1** | Framework: `Morph` interface, per-dictionary language resolution, candidate cap, search-path integration, ranking, "showing results for" UI | 3–4 d |
| **M2** | Curated suffix tables: es, ru, en, de, fr, it, pt | 3–5 d + ongoing tuning |
| **M3** | `.aff` reverse-application loader (the drop-in-a-folder story) | 5–8 d + long tail |
| **M4** | Japanese deconjugation table | 3–4 d (kagome instead: 1 d code, +30–60 MB binary) |
| **M5** | 簡繁 variant mapping via OpenCC tables | 1–2 d |

Defensible v1 = O1 + O2 + M1 + M2 + M5 ≈ **10–14 days**. Everything including `.aff` and Japanese
≈ **20–28 days plus indefinite linguistic tuning** — that tail never closes; it is a permanent
maintenance surface, not a project with an end. **This is the main reason the item is on hold.**

### Defect surface, most dangerous first

1. **Language attribution is the actual hard problem, not morphology.** Of the five formats only
   DSL declares languages (`#INDEX_LANGUAGE`); MDX declares essentially nothing. Script detection
   is trivial, language-within-script is not (Cyrillic → ru/uk/bg/sr; Latin → dozens). The
   headword oracle mitigates but does not eliminate cross-language collisions (`come`, `pie`,
   `son`, `chat`). **This forces a per-dictionary language override UI, which is more work than
   the feature itself** — and new persistent per-dictionary state.
2. **Preview vs prepared asymmetry (D15).** Prepared dictionaries validate candidates with a
   B-tree hit; preview backends scan in memory, so 10 candidates = 10 linear scans. Morphology
   would silently become a prepared-only feature — another axis of divergence between the two
   backends.
3. **Latency at real scale.** 105 dictionaries × ~10 candidates ≈ 1,050 extra index probes per
   query. Fine as B-tree hits, not fine unbounded. Needs a hard cap and a per-query candidate cache.
4. **Ranking regression.** Derived hits must never outrank exact ones — requires a rank field
   threaded through the whole NDJSON search path and the client's merge.
5. **Full-text is a separate, much larger decision.** Morphological FTS means index-time stemming
   → schema change → re-ingest → expanded FTS5 `OR` queries that can blow up. It must not ride
   along with headword morphology.
6. **Termination.** Iterative suffix stripping needs a depth cap and a visited set, or `.aff` rule
   chains loop.
7. **Licensing.** Hunspell dictionaries are mostly GPL/LGPL/MPL tri-licensed, some GPL-only;
   golem's data is ODbL; OpenCC is Apache-2.0. GPL-3.0-or-later makes *shipping* fine, but
   *embedding* third-party language data changes the attribution obligations.
8. **Binary size** versus the single self-contained binary (D28), and **RAM** versus
   `PREVIEW_MEMORY` and the `docs.local/PERF.md` budgets. Concrete figure for the forward-expansion
   approach (expanding `.dic` into a form→stem map): en_US ~50k stems → ~250k forms, fine;
   **ru_RU ~150k stems → 3–5M forms ≈ 100–200 MB resident**, not fine.

### If it is ever picked up, the order is

1. **O1** (`fold_version`) — precondition, stands alone.
2. **O2** (normalization) — the floor the oracle stands on. Every candidate is validated by an
   exact lookup; if query and headword disagree on normalization, *every* candidate fails and the
   suffix rules get blamed for a bug one layer down.
3. **M5** (簡繁) — pure table, zero linguistic risk, best value per line of code here.
4. **M1 + M2**, starting with **Spanish and Russian**, where the payoff is largest and the corpus
   already lives. Curated tables first; **M3** is a later optional upgrade behind the same
   interface, never a prerequisite.

### Assumptions this rests on

- The corpus is es/en-dominant (inferred from the test-fixture paths).
- Users look up **inflected forms encountered in text** — that is the entire premise. If they
  mostly look up citation forms, the whole item is worthless.
- Prepared mode is primary (D15), so index-backed validation is available for most dictionaries.
- `-tags purego` must keep building (D4/D18), which disqualifies every cgo binding outright.

---

## O4 — Speak the selection with the browser's own voice — **ON HOLD**


### The decision already taken

**Do not sniff dictionary markup. Do not patch dictionaries.** The feature is
*speak the user's current selection*, triggered in the app, using
`speechSynthesis.speak(new SpeechSynthesisUtterance(text))`. It knows nothing
about any dictionary, so no dictionary can break it and it can break none.

### Why it came up

ODE 2024 renders a speaker icon on thousands of examples that have **no audio in
the MDD**. Those icons are not shipped markup — `ODE_2024.js:398 initTTS()`
creates them at runtime (`$('<a>',{class:'audio_play_button tts'})`) and binds
its own click handler. They only became visible because D78 let `main()` run.

They are **not** browser TTS. `createEdgeTTS()` (`ODE_2024.js:432`) opens a
WebSocket to an undocumented Microsoft Edge read-aloud endpoint, signing the
request with a SHA-256 token derived from a 5-minute-rounded Windows FILETIME
(`generateSecMsGecToken`, needs CryptoJS, which *is* present in the MDD, so
`/res/<id>/crypto-js.min.js` resolves; the server sends no CSP, and
`enableOnlineTTS` is `true` at line 32).

**Observed:** the socket upgrades and closes cleanly, no error, no audio.
Diagnosis, from reading the file — **not fixed, and deliberately not fixed**:

- Playback is `globalAudio` (`:296`, a plain `new Audio()`), the *same object*
  the dictionary's ordinary MDD audio icons use (`:310-312`). If those icons
  play, then `Audio.play()`, user activation and the sandbox are all proven, and
  the fault is that `playText` (`:545`) never received a URL.
- `sendSSMLRequest`'s promise is resolved from exactly one place, `:516`:
  `dataView.getUint8(0)===0x00 && ===0x67 && ===0x58` — a hardcoded guess that
  the terminating binary frame's 2-byte big-endian header length is exactly
  0x0067 (103). If Microsoft's header is any other length, the branch never
  matches, the promise **never settles**, `await` hangs forever and `catch`
  never runs. That is precisely the observed symptom.
- Second candidate: `:502` `if (!(event.data instanceof Blob)) return;` discards
  every **text** frame, including `turn.end` and any error payload, so the code
  cannot learn that a turn ended or was refused.
- **Decisive check if anyone ever cares:** devtools → Network → the `edge/v1`
  entry → *Messages*. Multi-KB binary frames ⇒ the 0x0067 marker; only text
  frames ⇒ the service refused the request.

The general lesson, which is the actual reason for this item: a dictionary's
bespoke cloud-TTS integration built on a private protocol **will** rot — this one
already has, and its pinned `Sec-MS-GEC-Version=1-130.0.2849.68` is years stale.
A wudict-level fallback that owns nothing dictionary-specific is the durable
answer. Per-dictionary `res/` patching was considered and **rejected by the user
as unsustainable**.

### What already exists (do not rebuild it)

Getting selected text out of an article is the only awkward part, and it is
solved on **both** renderers:

- **Shadow-DOM articles** — `index.html:1662` uses `attachShadow({mode:"open"})`,
  and `index.html:2384` already reads
  `(root&&root.getSelection?root.getSelection():document.getSelection()).toString()`.
- **Sandboxed iframes** — the parent's `getSelection()` cannot see inside one,
  but `frame.js` already reads `document.getSelection()` in-frame on dblclick and
  posts it out as `{t:"pick"}` (the `HOST.postMessage` path, D78).

So the work is *a trigger plus a call*, reusing the paths that feed
`lookupSelection()` (`index.html:2227`) today. No new architecture.

### Open decisions, all small

1. **Trigger.** A keyboard shortcut collides with nothing and is cheapest. A
   floating button on selection is nicer and adds a UI surface. Double-click is
   already taken — it means *look this word up*.
2. **Voice.** `getVoices()` is empty until `voiceschanged` fires in Chrome, so
   one async settle is required. Otherwise it is a single preference; the honest
   default is the browser's default voice.
3. **Language.** **There is no per-dictionary language field anywhere in the
   model** — verified against `internal/dict` and a real `info.txt`. Either speak
   with the default voice and let the user choose one in prefs, or add the field
   (ingest + schema + prefs + UI) later. On an English-dominant library the
   default is fine; it is visibly wrong on Espasa-Calpe.
4. **Android (D52).** WebView has no TTS unless the device has an engine
   installed. `getVoices()` returning empty is the capability test — hide the
   affordance rather than offering a dead button.
5. **Cancel.** One platform voice, many frames. Cancel on navigate, on a new
   search and on section collapse, or a voice keeps talking about a word the user
   has left. Touches the D73 latch behaviour; nothing else.
6. **Chrome's ~15 s utterance cutoff.** Real and still unfixed upstream; the only
   workaround is a `resume()` timer hack. Irrelevant for a selected example
   sentence, bites only on paragraph-length text.

Every one of those is a preference or a capability check. **None is dictionary
knowledge**, which is the whole point.

