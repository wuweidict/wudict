#!/bin/sh
# Copyright (C) 2026 glowinthedark
#
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Build the macOS .app bundle (P85/D75).
#
# The bundle exists for one reason: it is the only launch path on macOS that
# tells the binary it was started by a person who is not looking at a terminal.
# internal/tray's darwin preflight looks for ".app/Contents/MacOS/" in
# os.Executable(), so the REAL binary must sit there — no wrapper script, no
# symlink (EvalSymlinks would resolve it back out of the bundle and the tray
# would silently refuse to start).
#
# Everything else is already wired: a bare `wudict` with no arguments serves,
# a second launch finds the port taken, opens the browser and exits, and a GUI
# launch redirects logx to ~/Library/Logs/wudict.log. This script adds no
# behaviour to the product; it only produces the directory layout and the
# Info.plist that make macOS treat the binary as an app.
#
# Env (all optional, set by the Makefile):
#   APP_BIN    binary to embed        default ./wudict
#   APP_ID     CFBundleIdentifier     default com.wuweidict.wudict
#   MACOS_MIN  LSMinimumSystemVersion default 12.0
#   BUILD_DIR  output directory       default dist
#   CODESIGN_ID  signing identity     default "-" (ad-hoc)
set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
bin=${APP_BIN:-$root/wudict}
id=${APP_ID:-com.wuweidict.wudict}
minos=${MACOS_MIN:-12.0}
out=${BUILD_DIR:-dist}
case $out in /*) ;; *) out=$root/$out ;; esac
sign=${CODESIGN_ID:--}
tmpl=$root/packaging/darwin/Info.plist.in
icns=$root/packaging/darwin/wudict.icns

[ "$(uname -s)" = Darwin ] || { echo "make-app: macOS only (needs plutil/codesign)" >&2; exit 1; }
for t in plutil codesign; do
	command -v "$t" >/dev/null 2>&1 || { echo "make-app: $t not found" >&2; exit 1; }
done
[ -f "$tmpl" ] || { echo "make-app: missing $tmpl" >&2; exit 1; }
[ -x "$bin" ]  || { echo "make-app: no binary at $bin — run: make build" >&2; exit 1; }
[ -f "$icns" ] || { echo "make-app: missing $icns — run: make icons" >&2; exit 1; }

# Identity comes from the binary itself rather than from a Makefile variable
# (tools/version.sh): `wudict --version` prints "<ProductName> <Version>", so
# the bundle cannot drift from cli.ProductName / cli.Version the way a second
# copy of the name would. The Windows installer reads the same helper.
eval "$(sh "$root/tools/version.sh" "$bin")"
name=$WUDICT_NAME
gitver=$WUDICT_VERSION
short=$WUDICT_NUM

app=$out/$name.app
contents=$app/Contents
rm -rf "$app"
mkdir -p "$contents/MacOS" "$contents/Resources"

# cp, never ln: see the EvalSymlinks note above. -p keeps the mode and the
# ad-hoc signature the Go linker already put on the arm64 binary.
cp -p "$bin" "$contents/MacOS/wudict"
chmod 755 "$contents/MacOS/wudict"
cp "$icns" "$contents/Resources/wudict.icns"
printf 'APPL????' > "$contents/PkgInfo"

sed -e '/<!-- TEMPLATE/,/^-->$/d' \
    -e "s|@NAME@|$name|g" \
    -e "s|@EXEC@|wudict|g" \
    -e "s|@ID@|$id|g" \
    -e "s|@SHORT@|$short|g" \
    -e "s|@BUILD@|$short|g" \
    -e "s|@GITVER@|$gitver|g" \
    -e "s|@ICON@|wudict|g" \
    -e "s|@MINOS@|$minos|g" \
    -e "s|@COPYRIGHT@|Copyright (C) 2026 glowinthedark. GPL-3.0-or-later.|g" \
    "$tmpl" > "$contents/Info.plist"

plutil -lint "$contents/Info.plist" >/dev/null || {
	echo "make-app: generated Info.plist is invalid" >&2
	rm -rf "$app"; exit 1
}
grep -q '@[A-Z]*@' "$contents/Info.plist" && {
	echo "make-app: unsubstituted token left in Info.plist" >&2
	rm -rf "$app"; exit 1
}

# Ad-hoc by default. Not security — an ad-hoc signature is worth nothing to
# Gatekeeper — but arm64 refuses to execute an unsigned Mach-O at all, and the
# bundle seal keeps macOS from caching a stale Info.plist across rebuilds.
# A real Developer ID goes in CODESIGN_ID; failure is a warning, because an
# unsigned bundle still runs from a local build.
if codesign --force --sign "$sign" "$app" 2>/dev/null; then
	codesign --verify --strict "$app" 2>/dev/null || echo "make-app: warning: signature did not verify" >&2
else
	echo "make-app: warning: could not sign the bundle (CODESIGN_ID=$sign)" >&2
fi

# The name is derived, so Make cannot know the path — it reads it back here.
printf '%s\n' "$app" > "$out/.app-path"

echo "built $app"
echo "  id      $id  (version $short, from $gitver)"
echo "  run     open \"$app\""
echo "  log     ~/Library/Logs/wudict.log"
echo "  install make mac-app-install"
