// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

// The Play flavour's listen policy: loopback, and nothing else exists.
//
// Not a disabled switch - an ABSENT one. There is no SERVER_IP row, so the
// settings screen has nothing to draw, ShellPrefs has no entry to emit, and
// the label and warning text are not in this APK's resources at all
// (src/foss/res/values/strings.xml). A build that reintroduced the control
// would fail to resolve the string rather than ship a hidden one.
//
// The reason is distribution, not capability. Google Play's Device and Network
// Abuse policy is applied by automated review to what an APK CAN do; an app
// that opens an unauthenticated HTTP server to the network - and on a mobile
// bearer, to a globally routable IPv6 address, because Go's wildcard listen is
// dual-stack - is the shape that review is looking for. The FOSS APK, which
// nobody reviews and whose user chose it deliberately, keeps the switch (see
// src/foss/.../Net.java for what it does and why it binds one concrete
// address rather than a wildcard).
package com.legbehindneck.wudict;

import android.content.Context;

final class Net {

    private Net() {
    }

    /** No listen-address row in this flavour. */
    static ShellPrefs.Override[] overrides() {
        return new ShellPrefs.Override[0];
    }

    /** Always loopback. Nothing in this APK can change it. */
    static String bindIp(Context c) {
        return ShellPrefs.DEFAULT_IP;
    }

    /** Never shown here: there is no row whose hint would say where. */
    static String lanAddress() {
        return null;
    }
}
