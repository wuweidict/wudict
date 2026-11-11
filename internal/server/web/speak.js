/**
 * Copyright (C) 2026 glowinthedark
 *
 * SPDX-License-Identifier: GPL-3.0-or-later
 */

// wudict read-aloud (D148): select text in an article or its headword, tap the
// speaker, hear it in the system voice. Fetched by index.html on the first
// such selection and never before, so a reader who does not select text pays for
// nothing but two listeners, and one who switched it off not even those.
//
// Web Speech only. The Android shell has no working speechSynthesis in its
// WebView and injects one of its own over the platform TextToSpeech (Speech.java);
// this file cannot tell the two apart, which is the point.
(function () {
"use strict";
if (window.wuSpeak) return;

// Android's getMaxSpeechInputLength floor; a selection past it is a page, not
// a phrase, and reading it all would hold the device for minutes.
const MAX = 4000;
// One utterance per ~sentence. Chrome's network voices stop dead after ~15 s
// of one utterance and never fire `end`; short utterances also make Stop
// immediate everywhere.
const CHUNK = 160;
const VOICE_KEY = "wudict_voice_";
const LANG_KEY = "wudict_speaklang_";

// ------------------------------------------------------------- language
// The article language alone is the wrong key: an en→ru article is Russian
// prose full of English examples. The SCRIPT of what was selected picks
// between the dictionary's two languages; a script neither of them writes in
// falls back to that script's most likely language.
const SCRIPTS = [
  ["Latn", /\p{Script=Latin}/u], ["Cyrl", /\p{Script=Cyrillic}/u],
  ["Kana", /[\p{Script=Hiragana}\p{Script=Katakana}]/u], ["Hani", /\p{Script=Han}/u],
  ["Hang", /\p{Script=Hangul}/u], ["Grek", /\p{Script=Greek}/u],
  ["Arab", /\p{Script=Arabic}/u], ["Hebr", /\p{Script=Hebrew}/u],
  ["Thai", /\p{Script=Thai}/u], ["Deva", /\p{Script=Devanagari}/u],
  ["Beng", /\p{Script=Bengali}/u], ["Guru", /\p{Script=Gurmukhi}/u],
  ["Gujr", /\p{Script=Gujarati}/u], ["Taml", /\p{Script=Tamil}/u],
  ["Telu", /\p{Script=Telugu}/u], ["Knda", /\p{Script=Kannada}/u],
  ["Mlym", /\p{Script=Malayalam}/u], ["Sinh", /\p{Script=Sinhala}/u],
  ["Geor", /\p{Script=Georgian}/u], ["Armn", /\p{Script=Armenian}/u],
  ["Ethi", /\p{Script=Ethiopic}/u], ["Khmr", /\p{Script=Khmer}/u],
  ["Laoo", /\p{Script=Lao}/u], ["Mymr", /\p{Script=Myanmar}/u],
  ["Tibt", /\p{Script=Tibetan}/u]
];
// language -> script, for every language not written in Latin. Absent = Latin.
const LANG_SCRIPT = {
  ru: "Cyrl", uk: "Cyrl", be: "Cyrl", bg: "Cyrl", sr: "Cyrl", mk: "Cyrl", kk: "Cyrl",
  ky: "Cyrl", tg: "Cyrl", mn: "Cyrl", ba: "Cyrl", tt: "Cyrl", cv: "Cyrl", ce: "Cyrl",
  os: "Cyrl", ab: "Cyrl", av: "Cyrl", cu: "Cyrl",
  el: "Grek", grc: "Grek",
  ar: "Arab", fa: "Arab", ur: "Arab", ps: "Arab", sd: "Arab", ug: "Arab",
  he: "Hebr", yi: "Hebr",
  zh: "Hani", yue: "Hani", ja: "Jpan", ko: "Hang",
  th: "Thai", hi: "Deva", mr: "Deva", ne: "Deva", sa: "Deva", bn: "Beng", as: "Beng",
  pa: "Guru", gu: "Gujr", ta: "Taml", te: "Telu", kn: "Knda", ml: "Mlym", si: "Sinh",
  ka: "Geor", hy: "Armn", am: "Ethi", ti: "Ethi", km: "Khmr", lo: "Laoo", my: "Mymr",
  bo: "Tibt", dz: "Tibt"
};
// script -> the language a selection in it most likely is, when neither of
// the dictionary's languages is written in it. Latin is decided separately.
const SCRIPT_LANG = {
  Cyrl: "ru", Grek: "el", Arab: "ar", Hebr: "he", Hani: "zh", Kana: "ja", Hang: "ko",
  Thai: "th", Deva: "hi", Beng: "bn", Guru: "pa", Gujr: "gu", Taml: "ta", Telu: "te",
  Knda: "kn", Mlym: "ml", Sinh: "si", Geor: "ka", Armn: "hy", Ethi: "am", Khmr: "km",
  Laoo: "lo", Mymr: "my", Tibt: "bo"
};
// Legacy and macro-language spellings that name the same voice.
const ALIAS = { iw: "he", in: "id", ji: "yi", nb: "no", fil: "tl", cmn: "zh" };

function norm(tag) {
  const b = String(tag || "").replace(/_/g, "-").split("-")[0].toLowerCase();
  return ALIAS[b] || b;
}

// The majority script of the selection's letters. Kana anywhere with Han
// around it is Japanese: Chinese is never written with kana.
function scriptOf(text) {
  const n = {};
  let seen = 0;
  for (const ch of text.slice(0, 400)) {
    if (ch < "A") continue;                                      // digits, space, ASCII punctuation
    if (ch <= "z") { if (/[A-Za-z]/.test(ch)) { n.Latn = (n.Latn || 0) + 1; seen++ } continue }
    for (const [sc, re] of SCRIPTS) if (re.test(ch)) { n[sc] = (n[sc] || 0) + 1; seen++; break }
  }
  if (!seen) return "";
  if (n.Kana) n.Kana += n.Hani || 0, n.Hani = 0;
  let best = "", max = 0;
  for (const sc in n) if (n[sc] > max) max = n[sc], best = sc;
  return best;
}

function fits(lang, sc) {
  const s = LANG_SCRIPT[lang] || "Latn";
  return s === "Jpan" ? sc === "Kana" || sc === "Hani" : s === sc;
}

function userLang() { return norm(navigator.language) || "en" }

// Article language first: it is what the reader is reading, and for a
// Latin/Latin pair (en→fr) script cannot tell the two apart, so the target
// wins and the other one is one tap away in the menu.
//
// t is the language of the platform's default voice: the one the reader set in
// the system's speech settings. It replaces the guesses - the script's likeliest
// language, the reader's interface language, English - and never a language
// the dictionary is known to have: an English dictionary read by a learner
// with a native-language default voice is still read in English (D148 Am. 1).
function decide(sc, a, h, t) {
  const tts = t && (!sc || fits(t, sc)) ? t : "";
  if (!sc) return a || h || tts || userLang();
  for (const l of [a, h]) if (l && fits(l, sc)) return l;
  if (tts) return tts;
  if (sc === "Latn") { const u = userLang(); return fits(u, "Latn") ? u : "en" }
  return SCRIPT_LANG[sc] || userLang();
}

let langNames = null;
function langName(code) {
  try {
    langNames = langNames || new Intl.DisplayNames([navigator.language, "en"], { type: "language" });
    return langNames.of(code) || code;
  } catch (_) { return code }
}

// --------------------------------------------------------------- voices
// Read at use, never captured: the Android shell replaces both objects after
// the page has loaded.
const S = () => window.speechSynthesis;
let voices = [], voicesDone = false, voicesStarted = false;

function readVoices() {
  try { voices = (S() && S().getVoices()) || [] } catch (_) { voices = [] }
  if (voices.length) voicesDone = true;
  indexVoices();
  if (stash) relang(stash); // the default voice may have arrived after the selection
  if (ui && !ui.hidden) syncMore();
  if (menu && !menu.hidden) openMenu(menuNote);
  if (waiting && voicesDone) { waiting = false; setBusy(false); if (stash) go(stash.lang, null) }
}
// Voices load asynchronously in Chrome and on Android, synchronously in Safari
// and Firefox; none of them fires `voiceschanged` when there are none at all.
function startVoices() {
  if (voicesStarted || !S()) return;
  voicesStarted = true;
  try { S().addEventListener("voiceschanged", readVoices) } catch (_) { }
  readVoices();
  setTimeout(() => { voicesDone = true; readVoices() }, 3500);
}

// The voice list's languages, and the language of the platform's default
// voice: "" when no voice is marked default, or several are and none of them
// is in the reader's own language. A browser's `default` is taken only in a
// language the browser is set to prefer: what a desktop engine marks default
// can be its first voice in any language. The Android shell's is the speech
// settings' own. Built once per voice list, never per selection.
let voiceLangs = [], ttsLang = "";
function indexVoices() {
  const all = new Set(), defs = new Set();
  for (const v of voices) {
    const l = norm(v.lang);
    if (!l) continue;
    all.add(l);
    if (v.default) defs.add(l);
  }
  voiceLangs = [...all];
  const u = userLang();
  let t = defs.size === 1 ? defs.values().next().value : defs.has(u) ? u : "";
  if (t && !settings() && !(navigator.languages || [navigator.language]).some(x => norm(x) === t)) t = "";
  ttsLang = t;
}

// macOS ships joke voices, and the robotic Eloquence set in every language;
// neither is ever the right automatic answer. They stay available only where
// they are all a language has.
const NOVELTY = /^(Albert|Bad News|Bahh|Bells|Boing|Bubbles|Cellos|Good News|Jester|Organ|Pipe Organ|Superstar|Trinoids|Whisper|Wobble|Zarvox|Deranged|Hysterical|Eddy|Flo|Grandma|Grandpa|Reed|Rocko|Sandy|Shelley)\b/;

function voicesFor(L) {
  const all = voices.filter(v => norm(v.lang) === L);
  const good = all.filter(v => !NOVELTY.test(v.name || ""));
  return good.length ? good : all;
}

function storedVoice(L) {
  try { return localStorage.getItem(VOICE_KEY + L) } catch (_) { return null }
}

// The reader's own choice; then the system default; then the region the
// reader's browser asks for (en-GB user, en-GB voice); then offline over
// online, because it starts at once and works on a plane.
function pickVoice(L) {
  const list = voicesFor(L);
  if (!list.length) return null;
  const uri = storedVoice(L);
  const mine = uri && list.find(v => v.voiceURI === uri);
  if (mine) return mine;
  const want = (navigator.languages || [navigator.language])
    .map(x => String(x).replace(/_/g, "-").toLowerCase()).find(x => x.includes("-") && norm(x) === L);
  const score = v => (v.default ? 8 : 0) +
    (want && String(v.lang).replace(/_/g, "-").toLowerCase() === want ? 4 : 0) +
    (v.localService ? 2 : 0);
  return list.reduce((b, v) => score(v) > score(b) ? v : b);
}

// ---------------------------------------------------------------- speech
let sess = 0, busy = false, poll = 0, waiting = false;
// Space pauses (D192 Am. 4). No engine pauses reliably - Android's has only
// stop, Chrome's network voices lose their place - so a pause is a stop that
// remembers the chunk being said (a sentence, at most CHUNK characters), and
// resuming says it again from its start. `reading` is what say() is saying.
let paused = false, reading = null;
const live = new Set(); // Chrome collects an unreferenced utterance and its `end` never comes

function chunks(text, L) {
  let sents;
  try { sents = Array.from(new Intl.Segmenter(L, { granularity: "sentence" }).segment(text), s => s.segment) }
  catch (_) { sents = text.match(/[^.!?。！？]*(?:[.!?。！？]+\s*|$)/g) || [text] }
  const out = [];
  let cur = "";
  for (const s of sents) {
    if ((cur + s).length <= CHUNK) { cur += s; continue }
    if (cur) out.push(cur);
    let rest = s;
    while (rest.length > CHUNK) {
      const p = Math.max(rest.lastIndexOf(", ", CHUNK), rest.lastIndexOf("; ", CHUNK));
      let cut = p > CHUNK / 2 ? p + 1 : rest.lastIndexOf(" ", CHUNK);
      if (cut < CHUNK / 2) cut = CHUNK; // no spaces: CJK, or one enormous word
      out.push(rest.slice(0, cut));
      rest = rest.slice(cut);
    }
    cur = rest;
  }
  if (cur) out.push(cur);
  return out.map(s => s.trim()).filter(Boolean);
}

function stop() {
  sess++;
  waiting = false;
  paused = false;
  reading = null;
  clearInterval(poll);
  live.clear();
  try { S() && S().cancel() } catch (_) { }
  setBusy(false);
}

function say(text, L, voice, from = 0) {
  const syn = S(), U = window.SpeechSynthesisUtterance;
  if (!syn || !U || !text) return;
  stop();
  const tok = sess, parts = chunks(text, L), r = { text, L, voice, at: from };
  reading = r;
  let i = from, cur = null, idle = 0;
  const next = () => {
    if (tok !== sess) return;
    if (i >= parts.length) { done(true); return }
    r.at = i;
    const u = new U(parts[i++]);
    let ended = false;
    const end = () => {
      if (ended) return;
      ended = true;
      live.delete(u);
      if (tok === sess && u === cur) next();
    };
    cur = u;
    u.lang = voice ? voice.lang : L;
    if (voice) u.voice = voice;
    u.onend = end;
    u.onerror = e => {
      const er = e && e.error;
      ended = true;
      live.delete(u);
      if (tok !== sess || u !== cur) return;
      // Our own stop() moves `sess` first, so an interruption still in this
      // session came from outside - the OS, another app's audio, the app
      // leaving the screen. It ends the reading; the watchdog must not take
      // the silence for an end and carry on at the next sentence.
      if (er === "interrupted" || er === "canceled") { stop(); done(); return }
      fail(er, L);
    };
    live.add(u);
    try { syn.resume() } catch (_) { } // Chrome can sit paused with nothing said
    syn.speak(u);
    idle = 0;
  };
  // A lost `end` must not leave Stop on screen forever: an engine that has
  // gone quiet with nothing queued has finished, whatever it reported.
  poll = setInterval(() => {
    if (tok !== sess) return;
    if (syn.speaking || syn.pending) { idle = 0; return }
    if (++idle >= 3 && cur) cur.onend(); // guarded: a late real `end` is a no-op
  }, 700);
  setBusy(true);
  next();
}

function pause() {
  if (!busy || paused) return;
  if (waiting || !reading) { halt(); return } // nothing said yet: nothing to come back to
  sess++; // the cancel's own "interrupted" belongs to no session now
  clearInterval(poll);
  live.clear();
  try { S() && S().cancel() } catch (_) { }
  paused = true;
  setBusy(true);
}

// say() stops first, which forgets `reading`: taken apart before the call.
function resume() {
  if (!paused || !reading) return;
  const { text, L, voice, at } = reading;
  say(text, L, voice, at);
}
function restart() {
  if (!busy || !reading) return;
  const { text, L, voice } = reading;
  say(text, L, voice, 0);
}

// natural: the text was read to its end - not stopped, not cut off by the
// system - which is when Keep reading carries on to the next unit.
function done(natural) {
  clearInterval(poll);
  setBusy(false);
  if (natural && keepOn() && stash && stash.tap && stash.next && advance(false)) return;
  if (!selLive) hide();
}

// ------------------------------------------------------- reading on (D192)
// ⏭ marks the next unit in reading order and reads it; Keep reading does the
// same at the end of every unit. Off by default and kept per device: it is
// how this device is listened to, not a reading habit.
const KEEP_KEY = "wudict_keepreading";
function keepOn() { try { return localStorage.getItem(KEEP_KEY) === "1" } catch (_) { return false } }
function setKeep(v) { try { v ? localStorage.setItem(KEEP_KEY, "1") : localStorage.removeItem(KEEP_KEY) } catch (_) { } }

// The next unit arrives as a unit() call, or from an article frame as a
// frame() report a message later; it is read when it comes within this
// window of asking. readStart is when the reader last started a reading,
// userScroll when they last scrolled: a page they scrolled since is left
// where they put it.
const ASKED = 2000;
let pendingRead = 0, pendingForce = false, readStart = 0, userScroll = 0;

// Asks for the next unit; false when there is none to come.
function advance(force) {
  if (!stash || !ctx || !ctx.step) return false;
  pendingRead = Date.now();
  pendingForce = force;
  if (ctx.step(stash) !== false) return true;
  pendingRead = 0;
  return false;
}
// ⏭, from its button or a media key.
function skip() {
  if (!stash || !stash.tap || !stash.next) return;
  closeMenu();
  readStart = Date.now();
  halt();
  advance(true);
}
function arrived() {
  const asked = pendingRead && Date.now() - pendingRead < ASKED;
  pendingRead = 0;
  return asked;
}

// The unit being read is brought into view when it is not, a quarter down
// the room between the bar and the docked speaker - unless the reader has
// scrolled since the reading began. ⏭ always brings it: it was asked for.
function reveal(force) {
  if (!force && userScroll > readStart) return;
  let b = null;
  if (stash.kind === "frame") b = anchorRect();
  else if (stash.range) b = stash.range.getBoundingClientRect();
  if (!b || !(b.width || b.height)) return;
  const top = (ctx && ctx.top ? ctx.top() : 0) + 8;
  let bottom = innerHeight - 8;
  if (touch && ui && !ui.hidden) bottom = Math.min(bottom, ui.getBoundingClientRect().top - 8);
  if (b.top >= top && b.bottom <= bottom) return;
  const dy = b.top - (top + (bottom - top) * 0.25);
  try { scrollBy({ top: dy, behavior: "smooth" }) } catch (_) { scrollBy(0, dy) }
}
function onUserScroll() { userScroll = Date.now() }
const SCROLL_KEY = /^(PageUp|PageDown|ArrowUp|ArrowDown|Home|End| )$/;

// Space is read aloud's while something is read or paused: it pauses and
// resumes. Not in a field, where it types, nor on a control reached by Tab,
// which Space presses; a button merely clicked earlier keeps no claim on it.
// A Space keydown comes only from a keyboard - the on-screen one types only
// into fields - so the event itself is all the keyboard detection there is.
function spaceIsOurs(e) {
  if (e.key !== " " || e.ctrlKey || e.metaKey || e.altKey || e.shiftKey || !busy) return false;
  const t = e.composedPath ? e.composedPath()[0] : e.target;
  if (!t || t.nodeType !== 1) return true;
  if (t.isContentEditable || /^(input|textarea|select)$/i.test(t.localName)) return false;
  try {
    if (t.matches("a[href],button,summary,[role=button],[tabindex]") && t.matches(":focus-visible")) return false;
  } catch (_) { }
  return true;
}
function space() {
  if (paused) resume();
  else if (busy) pause();
}

// ------------------------------------------------------- media keys (D192)
// Media buttons - a keyboard's, a headset's, the OS's now-playing panel -
// reach a page through navigator.mediaSession, and a browser gives a session
// only to a page whose media element plays: speech is none. So while read
// aloud reads or is paused, a silent clip plays on a loop and holds the
// session, the standard's own route. Five seconds, since Firefox leaves
// shorter media out of its media controls. The Android shell backs
// navigator.mediaSession with a platform MediaSession (Speech.java) and
// needs no clip.
const MS = () => ("mediaSession" in navigator ? navigator.mediaSession : null);
const shellMedia = () => !!(window.__wdSpeech && window.__wdSpeech.media);
let mediaState = "none", mediaOffT = 0, metaText = "", keepAlive = null, keepURL = "";

function silence() {
  if (keepURL) return keepURL;
  const rate = 8000, n = rate * 5, b = new Uint8Array(44 + n), v = new DataView(b.buffer);
  const tag = (o, t) => { for (let i = 0; i < t.length; i++) b[o + i] = t.charCodeAt(i) };
  tag(0, "RIFF"); v.setUint32(4, 36 + n, true); tag(8, "WAVE");
  tag(12, "fmt "); v.setUint32(16, 16, true); v.setUint16(20, 1, true); v.setUint16(22, 1, true);
  v.setUint32(24, rate, true); v.setUint32(28, rate, true); v.setUint16(32, 1, true); v.setUint16(34, 8, true);
  tag(36, "data"); v.setUint32(40, n, true);
  b.fill(128, 44); // 8-bit PCM is unsigned: 128 is silence
  return (keepURL = URL.createObjectURL(new Blob([b], { type: "audio/wav" })));
}

// The session follows the reading: playing, paused, or - a moment after it
// ends - none. The moment is the gap between two units of Keep reading,
// where a session given up would hand the media keys to another app.
function mediaSync() {
  const want = paused ? "paused" : busy ? "playing" : "none";
  const ms = MS();
  if (want !== "none") {
    clearTimeout(mediaOffT);
    if (ms && stash && stash.text !== metaText) {
      metaText = stash.text;
      try {
        if (window.MediaMetadata) ms.metadata = new MediaMetadata({
          title: metaText.length > 120 ? metaText.slice(0, 119) + "…" : metaText,
          artist: (ctx && ctx.name ? ctx.name(stash.dict) : "") || "", album: "wudict"
        });
      } catch (_) { }
    }
  }
  if (want === mediaState) return;
  if (want === "none") { clearTimeout(mediaOffT); mediaOffT = setTimeout(mediaOff, 1500); return }
  mediaState = want;
  if (ms) try { ms.playbackState = want } catch (_) { }
  if (shellMedia()) return;
  if (want === "playing") {
    if (!keepAlive) { keepAlive = new Audio(silence()); keepAlive.loop = true }
    try { const pr = keepAlive.play(); if (pr && pr.catch) pr.catch(() => { }) } catch (_) { }
  } else if (keepAlive) keepAlive.pause();
}
function mediaOff() {
  clearTimeout(mediaOffT);
  if (busy) return;
  mediaState = "none";
  metaText = "";
  const ms = MS();
  if (ms) try { ms.playbackState = "none"; ms.metadata = null } catch (_) { }
  if (keepAlive) keepAlive.pause();
}

// One meaning per button, from the session or from a keydown: play resumes,
// or reads the mark; previous says the unit again from its start; next is ⏭.
function mediaAction(a) {
  if (!on) return;
  if (a === "playpause") a = busy && !paused ? "pause" : "play";
  if (a === "play") { if (paused) resume(); else if (!busy && stash) toggle() }
  else if (a === "pause") pause();
  else if (a === "stop") halt();
  else if (a === "nexttrack") skip();
  else if (a === "previoustrack") restart();
}
function mediaHandlers(v) {
  const ms = MS();
  if (!ms) return;
  for (const a of ["play", "pause", "stop", "nexttrack", "previoustrack"])
    try { ms.setActionHandler(a, v ? () => mediaAction(a) : null) } catch (_) { }
}
// the same actions for media keys that arrive as keydowns (onKey)
const MEDIA_KEYS = {
  MediaPlayPause: "playpause", MediaPlay: "play", MediaPause: "pause", MediaStop: "stop",
  MediaTrackNext: "nexttrack", MediaTrackPrevious: "previoustrack"
};

function fail(er, L) {
  clearInterval(poll);
  setBusy(false);
  if (er === "language-unavailable" || er === "voice-unavailable" || er === "synthesis-unavailable")
    openMenu(noVoice(L));
  else console.warn("wudict: read aloud failed:", er || "unknown error");
}

const noVoice = L => "No voice for " + langName(L) + " on this device";

// ------------------------------------------------------------- the stash
// What was selected when it settled. Speaking reads this, never the live
// selection: the tap on the icon may itself collapse the selection (Android
// does), and the text has to survive it.
let stash = null, selLive = false, ctx = null;
const clean = s => String(s || "").replace(/\s+/g, " ").trim().slice(0, MAX);

function setStash(st) {
  const L = ctx && ctx.langs ? ctx.langs(st.dict) : {};
  st.a = norm(L.a);
  st.h = norm(L.h);
  st.text = clean(st.text);
  // what the voice gets: abbreviations in full, no pronunciation (pick.js)
  st.say = clean(st.say) || st.text;
  st.sc = scriptOf(st.say);
  // A unit marked by a tap (pick.js, D192): its kind, and a lang= the article
  // declares around it. Both arrive from article frames too: checked here.
  st.tap = st.tap === true;
  st.next = st.next === true;
  st.unit = st.unit === "x" ? "x" : "";
  const hint = norm(st.hint);
  st.hint = /^[a-z]{2,3}$/.test(hint) ? hint : "";
  relang(st);
  stash = st;
  selLive = true;
  startVoices();
  show();
}

// The language a selection is read in: picked in the menu for this
// selection, else picked for this dictionary and script, else decided. The
// alternatives "Read as" offers: the decided language, the one decided without
// the default voice (the guess it replaced), the dictionary's two, and for a
// headword the articles' pick it follows when it has none of its own.
function relang(st) {
  // A headword is in the headword language whatever its script shares with
  // the articles: it settles the Latin/Latin pairs the script cannot split.
  // So, by the lexicographer's convention, is an example (D192): de→en
  // examples are German. A lang= the article declares around the unit
  // outranks both, when it fits the script.
  const [p, q] = st.kind === "head" || st.unit === "x" ? [st.h, st.a] : [st.a, st.h];
  st.auto = (st.hint && fits(st.hint, st.sc) && st.hint) || decide(st.sc, p, q, ttsLang);
  st.lang = st.pick || storedLang(st) || st.auto;
  const plain = decide(st.sc, p, q, "");
  st.alts = [...new Set([st.auto, plain, st.a, st.h, inherited(st)])]
    .filter(l => l && l !== st.lang && (!st.sc || fits(l, st.sc)));
}

// A language picked in the menu is kept per dictionary and script: an en→ru
// dictionary may want German for its Latin text and still Russian for its
// Cyrillic. Headwords keep their own: an en→fr pick of French for the articles
// is not one for them. In a dictionary whose languages are both unknown they
// follow the articles' pick until they are given their own. Examples marked
// by a tap ("x") are kept apart the same way: they lean the other way.
const keyKind = st => st.kind === "head" ? "h" : st.unit === "x" ? "x" : "";
const langKey = (st, kind = keyKind(st)) => st.dict && st.sc ?
  LANG_KEY + kind + st.sc + "_" + st.dict : "";

function readLang(st, k) {
  let v = null;
  if (k) try { v = localStorage.getItem(k) } catch (_) { }
  return v && /^[a-z]{2,3}$/.test(v) && fits(v, st.sc) ? v : "";
}
const inherited = st => keyKind(st) && !st.a && !st.h ? readLang(st, langKey(st, "")) : "";
const storedLang = st => readLang(st, langKey(st)) || inherited(st);

function choose(L) {
  const k = langKey(stash);
  // Picking what the selection gets without a pick of its own forgets the pick.
  if (k) try { L === (inherited(stash) || stash.auto) ? localStorage.removeItem(k) : localStorage.setItem(k, L) } catch (_) { }
  stash.pick = L;
  relang(stash);
  syncMore();
  go(L, null);
}

// Languages with a voice that the menu does not offer already, in the
// selection's script. A walk over the voice list's languages (tens), not its
// voices.
function others() {
  const shown = new Set([stash.lang, ...stash.alts]);
  return voiceLangs.filter(l => !shown.has(l) && (!stash.sc || fits(l, stash.sc)));
}

// ------------------------------------------------------------------ UI
const SVG = 'viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" focusable="false"';
const ICON_SPEAK = '<svg ' + SVG + '><path d="M2.5 6h2.5l3.5-3v10l-3.5-3H2.5z"/><path d="M11 5.8a3 3 0 0 1 0 4.4"/><path d="M12.9 3.9a5.8 5.8 0 0 1 0 8.2"/></svg>';
const ICON_STOP = '<svg ' + SVG + '><rect x="4" y="4" width="8" height="8" rx="1.3" fill="currentColor"/></svg>';
const ICON_PLAY = '<svg ' + SVG + '><path d="M5 3.5l7 4.5-7 4.5z"/></svg>';
const ICON_MORE = '<svg ' + SVG + '><path d="M4.5 6.5l3.5 3.5 3.5-3.5"/></svg>';
const ICON_NEXT = '<svg ' + SVG + '><path d="M4 3.5l6 4.5-6 4.5z"/><path d="M12.5 3.5v9"/></svg>';
const ICON_CLOSE = '<svg ' + SVG + '><path d="M4.5 4.5l7 7M11.5 4.5l-7 7"/></svg>';
const CSS =
  ".wd-speak{position:fixed;z-index:150;display:flex;background:var(--bg-card,#fff);color:var(--fg,#222);" +
  "border:1px solid var(--line,#ddd);border-radius:999px;box-shadow:var(--shadow,0 2px 8px rgba(0,0,0,.2));overflow:hidden;" +
  "user-select:none;-webkit-user-select:none;-webkit-touch-callout:none;touch-action:manipulation}" +
  ".wd-speak[hidden],.wd-speak button[hidden],.wd-sp-menu[hidden]{display:none}" +
  ".wd-speak button{all:unset;display:flex;align-items:center;justify-content:center;cursor:pointer;" +
  "color:inherit;-webkit-tap-highlight-color:transparent;width:30px;height:30px}" +
  ".wd-speak .wd-sp-more{width:18px;border-left:1px solid var(--line-soft,#eee)}" +
  ".wd-speak .wd-sp-next{border-left:1px solid var(--line-soft,#eee)}" +
  ".wd-speak .wd-sp-close{color:var(--fg-faint,#999);border-right:1px solid var(--line-soft,#eee)}" +
  ".wd-speak svg{width:16px;height:16px}.wd-speak .wd-sp-more svg{width:12px;height:12px}" +
  ".wd-speak.touch button{width:48px;height:48px}.wd-speak.touch .wd-sp-more{width:30px}" +
  ".wd-speak.touch .wd-sp-close{width:40px}.wd-speak.touch .wd-sp-close svg{width:16px;height:16px}" +
  ".wd-speak.touch svg{width:22px;height:22px}.wd-speak.touch .wd-sp-more svg{width:14px;height:14px}" +
  ".wd-speak.busy .wd-sp-main{color:var(--accent,#e08600)}" +
  ".wd-speak button:hover{background:var(--accent-soft,#fff3e0)}" +
  ".wd-speak button:focus-visible,.wd-sp-menu button:focus-visible{outline:2px solid var(--focus,#7aa7d1);outline-offset:-2px}" +
  ".wd-sp-menu{position:fixed;z-index:151;min-width:12em;max-width:min(22em,calc(100vw - 16px));max-height:50vh;" +
  "overflow:auto;background:var(--bg-card,#fff);color:var(--fg,#222);border:1px solid var(--line,#ddd);" +
  "border-radius:10px;box-shadow:var(--shadow,0 2px 8px rgba(0,0,0,.2));padding:4px 0;" +
  "font:14px/1.35 var(--font,system-ui,sans-serif);user-select:none;-webkit-user-select:none}" +
  ".wd-sp-menu button{all:unset;box-sizing:border-box;display:flex;gap:.8em;align-items:baseline;width:100%;" +
  "padding:9px 12px;cursor:pointer}" +
  ".wd-sp-menu button:hover{background:var(--accent-soft,#fff3e0)}" +
  ".wd-sp-menu button[aria-checked=true]{color:var(--accent,#e08600)}" +
  ".wd-sp-menu small{margin-left:auto;color:var(--fg-faint,#999);font-size:12px}" +
  ".wd-sp-menu .note{padding:9px 12px;color:var(--fg-soft,#666)}" +
  ".wd-sp-menu hr{border:0;border-top:1px solid var(--line-soft,#eee);margin:4px 0}";

let ui = null, mainBtn = null, moreBtn = null, nextBtn = null, closeBtn = null;
let menu = null, menuNote = "", menuOther = false;
let touch = false, raf = 0;

function build() {
  if (ui) return;
  const st = document.createElement("style");
  st.textContent = CSS;
  document.head.appendChild(st);
  ui = document.createElement("div");
  ui.className = "wd-speak";
  ui.hidden = true;
  // ✕ first, farthest from the thumb that docks this to the right: putting
  // the mark away is the one button here a stray touch should not find.
  ui.innerHTML = '<button type="button" class="wd-sp-close" aria-label="Close" title="Close">' + ICON_CLOSE + "</button>" +
    '<button type="button" class="wd-sp-main"></button>' +
    '<button type="button" class="wd-sp-next" aria-label="Next" title="Next">' + ICON_NEXT + "</button>" +
    '<button type="button" class="wd-sp-more" aria-haspopup="menu" aria-expanded="false" ' +
    'aria-label="Voice" title="Voice">' + ICON_MORE + "</button>";
  [closeBtn, mainBtn, nextBtn, moreBtn] = ui.children;
  menu = document.createElement("div");
  menu.className = "wd-sp-menu";
  menu.setAttribute("role", "menu");
  menu.hidden = true;
  // Pressing the icon must not take the selection or the focus away from the
  // article; the action happens on click, which is the gesture Safari and
  // Chrome require before they will speak.
  for (const el of [ui, menu]) el.addEventListener("pointerdown", e => e.preventDefault());
  mainBtn.addEventListener("click", toggle);
  nextBtn.addEventListener("click", skip);
  closeBtn.addEventListener("click", dismiss);
  moreBtn.addEventListener("click", e => {
    if (!menu.hidden) { closeMenu(); return }
    openMenu("", e.detail === 0);
  });
  document.body.append(ui, menu);
  setBusy(busy);
}

function setBusy(v) {
  busy = v;
  mediaSync();
  if (!mainBtn) return;
  ui.classList.toggle("busy", v);
  mainBtn.innerHTML = paused ? ICON_PLAY : v ? ICON_STOP : ICON_SPEAK;
  const label = paused ? "Resume reading" : v ? "Stop reading" : "Read aloud";
  mainBtn.setAttribute("aria-label", label);
  mainBtn.title = label;
}

// Until the voice list has arrived the language is not final: the default
// voice can still change it (relang). A tap in that window shows Stop and
// waits for the list, or for startVoices' 3.5 s limit, then reads.
function go(L, voice) {
  if (!voicesDone) { waiting = true; setBusy(true); return }
  const v = voice || pickVoice(L);
  if (!v && voicesDone) { openMenu(noVoice(L)); return }
  say(stash.say, L, v);
}

// The ▾ is worth its room only when it has a choice to offer: a tapped unit
// always has one, Keep reading. ⏭ shows when there is a next unit; ✕ on touch
// only, where there is no Escape and no margin a finger finds reliably.
function syncMore() {
  if (!moreBtn || !stash) return;
  moreBtn.hidden = !(stash.tap || stash.alts.length || voicesFor(stash.lang).length > 1 ||
    (voicesDone && !voicesFor(stash.lang).length) || settings() || others().length);
  nextBtn.hidden = !(stash.tap && stash.next && ctx && ctx.step);
  closeBtn.hidden = !touch;
}

// ✕: the reading ends and the mark goes.
function dismiss() {
  closeMenu();
  pendingRead = 0;
  stop();
  if (stash && ctx && ctx.clear) ctx.clear(stash);
  stash = null;
  selLive = false;
  if (ui) ui.hidden = true;
}

const settings = () => window.__wdSpeech && typeof window.__wdSpeech.settings === "function";

function show() {
  build();
  touch = stash.ptr === "touch";
  ui.classList.toggle("touch", touch);
  syncMore();
  ui.hidden = false;
  place();
}

function hide() {
  if (!ui) return;
  closeMenu();
  if (busy) return; // Stop stays until the reading ends
  ui.hidden = true;
  cancelAnimationFrame(raf);
}

// Where the selection ends, in viewport pixels; null when it is gone.
function anchorRect() {
  if (!stash) return null;
  if (stash.kind === "frame") {
    const f = stash.frame, r = stash.rect;
    if (!f.isConnected || !r) return null;
    const b = f.getBoundingClientRect();
    return { left: b.left + r.x, top: b.top + r.y, right: b.left + r.x + r.w, bottom: b.top + r.y + r.h, height: r.h };
  }
  if (!stash.range) return null;
  const rs = stash.range.getClientRects();
  const b = rs.length ? rs[rs.length - 1] : stash.range.getBoundingClientRect();
  return b.width || b.height ? b : null;
}

// Touch: docked in the thumb zone, where neither the system's selection menu
// (above the selection) nor its handles (below it) can ever be. Mouse: just
// after the end of the selection, where the pointer already is.
function place() {
  if (!ui || ui.hidden) return;
  if (touch) {
    ui.style.cssText = "right:16px;bottom:calc(24px + var(--wd-inset-bottom,0px))";
    return;
  }
  const r = selLive ? anchorRect() : null;
  if (!r) { if (!busy) ui.hidden = true; return } // busy with no anchor: stay where it was
  const w = ui.offsetWidth, h = ui.offsetHeight, top = ctx && ctx.top ? ctx.top() : 0;
  let x = r.right + 6, y = r.top + (r.height - h) / 2;
  if (x + w > innerWidth - 8) { x = Math.max(8, r.right - w); y = r.bottom + 6 }
  const off = y < top || y + h > innerHeight;
  ui.style.cssText = "left:" + Math.round(x) + "px;top:" + Math.round(y) + "px" + (off ? ";visibility:hidden" : "");
  if (!menu.hidden) placeMenu();
}

function track() {
  if (touch || !ui || ui.hidden) return;
  cancelAnimationFrame(raf);
  raf = requestAnimationFrame(place);
}

function openMenu(note, focusFirst) {
  if (!stash) return;
  build();
  menuNote = note || "";
  const keep = menu.hidden ? 0 : menu.scrollTop; // a voice list arriving must not scroll the open menu
  menu.textContent = "";
  const add = (label, tag, fn, checked) => {
    const b = document.createElement("button");
    b.type = "button";
    b.setAttribute("role", checked == null ? "menuitem" : "menuitemradio");
    if (checked != null) b.setAttribute("aria-checked", checked ? "true" : "false");
    b.append((checked ? "✓ " : "") + label);
    if (tag) { const s = document.createElement("small"); s.textContent = tag; b.append(s) }
    b.addEventListener("click", e => { closeMenu(); fn(e) });
    menu.append(b);
  };
  const rule = () => { if (menu.childNodes.length) menu.append(document.createElement("hr")) };
  if (menuNote) {
    const d = document.createElement("div");
    d.className = "note";
    d.textContent = menuNote;
    menu.append(d);
  }
  if (stash.alts.length) {
    rule();
    // the language being read, checked: a remembered pick shows itself here
    const cur = stash.lang;
    add("Read as " + langName(cur), "", () => choose(cur), true);
    for (const l of stash.alts) add("Read as " + langName(l), "", () => choose(l), false);
  }
  if (stash.tap) {
    rule();
    const k = keepOn();
    add("Keep reading", "", () => setKeep(!k), k);
    menu.lastChild.setAttribute("role", "menuitemcheckbox");
  }
  const L = stash.lang, vs = voicesFor(L);
  if (vs.length) {
    rule();
    const cur = pickVoice(L);
    for (const v of vs.slice().sort((a, b) => String(a.label || a.name).localeCompare(String(b.label || b.name))))
      add(v.label || v.name, v.lang, () => {
        try { localStorage.setItem(VOICE_KEY + L, v.voiceURI) } catch (_) { }
        say(stash.say, L, v); // choosing IS the preview
      }, v === cur);
  }
  // Built only when asked for: a device can have voices in sixty languages.
  const more = others();
  if (more.length) {
    rule();
    if (!menuOther) {
      const note = menuNote;
      add("Other languages…", "", e => {
        menuOther = true;
        openMenu(note);
        const h = menu.querySelector(".wd-sp-other");
        if (!h) return;
        menu.scrollTop = h.offsetTop - 4;
        if (e.detail === 0 && h.nextElementSibling) h.nextElementSibling.focus();
      });
    } else {
      const h = document.createElement("div");
      h.className = "note wd-sp-other";
      h.textContent = "Other languages";
      menu.append(h);
      for (const [name, l] of more.map(l => [langName(l), l]).sort((x, y) => x[0].localeCompare(y[0])))
        add(name, "", () => choose(l));
    }
  }
  if (settings()) { rule(); add("Speech settings…", "", () => window.__wdSpeech.settings()) }
  if (!menu.childNodes.length) return;
  menu.hidden = false;
  moreBtn.setAttribute("aria-expanded", "true");
  if (ui.hidden) { ui.hidden = false; place() }
  placeMenu();
  if (keep) menu.scrollTop = keep;
  if (focusFirst) { const b = menu.querySelector("button"); if (b) b.focus() }
}

function placeMenu() {
  const b = ui.getBoundingClientRect(), mw = menu.offsetWidth, mh = menu.offsetHeight;
  const x = Math.max(8, Math.min(b.right - mw, innerWidth - mw - 8));
  const below = b.bottom + 6 + mh <= innerHeight - 8;
  const y = touch || !below ? Math.max(8, b.top - 6 - mh) : b.bottom + 6;
  menu.style.left = Math.round(x) + "px";
  menu.style.top = Math.round(y) + "px";
}

function closeMenu() {
  if (!menu || menu.hidden) return;
  menu.hidden = true;
  menuNote = "";
  menuOther = false;
  moreBtn.setAttribute("aria-expanded", "false");
}

// ------------------------------------------------------------ selections
// Shadow-DOM articles. The standard read is getComposedRanges; Chrome before
// it had only ShadowRoot.getSelection, and older Firefox exposed shadow nodes
// through the document's own ranges.
let root = null, ptr = "mouse", btn = false, settleT = 0;

function readShadow() {
  const host = root && root.host;
  if (!host || !host.isConnected) return null;
  const doc = document.getSelection();
  // Chrome's own shadow selection renders the text as laid out (line breaks,
  // no hidden nodes); a bare range's toString is the fallback elsewhere.
  const sel = root.getSelection ? root.getSelection() : null;
  let range = null;
  if (doc && doc.getComposedRanges) {
    let sr = null;
    try { sr = doc.getComposedRanges({ shadowRoots: [root] })[0] }
    catch (_) { try { sr = doc.getComposedRanges(root)[0] } catch (_) { } }
    if (sr && !sr.collapsed) {
      try {
        range = document.createRange();
        range.setStart(sr.startContainer, sr.startOffset);
        range.setEnd(sr.endContainer, sr.endOffset);
      } catch (_) { range = null }
    }
  }
  if (!range) {
    const s = sel || doc;
    if (!s || !s.rangeCount) return null;
    range = s.getRangeAt(0).cloneRange();
  }
  if (range.collapsed || range.commonAncestorContainer.getRootNode() !== root) return null;
  const text = ((sel && String(sel)) || range.toString()).trim();
  return text ? { kind: "shadow", text, range, dict: host.dataset.dict || "", ptr } : null;
}

// The headword: a <dt> in the page's own DOM, beside the article's shadow
// root. The selection starts in it and ends in it, or just past it with
// nothing selected in between, as a triple click's does. One that runs on
// into the article is a mixed selection and gets no button.
function readHead() {
  const doc = document.getSelection();
  if (!doc || !doc.rangeCount) return null;
  const sr = doc.getRangeAt(0), n = sr.startContainer;
  const el = n.nodeType === Node.ELEMENT_NODE ? n : n.parentElement;
  const head = el && el.closest("details.dict>dl>dt");
  if (!head || sr.collapsed) return null;
  const range = sr.cloneRange(), dd = head.nextElementSibling;
  if (!head.contains(range.endContainer)) {
    if (!nothingPast(doc, sr, head, dd && dd.firstElementChild)) return null;
    range.setEnd(head, head.childNodes.length);
  }
  const text = range.toString().trim();
  return text ? { kind: "head", text, range, dict: (dd && dd.dataset.dict) || "", ptr } : null;
}

// Chrome ends the document range of a triple click and of a drag into the
// article alike at the shadow host, or past the article when it opens inline;
// only the composed range shows whether article text is in it, and it stays
// at (host, 0) when none is. Without one, an end at or past the host is a
// drag as far as we can tell.
function nothingPast(doc, sr, head, art) {
  const sh = art && art.shadowRoot;
  let c = sr.endContainer, o = sr.endOffset, cr = null;
  if (sh && doc.getComposedRanges) {
    try { cr = doc.getComposedRanges({ shadowRoots: [sh] })[0] }
    catch (_) { try { cr = doc.getComposedRanges(sh)[0] } catch (_) { } }
    if (cr) { c = cr.endContainer; o = cr.endOffset }
  }
  const t = document.createRange(), r = c.getRootNode();
  if (sh && r === sh) t.setStart(sh, 0);
  else if (r === document) t.setStart(head, head.childNodes.length);
  else return false;
  t.setEnd(c, o);
  if (seesText(t)) return false;
  return (cr && c === art && o === 0) || !(art && t.intersectsNode(art));
}

// Whether a range covers text a reader can see. toString also counts what is
// never rendered (the <style> an article's shadow root opens with, <title>,
// display:none), and such text has no layout box.
function seesText(r) {
  const w = document.createTreeWalker(r.commonAncestorContainer, NodeFilter.SHOW_TEXT), box = document.createRange();
  for (let n = w.nextNode(); n; n = w.nextNode()) {
    if (r.comparePoint(n, 0) > 0) break;
    if (!r.intersectsNode(n)) continue;
    const a = n === r.startContainer ? r.startOffset : 0, b = n === r.endContainer ? r.endOffset : n.data.length;
    if (!n.data.slice(a, b).trim()) continue;
    box.setStart(n, a);
    box.setEnd(n, b);
    if (box.getClientRects().length) return true;
  }
  return false;
}

function settle() {
  if (btn) return; // the drag is still going; pointerup settles it
  const st = readShadow() || readHead();
  // The selection a tap marked, settling: the stash already has it, with what
  // only the tap knew - its kind and its declared language.
  if (st && stash && stash.tap && stash.kind === st.kind &&
    (stash.text === clean(st.text) || sameRange(stash.range, st.range))) {
    if (!selLive) { selLive = true; show() }
    return;
  }
  if (st) {
    // what it is read as (pick.js): worked out only for a selection that is news
    if (window.wuSpoken) st.say = window.wuSpoken(st.range);
    setStash(st);
    return;
  }
  if (stash && stash.kind !== "frame") { selLive = false; hide() }
}

// Ranges in different trees cannot be compared, and are not the same.
function sameRange(a, b) {
  if (!a || !b) return false;
  try {
    return a.compareBoundaryPoints(Range.START_TO_START, b) === 0 &&
      a.compareBoundaryPoints(Range.END_TO_END, b) === 0;
  } catch (_) { return false }
}

function schedule(ms) { clearTimeout(settleT); settleT = setTimeout(settle, ms) }

function onDown(e) {
  if (ui && (ui.contains(e.target) || menu.contains(e.target))) return;
  closeMenu();
  pendingRead = 0;
  // A frame keeps its selection when the host is clicked, and reports nothing.
  if (stash && stash.kind === "frame") { selLive = false; hide() }
  ptr = e.pointerType || "mouse";
  if (ptr === "mouse" && e.button === 0) btn = true;
  const host = e.composedPath().find(el => el.classList && el.classList.contains("article"));
  root = (host && host.shadowRoot) || null;
}
function onUp(e) {
  if (!btn || e.pointerType !== "mouse") return;
  btn = false;
  schedule(120);
}
// Android's selection handles deliver nothing to the page while they are
// dragged; a selection that has stopped changing is the only signal there is.
function onSel() { schedule(ptr === "touch" ? 350 : 200) }
// One listener for every key read aloud answers: Escape, Space (pause), the
// media keys where they arrive as keydowns (a WebView, a desktop whose browser
// leaves them to pages) while there is a mark, and the keys that scroll.
function onKey(e) {
  const k = e.key;
  if (k === "Escape") {
    if (menu && !menu.hidden) closeMenu();
    else halt();
    return;
  }
  const a = spaceIsOurs(e) ? "space" : stash ? MEDIA_KEYS[k] : "";
  if (a) {
    e.preventDefault();
    e.stopPropagation();
    if (e.repeat) return;
    if (a === "space") space();
    else mediaAction(a);
    return;
  }
  if (SCROLL_KEY.test(k)) userScroll = Date.now();
}
function onScroll() { track() }

let on = false;
function enable(v) {
  if (v === on) return;
  on = v;
  const f = (v ? window.addEventListener : window.removeEventListener).bind(window);
  const d = (v ? document.addEventListener : document.removeEventListener).bind(document);
  d("selectionchange", onSel);
  d("pointerdown", onDown, true);
  d("pointerup", onUp, true);
  d("keydown", onKey, true);
  f("wheel", onUserScroll, { capture: true, passive: true });
  f("touchmove", onUserScroll, { capture: true, passive: true });
  f("scroll", onScroll, { capture: true, passive: true });
  f("resize", onScroll, { passive: true });
  mediaHandlers(v);
  if (!v) { stop(); selLive = false; stash = null; hide(); mediaOff() }
}

window.wuSpeak = {
  // ctx: { langs(dictID) -> {a, h}, top() -> px the bar covers };
  // seed: the article root and pointer the selection that woke us came from.
  on(v, c, seed) {
    if (c) ctx = c;
    enable(!!v);
    if (v && seed) { root = seed.root || root; ptr = seed.ptr || ptr; schedule(0) }
  },
  // A selection inside a sandboxed (iframe) article, as frame.js reports it:
  // d = {s: its text, r: where it ends, p: the pointer, sp: what it is read
  // as}, and for a unit a tap marked (D192) u: its kind, l: its declared
  // language, n: whether a next unit exists. Another document's data: every
  // field is checked here or in setStash.
  frame(f, d) {
    if (!on || !f || !d) return;
    const text = String(d.s || ""), r = d.r, u = d.u;
    if (!text.trim()) {
      if (stash && stash.kind === "frame" && stash.frame === f) { selLive = false; hide() }
      return;
    }
    const rect = r && isFinite(r.x) && isFinite(r.y) && isFinite(r.w) && isFinite(r.h) ?
      { x: +r.x, y: +r.y, w: +r.w, h: +r.h } : null;
    const tap = typeof u === "string" && u !== "";
    // A new mark ends the reading; the same one, reported again as its
    // selection settles, must not - nor may it pass for the next unit asked for.
    const fresh = tap && !(stash && stash.frame === f && stash.text === clean(text));
    const auto = fresh && arrived();
    if (fresh && busy) halt();
    setStash({
      kind: "frame", frame: f, rect, text, dict: f.dataset.dict || "", ptr: d.p === "touch" ? "touch" : "mouse",
      tap, unit: u, hint: d.l, next: d.n === true, say: typeof d.sp === "string" ? d.sp : ""
    });
    if (auto) { reveal(pendingForce); read() }
  },
  // A unit a tap marked in a shadow-DOM article or a headword (pick.js, D192):
  // st = {kind: "shadow"|"head", text, say, range, dict, ptr, unit, hint,
  // next}. Shown at once, without waiting for the selection to settle.
  unit(st, r) {
    if (!on || !st || !clean(st.text)) return;
    const auto = arrived();
    halt();
    if (r) root = r;
    ptr = st.ptr === "touch" ? "touch" : "mouse";
    st.tap = true;
    st.ptr = ptr;
    setStash(st);
    if (auto) { reveal(pendingForce); read() }
  },
  // An article frame found no next unit: nothing is coming.
  noNext() { pendingRead = 0; if (!busy && !selLive) hide() },
  // The second tap on a mark: read it, or stop reading it.
  toggle,
  // The same, from an article frame: only for the stash that frame owns.
  frameToggle(f) { if (stash && stash.kind === "frame" && stash.frame === f) toggle() },
  // Escape inside an article frame.
  hush() { halt() },
  // Space inside an article frame (frame.js hands every one over): true when
  // it was read aloud's to take - something is read or paused - and the page
  // must not scroll for it.
  space(repeat) {
    if (!busy) return false;
    if (!repeat) space();
    return true;
  }
};

function halt() {
  if (!busy) return;
  stop();
  if (!selLive) hide();
}

function toggle() {
  if (!on || !stash) return;
  if (paused) { resume(); return }
  if (busy) { halt(); return }
  readStart = Date.now();
  read();
  // Text the docked speaker covers is brought clear of it now, when reading
  // starts - never at the mark, under the finger that just made it.
  reveal(false);
}

function read() {
  closeMenu();
  // the tap restored the mark's selection; settle confirms it, or hides
  selLive = true;
  show();
  go(stash.lang, null);
}
})();
