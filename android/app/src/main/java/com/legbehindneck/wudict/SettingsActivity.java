// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

// The shell's own settings (D100). Reached by long-pressing the launcher icon,
// which is the only entry point it has and deliberately so: the screen exists
// mainly so a setting that HIDES the lookup popup can still be undone, and
// every other route would cost either a second surface or another injection
// into a page the shell does not own.
//
// CHARTER, and it is the whole reason this class is allowed to exist: this
// screen holds ANDROID-SHELL FACTS ONLY - windows, intents, tasks,
// notifications, the process. A dictionary fact never appears here. Those
// belong to the page and the server, which own them, and the page must not
// learn that Android exists (D54). The test is simple: if a proposed row can
// be phrased without naming an Android concept, it does not go in this screen.
//
// D101 amends that charter in exactly one direction, and it is worth being
// precise about the shape of the hole. The Advanced section below sets GO
// CONFIG KEYS - but every row there passes a stricter test than the one above:
// its right value differs BECAUSE OF THIS DEVICE, and the shell is the only
// agent that knows the device. Storage headroom, RAM, cores, a port collision:
// device facts wearing a config key's name. Dictionary order, default search
// mode, theme: still not here, and a row that would read identically on a
// desktop still does not belong.
//
// Nothing here writes wudict.toml. An override is stored in SharedPreferences
// and delivered on the child's exec line, which is a HIGHER config layer than
// the file (flag > env > file > default), so the rule that Java never touches
// the user's config file is kept by construction rather than by discipline.
// Empty means emit nothing, so a row can only ever add a value, never
// countermand one the user wrote themselves.
//
// Views are built in code, like every other view in this app - there is no
// androidx and no res/layout, by design. The theme is the popup's own
// (Theme.WuWeiDict.Lookup): a floating window is exempt from the forced
// edge-to-edge that MainActivity.applyWindowInsets exists for, and it is
// dialog-shaped anyway.
//
// PROPORTION. The project's ratio is φ (D58's motion ladder, the stylesheet's
// --sp-* scale), and D70's lesson is that φ coordinates land between pixels:
// on an integer grid you use the Fibonacci integers, whose successive ratios
// are within half a percent of φ and which are whole numbers by definition. dp
// is an integer grid, so spacing here is 3·5·8·13·21·34·55 and the numeric
// field is 89. Type steps by √φ (1.272) rather than φ, because a full φ step
// from 13 sp lands on 21 and leaves nothing usable in between. Where beauty
// and ergonomics disagree, ergonomics wins: every interactive row is at least
// 55 dp tall, which is the ladder's own rung above Android's 48 dp touch
// minimum - the two agree here, which is why 55 and not 48.
package com.legbehindneck.wudict;

import android.app.Activity;
import android.app.AlertDialog;
import android.os.Bundle;
import android.text.InputType;
import android.util.TypedValue;
import android.view.Gravity;
import android.view.View;
import android.view.ViewGroup;
import android.view.WindowManager;
import android.widget.Button;
import android.widget.CheckBox;
import android.widget.EditText;
import android.widget.LinearLayout;
import android.widget.ScrollView;
import android.widget.TextView;
import android.widget.Toast;

import java.util.HashMap;
import java.util.LinkedHashMap;
import java.util.Map;

public class SettingsActivity extends Activity {

    // The Fibonacci dp ladder, and the two rungs that are ergonomic floors.
    private static final int SP_2 = 5, SP_3 = 8, SP_4 = 13, SP_5 = 21, SP_6 = 34;
    private static final int ROW_MIN = 55;   // ≥ the 48 dp touch minimum
    private static final int FIELD_W = 89;   // three digits, right-aligned column

    // 13 · 13√φ · 13φ
    private static final float TEXT_HINT = 13f, TEXT_LABEL = 16.5f, TEXT_HEAD = 21f;
    private static final float LINE_PHI = 1.618f;

    private final ShellPrefs.Override[] keys = ShellPrefs.OVERRIDES;
    private final CheckBox[] boxes = new CheckBox[keys.length];
    private final EditText[] fields = new EditText[keys.length];
    private final TextView[] hints = new TextView[keys.length];

    private TextView staleText;
    private Button applyNow;
    private volatile boolean gone;

    // What the running server answered, kept so that every later edit can be
    // re-tested against it without asking again: the server's own values
    // cannot change while it runs - only what this screen would spawn with can.
    private final Map<String, String> serverValues = new LinkedHashMap<>();
    private final Map<String, String> serverOrigins = new HashMap<>();
    private boolean serverAnswered;

    // The last value each key was seen resolving to, and whether that sighting
    // was a server answering just now or the cache from when one last did.
    private final String[] effective = new String[keys.length];
    private boolean effectiveLive;

    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        setTitle(R.string.settings_title);

        LinearLayout col = new LinearLayout(this);
        col.setOrientation(LinearLayout.VERTICAL);
        int pad = dp(SP_5);
        col.setPadding(pad, pad, pad, pad);

        col.addView(head(R.string.settings_lookup_head, 0));
        col.addView(caption(getString(R.string.settings_lookup_hint), SP_2, SP_3));

        // Written on toggle, so there is no Save button, no OK/Cancel and no
        // state machine - which is what makes the theme's
        // windowCloseOnTouchOutside safe: there is never an unsaved edit to
        // lose by tapping outside. The Advanced fields below commit on focus
        // loss and again in onPause, for the same reason.
        col.addView(row(ShellPrefs.TOOLBAR, R.string.settings_lookup_toolbar));
        col.addView(row(ShellPrefs.SHARE, R.string.settings_lookup_share));
        col.addView(row(ShellPrefs.LINK, R.string.settings_lookup_link));

        col.addView(head(R.string.settings_access_head, SP_6));
        col.addView(keyRow());
        col.addView(caption(getString(R.string.settings_access_key_hint), SP_2, SP_3));

        col.addView(head(R.string.settings_advanced_head, SP_6));
        col.addView(caption(getString(R.string.settings_advanced_hint), SP_2, SP_3));

        for (int i = 0; i < keys.length; i++) {
            col.addView(keys[i].kind == ShellPrefs.BOOL ? switchRow(i) : numberRow(i));
        }

        col.addView(restoreButton());

        staleText = caption("", SP_6, SP_2);
        staleText.setVisibility(View.GONE);
        col.addView(staleText);

        applyNow = new Button(this);
        applyNow.setText(R.string.settings_apply_now);
        applyNow.setVisibility(View.GONE);
        applyNow.setOnClickListener(v -> applyNow());
        col.addView(applyNow, wide(SP_2));

        // The way out, always present and always the same thing. It is not an
        // OK: there is nothing here to confirm, since every control writes as
        // it is used. It exists because a screen the user cannot see the end
        // of gives no sign that it is finished with them - and because with
        // the keyboard up, Back dismisses the keyboard first, so leaving would
        // otherwise cost two presses. Closing commits the fields on the way
        // out (onPause), exactly as Back and tapping outside already do.
        Button close = new Button(this);
        close.setText(R.string.settings_close);
        close.setOnClickListener(v -> finish());
        col.addView(close, wide(SP_6));

        ScrollView scroll = new ScrollView(this);
        scroll.addView(col, new ViewGroup.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT,
                ViewGroup.LayoutParams.WRAP_CONTENT));
        setContentView(scroll);

        // 92 % of the screen - the same proportion the web UI's .container
        // uses. Height stays WRAP_CONTENT: the platform caps a dialog at the
        // space available and the ScrollView takes the overflow, so a small
        // screen scrolls rather than clipping.
        getWindow().setLayout((int) (getResources().getDisplayMetrics().widthPixels * 0.92f),
                WindowManager.LayoutParams.WRAP_CONTENT);

        fillFromPrefs();
        askServer();
    }

    @Override
    protected void onPause() {
        super.onPause();
        // Leaving with a half-typed number in a field must not lose it, and
        // must not store a value the server would refuse either: commit()
        // either stores a valid number or puts the field back.
        for (int i = 0; i < keys.length; i++) {
            if (fields[i] != null) commit(i);
        }
    }

    @Override
    protected void onDestroy() {
        gone = true;
        super.onDestroy();
    }

    // ── rows ─────────────────────────────────────────────────────────────

    private CheckBox row(String key, int label) {
        CheckBox c = new CheckBox(this);
        c.setText(label);
        c.setTextSize(TypedValue.COMPLEX_UNIT_SP, TEXT_LABEL);
        c.setMinHeight(dp(ROW_MIN));
        // State first, listener second: painting the screen must never count
        // as the user having chosen anything.
        c.setChecked(ShellPrefs.opensApp(this, key));
        c.setOnCheckedChangeListener((v, on) -> ShellPrefs.set(this, key, on));
        return c;
    }

    /**
     * The access key switch. Not a {@link ShellPrefs.Override} row: it has no
     * inherited value to display and no server-reported effective value to
     * compare against, and its off-state is emitted rather than withheld
     * (ShellPrefs.REQUIRE_KEY). On by default, which is why it cannot reuse
     * {@link #row}.
     */
    private CheckBox keyRow() {
        CheckBox c = new CheckBox(this);
        c.setText(R.string.settings_access_key);
        c.setTextSize(TypedValue.COMPLEX_UNIT_SP, TEXT_LABEL);
        c.setMinHeight(dp(ROW_MIN));
        c.setChecked(ShellPrefs.requireKey(this));
        c.setOnCheckedChangeListener((v, on) ->
                ShellPrefs.set(this, ShellPrefs.REQUIRE_KEY, on));
        return c;
    }

    /** A BOOL override: the box is the whole control, its hint sits under it. */
    private View switchRow(int i) {
        ShellPrefs.Override o = keys[i];
        LinearLayout box = new LinearLayout(this);
        box.setOrientation(LinearLayout.VERTICAL);
        box.setPadding(0, dp(SP_2), 0, dp(SP_2));

        CheckBox c = new CheckBox(this);
        c.setText(o.label);
        c.setTextSize(TypedValue.COMPLEX_UNIT_SP, TEXT_LABEL);
        c.setMinHeight(dp(ROW_MIN));
        // State first, listener second - fillFromPrefs() repaints this box
        // after Restore defaults, and that must not write anything back.
        c.setChecked(ShellPrefs.override(this, o) != null);
        c.setOnCheckedChangeListener((v, on) -> {
            ShellPrefs.setOverride(this, o, on ? o.onValue : null);
            paintHint(i); // the address line below appears with the tick
            recheck();
        });
        boxes[i] = c;
        box.addView(c);

        hints[i] = caption(getString(o.hint), 0, SP_4);
        box.addView(hints[i]);
        return box;
    }

    /** A numeric override: label and hint on the left, an 89 dp field right. */
    private View numberRow(int i) {
        ShellPrefs.Override o = keys[i];
        LinearLayout box = new LinearLayout(this);
        box.setOrientation(LinearLayout.VERTICAL);
        box.setPadding(0, dp(SP_2), 0, dp(SP_2));

        LinearLayout line = new LinearLayout(this);
        line.setOrientation(LinearLayout.HORIZONTAL);
        line.setGravity(Gravity.CENTER_VERTICAL);
        line.setMinimumHeight(dp(ROW_MIN));

        TextView label = new TextView(this);
        label.setText(o.label);
        label.setTextSize(TypedValue.COMPLEX_UNIT_SP, TEXT_LABEL);
        line.addView(label, new LinearLayout.LayoutParams(0,
                ViewGroup.LayoutParams.WRAP_CONTENT, 1f));

        EditText f = new EditText(this);
        f.setInputType(InputType.TYPE_CLASS_NUMBER);
        f.setTextSize(TypedValue.COMPLEX_UNIT_SP, TEXT_LABEL);
        f.setGravity(Gravity.END);
        f.setSingleLine(true);
        f.setMinHeight(dp(ROW_MIN));
        // Committing on focus loss rather than on every keystroke: "1" on the
        // way to "128" is not an invalid value, it is an unfinished one.
        f.setOnFocusChangeListener((v, focused) -> {
            if (!focused) commit(i);
        });
        f.setOnEditorActionListener((v, id, ev) -> {
            commit(i);
            return false;
        });
        fields[i] = f;
        line.addView(f, new LinearLayout.LayoutParams(dp(FIELD_W),
                ViewGroup.LayoutParams.WRAP_CONTENT));
        box.addView(line);

        hints[i] = caption("", 0, SP_4);
        box.addView(hints[i]);
        return box;
    }

    private View restoreButton() {
        Button b = new Button(this);
        b.setText(R.string.settings_restore);
        // Confirmed, because one tap clears several tuned fields at once.
        // Clearing restores inheritance - it writes nothing anywhere.
        b.setOnClickListener(v -> new AlertDialog.Builder(this)
                .setMessage(R.string.settings_restore_confirm)
                .setNegativeButton(R.string.settings_cancel, null)
                .setPositiveButton(R.string.settings_restore, (d, w) -> {
                    ShellPrefs.clearOverrides(this);
                    fillFromPrefs();
                    toast(getString(R.string.settings_restore_done));
                    recheck();
                })
                .show());
        return wrap(b, SP_6);
    }

    // ── values ───────────────────────────────────────────────────────────

    /** Paints every control from what is stored right now. */
    private void fillFromPrefs() {
        for (int i = 0; i < keys.length; i++) {
            ShellPrefs.Override o = keys[i];
            String v = ShellPrefs.override(this, o);
            if (boxes[i] != null) {
                boxes[i].setChecked(v != null && v.equals(o.onValue));
            }
            if (fields[i] != null) {
                fields[i].setText(v == null ? "" : v);
            }
            paintHint(i, ShellPrefs.cachedEffective(this, o));
        }
    }

    /**
     * The hint under a row: what it costs, the range it accepts, and what the
     * key is resolving to right now. The last part is the reason an empty
     * field is legible at all - empty means "inherited", and this says
     * inherited from what.
     */
    private void paintHint(int i, String seen) {
        effective[i] = seen;
        paintHint(i);
    }

    private void paintHint(int i) {
        ShellPrefs.Override o = keys[i];
        String seen = effective[i];
        StringBuilder b = new StringBuilder(getString(o.hint));
        if (o.kind != ShellPrefs.BOOL) {
            b.append(' ').append(getString(R.string.settings_range, o.min, ShellPrefs.maxOf(this, o)));
            if (fields[i] != null) {
                // The placeholder IS the inherited value, in the field's own
                // grey - "chosen" and "inherited" then differ by colour alone,
                // with no extra chrome to explain the difference.
                fields[i].setHint(seen == null ? "" : display(o, seen));
            }
        }
        // "Now" may only be said of a server that answered just now. A cached
        // value is what was in effect the last time one ran, and quoting it in
        // the present tense is how a settings screen comes to contradict its
        // own controls - a ticked box over the words "In effect now:
        // 127.0.0.1", with nothing on screen to explain the disagreement.
        if (seen != null && effectiveLive) {
            b.append(' ').append(getString(R.string.settings_in_effect, seen));
        }
        // The one row whose value the user cannot look up anywhere else: while
        // it is on, say where this phone would be reached. Only while it is on
        // - an address printed under an unticked switch reads as an offer.
        if (boxes[i] != null && boxes[i].isChecked() && o.flag != null) {
            String ip = Net.lanAddress();
            b.append(' ').append(ip == null ? getString(R.string.settings_lan_none)
                    : getString(R.string.settings_lan_at, ip + ":" + ServerProcess.port(this)));
        }
        hints[i].setText(b.toString());
    }

    /** Stores a field's value, or puts the field back if it cannot be stored. */
    private void commit(int i) {
        ShellPrefs.Override o = keys[i];
        String stored = ShellPrefs.override(this, o);
        String typed = fields[i].getText().toString().trim();
        if (typed.isEmpty()) {
            if (stored != null) {
                ShellPrefs.setOverride(this, o, null);
                recheck();
            }
            return;
        }
        int max = ShellPrefs.maxOf(this, o);
        long n;
        try {
            n = Long.parseLong(typed);
        } catch (NumberFormatException e) {
            n = -1; // an unparseable value fails the same way an out-of-range one does
        }
        if (n < o.min || n > max) {
            toast(getString(R.string.settings_out_of_range, typed, o.min, max));
            fields[i].setText(stored == null ? "" : stored);
            return;
        }
        String v = String.valueOf(n);
        if (!v.equals(stored)) {
            ShellPrefs.setOverride(this, o, v);
            fields[i].setText(v); // canonical form: "0128" is stored as "128"
            recheck();
        }
    }

    /** A stored or effective value as the field shows it: bare, in MB. */
    private String display(ShellPrefs.Override o, String value) {
        if (o.kind != ShellPrefs.MEGABYTES) return value;
        long bytes = parseSize(value);
        return bytes < 0 ? value : String.valueOf(bytes >> 20);
    }

    /**
     * Reads what config.FormatSize writes: a number with an optional B/KB/MB/GB
     * suffix. Returns -1 for anything else, which is treated as "unknown"
     * rather than guessed at.
     */
    private static long parseSize(String s) {
        String t = s.trim().toUpperCase();
        int shift = 0;
        if (t.endsWith("GB")) {
            shift = 30;
            t = t.substring(0, t.length() - 2);
        } else if (t.endsWith("MB")) {
            shift = 20;
            t = t.substring(0, t.length() - 2);
        } else if (t.endsWith("KB")) {
            shift = 10;
            t = t.substring(0, t.length() - 2);
        } else if (t.endsWith("B")) {
            t = t.substring(0, t.length() - 1);
        }
        try {
            return Long.parseLong(t.trim()) << shift;
        } catch (NumberFormatException e) {
            return -1;
        }
    }

    // ── what the running server is actually running with ─────────────────
    //
    // Several of these defaults are computed by Go from the device itself
    // (internal/config/tuning.go), so Java must not recompute them - it asks.
    // /api/config also answers the second question this screen has: whether a
    // server that is already running was started with different values.

    private void askServer() {
        final int port = ServerProcess.port(this);
        Thread t = new Thread(() -> {
            Map<String, String> values = new LinkedHashMap<>();
            Map<String, String> origins = new HashMap<>();
            boolean answered = ServerProcess.fetchEffective(port, values, origins);
            if (gone) return;
            if (answered) ShellPrefs.cacheEffective(this, values);
            runOnUiThread(() -> {
                if (gone) return;
                serverAnswered = answered;
                effectiveLive = answered;
                serverValues.clear();
                serverOrigins.clear();
                serverValues.putAll(values);
                serverOrigins.putAll(origins);
                for (int i = 0; i < keys.length; i++) {
                    String v = answered ? values.get(keys[i].key)
                            : ShellPrefs.cachedEffective(this, keys[i]);
                    paintHint(i, v);
                }
                recheck();
            });
        }, "wudict-settings");
        t.setDaemon(true);
        t.start();
    }

    /**
     * Re-tests the running server against what this screen would spawn with
     * now. Called after every stored change, because a footer that was only
     * computed when the screen opened could never appear in the session that
     * made the change - which is the only session in which it is useful.
     * Cheap: no request, only the maps the one request already brought back.
     */
    private void recheck() {
        showStale(serverAnswered && isStale(serverValues, serverOrigins));
    }

    /**
     * Whether the running server would be started differently now. Two ways
     * that happens: a value we WOULD pass differs from the one it has, or we
     * would pass nothing for a key it received from a previous exec line -
     * origin "env" or "flag" is the shell's own fingerprint, since nothing
     * else on this device sets the child's environment.
     */
    private boolean isStale(Map<String, String> values, Map<String, String> origins) {
        for (ShellPrefs.Override o : keys) {
            String has = values.get(o.key);
            if (has == null) continue;
            String want = ShellPrefs.emitted(this, o);
            if (want == null) {
                String from = origins.get(o.key);
                if ("env".equals(from) || "flag".equals(from)) return true;
                continue;
            }
            if (!same(o, want, has)) return true;
        }
        return false;
    }

    /** Value equality in the key's own terms: sizes by bytes, the rest literally. */
    private static boolean same(ShellPrefs.Override o, String a, String b) {
        if (o.kind == ShellPrefs.MEGABYTES) {
            long x = parseSize(a), y = parseSize(b);
            return x >= 0 && x == y;
        }
        return a.equals(b);
    }

    private void showStale(boolean stale) {
        if (!stale) {
            staleText.setVisibility(View.GONE);
            applyNow.setVisibility(View.GONE);
            return;
        }
        // The button appears only when stopping the server cannot take
        // anything away from anyone: no window is holding it and no ingest is
        // in flight. Otherwise the same notice appears without it, saying the
        // true thing rather than promising a restart that will not happen.
        boolean safe = ServerProcess.holders() == 0 && !IndexService.isBusy();
        staleText.setText(getString(R.string.settings_stale)
                + (safe ? "" : " " + getString(R.string.settings_stale_later)));
        staleText.setVisibility(View.VISIBLE);
        applyNow.setVisibility(safe ? View.VISIBLE : View.GONE);
    }

    private void applyNow() {
        applyNow.setEnabled(false);
        Thread t = new Thread(() -> {
            ServerProcess.stopAny(this);
            if (gone) return;
            runOnUiThread(() -> {
                if (gone) return;
                applyNow.setEnabled(true);
                showStale(false);
                toast(getString(R.string.settings_applied));
            });
        }, "wudict-apply");
        t.setDaemon(true);
        t.start();
    }

    // ── the ladder ───────────────────────────────────────────────────────

    private TextView head(int text, int topDp) {
        TextView t = new TextView(this);
        t.setText(text);
        t.setTextSize(TypedValue.COMPLEX_UNIT_SP, TEXT_HEAD);
        t.setPadding(0, dp(topDp), 0, 0);
        return t;
    }

    private TextView caption(String text, int topDp, int bottomDp) {
        TextView t = new TextView(this);
        t.setText(text);
        t.setTextSize(TypedValue.COMPLEX_UNIT_SP, TEXT_HINT);
        t.setLineSpacing(0f, LINE_PHI);
        t.setAlpha(0.7f);
        t.setPadding(0, dp(topDp), 0, dp(bottomDp));
        return t;
    }

    private View wrap(View v, int topDp) {
        LinearLayout box = new LinearLayout(this);
        box.setOrientation(LinearLayout.VERTICAL);
        box.setPadding(0, dp(topDp), 0, 0);
        box.addView(v, new LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.WRAP_CONTENT, ViewGroup.LayoutParams.WRAP_CONTENT));
        return box;
    }

    private LinearLayout.LayoutParams wide(int topDp) {
        LinearLayout.LayoutParams p = new LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT);
        p.topMargin = dp(topDp);
        return p;
    }

    private void toast(String message) {
        Toast.makeText(this, message, Toast.LENGTH_LONG).show();
    }

    private int dp(float v) {
        return (int) TypedValue.applyDimension(TypedValue.COMPLEX_UNIT_DIP, v,
                getResources().getDisplayMetrics());
    }
}
