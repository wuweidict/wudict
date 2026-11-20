// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

// WuWeiDict's Android shell (D52): a WebView over the wudict server binary
// that ships inside the APK as libwudict.so. The Go program is unchanged -
// ServerProcess execs it as a child and it answers on 127.0.0.1 at ShellPrefs.DEFAULT_PORT.
package com.legbehindneck.wudict;

import android.app.Activity;
import android.content.Intent;
import android.graphics.Insets;
import android.graphics.drawable.ColorDrawable;
import android.os.Build;
import android.os.Bundle;
import android.net.Uri;
import android.os.PowerManager;
import android.view.Gravity;
import android.view.View;
import android.view.WindowInsets;
import android.view.WindowInsetsController;
import android.webkit.WebResourceRequest;
import android.webkit.WebResourceResponse;
import android.webkit.WebView;
import android.webkit.WebViewClient;
import android.widget.FrameLayout;
import android.widget.TextView;
import android.window.OnBackInvokedCallback;
import android.window.OnBackInvokedDispatcher;

import java.io.ByteArrayInputStream;

public class MainActivity extends Activity {

    // An optional search to run on load, handed over by LookupActivity - either
    // by the popup's handoff row (D67) or because the entry point is set to open
    // the app outright (D100). All three parts travel together: the wudict://
    // entry point can carry a mode and a dictionary, and forwarding the word
    // alone would quietly answer a different question than the caller asked.
    static final String EXTRA_QUERY = "com.legbehindneck.wudict.QUERY";
    static final String EXTRA_MODE = "com.legbehindneck.wudict.MODE";
    static final String EXTRA_DICT = "com.legbehindneck.wudict.DICT";
    // The launcher shortcut "Random word" (D202, res/xml/shortcuts.xml): its
    // presence alone asks for a random word, opened. A literal there, so the
    // name is spelled out rather than built from the package.
    static final String EXTRA_RANDOM = "com.legbehindneck.wudict.RANDOM";

    private FrameLayout root;
    private TextView status;
    private WebView web;
    private Speech speech;           // read-aloud (D148); binds no engine until asked

    private volatile boolean gone;   // onDestroy ran: late server callbacks must not touch the views
    private Object backCallback;     // OnBackInvokedCallback (API 33+), registered only while canGoBack()
    private boolean wantAutoFocus;   // this load is a cold start onto an empty screen
    private String pendingQuery;     // arrived from LookupActivity (D67, D100)
    private String pendingMode;
    private String pendingDict;
    private boolean pendingRandom;   // arrived from the "Random word" shortcut (D202)

    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);

        // The earliest point Java can correct the window's colour. The theme's
        // splash/window background was resolved from the OS day/night setting
        // before this process existed (values/styles.xml); the PAGE's theme is
        // a stored preference, so for a user whose theme disagrees with their
        // phone the window under everything is still the wrong colour - and it
        // shows through during the enter transition and any frame our own views
        // have not covered yet. applyEdges repaints it from the preference; do
        // it here too, before setContentView, so no frame of this activity is
        // ever drawn against the resource colour.
        getWindow().setBackgroundDrawable(new ColorDrawable(ShellPrefs.edgeColor(this)));

        root = new FrameLayout(this);
        status = new TextView(this);
        status.setText(R.string.starting);
        root.addView(status, new FrameLayout.LayoutParams(
                FrameLayout.LayoutParams.WRAP_CONTENT,
                FrameLayout.LayoutParams.WRAP_CONTENT,
                Gravity.CENTER));

        web = new WebView(this);
        // The PAGE's background, not the window's: this is the surface the
        // document lands on, and a user whose theme disagrees with their phone
        // would otherwise get one frame of the other one (D141).
        web.setBackgroundColor(ShellPrefs.pageBg(this)); // no white flash before first paint
        Shell.configure(web);
        Ime.hideOnScroll(web);
        speech = new Speech(this, web);
        web.setWebViewClient(new ShellWebViewClient());
        web.setWebChromeClient(Shell.windows(this));
        WebView.setWebContentsDebuggingEnabled(BuildConfig.DEBUG);

        setContentView(root);
        applyWindowInsets();
        applyEdges();
        applyBars();
        // Where dictionaries come from is the one thing that differs between
        // the FOSS and Play builds (D62), and it lives entirely in Storage -
        // a class that exists once per flavour and never in this source set.
        watchThermal();
        Storage.ensureAccess(this);
        // Launched by a share or an "open with", possibly. Intake takes it
        // only when it carries an archive; everything else is the flavour's.
        //
        // Only on a FRESH launch. A restored activity is handed the intent that
        // first created its task, again: a task a shared link started keeps that
        // VIEW intent as its base for good, and the platform replays it after
        // every process death - reopening the app from the launcher or recents
        // re-imported the link (and the import still held by the server that
        // outlived the process refused it as "already running"). Clearing the
        // data or the extra cannot prevent it: that edits this process's copy,
        // and the system restores from its own. A recreation carries saved
        // state; a relaunch from recents after the state is gone carries
        // FLAG_ACTIVITY_LAUNCHED_FROM_HISTORY.
        Intent launch = getIntent();
        boolean fresh = savedInstanceState == null && launch != null
                && (launch.getFlags() & Intent.FLAG_ACTIVITY_LAUNCHED_FROM_HISTORY) == 0;
        if (fresh && !Intake.onNewIntent(this, launch)) Storage.onNewIntent(this, launch);
        // A query is taken again on a recreation, as it always was; a random
        // word only on a real launch: a recreated activity (a theme, locale
        // or font-scale change) or a relaunch from Recents would otherwise
        // open a new word over the one being read.
        if (!takeQuery(launch) && fresh) takeRandom(launch);

        ServerProcess.retain();
        ServerProcess.ensure(this, new ServerProcess.Listener() {
            @Override public void onReady() { showPage(); }
            @Override public void onFailed(String message) { showFailure(message); }
        });
    }

    // ── window insets ────────────────────────────────────────────────────
    // Android 15 forces edge-to-edge on apps targeting API 35+, and Android 16
    // DISABLED the windowOptOutEdgeToEdgeEnforcement escape hatch for apps
    // targeting API 36 - which is us. So opting out is not available and the
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
    //
    // EDGE_NONE (D141) is the one mode that declines that service: the top and
    // bottom insets are handed to the PAGE instead, which wears them on the
    // elements that care, so the article really does run to the glass. Three
    // things stay with the shell even then:
    //
    //   - the SIDE insets, always. A landscape cutout eats the reading gutter,
    //     and no stylesheet rule can make that a good idea.
    //   - the IME, always. It is not a system bar, it is never hidden here, and
    //     an edge-to-edge window gets no adjustResize from the decor - without
    //     this the keyboard would stand over the search field.
    //   - the window background, which is what a side inset and the frame
    //     before the first paint actually show.
    private void applyWindowInsets() {
        root.setOnApplyWindowInsetsListener((v, insets) -> {
            if (Build.VERSION.SDK_INT >= 30) {
                Insets bars = insets.getInsets(
                        WindowInsets.Type.systemBars() | WindowInsets.Type.displayCutout());
                Insets ime = insets.getInsets(WindowInsets.Type.ime());
                boolean toPage = ShellPrefs.edgeMode(this) == ShellPrefs.EDGE_NONE;
                int top = toPage ? 0 : bars.top;
                int bottom = Math.max(toPage ? 0 : bars.bottom, ime.bottom);
                v.setPadding(bars.left, top, bars.right, bottom);
                // Zeroes in every other mode, which is how switching back out
                // of EDGE_NONE undoes itself. The bottom is withheld while the
                // keyboard is up: the shell is holding that space open already,
                // and the page must not hold it a second time.
                publishInsets(toPage ? bars.top : 0,
                        toPage && ime.bottom == 0 ? bars.bottom : 0);
            } else {
                legacyPadding(v, insets);
            }
            // not consumed: the WebView is welcome to see them too
            return insets;
        });
        root.requestApplyInsets();
    }

    // ── handing the insets to the page (EDGE_NONE only) ──────────────────
    // Published as CSS custom properties the stylesheet reads with a 0px
    // fallback, so an un-injected page - and every other mode, which publishes
    // zeroes - gets the plain inset layout.
    //
    // COALESCING IS NOT AN OPTIMISATION. A transient bar swipe delivers an
    // inset callback on every frame of its animation; without the comparison
    // below this is an evaluateJavascript at display rate for the length of
    // every edge swipe, which is the one way this design can be implemented
    // wrongly and still look right.
    private int insetTopPx, insetBottomPx;   // last seen, in device pixels
    private int sentTop = -1, sentBottom = -1; // last published, in CSS px

    private void publishInsets(int topPx, int bottomPx) {
        insetTopPx = topPx;
        insetBottomPx = bottomPx;
        float d = getResources().getDisplayMetrics().density;
        int t = Math.round(topPx / d), b = Math.round(bottomPx / d);
        if (t == sentTop && b == sentBottom) return;
        if (web.getParent() == null) return; // no document yet; onPageFinished republishes
        sentTop = t;
        sentBottom = b;
        web.evaluateJavascript(
                "(function(s){s.setProperty('--wd-inset-top','" + t + "px');"
                        + "s.setProperty('--wd-inset-bottom','" + b + "px')})"
                        + "(document.documentElement.style)", null);
    }

    /** Re-publishes into a document that has never been told. */
    private void republishInsets() {
        sentTop = sentBottom = -1;
        publishInsets(insetTopPx, insetBottomPx);
    }

    // API 26–29: no forced edge-to-edge, so these are normally all zero - the
    // decor has already inset the content view. Kept for cutout devices on 28/29.
    //
    // EDGE_NONE is not honoured here, deliberately: on these versions the window
    // is not edge-to-edge in the first place, so there is no space to hand the
    // page and nothing for it to paint. The colour choices all still apply.
    @SuppressWarnings("deprecation")
    private static void legacyPadding(View v, WindowInsets insets) {
        v.setPadding(insets.getSystemWindowInsetLeft(), insets.getSystemWindowInsetTop(),
                insets.getSystemWindowInsetRight(), insets.getSystemWindowInsetBottom());
    }

    // ── the display edge (D141) ──────────────────────────────────────────
    // What the inset padding SHOWS, and what the bar icons have to contrast
    // with. One method because those are one fact: the icons are drawn over
    // our padding, so the colour decides them and no second source may.
    //
    // The colour comes from ShellPrefs.edgeColor - the OS day/night setting,
    // the page's own theme, black, or a colour the user picked - and the icon
    // polarity from the contrast arithmetic on that same value, never from
    // uiMode, which would give a page-dark strip under a light OS the wrong
    // icons.
    //
    // Cheap enough to re-run on every report: two setters and a resource read.
    private void applyEdges() {
        int edge = ShellPrefs.edgeColor(this);
        root.setBackgroundColor(edge);
        // The window too, not just our root: it is what the enter transition
        // and the pre-first-layout frames show, and the theme could only give
        // it the OS's colour.
        getWindow().setBackgroundDrawable(new ColorDrawable(edge));
        boolean dark = ShellPrefs.darkIcons(edge);
        // "Starting…" and the server-failure sentence are the only text the
        // SHELL draws, and they sit on that same colour. Their theme colour is
        // the OS's, so a page-dark strip under a light OS put dark text on a
        // dark ground. Same arithmetic as the bar icons, same reason.
        status.setTextColor(dark ? 0xDE000000 : 0xFFFFFFFF);
        View decor = getWindow().getDecorView();
        if (Build.VERSION.SDK_INT >= 30) {
            WindowInsetsController c = decor.getWindowInsetsController();
            if (c != null) {
                int light = WindowInsetsController.APPEARANCE_LIGHT_STATUS_BARS
                        | WindowInsetsController.APPEARANCE_LIGHT_NAVIGATION_BARS;
                // APPEARANCE_LIGHT_* describes the BACKGROUND, so it is the
                // flag that asks for dark icons.
                c.setSystemBarsAppearance(dark ? light : 0, light);
            }
        } else {
            legacyBarAppearance(decor, !dark);
        }
    }

    @SuppressWarnings("deprecation")
    private static void legacyBarAppearance(View decor, boolean night) {
        int flags = decor.getSystemUiVisibility();
        int light = View.SYSTEM_UI_FLAG_LIGHT_STATUS_BAR
                | View.SYSTEM_UI_FLAG_LIGHT_NAVIGATION_BAR;
        decor.setSystemUiVisibility(night ? (flags & ~light) : (flags | light));
    }

    // ── the page's theme, reported by the page (D141) ────────────────────
    // EDGE_PAGE needs a fact only the document holds: wudict_theme resolves
    // auto -> light -> dark in localStorage, which is keyed by origin and
    // reachable from nowhere else. So the page is asked, by a watcher the
    // SHELL injects - web/index.html still knows nothing about Android (D54),
    // exactly as Storage.onPageFinished already does for the import control.
    //
    // The answer comes back as a SUBRESOURCE REQUEST caught below, not as a
    // navigation. The wudict:// navigation channel Shell.openExternal offers
    // is reached today only from a real anchor click; a scripted
    // location.href to a custom scheme is at the mercy of the engine's
    // user-gesture heuristics, and a channel that fires on a timer and a
    // matchMedia callback cannot be built on one. A same-origin subresource
    // is unconditional: shouldInterceptRequest sees every one of them, and
    // this one is answered here and never reaches the server, so no HTTP
    // surface is added either. Should interception ever miss it, the request
    // 404s and the strip keeps its last colour - the failure is a stale
    // colour, not a broken page.
    private static final String THEME_PATH = "/__wudict-shell/theme";

    private static final String THEME_JS =
            "(function(){if(window.__wdEdge)return;window.__wdEdge=1;"
          + "var m=matchMedia('(prefers-color-scheme: dark)'),last=null,n=0;"
          + "function dark(){var t=document.documentElement.getAttribute('data-theme');"
          + "return t?t==='dark':m.matches}"
          + "function report(){var v=dark();if(v===last)return;last=v;"
          // n= defeats the memory cache: the same value would otherwise be
          // re-requested only once per document, and toggling back and forth
          // would go silent after the first round trip.
          + "new Image().src='" + THEME_PATH + "?dark='+(v?1:0)+'&n='+(++n)}"
          + "new MutationObserver(report).observe(document.documentElement,"
          + "{attributes:true,attributeFilter:['data-theme']});"
          + "m.addEventListener('change',report);report()})()";

    /** True when this request was the watcher's report, and is now answered. */
    private boolean takeThemeReport(Uri u) {
        if (u == null || !THEME_PATH.equals(u.getPath())) return false;
        boolean dark = "1".equals(u.getQueryParameter("dark"));
        // shouldInterceptRequest runs on a WebView worker thread; every view
        // and preference below is the main thread's.
        runOnUiThread(() -> {
            if (gone) return;
            if (!ShellPrefs.setPageDark(this, dark)) return;
            web.setBackgroundColor(ShellPrefs.pageBg(this));
            applyEdges();
        });
        return true;
    }

    // ── which system bars hide ───────────────────────────────────────────
    // A reading app's window is worth more than its chrome, so the bars can be
    // asked to leave - either of them, independently (D141): a phone whose
    // clock is worth keeping still has a gesture bar worth losing. Two rules
    // make this safe to hand a user:
    //
    // TRANSIENT, NEVER STICKY-BY-SURPRISE. The bars come back on an edge swipe
    // and leave again on their own, so nothing is unreachable while they are
    // hidden - which is what makes this an acceptable control to offer at all
    // rather than a trap. On API 30+ that is BEHAVIOR_SHOW_TRANSIENT_BARS_BY_SWIPE;
    // below it, IMMERSIVE_STICKY, which is the same bargain spelled the old way.
    //
    // THE INSETS ARE NOT TOUCHED. applyWindowInsets() keeps padding the root by
    // whatever the window reports; hidden bars report zero, so the page gains
    // the space with nothing in this class knowing why, and a transient bar is
    // an overlay that reports nothing and therefore shifts nothing under it.
    // The display cutout is still inset while the bars are gone - text under a
    // camera hole is not what anyone asked for - and the IME is still inset,
    // because it is not a system bar and is never hidden here.
    //
    // Re-applied on every focus gain rather than once: a transient bar, a
    // dialog, the recents switcher and a return from the settings window all
    // restore the bars, and the platform expects the app to say again.
    private void applyBars() {
        int mask = ShellPrefs.bars(this);
        View decor = getWindow().getDecorView();
        if (Build.VERSION.SDK_INT >= 30) {
            WindowInsetsController c = decor.getWindowInsetsController();
            if (c == null) return;
            c.setSystemBarsBehavior(
                    WindowInsetsController.BEHAVIOR_SHOW_TRANSIENT_BARS_BY_SWIPE);
            int hide = 0;
            if ((mask & ShellPrefs.BARS_STATUS) != 0) hide |= WindowInsets.Type.statusBars();
            if ((mask & ShellPrefs.BARS_NAV) != 0) hide |= WindowInsets.Type.navigationBars();
            int show = (WindowInsets.Type.statusBars() | WindowInsets.Type.navigationBars())
                    & ~hide;
            // Both calls, every time: this runs on every focus gain, and the
            // bar that is NOT hidden has to be asked back after a transient
            // swipe just as firmly as the other one is asked away.
            if (hide != 0) c.hide(hide);
            if (show != 0) c.show(show);
        } else {
            legacyBars(decor, mask);
        }
    }

    // API 26–29. Read-modify-write, because applyEdges() owns two other bits
    // in the same field and must not be undone by this.
    @SuppressWarnings("deprecation")
    private static void legacyBars(View decor, int mask) {
        int all = View.SYSTEM_UI_FLAG_FULLSCREEN
                | View.SYSTEM_UI_FLAG_HIDE_NAVIGATION
                | View.SYSTEM_UI_FLAG_IMMERSIVE_STICKY
                | View.SYSTEM_UI_FLAG_LAYOUT_STABLE
                | View.SYSTEM_UI_FLAG_LAYOUT_FULLSCREEN
                | View.SYSTEM_UI_FLAG_LAYOUT_HIDE_NAVIGATION;
        int want = 0;
        if ((mask & ShellPrefs.BARS_STATUS) != 0) {
            want |= View.SYSTEM_UI_FLAG_FULLSCREEN | View.SYSTEM_UI_FLAG_LAYOUT_FULLSCREEN;
        }
        if ((mask & ShellPrefs.BARS_NAV) != 0) {
            want |= View.SYSTEM_UI_FLAG_HIDE_NAVIGATION
                    | View.SYSTEM_UI_FLAG_LAYOUT_HIDE_NAVIGATION;
        }
        if (want != 0) {
            want |= View.SYSTEM_UI_FLAG_IMMERSIVE_STICKY | View.SYSTEM_UI_FLAG_LAYOUT_STABLE;
        }
        decor.setSystemUiVisibility((decor.getSystemUiVisibility() & ~all) | want);
    }

    @Override
    public void onWindowFocusChanged(boolean hasFocus) {
        super.onWindowFocusChanged(hasFocus);
        // Only on gain: asking while the window is losing focus is asking on
        // behalf of whatever is taking it.
        if (hasFocus) {
            applyBars();
            // The settings window is another activity, so a changed edge mode
            // arrives as a focus gain and nothing else. Re-asking for the
            // insets is what re-routes them when the mode itself changed.
            applyEdges();
            web.setBackgroundColor(ShellPrefs.pageBg(this));
            root.requestApplyInsets();
        }
    }

    // ── navigation ───────────────────────────────────────────────────────

    private class ShellWebViewClient extends WebViewClient {
        @Override
        public boolean shouldOverrideUrlLoading(WebView view, WebResourceRequest req) {
            return Shell.openExternal(MainActivity.this, req.getUrl());
        }

        @Override
        public WebResourceResponse shouldInterceptRequest(WebView view, WebResourceRequest req) {
            if (takeThemeReport(req.getUrl())) {
                // An empty 200. The caller is an <img> that nobody looks at;
                // what it needs is to not be a pending request forever.
                return new WebResourceResponse("text/plain", "utf-8",
                        new ByteArrayInputStream(new byte[0]));
            }
            WebResourceResponse r = speech.intercept(req.getUrl());
            if (r != null) return r;
            return null; // everything else is the WebView's own business
        }

        @Override
        public void doUpdateVisitedHistory(WebView view, String url, boolean isReload) {
            syncBackCallback(); // canGoBack() just changed
        }

        @Override
        public void onPageFinished(WebView view, String url) {
            // The Play flavour adds its import control here. Nothing in
            // web/index.html knows what Android is - the D54 rule (the shell
            // absorbs the platform, not the page), applied to the DOM.
            Storage.onPageFinished(view);
            // Re-injected per document, and the watcher reports once on
            // injection, so a reload or a navigation re-states the theme
            // rather than leaving the shell on a remembered one.
            view.evaluateJavascript(THEME_JS, null);
            // Same rule for speech: the WebView's own speechSynthesis has no
            // voices, so the shell supplies one per document (Speech.java).
            speech.inject(view, url);
            // A new document starts with no custom properties at all, so the
            // shell has to say again - and the coalescing state has to forget
            // that it ever said.
            republishInsets();
            // One-shot, and only for the load showPage armed: the access-key
            // redirect can finish more than one document on the way in, and
            // every later navigation in this WebView is the user going
            // somewhere, which is never a moment to raise a keyboard.
            if (wantAutoFocus) {
                wantAutoFocus = false;
                Ime.showWhenPageFocuses(view);
            }
        }
    }

    // Back. Apps targeting API 35+ get predictive back enabled by default, and
    // for those onBackPressed() is NO LONGER CALLED - the plain override below
    // is dead code on any modern device, which would make the button exit the
    // app instead of walking the SPA's history. Registering the callback
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
    // here is the thermal subscription - the app's own window is where it is
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
    // TRIM_MEMORY_RUNNING_CRITICAL (15) is the opposite case - the app is
    // VISIBLE and the whole device is short. It is the only such signal we
    // ever get: the server's own heap-pressure handling measures our heap
    // against our ceiling, which says nothing about a shortage caused by
    // everything else on the phone. So it is obeyed, and then undone on a
    // timer, because nothing else would: no lifecycle callback is coming while
    // the user simply keeps reading, and a restricted state that never lifts
    // would leave the app single-threaded for the rest of the session. The
    // intermediate running levels (LOW, MODERATE) are advisory and are left
    // alone - shedding there would fight the user's actual work.
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
        Notif.top(this); // a window the one permission ask can be made from
    }

    @Override
    protected void onStop() {
        super.onStop();
        // Sent now, while the process is still running: a cached app is frozen
        // by the platform shortly after this, and the child freezes with it.
        Power.exit(this);
        Notif.gone(this);
        speech.stop(); // a reading does not follow the reader out of the app
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
            String m = pendingMode, d = pendingDict;
            boolean random = pendingRandom;
            pendingQuery = pendingMode = pendingDict = null;
            pendingRandom = false;
            // Nothing was forwarded, so this is a cold start onto an empty
            // screen: a search field and no content at all, which is the one
            // state where typing is unambiguously the task. A forwarded query
            // is the opposite - an article is on its way, and a keyboard would
            // be standing over it.
            //
            // Armed here and spent in onPageFinished, not started here: at
            // this instant the WebView still holds the OUTGOING document -
            // about:blank on a cold start, which is `complete` and has no
            // search field, so a watcher started now would take that for a
            // page with nothing to focus and give up before this load began.
            //
            // Reached only from onReady, which runs once per activity, so a
            // resume onto whatever the user was reading never arrives here and
            // needs no lifecycle test of its own.
            // A random word is the same: its article is on its way.
            wantAutoFocus = q == null && !random;
            web.loadUrl(q != null ? Shell.searchUrl(this, q, m, d)
                    : random ? Shell.randomUrl(this) : Shell.pageUrl(this));
        });
    }

    /**
     * Reads the "Random word" shortcut off an intent (D202). Its presence is
     * the request: launchers deliver a shortcut's extra with the type they
     * parsed, and what it holds says nothing more.
     */
    private boolean takeRandom(Intent intent) {
        if (intent == null || !intent.hasExtra(EXTRA_RANDOM)) return false;
        pendingRandom = true;
        return true;
    }

    /**
     * Reads a forwarded search off an intent. Kept as one call because the
     * three extras are one fact: a query with the mode dropped is a different
     * search, not a slightly poorer one.
     */
    private boolean takeQuery(Intent intent) {
        if (intent == null) return false;
        String q = intent.getStringExtra(EXTRA_QUERY);
        if (q == null) return false;
        pendingQuery = q;
        pendingMode = intent.getStringExtra(EXTRA_MODE);
        pendingDict = intent.getStringExtra(EXTRA_DICT);
        return true;
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
        if (!Intake.onNewIntent(this, intent)) Storage.onNewIntent(this, intent);
        // Only what THIS intent brought: an unrelated intent must not fire a
        // query left pending by an earlier one - showPage owns that.
        if (takeQuery(intent) && !gone && web.getParent() != null) {
            // Handed over by LookupActivity, so the app is very likely already
            // up and showing something else; if it is not, showPage takes it.
            String q = pendingQuery;
            String m = pendingMode, d = pendingDict;
            pendingQuery = pendingMode = pendingDict = null;
            web.loadUrl(Shell.searchUrl(this, q, m, d));
        } else if (takeRandom(intent) && !gone && web.getParent() != null) {
            // The shortcut, with the app already up: a word over whatever is
            // on screen, as a forwarded query is.
            pendingRandom = false;
            web.loadUrl(Shell.randomUrl(this));
        }
    }

    @Override
    @SuppressWarnings("deprecation") // startActivityForResult: no androidx here, by design
    protected void onActivityResult(int requestCode, int resultCode, Intent data) {
        super.onActivityResult(requestCode, resultCode, data);
        Storage.onActivityResult(this, requestCode, resultCode, data);
        Intake.onActivityResult(this, requestCode, resultCode, data);
        Shell.onActivityResult(this, requestCode, resultCode, data);
    }

    @Override
    protected void onDestroy() {
        gone = true;
        unwatchThermal();
        // The server is bound to the app's lifetime (D52): finishing kills
        // it; swiping the task away kills the process group, which includes
        // the child. Recreation keeps it - and so does a lookup popup that is
        // still up, which is why the decision is ServerProcess's and not this
        // activity's (D67).
        ServerProcess.release(isFinishing());
        if (web.getParent() != null) {
            ((FrameLayout) web.getParent()).removeView(web);
        }
        speech.shutdown(); // before the WebView goes: its callbacks post into it
        web.destroy();
        super.onDestroy();
    }
}
