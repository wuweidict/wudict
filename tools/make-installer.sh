#!/bin/sh
# Copyright (C) 2026 glowinthedark
#
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Compile the Windows installer (P86/D76) with Inno Setup 6.
#
# Runs where iscc runs — Windows, or Wine with Inno installed. It is a thin
# wrapper on purpose: everything that decides what the installer DOES lives in
# packaging/windows/wudict.iss, and everything that decides what it is CALLED
# comes from the binary itself via tools/version.sh, so neither the Makefile
# nor this script carries a second copy of the product name or version.
#
# Env (all optional, set by the Makefile):
#   WIN_EXE    the wudict.exe to package  default ./wudict.exe
#   BUILD_DIR  output directory           default dist
#   ISCC       Inno Setup compiler        default: found on PATH, then the
#              two standard install locations
set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
exe=${WIN_EXE:-$root/wudict.exe}
out=${BUILD_DIR:-dist}
case $out in /*|[A-Za-z]:*) ;; *) out=$root/$out ;; esac
iss=$root/packaging/windows/wudict.iss
ico=$root/packaging/windows/wudict.ico

[ -f "$exe" ] || { echo "make-installer: no binary at $exe — build the windows/amd64 exe first" >&2; exit 1; }
[ -f "$iss" ] || { echo "make-installer: missing $iss" >&2; exit 1; }
[ -f "$ico" ] || { echo "make-installer: missing $ico — run: make icons" >&2; exit 1; }

# iscc is not on PATH by default even when Inno Setup is installed.
iscc=${ISCC:-}
if [ -z "$iscc" ]; then
	for c in iscc ISCC.exe \
		"/c/Program Files (x86)/Inno Setup 6/ISCC.exe" \
		"/c/Program Files/Inno Setup 6/ISCC.exe"; do
		if command -v "$c" >/dev/null 2>&1; then iscc=$c; break; fi
	done
fi
[ -n "$iscc" ] || {
	cat >&2 <<'MSG'
make-installer: Inno Setup 6 not found.
  Windows: winget install JRSoftware.InnoSetup   (or set ISCC=/path/to/ISCC.exe)
  Elsewhere: the installer is built by the windows job of .github/workflows/build-cgo.yml
MSG
	exit 1
}

# Identity from the binary. On a non-Windows host the exe cannot be executed,
# so version.sh falls back to its defaults and the local artifact is stamped
# 0.0.0 — harmless, because the artifact that ships is built on Windows.
eval "$(sh "$root/tools/version.sh" "$exe")"

mkdir -p "$out"
"$iscc" \
	"/DAppName=$WUDICT_NAME" \
	"/DAppVersion=$WUDICT_VERSION" \
	"/DNumVersion=$WUDICT_NUM" \
	"/DSourceExe=$exe" \
	"/DOutputDir=$out" \
	"$iss"

setup=$out/wudict-setup-$WUDICT_NUM.exe
[ -s "$setup" ] || { echo "make-installer: iscc reported success but $setup is missing" >&2; exit 1; }
echo "built $setup"
echo "  name    $WUDICT_NAME $WUDICT_VERSION (version $WUDICT_NUM)"
echo "  install per-user, no admin prompt: %LOCALAPPDATA%\\Programs\\$WUDICT_NAME"
