#!/usr/bin/env bash
# diff.sh <before-dir> <after-dir> [fuzz%] — pixel diff of same-named PNGs (ImageMagick `magick compare`).
# Prints "<changed pixels>\t<file>" sorted by change, writes <after-dir>/diff/<file> with the changes in red.
# Exit 0 always: the numbers are for a human to read against what the change was meant to touch.
set -uo pipefail
a=${1:?before dir} b=${2:?after dir} fuzz=${3:-1}
command -v magick >/dev/null || { echo "diff.sh: ImageMagick (magick) not found" >&2; exit 2; }
mkdir -p "$b/diff"
for f in "$a"/*.png; do
  n=$(basename "$f")
  [[ -f $b/$n ]] || { printf 'missing\t%s\n' "$n"; continue; }
  px=$(magick compare -metric AE -fuzz "$fuzz%" "$f" "$b/$n" "$b/diff/$n" 2>&1 >/dev/null | awk '{print $1}')
  printf '%s\t%s\n' "${px:-error}" "$n"
done | sort -t$'\t' -k1,1 -g -r
