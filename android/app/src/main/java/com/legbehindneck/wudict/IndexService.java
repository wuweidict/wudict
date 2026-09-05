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
import android.os.IBinder;
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
 * <p>Started and stopped from {@link ServerProcess}, off the server's own
 * "@wudict busy" markers: the server is an exec'd child (D52) and the only part
 * of this app that knows whether a person is waiting on an ingest right now.
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

    // Whether an ingest a person is waiting on is in flight. Tracked here
    // rather than asked of the system because a start can legally fail (see
    // the class comment) while the work goes on regardless: this field answers
    // "is the server busy", not "is the service running", and the settings
    // screen needs the former before it offers to kill the server.
    private static volatile boolean inFlight;

    /** Whether the server is preparing a dictionary right now. */
    static boolean isBusy() {
        return inFlight;
    }

    /** Starts or stops the service. Never throws. */
    static void busy(Context ctx, boolean busy) {
        inFlight = busy;
        Context app = ctx.getApplicationContext();
        Intent i = new Intent(app, IndexService.class);
        try {
            if (busy) {
                app.startForegroundService(i);
            } else {
                app.stopService(i);
            }
        } catch (RuntimeException e) {
            // API 31+ ForegroundServiceStartNotAllowedException, and anything a
            // vendor build throws in its place. The work continues either way.
            Log.d(TAG, "index service " + (busy ? "start" : "stop") + ": " + e);
        }
    }

    @Override
    public int onStartCommand(Intent intent, int flags, int startId) {
        try {
            startInForeground();
        } catch (RuntimeException e) {
            Log.d(TAG, "index service foreground: " + e);
            stopSelf();
            return START_NOT_STICKY;
        }
        // NOT sticky: if the platform kills us anyway, the ingest died with the
        // server process, and restarting an empty service would protect
        // nothing. The next demand starts a fresh one.
        return START_NOT_STICKY;
    }

    private void startInForeground() {
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

        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
            // Declaring the type is required from API 29 and enforced from 34,
            // where an undeclared type is a crash rather than a warning.
            startForeground(NOTIFICATION, n, ServiceInfo.FOREGROUND_SERVICE_TYPE_DATA_SYNC);
        } else {
            startForeground(NOTIFICATION, n);
        }
    }

    @Override
    public void onDestroy() {
        stopForeground(STOP_FOREGROUND_REMOVE);
        super.onDestroy();
    }

    @Override
    public IBinder onBind(Intent intent) {
        return null; // started, never bound: it holds a state, it answers nothing
    }
}
