// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

// The Google Play flavour's storage policy (D62): no storage permission at
// all. Dictionaries are copied in through the Storage Access Framework and
// live in the app's own external files dir, which the server then reads as
// ordinary paths.
//
// The reason it must be a copy and not a content:// handoff: the server is an
// exec'd POSIX child (D52) with no JVM and no Binder handle, so a content://
// URI is a token it can never resolve. Bytes cross the process boundary;
// URIs do not.
//
// One class of this name exists per flavour and NEVER in src/main. The FOSS
// twin holds the All-files-access path, and this APK cannot reach it.
package com.legbehindneck.wudict;

import android.app.Activity;
import android.app.AlertDialog;
import android.content.ActivityNotFoundException;
import android.content.ClipData;
import android.content.Context;
import android.content.Intent;
import android.net.Uri;
import android.util.Log;
import android.webkit.WebView;

import java.io.File;
import java.util.ArrayList;
import java.util.List;

final class Storage {

    private static final String TAG = "wudict";

    // Arbitrary, and ours: MainActivity forwards every result here.
    private static final int REQ_TREE = 0x5AF0;

    private Storage() {
    }

    /**
     * The folders the server scans: the import target, plus "Dictionaries" on
     * any other app-specific external volume - i.e. a microSD card, which needs
     * no permission and no typed path. Nothing else is readable in this
     * flavour, which is exactly the point.
     */
    static File[] dictDirs(Context c) {
        return AppDirs.dictRoots(c);
    }

    /**
     * First run only: if nothing has been imported yet, explain and offer the
     * picker. Declining is fine - the server starts either way and reports an
     * empty library, and the import control stays available on both pages.
     *
     * "Nothing yet" means every scanned folder is empty, not just the import
     * target: a card populated over USB is already a full library and must not
     * be greeted with a first-run prompt.
     */
    static void ensureAccess(Activity a) {
        if (AppDirs.hasContent(dictDirs(a))) return;
        new AlertDialog.Builder(a)
                .setTitle(R.string.import_title)
                .setMessage(R.string.import_message)
                .setPositiveButton(R.string.import_choose, (d, w) -> pickFolder(a))
                .setNegativeButton(R.string.import_later, null)
                .show();
    }

    /**
     * The in-page trigger. MainActivity.openExternal already inspects every
     * navigation, so a link to wudict://import is caught there and never
     * leaves the WebView - no JavascriptInterface, no new HTTP surface, and
     * nothing added to the server or to the page's own source.
     */
    static boolean handleShellUri(Activity a, Uri uri) {
        if (!"wudict".equalsIgnoreCase(uri.getScheme())) return false;
        if ("import".equalsIgnoreCase(uri.getHost())) {
            pickFolder(a);
        }
        return true; // any wudict:// URL is ours: never hand it to the browser
    }

    static void onActivityResult(Activity a, int requestCode, int resultCode, Intent data) {
        if (requestCode != REQ_TREE) return;
        if (resultCode != Activity.RESULT_OK || data == null || data.getData() == null) return;
        Uri tree = data.getData();
        // Persist the grant so a later re-import of the same folder - to pick
        // up dictionaries added since - needs no second trip through the picker.
        try {
            a.getContentResolver().takePersistableUriPermission(
                    tree, Intent.FLAG_GRANT_READ_URI_PERMISSION
                            | Intent.FLAG_GRANT_WRITE_URI_PERMISSION);
        } catch (SecurityException e) {
            Log.w(TAG, "no persistable grant for " + tree, e); // the copy still works now
        }
        SafImporter.importTree(a, tree);
    }

    /**
     * Adds an import control to whichever page just loaded. Injected rather
     * than built into the web assets because those are the DESKTOP app's
     * pages: nothing in them should learn what Android is (the D54 rule,
     * applied to the DOM instead of to insets). The SPA loads once and
     * navigates by pushState, so one injection per page load is enough; the
     * id guard makes a second one harmless anyway.
     *
     * BOTH pages need it, and the setup page needs it more. A first run where
     * the user taps "Later" lands on /setup with an empty library, and its own
     * only affordance is a free-text path box - which in this flavour can name
     * nothing the app is allowed to read. Without a control there, the sole
     * way back to the picker is relaunching the app.
     *
     * Anchors are STATIC markup in both pages (the panel header; the Save
     * button), never a node the page builds after an async fetch - setup.html
     * fills its folder rows only once /api/config answers, so anchoring there
     * would be a race this has no way to win. The same rule rules out the
     * dictionary list itself, tempting as it is: loadDicts rebuilds it, and a
     * control that vanishes on the first refresh is worse than none.
     */
    static void onPageFinished(WebView web) {
        web.evaluateJavascript(IMPORT_BUTTON_JS, null);
    }

    private static final String IMPORT_BUTTON_JS =
            "(function(){"
                    + "if(document.getElementById('shellImportBtn'))return;"
                    + "var go=function(){location.href='wudict://import';};"
                    // index.html: a WORDED, full-width row at the top of the ☰
                    // panel, not an icon in .headacts. That strip is chrome -
                    // theme and close - and a user scans it for "get me out of
                    // here", so the one function without which this app is
                    // empty was the least findable thing in it. A glyph there
                    // cost nothing to miss; the row costs one line of panel and
                    // cannot be missed. It sits ABOVE .allrow so that row's
                    // border-bottom still divides header from list, and so the
                    // master switch stays adjacent to the cards it governs.
                    + "var ph=document.querySelector('#panel .panel-head');"
                    + "if(ph){"
                    + "var b=document.createElement('button');"
                    + "b.type='button';b.id='shellImportBtn';"
                    + "b.textContent='\\uD83D\\uDCE5 Import dictionaries\\u2026';"
                    // Inline, and only in terms of the page's own custom
                    // properties: the shell must not ship a palette that would
                    // have to be kept in step with the page's themes (D54).
                    + "b.style.cssText='display:block;width:100%;box-sizing:border-box;"
                    + "font:inherit;font-size:13px;text-align:left;cursor:pointer;"
                    + "background:none;border:0;color:var(--link);"
                    + "padding:var(--sp-1) var(--sp-2);margin:0 0 var(--sp-1)';"
                    + "b.addEventListener('click',go);"
                    + "var ar=ph.querySelector('.allrow');"
                    + "if(ar)ph.insertBefore(b,ar);else ph.appendChild(b);"
                    + "return;}"
                    // setup.html: a worded button before Save, which is itself
                    // disabled until a folder holds something - so on a fresh
                    // install this is the only live control on the page, and it
                    // looks like it. #rows identifies the page: #save alone is
                    // not proof of which page we are on.
                    + "var s=document.getElementById('save');"
                    + "if(!s||!document.getElementById('rows'))return;"
                    + "var i=document.createElement('button');"
                    + "i.type='button';i.id='shellImportBtn';"
                    + "i.textContent='Import dictionaries\\u2026';"
                    + "i.style.marginRight='.5em';"
                    + "i.addEventListener('click',go);"
                    + "s.parentNode.insertBefore(i,s);"
                    + "})();";

    /**
     * "Share to WuWeiDict" / "Open with" from a file manager. Multi-file
     * formats only survive this if the user selects every part (.mdx AND its
     * .mdd), which is why the folder picker is the primary route and this is
     * the convenience one.
     */
    static void onNewIntent(Activity a, Intent intent) {
        if (intent == null) return;
        String action = intent.getAction();
        if (action == null) return;
        List<Uri> docs = new ArrayList<>();
        if (Intent.ACTION_SEND.equals(action)) {
            Uri u = intent.getParcelableExtra(Intent.EXTRA_STREAM);
            if (u != null) docs.add(u);
        } else if (Intent.ACTION_SEND_MULTIPLE.equals(action)) {
            ArrayList<Uri> us = intent.getParcelableArrayListExtra(Intent.EXTRA_STREAM);
            if (us != null) {
                for (Uri u : us) {
                    if (u != null) docs.add(u);
                }
            }
        } else {
            return;
        }
        // Some senders put the payload in ClipData instead of EXTRA_STREAM.
        if (docs.isEmpty()) {
            ClipData clip = intent.getClipData();
            for (int i = 0; clip != null && i < clip.getItemCount(); i++) {
                Uri u = clip.getItemAt(i).getUri();
                if (u != null) docs.add(u);
            }
        }
        if (!docs.isEmpty()) SafImporter.importDocuments(a, docs);
    }

    private static void pickFolder(Activity a) {
        Intent i = new Intent(Intent.ACTION_OPEN_DOCUMENT_TREE)
                .addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION
                        | Intent.FLAG_GRANT_WRITE_URI_PERMISSION
                        | Intent.FLAG_GRANT_PERSISTABLE_URI_PERMISSION);
        try {
            a.startActivityForResult(i, REQ_TREE);
        } catch (ActivityNotFoundException e) {
            // A device with no documents provider at all. Nothing to fall back
            // to - say so rather than fail silently.
            Log.w(TAG, "no document picker on this device", e);
            new AlertDialog.Builder(a)
                    .setMessage(R.string.import_no_picker)
                    .setPositiveButton(android.R.string.ok, null)
                    .show();
        }
    }
}
