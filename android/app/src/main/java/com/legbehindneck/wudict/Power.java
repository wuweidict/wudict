// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Everything the server is allowed to know about the device's mood is decided
// here, in one function, from fields that are the only inputs (D64). One place,
// because these inputs contradict each other - the app can be visible while the
// device is hot, backgrounded while charging - and a set of independent
// callbacks each pushing its own state would make the last event win rather
// than the strictest condition.
//
// It became a class of its own with D67: the lookup popup is a SECOND window
// that can be visible, which turns MainActivity's `visible` boolean into a
// count. With only MainActivity running the computed state is what it always
// was.
//
// The battery and power-save inputs are SAMPLED here rather than watched with
// broadcast receivers: a receiver is a wakeup the app would not otherwise take,
// which is the exact cost this whole mechanism exists to avoid, and every
// transition that matters already calls apply().
package com.legbehindneck.wudict;

import android.content.Context;
import android.os.BatteryManager;
import android.os.PowerManager;

final class Power {

    // Both mutated from the main thread only (activity lifecycle, and the
    // thermal listener posted to the main executor), so no locking; volatile
    // for the reader in a delayed post.
    private static volatile int windows; // activities between onStart and onStop
    private static volatile int thermal; // PowerManager.THERMAL_STATUS_*, 0 when unknown

    private Power() {
    }

    /** A window became visible (onStart). */
    static void enter(Context c) {
        windows++;
        apply(c);
    }

    /** A window went away (onStop). */
    static void exit(Context c) {
        if (windows > 0) windows--;
        apply(c);
    }

    static void thermal(Context c, int status) {
        thermal = status;
        apply(c);
    }

    static void apply(Context c) {
        PowerManager pm = c.getSystemService(PowerManager.class);
        boolean hot = thermal >= PowerManager.THERMAL_STATUS_MODERATE;
        boolean saving = pm != null && pm.isPowerSaveMode();

        String state;
        if (hot) {
            // Thermal throttling has already begun at MODERATE. Holding caches
            // and threads through it is how an app earns a place on a vendor's
            // battery-abuse list, and the user is holding a device that is
            // getting warm.
            state = PowerSignal.RESTRICTED;
        } else if (windows == 0) {
            // Charging is the one case where staying active off-screen is
            // defensible: preparing a dictionary is the only real work this
            // app has, it is what the user is waiting for, and a plugged-in
            // device is not spending the user's battery on it.
            state = charging(c) ? PowerSignal.ACTIVE : PowerSignal.BACKGROUND;
        } else if (saving) {
            // Visible, but the user has asked the whole system to conserve.
            // Serve lookups, on one thread, holding nothing extra.
            state = PowerSignal.BACKGROUND;
        } else {
            state = PowerSignal.ACTIVE;
        }
        PowerSignal.set(state);
    }

    private static boolean charging(Context c) {
        BatteryManager bm = c.getSystemService(BatteryManager.class);
        return bm != null && bm.isCharging();
    }
}
