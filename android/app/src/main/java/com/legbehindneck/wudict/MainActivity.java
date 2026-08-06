// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

// WuWeiDict's Android shell (D52): a WebView over the wudict server binary
// that ships inside the APK as libwudict.so. The Go program is unchanged —
// ServerProcess execs it as a child and it answers on 127.0.0.1:6888.
package com.legbehindneck.wudict;

import android.Manifest;
import android.app.Activity;
import android.app.AlertDialog;
import android.content.ActivityNotFoundException;
import android.content.Intent;
import android.content.pm.PackageManager;
import android.content.res.Configuration;
import android.graphics.Insets;
import android.net.Uri;
import android.os.Build;
import android.os.Bundle;
import android.os.Environment;
import android.os.Message;
import android.provider.Settings;
import android.util.Log;
import android.view.Gravity;
import android.view.View;
import android.view.WindowInsets;
import android.view.WindowInsetsController;
import android.webkit.WebChromeClient;
import android.webkit.WebResourceRequest;
import android.webkit.WebView;
import android.webkit.WebViewClient;
import android.widget.FrameLayout;
import android.widget.TextView;
import android.window.OnBackInvokedCallback;
import android.window.OnBackInvokedDispatcher;

public class MainActivity extends Activity {

    private static final String TAG = "wudict";

    // The one origin that belongs to us. Everything else a dictionary links to
    // is somebody else's website and leaves for the browser (see openExternal).
    private static final String ORIGIN =
            "http://" + ServerProcess.HOST + ":" + ServerProcess.PORT;
    private static final String PAGE_URL = ORIGIN + "/";

    // One server per app process, shared across activity recreation: the
    // child holds port 6888, so a second spawn would lose the port to it.
    private static ServerProcess server;

    private FrameLayout root;
    private TextView status;
    private WebView web;

    private volatile boolean gone;   // onDestroy ran: late server callbacks must not touch the views
    private Object backCallback;     // OnBackInvokedCallback (API 33+), registered only while canGoBack()

    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);

        root = new FrameLayout(this);
        root.setBackgroundColor(getColor(R.color.window_bg));
        status = new TextView(this);
        status.setText(R.string.starting);
        root.addView(status, new FrameLayout.LayoutParams(
                FrameLayout.LayoutParams.WRAP_CONTENT,
                FrameLayout.LayoutParams.WRAP_CONTENT,
                Gravity.CENTER));

        web = new WebView(this);
        web.setBackgroundColor(getColor(R.color.window_bg)); // no white flash before first paint
        web.getSettings().setJavaScriptEnabled(true);            // the UI is one SPA
        web.getSettings().setDomStorageEnabled(true);            // UI prefs live in localStorage
        web.getSettings().setMediaPlaybackRequiresUserGesture(false); // dictionary audio
        // D51 hands an article's external links to the page, which opens them
        // with window.open(). A WebView has no tabs, so that call is inert
        // unless multiple windows are supported and onCreateWindow answers it.
        web.getSettings().setSupportMultipleWindows(true);
        web.setWebViewClient(new ShellWebViewClient());
        web.setWebChromeClient(new ShellWebChromeClient());
        WebView.setWebContentsDebuggingEnabled(BuildConfig.DEBUG);

        setContentView(root);
        applyWindowInsets();
        syncBarAppearance();
        ensureStorageAccess();

        if (server == null) {
            server = new ServerProcess(getApplication());
            server.start(new ServerProcess.Listener() {
                @Override public void onReady() { showPage(); }
                @Override public void onFailed(String message) { showFailure(message); }
            });
        } else {
            showPage();
        }
    }

    // ── window insets ────────────────────────────────────────────────────
    // Android 15 forces edge-to-edge on apps targeting API 35+, and Android 16
    // DISABLED the windowOptOutEdgeToEdgeEnforcement escape hatch for apps
    // targeting API 36 — which is us. So opting out is not available and the
    // window really does extend under the status bar and the gesture bar.
    //
    // The page is a website: it has a position:fixed top bar and a fullscreen
    // panel, and knows nothing of system bars. Rather than teach every
    // stylesheet about safe areas, the SHELL keeps the bars out of the way by
    // padding the root; the padding shows the window background, so the result
    // is the classic inset layout with none of the page changed.
    //
    // The IME is folded into the bottom inset because an edge-to-edge window
    // no longer gets adjustResize applied for it by the decor.
    private void applyWindowInsets() {
        root.setOnApplyWindowInsetsListener((v, insets) -> {
            if (Build.VERSION.SDK_INT >= 30) {
                Insets bars = insets.getInsets(
                        WindowInsets.Type.systemBars() | WindowInsets.Type.displayCutout());
                Insets ime = insets.getInsets(WindowInsets.Type.ime());
                v.setPadding(bars.left, bars.top, bars.right, Math.max(bars.bottom, ime.bottom));
            } else {
                legacyPadding(v, insets);
            }
            // not consumed: the WebView is welcome to see them too
            return insets;
        });
        root.requestApplyInsets();
    }

    // API 26–29: no forced edge-to-edge, so these are normally all zero — the
    // decor has already inset the content view. Kept for cutout devices on 28/29.
    @SuppressWarnings("deprecation")
    private static void legacyPadding(View v, WindowInsets insets) {
        v.setPadding(insets.getSystemWindowInsetLeft(), insets.getSystemWindowInsetTop(),
                insets.getSystemWindowInsetRight(), insets.getSystemWindowInsetBottom());
    }

    // The bars are transparent under edge-to-edge, so their icons are drawn
    // over OUR padding — they have to contrast with the window background,
    // which follows the system's day/night mode (values-night/colors.xml).
    private void syncBarAppearance() {
        boolean night = (getResources().getConfiguration().uiMode
                & Configuration.UI_MODE_NIGHT_MASK) == Configuration.UI_MODE_NIGHT_YES;
        View decor = getWindow().getDecorView();
        if (Build.VERSION.SDK_INT >= 30) {
            WindowInsetsController c = decor.getWindowInsetsController();
            if (c != null) {
                int light = WindowInsetsController.APPEARANCE_LIGHT_STATUS_BARS
                        | WindowInsetsController.APPEARANCE_LIGHT_NAVIGATION_BARS;
                c.setSystemBarsAppearance(night ? 0 : light, light);
            }
        } else {
            legacyBarAppearance(decor, night);
        }
    }

    @SuppressWarnings("deprecation")
    private static void legacyBarAppearance(View decor, boolean night) {
        int flags = decor.getSystemUiVisibility();
        int light = View.SYSTEM_UI_FLAG_LIGHT_STATUS_BAR
                | View.SYSTEM_UI_FLAG_LIGHT_NAVIGATION_BAR;
        decor.setSystemUiVisibility(night ? (flags & ~light) : (flags | light));
    }

    // ── navigation ───────────────────────────────────────────────────────

    private class ShellWebViewClient extends WebViewClient {
        @Override
        public boolean shouldOverrideUrlLoading(WebView view, WebResourceRequest req) {
            return openExternal(req.getUrl());
        }

        @Override
        public void doUpdateVisitedHistory(WebView view, String url, boolean isReload) {
            syncBackCallback(); // canGoBack() just changed
        }
    }

    private class ShellWebChromeClient extends WebChromeClient {
        @Override
        public boolean onCreateWindow(WebView view, boolean isDialog,
                                      boolean isUserGesture, Message resultMsg) {
            // window.open() has no URL in this callback — the only way to learn
            // it is to hand the transport a throwaway WebView and read the
            // navigation it is about to make.
            WebView sink = new WebView(view.getContext());
            sink.setWebViewClient(new WebViewClient() {
                @Override
                public boolean shouldOverrideUrlLoading(WebView v, WebResourceRequest req) {
                    openExternal(req.getUrl());
                    v.post(v::destroy); // never destroy a WebView inside its own callback
                    return true;
                }
            });
            ((WebView.WebViewTransport) resultMsg.obj).setWebView(sink);
            resultMsg.sendToTarget();
            return true;
        }
    }

    // openExternal reports whether it took the navigation off the WebView's
    // hands. Our own origin stays; a dictionary's link to a real website goes
    // to whatever the user browses with, because a WebView with no address
    // bar, no tabs and no back affordance is a trap to land a website in.
    private boolean openExternal(Uri uri) {
        if (uri == null) return false;
        String scheme = uri.getScheme();
        if (scheme == null) return false;
        scheme = scheme.toLowerCase();
        // The article machinery's own URLs: srcdoc frames, data: media, the
        // blob: URLs audio playback builds. None of those leave the app.
        if (scheme.equals("data") || scheme.equals("blob")
                || scheme.equals("about") || scheme.equals("javascript")) {
            return false;
        }
        String url = uri.toString();
        if (url.equals(ORIGIN) || url.startsWith(ORIGIN + "/")) return false;
        try {
            startActivity(new Intent(Intent.ACTION_VIEW, uri)
                    .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK));
        } catch (ActivityNotFoundException | SecurityException e) {
            Log.w(TAG, "no app can open " + uri, e);
        }
        return true;
    }

    // Back. Apps targeting API 35+ get predictive back enabled by default, and
    // for those onBackPressed() is NO LONGER CALLED — the plain override below
    // is dead code on any modern device, which would have made the button exit
    // the app instead of walking the SPA's history. Registering the callback
    // only while there is history to walk keeps the system's own
    // predictive-back-to-home animation for the last press.
    private void syncBackCallback() {
        if (Build.VERSION.SDK_INT < 33) return;
        boolean want = web.getParent() != null && web.canGoBack();
        if (want == (backCallback != null)) return;
        OnBackInvokedDispatcher d = getOnBackInvokedDispatcher();
        if (want) {
            OnBackInvokedCallback cb = () -> web.goBack();
            d.registerOnBackInvokedCallback(OnBackInvokedDispatcher.PRIORITY_DEFAULT, cb);
            backCallback = cb;
        } else {
            d.unregisterOnBackInvokedCallback((OnBackInvokedCallback) backCallback);
            backCallback = null;
        }
    }

    @Override
    @SuppressWarnings("deprecation")
    public void onBackPressed() { // API 26–32 only; see syncBackCallback
        if (web.getParent() != null && web.canGoBack()) {
            web.goBack(); // the SPA keeps its own history per search
        } else {
            super.onBackPressed();
        }
    }

    // ── lifecycle ────────────────────────────────────────────────────────

    private void showPage() {
        runOnUiThread(() -> {
            if (gone) return; // the activity died while the server was starting
            if (web.getParent() == null) {
                root.removeAllViews();
                root.addView(web, new FrameLayout.LayoutParams(
                        FrameLayout.LayoutParams.MATCH_PARENT,
                        FrameLayout.LayoutParams.MATCH_PARENT));
            }
            web.loadUrl(PAGE_URL);
        });
    }

    private void showFailure(String message) {
        runOnUiThread(() -> {
            if (gone) return;
            status.setText(getString(R.string.server_failed, message));
        });
    }

    // Dictionaries are read from the shared "Dictionaries" folder (D52).
    // That needs all-files access on API 30+, legacy WRITE below; either way
    // the server starts — it reports the folder as empty until files arrive.
    private void ensureStorageAccess() {
        if (Build.VERSION.SDK_INT >= 30) {
            if (!Environment.isExternalStorageManager()) {
                new AlertDialog.Builder(this)
                        .setTitle(R.string.storage_title)
                        .setMessage(R.string.storage_message)
                        .setPositiveButton(R.string.storage_grant, (dialog, which) ->
                                startActivity(new Intent(
                                        Settings.ACTION_MANAGE_APP_ALL_FILES_ACCESS_PERMISSION,
                                        Uri.parse("package:" + getPackageName()))))
                        .setNegativeButton(R.string.storage_later, null)
                        .show();
            }
        } else if (checkSelfPermission(Manifest.permission.WRITE_EXTERNAL_STORAGE)
                != PackageManager.PERMISSION_GRANTED) {
            requestPermissions(
                    new String[]{Manifest.permission.WRITE_EXTERNAL_STORAGE}, 1);
        }
    }

    @Override
    protected void onDestroy() {
        gone = true;
        // The server is bound to the app's lifetime (D52): finishing kills
        // it; swiping the task away kills the process group, which includes
        // the child. Recreation keeps it.
        if (isFinishing() && server != null) {
            server.stop();
            server = null;
        }
        if (web.getParent() != null) {
            ((FrameLayout) web.getParent()).removeView(web);
        }
        web.destroy();
        super.onDestroy();
    }
}
