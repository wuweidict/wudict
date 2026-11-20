---
name: dict-cli
description: Inspect real dictionaries with the wudict CLI — find them, read metadata, headwords, articles and resources (CSS/JS/media), and sweep the whole library for a property — at the lowest token cost. Use before reasoning about any dictionary's content, markup, CSS, resources or format behaviour, and for any "check every dictionary for X" request.
argument-hint: "[what to look for]"
---

Reason from the real file, never from a screenshot or memory. Build first (`make build` → `./wudict`); every command below is read-only unless marked.

## Find the dictionaries
`.claude/skills/dict-cli/corpus.sh > <scratchpad>/list.txt` — every dictionary in the configured `DICT_DIR` (toml, else env), one path per line, deduplicated.
Traps: in zsh an unquoted `$var` holding several paths is ONE argument (use an array or `${(f)var}`); `grep`/`cat` may be aliased (ugrep, bat) and change output shape — scripts use `command grep`, `/bin/cat`.

## One dictionary — cheapest view first
| Need | Command | Cost |
|---|---|---|
| metadata, caps, files, sizes | `wudict info <f>` | ~10–30 lines; `\| grep -E '^(name\|format\|entries\|capab)'` |
| headwords | `wudict keys -offset N -n K <f>` | never without `-n` (millions of lines) |
| article content | `wudict lookup -n 1 -format text <f> <word>` | ~45 % of raw |
| article markup (links, classes, `<link>`, `<style>`) | `wudict lookup -n 1 <f> <word> \| grep -oE '<link[^>]*>\|<style\|class="[^"]*"' \| sort \| uniq -c` | pipe raw HTML, never print it |
| prefix / pattern / full-text | `prefix`, `wildcard 'b???d'`, `fts` (the last two need the library folder) | same flags |
| one word, whole library | `wudict searchall -format list -n 2 <word>` | headwords per dictionary |
| resource names | `wudict res -dry-run -o <scratch> <f> '<pattern>'` (`'**'` = all) | names only |
| resource bytes | `wudict res -o - <f> <name> \| head -c 2000` or `-o <scratch-dir>` | |
Calls cost ~0.1 s each on a 250 k-entry mdx; library-wide loops are cheap.

## Resource names differ by format
mdx: names from the `.mdd`, flat (`style.css`). slob: `~/css/…`. ZIM: layout under `-/` (`-/mw/…css`), new-scheme files keep it in the content namespace. StarDict: `res/` folder. wudict markdown: `<name>.wudict.files/`. DSL/BGL: usually none.
`res` exits 1 with `lists no resources` or `matches no resource` — normal, not a failure.

## Whole library
`.claude/skills/dict-cli/sweep.sh <list> <scratch>/out '<pattern>'…` extracts matching resources per dictionary (`<n>/.src` = its path, TSV log). Then `command grep -rliE '<regex>' <out> | sed "s|^<out>/||" | cut -d/ -f1 | sort -un` → dictionary numbers. CSS of 190 dictionaries ≈ 3 min, ~30 MB; delete the folder afterwards.
Inline markup: sample `keys -offset {0,1000,20000} -n 1` + `lookup` per dictionary; skip keys that are resource names (slob `keys` lists `.jpg`/`.css` refs too).

## Source vs what the reader sees
The CLI shows the dictionary's own HTML/CSS. The app renders it in a shadow root (or an iframe), with `:host{all:initial}`, no `<body>` in the shadow root (dictionary `body{…}` rules do not apply there) and an invert filter in dark mode. For anything visual, confirm in the running app: `render-probe.js` (Playwright template, see its header) measures per-dictionary text contrast in dark vs light. A CSS match is a candidate; only the render is a finding.

## Known CLI quirks
- ZIM `keys -offset/-n` counts non-article entries before filtering them out: pages come back short or empty (`zim.go` `Keywords`). Use `keys | sed -n` on ZIM.
- `info` "entries" on ZIM counts more than `keys` lists.

## Mutating commands (ask first; the library is the user's)
`ingest`, `reindex`, `rm`, `clean -f`, `lemmas download|remove`, `token -rotate`; `dump` and `res` write files — always to the scratchpad, and check free disk before multi-GB sources.

Maintainer overlay: if `.claude/local/corpus.md` exists, take the dictionaries and paths from it.
