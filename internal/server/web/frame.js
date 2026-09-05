/**
 * Copyright (C) 2026 glowinthedark
 *
 * SPDX-License-Identifier: GPL-3.0-or-later
 */

// wudict iframe bridge: runs inside sandboxed article iframes
// (script-bearing dictionaries). Reports content height, forwards
// bword:// lookups and double-click word lookups to the app, follows
// theme changes.
(function () {
	"use strict";
	var fid = document.currentScript.dataset.fid;
	var dictID = document.currentScript.dataset.dict;
	// a #fragment the incoming cross-reference asked for, jumped to once the
	// article has reported its height (see the load handler)
	var wantFrag = document.currentScript.dataset.frag || "";
	var audioEl = null; // reused across clicks so playback isn't GC'd mid-load

	// ------------------------------------------------------------------ host
	// The app window. Captured before `window.parent` is replaced below, and
	// from here on the only channel this file uses to reach the application.
	var HOST = window.parent;

	// --- the article's faults become visible ------------------------------
	// A dead article and a working one look identical from the outside: one
	// unanswerable question about the host silently costs every feature the
	// dictionary has. Register before anything else runs.
	function report(msg, src, line, col) {
		try {
			HOST.postMessage({
				t: "jserr", fid: fid, dict: dictID,
				msg: String(msg == null ? "script error" : msg),
				src: String(src || ""), line: +line || 0, col: +col || 0
			}, "*");
		} catch (e) { /* the app is gone; there is nobody to tell */ }
	}
	// Bubble phase deliberately: a failed resource load (a 404 image in the
	// article's own markup) only reaches window in the capture phase, and is
	// not a script fault.
	addEventListener("error", function (e) {
		report((e.error && e.error.message) || e.message, e.filename, e.lineno, e.colno);
	});
	addEventListener("unhandledrejection", function (e) {
		var r = e.reason;
		report((r && r.message) || r, "", 0, 0);
	});

	// --- the soft value ---------------------------------------------------
	// "Nothing here", in every idiom JavaScript has for asking. A function, so
	// `typeof` reports "function"; calling it or reading any property yields
	// another one, so `parent.a.b.c()` chains instead of throwing; length 0 and
	// no items, so `parent.$("#k_iframe").length` correctly answers falsy.
	function softValue(path) {
		var target = function () {};
		return new Proxy(target, {
			apply: function () { return softValue(path + "()"); },
			construct: function () { return softValue("new " + path); },
			get: function (t, k) {
				if (k === "length") return 0;
				if (k === "name") return "";
				if (k === "then") return undefined;  // never a thenable: an await must not hang
				if (k === "prototype") return t.prototype;
				if (k === "toString" || k === "toJSON" || k === "toLocaleString")
					return function () { return ""; };
				if (k === "valueOf") return function () { return 0; };
				if (k === Symbol.iterator)
					return function () { return [][Symbol.iterator](); };
				if (k === Symbol.toPrimitive)
					return function (hint) { return hint === "number" ? 0 : ""; };
				if (typeof k === "symbol") return undefined;
				if (/^\d+$/.test(k)) return undefined;  // an empty collection has no items
				return softValue(path + "." + k);
			},
			// `prototype` is the function target's one non-configurable own
			// property; denying it would break the Proxy invariant.
			has: function (t, k) { return k === "prototype"; },
			set: function () { return true; },
			deleteProperty: function () { return true; }
		});
	}

	// --- window.parent is a declared contract, not the app's namespace -----
	// `parent` is [Replaceable] in the HTML spec's Window IDL, so assigning it
	// replaces the accessor with a data property. What an article finds there
	// is now a fixed, honest surface rather than ~100 of this application's
	// top-level names, which it was never designed to read and whose meanings
	// are not the ones it assumes (ODE 2024 asks for `parent.$` expecting
	// jQuery, gets our getElementById helper, and dies on the first line of
	// its initialiser).
	var IDENT = { self: 1, window: 1, parent: 1, top: 1 };
	var LIVE = { innerWidth: 1, innerHeight: 1, outerWidth: 1, outerHeight: 1, devicePixelRatio: 1 };

	function inertLocation(loc) {
		var out = {};
		["href", "origin", "protocol", "host", "hostname", "port", "pathname", "search", "hash"]
			.forEach(function (k) {
				var v = ""; try { v = String(loc[k]); } catch (e) { }
				// accessor, not a frozen value: a strict-mode article assigning
				// parent.location.href must be inert, not fatal.
				Object.defineProperty(out, k, {
					get: function () { return v; }, set: function () { }, enumerable: true
				});
			});
		out.toString = function () { return out.href; };
		out.assign = out.replace = out.reload = function () { };
		return out;
	}

	var hostAPI = {
		// the frame/app protocol stays open to the dictionary's own scripts
		postMessage: function () { return HOST.postMessage.apply(HOST, arguments); },
		// blank and detached: `parent.document.querySelector("#k_iframe")` is
		// null by contract instead of by luck. This is a COMPATIBILITY shim,
		// not a sandbox - the frame is same-origin by necessity (the article
		// has to be styled and measured), so a script that goes looking can
		// still reach the app through another route. A dictionary whose own
		// scripts are hostile is trusted code the user installed; what this
		// prevents is the far commoner accident, an article's script written
		// for a full page walking into the app and breaking it.
		document: document.implementation.createHTMLDocument(""),
		location: inertLocation(HOST.location),
		frames: [], length: 0, closed: false, name: ""
	};
	var own = Object.create(null);   // whatever the article writes lands here
	var softs = Object.create(null);
	var facade;

	function facadeGet(t, k) {
		if (k in t) return t[k];
		if (IDENT[k] === 1) return facade;  // parent.parent must not hand back the real window
		if (Object.prototype.hasOwnProperty.call(hostAPI, k)) return hostAPI[k];
		if (LIVE[k] === 1) { try { return HOST[k]; } catch (e) { return 0; } }
		if (typeof k === "symbol") return undefined;
		if (!softs[k]) {
			softs[k] = softValue("parent." + k);
			// The one line that names the next unknown host API without anyone
			// opening a debugger.
			try { console.debug("wudict: article read parent." + k + " - answering empty"); } catch (e) { }
		}
		return softs[k];
	}
	function reserved(k) {
		return IDENT[k] === 1 || LIVE[k] === 1 ||
			Object.prototype.hasOwnProperty.call(hostAPI, k);
	}
	facade = new Proxy(own, {
		get: facadeGet,
		// Refusing the contract names keeps ownKeys duplicate-free, which is a
		// Proxy invariant, not a preference.
		set: function (t, k, v) { if (!reserved(k)) t[k] = v; return true; },
		defineProperty: function (t, k, d) { if (!reserved(k)) Object.defineProperty(t, k, d); return true; },
		deleteProperty: function (t, k) { if (!reserved(k)) delete t[k]; return true; },
		// honest: `"mdict" in parent` is false, so in/typeof probes get the
		// truth and only call-shaped probes get the soft value.
		has: function (t, k) { return k in t || reserved(k); },
		ownKeys: function (t) {
			return Object.getOwnPropertyNames(t)
				.concat(Object.keys(IDENT), Object.keys(hostAPI), Object.keys(LIVE));
		},
		getOwnPropertyDescriptor: function (t, k) {
			var d = Object.getOwnPropertyDescriptor(t, k);
			if (d) return d;
			if (reserved(k))
				return { value: facadeGet(t, k), writable: false, enumerable: true, configurable: true };
			return undefined;
		}
	});

	if (HOST && HOST !== window) {
		try { window.parent = facade; } catch (e) { }
		// no [LegacyUnforgeable], and [Global] makes it a configurable own
		// property. null is what a top-level or cross-origin frame reports, so
		// it is the answer least likely to surprise - and it removes the most
		// obvious handle on the app's DOM. Not the last one: see hostAPI above
		// for why that is a shim and not a boundary.
		try {
			Object.defineProperty(window, "frameElement", {
				get: function () { return null; }, configurable: true
			});
		} catch (e) { }
	}

	// Reports the content's natural height so the parent can size this frame.
	//
	// The rule this function exists to obey (D60): every term must be a function of
	// the CONTENT, never of the frame we were last given. The parent sizes us from
	// what we report, so anything that reads our own viewport back is a RATCHET —
	// it can only grow, and it grows once per measurement.
	//
	// For the ROOT element, scrollHeight is DEFINED as max(scrolling area, VIEWPORT),
	// and the viewport here IS the iframe the parent already sized from our last
	// answer. Invisible while every article only ever gets taller; obvious the moment
	// content can shrink in place — Cambridge's entry tabs switch from a long
	// "English" panel to a short "American" one and left ~400px of blank frame below.
	// So it is consulted only when it genuinely EXCEEDS the viewport, i.e. when it
	// reports real overflow rather than the clamp.
	//
	// The rest of D60's terms are content-driven only while the ARTICLE'S OWN
	// STYLESHEET leaves html and body alone. Stylesheets written for a full page do
	// not: LDOCE's ldoce.css opens with `html, body { height: 100% }`, where 100% is
	// now ours, and then body's box, the root's box and (via body's 8px margins) the
	// root's scrolling area are all our own height handed back — 22px of growth per
	// pass, to the parent's 60000px cap. frameDoc neutralises that at the source with
	// a trailing !important override, and this is the backstop for an article that
	// defeats it: when body's box IS the viewport, nothing body or the root reports
	// can be believed, and the extent of body's in-flow children is measured instead.
	//
	// Why children and not a Range over body's contents: a Range unions the border
	// boxes of everything it contains, DESCENDANTS INCLUDED, so an absolutely
	// positioned box anchored near the viewport bottom lands in the union and the
	// ratchet returns by another door — measured at +241px on Merriam-Webster
	// Online, whose stylesheet has eight `position:absolute` rules. An element's own
	// border box excludes its out-of-flow descendants, which is exactly the property
	// wanted here.
	//
	// The +8 and +16 are body's 8px margins (see frameDoc). They do not collapse into
	// html, because html carries overflow-y:hidden and so establishes a block
	// formatting context — which is also why the root's own border box already
	// includes them, and why that measure needs no adjustment.
	function inflowBottom(b) {
		var bot = 0, seen = false;
		for (var n = b.firstElementChild; n; n = n.nextElementSibling) {
			var pos = getComputedStyle(n).position;
			if (pos === "fixed" || pos === "absolute") continue;
			var r = n.getBoundingClientRect();
			if (!r.height && !r.width) continue;
			seen = true;
			if (r.bottom > bot) bot = r.bottom;
		}
		// an article that is bare text, with no element children at all
		if (!seen && b.firstChild) {
			var rg = document.createRange();
			rg.selectNodeContents(b);
			bot = rg.getBoundingClientRect().bottom;
		}
		// viewport coordinates -> document coordinates. The frame never scrolls
		// itself, but a dictionary is free to make body a scroll container.
		return bot + (document.documentElement.scrollTop || 0) + (b.scrollTop || 0);
	}
	function post() {
		var d = document, e = d.documentElement, b = d.body;
		var viewport = e.clientHeight;
		var box = b ? b.getBoundingClientRect().height : 0;
		var h;
		if (b && Math.abs(box - viewport) <= 1) {
			// body is the viewport, to the pixel: it is being sized BY us, not by
			// its content. Equality is the test, not "at least" — on the first
			// measurement the frame is 90px and a real article is far taller.
			h = inflowBottom(b) + 8;
		} else {
			h = Math.max(
				e.getBoundingClientRect().height,
				b ? box + 16 : 0,
				b ? b.scrollHeight + 16 : 0,
				e.scrollHeight > viewport ? e.scrollHeight : 0
			);
		}
		HOST.postMessage({ t: "h", fid: fid, h: Math.ceil(h) }, "*");
	}
	addEventListener("message", function (e) {
		if (!e.data) return;
		if (e.data.t === "theme") {
			document.documentElement.classList.toggle("dark", !!e.data.dark);
		} else if (e.data.t === "fs") {
			// The parent bakes the size into our srcdoc at creation; this is
			// how every change AFTER that arrives, because a CSS custom
			// property cannot cross a document boundary. The bound is a
			// sanity check on an unauthenticated channel, not the app's
			// range — applyFS in index.html owns that.
			document.documentElement.style.fontSize =
				Math.min(64, Math.max(6, +e.data.px || 15)) + "px";
			// The ResizeObserver on <body> catches this too, but only after
			// layout settles; posting on the next frame lets the parent grow
			// the iframe in the same beat as the text reflows inside it.
			requestAnimationFrame(post);
		}
	});
	addEventListener("load", function () {
		if (window.ResizeObserver) {
			new ResizeObserver(post).observe(document.body);
		}
		post();
		// after post(), never before: the parent sizes this iframe from that
		// message, and a scroll target beyond an unsized 90px frame would be
		// clamped by a document that has not grown yet.
		if (wantFrag) { jumpToFragment(wantFrag); wantFrag = ""; }
		// late reflows: webfonts, MathJax typesetting, lazy images
		setTimeout(post, 600);
		setTimeout(post, 2500);
	});

	// The reference parser, mirroring parseRef in index.html — keep the two in
	// step. `bword://Some Headword#frag` is NOT a URL: "//" promises an
	// authority, which cannot hold the spaces that headwords are full of, so
	// this works by string position and never touches URL().
	var REF_SCHEME = /^(?:(?:bword|entry):(?:\/\/)?|[dx]:)/i;
	function decodeRef(s) { try { return decodeURIComponent(s); } catch (_) { return s; } }
	function parseRef(href) {
		var m = REF_SCHEME.exec(href || "");
		if (!m) return null;
		// split before decoding: a "#" inside a headword arrives as %23
		var rest = href.slice(m[0].length), frag = "";
		var h = rest.indexOf("#");
		if (h >= 0) { frag = rest.slice(h + 1); rest = rest.slice(0, h); }
		var word = decodeRef(rest).trim();
		frag = decodeRef(frag);
		if (word.charAt(0) === "@" && word.length > 1) return { kind: "sub", word: word, frag: frag };
		return { kind: word ? "lookup" : "anchor", word: word, frag: frag };
	}
	// A lookup-scheme link carrying only a fragment (`bword://#HistAI`) is the
	// article's own table of contents, not a cross-reference. We cannot scroll
	// to it here: the iframe is sized to its content and so never scrolls
	// itself. Report the target's offset and let the page move, where the fixed
	// bar and the section's sticky header are known.
	function jumpToFragment(frag) {
		if (!frag) return;
		var el = document.getElementById(frag);
		if (!el) {
			var named = document.querySelectorAll("a[name]");
			for (var i = 0; i < named.length && !el; i++) {
				if (named[i].getAttribute("name") === frag) el = named[i];
			}
		}
		if (!el) return;
		HOST.postMessage({
			t: "anchor", fid: fid,
			y: el.getBoundingClientRect().top + (window.pageYOffset || 0)
		}, "*");
	}

	// One referenced element, reused: a fresh `new Audio(url)` per click can be
	// collected during the (possibly slow, speexdec-transcoded) load. Decoding
	// follows the Content-Type, not the .spx extension in the URL.
	function playURL(url) {
		if (!url) return;
		if (!audioEl) audioEl = new Audio();
		audioEl.src = url;
		var p = audioEl.play();
		if (p && p.catch) p.catch(function (err) { console.warn("audio play failed:", url, err); });
	}

	// CAPTURE phase, deliberately. Dictionary scripts attach their own click
	// handlers inside the article — LDOCE6's entry.js calls stopPropagation()
	// on the speaker <img> so a play click does not also toggle the accordion
	// around it. A bubble-phase listener on `document` never sees those clicks,
	// so the browser followed the link and replaced the article with a bare
	// media player. Capture runs on the way down, before any of that.
	document.addEventListener("click", function (e) {
		var a = e.target && e.target.closest ? e.target.closest("a") : null;
		if (!a) {
			// GoldenDict-era pronunciation: <object type="audio/…" data="…">.
			// DSL emits a link now (D81), but every library folder prepared
			// before that still stores the object, and this renderer had no
			// handler for it at all — the audio was simply dead here.
			var o = e.target && e.target.closest ? e.target.closest("object") : null;
			if (o && /^audio\//i.test(o.getAttribute("type") || "")) {
				e.preventDefault();
				playURL(o.getAttribute("data") || "");
			}
			return;
		}
		var href = a.getAttribute("href") || "";
		var ref = parseRef(href);
		if (ref) {
			e.preventDefault();
			// We own this click completely, so take it off the wire. preventDefault
			// only cancels the browser's DEFAULT action; the dictionary's own
			// handlers still run, and LDOCE6's entry.js navigates PROGRAMMATICALLY
			// (assigning location from the anchor), which no preventDefault can
			// stop — hence "Failed to launch 'entry:@…'". Stopping propagation
			// during the document-level CAPTURE phase means the event never
			// reaches the anchor or any ancestor inside the article, so those
			// handlers never fire. Scoped to cross-reference links only: every
			// other click in the article still belongs to the dictionary.
			e.stopPropagation();
			if (e.stopImmediatePropagation) e.stopImmediatePropagation();
			// "@name" is not a word: MDict repacks store an article's expandable
			// sections (Examples, Collocations, Word Origin…) as headwords with
			// an "@" prefix. Following one as a search would replace the article
			// the user is reading with a fragment of it. Inline it instead,
			// where the link is — no search, no URL, no history.
			if (ref.kind === "sub") { toggleSub(a, ref.word); return; }
			if (ref.kind === "lookup") {
				// "ref", not "pick": an author's link is an exact headword and
				// carries where it was written, so the app searches that
				// dictionary and leaves the term untouched.
				HOST.postMessage({ t: "ref", w: ref.word, dict: dictID, frag: ref.frag }, "*");
				return;
			}
			jumpToFragment(ref.frag); // "anchor": a place in this same article
		} else if (/\.(mp3|ogg|wav|spx|m4a)([?#]|$)/i.test(href)) {
			e.preventDefault();
			playURL(href);
		} else if (a.classList && a.classList.contains("wudict-file")) {
			// A dictionary attachment this page cannot display (PDF, document,
			// a DSL video format no browser decodes). Following it in place
			// is the one thing that must not happen: with no
			// allow-top-navigation the sandbox would load it INSIDE this small
			// box, in place of the article. The parent opens the tab instead.
			e.preventDefault();
			HOST.postMessage({ t: "open", url: href }, "*");
		} else if (href.charAt(0) === "#") {
			// A fragment link inside an about:srcdoc document. This container
			// splits the two URLs that decide what "#x" means: the DOCUMENT URL
			// is about:srcdoc, while the BASE URL is inherited from the parent
			// page. Link resolution uses the base; same-document detection
			// compares against the document URL. They disagree, so the browser
			// resolves "#x" to <the app's own URL>#x, concludes it is a
			// different document, and performs a CROSS-document navigation —
			// loading wudict inside its own iframe, at the parent's exact query
			// string, in place of the article.
			//
			// preventDefault here is therefore not a guess about one dictionary:
			// the browser's default action is categorically wrong for every
			// fragment link in every srcdoc article. D44 exempted "#" on the
			// belief that "the browser follows those natively here" — true of an
			// ordinary document, false of this one, and it is the single entry
			// where this renderer and the shadow-DOM one genuinely differ, so
			// mirroring index.html's exclusion list was bound to get it wrong.
			//
			// Propagation is deliberately NOT stopped. Unlike a bword:// link,
			// this click belongs to the dictionary — Cambridge's English/American
			// tabs are jQuery handlers on <a href="#dataset-british"> — and
			// cancelling the navigation is the only thing they needed from us.
			e.preventDefault();
			var frag = decodeRef(href.slice(1));
			// After the dictionary's own handlers, never before: a tab or an
			// accordion changes what is visible, and an offset measured first
			// would describe the layout that is being replaced. post() before
			// the jump for the same reason the load handler does — the parent
			// sizes this frame from that message, and a target beyond the old
			// height would be clamped by a document that has not grown yet.
			setTimeout(function () { post(); jumpToFragment(frag); }, 0);
		} else if (/^https?:\/\//i.test(href)) {
			// An external link must LEAVE the article. The sandbox grants no
			// allow-top-navigation, so the browser's default is to navigate THIS
			// iframe — putting a whole website inside the small box where the
			// article was. One Cambridge entry links out three times.
			//
			// Handed to the parent rather than opened here: a popup from a
			// sandboxed frame inherits that sandbox unless
			// allow-popups-to-escape-sandbox is granted, so the site would open
			// without allow-forms and its own search box would not work. The
			// parent is not sandboxed, so it can open an ordinary tab — and
			// widening this sandbox to fix a link would be the wrong trade.
			e.preventDefault();
			HOST.postMessage({ t: "open", url: href }, "*");
		} else if (href && !/^([a-z][\w+.-]*:|\/|#|res\/|assets\/)/i.test(href)) {
			// A bare relative href with no scheme is a cross-reference: OALD10
			// writes <a class="Ref" href="defendant">, and Aard/slob articles
			// generally address their own headwords this way. index.html has
			// handled it since D41; this file did not, so for every
			// script-bearing dictionary — which is exactly the ones rendered
			// in here — those links navigated the iframe to a relative URL
			// under about:srcdoc and lost the article.
			//
			// The exclusions are a real scheme, a rooted path, an in-page
			// anchor and our own /res/ and /assets/ prefixes. Each of those
			// has an owner above; "#" in particular must stay excluded HERE
			// precisely because it now has its own branch — letting a fragment
			// fall through to this one would search "#dataset-british" as a
			// headword.
			e.preventDefault();
			e.stopPropagation();
			if (e.stopImmediatePropagation) e.stopImmediatePropagation();
			HOST.postMessage({ t: "ref", w: decodeRef(href), dict: dictID, frag: "" }, "*");
		}
	}, true);
	// Sub-entry inlining. The iframe is same-origin (sandbox allows it), so it
	// fetches the fragment itself; the article's own stylesheet and scripts are
	// already loaded here, so the fragment styles itself and its ►/▼ accordions
	// work with no extra plumbing.
	function toggleSub(link, word) {
		var existing = link.wudictSub;
		if (existing) { // second click closes it
			if (existing.parentNode) existing.parentNode.removeChild(existing);
			link.wudictSub = null;
			post();
			return;
		}
		var box = document.createElement("div");
		box.className = "wudict-sub";
		box.textContent = "…";
		link.parentNode.insertBefore(box, link.nextSibling);
		link.wudictSub = box;
		post();
		fetch("/api/search?mode=exact&n=1&dict=" + encodeURIComponent(dictID) +
			"&q=" + encodeURIComponent(word))
			.then(function (r) { return r.text(); })
			.then(function (text) {
				var html = null;
				text.split("\n").forEach(function (line) {
					if (!line.trim()) return;
					var m;
					try { m = JSON.parse(line); } catch (_) { return; }
					if (m.t === "hit" && m.results && m.results.length) html = m.results[0].Body;
				});
				if (html === null) { box.textContent = "(not in this dictionary)"; post(); return; }
				// the fragment repeats the article's stylesheet link; one is enough
				box.innerHTML = '<span class="wudict-sub-close" title="Close">✕</span>' +
					html.replace(/<link\b[^>]*>/gi, "");
				box.querySelector(".wudict-sub-close").addEventListener("click", function () {
					toggleSub(link, word);
				});
				post();
				setTimeout(post, 250); // late images/reflow
			})
			.catch(function () { box.textContent = "(could not load)"; post(); });
	}

	// "pick", not "ref": a double-clicked word is the user asking what
	// something means, not following the author's reference. The app tidies it
	// (trailing punctuation, footnote digits) and searches everywhere.
	document.addEventListener("dblclick", function () {
		var sel = String(document.getSelection() || "").trim();
		if (sel) HOST.postMessage({ t: "pick", w: sel }, "*");
	});
})();
