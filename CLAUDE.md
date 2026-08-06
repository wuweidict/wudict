# WuWeiDict

**Naming (D27).** The product is **WuWeiDict** in anything a user reads; the technical name is **`wudict`** everywhere else — binary, env prefix (`WUDICT_*`), config dir (`~/.wudict`), db format prefix (`wudict:`), localStorage keys, CSS classes. The module path is `github.com/legbehindneck/wudict` because the repo is `wudict`.

Go web dictionary app supporting MDX/MDD, StarDict, Aard2 Slob, Lingvo DSL, Babylon BGL. Dual-backend: a dictionary is searched through its own format ("preview", D15) until it is **prepared** into a library folder — `<db dir>/<name>/{text.db, media.db, info.txt}` (D20) — which is the primary mode. Preparation is automatic and cheap (headwords only, `AUTO_INDEX`); *contains* (trigram) and *full-text* are per-dictionary switches, and media packing a third (D24). Search modes are exact · prefix · contains · full-text (D16 — "fuzzy" is retired).

## Read this first (token discipline)
- `docs/SPEC.md` — architecture, schema, query engine. Read before writing any code.
- `docs/DECISIONS.md` — ADR log. Check status before re-opening any settled question.
- `docs/FORMATS.md` — per-format facts + exact reference-code pointers. Read the section for the format you touch, nothing more.
- `docs/PHASES.md` — phase plan + running record. Update when a phase advances.
- `docs/PERF.md` — measured RAM/CPU audit against the real 105-dictionary corpus (unit costs per headword, the first-run indexing storm, ranked fixes). Read before touching concurrency, caching or the open/ingest paths.
- `code-review.md` — standing optimization backlog (C1–C10), prioritized by D15.
- `docs/OPEN.md` — researched but **unscheduled** items (O-numbers). Check before proposing morphology, stemming or Unicode-folding work; do not start anything in it without being asked.

## Layout

This repository is the whole project (`github.com/legbehindneck/wudict`); there is no enclosing workspace.

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
- Update `docs/PHASES.md` (record section) at the end of every working session; update `docs/DECISIONS.md` when a decision is taken.

## Non-Negotiables
- organize the process in such a way that everything is correct and works without testing and verification and validation ON THE FIRST PASS, AVOID SLOPPY ASSUMPTIONS and DON'T RELY ON DEBUGGING IT LATER - you MUST do INTERNAL testing, verifications, validation and iterative SELF-CORRECTION **in your deep subconscious and conscious mind** before writing any code. leave all the manual testing and verification to the user, who will follow up as needed; NEVER COMMIT unless explicitly instructed, NEVER ACCESS THE EMULATOR FOR tests unless critically justified and approved by the user
- never every commit unless explicitly requested