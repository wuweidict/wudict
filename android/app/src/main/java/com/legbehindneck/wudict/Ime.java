// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

// The keyboard comes up when there is nothing to read, and gets out of the
// way when the reading starts (P76).
//
// Typing a word leaves the IME up, and it stays up while the definition is
// being read: on a phone that is a third of the screen spent on a control the
// user has finished with. The only way out otherwise is the Back key, which is
// a thing to KNOW rather than a thing to notice.
//
// The signal is the scroll gesture itself: a finger dragging vertically is the
// moment the user stops writing and starts reading. It is taken HERE rather
// than in the page for two reasons. Articles render in sandboxed iframes sized
// to their content (frame.js), so a drag over an article is delivered to the
// CHILD document and the page's own listeners never see it - while the WebView
// sees every touch in the window whichever document it lands in. And an IME is
// a platform input concern, which is what this shell is for; the pages stay
// unaware that Android exists (D54).
//
// Both windows onto the server - the app and the lookup popup (D67) - hold the
// same search field over the same articles, so both want the same rule.
package com.legbehindneck.wudict;

import android.annotation.SuppressLint;
import android.os.Build;
import android.os.SystemClock;
import android.view.MotionEvent;
import android.view.View;
import android.view.ViewConfiguration;
import android.view.WindowInsets;
import android.view.WindowInsetsController;
import android.view.inputmethod.InputMethodManager;
import android.webkit.WebView;

final class Ime {

    private Ime() {
    }

    /**
     * The focused element keeps its focus: only the IME is hidden, exactly what
     * the Back key does, so tapping the field brings the keyboard back with no
     * help from us and the caret, the selection and the query are untouched.
     */
    @SuppressLint("ClickableViewAccessibility") // a spy: consumes nothing, clicks unaffected
    static void hideOnScroll(View v) {
        final int slop = ViewConfiguration.get(v.getContext()).getScaledTouchSlop();
        final float[] down = new float[2];      // where the gesture started
        final boolean[] fired = new boolean[1]; // once per gesture, not per event
        v.setOnTouchListener((view, e) -> {
            switch (e.getActionMasked()) {
                case MotionEvent.ACTION_DOWN:
                    down[0] = e.getX();
                    down[1] = e.getY();
                    fired[0] = false;
                    break;
                case MotionEvent.ACTION_MOVE:
                    if (fired[0]) break;
                    float dy = Math.abs(e.getY() - down[1]);
                    // Vertical dominance is what keeps a drag INSIDE the search
                    // field - selecting the word just typed - from being read
                    // as a scroll. That gesture is horizontal, and it is the
                    // one case where the keyboard has to stay.
                    if (dy > slop && dy > Math.abs(e.getX() - down[0])) {
                        fired[0] = true;
                        hide(view);
                    }
                    break;
                default:
                    break;
            }
            return false; // never consumed: the WebView scrolls, zooms and clicks as before
        });
    }

    // ── and comes up when the screen has nothing else to offer ───────────

    private static final long POLL_MS = 150;
    // Long enough for a cold start over a large library (the page's own boot
    // screen runs for as long as the dictionaries take); past it the user has
    // certainly either interacted or given up, and one tap is the fallback.
    private static final long WAIT_MS = 20000;

    /**
     * Raise the keyboard once the page has put the caret in its search field.
     *
     * <p>Android focuses the first text field at start but deliberately does
     * NOT show the keyboard, because typing is not every screen's primary task
     * - and the documented way to say "here it is" is the manifest's
     * stateVisible. That is the wrong instrument for this app. It fires on
     * every activity start, including a resume onto an article the user is
     * reading, and on a cold start it would raise the keyboard over the
     * loading screen, where the search field is still disabled and there is
     * nothing to type into.
     *
     * <p>So the decision is split along D54's line. The PAGE decides focus by
     * its own platform-neutral rule - dictionaries ready, field empty, nothing
     * else focused - and this waits for that decision to appear in the DOM and
     * then does the one thing the page cannot do here: element.focus() in a
     * WebView places a caret but never summons the IME. That gap is the whole
     * of this method, and closing it teaches the page nothing about Android.
     *
     * <p>The poll is also the abort. If the user got there first - opened the
     * panel, tapped a link, tapped the field itself - focus is somewhere else
     * and the deadline simply passes with nothing done (or the keyboard is
     * already up, and showing it again is a no-op).
     *
     * <p>Call only where the screen really is empty: a cold start with no
     * forwarded query. A hardware keyboard needs no help, and the platform
     * already declines to show a soft one when there is one attached.
     */
    static void showWhenPageFocuses(WebView web) {
        poll(web, SystemClock.uptimeMillis() + WAIT_MS);
    }

    private static void poll(WebView web, long deadline) {
        if (!web.isAttachedToWindow() || SystemClock.uptimeMillis() > deadline) return;
        // "gone" ends the wait early for a page that has no search field at
        // all - the first-run setup page, which the server serves in place of
        // the app when no dictionary has been added yet.
        web.evaluateJavascript(
                "(function(){var q=document.getElementById('q');"
                        + "if(!q)return document.readyState==='complete'?'gone':'wait';"
                        + "return (!q.disabled&&document.activeElement===q)?'go':'wait'})()",
                value -> {
                    if (!web.isAttachedToWindow()) return;
                    if ("\"go\"".equals(value)) {
                        raise(web);
                    } else if (!"\"gone\"".equals(value)) {
                        web.postDelayed(() -> poll(web, deadline), POLL_MS);
                    }
                });
    }

    /**
     * The page has the caret; give the platform what it needs to agree.
     *
     * <p>Two things a page cannot do for itself. A WebView holds no View focus
     * until something touches it - the served view stays the DecorView and the
     * page's focused element is invisible to the input method - and in touch
     * mode requestFocus() is refused outright unless the view is focusable in
     * touch mode. That pair is exactly how a caret ends up sitting in a
     * WebView with no keyboard under it.
     *
     * <p>Then the DOM focus is re-asserted, because taking View focus can cost
     * the page the focus it just took, and an IME raised over nothing focused
     * is worse than no IME at all. Show last, in the same beat as the focus it
     * belongs to.
     */
    private static void raise(WebView web) {
        web.setFocusable(true);
        web.setFocusableInTouchMode(true);
        web.requestFocus();
        web.evaluateJavascript(
                "(function(){var q=document.getElementById('q');if(q)q.focus()})()",
                ignored -> {
                    if (web.isAttachedToWindow()) show(web);
                });
    }

    /** The IME, for the view that already holds focus (see {@link #raise}). */
    static void show(View v) {
        InputMethodManager imm = v.getContext().getSystemService(InputMethodManager.class);
        // SHOW_IMPLICIT, never SHOW_FORCED: forced can leave the keyboard up
        // after the app is gone, which is the platform's own warning. The
        // request is addressed to the view rather than to the window's insets
        // controller, which shows the IME for whatever the window believes is
        // served - and being wrong about that is silent.
        if (imm != null) {
            imm.showSoftInput(v, InputMethodManager.SHOW_IMPLICIT);
        }
    }

    static void hide(View v) {
        if (Build.VERSION.SDK_INT >= 30) {
            WindowInsets in = v.getRootWindowInsets();
            // Hiding what is already hidden would be harmless; asking first
            // also skips the controller lookup on every scroll of a session
            // that never opened the keyboard at all.
            if (in == null || !in.isVisible(WindowInsets.Type.ime())) return;
            WindowInsetsController c = v.getWindowInsetsController();
            if (c != null) c.hide(WindowInsets.Type.ime());
            return;
        }
        InputMethodManager imm = v.getContext().getSystemService(InputMethodManager.class);
        if (imm != null) imm.hideSoftInputFromWindow(v.getWindowToken(), 0);
    }
}
