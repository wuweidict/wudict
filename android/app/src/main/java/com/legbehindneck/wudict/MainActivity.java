// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

// WuWeiDict's Android shell (D52): a WebView over the wudict server binary
// that ships inside the APK as libwudict.so. The Go program is unchanged —
// ServerProcess execs it as a child and it answers on 127.0.0.1:6888.
package com.legbehindneck.wudict;

import android.app.Activity;
import android.content.Intent;
import android.content.res.Configuration;
import android.graphics.Insets;
import android.os.Build;
import android.os.Bundle;
import android.os.PowerManager;
import android.view.Gravity;
import android.view.View;
import android.view.WindowInsets;
import android.view.WindowInsetsController;
import android.webkit.WebResourceRequest;
import android.webkit.WebView;
import android.webkit.WebViewClient;
import android.widget.FrameLayout;
import android.widget.TextView;
import android.window.OnBackInvokedCallback;
import android.window.OnBackInvokedDispatcher;

public class MainActivity extends Activity {

    /** Optional query to run on load — set by the lookup popup's handoff (D67). */
    static final String EXTRA_QUERY = "com.legbehindneck.wudict.QUERY";

    private FrameLayout root;
    private TextView status;
    private WebView web;

    private volatile boolean gone;   // onDestroy ran: late server callbacks must not touch the views
    private Object backCallback;     // OnBackInvokedCallback (API 33+), registered only while canGoBack()
    private boolean openPanelOnLoad; // arrived from "Manage space" (D63): show the panel when the page is up
    private String pendingQuery;     // arrived from the lookup popup's handoff (D67)

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
        Shell.configure(web);
        Ime.hideOnScroll(web);
        web.setWebViewClient(new ShellWebViewClient());
        web.setWebChromeClient(Shell.windows(this));
        WebView.setWebContentsDebuggingEnabled(BuildConfig.DEBUG);

        setContentView(root);
        applyWindowInsets();
        syncBarAppearance();
        // Where dictionaries come from is the one thing that differs between
        // the FOSS and Play builds (D62), and it lives entirely in Storage —
        // a class that exists once per flavour and never in this source set.
        watchThermal();
        Storage.ensureAccess(this);
        Storage.onNewIntent(this, getIntent()); // launched by a share, possibly
        openPanelOnLoad = getIntent() != null
                && getIntent().getBooleanExtra(ManageSpaceActivity.EXTRA_MANAGE, false);
        pendingQuery = getIntent() == null ? null : getIntent().getStringExtra(EXTRA_QUERY);

        ServerProcess.retain();
        ServerProcess.ensure(this, new ServerProcess.Listener() {
            @Override public void onReady() { showPage(); }
            @Override public void onFailed(String message) { showFailure(message); }
        });
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
            return Shell.openExternal(MainActivity.this, req.getUrl());
        }

        @Override
        public void doUpdateVisitedHistory(WebView view, String url, boolean isReload) {
            syncBackCallback(); // canGoBack() just changed
        }

        @Override
        public void onPageFinished(WebView view, String url) {
            // The Play flavour adds its import control here. Nothing in
            // web/index.html knows what Android is — the D54 rule (the shell
            // absorbs the platform, not the page), applied to the DOM.
            Storage.onPageFinished(view);
            if (openPanelOnLoad) {
                openPanelOnLoad = false;
                openPanel(view);
            }
        }
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

    // ── power ────────────────────────────────────────────────────────────
    // The decision itself lives in Power (D64: one place decides), because the
    // lookup popup (D67) is a second window that can be visible. What stays
    // here is the thermal subscription — the app's own window is where it is
    // worth paying for, and a popup that lives for a few seconds would learn
    // nothing from one.

    private Object thermalListener; // PowerManager.OnThermalStatusChangedListener, API 29+

    private void watchThermal() {
        if (Build.VERSION.SDK_INT < 29) return;
        PowerManager pm = getSystemService(PowerManager.class);
        if (pm == null) return;
        Power.thermal(this, pm.getCurrentThermalStatus());
        PowerManager.OnThermalStatusChangedListener l = status -> Power.thermal(this, status);
        pm.addThermalStatusListener(getMainExecutor(), l);
        thermalListener = l;
    }

    private void unwatchThermal() {
        if (Build.VERSION.SDK_INT < 29 || thermalListener == null) return;
        PowerManager pm = getSystemService(PowerManager.class);
        if (pm != null) {
            pm.removeThermalStatusListener(
                    (PowerManager.OnThermalStatusChangedListener) thermalListener);
        }
        thermalListener = null;
    }

    // The platform's own verdict that memory is short, in two quite different
    // situations.
    //
    // From TRIM_MEMORY_BACKGROUND (40) upwards the app is cached and the next
    // step is being killed, so drop everything and leave it dropped: onStart
    // will restore the state when the user comes back.
    //
    // TRIM_MEMORY_RUNNING_CRITICAL (15) is the opposite case — the app is
    // VISIBLE and the whole device is short. It is the only such signal we
    // ever get: the server's own heap-pressure handling measures our heap
    // against our ceiling, which says nothing about a shortage caused by
    // everything else on the phone. So it is obeyed, and then undone on a
    // timer, because nothing else would: no lifecycle callback is coming while
    // the user simply keeps reading, and a restricted state that never lifts
    // would leave the app single-threaded for the rest of the session. The
    // intermediate running levels (LOW, MODERATE) are advisory and are left
    // alone — shedding there would fight the user's actual work.
    //
    // Recent platform versions have narrowed which of these levels an app
    // targeting a modern API still receives, so this is treated as a bonus
    // rather than a mechanism: onStop already covers going away.
    private static final long TRIM_RECOVERY_MS = 30_000;

    @Override
    @SuppressWarnings("deprecation")
    public void onTrimMemory(int level) {
        super.onTrimMemory(level);
        if (level >= TRIM_MEMORY_BACKGROUND) {
            PowerSignal.set(PowerSignal.RESTRICTED);
        } else if (level == TRIM_MEMORY_RUNNING_CRITICAL && web != null) {
            // equality, not >=: TRIM_MEMORY_UI_HIDDEN (20) also sits below
            // BACKGROUND and means the app went away, which onStop already
            // said better.
            PowerSignal.set(PowerSignal.RESTRICTED);
            web.postDelayed(() -> Power.apply(this), TRIM_RECOVERY_MS);
        }
    }

    // ── lifecycle ────────────────────────────────────────────────────────

    @Override
    protected void onStart() {
        super.onStart();
        Power.enter(this);
    }

    @Override
    protected void onStop() {
        super.onStop();
        // Sent now, while the process is still running: a cached app is frozen
        // by the platform shortly after this, and the child freezes with it.
        Power.exit(this);
    }

    private void showPage() {
        runOnUiThread(() -> {
            if (gone) return; // the activity died while the server was starting
            if (web.getParent() == null) {
                root.removeAllViews();
                root.addView(web, new FrameLayout.LayoutParams(
                        FrameLayout.LayoutParams.MATCH_PARENT,
                        FrameLayout.LayoutParams.MATCH_PARENT));
            }
            String q = pendingQuery;
            pendingQuery = null;
            web.loadUrl(q == null ? Shell.PAGE_URL : Shell.searchUrl(q, null, null));
        });
    }

    private void showFailure(String message) {
        runOnUiThread(() -> {
            if (gone) return;
            status.setText(getString(R.string.server_failed, message));
        });
    }

    /** Re-loads the page after the library behind it changed (an import). */
    void reloadPage() {
        if (!gone && web.getParent() != null) web.reload();
    }

    // The activity is singleTask, so a share arriving while it is already up
    // comes here rather than through onCreate.
    @Override
    protected void onNewIntent(Intent intent) {
        super.onNewIntent(intent);
        setIntent(intent);
        Storage.onNewIntent(this, intent);
        String q = intent == null ? null : intent.getStringExtra(EXTRA_QUERY);
        if (q != null) {
            // Handed over by the popup, so the app is very likely already up
            // and showing something else; if it is not, showPage takes it.
            if (!gone && web.getParent() != null) web.loadUrl(Shell.searchUrl(q, null, null));
            else pendingQuery = q;
        }
        if (intent != null && intent.getBooleanExtra(ManageSpaceActivity.EXTRA_MANAGE, false)) {
            // The page is already loaded in the common case (the app was in the
            // background), so act now; if it is not, onPageFinished will.
            if (!gone && web.getParent() != null) openPanel(web);
            else openPanelOnLoad = true;
        }
    }

    /**
     * Opens the ☰ panel — the screen that lists dictionaries with their sizes
     * and, on this platform, can remove them (D63). Driven by clicking the
     * page's own control rather than by a wudict:// URL or an added API,
     * because the panel's opener already does everything that must happen
     * (loads the folder section, refreshes the config): D54, the shell absorbs
     * the platform and the page is untouched.
     */
    private void openPanel(WebView view) {
        view.evaluateJavascript(
                "(function(){var b=document.getElementById('panelBtn');if(b)b.click();})()", null);
    }

    @Override
    @SuppressWarnings("deprecation") // startActivityForResult: no androidx here, by design
    protected void onActivityResult(int requestCode, int resultCode, Intent data) {
        super.onActivityResult(requestCode, resultCode, data);
        Storage.onActivityResult(this, requestCode, resultCode, data);
    }

    @Override
    protected void onDestroy() {
        gone = true;
        unwatchThermal();
        // The server is bound to the app's lifetime (D52): finishing kills
        // it; swiping the task away kills the process group, which includes
        // the child. Recreation keeps it — and so does a lookup popup that is
        // still up, which is why the decision is ServerProcess's and not this
        // activity's (D67).
        ServerProcess.release(isFinishing());
        if (web.getParent() != null) {
            ((FrameLayout) web.getParent()).removeView(web);
        }
        web.destroy();
        super.onDestroy();
    }
}
