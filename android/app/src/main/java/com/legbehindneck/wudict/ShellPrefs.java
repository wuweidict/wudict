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
import android.util.Base64;

import java.net.HttpURLConnection;
import java.security.SecureRandom;
import java.util.LinkedHashMap;
import java.util.Map;

final class ShellPrefs {

    private static final String FILE = "shell";

    // One key per way in, because the three carry different intent: a
    // selection-toolbar tap is a glance, a shared passage is deliberate, a
    // wudict:// call is programmatic. All default to false - float - so an
    // upgrade changes nothing for anyone.
    static final String TOOLBAR = "lookup_toolbar_in_app";
    static final String SHARE = "lookup_share_in_app";
    static final String LINK = "lookup_link_in_app";

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

    static SharedPreferences of(Context c) {
        return c.getSharedPreferences(FILE, Context.MODE_PRIVATE);
    }

    /** Whether lookups arriving this way skip the popup and open the app. */
    static boolean opensApp(Context c, String key) {
        return of(c).getBoolean(key, false);
    }

    static void set(Context c, String key, boolean on) {
        of(c).edit().putBoolean(key, on).apply();
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

    // Bounds that only the device can answer. Resolved by maxOf().
    private static final int MAX_RAM_MB = -1;
    private static final int MAX_CORES = -2;

    /**
     * One overridable config key. Everything the settings screen draws and
     * everything ServerProcess emits comes from this table, so a second
     * override is one entry and two strings - which is the point of D101;
     * NO_COMPRESS is merely its first instance.
     */
    static final class Override {
        final String key;      // the wudict config key
        final String flag;     // argv flag, or null to deliver through the environment
        final int kind;
        final String onValue;  // BOOL: what "on" emits
        final String offValue; // BOOL: what "off" means, for display only - never emitted
        final int min, max;    // COUNT/MEGABYTES, inclusive; negative = ask the device
        final int label, hint;

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
                new Override("NO_COMPRESS", null, BOOL, "1", "0", 0, 0,
                        R.string.settings_no_compress, R.string.settings_no_compress_hint),
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
