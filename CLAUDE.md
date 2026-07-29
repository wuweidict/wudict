# dict-go-web workspace

Go web dictionary app (working name **gonow-dict**) supporting MDX/MDD, StarDict, Aard2 Slob, Lingvo DSL. Dual-backend: direct native-format readers by default; opt-in ingest (`off|text|full`) into SQLite+FTS5 (`<slug>.text.db` + separate `<slug>.media.db`) unlocks fuzzy/full-text search.

## Read this first (token discipline)
- `docs/SPEC.md` — architecture, schema, query engine. Read before writing any code.
- `docs/DECISIONS.md` — ADR log. Check status before re-opening any settled question.
- `docs/FORMATS.md` — per-format facts + exact reference-code pointers. Read the section for the format you touch, nothing more.
- `docs/PHASES.md` — phase plan + running record. Update when a phase advances.
- `docs/PERF.md` — measured RAM/CPU audit against the real 105-dictionary corpus (unit costs per headword, the first-run indexing storm, ranked fixes). Read before touching concurrency, caching or the open/ingest paths.
- Do NOT bulk-read the reference projects below; they are large. Use the pointers in `docs/FORMATS.md` and read only the named files/functions.

## Layout
- `gonow-dict/` — the NEW app (`github.com/glowinthedark/gonow-dict`). `cmd/gonow-dict` CLI; `internal/dict` core interfaces + format registry; `internal/format/<fmt>` backends; `internal/gomdict` inlined MDX parser. **The Makefile is the developer UI (D10): every action has a target, `make help` lists them, `make check` before declaring work done.**
- `mdict-go-web/` — reference: working MDX/MDD server. Reusable: `internal/gomdict/` (MDX/MDD reader, ~2.5k LOC), parts of `main.go` (HTTP server, asset serving, speex audio), `web/mdict.html` (UI foundation).
- `draego/` — reference: CGI SQLite dictionary server. Reusable: FTS5 schema/query logic in `drae.go` (audited; known bugs listed in SPEC §FTS-audit — do not port verbatim).
- `pyglossary/` — reference ONLY (Python, do not run). Format parsers: `pyglossary/plugins/{octopus_mdict_new,stardict,aard2_slob,dsl}/` and `pyglossary/slob/`.

## Conventions
- Go, modules, no cgo unless D4 decides otherwise. Table-driven tests. `make` targets per project.
- Each format package implements both `Lookuper` (direct runtime lookup) and `Reader` (sequential ingest scan) — parsing logic written once, shared by both. Ingesters are one-shot batch paths.
- Update `docs/PHASES.md` (record section) at the end of every working session; update `docs/DECISIONS.md` when a decision is taken.

## Non-Negotiables
- organize the process in such a way that everything is correct and works without testing and verification and validation ON THE FIRST PASS, AVOID SLOPPY ASSUMPTIONS and DON'T RELY ON DEBUGGING IT LATER - you MUST do INTERNAL testing, verifications, validation and iterative SELF-CORRECTION **in your deep subconscious and conscious mind** before writing any code. leave all the manual testing and verification to the user, who will follow up as needed; NEVER COMMIT unless explicitly instructed, NEVER ACCESS THE EMULATOR FOR tests unless critically justified and approved by the user
- never every commit unless explicitly requested