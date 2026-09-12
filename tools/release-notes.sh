#!/bin/sh
# Copyright (C) 2026 glowinthedark
#
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Release notes for one tag, rendered from the git history by git-cliff and
# printed on stdout. Used by `make release-notes` and by the release job in
# .github/workflows/build-cgo.yml, so what CI publishes is exactly what you
# can read locally before tagging.
#
# THIS SCRIPT NEVER FAILS. A missing git-cliff, an unresolvable range, a
# malformed config or a commit nobody could parse all end the same way:
# nothing on stdout, a note on stderr, exit 0. The caller reads "empty" as
# "fall back to GitHub's own notes" — release automation must not be held
# hostage by a changelog generator.
#
# Usage: sh tools/release-notes.sh [TAG]
#   TAG defaults to the most recent tag reachable from HEAD. It does not have
#   to exist yet: naming a tag you are about to create renders the commits
#   that will be in it.

set -u

CLIFF_VERSION=2.14.1   # pinned: a changelog must not change shape on its own

# Which tag are we writing notes for?
TAG="${1:-}"
[ -n "$TAG" ] || TAG="$(git describe --tags --abbrev=0 2>/dev/null || true)"
if [ -z "$TAG" ]; then
  echo "release-notes: no tag given and none found - nothing to render" >&2
  exit 0
fi

# The tag vocabulary for RANGE ENDPOINTS. Wider than `tag_pattern` in
# cliff.toml on purpose: cliff.toml matches stable tags only, so that a whole
# vX.Y.Z cycle renders as ONE section with each category appearing once, while
# a pre-release still has to be able to start and end a range here. A tag
# outside this vocabulary (v3.8.0-rc1, v3.8-beta, a stray `git describe`
# output) is not a release and must never become a range endpoint.
RE_ANY='^v[0-9]+\.[0-9]+(\.[0-9]+)?(-(alpha|beta|rc)\.[0-9]+)?$'
RE_STABLE='^v[0-9]+\.[0-9]+(\.[0-9]+)?$'

# Where does the range START?
#
#   a pre-release  → the previous tag of ANY kind: "what is new in this rc"
#   a final release → the previous STABLE tag: everything the rcs carried,
#                     because nobody reads three rc pages to learn what
#                     shipped. Without this, notes for v3.8.0 begin at
#                     v3.8.0-rc.2 and silently describe a fraction of it.
case "$TAG" in
  *-alpha.*|*-beta.*|*-rc.*) FROM_RE="$RE_ANY" ;;
  *)                         FROM_RE="$RE_STABLE" ;;
esac

# The endpoint is the tag itself once it exists, HEAD while it does not.
if git rev-parse -q --verify "refs/tags/$TAG" >/dev/null 2>&1; then TO="$TAG"; else TO="HEAD"; fi

# --merged "$TO", not --merged HEAD: the predecessor must be an ANCESTOR of
# the endpoint. Picking the newest tag in the repo instead would build the
# range v3.7.2..v3.7.1 when re-rendering notes for an older release - a
# backwards range, which renders nothing and looks like a config fault.
# Sorted by when the tag was made, newest first; a tag is not its own
# predecessor.
PREV="$(git tag --list --merged "$TO" --sort=-creatordate 2>/dev/null \
        | grep -E "$FROM_RE" | grep -vx "$TAG" | head -n 1)"
if [ -n "$PREV" ]; then RANGE="$PREV..$TO"; else RANGE=""; fi   # first release: whole history

# git-cliff: the one on PATH if there is one, otherwise a pinned binary
# fetched into a scratch dir. Downloading is the CI path; it is deliberately
# not cached, because a release happens a handful of times a month and a
# stale cache is a debugging surface nobody needs.
CLIFF="$(command -v git-cliff 2>/dev/null || true)"
if [ -z "$CLIFF" ]; then
  case "$(uname -s)-$(uname -m)" in
    Linux-x86_64)   SLUG=x86_64-unknown-linux-musl ;;
    Linux-aarch64)  SLUG=aarch64-unknown-linux-musl ;;
    Darwin-arm64)   SLUG=aarch64-apple-darwin ;;
    Darwin-x86_64)  SLUG=x86_64-apple-darwin ;;
    *) echo "release-notes: no git-cliff for $(uname -s)-$(uname -m) - install it to get notes" >&2; exit 0 ;;
  esac
  TMP="$(mktemp -d)"
  trap 'rm -rf "$TMP"' EXIT
  URL="https://github.com/orhun/git-cliff/releases/download/v${CLIFF_VERSION}/git-cliff-${CLIFF_VERSION}-${SLUG}.tar.gz"
  if ! curl -sSfL "$URL" 2>/dev/null | tar xz -C "$TMP" 2>/dev/null; then
    echo "release-notes: could not fetch git-cliff $CLIFF_VERSION - skipping notes" >&2
    exit 0
  fi
  CLIFF="$TMP/git-cliff-${CLIFF_VERSION}/git-cliff"
  [ -x "$CLIFF" ] || { echo "release-notes: fetched archive has no git-cliff binary - skipping notes" >&2; exit 0; }
fi

# --strip all drops the "# Changelog" header; the `## [3.8.0] - date` line is
# dropped too, because the release page already shows the tag and the date
# right above the notes.
OUT="$("$CLIFF" --config cliff.toml --tag "$TAG" --strip all $RANGE 2>/dev/null | sed '/^## \[/d')"

# An empty render is not an error and does not look like one: an empty commit
# range renders a bare "[unreleased]" heading and exits 0. Judge the OUTPUT,
# never the exit status - a release published with blank notes is the failure
# this check exists to prevent.
if ! printf '%s' "$OUT" | grep -q '^- '; then
  echo "release-notes: git-cliff produced no entries for ${RANGE:-<all history>} - skipping notes" >&2
  exit 0
fi

printf '%s\n' "$OUT"
