---
name: commit-msg
description: Write the commit message (and optionally the changelog.txt lines) for the uncommitted wudict work. Use whenever the user asks for a commit message in any wording ("compact", "brief", "ASD-STE100", "group semantically"). Never commits.
argument-hint: "[compact|full] [changelog]"
---

Never run `git commit`, `git add` or `git stash` unless the user explicitly asks you to commit.

## Read the change, cheaply
1. `git status --short`, `git diff --stat`, `git diff --cached --stat`.
2. `git diff -U0 -- <file>` only for files whose purpose the session did not already establish. Never dump the whole diff of `internal/server/web/index.html`; diff it per hunk range.
3. If `docs.local/PHASES.md` exists, its last record (`### ` heading to end of file) usually holds the why: use it instead of re-deriving.

## Format (the repo's history; match it)
- Subject: `type(scope): what changed; second change` — types `feat fix perf chore test docs refactor`; scope = area (`tap`, `zim`, `wildcard`, `tts`, `panel`, `ci`, `release`, `android`…). ≤ 72 chars, lowercase, no period.
- Body, wrapped at 72: one block per functional group, headed `Area (files, D<n>):`, then `- ` bullets. A bug fix states **Root cause**, **Fix**, **Verified** (where, how); a feature states behaviour, then mechanism. Cite D-numbers and `#issue` numbers. Say what was not tested if it matters.
- Style: ASD-STE100 — one statement per sentence, active voice, present tense, no filler, no "this commit". Identifiers in backticks only where a reader needs them.
- `compact` (default when the user says brief/compact/concise): subject + ≤ 6 bullets, no root-cause prose. `full`: the structure above.
- Several unrelated changes → one subject naming the two main ones joined by `;`, groups in the body; or, if asked, one message per group with its files.

## changelog.txt (only with `changelog`, or when the user asks)
Top section is the unreleased version (`vX.Y.Z` line). Append under its emoji headers, creating one if missing, in this order: `🚀 Features`, `⚡ Performance`, `🐛 Bug Fixes`. Line form: `(area) user-visible effect, in the reader's words` — no internals, no file names, no D-numbers (D102; docs/STYLE.md register). Skip purely internal changes.

Output the message once, in a fenced block so it copies cleanly, plus at most a one-line note of what was left out.
