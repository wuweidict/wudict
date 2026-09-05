// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

// What every WebView in this app has in common: the one origin that belongs to
// us, how a URL is built for it, which settings the SPA needs, and where a
// navigation that is NOT ours goes instead.
//
// It lives here rather than in MainActivity because there are now two windows
// onto the same server - the app and the selection-lookup popup (D67) - and a
// second copy of this policy is a second thing to keep in step. Nothing here
// knows about either activity's layout or lifecycle.
package com.legbehindneck.wudict;

import android.app.Activity;
import android.content.ActivityNotFoundException;
import android.content.Context;
import android.content.Intent;
import android.net.Uri;
import android.os.Message;
import android.util.Log;
import android.webkit.CookieManager;
import android.webkit.WebChromeClient;
import android.webkit.WebResourceRequest;
import android.webkit.WebView;
import android.webkit.WebViewClient;

import java.io.UnsupportedEncodingException;
import java.net.URLEncoder;

final class Shell {

    private static final String TAG = "wudict";

    /**
     * The one origin that belongs to us; everything else is somebody's website.
     *
     * <p>A method rather than a constant since D101, because the port is an
     * install-level setting a device may override. The host never varies: it is
     * the address we connect to, not the one the server binds to.
     */
    static String origin(Context c) {
        return "http://" + ServerProcess.HOST + ":" + ServerProcess.port(c);
    }

    static String pageUrl(Context c) {
        String k = key(c);
        return origin(c) + "/" + (k.isEmpty() ? "" : "?" + k);
    }

    /**
     * The access key as a query fragment, or "" when this install does not use
     * one (ShellPrefs.REQUIRE_KEY).
     *
     * <p>Carried on the URL rather than installed with CookieManager, which
     * would be the obvious way and is the fragile one: setCookie's write is
     * not ordered against a loadUrl issued in the next statement, so the first
     * page of a cold start would race it. The server takes the key off the
     * URL, sets the cookie itself and redirects to the address without it -
     * one loopback round trip, no timing to reason about, and the key never
     * stays in the page's own location. Every subsequent request in that
     * WebView - articles, media, the search stream - rides the cookie.
     */
    private static String key(Context c) {
        String t = ShellPrefs.token(c);
        return t.isEmpty() ? "" : "k=" + t;
    }

    private Shell() {
    }

    /**
     * A search the page will run on load. `?q=…&mode=…&dict=…` is the SPA's own
     * deep-link shape (web/index.html, applyURL) - the shell adds no API and the
     * page learns nothing about Android (D54).
     *
     * <p>Every component is encoded here, so a caller's text - a selection from
     * another app, a URI from an intent - can carry anything at all.
     */
    static String searchUrl(Context c, String q, String mode, String dict) {
        StringBuilder b = new StringBuilder(origin(c)).append("/?q=").append(enc(q));
        if (mode != null && !mode.isEmpty()) b.append("&mode=").append(enc(mode));
        if (dict != null && !dict.isEmpty()) b.append("&dict=").append(enc(dict));
        String k = key(c);
        if (!k.isEmpty()) b.append("&").append(k);
        return b.toString();
    }

    // URLEncoder writes ' ' as '+', which URLSearchParams reads back as ' ' -
    // the same pair the page already uses for its own links.
    private static String enc(String s) {
        try {
            return URLEncoder.encode(s, "UTF-8");
        } catch (UnsupportedEncodingException e) {
            return ""; // UTF-8 is guaranteed by the platform; this branch is unreachable
        }
    }

    /** The settings the SPA needs. Identical in every window, by construction. */
    static void configure(WebView web) {
        // The access key arrives as a cookie the server sets (see key()), so a
        // WebView that refuses cookies would authenticate once and then fail
        // every request the page makes. It is the platform default; stated
        // here because it is now load-bearing.
        CookieManager.getInstance().setAcceptCookie(true);
        web.getSettings().setJavaScriptEnabled(true);                 // the UI is one SPA
        web.getSettings().setDomStorageEnabled(true);                 // prefs live in localStorage
        web.getSettings().setMediaPlaybackRequiresUserGesture(false); // dictionary audio
        // D51 hands an article's external links to the page, which opens them
        // with window.open(). A WebView has no tabs, so that call is inert
        // unless multiple windows are supported and onCreateWindow answers it.
        web.getSettings().setSupportMultipleWindows(true);
    }

    /**
     * Reports whether it took the navigation off the WebView's hands. Our own
     * origin stays; a dictionary's link to a real website goes to whatever the
     * user browses with, because a WebView with no address bar, no tabs and no
     * back affordance is a trap to land a website in.
     */
    static boolean openExternal(Activity a, Uri uri) {
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
        String origin = origin(a);
        if (url.equals(origin) || url.startsWith(origin + "/")) return false;
        // Shell-private URLs (wudict://…) are a channel from the page to the
        // Java side that costs no JavascriptInterface and no server API: this
        // method already inspects every navigation, so the branch is free.
        if (Storage.handleShellUri(a, uri)) return true;
        try {
            a.startActivity(new Intent(Intent.ACTION_VIEW, uri)
                    .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK));
        } catch (ActivityNotFoundException | SecurityException e) {
            Log.w(TAG, "no app can open " + uri, e);
        }
        return true;
    }

    /** Answers window.open(): reads the URL the new window wants, then sends it out. */
    static WebChromeClient windows(Activity a) {
        return new WebChromeClient() {
            @Override
            public boolean onCreateWindow(WebView view, boolean isDialog,
                                          boolean isUserGesture, Message resultMsg) {
                // window.open() has no URL in this callback - the only way to
                // learn it is to hand the transport a throwaway WebView and read
                // the navigation it is about to make.
                WebView sink = new WebView(view.getContext());
                sink.setWebViewClient(new WebViewClient() {
                    @Override
                    public boolean shouldOverrideUrlLoading(WebView v, WebResourceRequest req) {
                        openExternal(a, req.getUrl());
                        v.post(v::destroy); // never destroy a WebView inside its own callback
                        return true;
                    }
                });
                ((WebView.WebViewTransport) resultMsg.obj).setWebView(sink);
                resultMsg.sendToTarget();
                return true;
            }
        };
    }
}
