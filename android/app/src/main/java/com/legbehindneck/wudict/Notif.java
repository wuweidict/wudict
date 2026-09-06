// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package com.legbehindneck.wudict;

import android.Manifest;
import android.app.Activity;
import android.content.Context;
import android.content.SharedPreferences;
import android.content.pm.PackageManager;
import android.os.Build;
import android.util.Log;

import java.lang.ref.WeakReference;

/**
 * The one runtime permission this app asks for, asked at the one moment it
 * means something.
 *
 * <p>Until API 33 a foreground service's notification was posted because the
 * service existed. From 33 it is posted only if {@code POST_NOTIFICATIONS} is
 * granted, and an app that never declares it has the appop set to
 * {@code ignore} for its whole life: {@link IndexService} came up foreground,
 * enqueued its notification, and the system dropped it - measured on an API 37
 * emulator as {@code numEnqueuedByApp=7, numPostedByApp=0} with no
 * {@code startForeground} failure anywhere in the log. Nothing was broken; the
 * receipt was simply invisible, which is indistinguishable from "the app is
 * doing nothing" to the person who started a ten-minute import.
 *
 * <p>This does not affect whether the work SURVIVES. A foreground service
 * protects the process whether or not its notification is shown - the proc
 * state, the freezer exemption and the low-memory killer's ranking come from
 * the service record, not from the shade. The permission buys visibility, and
 * only visibility (D62).
 *
 * <h2>When it asks</h2>
 *
 * <p>Not at launch, which is where a permission prompt is noise attached to
 * nothing. The ask is made from {@link IndexService}'s debounce, after work has
 * already lasted {@code DEBOUNCE_MS} and immediately before a foreground
 * service is started for it: by then the user has tapped something that is
 * demonstrably slow, and the dialog arrives as an answer to that tap. Work that
 * finishes inside the debounce - selecting an already-prepared dictionary, 36 ms
 * - never reaches this code, so it can never prompt.
 *
 * <h2>Asked once, and fail-open throughout</h2>
 *
 * <p>Recorded after the first ask and never repeated: the platform silently
 * no-ops a re-request once the user has decided, so a second dialog would be a
 * dialog the user never sees refusing to be dismissed. A denial leaves the app
 * exactly as it shipped before this class existed - the service still runs,
 * still protects the ingest, and the user can still see and stop it from the
 * system's Active apps list.
 *
 * <p>Every path here is best-effort. No started activity (the import outlived
 * the screen it began on) means no ask and, deliberately, no record of one:
 * the next long ingest with a window on screen gets the chance instead.
 */
final class Notif {

    private static final String TAG = "wudict";

    /** Sticky record of the single ask, so a decided user is never asked twice. */
    private static final String ASKED = "notif_permission_asked";

    /** Result code, ignored: nothing here reacts to the answer. */
    private static final int REQUEST = 0x4E4F; // 'NO'

    /**
     * The started activity, if any. Main-thread only in practice - the
     * lifecycle callbacks and IndexService's debounce all run there - and
     * volatile so a stale reference can never be read from anywhere else.
     */
    private static volatile WeakReference<Activity> top = new WeakReference<>(null);

    private Notif() {
    }

    /** Called from onStart: this activity can host a permission dialog. */
    static void top(Activity a) {
        top = new WeakReference<>(a);
    }

    /**
     * Called from onStop. Compares identity rather than clearing blindly: two
     * activities overlap during a transition (the popup starting over the main
     * window), and the one going away is not always the one that arrived last.
     */
    static void gone(Activity a) {
        if (top.get() == a) top = new WeakReference<>(null);
    }

    /**
     * Asks for the notification permission if this is the first long piece of
     * work and there is a window to ask from. Never throws.
     */
    static void ask(Context app) {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.TIRAMISU) return; // posted unconditionally
        try {
            if (app.checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS)
                    == PackageManager.PERMISSION_GRANTED) {
                return;
            }
            SharedPreferences p = ShellPrefs.of(app);
            if (p.getBoolean(ASKED, false)) return;
            Activity a = top.get();
            // No window: not asked, and not recorded as asked.
            if (a == null || a.isFinishing() || a.isDestroyed()) return;
            p.edit().putBoolean(ASKED, true).apply();
            a.requestPermissions(new String[]{Manifest.permission.POST_NOTIFICATIONS}, REQUEST);
        } catch (RuntimeException e) {
            // A vendor build that refuses the request, an activity that died
            // between the checks above: the work goes on either way.
            Log.d(TAG, "notification permission: " + e);
        }
    }
}
