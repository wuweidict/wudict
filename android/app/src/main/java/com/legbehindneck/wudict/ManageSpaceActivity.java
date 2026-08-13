// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package com.legbehindneck.wudict;

import android.app.Activity;
import android.content.Intent;
import android.os.Bundle;

/**
 * The second door onto per-dictionary removal (D63).
 *
 * <p>Settings → Apps → WuWeiDict offers "Clear storage", which deletes the
 * whole library, the config and every imported file at once — the only storage
 * operation Android defines on an app's own data, and far too blunt for a user
 * who wants one dictionary gone. Declaring {@code android:manageSpaceActivity}
 * replaces that button with "Manage space" and hands the request to us.
 *
 * <p>This activity therefore owns no UI of its own: the app already has a
 * screen that lists dictionaries with their sizes and can delete them, so the
 * right answer is to open it. A second, Java-drawn management screen would be
 * a duplicate of the ☰ panel that could only drift from it. It is translucent
 * and finishes immediately, so the user sees the app open on the panel and
 * never a blank frame in between.
 */
public class ManageSpaceActivity extends Activity {

    /** Asks MainActivity to open the ☰ panel once the page is up. */
    static final String EXTRA_MANAGE = "com.legbehindneck.wudict.MANAGE_SPACE";

    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        // CLEAR_TOP + singleTask: a running instance is reused and gets this
        // through onNewIntent rather than being recreated behind a second copy.
        startActivity(new Intent(this, MainActivity.class)
                .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK | Intent.FLAG_ACTIVITY_CLEAR_TOP)
                .putExtra(EXTRA_MANAGE, true));
        finish();
    }
}
