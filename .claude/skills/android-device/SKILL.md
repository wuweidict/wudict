---
name: android-device
description: Run or verify wudict on an Android emulator or real device over adb — install debug APKs, drive taps, inspect the WebView over CDP, run cross-compiled Go test binaries with the app's memory limits. Use for any Android-only bug, device measurement or "check on the phone" request.
argument-hint: "[<adb serial>] [install|ui|bench]"
---

**Gate:** ask the user before using an emulator or a device unless the request already says to; name the device and what will run. Always pass `adb -s <serial>`: several devices may be attached.

## App
- Release `com.legbehindneck.wudict` (not debuggable, port 6888); debug `com.legbehindneck.wudict.debug` (`applicationIdSuffix`), debuggable, port **6886** (D201), installs beside the release, starts with fresh page preferences (new origin).
- `make android-go` when Go changed, then `make apk-foss-debug` (prints the APK path last) → `adb -s <s> install -r <path>`. Redirect build output to a file and read only the failures.
- Old WebViews matter: Chromium 104 (e.g. Bromite System WebView) has no Chrome 105+ API (`caretPositionFromPoint`), and a shadow root's `Selection.isCollapsed` is wrong there. When a feature fails only on a device, check the API against its WebView version (`adb shell dumpsys webviewupdate`) first; reproduce on desktop with Playwright and an init script that deletes the API.

## UI checks
- Taps: `adb -s <s> shell input tap X Y` (a double tap: two taps in one `shell` call, < 300 ms apart). Screenshots: `adb exec-out screencap -p > <scratchpad>/x.png`.
- WebView DevTools (debug build only): `adb forward tcp:9222 localabstract:webview_devtools_remote_<pid>`, then CDP; logpoints work. Served JS has comments stripped, so its line numbers differ from the source.
- System popups (action bar, selection handles) are invisible to uiautomator: `adb shell dumpsys window`, the app's `PopupWindow`s with `isOnScreen=true`.

## Go test binaries on device
`GOOS=android GOARCH=arm64 CGO_ENABLED=0 go test -c -o <scratchpad>/t ./internal/<pkg>`, `adb push` to `/data/local/tmp/wd-<task>/`, run with `GOMEMLIMIT=248MiB GOMAXPROCS=2` (the app's limits: total/16, NumCPU/2), read peak RSS from `VmHWM` in `/proc/self/status`. Always `adb shell rm -rf /data/local/tmp/wd-<task>` afterwards.

Maintainer overlay: if `.claude/local/devices.md` exists, take the devices from it.
