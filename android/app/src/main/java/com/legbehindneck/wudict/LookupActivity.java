// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Look up a word that was selected in some OTHER app (D67).
//
// One activity behind three filters - the selection toolbar
// (ACTION_PROCESS_TEXT), the share sheet (ACTION_SEND text/plain) and
// wudict://lookup?q= (ACTION_VIEW, for other apps and automation). They differ
// only in where the string comes from; everything after that is the same, and
// the whole feature reduces to loading `…/?q=<word>` in a WebView, because
// applyURL() in web/index.html already fills the box and searches. No server
// change, no page change, no new permission: D54 holds, the pages never learn
// that Android exists.
//
// The window floats over the app the user was reading (dialog theme), so a
// definition costs them no place in the text: Back or a tap outside returns
// them to their selection.
package com.legbehindneck.wudict;

import android.app.Activity;
import android.content.Intent;
import android.content.res.Configuration;
import android.graphics.Color;
import android.graphics.Rect;
import android.net.Uri;
import android.os.Build;
import android.os.Bundle;
import android.util.DisplayMetrics;
import android.util.TypedValue;
import android.view.Gravity;
import android.view.ViewGroup;
import android.webkit.WebResourceRequest;
import android.webkit.WebView;
import android.webkit.WebViewClient;
import android.widget.FrameLayout;
import android.widget.LinearLayout;
import android.widget.TextView;
import android.widget.Toast;

public class LookupActivity extends Activity {

    // A selection can be a whole paragraph - PROCESS_TEXT hands over whatever
    // was highlighted, and a "select all" in a reader is megabytes. Two caps,
    // because they answer different questions: the first bounds the work of
    // normalising a hostile CharSequence at all, the second is what a search
    // box can meaningfully be given.
    private static final int SCAN_LIMIT = 4096;
    private static final int QUERY_LIMIT = 256;

    private static final int DIALOG_MAX_WIDTH_DP = 640;

    private FrameLayout root;
    private TextView status;
    private WebView web;

    private int winW, winH; // the popup's size in pixels; see sizeWindow

    private String query;
    private String mode;
    private String dict;
    private boolean retained;
    private volatile boolean gone;

    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);

        Intent i = getIntent();
        query = clean(text(i));
        if (query == null) {
            // Nothing survived - a selection of whitespace, or a wudict:// URI
            // with no q. A blank floating window over someone else's app would
            // be worse than saying so and getting out of the way.
            Toast.makeText(this, R.string.lookup_empty, Toast.LENGTH_SHORT).show();
            finish();
            return;
        }
        Uri data = i == null ? null : i.getData();
        mode = mode(param(data, "mode"));
        dict = clean(param(data, "dict"));

        sizeWindow();
        setFinishOnTouchOutside(true);

        root = new FrameLayout(this);
        root.setBackgroundColor(getColor(R.color.window_bg));
        status = new TextView(this);
        status.setText(getString(R.string.lookup_starting, query));
        status.setGravity(Gravity.CENTER);
        root.addView(status, new FrameLayout.LayoutParams(
                FrameLayout.LayoutParams.MATCH_PARENT,
                FrameLayout.LayoutParams.WRAP_CONTENT,
                Gravity.CENTER));
        // Explicit pixels, not MATCH_PARENT: see sizeWindow.
        setContentView(root, new ViewGroup.LayoutParams(winW, winH));

        web = new WebView(this);
        web.setBackgroundColor(getColor(R.color.window_bg));
        Shell.configure(web);
        Ime.hideOnScroll(web);
        web.setWebViewClient(new WebViewClient() {
            @Override
            public boolean shouldOverrideUrlLoading(WebView view, WebResourceRequest req) {
                return Shell.openExternal(LookupActivity.this, req.getUrl());
            }
        });
        web.setWebChromeClient(Shell.windows(this));
        WebView.setWebContentsDebuggingEnabled(BuildConfig.DEBUG);

        // Deliberately NOT Storage.ensureAccess: this window opened over
        // another app because the user selected a word there, and answering
        // that with a permission dialog would be an ambush. Whether the app
        // can reach any dictionaries is settled in the app itself.
        retained = true;
        ServerProcess.retain();
        ServerProcess.ensure(this, new ServerProcess.Listener() {
            @Override public void onReady() { showPage(); }
            @Override public void onFailed(String message) { showFailure(message); }
        });
    }

    // ── the string ───────────────────────────────────────────────────────

    /** The selection, wherever this launch put it. */
    private static CharSequence text(Intent i) {
        if (i == null) return null;
        CharSequence cs = i.getCharSequenceExtra(Intent.EXTRA_PROCESS_TEXT);
        // The read-only variant is what a non-editable view sends - a web page,
        // a reader, a received message: the majority of real lookups.
        if (cs == null) cs = i.getCharSequenceExtra(Intent.EXTRA_PROCESS_TEXT_READONLY);
        if (cs == null) cs = i.getCharSequenceExtra(Intent.EXTRA_TEXT); // ACTION_SEND
        if (cs == null) cs = param(i.getData(), "q");
        return cs;
    }

    /**
     * A query parameter from a URI that another app controls entirely.
     * getQueryParameter throws on an opaque URI (`wudict:lookup?q=x`, no `//`),
     * which is a shape a hand-written intent can easily have.
     */
    private static String param(Uri uri, String key) {
        if (uri == null) return null;
        try {
            return uri.getQueryParameter(key);
        } catch (UnsupportedOperationException opaque) {
            String rest = uri.getEncodedSchemeSpecificPart();
            int q = rest == null ? -1 : rest.indexOf('?');
            if (q < 0) return null;
            try {
                return Uri.parse("wudict://x?" + rest.substring(q + 1)).getQueryParameter(key);
            } catch (RuntimeException e) {
                return null;
            }
        }
    }

    /** Whitespace collapsed, bounded, or null if nothing usable is left. */
    private static String clean(CharSequence cs) {
        if (cs == null) return null;
        String s = cs.length() > SCAN_LIMIT
                ? cs.subSequence(0, SCAN_LIMIT).toString()
                : cs.toString();
        // A selection spanning a line break arrives with the break in it; the
        // search box wants one line, and the page trims for itself anyway.
        s = s.replaceAll("\\s+", " ").trim();
        if (s.length() > QUERY_LIMIT) {
            int end = QUERY_LIMIT;
            // Never cut between a surrogate pair - half a code point is not text.
            if (Character.isHighSurrogate(s.charAt(end - 1))) end--;
            s = s.substring(0, end).trim();
        }
        return s.isEmpty() ? null : s;
    }

    /** The page's four modes (index.html #mode); anything else is the caller's typo. */
    private static String mode(String m) {
        if (m == null) return null;
        switch (m) {
            case "exact":
            case "prefix":
            case "contains":
            case "fts":
                return m;
            default:
                return null; // leave the page on its own default
        }
    }

    // ── the window ───────────────────────────────────────────────────────

    // A floating window is sized to its CONTENT - the decor measures the
    // content view with AT_MOST - so the size has to be stated in pixels on
    // the content view itself, not asked for with MATCH_PARENT and a
    // Window.setLayout. Doing only the latter is what produced a window the
    // height of the handoff row: the content wrapped, and the WebView, being
    // `height=0, weight=1`, was handed the excess of a parent that had none.
    // setLayout stays as well, so the window agrees with what it contains.
    //
    // Floating windows are exempt from the forced edge-to-edge that
    // MainActivity.applyWindowInsets exists for, so none of that machinery is
    // needed here; the IME is handled by windowSoftInputMode in the manifest,
    // as it is for MainActivity.
    //
    // Wide enough to read an article, never wider than a tablet's comfortable
    // column, and short enough that the app underneath is still visibly there
    // - the whole point being that the user keeps their place in it.
    private void sizeWindow() {
        Rect bounds = windowBounds();
        int max = (int) TypedValue.applyDimension(TypedValue.COMPLEX_UNIT_DIP,
                DIALOG_MAX_WIDTH_DP, getResources().getDisplayMetrics());
        winW = Math.min((int) (bounds.width() * 0.95f), max);
        winH = (int) (bounds.height() * 0.70f);
        getWindow().setLayout(winW, winH);
    }

    // The window we are actually in, which on a foldable or in split-screen is
    // not the display: getCurrentWindowMetrics answers that, and below API 30
    // the activity's own resources are the closest available.
    @SuppressWarnings("deprecation")
    private Rect windowBounds() {
        if (Build.VERSION.SDK_INT >= 30) {
            return getWindowManager().getCurrentWindowMetrics().getBounds();
        }
        DisplayMetrics dm = getResources().getDisplayMetrics();
        return new Rect(0, 0, dm.widthPixels, dm.heightPixels);
    }

    // Rotation and split-screen resizes are in configChanges, so the activity
    // is NOT recreated and nothing else would recompute a size taken once in
    // onCreate: a popup opened in portrait would stay portrait-shaped on its
    // side.
    @Override
    public void onConfigurationChanged(Configuration newConfig) {
        super.onConfigurationChanged(newConfig);
        sizeWindow();
        if (root == null) return;
        ViewGroup.LayoutParams lp = root.getLayoutParams();
        if (lp == null) return;
        lp.width = winW;
        lp.height = winH;
        root.setLayoutParams(lp);
    }

    private void showPage() {
        runOnUiThread(() -> {
            if (gone) return; // dismissed while the server was starting
            if (web.getParent() == null) {
                root.removeAllViews();
                LinearLayout col = new LinearLayout(this);
                col.setOrientation(LinearLayout.VERTICAL);
                col.addView(web, new LinearLayout.LayoutParams(
                        ViewGroup.LayoutParams.MATCH_PARENT, 0, 1f));
                col.addView(handoff(), new LinearLayout.LayoutParams(
                        ViewGroup.LayoutParams.MATCH_PARENT,
                        ViewGroup.LayoutParams.WRAP_CONTENT));
                root.addView(col, new FrameLayout.LayoutParams(
                        FrameLayout.LayoutParams.MATCH_PARENT,
                        FrameLayout.LayoutParams.MATCH_PARENT));
            }
            web.loadUrl(Shell.searchUrl(query, mode, dict));
        });
    }

    /**
     * For when a quick answer turns into real reading: hand the same query to
     * the app proper and get out of the way. MainActivity is singleTask, so
     * this reaches the existing instance if there is one.
     */
    private TextView handoff() {
        TextView t = new TextView(this);
        t.setText(R.string.lookup_open_app);
        t.setGravity(Gravity.CENTER_VERTICAL | Gravity.END);
        t.setTextColor(getColor(R.color.accent));
        t.setBackgroundColor(Color.TRANSPARENT);
        int pad = (int) TypedValue.applyDimension(TypedValue.COMPLEX_UNIT_DIP, 12,
                getResources().getDisplayMetrics());
        t.setPadding(pad, pad, pad, pad);
        t.setOnClickListener(v -> {
            startActivity(new Intent(this, MainActivity.class)
                    .putExtra(MainActivity.EXTRA_QUERY, query)
                    .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK | Intent.FLAG_ACTIVITY_CLEAR_TOP));
            finish();
        });
        return t;
    }

    private void showFailure(String message) {
        runOnUiThread(() -> {
            if (gone) return;
            status.setText(getString(R.string.server_failed, message));
        });
    }

    // The activity is singleTop: a second selection while the popup is up
    // arrives here rather than stacking another window.
    @Override
    protected void onNewIntent(Intent intent) {
        super.onNewIntent(intent);
        setIntent(intent);
        String q = clean(text(intent));
        if (q == null) return;
        query = q;
        Uri data = intent.getData();
        mode = mode(param(data, "mode"));
        dict = clean(param(data, "dict"));
        if (!gone && web != null && web.getParent() != null) {
            web.loadUrl(Shell.searchUrl(query, mode, dict));
        }
    }

    // ── lifecycle ────────────────────────────────────────────────────────
    // Back is left to the platform: it dismisses the popup rather than walking
    // the SPA's history. A lookup window is one answer to one selection, and
    // the user's way back to what they were reading has to be the obvious one.

    @Override
    protected void onStart() {
        super.onStart();
        Power.enter(this);
    }

    @Override
    protected void onStop() {
        super.onStop();
        Power.exit(this);
    }

    @Override
    protected void onDestroy() {
        gone = true;
        // false, always: a popup closing never stops the server (D67). It goes
        // to BACKGROUND via Power, the Go side sheds caches and threads, and
        // the next lookup from any app is instant rather than a cold start.
        if (retained) ServerProcess.release(false);
        if (web != null) {
            if (web.getParent() != null) ((ViewGroup) web.getParent()).removeView(web);
            web.destroy(); // the popup's WebView is transient, not a second resident one
        }
        super.onDestroy();
    }
}
