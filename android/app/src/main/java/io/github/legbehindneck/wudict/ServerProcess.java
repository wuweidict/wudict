// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Runs the wudict server binary that ships inside the APK as
// lib/arm64-v8a/libwudict.so — named like a library so the package manager
// extracts it to the filesystem (with extractNativeLibs=true) and it can be
// exec'd (D52). This is the same pattern Syncthing-Fork and InviZible Pro
// ship in production; the alternative (JNI) would force an NDK and glue code
// for no benefit to a localhost server.
//
// The binary is the same pure-Go program `make android-go` cross-compiles;
// only its environment is arranged here: HOME and TMPDIR point at
// app-private storage, the prepared library goes to internal flash, and
// dictionary folders are the shared "Dictionaries" folder plus an
// app-private fallback that needs no permission at all.
package io.github.legbehindneck.wudict;

import android.content.Context;
import android.os.Environment;
import android.util.Log;

import java.io.BufferedReader;
import java.io.File;
import java.io.IOException;
import java.io.InputStream;
import java.io.InputStreamReader;
import java.net.InetSocketAddress;
import java.net.Socket;
import java.util.Map;

class ServerProcess {

    static final String HOST = "127.0.0.1";
    // Fixed port (D52): UI prefs live in localStorage, which is keyed by
    // origin — a random port would forget them on every launch.
    static final int PORT = 6888;

    interface Listener {
        void onReady();
        void onFailed(String message);
    }

    private static final String BINARY = "libwudict.so";
    private static final String TAG = "wudict";

    private final Context app;
    private Process process;

    ServerProcess(Context app) {
        this.app = app;
    }

    void start(Listener listener) {
        Thread t = new Thread(() -> run(listener), "wudict-server");
        t.setDaemon(true);
        t.start();
    }

    private void run(Listener listener) {
        String bin = app.getApplicationInfo().nativeLibraryDir + "/" + BINARY;
        if (!new File(bin).canExecute()) {
            listener.onFailed("server binary not extracted: " + bin);
            return;
        }

        File files = app.getFilesDir();
        File dbDir = new File(files, "db");                  // prepared library: internal flash
        File appDicts = new File(files, "Dictionaries");     // zero-permission fallback
        File sharedDicts = new File(Environment.getExternalStorageDirectory(), "Dictionaries");
        dbDir.mkdirs();
        appDicts.mkdirs();
        sharedDicts.mkdirs(); // best effort: needs the storage grant on API 30+

        ProcessBuilder pb = new ProcessBuilder(bin, "serve",
                "--no-browser",                  // a WebView, not a browser tab
                "--ip", HOST,
                "--port", String.valueOf(PORT),
                "--db-dir", dbDir.getAbsolutePath(),
//                "--dict-dir", sharedDicts.getAbsolutePath(),
//                "--dict-dir", appDicts.getAbsolutePath(),
                "--use-cached");                 // list what dbDir already holds
        Map<String, String> env = pb.environment();
        env.put("HOME", files.getAbsolutePath()); // ~/.wudict/wudict.toml lands in app storage
        env.put("TMPDIR", app.getCacheDir().getAbsolutePath());
        pb.directory(files);
        pb.redirectErrorStream(true);
        try {
            process = pb.start();
        } catch (IOException e) {
            listener.onFailed(String.valueOf(e.getMessage()));
            return;
        }
        logOutput(process.getInputStream());

        if (awaitPort()) {
            listener.onReady();
        } else {
            listener.onFailed("port " + PORT + " never opened");
        }
    }

    private void logOutput(InputStream in) {
        Thread t = new Thread(() -> {
            try (BufferedReader r = new BufferedReader(new InputStreamReader(in))) {
                for (String line; (line = r.readLine()) != null; ) {
                    Log.d(TAG, line);
                }
            } catch (IOException ignored) {
                // process ended; nothing more to log
            }
        }, "wudict-log");
        t.setDaemon(true);
        t.start();
    }

    private static boolean awaitPort() {
        long deadline = System.nanoTime() + 60_000_000_000L; // 60 s: first run writes its config
        while (System.nanoTime() < deadline) {
            try (Socket s = new Socket()) {
                s.connect(new InetSocketAddress(HOST, PORT), 500);
                return true;
            } catch (IOException refused) {
                try {
                    Thread.sleep(200);
                } catch (InterruptedException e) {
                    Thread.currentThread().interrupt();
                    return false;
                }
            }
        }
        return false;
    }

    void stop() {
        Process p = process;
        if (p != null) {
            // No graceful shutdown: Java's destroy() is a hard kill. That is
            // safe here — SQLite commits are transactional, so the library
            // cannot be corrupted by it.
            p.destroy();
        }
    }
}
