// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package com.legbehindneck.wudict;

import android.util.Log;

import java.io.IOException;
import java.net.HttpURLConnection;
import java.net.URL;

/**
 * Tells the server how much of the device it may use (D64).
 *
 * The server is an exec'd POSIX child with no JVM and no Binder handle (D52),
 * so it cannot see a lifecycle callback, a thermal event or a battery state —
 * every one of those is an Android API, and the shell is the only part of this
 * app that has one. So the shell watches, and forwards a single verdict over
 * the loopback API the server already serves. Nothing is added to the Go
 * program's platform knowledge: it is told a state, not a platform.
 *
 * Delivery is deliberately lossy in one direction and stubborn in the other.
 * A dropped "active" costs a moment of conservatism; a dropped "background"
 * costs battery for as long as the app stays away, so a failed send is retried
 * a few times. States coalesce: only the latest one matters, and sending an
 * obsolete one would be wrong as well as wasteful.
 */
final class PowerSignal {

    private static final String TAG = "wudict";

    static final String ACTIVE = "active";
    static final String BACKGROUND = "background";
    static final String RESTRICTED = "restricted";

    private static final int ATTEMPTS = 3;
    private static final long RETRY_MS = 800;
    // How long the sender waits for more work before ending. A parked thread is
    // a stack this app has no use for while nothing is changing.
    private static final long IDLE_MS = 30_000;

    private static final Object LOCK = new Object();
    private static String pending;   // newest state not yet delivered
    private static String delivered; // newest state the server acknowledged
    private static boolean running;  // a sender thread is alive

    private PowerSignal() {}

    /** Requests that the server move to {@code state}; returns immediately. */
    static void set(String state) {
        synchronized (LOCK) {
            if (state.equals(pending) || (pending == null && state.equals(delivered))) {
                return; // already there, or already on its way there
            }
            pending = state;
            LOCK.notifyAll();
            if (running) return;
            running = true;
        }
        Thread t = new Thread(PowerSignal::pump, "wudict-power");
        t.setDaemon(true);
        t.start();
    }

    private static void pump() {
        for (;;) {
            String state;
            synchronized (LOCK) {
                long deadline = System.currentTimeMillis() + IDLE_MS;
                while (pending == null) {
                    long left = deadline - System.currentTimeMillis();
                    if (left <= 0) {
                        running = false;
                        return;
                    }
                    try {
                        LOCK.wait(left);
                    } catch (InterruptedException e) {
                        running = false;
                        return;
                    }
                }
                state = pending;
            }
            boolean ok = false;
            for (int i = 0; i < ATTEMPTS && !ok; i++) {
                if (i > 0) {
                    synchronized (LOCK) {
                        if (!state.equals(pending)) break; // superseded: abandon this one
                        try {
                            LOCK.wait(RETRY_MS);
                        } catch (InterruptedException e) {
                            running = false;
                            return;
                        }
                        if (!state.equals(pending)) break;
                    }
                }
                ok = post(state);
            }
            synchronized (LOCK) {
                if (state.equals(pending)) {
                    pending = null;
                    if (ok) delivered = state;
                }
            }
        }
    }

    private static boolean post(String state) {
        HttpURLConnection c = null;
        try {
            c = (HttpURLConnection) new URL("http://" + ServerProcess.HOST + ":"
                    + ServerProcess.PORT + "/api/power?state=" + state).openConnection();
            c.setRequestMethod("POST");
            c.setFixedLengthStreamingMode(0);
            c.setConnectTimeout(900);
            c.setReadTimeout(900);
            int code = c.getResponseCode();
            if (code != 200) {
                Log.w(TAG, "power " + state + ": HTTP " + code);
                return false;
            }
            return true;
        } catch (IOException | RuntimeException e) {
            // The server may not be up yet, or may already be frozen behind us.
            // Neither is worth more than a debug line.
            Log.d(TAG, "power " + state + ": " + e);
            return false;
        } finally {
            if (c != null) c.disconnect();
        }
    }
}
