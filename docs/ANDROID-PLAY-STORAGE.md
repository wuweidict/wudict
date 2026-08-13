# Android storage on Google Play — feasibility

Status: **superseded by D62 — frozen, kept for the options it rejects.** Written
2026-08-13 against the shipped shell (D52/D53/D54) as the pre-decision analysis;
Option A was accepted and built the same day. `docs/DECISIONS.md` **D62** is the
authority on what was decided and what shipped — where the two differ, D62 wins,
and this file is not updated to follow the code. Two points here were amended by
the user after it was written: **both** flavours use `getExternalFilesDir()` (not
just `play`), and Export is postponed indefinitely.

## 0. The problem, corrected

The shell today asks for **`MANAGE_EXTERNAL_STORAGE`** ("All files access",
`AndroidManifest.xml:17`) and, on API 26–29 only, the legacy
`WRITE_EXTERNAL_STORAGE` pair. Two corrections to the framing:

- **`WRITE_EXTERNAL_STORAGE` is not the risk.** It carries
  `maxSdkVersion="29"`, so it is inert on every device Play ships policy for,
  and it is not one of Play's *restricted permissions* (that list is SMS and
  Call Log). It can stay in both flavours untouched.
- **`MANAGE_EXTERNAL_STORAGE` is the risk.** It has its own Play Console
  declaration with a closed list of eligible core functionalities — file
  manager, backup/restore, antivirus, device/document search, on-device
  file management. "Dictionary reader that opens the user's `.mdx` files"
  is not on that list. Approval is possible by arguing the document-management
  angle; it is not predictable, the review cycle is measured in weeks, and a
  rejection attaches to the listing.

*(Policy specifics are time-sensitive and were not re-verified online for this
document — confirm against the current Play Console declaration form before
acting.)*

## 1. The fact that decides everything

`content://` cannot reach the Go program. The server is exec'd as a plain child
process (D52); it has no JVM, no Binder handle, no `ContentResolver`. A
`content://` URI is a token meaningful only to the Android runtime. No Go
change makes the child able to resolve one. The only things that can cross the
process boundary are **bytes** (copy) or **file descriptors** (SCM_RIGHTS).

## 2. Where the code actually touches shared storage

Verified against the tree, not assumed:

| Path | Site | Needs shared storage? |
|---|---|---|
| Discovery | `internal/dict/registry.go` — `Discover` walks `DICT_DIR` | yes |
| Ingest | `internal/store/ingest.go` reads the source once | yes, once |
| Preview mode (D15) | format backends open the source per lookup | yes, continuously |
| Prepared lookup | `text.db` in the db dir (internal flash) | **no** |
| Resource fallback | `internal/server/registry.go:58` — `source()` opens the original **only when `media.db` misses** | only if media unpacked |
| Prepared-orphan logic | `internal/store/clean.go` — a vanished source is deliberately *not* an orphan | — |

**Consequence:** with the D24 media switch on, a dictionary that has been
prepared never needs its source file again. The source is an *import-time*
input, not a runtime dependency. That is the lever every clean option pulls,
and it retires D52's stated objection to SAF copy-in ("doubles storage") —
which was only ever true for copy-and-keep, not for copy-then-drop.

The `os.Open` surface, for costing option C: ~20 sites in
`internal/format/{mdx,stardict,slob,dsl,bgl}` and `internal/gomdict`
(`mdict_base.go` alone re-opens `mdict.filePath` at six sites, `v3reader.go` at
three more), plus **sibling resolution by path arithmetic** —
`stardict.go:153` (`base + ".dict"`), `stardict.go:402`
(`filepath.Join(dir, norm)`), `dsl.go:121` (resource next to `srcPath`) — plus
directory enumeration in `registry.go`. Any indirection has to cover opens,
stats, sibling joins *and* directory listing, in four packages.

## 3. Options, cleanest first

### A — Play flavour imports through SAF into app-owned storage (recommended)

Java holds the SAF grant; Go keeps seeing ordinary paths.

- User picks a **folder** with `ACTION_OPEN_DOCUMENT_TREE` (folder, not file:
  `.mdx`+`.mdd`, StarDict's `.ifo`/`.idx`/`.dict.dz`, DSL + `_abrv` + `res/`
  are multi-file bundles — a single-file picker cannot express a dictionary).
- The shell enumerates the tree with `DocumentsContract` and copies the subtree
  into an app-owned directory, preserving relative layout so sibling
  resolution keeps working unchanged.
- `DICT_DIR` (seeded in `ServerProcess.seedConfig`) points at that directory.
- Optional, and the reason storage does not double: after `/api/ingest`
  completes with media packed, offer "delete imported source" (or delete the
  originals via `DocumentsContract.deleteDocument`, making the import a genuine
  *move*). Steady-state footprint is then **smaller** than today's
  source-plus-index.

**Go changes: none.** D52's standing rule survives intact.

*Target directory:* `getExternalFilesDir(null)` (app-specific external — no
permission on any API level, may sit on an SD card via `getExternalFilesDirs()`)
rather than `getFilesDir()`, because internal flash is the scarcer volume and a
prepared corpus is gigabytes. Caveat to accept explicitly: app-specific dirs are
**wiped on uninstall**, so an export-back-out path (SAF write of the db dir)
should exist before this is the only storage. `allowBackup=false` already
prevents Auto Backup from touching it.

**Effort:** flavour split ~1 h. The importer — tree walk, recursive copy with
progress, cancel, free-space precheck, hostile-name sanitisation, resume after
process death — is ~400 lines of dependency-free Java, 1–2 days done properly.

**The one design decision it forces:** the shell has no UI, so the import has to
be triggered from the page. Three ways, in order of cleanliness:

1. **Reuse the existing scheme interception.** `MainActivity.openExternal`
   already inspects every navigation. A link to `wudict://import` is caught
   there and never leaves the WebView. No `@JavascriptInterface`, no new HTTP
   surface, no Go change. Progress reports back by `evaluateJavascript`.
2. `addJavascriptInterface` — a real Java↔page contract, more capable, more
   attack surface (the page is ours, but so is the reason to keep it one-way).
3. Shell-injected JS on `onPageFinished` — fragile against an SPA that
   re-renders; rejected.

Option 1 is the recommendation; the page needs one conditionally-shown button,
which the shell can reveal by the same channel.

### B — App-specific external dir, no importer, user drops files in

Zero permissions, zero copy, zero code. Dead on arrival as the *primary* story:
since Android 11 `Android/data/<pkg>` is not browsable by DocumentsUI or by any
third-party file manager. It survives only as the power-user/ADB path — which
is what the existing app-private fallback dir already is. Keep it; do not build
on it.

### C — fd bridge: SAF → `LocalSocket` SCM_RIGHTS → Go

Mechanically real. `LocalSocket.setFileDescriptorsForSend()` passes genuine
descriptors; the child runs under the same UID so no permission problem;
`ExternalStorageProvider` hands back seekable, mmap-able fds; Go receives them
with `ReadMsgUnix` + `os.NewFile`.

The cost is entirely in Go and it is structural: an `io/fs`-shaped indirection
(`Open` / `Stat` / `ReadDir`, plus a sibling-join primitive) threaded through
`internal/dict`, all five format packages and `internal/gomdict` — see §2 for
why a single hook cannot cover it. Worse, `gomdict` re-opens the same file by
path repeatedly *during* a lookup, so the broker must answer arbitrary opens on
demand, which means Java must pre-enumerate the tree and hold a path→URI table.
You end up having written a userspace filesystem to avoid one file copy.

**Verdict: feasible, disproportionate**, and it breaks D52's standing rule
("the Android app adds no Go code and no build tags"). It becomes cheaper only
if an `fs.FS` read path is wanted on *desktop* for its own sake — dictionaries
inside zip/tar, or network sources. If that is ever on the roadmap, revisit;
do not do it for Android alone.

### D — MediaStore

Non-media types can be written to `Downloads`/`Documents`, but an app can only
read *its own* contributions by path. Gives nothing option A does not, and
loses the folder layout. Rejected.

### E — Declare `MANAGE_EXTERNAL_STORAGE` and argue the case

Fallback, not a plan. Non-deterministic outcome, multi-week cycles, and the
argument has to be re-made at every policy revision. Worth exactly one attempt
in parallel with building A, never instead of it.

## 4. Recommended shape

Gradle product flavours on one dimension (`dist`): `foss` and `play`.

- `src/main/AndroidManifest.xml` keeps `INTERNET` and the `maxSdkVersion`-capped
  legacy pair.
- `src/foss/AndroidManifest.xml` alone adds `MANAGE_EXTERNAL_STORAGE`.
  Manifest-merger union does the rest; nothing is removed with
  `tools:node="remove"`, which is the fragile direction.
- Flavour source sets, not runtime branches: `src/foss/java/…` keeps
  `ensureStorageAccess`'s all-files dialog, `src/play/java/…` holds the SAF
  importer, both behind one small interface in `src/main`. Neither flavour
  compiles the other's code, so the Play APK cannot even reference the
  all-files intent — which is what an automated scan looks for.
- `ServerProcess.seedConfig` seeds a different `DICT_DIR` per flavour: shared
  `Dictionaries` for `foss`, the app-owned import dir for `play`.

Packaging consequences to handle when this is scheduled:

- Play wants an **AAB**: `make apk-release` needs a `bundleRelease` sibling, and
  Play App Signing means the upload key differs from today's release key.
  `useLegacyPackaging true` must be verified to survive bundling — the binary
  must still land in `nativeLibraryDir` as a real file or the exec design dies.
- 16 KB page alignment is already satisfied (D53).
- Exec-from-`nativeLibraryDir` is **not** a Play violation (Syncthing-Android
  ships this pattern); exec from the app's *data* dir is the thing Android 10+
  blocks, and the design never did that.
- Same `applicationId` across flavours with different signing keys means a user
  cannot cross-update between the GitHub/F-Droid build and the Play build. That
  is inherent, not fixable; decide whether to accept it or suffix the Play id.

## 5. Answer to "do we want to contaminate the Go code?"

No, and option A means you do not have to. The Android-specific knowledge stays
where it belongs — in the process that has a `ContentResolver`. The Go program
continues to be a desktop program that opens paths, which is the property that
made D52's packaging story work in the first place.
