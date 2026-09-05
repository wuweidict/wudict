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

## O3 — Morphology (inflected-form lookup) — **M1 DONE (D82); the rest ON HOLD**

**What was built.** Headword lemmatization for de/en/es/fr/it/ru via `aaaton/golem/v4`, fired only
when the whole search found nothing, restricted to dictionaries whose language was detected from
declared metadata, a file-name prefix, or an exact ancestor folder name (English is the fallback
for an undetected dictionary; every other language must be stated). See `docs/SPEC.md` §4
"Lemmatization — the second wave" and D82. Two of the defects below were **dissolved rather than
solved** by the zero-results rule, and are marked so.

Still open here: M2 (`.aff` reverse-application / curated suffix tables), M4 (Japanese
deconjugation), M5 (簡繁), morphological full-text, and every non-European script.


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

1. ~~**Language attribution is the actual hard problem, not morphology.**~~ **Dissolved (D82).**
   The premise held that attribution must be *right*, which forced a per-dictionary override UI.
   Under the zero-results rule it only has to be *cheap*: a wrong language yields a candidate that
   is almost certainly not a headword either, on a query that already found nothing. So attribution
   is declared-metadata → file-name prefix → exact ancestor folder → English, with no UI, no new
   persistent state (the declared value rides in the existing `meta` k/v table), and no heuristics
   to tune. A dictionary that is silently unlemmatized is fixed by renaming it. Cross-language
   collisions are handled structurally instead: a candidate is only ever offered to dictionaries of
   the language it was derived from.
2. **Preview vs prepared asymmetry (D15).** Prepared dictionaries validate candidates with a
   B-tree hit; preview backends scan in memory, so 10 candidates = 10 linear scans. Morphology
   would silently become a prepared-only feature — another axis of divergence between the two
   backends.
3. **Latency at real scale.** Bounded in D82 by construction — a lemmatizer returns **one**
   candidate per language, so the second wave is at most one extra search per detected language
   (six, ceiling), over a subset of the dictionaries, on a query that already returned nothing.
   The concern returns in full with M2/M3, where a rule table generates many candidates.
4. ~~**Ranking regression.**~~ **Dissolved (D82).** Morphology runs only when the result set is
   empty, so there is nothing for a derived hit to outrank and no rank field is needed anywhere.
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


---

## O5 — Babylon's per-dictionary part-of-speech table (block 0x27) — **ON HOLD**

`internal/format/bgl/tables.go` maps a definition's `0x02` field to an English
label from a static `partOfSpeechByCode` (0x30 → "noun", 0x31 → "adjective", …),
faithfully ported from pyglossary's `bgl_pos.py`. Some BGLs carry **their own**
table in metadata block type 3, code `0x27`, as a pipe-separated list:

```
style=1|48,s.|49,adj.|50,v.|51,adv.|52,interj.|53,pron.|54,p…
```

48 is 0x30. So `Babylon English-Spanish` declares 0x30 = "s." (*sustantivo*)
where we render "noun", and `Babylon Spanish-English` declares 0x30 = "n." — the
same code, two labels, chosen by the dictionary. Where the block exists we are
overriding the author with a hardcoded English word.

**Why it is on hold rather than done.** Measured over the maintainer's 21-file
BGL corpus: **2 files** carry a populated `0x27`, both official Babylon
products; 4 carry a single `0x00` byte, the rest carry nothing. Two samples is
not enough to be sure of the grammar (what `style=1` selects, whether the codes
are always decimal, what a second `style=` group would mean), and getting it
wrong changes rendered article text for everyone. The static table is right for
19 of 21 files today.

**If it is taken up:** parse `0x27` in `readType3`, keep the bytes raw as
`title`/`desc` are (charset codes arrive later), build a `map[byte]string` after
`detectEncoding`, and consult it in `collectDefiFields` *before* falling back to
`partOfSpeechByCode`. Strictly additive — an absent or unparseable block leaves
today's behaviour exactly as it is.

## O6 — Dictionary icons — **ON HOLD (a consumer now exists; still unscheduled)**

Several formats ship an icon, and we read none of them. `dict.Meta` has no field
for one, and nothing in the UI or the CLI would display it, so this is a
*feature* waiting on a design, not a gap in the readers.

**What is already known, so the research is not repeated:**

- **BGL.** The icon is metadata block type 3, code **`0x0B`**, and its payload
  is a plain `.ico` file (`00 00 01 00` ICONDIR, multiple sizes: 16×16, 32×32
  and 48×48 at 8/24/32bpp were present in the samples). Sizes observed: 1.4 KB
  to 565 KB. `unidict/docs/bgl_format.md §5.1` calls `0x0B` "Browsing Enabled
  (bool)" and names `0x24` "Icon2" — **that document is wrong on both counts**;
  `0x24` was empty in every file that had it. Measured, not read.
- **MDX** carries no icon. **StarDict** has no icon field in `.ifo` (some
  distributions ship a loose `NAME.png` beside it, by convention only).

**Where it would go.** Not a new mechanism: `text.db`'s `meta` table already
holds a dictionary's name, description and source, and `info.txt` is regenerated
*from* it (`internal/store/library.go`). An icon is one more meta row — a blob,
or a file `icon.png` written into the library folder beside `text.db` — captured
at ingest and served by the API. The prepared folder is already self-describing;
this is one field, not an architecture.

**The order of work is deliberate:** decide the UI first (dictionary panel? the
`/setup` list? favicon of a single-dictionary view?), then add the `Meta` field
and the one or two readers that can fill it. Capturing icons with nothing to
show them is dead weight in the ingest path and in every `text.db`.

**Update (D98).** The UI question above is now half answered: the panel card has
an **About** disclosure (`GET /api/about`, `internal/server/about.go`) showing
what a dictionary says about itself, and an icon belongs at the head of it. What
still blocks the work is unchanged and is the *other* half: About is a text
surface end to end — a provider returns text, one function normalises it, the
client inserts a string — while an icon is bytes that need a media route, a
cache key and an ingest-time capture. Also unresolved: DSL's own icon is a
loose `<name>.bmp` (BMP, which browsers do not reliably render), ZIM's is
`Illustration_48x48@1`, slob's a tag, and BGL's the block above — four different
retrievals for one 48-pixel square. Not scheduled.

## O7 — DSL optional-part headwords are indexed as 2 variants, not 2ⁿ — **ON HOLD**

`internal/format/dsl/title.go` `transformTitle` emits exactly two lookup keys
per headword line: `Full`, with every `(optional)` part kept, and `Alt`, with
every one dropped. It is a faithful port of pyglossary's `TitleTransformer`.
Lingvo and GoldenDict index every *combination*, so `abandonar(se)(lo)` should
also be findable as `abandonarse` and `abandonarlo`; we index only
`abandonarselo` and `abandonar`.

**Why it is on hold.** Measured over the maintainer's DSL corpus — ~1.2 M
headword lines across 20 files — **49 lines** have two or more optional groups
(0.004%); most files have none at all. With a single group our two variants are
already the complete set, which is why this has never been noticed. The change
would touch the ingest hot path of every DSL dictionary, and would need a cap
(2⁴ = 16 keys) so a pathological headword cannot explode the key count, to make
49 headwords findable.

**If it is taken up:** rewrite `transformTitle`'s accumulation to collect
`(text, optional)` segments and derive `Full`/`Alt`/`Display` plus a capped
variant product from them, rather than building the three strings in one pass.
`title_test.go` already pins the current behaviour and would guard the rewrite.

---

## O8 — Serve media by locator instead of by opening the whole dictionary — **DONE for mdx/stardict/dsl (D95); slob still open**

*Provoked by unidict's UDX, whose resource values are locations
(`mdd_index(4)+offset(8)+size(8)`) rather than bytes. The idea is right; the
reason it is right is not the one it looks like.*

### The premise that has to be corrected first

**We already do not copy media bytes by default.** `AUTO_INDEX` prepares
headwords only; `media.db` is opt-in (D24), and there is no locator database of
any kind in the tree today. So "store locations, not bytes" **buys no disk and
saves no ingest time** — both are already banked.

What the default actually costs is this: with no `media.db`, every resource
falls through `upgraded.Resource` to `upgraded.source()`
(`internal/server/registry.go:52`), which is `dict.Open` on the original file —
**the full direct backend**. For an `.mdx` that decompresses every key block and
decodes every headword; then `Open` does the same for each companion `.mdd`
(`internal/format/mdx/mdx.go:124`); then `resourceIndex()` builds a map over
every resource entry. At the ~350 B/headword of `docs.local/PERF.md §3.1`, a
300 k-headword MDX with a 500 k-file MDD is **≈280 MB resident and seconds of
CPU to serve one 8 KB `.ogg`** — and the handle is evictable, so the next
article after a janitor pass pays it again.

**The locator is therefore not an alternative to `media.db`. It is the missing
floor under the mode we already ship as the default.** Nothing is traded away
for it, which is why it is worth doing at all.

### Why it is cheap: the key index is expensive, the record index is not

`gomdict.BuildIndex` is monolithic — `readKeyBlockInfo` → `readKeyEntries` →
`readRecordBlockMeta` → `readRecordBlockInfo` → `buildRecordRangeTree`. Only the
first two are expensive, and **nothing in the last three depends on them**:
`readRecordBlockMeta` needs exactly one value,

```go
recordBlockStartOffset := keyBlockInfo.keyBlockEntriesStartOffset + keyBlockMeta.keyBlockDataTotalSize
```

and `keyBlockEntriesStartOffset` is assigned at `mdict_base.go:484` as pure
arithmetic on `keyBlockMeta`, which `gomdict.New()` already produced. So a new

```go
func (mdict *Mdict) BuildRecordIndex() error   // no key blocks, no keywords
```

is a dozen lines, and its cost is **~24 B per record BLOCK, not per file**:
a 5 GB `.mdd` at 64 KB blocks is ~80 k blocks ≈ a 1–2 MB read and a few MB of
structs. Two orders of magnitude under the 280 MB. `MDictKeywordEntry` is
`{RecordStartOffset, RecordEndOffset}` into the *decompressed* stream, so those
two numbers plus the block table are the whole of what `locateByKeywordEntry`
consumes — which is exactly UDX's 20-byte value, arrived at independently.
**v3 is easier**: `scanV3Blocks` already runs in `New()` and
`locateByKeywordEntryV3` re-scans the block table per call from `meta.v3Offsets`,
needing no key state at all.

### The locator is a CACHE, not an artifact — this is the whole design

It is derivable from the source, disposable, and its absence is never an error.
That settles every hard question at once:

- **It is not part of D20's portable unit.** So it is not `media.db` and does not
  touch `media.db`'s meaning. Proposed sibling name `media.link.db` (alt:
  `media.map.db`); `info.txt` describes it as a cache and deleting it is always
  safe.
- **It needs no ingest changes.** Build it **lazily, on the first media miss** —
  the one expensive open is the cost paid *today* on every miss, now paid once.
  Dictionaries whose articles reference no media never build one.
- **After it exists the janitor can drop the direct backend permanently**
  instead of thrashing a 280 MB cache entry.

**Modes are a resolution order, not a switch.** `upgraded.Resource` gains one
rung, ordered by cost, and `media.db` keeps winning when present:

```
1. Store.Resource        packed bytes in media.db   (unchanged, D2/D24)
2. locator + pread       media.link.db              (new)
3. u.source()            full direct backend         (last resort, as it should always have been)
```

Having both packed and linked is redundant, never wrong. **Portable mode is not
endangered by this and must stay**: a linked folder serves media only while the
original file is where it was.

### Shape

```sql
-- media.link.db
CREATE TABLE part(id INTEGER PRIMARY KEY, path TEXT, size INTEGER, mtime INTEGER);
CREATE TABLE link(name TEXT PRIMARY KEY, mime TEXT, part INTEGER, off INTEGER, len INTEGER);
```

```go
// internal/dict — producer, implemented by the format at build time
type MediaLink struct{ Name, MIME string; Part int; Off, Len int64 }
type MediaLinker interface {
    MediaParts() []string
    MediaLinks() ([]MediaLink, error)
}
// consumer — registered per format, opened cheaply at serve time
type MediaFetcher interface {
    Fetch(part int, off, size int64) ([]byte, error)
    Close() error
}
```

Serving is format-agnostic; only the opener is format-specific.

*Corrected while implementing:* the `*os.File` + `ReadAt` sketch above is wrong
for MDict, whose records live in COMPRESSED blocks — a raw file offset names
nothing. The recorded offset is into the decompressed record stream, and the
read goes through `gomdict.LocateAt`, which is the existing record path with a
synthesized entry. The "small LRU of decompressed record blocks" needs no work
either: `MdictBase.blkCache`/`blkOrder` already is one, bounded at
`recordBlockCacheCap`, so the 20-audio-icon page already collapses to ~2
decompressions.

### Per format, honestly

| format | linkable | note |
|---|---|---|
| **mdx/mdd** | **yes — the whole point** | `BuildRecordIndex` above; hundreds of thousands of files is the normal case |
| **stardict, dsl** | **yes, and trivially** | resources are already loose dirs / zips behind `internal/resource`. No offsets are needed at all — the locator degenerates to *recording the resource ROOTS* so the dictionary is never opened. Kills the worst case (parsing a 400 MB DSL to serve one image) for almost no code. **Do this first.** |
| **slob** | yes, later | `getItem(bin, item)`: `part`=bin, `off`=item; the bin offset table is small |
| **bgl** | **no** | one gzip stream, no random access, and `loadRes` holds every resource in RAM. Packed or source-open only. Its resources are few and small; this is not a loss. |

### Where it backfires — the non-negotiables

1. **A locator is offsets into a specific byte sequence.** If the user
   re-downloads or recompresses the `.mdd`, stale offsets serve *wrong bytes* —
   an `.ogg` where a `.png` was — not a clean 404. `part.size`/`part.mtime` must
   be revalidated on open (one `stat`) and the cache dropped on mismatch. We
   already store `source_size`/`source_mtime` in `text.db` meta, so this is a
   pattern the code already has.
2. **Scoped storage.** Path-based `pread` is fine for the current Android port
   (D52 execs a native binary over real paths), but a future SAF/content-URI
   source would force packed mode. Stated so it is not a surprise.
3. **A linked folder is not self-contained.** That is the honest cost, and the
   reason packed mode stays and stays advertised.

### UI: the locator has NO UI, and that is the design

A cache does not get a chip. What already exists is D24's media switch, and only
its wording needs to become honest:

- off (default) — *"Media stays in the original files"*
- on — *"Copy media into this folder"* + size — *"works on its own even if the
  original is deleted or the card is removed"*

One new state deserves surfacing, and only when the promise actually breaks:
source gone **and** not packed. `WriteInfo` already detects exactly this
(`[no longer on disk - this folder is now the only copy]`), so the panel shows
`⚠ media offline` **only then**, offering "Copy media in" when the source is
still reachable.

**Refused: a three-way none/linked/packed mode selector.** It exposes MDict
internals to someone who wants to look up a word, has an obviously-correct
default, and would be a support surface forever.

### Decision matrix

| | disk | ingest | RAM to serve | first byte | standalone | survives an edited source |
|---|---|---|---|---|---|---|
| today, unpacked *(default)* | 0 | 0 | **~280 MB** | **seconds** | no | yes |
| **linked** *(proposed)* | ~40 MB cache | 0 (lazy) | ~5 MB | ~1 ms | no | yes, revalidated |
| packed *(D24, opt-in)* | = source media | minutes–hours | ~0 | ~1 ms | **yes** | yes |

The only row being deleted is the current default's RAM and latency.

### Phasing

**P1** stardict/dsl resource roots — no new format code, immediate. **Shipped.**
**P2** `gomdict.BuildRecordIndex` + `media.link.db` + the mdd fetcher — the win. **Shipped.**
**P3** slob — still open, and the only part of O8 left.
**Never** bgl. **Untouched** `media.db`.

Shipped as D95: `internal/resource/link.go` (the provider registry),
`internal/gomdict` (`BuildRecordIndex`, `LocateAt`), `internal/format/mdx/link.go`,
the extracted `MediaSources` in the dsl and stardict backends,
`internal/store/link.go` (`media.link.db`), and the three-rung `upgraded.Resource`
in `internal/server/registry.go`.

---

## O9 — Auto-preparation has no size threshold — **DONE (D99)**

*Found while adding ZIM (P92), which needed an exemption from `maybeAutoIndex`
and got one on a property (`SelfIndexed`) rather than on size. The general
version of the problem was left here on purpose.*

`entry.maybeAutoIndex` (`internal/server/registry.go`) fires once per process on
the first successful search and prepares with `store.Plan{}` — headwords only,
which is cheap **per entry** and unbounded **per dictionary**. The cost model
behind D13 was a normal dictionary: tens of thousands of headwords, a text.db
smaller than the source. Two things break it:

- **Entry count.** A 300 k-headword MDX pays a multi-second ingest and a
  hundreds-of-megabytes write on a search the user expected to be instant. The
  first-run indexing storm in `docs.local/PERF.md` is this, multiplied by a
  corpus.
- **Compression ratio.** A source that packs whole blocks beats a `text.db` that
  DEFLATEs one row at a time (D24) by enough that preparation *expands* the
  library. ZIM is the extreme case (~14× vs ~3.5×, a 123 MB file becoming a
  ~431 MB text.db), which is why it declines automation outright — but the same
  arithmetic applies weakly to block-compressed MDX.

The fix is one threshold: above N headwords (or an estimated text.db size),
`maybeAutoIndex` returns and the panel's index chip (D93) becomes the only way
in — the dictionary still works, from its own backend, exactly as a ZIM does.

**Closed by D99.** N is now `autoIndexMaxEntries` = 1,000,000 headwords, and it
gates `maybeAutoIndex` **only** — a demanded index stays ungated, D92 unchanged.
The number is defensible rather than picked: the dictionary that forced the
question carries 2.9 M keys, and the corpus that had to keep working
automatically tops out well under a million.

The second half of the fix is the chip (D93). Its *absent* state now carries an
estimate of what preparing would cost, calibrated at render time against the
user's own already-indexed dictionaries rather than a constant — the measured
spread is 614 B/entry median against 1041 B/entry in aggregate over the large
ones, which is too wide for one number to stand in for. The estimate is worded
and styled so it can never read as a measured size: `est.`, a `~`, a tooltip
that says so in words, and neither the `.on` nor the `.locked` styling that the
real on-disk size chip has.

The compression-ratio half of the problem statement is **not** addressed here
and stays with ZIM's property-based exemption: an entry count is a cheap,
already-populated number, while a ratio is not knowable before the ingest that
the gate exists to avoid.

---

## O10 — Sharing the library over Wi-Fi — **WON'T DO (2026-09-05)**

A "Share link" button next to the D101 *Reachable from* switch. Costed in full in
`docs/ANDROID-LAN-SHARING.md`: the button is 40 lines, the promise it makes is a
foreground service, a wake lock, a Wi-Fi lock, a fourth power state, two permissions,
a Play declaration — and, underneath all of it, an HTTP surface with no
authentication (`PUT /api/prefs`, `POST /api/demand` and the whole library are open
to any LAN peer). Verdict there: reject the button; if anything ships now, show the
address as text under the switch and offer nothing.

The switch is for *TESTING ONLY* with its limits stated in the hint; nothing was built to
prolong a session or to advertise the address. Reopen only together with §3 of that
document — server-side authentication — since every larger version of this depends on it.
