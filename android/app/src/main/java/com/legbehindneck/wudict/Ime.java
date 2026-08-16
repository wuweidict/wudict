// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

// The keyboard gets out of the way when the reading starts (P76).
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
// CHILD document and the page's own listeners never see it — while the WebView
// sees every touch in the window whichever document it lands in. And an IME is
// a platform input concern, which is what this shell is for; the pages stay
// unaware that Android exists (D54).
//
// Both windows onto the server — the app and the lookup popup (D67) — hold the
// same search field over the same articles, so both want the same rule.
package com.legbehindneck.wudict;

import android.annotation.SuppressLint;
import android.os.Build;
import android.view.MotionEvent;
import android.view.View;
import android.view.ViewConfiguration;
import android.view.WindowInsets;
import android.view.WindowInsetsController;
import android.view.inputmethod.InputMethodManager;

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
                    // field — selecting the word just typed — from being read
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
