// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Read-aloud for the WebView (D148). The page speaks through the Web Speech
// API and nothing else; Android's WebView has that API's objects but no voices
// behind them - getVoices() is empty and speak() is silent - so the shell puts
// a working one in their place, over the platform TextToSpeech.
//
// The channel is the theme watcher's (MainActivity.THEME_PATH): a script the
// SHELL injects, reaching back through same-origin subresource requests that
// shouldInterceptRequest answers here and the server never sees. No
// @JavascriptInterface, no HTTP surface, and web/index.html still knows
// nothing about Android (D54) - speak.js cannot tell this engine from a
// browser's, which is the point.
//
// Nothing is constructed until the page first asks for the voice list, which
// speak.js does only once a reader has selected article text: a reader who
// never does never binds a TTS engine.
package com.legbehindneck.wudict;

import android.app.Activity;
import android.content.ActivityNotFoundException;
import android.content.Intent;
import android.media.AudioAttributes;
import android.media.session.MediaController;
import android.media.session.MediaSession;
import android.media.session.PlaybackState;
import android.net.Uri;
import android.speech.tts.TextToSpeech;
import android.speech.tts.UtteranceProgressListener;
import android.speech.tts.Voice;
import android.util.Log;
import android.webkit.WebResourceResponse;
import android.webkit.WebView;

import org.json.JSONArray;
import org.json.JSONException;
import org.json.JSONObject;

import java.io.ByteArrayInputStream;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.Comparator;
import java.util.HashMap;
import java.util.HashSet;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.MissingResourceException;
import java.util.Set;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.TimeUnit;

final class Speech {

    private static final String TAG = "wudict";
    private static final String PATH = "/__wudict-shell/tts/";

    // The page's side: speechSynthesis and SpeechSynthesisUtterance, the subset
    // speak.js uses. One utterance at a time goes to the engine; the next is
    // sent when the engine reports the last one done, so cancel() never has to
    // chase a queue it cannot see. `g` is the cancel generation: a speak that
    // was already on its way when cancel() ran carries the old one and is
    // dropped on arrival, and nothing new is sent until the stop has landed,
    // because two requests may be answered on two threads in either order.
    private static final String JS =
            "(function(){if(window.__wdSpeech)return;"
          + "var P='" + PATH + "',gen=0,seq=0,q=[],cur=null,curId=0,voices=[],"
          + "loading=false,asked=false,tries=0,stopping=0,ls=[];"
          + "function get(p){return fetch(P+p,{cache:'no-store'})}"
          + "function call(o,k,e){if(typeof o[k]==='function')try{o[k].call(o,e)}"
          + "catch(x){setTimeout(function(){throw x})}}"
          + "function fire(){var e={type:'voiceschanged'};"
          + "ls.slice().forEach(function(f){try{f.call(S,e)}catch(_){}});call(S,'onvoiceschanged',e)}"
          + "function load(){if(loading)return;loading=true;"
          + "get('voices?n='+(++seq)).then(function(r){return r.json()}).then(function(j){"
          + "loading=false;voices=(j.voices||[]).map(function(v){return{name:v.name,lang:v.lang,"
          + "voiceURI:v.name,localService:!!v.localService,'default':!!v['default'],label:v.label||v.name}});"
          // an engine that is still binding answers "not ready", not "none"
          + "if(!voices.length&&!j.ready&&++tries<10){setTimeout(load,1000);return}fire()},"
          + "function(){loading=false})}"
          // back from the system's speech settings: a voice may just have been installed
          + "document.addEventListener('visibilitychange',function(){"
          + "if(!document.hidden&&asked){tries=0;load()}});"
          + "function U(t){this.text=t==null?'':String(t);this.lang='';this.voice=null;"
          + "this.rate=1;this.pitch=1;this.volume=1;"
          + "this.onstart=this.onend=this.onerror=this.onpause=this.onresume=this.onboundary=this.onmark=null}"
          + "function pump(){if(cur||stopping||!q.length)return;"
          + "var u=q.shift(),id=++seq,v=u.voice;cur=u;curId=id;"
          + "var s='id='+id+'&g='+gen+'&lang='+encodeURIComponent(u.lang||(v&&v.lang)||'')"
          + "+'&voice='+encodeURIComponent((v&&v.name)||'')+'&rate='+(+u.rate||1)"
          + "+'&text='+encodeURIComponent(u.text);"
          + "call(u,'onstart',{type:'start',utterance:u});"
          + "get('speak?'+s).then(function(r){if(!r.ok)done(id,'synthesis-failed')},"
          + "function(){done(id,'synthesis-failed')})}"
          + "function done(id,err){if(!cur||id!==curId)return;var u=cur;cur=null;curId=0;"
          + "if(err)call(u,'onerror',{type:'error',error:err,utterance:u});"
          + "else call(u,'onend',{type:'end',utterance:u});pump()}"
          + "var S={paused:false,onvoiceschanged:null,"
          + "getVoices:function(){if(!asked){asked=true;load()}return voices.slice()},"
          + "speak:function(u){if(!(u instanceof U))return;q.push(u);pump()},"
          + "cancel:function(){q=[];cur=null;curId=0;gen++;stopping++;"
          + "var fin=function(){stopping--;pump()};get('stop?g='+gen).then(fin,fin)},"
          + "pause:function(){},resume:function(){},"
          + "addEventListener:function(t,f){if(t==='voiceschanged'&&typeof f==='function'&&ls.indexOf(f)<0)ls.push(f)},"
          + "removeEventListener:function(t,f){var i=ls.indexOf(f);if(i>=0)ls.splice(i,1)},"
          + "dispatchEvent:function(){return true}};"
          + "Object.defineProperty(S,'speaking',{get:function(){return!!cur}});"
          + "Object.defineProperty(S,'pending',{get:function(){return q.length>0||stopping>0}});"
          // navigator.mediaSession, backed by a platform MediaSession (D192 Am. 5):
          // WebView exposes the API with nothing behind it, so headset and
          // keyboard media buttons never reach the page. The page sets
          // playbackState; the buttons come back as its action handlers.
          + "var H={},ms='none',M={metadata:null,"
          + "setActionHandler:function(a,f){H[a]=typeof f==='function'?f:null},"
          + "setPositionState:function(){},setMicrophoneActive:function(){},setCameraActive:function(){}};"
          + "Object.defineProperty(M,'playbackState',{get:function(){return ms},set:function(v){"
          + "v=String(v);if(v!=='playing'&&v!=='paused')v='none';if(v===ms)return;ms=v;get('media?state='+v)}});"
          + "try{Object.defineProperty(navigator,'mediaSession',{value:M,configurable:true})}catch(e){}"
          + "try{Object.defineProperty(window,'speechSynthesis',{value:S,configurable:true,writable:true});"
          + "Object.defineProperty(window,'SpeechSynthesisUtterance',{value:U,configurable:true,writable:true})"
          + "}catch(e){return}"
          + "window.__wdSpeech={done:done,settings:function(){get('settings?n='+(++seq))},"
          + "media:true,action:function(a){var f=H[a];if(f)try{f({action:a})}catch(_){}}}})()";

    private final Activity activity;
    private final WebView web;

    private final Object lock = new Object();
    private TextToSpeech tts;         // guarded by lock
    private CountDownLatch init;      // guarded by lock; set once, on first use
    private boolean configured;       // guarded by lock
    private long stopGen;             // guarded by lock
    private MediaSession session;     // main thread only; made by the first reading
    private List<Locale> probed;      // guarded by lock; see fallbackVoices
    private volatile boolean ok;      // onInit reported SUCCESS
    private volatile boolean dead;    // shutdown() ran

    Speech(Activity activity, WebView web) {
        this.activity = activity;
        this.web = web;
    }

    // ── the activity's side ──────────────────────────────────────────────

    /** From onPageFinished: every new document needs the shim again. */
    void inject(WebView view, String url) {
        // Our pages only. The shim reaches this class, and a page from
        // anywhere else has no business making the device talk.
        if (url == null || !url.startsWith(Shell.origin(activity) + "/")) return;
        // A new document counts its cancels from 0 again. The last one's count
        // must not outrank it: every speak it sent would be taken for one
        // cancelled on its way and dropped, unanswered, and the page would
        // stay "speaking" in silence for as long as it lives. What the last
        // document was saying ends with it.
        synchronized (lock) {
            stopGen = 0;
            if (tts != null && ok) try { tts.stop(); } catch (RuntimeException ignored) { }
        }
        media("none"); // the new document is reading nothing yet
        view.evaluateJavascript(JS, null);
    }

    /**
     * From shouldInterceptRequest, on a WebView worker thread: the answer to a
     * shim request, or null when the request is not one.
     */
    WebResourceResponse intercept(Uri u) {
        String p = u == null ? null : u.getPath();
        if (p == null || !p.startsWith(PATH)) return null;
        String op = p.substring(PATH.length()), body = "";
        try {
            switch (op) {
                case "voices": body = voices(); break;
                case "speak": speak(u); break;
                case "stop": stop(num(u.getQueryParameter("g"))); break;
                case "settings": activity.runOnUiThread(this::openSettings); break;
                case "media": media(u.getQueryParameter("state")); break;
                default: break;
            }
        } catch (RuntimeException e) {
            // an engine is another app's code; its failure must not become ours
            Log.w(TAG, "speech " + op + " failed", e);
        }
        Map<String, String> h = new HashMap<>();
        h.put("Cache-Control", "no-store");
        return new WebResourceResponse("voices".equals(op) ? "application/json" : "text/plain",
                "utf-8", 200, "OK", h,
                new ByteArrayInputStream(body.getBytes(StandardCharsets.UTF_8)));
    }

    /**
     * Leaving the app ends the reading. The engine reports the utterance as
     * interrupted, and speak.js takes an interruption it did not cause as the
     * end of the whole reading, so it does not carry on at the next sentence.
     */
    void stop() {
        synchronized (lock) {
            if (tts == null || !ok) return;
            try { tts.stop(); } catch (RuntimeException ignored) { }
        }
    }

    /** From onDestroy. */
    void shutdown() {
        TextToSpeech t;
        synchronized (lock) {
            dead = true;
            t = tts;
            tts = null;
        }
        if (session != null) { session.release(); session = null; }
        if (t == null) return;
        try {
            t.stop();
            t.shutdown();
        } catch (RuntimeException ignored) { }
    }

    // ── the engine ───────────────────────────────────────────────────────

    /**
     * The engine, bound on first use and ready, or null: no engine installed,
     * binding failed, or still binding after 3 s. Called on worker threads
     * only - it waits for onInit, which the main thread delivers.
     */
    private TextToSpeech engine() {
        CountDownLatch l;
        synchronized (lock) {
            if (dead) return null;
            if (init == null) {
                final CountDownLatch mine = new CountDownLatch(1);
                init = mine;
                activity.runOnUiThread(() -> {
                    synchronized (lock) {
                        if (dead) { mine.countDown(); return; }
                        // The user's default engine. onInit may run inside
                        // this constructor (a failed bind reports at once),
                        // so it touches nothing but its own two fields.
                        tts = new TextToSpeech(activity.getApplicationContext(), status -> {
                            ok = status == TextToSpeech.SUCCESS;
                            mine.countDown();
                        });
                    }
                });
            }
            l = init;
        }
        try {
            if (!l.await(3, TimeUnit.SECONDS)) return null;
        } catch (InterruptedException e) {
            Thread.currentThread().interrupt();
            return null;
        }
        synchronized (lock) {
            if (!ok || dead || tts == null) return null;
            if (!configured) {
                configured = true;
                configure(tts);
            }
            return tts;
        }
    }

    private boolean initDone() {
        synchronized (lock) {
            return init != null && init.getCount() == 0;
        }
    }

    private void configure(TextToSpeech t) {
        // Media, not accessibility: the volume keys a reader reaches for while
        // it talks are the media ones.
        t.setAudioAttributes(new AudioAttributes.Builder()
                .setUsage(AudioAttributes.USAGE_MEDIA)
                .setContentType(AudioAttributes.CONTENT_TYPE_SPEECH)
                .build());
        t.setOnUtteranceProgressListener(new UtteranceProgressListener() {
            @Override public void onStart(String id) { }
            @Override public void onDone(String id) { done(id, null); }
            @Override @SuppressWarnings("deprecation")
            public void onError(String id) { done(id, "synthesis-failed"); }
            @Override public void onError(String id, int code) {
                done(id, code == TextToSpeech.ERROR_NETWORK || code == TextToSpeech.ERROR_NETWORK_TIMEOUT
                        ? "network" : "synthesis-failed");
            }
            @Override public void onStop(String id, boolean interrupted) { done(id, "interrupted"); }
        });
    }

    /** Tells the shim an utterance is over; `err` is a Web Speech error code. */
    private void done(String id, String err) {
        // The id went out from the page and came back through the engine:
        // digits only before it is written into a script.
        if (dead || id == null || !id.matches("\\d{1,15}")) return;
        String js = "window.__wdSpeech&&__wdSpeech.done(" + id + ","
                + (err == null ? "null" : "'" + err + "'") + ")";
        activity.runOnUiThread(() -> {
            if (!dead) web.evaluateJavascript(js, null);
        });
    }

    // ── media buttons ────────────────────────────────────────────────────

    /**
     * The page's navigator.mediaSession.playbackState: "playing", "paused" or
     * "none" (D192 Am. 5). A playing or paused session is active, so the
     * system routes media buttons - a headset's, a Bluetooth keyboard's - to
     * it; the Activity's media controller sends it the ones pressed while
     * wuDict is in front. The session is made by the first reading: a reader
     * who never listens has none.
     */
    private void media(String state) {
        if (state == null) return;
        activity.runOnUiThread(() -> {
            if (dead) return;
            int s = "playing".equals(state) ? PlaybackState.STATE_PLAYING
                    : "paused".equals(state) ? PlaybackState.STATE_PAUSED : PlaybackState.STATE_NONE;
            if (session == null) {
                if (s == PlaybackState.STATE_NONE) return;
                session = new MediaSession(activity, "wudict read aloud");
                session.setCallback(new MediaSession.Callback() {
                    @Override public void onPlay() { action("play"); }
                    @Override public void onPause() { action("pause"); }
                    @Override public void onStop() { action("stop"); }
                    @Override public void onSkipToNext() { action("nexttrack"); }
                    @Override public void onSkipToPrevious() { action("previoustrack"); }
                });
                activity.setMediaController(new MediaController(activity, session.getSessionToken()));
            }
            session.setPlaybackState(new PlaybackState.Builder()
                    .setActions(PlaybackState.ACTION_PLAY | PlaybackState.ACTION_PAUSE
                            | PlaybackState.ACTION_PLAY_PAUSE | PlaybackState.ACTION_STOP
                            | PlaybackState.ACTION_SKIP_TO_NEXT | PlaybackState.ACTION_SKIP_TO_PREVIOUS)
                    .setState(s, PlaybackState.PLAYBACK_POSITION_UNKNOWN, s == PlaybackState.STATE_PLAYING ? 1f : 0f)
                    .build());
            session.setActive(s != PlaybackState.STATE_NONE);
        });
    }

    /** A media button, to the page's action handler; main thread. `a` is one of ours. */
    private void action(String a) {
        if (!dead) web.evaluateJavascript("window.__wdSpeech&&__wdSpeech.action&&__wdSpeech.action('" + a + "')", null);
    }

    // ── requests ─────────────────────────────────────────────────────────

    private String voices() {
        TextToSpeech t = engine();
        JSONObject out = new JSONObject();
        try {
            JSONArray list = t == null ? new JSONArray() : listVoices(t);
            // "Not ready" is a binding that has not finished, and the shim asks
            // again; a finished one with no voices is an answer.
            out.put("ready", t != null || initDone());
            out.put("voices", list);
        } catch (JSONException e) {
            Log.w(TAG, "speech voices", e);
        }
        return out.toString();
    }

    private JSONArray listVoices(TextToSpeech t) throws JSONException {
        Set<Voice> vs = null;
        String def = null;
        try { vs = t.getVoices(); } catch (RuntimeException ignored) { }
        try {
            Voice d = t.getDefaultVoice();
            if (d != null) def = d.getName();
        } catch (RuntimeException ignored) { }
        List<Voice> sorted = new ArrayList<>();
        if (vs != null) sorted.addAll(vs);
        sorted.sort(Comparator.comparing(Voice::getName));
        JSONArray arr = new JSONArray();
        List<Locale> locs = new ArrayList<>();
        Map<String, Integer> seen = new HashMap<>();
        boolean marked = false;
        for (Voice v : sorted) {
            Set<String> f = v.getFeatures();
            if (f != null && f.contains(TextToSpeech.Engine.KEY_FEATURE_NOT_INSTALLED)) continue;
            Locale l = Iso.two(v.getLocale());
            if (l == null) continue;
            boolean online = v.isNetworkConnectionRequired();
            // Engine voice names are ids ("en-us-x-iol-local"); a reader
            // chooses between "English (United States)" and "… 2".
            String disp = l.getDisplayName();
            int n = seen.merge(disp, 1, Integer::sum);
            JSONObject o = new JSONObject();
            o.put("name", v.getName());
            o.put("lang", l.toLanguageTag());
            o.put("localService", !online);
            boolean isDef = v.getName().equals(def);
            marked |= isDef;
            o.put("default", isDef);
            o.put("label", disp + (n > 1 ? " " + n : "") + (online ? " · online" : ""));
            arr.put(o);
            locs.add(l);
        }
        if (arr.length() == 0) return fallbackVoices(t);
        // no default voice, or one that is not listed (not installed, an
        // unmappable locale): its language still marks one
        if (!marked) markDefault(arr, locs, defaultLocale(t));
        return arr;
    }

    /**
     * An engine that predates the Voice API (older eSpeak and RHVoice builds
     * still ship) lists no voices and speaks anyway. Report its languages as
     * one voice each, or the page would tell the reader there is none. The
     * probe is up to one binder call per ISO language, so it is made once;
     * the default is marked on every call, because the reader may change it
     * in the speech settings and come back.
     */
    private JSONArray fallbackVoices(TextToSpeech t) throws JSONException {
        List<Locale> found;
        synchronized (lock) {
            found = probed;
        }
        if (found == null) {
            found = probeLanguages(t);
            synchronized (lock) {
                probed = found;
            }
        }
        JSONArray arr = new JSONArray();
        for (Locale l : found) {
            JSONObject o = new JSONObject();
            // not a real voice name: speak() finds no such voice and falls
            // back to setLanguage, which is all such an engine understands
            o.put("name", "lang:" + l.toLanguageTag());
            o.put("lang", l.toLanguageTag());
            o.put("localService", true);
            o.put("default", false);
            o.put("label", l.getDisplayName());
            arr.put(o);
        }
        markDefault(arr, found, defaultLocale(t));
        return arr;
    }

    private static List<Locale> probeLanguages(TextToSpeech t) {
        Set<Locale> ls = null;
        try { ls = t.getAvailableLanguages(); } catch (RuntimeException ignored) { }
        if (ls == null || ls.isEmpty()) {
            ls = new HashSet<>();
            for (String code : Locale.getISOLanguages()) {
                Locale l = Locale.forLanguageTag(code);
                try {
                    if (t.isLanguageAvailable(l) >= TextToSpeech.LANG_AVAILABLE) ls.add(l);
                } catch (RuntimeException ignored) { }
            }
        }
        List<Locale> out = new ArrayList<>();
        Set<String> tags = new HashSet<>();
        for (Locale raw : ls) {
            Locale l = Iso.two(raw);
            if (l != null && tags.add(l.toLanguageTag())) out.add(l);
        }
        return out;
    }

    /**
     * The language set in the system's speech settings, for an engine that
     * names no default voice: speak.js reads a selection in that language
     * when the dictionary does not decide it.
     */
    @SuppressWarnings("deprecation")
    private static Locale defaultLocale(TextToSpeech t) {
        try {
            Voice v = t.getDefaultVoice();
            if (v != null) return Iso.two(v.getLocale());
        } catch (RuntimeException ignored) { }
        try {
            return Iso.two(t.getDefaultLanguage());
        } catch (RuntimeException ignored) { }
        return null;
    }

    /** Marks the first entry in `d`'s exact locale, else in its language, as the default. */
    private static void markDefault(JSONArray arr, List<Locale> locs, Locale d) throws JSONException {
        if (d == null) return;
        int at = -1;
        for (int i = 0; i < locs.size() && at < 0; i++) if (locs.get(i).equals(d)) at = i;
        for (int i = 0; i < locs.size() && at < 0; i++)
            if (locs.get(i).getLanguage().equals(d.getLanguage())) at = i;
        if (at >= 0) arr.getJSONObject(at).put("default", true);
    }

    private void speak(Uri u) {
        String id = u.getQueryParameter("id");
        if (id == null || !id.matches("\\d{1,15}")) return;
        long g = num(u.getQueryParameter("g"));
        String text = u.getQueryParameter("text");
        String lang = u.getQueryParameter("lang");
        String voice = u.getQueryParameter("voice");
        float rate = 1f;
        try {
            String r = u.getQueryParameter("rate");
            if (r != null) rate = Math.max(0.1f, Math.min(4f, Float.parseFloat(r)));
        } catch (NumberFormatException ignored) { }
        TextToSpeech t = engine();
        if (t == null) { done(id, "synthesis-unavailable"); return; }
        if (text == null || text.trim().isEmpty()) { done(id, null); return; }
        synchronized (lock) {
            if (g < stopGen || dead) return; // cancelled while it was on its way
            boolean set = false;
            if (voice != null && !voice.isEmpty() && !voice.startsWith("lang:")) {
                Set<Voice> vs = t.getVoices();
                if (vs != null) for (Voice v : vs) {
                    if (voice.equals(v.getName())) {
                        set = t.setVoice(v) == TextToSpeech.SUCCESS;
                        break;
                    }
                }
            }
            if (!set) {
                Locale l = lang == null || lang.isEmpty() ? Locale.getDefault() : Locale.forLanguageTag(lang);
                if (t.setLanguage(l) < TextToSpeech.LANG_AVAILABLE) {
                    done(id, "language-unavailable");
                    return;
                }
            }
            t.setSpeechRate(rate);
            if (t.speak(text, TextToSpeech.QUEUE_ADD, null, id) != TextToSpeech.SUCCESS) {
                done(id, "synthesis-failed");
            }
        }
    }

    private void stop(long g) {
        synchronized (lock) {
            if (g > stopGen) stopGen = g;
            if (tts == null || !ok) return; // never bound: nothing is talking
            tts.stop();
        }
    }

    private void openSettings() {
        if (dead) return;
        // The system's text-to-speech page, where an engine is chosen and its
        // voices installed; not a public action, so the engine's own
        // install-data screen stands in where a vendor removed it.
        try {
            activity.startActivity(new Intent("com.android.settings.TTS_SETTINGS"));
            return;
        } catch (ActivityNotFoundException | SecurityException ignored) { }
        try {
            activity.startActivity(new Intent(TextToSpeech.Engine.ACTION_INSTALL_TTS_DATA));
        } catch (ActivityNotFoundException | SecurityException e) {
            Log.w(TAG, "no speech settings to open", e);
        }
    }

    private static long num(String s) {
        try {
            return s == null ? 0 : Long.parseLong(s);
        } catch (NumberFormatException e) {
            return 0;
        }
    }

    // ── locales ──────────────────────────────────────────────────────────

    /**
     * Engines report voices with ISO 639-2 and 3166 alpha-3 codes as often as
     * with the two-letter ones ("eng-USA"); the page compares against
     * two-letter tags. Built on first use, from the platform's own tables.
     */
    private static final class Iso {
        private static final Map<String, String> LANG = new HashMap<>();
        private static final Map<String, String> REGION = new HashMap<>();
        static {
            for (String c : Locale.getISOLanguages()) {
                try { LANG.put(Locale.forLanguageTag(c).getISO3Language(), c); }
                catch (MissingResourceException ignored) { }
            }
            for (String c : Locale.getISOCountries()) {
                try { REGION.put(Locale.forLanguageTag("und-" + c).getISO3Country(), c); }
                catch (MissingResourceException ignored) { }
            }
        }

        static Locale two(Locale l) {
            if (l == null) return null;
            String lang = l.getLanguage(), region = l.getCountry();
            if (lang.isEmpty()) return null;
            if (lang.length() == 3) { String s = LANG.get(lang); if (s != null) lang = s; }
            if (region.length() == 3) { String s = REGION.get(region); if (s != null) region = s; }
            // an unmapped alpha-3 would parse as an extlang, not a region
            if (!region.matches("[A-Za-z]{2}|\\d{3}")) region = "";
            Locale out = Locale.forLanguageTag(region.isEmpty() ? lang : lang + "-" + region);
            return out.getLanguage().isEmpty() ? null : out;
        }
    }
}
