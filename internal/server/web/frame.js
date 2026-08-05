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
		parent.postMessage({
			t: "anchor", fid: fid,
			y: el.getBoundingClientRect().top + (window.pageYOffset || 0)
		}, "*");
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
				parent.postMessage({ t: "ref", w: ref.word, dict: dictID, frag: ref.frag }, "*");
				return;
			}
			jumpToFragment(ref.frag); // "anchor": a place in this same article
		} else if (/\.(mp3|ogg|wav|spx|m4a)([?#]|$)/i.test(href)) {
			e.preventDefault();
			// reuse one referenced element so it can't be GC'd during the
			// (possibly slow, speexdec-transcoded) load; decode is by
			// Content-Type, not the .spx URL extension.
			if (!audioEl) audioEl = new Audio();
			audioEl.src = href;
			var p = audioEl.play();
			if (p && p.catch) p.catch(function (err) { console.warn("audio play failed:", href, err); });
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
			parent.postMessage({ t: "open", url: href }, "*");
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
			parent.postMessage({ t: "ref", w: decodeRef(href), dict: dictID, frag: "" }, "*");
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
		if (sel) parent.postMessage({ t: "pick", w: sel }, "*");
	});
})();
