---
name: ux-check
description: Measure the wudict UI instead of eyeballing it — deterministic before/after screenshots with a pixel diff, tap-target / contrast / type-size metrics on desktop (Playwright) and on an Android WebView (CDP), and the colour-token contrast test. Use before and after any change to index.html, setup/lemmas/groups/browse pages, speak.js, pick.js or frame.js that affects what the user sees or touches.
argument-hint: "[snap|diff|metrics|device]"
---

Run every measurement against **a server you started**, never the user's: an isolated instance with a scratch config, real dictionary folders read in place, and nothing written to a library —
`HOME=<scratch>/home AUTO_INDEX=off ./wudict serve --no-browser -port <p> -dict-dir <folder>… -db-dir <scratch>/db`.
The same instance setup (and the same folders) for the before and the after run, or the diff measures the library, not the change. Stop it and delete the scratch folders afterwards.

## Snapshots and diff
1. Before touching code: build the current tree to a scratch binary, start it from a fresh home, and in a copy of `snap.js` under `.playwright-mcp/` replace the code line `const origin = ORIGIN, out = OUT;` (the header comment names the same words) → run it with `browser_run_code`, `out = ".playwright-mcp/ux/base/"`. 40 shots: 10 states (empty, results, panel, settings, styler, recall, setup, lemmas, groups, browse) × 1440 px fine pointer / 390 px touch × light / dark.
2. After the change: same with the new binary, again from a fresh home (history, favourites and settings from one run otherwise show in the next) → `.playwright-mcp/ux/after/`.
3. `.claude/skills/ux-check/diff.sh .playwright-mcp/ux/base .playwright-mcp/ux/after` — changed pixels per shot, red overlays in `after/diff/`. Every non-zero count must be one the change meant; read the overlay, not just the number. `lemmas` can differ by catalogue timing.

## Metrics
- Desktop: `metrics.js` with `browser_run_code` on the current page — visible controls under 44 / 24 px, text under 4.5:1 (3:1 large), font sizes in use, horizontal scroll, bar height.
- Device (approval per task, skill `android-device`): `adb forward tcp:<port> localabstract:webview_devtools_remote_<pid>`, then `node .claude/skills/ux-check/cdp.mjs <port> .claude/skills/ux-check/metrics-inpage.js`. Bring the app to the foreground first. `adb forward --remove` afterwards.
- Contrast inside articles (dictionary CSS, dark vs light): skill `dict-cli`, `render-probe.js`.

## Colour tokens
`TestTokenContrast` (`internal/server`) parses the light and dark token blocks of the embedded `index.html` and holds every text-on-surface pair to its minimum; a new token pair that carries text gets a row there. Yardsticks, not a conformance claim (D107).

## Traps
- `page.goto` with `?q=` records the word in the server's history unless writes are intercepted (the scripts answer them 204). Never point an ad-hoc script at the user's own server without that: its history is theirs.
- An article can navigate on its own (a ZIM page's script or refresh) and each step is a lookup; a probe that waits on such a page records them.
- Theme: emulate `colorScheme`; it only works while the app's own theme is "auto".
- A colour set from script (`el.style.color="var(--accent)"`) beats stylesheet rules; grep `style.color=` before concluding a token change covers a control.
