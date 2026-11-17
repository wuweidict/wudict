---
name: bench-diff
description: Measure a wudict change against the old code on real dictionaries — result-set hash differential plus best-of-N timings, old tree vs new tree, with automatic cleanup. Use before claiming a search/ingest/compression/format change is faster, slower or regression-free.
argument-hint: "<old-ref> [new-ref|worktree] <pkg> <harness_test.go>"
---

Script: `.claude/skills/bench-diff/bench.sh OLD NEW PKG HARNESS [go test args…]`
- `OLD`, `NEW`: git refs; `NEW=worktree` takes the tracked + untracked-not-ignored files as they are on disk.
- `PKG`: package dir, e.g. `internal/store`. `HARNESS`: a `zz_*_test.go` written in the session scratchpad, never in the repo. `HARNESS_NEW=` gives the new tree its own harness when the API changed.
- Env passes through to the harness (e.g. `WUDICT_BENCH_DB=…`, `N=5`); `SCRATCH=` sets where the trees go (default `$TMPDIR`); `GO_TAGS=` (default `sqlite_fts5`). Trees are removed on exit, success or failure. The script fails when either side printed no `case=` line.

## Writing the harness
- `package <pkg>` (internal access), one `func TestZZ…(t *testing.T)`, `t.Skip` when its env path is unset.
- One `t.Logf` line per case: `case=<name> hash=<sha1 of the results in order> n=<count> best=<min of N runs>`. Hashes are compared first: a timing win with a changed hash is a regression until explained.
- Open prepared databases with `store.Open` (read-only), so a real library is safe. Time stages with `time.Now()` around each; on darwin `pprof` misattributes cgo time to `runtime.cgocall`.

## Data
Real prepared dictionaries (`<data dir>/db/<name>/text.db`), chosen to stress the change: alias-heavy (millions of alias rows), capital-filed headwords, non-Latin scripts (Cyrillic, CJK), and the largest you have. Synthetic fixtures only catch what they contain. Measure multi-GB sources in place, never copy them; check free disk space before anything that writes GBs.

Android: cross-compile each tree's test binary (`GOOS=android GOARCH=arm64 CGO_ENABLED=0 go test -c`) and follow the `android-device` skill.

Maintainer overlay: if `.claude/local/corpus.md` exists, take the dictionaries and paths from it.
