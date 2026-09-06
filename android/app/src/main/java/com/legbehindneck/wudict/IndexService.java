// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package com.legbehindneck.wudict;

import android.app.Notification;
import android.app.NotificationChannel;
import android.app.NotificationManager;
import android.app.PendingIntent;
import android.app.Service;
import android.content.Context;
import android.content.Intent;
import android.content.pm.ServiceInfo;
import android.os.Build;
import android.os.Handler;
import android.os.IBinder;
import android.os.Looper;
import android.util.Log;

/**
 * Keeps the app alive while it is preparing a dictionary the user asked for.
 *
 * <p>This is the only Android mechanism in this app that changes the outcome of
 * a long ingest, and it is worth being precise about why the obvious ones do
 * not. Measured on the device this was written for: while indexing, the process
 * held 87% of 800% CPU with 626% <em>idle</em>, already in
 * {@code /dev/cpuset/top-app} with every core available, {@code schedtune.boost}
 * at 0 on every group, and two big cores isolated by {@code core_ctl} precisely
 * because nothing was loading them. There was no contention to win, so nothing
 * in the priority family - {@code setpriority}, {@code schedtune}, ADPF - had
 * anything to give. {@code setSustainedPerformanceMode} would have made it
 * worse: it <em>caps</em> clocks, being a thermal guarantee rather than a boost.
 *
 * <p>What actually kills a ten-minute ingest is the platform deciding the app
 * is idle: the freezer, and the low-memory killer, both of which read a
 * backgrounded app as a candidate. A {@code dataSync} foreground service is the
 * documented way to say "there is work in flight here", it works on every API
 * level this app supports, and it costs a notification the user can see and
 * dismiss the work from. That is the whole intervention.
 *
 * <p>From API 33 that notification is posted only if the user has allowed
 * notifications, and an app that never asks has the appop pinned to
 * {@code ignore}: the service came up, enqueued, and the system dropped every
 * one of them. The protection was never affected - it comes from the service
 * record, not the shade - but the receipt was invisible, so the ask now happens
 * at the one moment it is meaningful ({@link Notif}).
 *
 * <p>Started and stopped from {@link ServerProcess}, off the server's own
 * "@wudict busy" markers: the server is an exec'd child (D52) and the only part
 * of this app that knows whether a person is waiting on an ingest right now.
 *
 * <h2>The three rules that make this safe</h2>
 *
 * <p><b>A start is debounced.</b> The server refcounts its markers around every
 * demanded ingest, including the ones that finish instantly - selecting an
 * already-prepared dictionary emits {@code busy 1} and {@code busy 0} 36 ms
 * apart. Arming a foreground service for that buys nothing and flashes a
 * notification at the user, so a start waits {@link #DEBOUNCE_MS} and is
 * cancelled outright if the work ends first. Nothing is at risk in that window:
 * the freezer and the low-memory killer act on minutes of apparent idleness,
 * not on a third of a second.
 *
 * <p><b>A stop never races the start.</b> Calling {@code stopService} on a
 * service that was started with {@code startForegroundService} but has not yet
 * reached {@code startForeground} is not a lost notification - it is
 * {@code ActiveServices.bringDownServiceLocked} killing the process with
 * {@code ForegroundServiceDidNotStartInTimeException}, unconditionally. So a
 * stop that arrives too early is recorded rather than performed, and the
 * service completes the contract and then stops itself.
 *
 * <p><b>Nothing brings the record down before it is foreground.</b> The same
 * applies to the service's own escape hatch: if {@code startForeground} throws,
 * {@code stopSelf} is not a retreat, it is the fatal call again from the other
 * side. So a failure retries inside the platform's window and gives up quietly
 * rather than tearing itself down.
 *
 * <p>Every call is fail-open. From API 31 a foreground service may not be
 * started while the app is in the background, which is a state this can legally
 * be reached from - a demanded ingest outlives the screen it was started from.
 * The throw is caught and the ingest simply runs unprotected, as it did before
 * this class existed.
 */
public final class IndexService extends Service {

    private static final String TAG = "wudict";
    private static final String CHANNEL = "wudict.index";
    private static final int NOTIFICATION = 1;

    /** How long work must last before it is worth a foreground service. */
    private static final long DEBOUNCE_MS = 400;

    /** Retries of a refused startForeground, and the gap between them. */
    private static final int RETRIES = 4;
    private static final long RETRY_MS = 700;

    private static final Object LOCK = new Object();
    private static final Handler HANDLER = new Handler(Looper.getMainLooper());

    // Whether work a person is waiting on is in flight, from either of the two
    // sources that have any. Tracked here rather than asked of the system
    // because a start can legally fail (see the class comment) while the work
    // goes on regardless.
    //
    // The two are kept apart because they are cleared differently and answer
    // different questions. `serverBusy` is a STATE the server publishes and the
    // shell resets outright when the child dies (ServerProcess), so it can
    // never be a counter. `holds` is balanced acquire/release taken by work
    // inside this process - an import copying gigabytes through the
    // ContentResolver before the server has ever seen the files. The service
    // arms on either; isBusy() reports only the first, because its caller is
    // asking whether the SERVER is busy before offering to kill it, and a file
    // copy is not an answer to that question.
    private static volatile boolean serverBusy;
    private static int holds;
    private static volatile boolean inFlight;

    // The service's own state, all of it under LOCK. `pendingStart` is a
    // debounce posted and not yet fired; `started` is a startForegroundService
    // the system accepted; `foreground` is startForeground having returned,
    // which is the only state in which stopService is survivable; `stopWanted`
    // is a stop that arrived before that and has to be honoured by the service.
    private static Runnable pendingStart;
    private static boolean started;
    private static boolean foreground;
    private static boolean stopWanted;

    /** Whether the server is preparing a dictionary right now. */
    static boolean isBusy() {
        return serverBusy;
    }

    /** The server's marker: a state, set and cleared, never counted. Never throws. */
    static void busy(Context ctx, boolean busy) {
        Context app = ctx.getApplicationContext();
        boolean up;
        synchronized (LOCK) {
            serverBusy = busy;
            up = recompute();
        }
        apply(app, up);
    }

    /**
     * Claims the service for long work running inside this process, which the
     * server's markers cannot cover because the server is not doing it: an
     * import copies its gigabytes here, before the files exist anywhere the
     * server can see them, and that copy is the phase most likely to be killed.
     * Balanced by {@link #release}, and safe to nest with a concurrent ingest.
     */
    static void hold(Context ctx) {
        Context app = ctx.getApplicationContext();
        boolean up;
        synchronized (LOCK) {
            holds++;
            up = recompute();
        }
        apply(app, up);
    }

    /** Releases a {@link #hold}. Must be reached on every path, including throws. */
    static void release(Context ctx) {
        Context app = ctx.getApplicationContext();
        boolean up;
        synchronized (LOCK) {
            if (holds > 0) holds--;
            up = recompute();
        }
        apply(app, up);
    }

    /** LOCK held. Republishes the combined state and returns it. */
    private static boolean recompute() {
        inFlight = serverBusy || holds > 0;
        return inFlight;
    }

    private static void apply(Context app, boolean up) {
        if (up) {
            arm(app);
        } else {
            disarm(app);
        }
    }

    private static void arm(Context app) {
        Runnable task;
        synchronized (LOCK) {
            stopWanted = false;
            if (pendingStart != null || started) return; // already armed or running
            // One task per arm: postDelayed identity is the Runnable, so a
            // shared instance could be cancelled by a stop belonging to an
            // older start. It is idempotent against a stop that cancelled it
            // late - it re-checks its own identity under the lock.
            final Runnable[] self = new Runnable[1];
            task = self[0] = () -> {
                boolean asking = false;
                synchronized (LOCK) {
                    if (pendingStart != self[0]) return; // superseded or cancelled
                    pendingStart = null;
                    try {
                        app.startForegroundService(new Intent(app, IndexService.class));
                        started = true;
                        asking = true;
                    } catch (RuntimeException e) {
                        // API 31+ ForegroundServiceStartNotAllowedException, and
                        // anything a vendor build throws in its place. Nothing
                        // was armed, so there is nothing to satisfy: the ingest
                        // continues unprotected, as designed.
                        Log.d(TAG, "index service start: " + e);
                    }
                }
                // Outside the lock, and only for work that outlived the
                // debounce: from API 33 the notification this service is about
                // to post is dropped unless the user has allowed notifications,
                // and this is the moment where asking means something (Notif).
                // It reaches into an activity and the system, neither of which
                // has any business running under this class's lock.
                if (asking) Notif.ask(app);
            };
            pendingStart = task;
        }
        HANDLER.postDelayed(task, DEBOUNCE_MS);
    }

    private static void disarm(Context app) {
        boolean stopNow = false;
        Runnable cancel = null;
        synchronized (LOCK) {
            if (pendingStart != null) {
                // Never started: cancel the debounce and there is nothing to
                // stop, nothing to notify, and no contract to satisfy. Only
                // this task is cancelled - a retry posted by a live service
                // still owes the system its startForeground.
                cancel = pendingStart;
                pendingStart = null;
            }
            if (cancel == null && !started) return;
            if (cancel != null) {
                HANDLER.removeCallbacks(cancel);
                return;
            }
            if (foreground) {
                started = false;
                stopNow = true;
            } else {
                // The start is in flight in the system. Stopping it here is
                // the fatal case; the service stops itself instead, as soon as
                // it has legally become a foreground service.
                stopWanted = true;
            }
        }
        if (stopNow) {
            try {
                app.stopService(new Intent(app, IndexService.class));
            } catch (RuntimeException e) {
                Log.d(TAG, "index service stop: " + e);
            }
        }
    }

    @Override
    public int onStartCommand(Intent intent, int flags, int startId) {
        attempt(0);
        // NOT sticky: if the platform kills us anyway, the ingest died with the
        // server process, and restarting an empty service would protect
        // nothing. The next demand starts a fresh one.
        return START_NOT_STICKY;
    }

    /**
     * Satisfies the {@code startForegroundService} contract, or keeps trying.
     *
     * <p>The contract is "call startForeground, whatever happens": once the
     * start has been accepted, an untyped notification is a better answer than
     * none, and a retry is a better answer than {@code stopSelf} - which is not
     * a retreat but the fatal bring-down again, this time self-inflicted.
     * Eligibility failures here are transient by nature (a background start
     * racing the app going to foreground), so the retries stay well inside the
     * platform's window and then stop making noise.
     */
    private void attempt(int n) {
        boolean up = false;
        try {
            startInForeground(true);
            up = true;
        } catch (RuntimeException e) {
            Log.d(TAG, "index service foreground: " + e);
            try {
                startInForeground(false);
                up = true;
            } catch (RuntimeException e2) {
                Log.d(TAG, "index service foreground (untyped): " + e2);
            }
        }
        if (!up) {
            if (n + 1 < RETRIES) {
                HANDLER.postDelayed(() -> {
                    // Not if the record is gone: the contract died with it,
                    // and a startForeground from a destroyed service is only
                    // another throw.
                    synchronized (LOCK) {
                        if (!started) return;
                    }
                    attempt(n + 1);
                }, RETRY_MS);
            } else {
                Log.d(TAG, "index service foreground: giving up after " + RETRIES);
            }
            return; // the record still owes startForeground: do not bring it down
        }
        boolean stop;
        synchronized (LOCK) {
            foreground = true;
            // Now legally foreground, so stopping is survivable: honour a stop
            // that arrived while this was starting, or one that never reached
            // disarm() because the work ended before the service came up.
            stop = stopWanted || !inFlight;
            stopWanted = false;
            if (stop) started = false;
        }
        if (stop) stopSelf();
    }

    private void startInForeground(boolean typed) {
        NotificationManager nm = getSystemService(NotificationManager.class);
        if (nm != null && nm.getNotificationChannel(CHANNEL) == null) {
            // LOW: no sound, no heads-up. It is a receipt for work in progress,
            // not an alert - the user started this and is watching the page.
            NotificationChannel ch = new NotificationChannel(
                    CHANNEL, getString(R.string.index_channel), NotificationManager.IMPORTANCE_LOW);
            ch.setShowBadge(false);
            nm.createNotificationChannel(ch);
        }
        Intent open = new Intent(this, MainActivity.class)
                .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK | Intent.FLAG_ACTIVITY_CLEAR_TOP);
        PendingIntent pi = PendingIntent.getActivity(this, 0, open,
                PendingIntent.FLAG_UPDATE_CURRENT | PendingIntent.FLAG_IMMUTABLE);

        Notification n = new Notification.Builder(this, CHANNEL)
                .setContentTitle(getString(R.string.index_title))
                .setContentText(getString(R.string.index_text))
                .setSmallIcon(android.R.drawable.stat_sys_download)
                .setContentIntent(pi)
                .setOngoing(true)
                .build();

        if (typed && Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
            // Declaring the type is required from API 29 and enforced from 34,
            // where an undeclared type is a crash rather than a warning.
            startForeground(NOTIFICATION, n, ServiceInfo.FOREGROUND_SERVICE_TYPE_DATA_SYNC);
        } else {
            startForeground(NOTIFICATION, n);
        }
    }

    @Override
    public void onDestroy() {
        synchronized (LOCK) {
            started = false;
            foreground = false;
            stopWanted = false;
        }
        stopForeground(STOP_FOREGROUND_REMOVE);
        super.onDestroy();
    }

    @Override
    public IBinder onBind(Intent intent) {
        return null; // started, never bound: it holds a state, it answers nothing
    }
}
