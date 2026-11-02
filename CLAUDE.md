# WuWeiDict

**Naming (D27, amended 2026-09-28).** In anything a user reads, prefer **`wudict`** (lowercase; **wuDict** in the Android app and browser extensions); **WuWeiDict** is the official full name, used only where the official name is required. The technical name is **`wudict`** everywhere else — binary, env prefix (`WUDICT_*`), user folders (`~/.wudict`; `wudict` in each XDG base dir on Linux/BSD, `internal/basedir`), db format prefix (`wudict:`), localStorage keys, CSS classes. The module path is `github.com/wuweidict/wudict` because the repo is `wudict`.

Go web dictionary app supporting MDX/MDD, StarDict, Aard2 Slob, Lingvo DSL, Babylon BGL, openZIM; wudict markdown (`.wudict.md`: standard CommonMark, read by `internal/format/wmd` and written by `wudict dump -format md`). Dual-backend: a dictionary is searched through its own format ("preview", D15) until it is **prepared** into a library folder — `<db dir>/<name>/{text.db, media.db, info.txt}` (D20) — which is the primary mode. Preparation is automatic and cheap (headwords only, `AUTO_INDEX`); *full-text* is a per-dictionary switch, and media packing a second (D24). Search modes are exact · prefix · full-text · wildcard (D16, D167 — "fuzzy" and "contains" are retired, D170).

## Read this first (token discipline)
Every doc below is long; read the section your change touches, never a whole file. `grep -n '^##' <file>` is the map.
- `docs/SPEC.md` — architecture, schema, query engine (78 KB). Read the § you touch before writing code there.
- `docs/FORMATS.md` — per-format facts + exact reference-code pointers. Read the section for the format you touch, nothing more.
- `docs/WUDICT-MARKDOWN.md` — normative spec of wudict markdown (reader + writer). Read only when touching that format or its export. `entry://` is the canonical lookup link; `bword://` is a legacy alias: parse, never emit.
- `docs/STYLE.md` — terms and register for every text a user reads (manual, README, UI, `--help`, howto). Read before writing any of it.
- `docs/OPEN.md` — researched but **unscheduled** items (O-numbers). Check before proposing morphology, stemming or Unicode-folding work; do not start anything in it without being asked.

**D-, P- and C-numbers** cited throughout the code (`D15`, `docs.local/PERF.md §3.1`,
`P88`) refer to the maintainer's decision log, phase record, performance audit and
review backlog. Those are working records kept outside the repository and are not
distributed with it; the citations are provenance, so treat a reference you cannot
open as a note on *why* the code is the way it is, not as a missing file.

## Layout

This repository is the whole project (`github.com/wuweidict/wudict`); there is no enclosing workspace.

- `internal/cli` — the CLI and server entry point (all of it). One `package main` shim builds from it: `./main.go` at the module root → `wudict` (D28). The version stamp targets `internal/cli.Version`, not `main.version`.
- `internal/dict` — core interfaces, format registry, discovery, `Fold`.
- `internal/format/<fmt>` — mdx, stardict, slob, dsl, bgl, zim, wmd backends.
- `internal/store` — the prepared SQLite backend: schema, ingest, library folders, media, `Reconcile`.
- `internal/server` — registry, HTTP API (`web/openapi.yaml`, kept honest by `make api-test`), embedded UI (`web/index.html`, `web/setup.html`, `web/frame.js`).
- `internal/search` — the per-dictionary query and the streaming fan-out; `internal/ftsq` full-text query plans; `internal/wildcard`.
- `internal/htmlref` — article HTML: resource-reference rewriting and sanitising. `internal/resource` — resource-name matching inside containers.
- `internal/basedir` — where the user's files live (`~/.wudict`, or the XDG base dirs on Linux/BSD) and the one-time move between them.
- `internal/intake` — archive/URL/folder → candidate dictionaries → installed. `internal/config` — flag > env > toml layering.
- `internal/lang`, `internal/morph`, `internal/lemmas` — language attribution and lemmatisation (D82, D88).
- `internal/gomdict` — inlined MDX/MDD parser. `internal/speex` — in-process .spx decoder (D18).
- `internal/howto` — the **wudict howto**, the app's guide as an embedded wudict markdown dictionary (`wudict-howto.wudict.md` + `.files/`). Every headword starts with `wudict `; `howto_test.go` holds it to the format (gate, zero warnings, links resolve, a clean dump reproduces it byte for byte). Edit the guide there; `pages/` is the website and is not part of the app.
- `android/` — the Android app (D52): a dependency-free Java WebView shell that execs the android/arm64 binary shipped inside the APK as `libwudict.so`. `make android-go` rebuilds the Go side (cgo flavour, D53: mattn FTS5 + built-in speex, needs the NDK; `make android-go-purego` is the NDK-less fallback), `make apk-foss-debug` / `apk-foss-release` / `aab-play` the whole app; CI signs via repo secrets. The port adds no Go code and no build tags.
- **The Makefile is the developer UI (D10): every action has a target, `make help` lists them, `make check` before declaring work done.**

The reference projects this was built from (`mdict-go-web`, `draego`, `pyglossary`, the speex sources) have been **removed from disk**. `docs/FORMATS.md` still cites their file and function names: read those as provenance for where a rule came from, not as paths you can open.

## Conventions
- Go, modules. `make build` uses cgo (`-tags sqlite_fts5` → mattn sqlite + built-in speex); a **tag-less** `go build`/`go install` gets the pure-Go driver and must keep working, because FTS5 is mandatory (D29). `-tags purego` must keep building and passing (D4, D18). Table-driven tests.
- **Need-to-know (D102).** Internal mechanics and the UI are separate layers; some
  internals map 1:1 to a control, most must not surface at all. A mechanism reaches
  the UI only when the user must *decide* or *act* on it — never as an explanation of
  how the app works. Applies to every label, hint and message.
- Each format package implements both `Dictionary` (direct runtime lookup) and `Reader` (sequential ingest scan) — parsing logic written once, shared by both. Ingesters are one-shot batch paths.
- **Comments state the present (D161).** Invariants, reasons, measurements and D/P/§ citations; never "used to" narratives - history belongs in the decision log.
- **Prepared data changes only through `store.Reconcile` (D157).** A new way to prepare, rebuild or pack states a `store.Target`; it never calls `IngestPlan`/`IngestMedia` itself, and it judges a prepared database through one `store.Inspect` read. A dictionary never prepared starts from headwords only everywhere — the app, `wudict ingest`, the self-preparing formats (D24, D152 Am. 1).
