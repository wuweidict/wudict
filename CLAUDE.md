# WuWeiDict

**Naming (D27).** The product is **WuWeiDict** in anything a user reads; the technical name is **`wudict`** everywhere else — binary, env prefix (`WUDICT_*`), config dir (`~/.wudict`), db format prefix (`wudict:`), localStorage keys, CSS classes. The module path is `github.com/wuweidict/wudict` because the repo is `wudict`.

Go web dictionary app supporting MDX/MDD, StarDict, Aard2 Slob, Lingvo DSL, Babylon BGL. Dual-backend: a dictionary is searched through its own format ("preview", D15) until it is **prepared** into a library folder — `<db dir>/<name>/{text.db, media.db, info.txt}` (D20) — which is the primary mode. Preparation is automatic and cheap (headwords only, `AUTO_INDEX`); *contains* (trigram) and *full-text* are per-dictionary switches, and media packing a third (D24). Search modes are exact · prefix · contains · full-text (D16 — "fuzzy" is retired).

## Read this first (token discipline)
- `docs/SPEC.md` — architecture, schema, query engine. Read before writing any code.
- `docs/FORMATS.md` — per-format facts + exact reference-code pointers. Read the section for the format you touch, nothing more.
- `docs/OPEN.md` — researched but **unscheduled** items (O-numbers). Check before proposing morphology, stemming or Unicode-folding work; do not start anything in it without being asked.

**D-, P- and C-numbers** cited throughout the code (`D15`, `docs.local/PERF.md §3.1`,
`P88`) refer to the maintainer's decision log, phase record, performance audit and
review backlog. Those are working records kept outside the repository and are not
distributed with it; the citations are provenance, so treat a reference you cannot
open as a note on *why* the code is the way it is, not as a missing file.

## Layout

This repository is the whole project (`github.com/wuweidict/wudict`); there is no enclosing workspace.

- `internal/cli` — the CLI and server entry point (all of it). One `package main` shim builds from it: `./main.go` at the module root → `wudict` (D28). The version stamp targets `internal/cli.Version`, not `main.version`.
- `internal/dict` — core interfaces, format registry, discovery.
- `internal/format/<fmt>` — mdx, stardict, slob, dsl, bgl backends.
- `internal/store` — the prepared SQLite backend: schema, ingest, library folders, media.
- `internal/server` — registry, HTTP API, embedded UI (`web/index.html`, `web/setup.html`, `web/frame.js`).
- `internal/gomdict` — inlined MDX/MDD parser. `internal/speex` — in-process .spx decoder (D18).
- `android/` — the Android app (D52): a dependency-free Java WebView shell that execs the android/arm64 binary shipped inside the APK as `libwudict.so`. `make android-go` rebuilds the Go side (cgo flavour, D53: mattn FTS5 + built-in speex, needs the NDK; `make android-go-purego` is the NDK-less fallback), `make apk` the whole app; CI signs via repo secrets. The port adds no Go code and no build tags.
- **The Makefile is the developer UI (D10): every action has a target, `make help` lists them, `make check` before declaring work done.**

The reference projects this was built from (`mdict-go-web`, `draego`, `pyglossary`, the speex sources) have been **removed from disk**. `docs/FORMATS.md` still cites their file and function names: read those as provenance for where a rule came from, not as paths you can open.

## Conventions
- Go, modules. `make build` uses cgo (`-tags sqlite_fts5` → mattn sqlite + built-in speex); a **tag-less** `go build`/`go install` gets the pure-Go driver and must keep working, because FTS5 is mandatory (D29). `-tags purego` must keep building and passing (D4, D18). Table-driven tests.
- Each format package implements both `Lookuper` (direct runtime lookup) and `Reader` (sequential ingest scan) — parsing logic written once, shared by both. Ingesters are one-shot batch paths.
