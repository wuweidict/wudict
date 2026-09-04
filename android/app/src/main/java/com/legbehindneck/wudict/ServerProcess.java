// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Runs the wudict server binary that ships inside the APK as
// lib/arm64-v8a/libwudict.so - named like a library so the package manager
// extracts it to the filesystem (with extractNativeLibs=true) and it can be
// exec'd (D52). This is the same pattern Syncthing-Fork and InviZible Pro
// ship in production; the alternative (JNI) would force an NDK and glue code
// for no benefit to a localhost server.
//
// The binary is the same program `make android-go` cross-compiles; only its
// environment is arranged here. HOME and the prepared library come from
// AppDirs - the app's own external files dir, so that a phone with no root
// can still reach wudict.toml and the db dir (D62). TMPDIR stays on internal
// storage. Which dictionary folders exist is the flavour's decision and
// belongs to Storage, not here.
package com.legbehindneck.wudict;

import android.content.Context;
import android.util.Log;

import java.io.BufferedReader;
import java.io.File;
import java.io.IOException;
import java.io.InputStream;
import java.io.InputStreamReader;
import java.net.HttpURLConnection;
import java.net.InetSocketAddress;
import java.net.Socket;
import java.net.URL;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.util.ArrayList;
import java.util.List;
import java.util.Map;

class ServerProcess {

    static final String HOST = "127.0.0.1";
    // Fixed port (D52): UI prefs live in localStorage, which is keyed by
    // origin - a random port would forget them on every launch.
    static final int PORT = 6888;

    interface Listener {
        void onReady();
        void onFailed(String message);
    }

    private static final String BINARY = "libwudict.so";
    private static final String TAG = "wudict";

    // ── the one child, and who is holding it ─────────────────────────────
    // One server per app process, shared across activity recreation: the child
    // holds port 6888, so a second spawn would lose the port to it.
    //
    // This used to be a `private static ServerProcess` inside MainActivity,
    // started in onCreate and read back as "non-null means ready". D67 made
    // that untrue in two ways at once: a lookup popup must work with
    // MainActivity dead, and two activities can now ask at the same time -
    // during which non-null means *starting*. So the field lives here as a
    // state machine with a waiting list, and a start in flight is joined
    // rather than duplicated.
    private static final int IDLE = 0, STARTING = 1, READY = 2, FAILED = 3;

    private static int state = IDLE;
    private static ServerProcess shared;
    private static final List<Listener> waiting = new ArrayList<>();
    private static int holders;    // live windows retaining the server
    private static int generation; // invalidates callbacks from a child we stopped

    /**
     * Runs {@code l} against a ready server, starting one if needed. A second
     * caller during STARTING queues rather than exec'ing a second child; READY
     * calls back immediately; a previous FAILED is retried, because the reason
     * (a folder that was not there yet, a port a stale child was still holding)
     * may well be gone by the next attempt.
     *
     * <p>Callbacks arrive on whichever thread settles the start - the
     * activities hop to the main thread themselves, as they must anyway for the
     * adopt path, which answers inline.
     */
    static synchronized void ensure(Context ctx, Listener l) {
        if (state == READY) {
            l.onReady();
            return;
        }
        waiting.add(l);
        if (state == STARTING) return;
        state = STARTING;
        final int gen = generation;
        shared = new ServerProcess(ctx.getApplicationContext());
        shared.start(new Listener() {
            @Override public void onReady() { settle(gen, READY, null); }
            @Override public void onFailed(String message) { settle(gen, FAILED, message); }
        });
    }

    private static void settle(int gen, int newState, String message) {
        List<Listener> pending;
        synchronized (ServerProcess.class) {
            if (gen != generation) return; // this child was stopped; nobody is waiting on it
            state = newState;
            pending = new ArrayList<>(waiting);
            waiting.clear();
        }
        // Outside the lock: a listener runs activity code, and holding the
        // class monitor across it would let a UI callback block the next
        // ensure().
        for (Listener l : pending) {
            if (newState == READY) l.onReady();
            else l.onFailed(message);
        }
    }

    /** A window that needs the server is alive. Paired with {@link #release}. */
    static synchronized void retain() {
        holders++;
    }

    /**
     * A window is gone. {@code mayStop} is the caller's answer to "is this
     * window's own reason for the server ending too?" - MainActivity passes
     * {@code isFinishing()}, the lookup popup always passes false: a popup
     * closing must never stop the server (D67), it drops to
     * {@link PowerSignal#BACKGROUND} instead, so the next lookup from any app
     * is instant. The child is killed only when the last holder says so.
     */
    static synchronized void release(boolean mayStop) {
        if (holders > 0) holders--;
        if (!mayStop || holders > 0) return;
        generation++; // any callback still in flight from this child is now stale
        if (shared != null) shared.stop();
        shared = null;
        state = IDLE;
        waiting.clear();
    }

    private final Context app;
    private Process process;
    // written by the log thread, read by the start thread when the child dies
    private volatile String lastLine;

    private ServerProcess(Context app) {
        this.app = app;
    }

    private void start(Listener listener) {
        Thread t = new Thread(() -> run(listener), "wudict-server");
        t.setDaemon(true);
        t.start();
    }

    private void run(Listener listener) {
        // A previous run's child can outlive the app process: Android kills
        // the app, but an exec'd child is reparented to init and keeps
        // running - still holding 6888, still serving the same library. Only
        // a clean finish reaches onDestroy and stop(). Spawning a second
        // server then means a bind failure and a misleading wait, when a
        // perfectly good one is already there, so adopt it instead.
        if (adoptRunningServer()) {
            Log.i(TAG, "adopted a wudict server already listening on " + PORT);
            listener.onReady();
            return;
        }

        String bin = app.getApplicationInfo().nativeLibraryDir + "/" + BINARY;
        if (!new File(bin).canExecute()) {
            listener.onFailed("server binary not extracted: " + bin);
            return;
        }

        // Where these live is AppDirs' business (D62: the app's external files
        // dir, so an unrooted phone can still reach the config and the db
        // dir), and WHICH dictionary folders exist is the flavour's (Storage).
        File home = AppDirs.home(app);
        File dbDir = AppDirs.dbDir(app);
        File[] dicts = Storage.dictDirs(app);
        seedConfig(home, dicts);

        ProcessBuilder pb = new ProcessBuilder(bin, "serve",
                "--no-browser",                  // a WebView, not a browser tab
                "--ip", HOST,
                "--port", String.valueOf(PORT),
                "--db-dir", dbDir.getAbsolutePath(),
                // deliberately NO --dict-dir: see seedConfig
                "--use-cached");                 // list what dbDir already holds
        Map<String, String> env = pb.environment();
        env.put("HOME", home.getAbsolutePath()); // ~/.wudict/wudict.toml lands in app storage
        env.put("TMPDIR", app.getCacheDir().getAbsolutePath());
        // Give freed pages back to the kernel immediately, and be seen to.
        // The Go runtime defaults to MADV_DONTNEED only when GOOS == "linux"
        // (runtime1.go, parseRuntimeDebugVars); GOOS == "android" misses that
        // branch and keeps MADV_FREE, which leaves every reclaimed page counted
        // in RSS until the kernel is short of memory. Measured on device: after
        // shedding a 464k-headword preview backend the Go heap was empty and
        // VmRSS still read 184 MB. On Android the number is the outcome - the
        // low-memory killer, the vendor "RAM hog" watchdogs and the user's own
        // battery screen all read RSS/PSS, so memory we have released but are
        // still charged for is memory we did not release. Costs a page-fault
        // per page on reuse; that is the cheaper side of the trade here.
        env.put("GODEBUG", "madvdontneed=1");
        // Ask the server to announce work a person is waiting on, so this shell
        // can hold a foreground service across it (IndexService). Opt-in per
        // process rather than a config setting: the markers are a private
        // protocol between these two processes and are noise anywhere else.
        env.put("WUDICT_BUSY_LINES", "1");
        pb.directory(home);
        pb.redirectErrorStream(true);
        try {
            process = pb.start();
        } catch (IOException e) {
            listener.onFailed(String.valueOf(e.getMessage()));
            return;
        }
        logOutput(process.getInputStream());

        if (awaitPort(process)) {
            listener.onReady();
        } else if (process.isAlive()) {
            listener.onFailed("port " + PORT + " never opened");
        } else {
            // The child is gone, so its last line of output is the diagnosis -
            // a bad --db-dir, a port already held, a permission refusal. Saying
            // "port never opened" instead would send the user hunting for a
            // network problem that does not exist.
            listener.onFailed(lastLine == null
                    ? "server exited immediately (" + process.exitValue() + ")"
                    : lastLine);
        }
    }

    // seedConfig writes the dictionary folders into ~/.wudict/wudict.toml -
    // HOME is the app's own directory (AppDirs) - instead of passing
    // --dict-dir. The distinction is not cosmetic: a flag is the
    // HIGHEST config layer, so Config.EditableInFile("DICT_DIR") would be
    // false and the ☰ panel would (correctly) refuse to change the folders,
    // on the one platform that has no command line to change them from. Coming
    // from the file, the panel owns them.
    //
    // Written once, only when nothing is there: after that the file is the
    // user's, and the server's own first-run template step finds it and leaves
    // it alone. Paths are device-generated (/storage/emulated/0/…, /data/…) so
    // they need no TOML escaping.
    //
    // Consequence worth stating, since Storage.dictDirs can now answer with a
    // microSD card's folder as well: the volumes present at FIRST launch are
    // the ones seeded. A card inserted later is not added behind the user's
    // back - doing so would mean the shell parsing and rewriting a config file
    // that belongs to the user, in Java, to re-implement what the ☰ panel and
    // /setup already do. Its folder is app-specific external storage, so it is
    // readable in every flavour with no permission: the user pastes the path
    // once, and that is the same act as any other folder they add.
    private static void seedConfig(File home, File[] dicts) {
        File cfg = new File(new File(home, ".wudict"), "wudict.toml");
        if (cfg.exists()) return;
        StringBuilder list = new StringBuilder();
        for (File d : dicts) {
            if (list.length() > 0) list.append(", ");
            list.append('"').append(d.getAbsolutePath()).append('"');
        }
        String toml = "# WuWeiDict - written by the Android shell on first launch.\n"
                + "# Priority: CLI flag > environment variable > this file > default.\n"
                + "# The app passes no --dict-dir, so the ☰ panel can edit this.\n"
                + "\n"
                + "DICT_DIR = [" + list + "]\n";
        try {
            Files.createDirectories(cfg.getParentFile().toPath());
            Files.write(cfg.toPath(), toml.getBytes(StandardCharsets.UTF_8));
        } catch (IOException e) {
            // Not fatal: the server falls back to $HOME/Dictionaries, which is
            // appPrivate - reachable, just without the shared folder.
            Log.w(TAG, "could not seed " + cfg, e);
        }
    }

    // adoptRunningServer reports whether a wudict server is already answering
    // on the port. Loopback is shared with every other app on the device, so
    // an open socket is not proof: it is only adopted if /api/config answers
    // like ours. process stays null, so stop() will not kill something this
    // instance never started.
    private static boolean adoptRunningServer() {
        HttpURLConnection c = null;
        try {
            c = (HttpURLConnection) new URL(
                    "http://" + HOST + ":" + PORT + "/api/config").openConnection();
            c.setConnectTimeout(700);
            c.setReadTimeout(700);
            if (c.getResponseCode() != 200) return false;
            StringBuilder body = new StringBuilder();
            try (BufferedReader r = new BufferedReader(
                    new InputStreamReader(c.getInputStream(), StandardCharsets.UTF_8))) {
                for (String line; (line = r.readLine()) != null; ) body.append(line);
            }
            // configInfo's own field names (internal/server/folders.go)
            return body.indexOf("\"libDir\"") >= 0 && body.indexOf("\"revealLabel\"") >= 0;
        } catch (IOException | RuntimeException e) {
            return false; // nothing listening, or not us
        } finally {
            if (c != null) c.disconnect();
        }
    }

    // The server's out-of-band markers, read off the same stream as its log
    // (WUDICT_BUSY_LINES above). "1" means an ingest a person is waiting on has
    // started, "0" that the last one finished; they are refcounted on the
    // server side, so this sees one of each and not one per dictionary.
    private static final String BUSY_ON = "@wudict busy 1";
    private static final String BUSY_OFF = "@wudict busy 0";

    private void logOutput(InputStream in) {
        Thread t = new Thread(() -> {
            try (BufferedReader r = new BufferedReader(new InputStreamReader(in))) {
                for (String line; (line = r.readLine()) != null; ) {
                    Log.d(TAG, line);
                    String marker = line.trim();
                    if (BUSY_ON.equals(marker) || BUSY_OFF.equals(marker)) {
                        IndexService.busy(app, BUSY_ON.equals(marker));
                        continue; // a marker is not a diagnosis; keep lastLine
                    }
                    if (!marker.isEmpty()) lastLine = line; // not isBlank(): API 35+
                }
            } catch (IOException ignored) {
                // process ended; nothing more to log
            }
        }, "wudict-log");
        t.setDaemon(true);
        t.start();
    }

    // Waits for the server to accept a connection. Watching the child as well
    // as the port matters: a server that dies on startup - the common failure,
    // since it is the run that creates the config and the library folders -
    // would otherwise hold the "Starting…" screen for the full minute before
    // reporting the wrong thing.
    private static boolean awaitPort(Process child) {
        long deadline = System.nanoTime() + 60_000_000_000L; // 60 s: first run writes its config
        while (System.nanoTime() < deadline) {
            try (Socket s = new Socket()) {
                s.connect(new InetSocketAddress(HOST, PORT), 500);
                return true;
            } catch (IOException refused) {
                if (!child.isAlive()) return false; // it will never open now
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

    private void stop() {
        // Whatever it was preparing died with it, so the notification and the
        // service holding the app up must go with it too. Doing this from the
        // stdout reader instead would not be enough: a killed child closes the
        // stream without ever emitting the closing marker.
        IndexService.busy(app, false);
        Process p = process;
        if (p != null) {
            // No graceful shutdown: Java's destroy() is a hard kill. That is
            // safe here - SQLite commits are transactional, so the library
            // cannot be corrupted by it.
            p.destroy();
        }
    }
}
