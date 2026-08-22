#!/bin/sh
# Copyright (C) 2026 glowinthedark
#
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Print the product identity of a built binary as shell assignments, so a
# packager can be derived from the binary instead of carrying a second copy of
# the name and version that can drift from cli.ProductName / cli.Version:
#
#   eval "$(sh tools/version.sh ./wudict)"
#   WUDICT_NAME='wuDict'         # CFBundleName
#   WUDICT_VERSION='v0.9-4-gabc' # what a human is shown
#   WUDICT_NUM='0.9.0'           # what Info.plist and VERSIONINFO demand: x.y.z
#
# `wudict --version` prints "<ProductName> <Version>" on its first line. If the
# binary cannot run (cross-built for another architecture) the fallbacks below
# keep the packager working rather than failing on a cosmetic string.
#
# Used by tools/make-app.sh. The Windows installer asks the same binary the
# same question from PowerShell (tools/make-installer.ps1) rather than dragging
# a POSIX shell onto Windows to read three strings.
set -eu

bin=${1:?usage: version.sh <binary>}

line=$("$bin" --version 2>/dev/null | head -1 || true)
name=$(printf '%s\n' "$line" | awk '{print $1}')
ver=$(printf '%s\n' "$line" | awk '{print $2}')
case $name in ''|*/*|*[!A-Za-z0-9._-]*) name=wuDict ;; esac
[ -n "$ver" ] || ver=dev

# The numeric form: take the tag part of `git describe`
# (v1.2.3-4-gdeadbee-dirty -> 1.2.3) and pad to exactly three fields.
num=$(printf '%s' "$ver" | sed -e 's/^[vV]//' -e 's/^\([0-9.]*\).*/\1/')
case $num in ''|.*|*[!0-9.]*) num=0.0.0 ;; esac
num=$(printf '%s' "$num" | awk -F. '{printf "%s.%s.%s", ($1==""?0:$1), ($2==""?0:$2), ($3==""?0:$3)}')

# Single-quoted output, with embedded quotes neutralised: the values come from
# our own binary, but eval is unforgiving and this costs one sed.
q() { printf "%s='%s'\n" "$1" "$(printf '%s' "$2" | sed "s/'/'\\\\''/g")"; }
q WUDICT_NAME "$name"
q WUDICT_VERSION "$ver"
q WUDICT_NUM "$num"
