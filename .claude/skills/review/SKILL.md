---
name: review
description: wudict code review of the uncommitted diff, a commit, a branch or a plan — correctness, RAM/CPU/allocations, simplicity, regressions against the design decisions. Use for any "review", "red team", "audit", "independent review" request in this repo. Ends in fixes for the deterministic items and a lettered decision matrix for the rest.
argument-hint: "[diff|HEAD|<rev>|<branch>|plan] [perf|correctness|ui|all] [nodelegate]"
---

## 1. Scope
- `diff` (default): `git diff --stat` + per-file `git diff`. `<rev>`: `git show --stat <rev>` then per file. `<branch>`: `git log --oneline master..<branch>`, `git diff master...<branch> --stat`.
- `plan`: review the plan text in the conversation (or the file named) against the code it touches; no diff.
- Read only the hunks and the functions around them (`grep -n '^func '` / LSP `findReferences` for callers). Never read `internal/server/web/index.html` whole.
- "independent" / "delegate": spawn ONE `general-purpose` agent with the scope, the focus and steps 2–4 pasted in; it lacks the session, so give it the D-numbers involved. `nodelegate` or "do NOT delegate": do it inline.

## 2. Against the decisions (always)
The code cites D-numbers (CLAUDE.md: the maintainer's decision log). For every behaviour the change alters, collect the D-numbers cited in and around the changed code.
- If `docs.local/DECISIONS.md` exists: read each entry (`grep -n '^## D<n> '`, then its range); read **Decision** and **Rejected**. A change that reopens a Rejected item or contradicts an ACCEPTED decision is a matrix item — never a silent fix, never a revert. Missing D-entry, phase record or changelog line for a decision-level change is a finding.
- If it does not exist: say so once in the report and list the D-numbers the change touches, so the maintainer can check them. Do not guess what a decision says.

## 3. Focus checklist
- **correctness**: every error path; empty/huge/hostile input (corrupt archives, BOM-less UTF-16, capital-filed headwords, alias-heavy dictionaries); context cancellation; concurrent open/close; `-tags purego` and tag-less builds still compile (D4, D29); `bword://` never emitted (`htmlref.CanonRef`); prepared data changes only through `store.Reconcile` (D157).
- **perf**: allocations in per-entry loops, per-row Go↔SQLite crossings (~0.33 µs/row, ~5 µs/statement), statement shapes per pooled connection, whole-file reads, RAM on Android (`GOMEMLIMIT=248MiB GOMAXPROCS=2`). Anything touching search, ingest or compression → measure with the `bench-diff` skill on real dictionaries before claiming "no regression".
- **ui**: old Android WebViews (Chromium 104: no Chrome 105+ API), touch vs mouse, shadow-DOM vs iframe articles, keyboard, the D102 need-to-know rule for every label, docs/STYLE.md register. No WCAG/accessibility audit unless asked.
- **simplicity**: duplication, parallel knobs that must move together, shims that only delegate, comments that narrate history (D161). Prefer the fix one layer up, at the input that forced the workaround.

## 4. Output
1. Fix right away what is deterministic and has one correct answer; one line each on what was fixed.
2. Everything else: a numbered matrix. Each item gives the finding, the evidence (file:line, measurement), options **A/B/C**, and the recommendation marked. The user answers like `1A 2B 3: noop`.
3. After the answers: implement; never revert work the user applied unless asked.
4. Before ending: `make check`, output redirected to a file and only failures read.

Maintainer overlay: if `.claude/local/review.md` exists, read it and follow it as well.
