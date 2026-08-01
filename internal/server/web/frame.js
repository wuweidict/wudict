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

	// CAPTURE phase, deliberately. Dictionary scripts attach their own click
	// handlers inside the article — LDOCE6's entry.js calls stopPropagation()
	// on the speaker <img> so a play click does not also toggle the accordion
	// around it. A bubble-phase listener on `document` never sees those clicks,
	// so the browser followed the link and replaced the article with a bare
	// media player. Capture runs on the way down, before any of that.
	document.addEventListener("click", function (e) {
		var a = e.target && e.target.closest ? e.target.closest("a") : null;
		if (!a) return;
		var href = a.getAttribute("href") || "";
		var w = wordFromHref(href);
		if (w !== null) {
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
			if (w.charAt(0) === "@" && w.length > 1) { toggleSub(a, w); return; }
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

	document.addEventListener("dblclick", function () {
		var sel = String(document.getSelection() || "").trim();
		if (sel) parent.postMessage({ t: "lookup", w: sel }, "*");
	});
})();
