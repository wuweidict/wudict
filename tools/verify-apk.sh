#!/bin/sh
# Copyright (C) 2026 glowinthedark
#
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Asserts the two properties that make the flavour split worth having (D62),
# on APKs that already exist — build them first with `make apk` / `make apk-play`.
#
#   1. the Play APK declares no permission beyond the three install-time,
#      never-prompted ones it is entitled to, and NO storage permission (that
#      is the whole point); the FOSS APK still declares All-files access (the
#      split did not leak);
#   2. the Play APK carries no listen-address control at all: the string the
#      switch needs lives in src/foss/res, so its absence here is proof the
#      flavour split (Net, D62) held and the switch cannot be drawn;
#   3. both still ship libwudict.so with extractNativeLibs=true, because the
#      server is EXEC'd from nativeLibraryDir and a compressed lib is not a
#      file (D52) — a packaging change is exactly what would break this
#      silently.
#
# Read-only. Exits non-zero on the first violation.
set -eu

SDK="${ANDROID_HOME:-${ANDROID_SDK_ROOT:-$HOME/Library/Android/sdk}}"
AAPT2=$(ls -1 "$SDK"/build-tools/*/aapt2 2>/dev/null | sort -V | tail -1 || true)
[ -n "$AAPT2" ] || { echo "error: no aapt2 under $SDK/build-tools"; exit 2; }

OUT=android/app/build/outputs/apk
# EVERY apk of each flavour, not the "latest" one. Picking one by listing order
# checked a stale release build while the debug build just made was ignored —
# exactly the case this script exists to catch. Every artifact on disk is one
# somebody could install, so every one is checked; a failure naming an old path
# means "rebuild or delete that", not "the assertion is wrong". Unquoted below
# on purpose: these are build-output paths, which contain no spaces.
FOSS=$(ls -1 "$OUT"/foss/debug/*.apk "$OUT"/foss/release/*.apk 2>/dev/null || true)
PLAY=$(ls -1 "$OUT"/play/debug/*.apk "$OUT"/play/release/*.apk 2>/dev/null || true)

fail() { echo "FAIL: $1"; exit 1; }

# Storage permissions must be absent from the Play APK, in the manifest as
# merged — not merely absent from the flavour's own manifest source.
if [ -n "$PLAY" ]; then
    for apk in $PLAY; do
        perms=$("$AAPT2" dump permissions "$apk")
        # `if`, not `grep … && fail`: under set -e an AND-OR list whose left
        # side fails is exempt from the exit, so the negative case would read
        # as a pass by accident. Here the intent is on the page.
        if echo "$perms" | grep -q 'EXTERNAL_STORAGE'; then
            fail "$apk declares a storage permission: $(echo "$perms" | grep 'EXTERNAL_STORAGE')"
        fi
        if ! echo "$perms" | grep -q 'android.permission.INTERNET'; then
            fail "$apk lost INTERNET"
        fi
        # An explicit allow-set, not a list of things to be absent: the next
        # permission to creep in is by definition one nobody thought to forbid
        # — a feature added to the shell is exactly how it would arrive. Adding
        # a name here is a deliberate act; the three below are install-time and
        # never prompted, so none of them costs the user a dialog.
        #
        #   INTERNET                     the WebView reaches the loopback server
        #   FOREGROUND_SERVICE           IndexService survives the freezer and
        #   FOREGROUND_SERVICE_DATA_SYNC the LMK while preparing (API 34+ needs
        #                                the typed grant as well)
        #
        # Anchored alternation: an unanchored 'FOREGROUND_SERVICE' would also
        # wave through FOREGROUND_SERVICE_LOCATION and every other subtype.
        extra=$(echo "$perms" | sed -n "s/^uses-permission: name='\([^']*\)'.*/\1/p" \
            | grep -vE '^android\.permission\.(INTERNET|FOREGROUND_SERVICE|FOREGROUND_SERVICE_DATA_SYNC)$' || true)
        if [ -n "$extra" ]; then
            fail "$apk declares permissions outside the allowed set: $extra"
        fi
        echo "ok: $apk declares only the allowed permissions"
        # The LAN switch is FOSS-only. Its label is a foss-flavour resource,
        # so a Play APK that contains the name has had the control put back -
        # by a string moved into src/main, or by a Net.java that grew a row.
        # Asserted on the resource table rather than on the source tree,
        # because the source tree is not what gets uploaded.
        if "$AAPT2" dump resources "$apk" | grep -q 'settings_server_ip'; then
            fail "$apk carries the LAN switch resources: the flavour split leaked"
        fi
        echo "ok: $apk has no listen-address control"
    done
else
    echo "skip: no play APK built (make apk-play)"
fi

if [ -n "$FOSS" ]; then
    for apk in $FOSS; do
        if ! "$AAPT2" dump permissions "$apk" | grep -q 'MANAGE_EXTERNAL_STORAGE'; then
            fail "$apk LOST MANAGE_EXTERNAL_STORAGE"
        fi
        # The mirror of the Play assertion above: the switch must still BE
        # here. A row lost by an edit to the wrong flavour's Net.java takes
        # nothing with it - the build succeeds, the setting simply stops
        # existing - so absence has to be an error, not a silence.
        if ! "$AAPT2" dump resources "$apk" | grep -q 'settings_server_ip'; then
            fail "$apk LOST the listen-address control"
        fi
        echo "ok: $apk keeps All-files access and the LAN switch"
    done
else
    echo "skip: no foss APK built (make apk)"
fi

for apk in $FOSS $PLAY; do
    "$AAPT2" dump badging "$apk" | grep -q "native-code: 'arm64-v8a'" \
        || fail "$apk ships no arm64-v8a native code"
    # AGP 9's aapt2 prints the resolved boolean; older ones print the raw
    # typed value. Accept both rather than pin to the current toolchain.
    "$AAPT2" dump xmltree --file AndroidManifest.xml "$apk" \
        | grep -Eq 'extractNativeLibs[^=]*=(true|\(type 0x12\)0xffffffff)' \
        || fail "$apk has extractNativeLibs=false: the server could not be exec'd"
    echo "ok: $apk extracts libwudict.so"

    # Selection lookup (D67) is declared in src/main, so BOTH flavours must
    # carry it — a filter that quietly lands in one flavour's manifest is the
    # classic way this split rots. It is also the property most likely to be
    # lost silently: nothing fails to build without it, the entry simply stops
    # appearing in the selection toolbar.
    "$AAPT2" dump xmltree --file AndroidManifest.xml "$apk" \
        | grep -q 'android.intent.action.PROCESS_TEXT' \
        || fail "$apk declares no PROCESS_TEXT filter: no selection lookup"
    echo "ok: $apk offers selection lookup"

    # Cleartext HTTP is scoped to loopback by a network security config
    # (src/main/res/xml). Reverting to android:usesCleartextTraffic="true"
    # builds and runs identically on the device, so nothing but this notices.
    "$AAPT2" dump xmltree --file AndroidManifest.xml "$apk" \
        | grep -q 'networkSecurityConfig' \
        || fail "$apk declares no networkSecurityConfig: cleartext is open to every host"
    echo "ok: $apk scopes cleartext to loopback"
done
