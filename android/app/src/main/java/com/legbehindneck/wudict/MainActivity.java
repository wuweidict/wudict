// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

// WuWeiDict's Android shell (D52): a WebView over the wudict server binary
// that ships inside the APK as libwudict.so. The Go program is unchanged —
// ServerProcess execs it as a child and it answers on 127.0.0.1:6888.
package com.legbehindneck.wudict;

import android.app.Activity;
import android.content.ActivityNotFoundException;
import android.content.Intent;
import android.content.res.Configuration;
import android.graphics.Insets;
import android.net.Uri;
import android.os.BatteryManager;
import android.os.Build;
import android.os.Bundle;
import android.os.Message;
import android.os.PowerManager;
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
    private boolean openPanelOnLoad; // arrived from "Manage space" (D63): show the panel when the page is up

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
        // Where dictionaries come from is the one thing that differs between
        // the FOSS and Play builds (D62), and it lives entirely in Storage —
        // a class that exists once per flavour and never in this source set.
        watchThermal();
        Storage.ensureAccess(this);
        Storage.onNewIntent(this, getIntent()); // launched by a share, possibly
        openPanelOnLoad = getIntent() != null
                && getIntent().getBooleanExtra(ManageSpaceActivity.EXTRA_MANAGE, false);

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
        // Shell-private URLs (wudict://…) are a channel from the page to the
        // Java side that costs no JavascriptInterface and no server API: this
        // method already inspects every navigation, so the branch is free.
        if (Storage.handleShellUri(this, uri)) return true;
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

    // ── power ────────────────────────────────────────────────────────────
    // Everything the server is allowed to know about the device's mood is
    // decided here, in one function, from fields that are the only inputs
    // (D64). One place, because these inputs contradict each other — the app
    // can be visible while the device is hot, backgrounded while charging —
    // and a set of independent callbacks each pushing its own state would make
    // the last event win rather than the strictest condition.
    //
    // The battery and power-save inputs are SAMPLED here rather than watched
    // with broadcast receivers: a receiver is a wakeup the app would not
    // otherwise take, which is the exact cost this whole change exists to
    // avoid, and every transition that matters already calls this.

    private boolean visible;    // between onStart and onStop
    private int thermal;        // PowerManager.THERMAL_STATUS_*, 0 when unknown
    private Object thermalListener; // PowerManager.OnThermalStatusChangedListener, API 29+

    private void applyPower() {
        PowerManager pm = getSystemService(PowerManager.class);
        boolean hot = thermal >= PowerManager.THERMAL_STATUS_MODERATE;
        boolean saving = pm != null && pm.isPowerSaveMode();

        String state;
        if (hot) {
            // Thermal throttling has already begun at MODERATE. Holding caches
            // and threads through it is how an app earns a place on a vendor's
            // battery-abuse list, and the user is holding a device that is
            // getting warm.
            state = PowerSignal.RESTRICTED;
        } else if (!visible) {
            // Charging is the one case where staying active off-screen is
            // defensible: preparing a dictionary is the only real work this
            // app has, it is what the user is waiting for, and a plugged-in
            // device is not spending the user's battery on it.
            state = charging() ? PowerSignal.ACTIVE : PowerSignal.BACKGROUND;
        } else if (saving) {
            // Visible, but the user has asked the whole system to conserve.
            // Serve lookups, on one thread, holding nothing extra.
            state = PowerSignal.BACKGROUND;
        } else {
            state = PowerSignal.ACTIVE;
        }
        PowerSignal.set(state);
    }

    private boolean charging() {
        BatteryManager bm = getSystemService(BatteryManager.class);
        return bm != null && bm.isCharging();
    }

    private void watchThermal() {
        if (Build.VERSION.SDK_INT < 29) return;
        PowerManager pm = getSystemService(PowerManager.class);
        if (pm == null) return;
        thermal = pm.getCurrentThermalStatus();
        PowerManager.OnThermalStatusChangedListener l = status -> {
            thermal = status;
            applyPower();
        };
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
            web.postDelayed(this::applyPower, TRIM_RECOVERY_MS);
        }
    }

    // ── lifecycle ────────────────────────────────────────────────────────

    @Override
    protected void onStart() {
        super.onStart();
        visible = true;
        applyPower();
    }

    @Override
    protected void onStop() {
        super.onStop();
        visible = false;
        // Sent now, while the process is still running: a cached app is frozen
        // by the platform shortly after this, and the child freezes with it.
        applyPower();
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
            web.loadUrl(PAGE_URL);
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
