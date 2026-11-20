#!/usr/bin/env bash
# sweep.sh <list> <outdir> <res-pattern>…
# For each dictionary path in <list> (one per line, e.g. from corpus.sh): extract the resources matching the
# patterns into <outdir>/<n>/ and write <outdir>/<n>/.src (the path). One TSV line per dictionary on stdout:
#   n  rc  seconds  files  path
# rc=1 with "lists no resources" / "matches no resource" in <outdir>/<n>/.log is the normal "nothing there".
# Then search with: command grep -rliE '<regex>' <outdir> | sed "s|^<outdir>/||" | cut -d/ -f1 | sort -un
set -uo pipefail
W=${WUDICT:-./wudict}
[[ $# -ge 3 ]] || { echo "usage: sweep.sh <list> <outdir> <res-pattern>…" >&2; exit 2; }
list=$1 out=$2; shift 2
n=0
while IFS= read -r f; do
  [[ -z $f ]] && continue
  n=$((n+1)); d=$out/$n; mkdir -p "$d"; printf '%s\n' "$f" > "$d/.src"
  t0=$SECONDS
  timeout 120 "$W" res -f -o "$d" "$f" "$@" > "$d/.log" 2>&1; rc=$?
  printf '%s\trc=%s\t%ss\t%s\t%s\n' "$n" "$rc" "$((SECONDS-t0))" "$(find "$d" -type f ! -name '.*' | wc -l | tr -d ' ')" "$f"
done < "$list"
