#!/usr/bin/env bash
# bench.sh OLD NEW PKG HARNESS [go test args...]
# Builds two trees (git archive, or the work tree for NEW=worktree), drops the
# harness into PKG of each, runs `go test -run ZZ`, diffs the hash= columns and
# prints timings side by side. Trees are removed on exit.
set -euo pipefail
[ $# -ge 4 ] || { sed -n 2,5p "$0"; exit 2; }
old=$1 new=$2 pkg=${3%/} harness=$4; shift 4
root=$(git rev-parse --show-toplevel)
[ -f "$harness" ] || { echo "no harness: $harness" >&2; exit 2; }
hnew=${HARNESS_NEW:-$harness}
base=${SCRATCH:-${TMPDIR:-/tmp}}
work=$(mktemp -d "$base/bench-diff.XXXXXX")
trap 'rm -rf "$work"' EXIT

mktree() { # ref dir
	mkdir -p "$2"
	if [ "$1" = worktree ]; then
		(cd "$root" && git ls-files -z -co --exclude-standard | tar --null -T - -cf -) | tar -xf - -C "$2"
	else
		git -C "$root" archive "$1" | tar -xf - -C "$2"
	fi
}
mktree "$old" "$work/old"
mktree "$new" "$work/new"
cp "$harness" "$work/old/$pkg/zz_bench_test.go"
cp "$hnew" "$work/new/$pkg/zz_bench_test.go"

run() { # side [go test args...]
	local side=$1; shift
	(cd "$work/$side" && go test -tags "${GO_TAGS:-sqlite_fts5}" -count=1 -v -run ZZ "$@" "./$pkg" 2>&1) |
		grep -E 'case=|FAIL|panic|^ok|cannot|undefined' || true
}
echo "== old ($old)"; run old "$@" | tee "$work/old.out"
echo "== new ($new)"; run new "$@" | tee "$work/new.out"

hashes() { grep -oE 'case=[^ ]+ hash=[^ ]+' "$1" | sort; }
echo "== result differential"
if ! grep -q 'case=' "$work/old.out" || ! grep -q 'case=' "$work/new.out"; then
	echo "NO case= LINES on one side: harness failed or skipped (env unset?)"; exit 1
fi
if diff <(hashes "$work/old.out") <(hashes "$work/new.out"); then echo "hashes identical"; else echo "HASHES DIFFER"; fi
echo "== timings (old | new)"
join <(grep -oE 'case=[^ ]+.*best=[^ ]+' "$work/old.out" | sed -E 's/(case=[^ ]+).*best=([^ ]+)/\1 \2/' | sort) \
     <(grep -oE 'case=[^ ]+.*best=[^ ]+' "$work/new.out" | sed -E 's/(case=[^ ]+).*best=([^ ]+)/\1 \2/' | sort) | column -t
