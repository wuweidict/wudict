// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

// SHELL FACTS: the fourth category of state in this project, and the only one
// that belongs to Java (D100).
//
// The other three are spoken for. Collection facts - which dictionaries are
// searched, in what order - live in the server's state.json. Install facts -
// folders, port, memory limits - live in wudict.toml, which this shell seeds
// once and never reads back (ServerProcess.seedConfig). Browser-view facts -
// theme, wide mode - live in the page's localStorage.
//
// A shell fact is none of those: it is about the Android shell's own
// behaviour, and two properties force it here rather than into any of them.
//
// It must be READABLE BY THE DECIDER AT DECISION TIME. LookupActivity chooses
// a window before ServerProcess.ensure() is even called, so there may be no
// server and no WebView: localStorage is unreachable by construction (it is
// keyed by origin and needs a live page), and a running server cannot be
// assumed.
//
// And it must be OWNED BY WHOEVER THE VALUE IS ABOUT. state.json is readable
// from Java - it is JSON, and Prefs.Replace writes it temp-file + rename, so a
// read cannot tear - but it is the SERVER's file: heal() rewrites it, Replace()
// is its only writer, and its `version` exists so a format change can be
// recognised rather than guessed at. Putting a value there that Go would never
// read turns every future change to that schema into a cross-language
// compatibility event, invisible from the Go side. A store whose owner never
// reads the value is the wrong store.
//
// SharedPreferences is the only store that passes both: same process as the
// decider, synchronous, no parse, no schema, no version negotiation, and a
// missing key is simply the default.
//
// ── PLATFORM OVERRIDES (D101) ────────────────────────────────────────────────
//
// The second half of this file stores a different kind of value: a Go config
// key whose right answer differs BECAUSE OF THIS DEVICE. It is still a shell
// fact - the device is the shell's subject - and it is still stored here, but
// it is DELIVERED into the config layering rather than read by Java.
//
// config.Load resolves every key as flag > env > file > default. The shell
// already writes the top two layers on every spawn, so an override rides a
// layer that OUTRANKS wudict.toml and never reads it: the rule that Java must
// not parse or rewrite the user's config file stands untouched, and nothing
// new had to be invented to keep it.
//
// Two states, never three. Set means "emit this value"; unset means "emit
// nothing", so the file and the built-in default decide. The shell never emits
// a NEGATING value, so a control on the settings screen can never silently
// countermand something the user wrote in wudict.toml themselves.
package com.legbehindneck.wudict;

import android.app.ActivityManager;
import android.content.Context;
import android.content.Intent;
import android.content.SharedPreferences;
import android.content.res.Configuration;
import android.graphics.Color;
import android.util.Base64;

import java.net.HttpURLConnection;
import java.security.SecureRandom;
import java.util.LinkedHashMap;
import java.util.Map;

final class ShellPrefs {

    private static final String FILE = "shell";

    // One key per way in, because the four carry different intent: a
    // selection-toolbar tap is a glance, a shared passage is deliberate, a
    // wudict:// call is programmatic, and a reader's dictionary button is the
    // reader's habit, configured once in the reader. All default to false -
    // float - so an upgrade changes nothing for anyone.
    static final String TOOLBAR = "lookup_toolbar_in_app";
    static final String SHARE = "lookup_share_in_app";
    static final String LINK = "lookup_link_in_app";
    static final String READER = "lookup_reader_in_app";

    // Whether the app's own window hides the system bars while it is read.
    // A shell fact by the same two tests as the four above: the WINDOW is the
    // subject, and the decider - MainActivity, painting its first frame - has
    // no page and no server to ask. The popup is exempt by construction: a
    // floating window does not own the bars, so this key is read in one place.
    //
    // SUPERSEDED by BARS, which says WHICH bars rather than merely whether.
    // Kept because migrate() reads it: an upgrade must not silently turn a
    // user's full-screen reading off.
    private static final String IMMERSIVE = "immersive";

    // ── the display edge (D141) ──────────────────────────────────────────────
    //
    // On a cutout device the strip behind the camera is painted by THIS APP and
    // by nothing else. At targetSdk 36 the platform has taken every other lever
    // away: setStatusBarColor and setNavigationBarColor are no-ops from API 35,
    // layoutInDisplayCutoutMode is forced to ALWAYS for non-floating windows, and
    // windowOptOutEdgeToEdgeEnforcement was disabled for API 36. What is left is
    // MainActivity.applyWindowInsets padding the root and that padding showing a
    // colour - so the entire design space is which colour, and whether to pad.
    //
    // Which is why this is a shell fact and not a page preference: the decider
    // paints the first frame before any page or server exists, and the thing
    // being decided is a window, not a document.
    static final String EDGE_MODE = "edge_mode";

    /** The strip follows the OS day/night setting - @color/window_bg. */
    static final int EDGE_SYSTEM = 0;
    /** The strip follows the PAGE's own theme, whatever it last reported. */
    static final int EDGE_PAGE = 1;
    /** The strip is black, whatever anything else says. */
    static final int EDGE_BLACK = 2;
    /** The strip is {@link #EDGE_COLOR}. */
    static final int EDGE_CUSTOM = 3;
    /** There is no strip: the page is given the insets and paints them itself. */
    static final int EDGE_NONE = 4;

    private static final String EDGE_COLOR = "edge_color";

    // The page's resolved theme, as the page itself last reported it through
    // the wudict://theme channel. Cached here for ONE reason: the first frame
    // is painted before a WebView has loaded anything, so without a remembered
    // answer EDGE_PAGE would have to guess on every cold start. It is written
    // by MainActivity and read by nobody else.
    private static final String PAGE_DARK = "page_dark";

    // ── which system bars hide (D141) ────────────────────────────────────────
    //
    // A BITMASK, not an enum, because the four states are exactly the subsets of
    // a two-element set and the mask is what WindowInsetsController wants anyway.
    static final String BARS = "hide_bars";

    static final int BARS_OFF = 0;
    static final int BARS_STATUS = 1;
    static final int BARS_NAV = 2;
    static final int BARS_BOTH = BARS_STATUS | BARS_NAV;

    // ── the access key ───────────────────────────────────────────────────────
    //
    // Android has no per-app loopback: 127.0.0.1:6888 is reachable by every
    // other app on the device that holds INTERNET, which is nearly all of
    // them. Nothing about the server's own defaults can fix that - it is a
    // property of the platform's network stack - so the shell generates a
    // secret and gives it only to its own WebView. That is why this defaults
    // to ON here and to OFF on the desktop, where a loopback socket really is
    // private to the account that owns it.
    //
    // The switch is a shell fact rather than a platform override (D101): both
    // its states have to be emitted. An override's off-state emits NOTHING, so
    // that wudict.toml keeps the last word - but here "nothing" would let AUTH
    // default to "auto", and on a FOSS build serving the LAN, auto means ON.
    // A user who turned the key off would be given one anyway. So AUTH joins
    // SERVER_IP and SERVER_PORT in the small set of keys the shell owns
    // outright and always emits, and the D101 rule stands for every key a user
    // might reasonably write in the file themselves.
    static final String REQUIRE_KEY = "require_access_key";
    private static final String KEY_TOKEN = "access_key";

    private ShellPrefs() {
    }

    private static boolean migrated;

    static SharedPreferences of(Context c) {
        SharedPreferences p = c.getSharedPreferences(FILE, Context.MODE_PRIVATE);
        migrate(p);
        return p;
    }

    /** A stored NO_COMPRESS override, which migrate() maps to the COMPRESSION picker (D191). */
    private static final String OLD_NO_COMPRESS = "override_NO_COMPRESS";

    /**
     * Brings a preference file written by an older version up to date, once per
     * process.
     *
     * <p>Hung off {@link #of} rather than called from an activity because it has
     * to run BEFORE the file is written, and three activities plus a service can
     * each be the first thing this process starts. Every read and every write in
     * this class goes through {@code of()}, so no caller can beat it - which is
     * what makes the "is this file empty?" test below mean what it says.
     *
     * <p>Each step is guarded by {@code contains()}, so running it twice is the
     * same as running it once and a user's later choice is never reverted.
     */
    private static synchronized void migrate(SharedPreferences p) {
        if (migrated) return;
        migrated = true;
        SharedPreferences.Editor e = null;

        if (!p.contains(EDGE_MODE)) {
            // An EXISTING install keeps what it has always looked like; only a
            // genuinely new one gets the better default. An upgrade is
            // recognised by the file having anything in it at all - every
            // install that has ever started a server has an access key here.
            boolean fresh = p.getAll().isEmpty();
            e = p.edit().putInt(EDGE_MODE, fresh ? EDGE_PAGE : EDGE_SYSTEM);
        }

        if (!p.contains(BARS)) {
            int v = p.getBoolean(IMMERSIVE, false) ? BARS_BOTH : BARS_OFF;
            if (e == null) e = p.edit();
            e.putInt(BARS, v);
        }

        // A NO_COMPRESS override is the COMPRESSION picker's "none" when it was
        // ticked; unticked, it stored nothing (D191).
        if (p.contains(OLD_NO_COMPRESS)) {
            if (e == null) e = p.edit();
            if ("1".equals(p.getString(OLD_NO_COMPRESS, null)) && !p.contains("override_COMPRESSION")) {
                e.putString("override_COMPRESSION", "none");
            }
            e.remove(OLD_NO_COMPRESS);
        }

        if (e != null) e.apply();
    }

    /** Whether lookups arriving this way skip the popup and open the app. */
    static boolean opensApp(Context c, String key) {
        return of(c).getBoolean(key, false);
    }

    static void set(Context c, String key, boolean on) {
        of(c).edit().putBoolean(key, on).apply();
    }

    /** Which system bars the app window hides. A {@code BARS_*} mask. */
    static int bars(Context c) {
        return of(c).getInt(BARS, BARS_OFF);
    }

    static void setBars(Context c, int mask) {
        of(c).edit().putInt(BARS, mask & BARS_BOTH).apply();
    }

    /** How the window's edges are painted. One of the {@code EDGE_*} constants. */
    static int edgeMode(Context c) {
        int v = of(c).getInt(EDGE_MODE, EDGE_SYSTEM);
        return v < EDGE_SYSTEM || v > EDGE_NONE ? EDGE_SYSTEM : v;
    }

    static void setEdgeMode(Context c, int mode) {
        of(c).edit().putInt(EDGE_MODE, mode).apply();
    }

    /** The stored custom strip colour. Opaque black until the user picks one. */
    static int edgeColorValue(Context c) {
        return of(c).getInt(EDGE_COLOR, 0xFF000000);
    }

    static void setEdgeColorValue(Context c, int argb) {
        of(c).edit().putInt(EDGE_COLOR, 0xFF000000 | argb).apply();
    }

    /** What the page last reported its own resolved theme to be. */
    static boolean pageDark(Context c) {
        // Falls back to the OS, which is what the page's own default - "auto" -
        // resolves to anyway, so a first-ever launch guesses the way the page
        // is about to decide rather than at random.
        return of(c).getBoolean(PAGE_DARK, systemDark(c));
    }

    /** Records the page's resolved theme. True when the value changed. */
    static boolean setPageDark(Context c, boolean dark) {
        SharedPreferences p = of(c);
        if (p.contains(PAGE_DARK) && p.getBoolean(PAGE_DARK, false) == dark) return false;
        p.edit().putBoolean(PAGE_DARK, dark).apply();
        return true;
    }

    static boolean systemDark(Context c) {
        return (c.getResources().getConfiguration().uiMode
                & Configuration.UI_MODE_NIGHT_MASK) == Configuration.UI_MODE_NIGHT_YES;
    }

    /** The page's own background colour, per whatever theme it is showing. */
    static int pageBg(Context c) {
        return c.getColor(pageDark(c) ? R.color.page_bg_dark : R.color.page_bg_light);
    }

    /**
     * The colour the shell paints into the inset padding - the strip behind the
     * camera, and the band under the gesture bar.
     *
     * <p>{@link #EDGE_NONE} has no strip, but the window still needs a colour to
     * paint before the first frame and behind a side cutout, and the page's own
     * background is the only answer that cannot flash.
     */
    static int edgeColor(Context c) {
        switch (edgeMode(c)) {
            case EDGE_BLACK:
                return 0xFF000000;
            case EDGE_CUSTOM:
                return edgeColorValue(c);
            case EDGE_PAGE:
            case EDGE_NONE:
                return pageBg(c);
            default:
                return c.getColor(R.color.window_bg);
        }
    }

    /**
     * Whether the system bars should draw their icons DARK - which they must
     * whenever the colour behind them is light.
     *
     * <p>Decided by contrast rather than by the day/night setting, because a
     * custom colour has no day/night setting to consult and a strip that follows
     * the page can disagree with the OS outright. The two candidate contrast
     * ratios are computed and the better one wins, so there is no threshold here
     * to be wrong about: it is arithmetic, not a preference, which is why it is
     * not a row on the settings screen.
     */
    static boolean darkIcons(int argb) {
        double l = luminance(argb);
        double onDark = (l + 0.05) / 0.05;          // black icons over this colour
        double onLight = 1.05 / (l + 0.05);         // white icons over this colour
        return onDark >= onLight;
    }

    /** WCAG 2.x relative luminance. */
    private static double luminance(int argb) {
        return 0.2126 * channel(Color.red(argb))
                + 0.7152 * channel(Color.green(argb))
                + 0.0722 * channel(Color.blue(argb));
    }

    private static double channel(int v) {
        double s = v / 255.0;
        return s <= 0.03928 ? s / 12.92 : Math.pow((s + 0.055) / 1.055, 2.4);
    }

    /** Whether this install asks its server for an access key. Default: yes. */
    static boolean requireKey(Context c) {
        return of(c).getBoolean(REQUIRE_KEY, true);
    }

    // tokenCache exists for the same reason portCache does (ServerProcess):
    // PowerSignal is all statics and has no Context to ask. Primed by
    // token(Context), which every path that starts or adopts a server calls
    // first, so a request made without one is a request to a server this app
    // never asked for.
    private static volatile String tokenCache = "";

    /**
     * This install's access key, generated on first use and kept in the app's
     * private preferences - which is exactly the boundary the key is meant to
     * draw, since no other app can read them. Empty when the key is off.
     */
    static String token(Context c) {
        if (!requireKey(c)) {
            tokenCache = "";
            return "";
        }
        SharedPreferences p = of(c);
        String t = p.getString(KEY_TOKEN, null);
        if (t == null || t.isEmpty()) {
            byte[] b = new byte[32];
            new SecureRandom().nextBytes(b);
            // URL_SAFE | NO_PADDING: it travels as a query parameter, a cookie
            // value and an environment variable, and must survive all three
            // without escaping. NO_WRAP because Base64 otherwise emits a
            // newline every 76 characters.
            t = Base64.encodeToString(b, Base64.URL_SAFE | Base64.NO_WRAP | Base64.NO_PADDING);
            p.edit().putString(KEY_TOKEN, t).apply();
        }
        tokenCache = t;
        return t;
    }

    /** The last key {@link #token(Context)} resolved. For callers with no Context. */
    static String token() {
        return tokenCache;
    }

    /** Adds the key to a request this shell makes to its own server. */
    static void authorize(HttpURLConnection c) {
        String t = token();
        if (!t.isEmpty()) c.setRequestProperty("Authorization", "Bearer " + t);
    }

    /**
     * AUTH and AUTH_TOKEN for a spawn. Always both, never neither: see
     * REQUIRE_KEY. Kept out of {@link #env} because the key is not an
     * override - it has no settings row of that shape, no inherited value to
     * display, and must never appear in the effective-values cache.
     */
    static Map<String, String> authEnv(Context c) {
        Map<String, String> out = new LinkedHashMap<>();
        String t = token(c);
        out.put("AUTH", t.isEmpty() ? "off" : "on");
        if (!t.isEmpty()) out.put("AUTH_TOKEN", t);
        return out;
    }

    /**
     * Which key governs this launch. An action we do not recognise - or none at
     * all, which a hand-written intent can easily arrive with - falls to
     * TOOLBAR, whose default is the floating window: an unrecognised caller
     * gets the least intrusive outcome rather than a hijacked task.
     */
    static String sourceKey(Intent i) {
        // Before VIEW: Dictan's dispatcher is a VIEW told apart by its category.
        if (LookupActivity.fromReader(i)) return READER;
        String action = i == null ? null : i.getAction();
        if (Intent.ACTION_SEND.equals(action)) return SHARE;
        if (Intent.ACTION_VIEW.equals(action)) return LINK;
        return TOOLBAR;
    }

    // ── platform overrides ───────────────────────────────────────────────────

    /** A switch: stored on/off, emitted as {@link Override#onValue} when on. */
    static final int BOOL = 0;
    /** A count: stored and emitted verbatim, validated against [min, max]. */
    static final int COUNT = 1;
    /** Megabytes: stored as a bare number, emitted with the "MB" suffix. */
    static final int MEGABYTES = 2;
    /**
     * One of {@link Override#values}: stored and emitted verbatim. Unset leaves
     * the key to the config; the first option is the config's own default.
     */
    static final int CHOICE = 3;

    // Bounds that only the device can answer. Resolved by maxOf().
    private static final int MAX_RAM_MB = -1;
    private static final int MAX_CORES = -2;

    /**
     * One overridable config key. Everything the settings screen draws and
     * everything ServerProcess emits comes from this table, so a second
     * override is one entry and two strings - which is the point of D101.
     */
    static final class Override {
        final String key;      // the wudict config key
        final String flag;     // argv flag, or null to deliver through the environment
        final int kind;
        final String onValue;  // BOOL: what "on" emits
        final String offValue; // BOOL: what "off" means, for display only - never emitted
        final int min, max;    // COUNT/MEGABYTES, inclusive; negative = ask the device
        final int label, hint;
        final String[] values; // CHOICE: the config values, index 0 being the default
        final int options;     // CHOICE: string-array of the option labels
        final int optionHints; // CHOICE: string-array of one hint per option

        // Package-private, not private: the per-flavour Net (D62) builds the
        // listen-address rows, and only the flavour that HAS one compiles it.
        Override(String key, String flag, int kind, String onValue, String offValue,
                         int min, int max, int label, int hint) {
            this.key = key;
            this.flag = flag;
            this.kind = kind;
            this.onValue = onValue;
            this.offValue = offValue;
            this.min = min;
            this.max = max;
            this.label = label;
            this.hint = hint;
            this.values = null;
            this.options = 0;
            this.optionHints = 0;
        }

        /** A CHOICE override, delivered through the environment. */
        Override(String key, String[] values, int options, int optionHints, int label, int hint) {
            this.key = key;
            this.flag = null;
            this.kind = CHOICE;
            this.onValue = null;
            this.offValue = null;
            this.min = 0;
            this.max = 0;
            this.label = label;
            this.hint = hint;
            this.values = values;
            this.options = options;
            this.optionHints = optionHints;
        }

        /** The option a value names, or -1 for none or an unknown one. */
        int indexOf(String v) {
            if (v != null) {
                for (int i = 0; i < values.length; i++) {
                    if (values[i].equals(v)) return i;
                }
            }
            return -1;
        }

        String pref() {
            return "override_" + key;
        }
    }

    /**
     * The listen address the child binds to. Not the address anything CONNECTS
     * to: you connect to a loopback address, never to 0.0.0.0, so opening the
     * server to the local network deliberately leaves the WebView's origin -
     * and with it every page preference stored against that origin - alone.
     */
    static final String DEFAULT_IP = "127.0.0.1";
    /** D52: fixed, because localStorage is keyed by origin. */
    static final int DEFAULT_PORT = 6888;

    private static final int MIN_PORT = 1024, MAX_PORT = 65535;

    // Flags carry only what the shell must know DETERMINISTICALLY: it has to
    // build the WebView's URL before it can ask anyone anything, so the listen
    // address and port cannot be discovered after the fact. Flags outrank the
    // environment, which also means no override here can leave the app unable
    // to find its own server. Everything else goes through the environment,
    // where it is ranked above wudict.toml and reported as origin "env".
    // Assembled rather than written out, because ONE row is flavour-specific:
    // the listen address exists in the FOSS build and does not exist in the
    // Play build (Net, D62). Order is the screen's order, and the Net rows
    // keep the position SERVER_IP has always had - above the port.
    static final Override[] OVERRIDES = table();

    private static Override[] table() {
        Override[] head = {
                // D191: index 0 is zstd, the config's default.
                new Override("COMPRESSION", new String[]{"zstd", "deflate", "none"},
                        R.array.settings_compression_modes, R.array.settings_compression_hints,
                        R.string.settings_compression, R.string.settings_compression_hint),
                new Override("SEARCH_MEMORY", null, MEGABYTES, null, null, 0, MAX_RAM_MB,
                        R.string.settings_search_memory, R.string.settings_search_memory_hint),
                new Override("PREVIEW_MEMORY", null, MEGABYTES, null, null, 0, MAX_RAM_MB,
                        R.string.settings_preview_memory, R.string.settings_preview_memory_hint),
                new Override("INDEX_WORKERS", null, COUNT, null, null, 1, MAX_CORES,
                        R.string.settings_index_workers, R.string.settings_index_workers_hint),
        };
        Override[] net = Net.overrides();
        Override port = new Override("SERVER_PORT", "--port", COUNT, null, null, MIN_PORT, MAX_PORT,
                R.string.settings_server_port, R.string.settings_server_port_hint);

        Override[] out = new Override[head.length + net.length + 1];
        System.arraycopy(head, 0, out, 0, head.length);
        System.arraycopy(net, 0, out, head.length, net.length);
        out[out.length - 1] = port;
        return out;
    }

    /** The stored override, or null when the key is left to the config. */
    static String override(Context c, Override o) {
        String v = of(c).getString(o.pref(), null);
        return v == null || v.isEmpty() ? null : v;
    }

    /** Stores an override; null or empty withdraws it. */
    static void setOverride(Context c, Override o, String value) {
        SharedPreferences.Editor e = of(c).edit();
        if (value == null || value.isEmpty()) {
            e.remove(o.pref());
        } else {
            e.putString(o.pref(), value);
        }
        e.apply();
    }

    static void clearOverrides(Context c) {
        SharedPreferences.Editor e = of(c).edit();
        for (Override o : OVERRIDES) e.remove(o.pref());
        e.apply();
    }

    static boolean anyOverride(Context c) {
        for (Override o : OVERRIDES) {
            if (override(c, o) != null) return true;
        }
        return false;
    }

    /**
     * The value this key would resolve to if the server started now - or null
     * when nothing here sets it and the config's own layers decide. The two
     * flag keys always have an answer, because the shell always passes them.
     */
    static String emitted(Context c, Override o) {
        // SERVER_IP is resolved, not echoed: what the switch stores is a
        // marker, what the child is passed is an address the device holds
        // right now (Net.bindIp). The settings screen compares this against
        // what the RUNNING server reports, so resolving here is also what
        // makes a roam show up as "restart to apply" instead of as silence.
        if ("SERVER_IP".equals(o.key)) return bindIp(c);
        String v = override(c, o);
        if (v == null) {
            if ("SERVER_PORT".equals(o.key)) return String.valueOf(DEFAULT_PORT);
            return null;
        }
        return o.kind == MEGABYTES ? v + "MB" : v;
    }

    /** The environment additions for a spawn: the env-delivered overrides. */
    static Map<String, String> env(Context c) {
        Map<String, String> out = new LinkedHashMap<>();
        for (Override o : OVERRIDES) {
            if (o.flag != null) continue;
            String v = emitted(c, o);
            if (v != null) out.put(o.key, v);
        }
        return out;
    }

    /** The table entry for a key. Never null for a key spelled as in OVERRIDES. */
    static Override byKey(String key) {
        for (Override o : OVERRIDES) {
            if (o.key.equals(key)) return o;
        }
        throw new IllegalArgumentException(key);
    }

    /**
     * The bind address for --ip. Never used to connect - see DEFAULT_IP. The
     * answer is the flavour's (Net): the Play build has no listen-address row
     * at all, so there is no key here to look up.
     */
    static String bindIp(Context c) {
        return Net.bindIp(c);
    }

    /** The port the server listens on and every URL in this shell connects to. */
    static int port(Context c) {
        String v = override(c, byKey("SERVER_PORT"));
        if (v == null) return DEFAULT_PORT;
        try {
            int n = Integer.parseInt(v);
            return n >= MIN_PORT && n <= MAX_PORT ? n : DEFAULT_PORT;
        } catch (NumberFormatException e) {
            return DEFAULT_PORT; // a value we cannot use is a value we do not use
        }
    }

    /**
     * The upper bound for a numeric row, resolved against the device rather
     * than a constant: RAM for the memory caps, cores for the workers. A phone
     * with more of either simply gets more room, with nothing to revisit.
     */
    static int maxOf(Context c, Override o) {
        if (o.max == MAX_RAM_MB) return (int) Math.max(64, deviceRamMb(c));
        if (o.max == MAX_CORES) return Math.max(1, Runtime.getRuntime().availableProcessors());
        return o.max;
    }

    static long deviceRamMb(Context c) {
        ActivityManager am = (ActivityManager) c.getSystemService(Context.ACTIVITY_SERVICE);
        if (am == null) return 4096; // unreachable in practice; a sane phone-sized answer
        ActivityManager.MemoryInfo mi = new ActivityManager.MemoryInfo();
        am.getMemoryInfo(mi);
        return mi.totalMem >> 20;
    }

    // ── what the running server reported ─────────────────────────────────────
    //
    // Several defaults - the memory caps above all - are computed by Go from
    // the device itself (internal/config/tuning.go), so Java must never
    // recompute them: it asks /api/config instead. The last answer is cached
    // here so the screen can still show inherited values when no server is up.

    private static final String CACHE = "effective_";

    static void cacheEffective(Context c, Map<String, String> values) {
        SharedPreferences.Editor e = of(c).edit();
        for (Override o : OVERRIDES) {
            String v = values.get(o.key);
            if (v == null) {
                e.remove(CACHE + o.key);
            } else {
                e.putString(CACHE + o.key, v);
            }
        }
        e.apply();
    }

    /** The last value this key was seen resolving to, or null if never seen. */
    static String cachedEffective(Context c, Override o) {
        return of(c).getString(CACHE + o.key, null);
    }
}
