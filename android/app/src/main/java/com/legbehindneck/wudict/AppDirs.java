// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Where the app keeps what it owns (D62): the config, the prepared library,
// and — in the Play flavour — the imported dictionaries themselves.
//
// The answer is the app's EXTERNAL files dir, /sdcard/Android/data/<pkg>/files,
// not the internal one. Both are app-private and permission-free, but internal
// storage lives under /data/data and is unreachable on an unrooted phone: a
// user could neither read wudict.toml nor get at the db dir, on the one
// platform with no command line to reach them from. The external dir is
// reachable over USB/MTP and adb (third-party file managers are locked out of
// Android/data since Android 11, so it is better, not perfect), and it is the
// larger volume — which matters when a prepared library is gigabytes.
//
// Cost accepted: the external files dir is deleted when the app is uninstalled.
package com.legbehindneck.wudict;

import android.content.Context;
import android.os.Environment;
import android.util.Log;

import java.io.File;
import java.io.IOException;
import java.nio.file.Files;
import java.util.LinkedHashMap;
import java.util.Map;

final class AppDirs {

    private static final String TAG = "wudict";

    private AppDirs() {
    }

    // Resolved once per process: the checks below depend on the state of the
    // filesystem BEFORE the server has written anything, so re-running them
    // later would answer differently.
    private static volatile File root;

    /**
     * The app's own directory: HOME for the server child, and the parent of
     * everything else here.
     */
    static synchronized File root(Context c) {
        if (root != null) return root;
        root = resolveRoot(c);
        return root;
    }

    /** HOME for the child: the server writes ~/.wudict/wudict.toml under it. */
    static File home(Context c) {
        return root(c);
    }

    /** The prepared library (SPEC §3 folders), passed as --db-dir. */
    static File dbDir(Context c) {
        File d = new File(root(c), "db");
        d.mkdirs();
        return d;
    }

    /** The app-owned dictionary folder: the Play flavour's import target, the FOSS fallback. */
    static File appDicts(Context c) {
        File d = new File(root(c), "Dictionaries");
        d.mkdirs();
        return d;
    }

    /**
     * "Dictionaries" under every app-specific external volume that is mounted
     * and writable — the import target first, then a microSD card if the
     * device has one. All of these are permission-free on every API level, so
     * a user whose corpus outgrows internal flash can drop files on the card
     * over USB and have them found with nothing to configure and nothing to
     * type into the setup page.
     *
     * The card is READ here, not written: the importer's destination stays
     * {@link #appDicts}, because SAF hands us bytes and a single destination is
     * the only thing a cancel/resume can reason about.
     */
    static File[] dictRoots(Context c) {
        Map<String, File> out = new LinkedHashMap<>();
        add(out, appDicts(c)); // always present, always first
        File[] vols = null;
        try {
            vols = c.getExternalFilesDirs(null);
        } catch (RuntimeException e) {
            // Some OEM builds throw rather than return null on an odd volume set.
            Log.w(TAG, "cannot enumerate external volumes", e);
        }
        if (vols != null) {
            for (File vol : vols) {
                // An entry is null for a volume that is not mounted right now.
                if (vol == null) continue;
                if (!Environment.MEDIA_MOUNTED.equals(Environment.getExternalStorageState(vol))) continue;
                if (!vol.canWrite()) continue;
                File d = new File(vol, "Dictionaries");
                // Created even when empty: a folder visible over MTP is how the
                // user learns where files go on the card.
                if (!d.isDirectory() && !d.mkdirs()) continue;
                add(out, d);
            }
        }
        return out.values().toArray(new File[0]);
    }

    // Keyed by canonical path: the primary volume answers to several names on
    // most devices (/sdcard, /storage/emulated/0, /storage/self/primary), and
    // the same folder listed twice makes the server scan it twice.
    private static void add(Map<String, File> out, File d) {
        String key;
        try {
            key = d.getCanonicalPath();
        } catch (IOException e) {
            key = d.getAbsolutePath();
        }
        out.putIfAbsent(key, d);
    }

    /** Whether any of these folders holds something. */
    static boolean hasContent(File[] dirs) {
        for (File d : dirs) {
            if (hasContent(d)) return true;
        }
        return false;
    }

    // An install that predates this change keeps its internal root: its
    // prepared library is already there and may be gigabytes, so moving it is
    // not something to do behind the user's back at startup. The marker is a
    // db dir with content — the config file alone is not one, because it is
    // written on the very first launch and would pin every install forever.
    // A fresh install (or one that never prepared anything) goes external, and
    // the tiny config file follows it so an edited DICT_DIR is not lost.
    private static File resolveRoot(Context c) {
        File internal = c.getFilesDir();
        if (hasContent(new File(internal, "db"))) {
            Log.i(TAG, "keeping the legacy internal root: " + internal
                    + " (a prepared library is already there)");
            return internal;
        }
        File external = externalRoot(c);
        if (external == null) {
            Log.w(TAG, "no external files dir (storage not mounted): using " + internal);
            return internal;
        }
        migrateConfig(internal, external);
        return external;
    }

    // getExternalFilesDir creates the directory and returns null when the
    // volume is not mounted; a device with a mounted-but-unwritable volume is
    // caught by the write check rather than assumed away.
    private static File externalRoot(Context c) {
        File d = c.getExternalFilesDir(null);
        if (d == null) return null;
        if (!d.isDirectory() && !d.mkdirs()) return null;
        if (!Environment.MEDIA_MOUNTED.equals(Environment.getExternalStorageState(d))) return null;
        return d.canWrite() ? d : null;
    }

    // Moves ~/.wudict/wudict.toml from the old root to the new one. Copy and
    // delete, not rename: the two roots are different mounts, so rename fails.
    // Best effort throughout — a failure here costs the user their DICT_DIR
    // edits, not their data, and the server will seed a fresh config.
    private static void migrateConfig(File from, File to) {
        File old = new File(new File(from, ".wudict"), "wudict.toml");
        if (!old.isFile()) return;
        File dst = new File(new File(to, ".wudict"), "wudict.toml");
        if (dst.exists()) return;
        try {
            Files.createDirectories(dst.getParentFile().toPath());
            Files.copy(old.toPath(), dst.toPath());
            if (!old.delete()) {
                Log.w(TAG, "copied but could not remove the old config: " + old);
            }
            Log.i(TAG, "moved wudict.toml to " + dst);
        } catch (IOException e) {
            Log.w(TAG, "could not move " + old + " to " + dst, e);
        }
    }

    /** Whether dir exists and holds at least one entry. */
    static boolean hasContent(File dir) {
        if (!dir.isDirectory()) return false;
        String[] names = dir.list();
        return names != null && names.length > 0;
    }
}
