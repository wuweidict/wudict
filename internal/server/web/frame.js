/**
 * Copyright (C) 2026 glowinthedark
 *
 * SPDX-License-Identifier: GPL-3.0-or-later
 */

// gonow-dict iframe bridge: runs inside sandboxed article iframes
// (script-bearing dictionaries). Reports content height, forwards
// bword:// lookups and double-click word lookups to the app, follows
// theme changes.
(function () {
	"use strict";
	var fid = document.currentScript.dataset.fid;
	var audioEl = null; // reused across clicks so playback isn't GC'd mid-load

	function post() {
		var d = document;
		var h = Math.max(
			d.documentElement.scrollHeight,
			d.body ? d.body.scrollHeight + 16 : 0,
			d.body ? d.body.offsetHeight + 16 : 0
		);
		parent.postMessage({ t: "h", fid: fid, h: h }, "*");
	}
	addEventListener("message", function (e) {
		if (e.data && e.data.t === "theme") {
			document.documentElement.classList.toggle("dark", !!e.data.dark);
		}
	});
	addEventListener("load", function () {
		if (window.ResizeObserver) {
			new ResizeObserver(post).observe(document.body);
		}
		post();
		// late reflows: webfonts, MathJax typesetting, lazy images
		setTimeout(post, 600);
		setTimeout(post, 2500);
	});

	// Same accepted forms as the main page (bword:/entry: with or without the
	// slashes, d:/x:, trailing #fragment dropped). Kept in step with
	// wordFromHref in index.html.
	function wordFromHref(href) {
		if (!/^(bword|entry):(\/\/)?/i.test(href) && !/^[dx]:/i.test(href)) return null;
		var raw = href.replace(/^(bword|entry):(\/\/)?/i, "").replace(/^[dx]:/i, "").replace(/#.*$/, "");
		try { return decodeURIComponent(raw).trim(); } catch (_) { return raw.trim(); }
	}

	document.addEventListener("click", function (e) {
		var a = e.target && e.target.closest ? e.target.closest("a") : null;
		if (!a) return;
		var href = a.getAttribute("href") || "";
		var w = wordFromHref(href);
		if (w !== null) {
			e.preventDefault();
			if (w) parent.postMessage({ t: "lookup", w: w }, "*");
		} else if (/\.(mp3|ogg|wav|spx|m4a)([?#]|$)/i.test(href)) {
			e.preventDefault();
			// reuse one referenced element so it can't be GC'd during the
			// (possibly slow, speexdec-transcoded) load; decode is by
			// Content-Type, not the .spx URL extension.
			if (!audioEl) audioEl = new Audio();
			audioEl.src = href;
			var p = audioEl.play();
			if (p && p.catch) p.catch(function (err) { console.warn("audio play failed:", href, err); });
		}
	});
	document.addEventListener("dblclick", function () {
		var sel = String(document.getSelection() || "").trim();
		if (sel) parent.postMessage({ t: "lookup", w: sel }, "*");
	});
})();
