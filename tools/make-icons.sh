#!/bin/sh
# Copyright (C) 2026 glowinthedark
#
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Render the tray icons from the one source mark (D70: the mark is generated
# from internal/server/web/favicon.svg, never redrawn — it already exists in
# five unlinked places and this must not make a sixth).
#
# Outputs are COMMITTED, so `make build` needs no image toolchain. Run
# `make icons` by hand when the mark changes.
#
#   internal/tray/icons/tray.png           32x32 colour  — Windows, Linux
#   internal/tray/icons/tray-template.png  44x44 mono    — macOS @2x template
#
# The icons live under internal/tray/ rather than packaging/ because go:embed
# cannot reach outside its own package directory.
#
# macOS template images are drawn by the system from their ALPHA channel alone;
# colour is discarded. So the template variant drops the rounded-rect tile (a
# solid tile would render as a solid black blob in the menu bar) and keeps only
# the ink, which is what a menu-bar glyph is supposed to be.
set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
src="$root/internal/server/web/favicon.svg"
out="$root/internal/tray/icons"

command -v rsvg-convert >/dev/null 2>&1 || {
	echo "make-icons: rsvg-convert not found (brew install librsvg)" >&2
	exit 1
}
[ -f "$src" ] || { echo "make-icons: missing $src" >&2; exit 1; }

mkdir -p "$out"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

# The template variant: same geometry, no tile, ink in black. Derived from the
# source by substitution so rule (3) of the mark's geometry (even strokes on odd
# centres) survives untouched — nothing here moves a coordinate.
sed -e '/<rect /d' -e 's/stroke="#fff"/stroke="#000"/' "$src" > "$tmp/template.svg"

rsvg-convert -w 32 -h 32 -o "$out/tray.png"          "$src"
rsvg-convert -w 44 -h 44 -o "$out/tray-template.png" "$tmp/template.svg"

for f in tray.png tray-template.png; do
	[ -s "$out/$f" ] || { echo "make-icons: $f is empty" >&2; exit 1; }
	printf '%s  ' "$f"
	ls -l "$out/$f" | awk '{print $5 " bytes"}'
done

# ---- the macOS app icon (P85) -------------------------------------------
# Same mark, same rule: rendered, never redrawn. Full-bleed, keeping the tile's
# own rx=7/32 corner — Apple's squircle-with-margins grid would mean moving
# coordinates, which D70 forbids, and the tile already reads as an app icon at
# every size.
#
# iconutil is macOS-only, so a Linux contributor regenerating the tray PNGs
# gets a note rather than a failure; wudict.icns is committed, so nothing that
# builds needs this branch.
icns="$root/packaging/darwin/wudict.icns"
if ! command -v iconutil >/dev/null 2>&1; then
	echo "make-icons: iconutil not found — skipped $icns (macOS only; the committed copy is unchanged)" >&2
	exit 0
fi

set -- 16 icon_16x16 32 icon_16x16@2x 32 icon_32x32 64 icon_32x32@2x \
       128 icon_128x128 256 icon_128x128@2x 256 icon_256x256 512 icon_256x256@2x \
       512 icon_512x512 1024 icon_512x512@2x
mkdir -p "$tmp/wudict.iconset" "$(dirname "$icns")"
while [ $# -gt 0 ]; do
	rsvg-convert -w "$1" -h "$1" -o "$tmp/wudict.iconset/$2.png" "$src"
	shift 2
done

iconutil -c icns -o "$icns" "$tmp/wudict.iconset"
[ -s "$icns" ] || { echo "make-icons: wudict.icns is empty" >&2; exit 1; }
printf 'wudict.icns  '
ls -l "$icns" | awk '{print $5 " bytes"}'

# ---- the Windows app icon (P86) -----------------------------------------
# Same mark, same rule. An .ico is a directory of images; since Vista each
# entry may be a PNG verbatim, so the container is a 6-byte header plus one
# 16-byte record per size and needs no image library to assemble — python3
# (already required by nothing else here, but present on every dev machine
# that has the Xcode tools) packs it in a dozen lines.
#
# The installer uses this for the Setup icon, the Start-menu shortcut and the
# .mdx/.dsl/.slob/.bgl file type. The wudict.exe binary itself still shows
# Explorer's generic icon: embedding one needs a PE resource (.syso), which
# means committing a binary blob and a resource compiler — deliberately not
# done, see D76.
ico="$root/packaging/windows/wudict.ico"
command -v python3 >/dev/null 2>&1 || {
	echo "make-icons: python3 not found — skipped $ico (the committed copy is unchanged)" >&2
	exit 0
}

mkdir -p "$tmp/ico" "$(dirname "$ico")"
for s in 16 32 48 64 128 256; do
	rsvg-convert -w "$s" -h "$s" -o "$tmp/ico/$s.png" "$src"
done

python3 - "$tmp/ico" "$ico" <<'PY'
import struct, sys, os
srcdir, out = sys.argv[1], sys.argv[2]
sizes = [16, 32, 48, 64, 128, 256]
blobs = [open(os.path.join(srcdir, "%d.png" % s), "rb").read() for s in sizes]
# ICONDIR: reserved, type=1 (icon), count. Then one 16-byte ICONDIRENTRY each.
head = struct.pack("<HHH", 0, 1, len(sizes))
offset = len(head) + 16 * len(sizes)
entries = b""
for s, b in zip(sizes, blobs):
    # 256 is stored as 0: the field is one byte wide.
    entries += struct.pack("<BBBBHHII", s % 256, s % 256, 0, 0, 1, 32, len(b), offset)
    offset += len(b)
with open(out, "wb") as f:
    f.write(head + entries + b"".join(blobs))
PY
[ -s "$ico" ] || { echo "make-icons: wudict.ico is empty" >&2; exit 1; }
printf 'wudict.ico   '
ls -l "$ico" | awk '{print $5 " bytes"}'
