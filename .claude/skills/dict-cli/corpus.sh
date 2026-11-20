#!/usr/bin/env bash
# corpus.sh [wudict.toml] — print every dictionary in the configured DICT_DIR folders, one path per line.
# Reads DICT_DIR from the toml (string or array); falls back to $DICT_DIR (os.PathListSeparator-separated).
# Needs ./wudict (or WUDICT=path) built from this repo.
set -euo pipefail
W=${WUDICT:-./wudict}
[[ -x $W ]] || { echo "corpus.sh: $W not found; make build or set WUDICT" >&2; exit 2; }
toml=${1:-}
if [[ -z $toml ]]; then
  for c in "$HOME/.wudict/wudict.toml" "${XDG_CONFIG_HOME:-$HOME/.config}/wudict/wudict.toml"; do
    [[ -f $c ]] && { toml=$c; break; }
  done
fi
dirs=()
if [[ -n $toml && -f $toml ]]; then
  line=$(command grep -E '^[[:space:]]*DICT_DIR[[:space:]]*=' "$toml" | head -1 || true)
  while IFS= read -r d; do [[ -n $d ]] && dirs+=("$d"); done < <(printf '%s\n' "$line" | command grep -oE '"[^"]+"' | tr -d '"')
fi
if [[ ${#dirs[@]} -eq 0 && -n ${DICT_DIR:-} ]]; then IFS=: read -r -a dirs <<< "$DICT_DIR"; fi
[[ ${#dirs[@]} -gt 0 ]] || { echo "corpus.sh: no DICT_DIR in ${toml:-<none>} or env" >&2; exit 2; }
# One argument per folder (a quoted array: zsh would not split an unquoted $var). A nested folder lists
# its dictionaries twice; sort -u removes that. "no dictionaries found" goes to stderr.
"$W" list "${dirs[@]}" | sort -u
