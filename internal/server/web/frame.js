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

	document.addEventListener("click", function (e) {
		var a = e.target && e.target.closest ? e.target.closest("a") : null;
		if (!a) return;
		var href = a.getAttribute("href") || "";
		if (/^(bword|entry):\/\//i.test(href) || /^[dx]:/i.test(href)) {
			e.preventDefault();
			var w = href.replace(/^(bword|entry):\/\//i, "").replace(/^[dx]:/i, "");
			parent.postMessage({ t: "lookup", w: decodeURIComponent(w) }, "*");
		} else if (/\.(mp3|ogg|wav|spx|m4a)([?#]|$)/i.test(href)) {
			e.preventDefault();
			new Audio(href).play();
		}
	});
	document.addEventListener("dblclick", function () {
		var sel = String(document.getSelection() || "").trim();
		if (sel) parent.postMessage({ t: "lookup", w: sel }, "*");
	});
})();
