// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

// The FOSS flavour's storage policy (D62): the shared "Dictionaries" folder,
// reached with All-files access, exactly as D52 shipped it.
//
// One class of this name exists per flavour and NEVER in src/main, so the
// Play build cannot compile — or even name — the intents and permissions
// below. That is the whole point of the split: the shell calls Storage, and
// what Storage is depends on which APK is being built.
package com.legbehindneck.wudict;

import android.Manifest;
import android.app.Activity;
import android.app.AlertDialog;
import android.content.Intent;
import android.content.pm.PackageManager;
import android.net.Uri;
import android.os.Build;
import android.os.Environment;
import android.provider.Settings;
import android.webkit.WebView;

import java.io.File;

final class Storage {

    private Storage() {
    }

    /**
     * Folders seeded into DICT_DIR, in order: the shared folder the user drops
     * files into, then the app-owned one that needs no permission at all.
     */
    static File[] dictDirs(android.content.Context c) {
        File shared = new File(Environment.getExternalStorageDirectory(), "Dictionaries");
        shared.mkdirs(); // best effort: needs the storage grant on API 30+
        return new File[]{shared, AppDirs.appDicts(c)};
    }

    /**
     * Asks for the storage grant. Either way the server starts — it reports
     * the folder as empty until files arrive.
     */
    static void ensureAccess(Activity a) {
        if (Build.VERSION.SDK_INT >= 30) {
            if (!Environment.isExternalStorageManager()) {
                new AlertDialog.Builder(a)
                        .setTitle(R.string.storage_title)
                        .setMessage(R.string.storage_message)
                        .setPositiveButton(R.string.storage_grant, (dialog, which) ->
                                a.startActivity(new Intent(
                                        Settings.ACTION_MANAGE_APP_ALL_FILES_ACCESS_PERMISSION,
                                        Uri.parse("package:" + a.getPackageName()))))
                        .setNegativeButton(R.string.storage_later, null)
                        .show();
            }
        } else if (a.checkSelfPermission(Manifest.permission.WRITE_EXTERNAL_STORAGE)
                != PackageManager.PERMISSION_GRANTED) {
            a.requestPermissions(
                    new String[]{Manifest.permission.WRITE_EXTERNAL_STORAGE}, 1);
        }
    }

    /** No shell-private URLs in this flavour: the folder is reached directly. */
    static boolean handleShellUri(Activity a, Uri uri) {
        return false;
    }

    /** No import flow to receive a result for. */
    static void onActivityResult(Activity a, int requestCode, int resultCode, Intent data) {
    }

    /** Nothing to add to the page: the user manages the folder in a file manager. */
    static void onPageFinished(WebView web) {
    }

    /** No share-target filters in this flavour's manifest. */
    static void onNewIntent(Activity a, Intent intent) {
    }
}
