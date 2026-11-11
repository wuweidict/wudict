/**
 * Copyright (C) 2026 glowinthedark
 *
 * SPDX-License-Identifier: GPL-3.0-or-later
 */

// wudict text-at-point, for both article surfaces - index.html's shadow roots
// and frame.js's sandboxed iframes each load this file into their own window.
//
// wuWordAt: the word under a double tap. It is only the fallback. A double
// click that selected something is looked up by its selection, phrase and
// all; this answers when there is none, which is what Android WebView leaves
// behind a double TAP - it selects on long-press only. Callers hand the result
// to the same tidying a selection gets.
//
// wuTapUnits (D192): a single tap on article text marks the unit it most
// likely means - the headword, a styled example, else the sentence - as a
// real selection, so read aloud (D148) offers it as it offers any other; a
// second tap on the mark reads it. The mark is silent: a wrong guess costs a
// glance, while a wrong sound costs far more, so only the second tap speaks.
//
// The browser's own word selection is not reachable without a selection, so
// this does what it does: the caret under the point, then Unicode word and
// sentence boundaries (Intl.Segmenter) - which is what makes a tap in Chinese
// or Thai text find a word and not a whole run of script.
(function () {
	"use strict";
	// a double tap on one of these is the element's, not a lookup
	var OWNED = "a,button,input,textarea,select,audio,video,[contenteditable]";
	// a single tap on one of these is the element's too: it is a control, or
	// says it is one (a computed cursor:pointer is checked beside this)
	var CONTROL = OWNED + ",summary,label,[onclick],[role=button],[role=link],[tabindex]";
	var HEAD = "details.dict>dl>dt";
	// a run inside one of these is a link or a form, never a label
	var LINKISH = "a,button,input,select,textarea,summary,label";
	var SKIP = /^(script|style|noscript|template|input|textarea|select)$/;
	var BLOCK = /^(block|flow-root|list-item|table|table-row|table-cell|table-caption|flex|grid)$/;
	// The double-tap window: a single tap acts only once it has passed.
	// Android's own (ViewConfiguration.getDoubleTapTimeout) on touch; with a
	// mouse, the double-clicks people make, short of the OS's 500 ms ceiling.
	var WAIT_TOUCH = 300, WAIT_MOUSE = 350;
	// how far the walk reads on each side of the tap: word and sentence
	// boundaries are decided locally, whatever the article's size
	var REACH = 1024;
	// a styled run longer than this is a section, not an example
	var RUN = 300;
	// a sentence longer than this is also cut at its semicolons
	var CLAUSE = 160;
	// Sense numbers and bullets that open a unit, and are not read: "1. ",
	// "b) ", "(2) ", "IV. ", "• ", "◊ ". A marker must be followed by a space,
	// so "1.5 kg" stays whole. "~" is not one: it stands for the headword.
	var MARK = /^\s*(?:(?:\(?(?:\d{1,3}|[ivxlcIVXLC]{1,6}|[a-zA-Z])[.)](?=\s)|[•◊▪►▸■□●·*]|[-–—](?=\s))\s*)*/;
	// A dictionary's own separators - an example from its translation, one
	// sense from the next - cut a sentence always; a semicolon only a long one,
	// since in a definition it joins near-synonyms worth hearing together.
	var HARD = /[|◊•]|\s[—–]\s/g, SOFT = /;/g;
	// a letter or a digit: what makes text worth marking and saying
	var WORDCH = /[\p{L}\p{N}]/u;
	// what may open a sentence before its first letter
	var OPEN = /[(\[{"'«‹“‘„¿¡]/;
	// text nodes a search for the next one may pass
	var LIMIT = 20000;

	function norm(s) { return String(s || "").replace(/\s+/g, " ").trim(); }

	function target(e) {
		var t = e.composedPath ? e.composedPath()[0] : e.target;
		if (t && t.nodeType !== 1) t = t.parentElement;
		return t || null;
	}

	function caretAt(doc, root, x, y) {
		var p;
		if (doc.caretPositionFromPoint) {
			// a shadow root must be named, or the caret stops at its host
			p = root.nodeType === 11
				? doc.caretPositionFromPoint(x, y, { shadowRoots: [root] })
				: doc.caretPositionFromPoint(x, y);
			if (p) return { node: p.offsetNode, offset: p.offset };
		} else if (doc.caretRangeFromPoint) {
			p = doc.caretRangeFromPoint(x, y);
			if (p) return { node: p.startContainer, offset: p.startOffset };
		}
		return null;
	}

	// One per language and granularity: making one loads break rules, and
	// reading on asks for the same two at every unit. A handful per page.
	var segmenters = {};
	function segmenter(lang, granularity) {
		var key = granularity + "|" + (lang || ""), seg = segmenters[key];
		if (seg) return seg;
		// a dictionary's lang="en_US" is not a BCP 47 tag, and the constructor throws
		try { seg = new Intl.Segmenter(lang || undefined, { granularity: granularity }); }
		catch (_) { seg = new Intl.Segmenter(undefined, { granularity: granularity }); }
		return (segmenters[key] = seg);
	}

	function textAt(e, root) {
		var c = caretAt(root.ownerDocument || document, root, e.clientX, e.clientY);
		return c ? textAtNode(c.node, c.offset, root) : null;
	}

	// The text of the block around a point (a text node and an offset in it),
	// with where the point is in it and which text node each stretch came
	// from, so a span of it can become a Range. Positions in `segs` and
	// `spans` are absolute; `text` starts at `base`.
	function textAtNode(node, offset, root) {
		if (!node || node.nodeType !== 3 || !root.contains(node)) return null;
		var doc = root.ownerDocument || document, view = doc.defaultView || window;

		var display = function (el) { return view.getComputedStyle(el).display; };
		var drawn = function (a, b) { return a.color !== b.color || a.fontStyle !== b.fontStyle || a.fontFamily !== b.fontFamily; };
		// The text is the tapped node's whole block, never the node alone:
		// <b>un</b>likely is one word split across two nodes.
		var block = node.parentElement;
		while (block && block !== root && !BLOCK.test(display(block))) block = block.parentElement;
		if (!block) block = root; // top level of a shadow root: no element parent
		// the inline elements between the tapped text and its block, innermost first
		var chain = [];
		for (var el = node.parentElement; el && el !== block; el = el.parentElement) chain.push(el);
		var spans = chain.map(function () { return null; });

		// Blocks and <br> inside it become line breaks, or a table's cells would
		// run together into one word, and a sentence would run on into the next
		// item. Bounded around the tap: before the tapped node only a tail is
		// kept, and the walk ends REACH past it.
		var text = "", base = 0, at = -1, done = false, segs = [], runs = [];
		// Inherited properties cross into a shadow root from its host, so the
		// host is the reference for text at a shadow root's top level.
		var bel = block.nodeType === 1 ? block : block.host, bs = bel ? view.getComputedStyle(bel) : null;
		var add = function (s) {
			text += s;
			if (at < 0) {
				if (text.length > 4 * REACH) {
					var cut = text.length - REACH;
					text = text.slice(cut);
					base += cut;
					while (segs.length && segs[0].s + segs[0].l <= base) segs.shift();
				}
			} else if (text.length > at + REACH) done = true;
		};
		var walk = function (n) {
			if (n.nodeType === 3) {
				if (n === node) at = text.length + offset;
				segs.push({ n: n, s: base + text.length, l: n.nodeValue.length });
				add(n.nodeValue);
				return;
			}
			var edge = false, k = -1, s0 = 0, run = false;
			if (n.nodeType === 1) {
				if (SKIP.test(n.localName)) return;
				if (n.localName === "br") { add("\n"); return; }
				if (n !== block) {
					var cs = view.getComputedStyle(n), d = cs.display;
					if (d === "none") return;
					edge = BLOCK.test(d);
					// an inline run drawn apart from its block: a label, maybe
					run = !edge && !!bs && drawn(cs, bs) && !n.closest(LINKISH);
				}
				k = chain.indexOf(n);
			} else if (n.nodeType !== 11) return;
			if (edge) add("\n");
			s0 = base + text.length;
			for (var ch = n.firstChild; ch && !done; ch = ch.nextSibling) walk(ch);
			if (k >= 0 && !done) spans[k] = { s: s0, e: base + text.length };
			if (run && !done) runs.push({ s: s0, e: base + text.length });
			if (edge) add("\n");
		};
		walk(block);
		if (at < 0 || !text) return null;

		// The language the segmenter is told: the nearest declaration anywhere.
		// The language a unit is SPOKEN in may only come from inside the
		// article - the document's own lang is the app's, or a dictionary-wide
		// guess - so that one stops at the root, <body> and <html>.
		var tagged = node.parentElement && node.parentElement.closest("[lang]");
		var seglang = (tagged ? tagged.lang : doc.documentElement.lang) || "", lang = "";
		for (el = node.parentElement; el && el !== root && el !== doc.body && el !== doc.documentElement; el = el.parentElement)
			if (el.hasAttribute("lang")) { lang = el.getAttribute("lang") || ""; break; }

		return {
			text: text, at: at, base: base, segs: segs, chain: chain, spans: spans, runs: runs,
			block: block, bs: bs, lang: lang, seglang: seglang, view: view, doc: doc
		};
	}

	// The word segment under the event, with the text it was found in.
	function wordSeg(e, root) {
		if (typeof Intl === "undefined" || !Intl.Segmenter) return null;
		var t = target(e);
		if (t && t.closest && t.closest(OWNED)) return null;
		var x = textAt(e, root);
		if (!x) return null;
		var parts = segmenter(x.seglang, "word").segment(x.text), i = Math.min(x.at, x.text.length - 1);
		var s = parts.containing(i);
		// A tap on a word's last letter puts the caret AFTER it, on the space.
		if ((!s || !s.isWordLike) && i > 0) s = parts.containing(i - 1);
		return s && s.isWordLike ? { x: x, s: s } : null;
	}

	function wordAt(e, root) {
		var w = wordSeg(e, root);
		return w ? w.s.segment : "";
	}

	function countWords(seg, s) {
		var n = 0;
		for (var p of seg.segment(s)) if (p.isWordLike && ++n > 1) break;
		return n;
	}

	// [a, b) of x.text without its opening markers and its surrounding space
	function tidy(text, a, b) {
		a += MARK.exec(text.slice(a, b))[0].length;
		while (b > a && /\s/.test(text.charAt(b - 1))) b--;
		return [a, b];
	}

	// tidy, except that an opening "f." or "n." that is an <abbr> with an
	// expansion is a word - "nom femení" - not a sense letter
	function tidyAt(x, a, b) {
		var r = tidy(x.text, a, b), s0 = a + /^\s*/.exec(x.text.slice(a, b))[0].length;
		if (s0 < r[0]) {
			var p = point(x, x.base + s0, false), el = p && p[0].parentElement, ab = el && el.closest("abbr");
			if (ab && expansion(ab)) {
				r[0] = s0;
				// the marker took the space after it; the end is the abbreviation's
				while (r[1] > r[0] && /\s/.test(x.text.charAt(r[1] - 1))) r[1]--;
			}
		}
		return r;
	}

	// The text node and offset of an absolute position: the character that
	// starts there, or, for an end, the one that ends there.
	function point(x, abs, end) {
		for (var i = 0; i < x.segs.length; i++) {
			var g = x.segs[i];
			if (end ? abs > g.s && abs <= g.s + g.l : abs >= g.s && abs < g.s + g.l) return [g.n, abs - g.s];
		}
		return null;
	}

	// ------------------------------------------------------- what is said
	// Pronunciation is never read aloud: a voice cannot say IPA, it spells it.
	// By class token, wudict's own (.wu-ipa, .wu-trs) and the common ones.
	var PRON = /(?:^|\s)(?:wu-)?(?:ipa|pron|pronunciation|phon|phonetics?|transcr|transcription|trs)(?:\s|$)/i;
	function isPron(el) {
		for (; el; el = el.parentElement) {
			var c = el.getAttribute("class");
			if (c && PRON.test(c)) return true;
		}
		return false;
	}
	// What an <abbr> stands for: its title, else the data attribute a CSS-only
	// tooltip shows (data-t="nom femení"); "" when it says nothing more.
	var EXPAND = ["title", "data-title", "data-t", "data-tooltip", "data-tip", "data-full", "data-expansion", "data-text", "data-original-title"];
	function expansion(ab) {
		var own = norm(ab.textContent), i, v;
		for (i = 0; i < EXPAND.length; i++) {
			v = norm(ab.getAttribute(EXPAND[i]));
			if (v && v !== own && /\p{L}/u.test(v)) return v;
		}
		for (i = 0; i < ab.attributes.length; i++) {
			var at = ab.attributes[i];
			if (at.name.indexOf("data-") !== 0) continue;
			v = norm(at.value);
			if (v.length > own.length && /\p{L}{2}/u.test(v) && /\s|^[\p{L}\p{M}.\-]+$/u.test(v)) return v;
		}
		return "";
	}
	// The text a range is read as: pronunciation left out, each abbreviation
	// said in full, blocks kept apart. The mark shows what is on the page; this
	// is what the voice gets.
	function spoken(r) {
		var top = r.commonAncestorContainer;
		if (top.nodeType === 3) top = top.parentNode;
		var doc = top.ownerDocument || document, view = doc.defaultView || window;
		var w = doc.createTreeWalker(top, NodeFilter.SHOW_TEXT), out = "", lastAb = null, lastBlock = null, lastP = null;
		var blockOf = function (el) {
			for (; el && el !== top; el = el.parentElement)
				if (!/^(inline|contents)/.test(view.getComputedStyle(el).display)) return el;
			return top;
		};
		var skipP = false;
		for (var n = w.nextNode(); n; n = w.nextNode()) {
			if (!r.intersectsNode(n)) { if (r.comparePoint(n, 0) > 0) break; continue; }
			var p = n.parentElement;
			if (p === lastP) { if (skipP) continue; }
			else {
				lastP = p;
				skipP = !p || !!p.closest("script,style,noscript,template") || isPron(p);
				if (skipP) continue;
				var b = blockOf(p);
				if (lastBlock && b !== lastBlock) out += " ";
				lastBlock = b;
			}
			var ab = p.closest("abbr"), ex = ab && expansion(ab);
			if (ex) {
				if (ab !== lastAb) { out += " " + ex + " "; lastAb = ab; }
				continue;
			}
			out += n.data.slice(n === r.startContainer ? r.startOffset : 0, n === r.endContainer ? r.endOffset : n.length);
		}
		return norm(out);
	}

	// A label is its own unit (D192 Am. 6): a run drawn apart from its block -
	// "Arquitectura; Llenguatge administratiu" in grey before a definition, a
	// register note after it - that opens or closes the sentence is cut off
	// it; with the tap in the run, the run is the unit, one word or many. Only
	// at the ends: a coloured word inside a sentence stays in it.
	function unlabel(x, lo, hi) {
		for (var guard = 0; guard < 8; guard++) {
			var a = lo + MARK.exec(x.text.slice(lo, hi))[0].length, z = hi, cut = false;
			while (z > a && /\s/.test(x.text.charAt(z - 1))) z--;
			for (var i = 0; i < x.runs.length && !cut; i++) {
				var rs = x.runs[i].s - x.base, re = x.runs[i].e - x.base;
				if (rs < 0) continue;
				var mine = x.at >= rs && x.at < re;
				if (rs <= a && re > a && re < z && WORDCH.test(x.text.slice(re, z))) {
					if (mine) return [a, re];
					lo = re; cut = true;
				} else if (rs > a && rs < z && re >= z && WORDCH.test(x.text.slice(a, rs))) {
					if (mine) return [rs, z];
					hi = rs; cut = true;
				}
			}
			if (!cut) break;
		}
		return [lo, hi];
	}

	// [lo, hi) narrowed to the stretch between the separators around `at`
	function clause(text, lo, hi, at, re) {
		var sub = text.slice(lo, hi), rel = at - lo, a = lo, z = hi, m;
		re.lastIndex = 0;
		while ((m = re.exec(sub))) {
			if (m.index + m[0].length <= rel) a = lo + m.index + m[0].length;
			else if (m.index >= rel) { z = lo + m.index; break; }
		}
		return [a, z];
	}

	function headUnit(dt) {
		if (!dt.textContent.trim()) return null;
		var r = dt.ownerDocument.createRange();
		r.selectNodeContents(dt);
		return { range: r, kind: "head", lang: "" };
	}

	// The unit a single tap means: {range, kind, lang}, or null.
	//   head  the headword, whole;
	//   x     an example: the innermost inline run, 2+ words and at most RUN
	//         characters, that wudict marks as one (.wu-ex) or that is set in
	//         italics against its block - the lexicographer's convention, and
	//         read in the headword language first (speak.js relang);
	//   s     a styled run in colour or another face only, or else the
	//         sentence around the tap, cut at the separators around it;
	//   w     either of those, when it is one word.
	// lang is a lang= declared inside the article, "" when there is none.
	function unitAt(e, root) {
		if (typeof Intl === "undefined" || !Intl.Segmenter) return null;
		var t = target(e), head = t && t.closest && t.closest(HEAD);
		if (head) return headUnit(head);
		var x = textAt(e, root);
		return x ? unitIn(x) : null;
	}

	// The unit at x.at, as unitAt describes it.
	function unitIn(x) {
		var words = segmenter(x.seglang, "word"), lo = -1, hi = -1, kind = "s", r;
		var bs = x.bs;
		for (var k = 0; bs && k < x.chain.length; k++) {
			var sp = x.spans[k];
			if (!sp || sp.s < x.base) break; // cut by the walk: longer than any unit
			var el = x.chain[k], cs = x.view.getComputedStyle(el);
			var ex = el.classList.contains("wu-ex"), italic = cs.fontStyle !== bs.fontStyle;
			if (!ex && !italic && cs.color === bs.color && cs.fontFamily === bs.fontFamily) continue;
			r = tidyAt(x, sp.s - x.base, sp.e - x.base);
			if (r[1] - r[0] > RUN) break; // the ones outside it are longer still
			if (countWords(words, x.text.slice(r[0], r[1])) < 2) continue;
			lo = r[0]; hi = r[1]; kind = ex || italic ? "x" : "s";
			break;
		}

		if (lo < 0) {
			var i = Math.min(x.at, x.text.length - 1);
			var s = segmenter(x.seglang, "sentence").segment(x.text).containing(i);
			if (!s) return null;
			r = clause(x.text, s.index, s.index + s.segment.length, x.at, HARD);
			if (r[1] - r[0] > CLAUSE) r = clause(x.text, r[0], r[1], x.at, SOFT);
			r = unlabel(x, r[0], r[1]);
			r = tidyAt(x, r[0], r[1]);
			lo = r[0]; hi = r[1];
		}

		var n = countWords(words, x.text.slice(lo, hi));
		if (!n) return null;
		if (n === 1) kind = "w";
		var p = point(x, x.base + lo, false), q = point(x, x.base + hi, true);
		if (!p || !q) return null;
		r = x.doc.createRange();
		r.setStart(p[0], p[1]);
		r.setEnd(q[0], q[1]);
		return { range: r, kind: kind, lang: x.lang };
	}

	// Whether the point is on the text the caret snapped to, not beside it. A
	// caret is found for any point - past a line's end, in a margin, between
	// paragraphs - and a tap there is a tap on nothing. The vertical slack is
	// the line gap a finger lands in when it aims at a line.
	function overText(c, x, y, slack) {
		var n = c && c.node;
		if (!n || n.nodeType !== 3) return false;
		var r = n.ownerDocument.createRange();
		for (var k = Math.max(0, c.offset - 1); k <= Math.min(n.length - 1, c.offset); k++) {
			if (!/\S/.test(n.data.charAt(k))) continue;
			r.setStart(n, k);
			r.setEnd(n, k + 1);
			var rs = r.getClientRects();
			for (var i = 0; i < rs.length; i++) {
				var b = rs[i], v = b.height * 0.4;
				if (x >= b.left - slack && x <= b.right + slack && y >= b.top - v && y <= b.bottom + v) return true;
			}
		}
		return false;
	}

	// ------------------------------------------------------------ the next unit
	// On screen in principle: rendered, and not inside a closed <details>
	// (bar its summary). Chrome lays out a closed <details>' content and
	// reports boxes for it, but it is hidden, and no selection can go there.
	function visible(el) {
		if (el.checkVisibility ? !el.checkVisibility({ visibilityProperty: true }) : !el.getClientRects().length) return false;
		for (var d = el.closest("details"); d; d = d.parentElement && d.parentElement.closest("details")) {
			if (d.open) continue;
			var sum = d.querySelector(":scope > summary");
			if (!sum || !sum.contains(el)) return false;
		}
		return true;
	}
	function shown(n) {
		var p = n.parentElement;
		return !!p && !p.closest("script,style,noscript,template") && visible(p);
	}
	// All link or button. An <abbr> is focusable for its tooltip, not a
	// control: read aloud says what it stands for.
	function inControl(r) {
		var a = r.commonAncestorContainer, el = a.nodeType === 1 ? a : a.parentElement;
		var c = el && el.closest(CONTROL);
		return !!c && c.localName !== "abbr";
	}
	// the first shown text node after `from`, with something to read
	function textAfter(root, from) {
		var w = (root.ownerDocument || document).createTreeWalker(root, NodeFilter.SHOW_TEXT);
		w.currentNode = from;
		for (var n = w.nextNode(), i = 0; n && i < LIMIT; n = w.nextNode(), i++)
			if (/\S/.test(n.nodeValue) && shown(n)) return n;
		return null;
	}

	// The next unit in reading order: after a headword, its article; after
	// anything else, whatever comes next in the article - definition,
	// example, label - each read as itself, in its own language (D192 Am. 3).
	// null at the end of the article.
	function nextOf(u) {
		if (u.kind === "head") return articleStart(u);
		var node = u.range.endContainer;
		return node.nodeType === 3 ? nextFrom(u.root, node, u.range.endOffset, true) : null;
	}

	// A headword's article, when it is a shadow-DOM one: its first unit. An
	// iframe article is another window, whose own pick.js marks its units.
	function articleStart(u) {
		var n = u.range.startContainer, el = n.nodeType === 1 ? n : n.parentElement;
		var dt = el && el.closest(HEAD), dd = dt && dt.nextElementSibling;
		var host = dd && dd.querySelector(":scope > .article"), root = host && host.shadowRoot;
		if (!root) return null;
		var t = textAfter(root, root);
		var v = t ? nextFrom(root, t, 0, false) : null;
		// The article's own header - the headword again, with its
		// pronunciation, level badges and speakers - was just read as the
		// headword: its block is passed over (D192 Am. 6).
		var head = norm(dt.textContent).toLowerCase();
		for (var k = 0; v && k < 3; k++) {
			var x = textAtNode(v.range.startContainer, v.range.startOffset, root);
			if (!x || x.block === root || !isHeader(x.block, head)) break;
			var last = null, w = root.ownerDocument.createTreeWalker(x.block, NodeFilter.SHOW_TEXT);
			for (var n2 = w.nextNode(); n2; n2 = w.nextNode()) last = n2;
			v = last ? nextFrom(root, last, last.length, false) : null;
		}
		if (v) v.root = root;
		return v;
	}

	// A block that says nothing but the headword, once pronunciation, links,
	// badges and speakers are set aside.
	function isHeader(block, head) {
		if (!head) return false;
		var out = "", w = block.ownerDocument.createTreeWalker(block, NodeFilter.SHOW_TEXT);
		for (var n = w.nextNode(); n; n = w.nextNode()) {
			var p = n.parentElement, c = p && p.closest(CONTROL);
			if (!p || isPron(p) || (c && block.contains(c))) continue;
			out += n.data + " ";
		}
		out = norm(out).toLowerCase();
		if (out === head) return true;
		if (out.indexOf(head) !== 0) return false;
		var rest = out.slice(head.length), words = 0;
		for (var g of segmenter("", "word").segment(rest)) if (g.isWordLike && ++words > 1) return false;
		return true;
	}

	// The unit at the next letter or digit after (node, offset); text that is
	// all link is passed over. `after` says (node, offset) ends the unit read
	// before: a sentence that began inside that one is read from where it
	// ended, since its start was read already; one that began after it keeps
	// its opening "(" or quote.
	function nextFrom(root, node, offset, after) {
		var from = after ? [node, offset] : null;
		for (var guard = 0; node && guard < 400; guard++) {
			var x = textAtNode(node, offset, root);
			if (!x) return null;
			var j = -1;
			for (var i = x.at; i < x.text.length; i++) if (WORDCH.test(x.text.charAt(i))) { j = i; break; }
			if (j < 0) { // nothing more in this block: the next text after it
				var last = x.segs[x.segs.length - 1];
				node = textAfter(root, last ? last.n : node);
				offset = 0;
				from = null;
				continue;
			}
			var p = point(x, x.base + j, false);
			if (!p) return null;
			// the walk just made serves when the letter is in the node it was made for
			var y = x;
			if (p[0] === node) x.at = j; else y = textAtNode(p[0], p[1], root);
			var v = y && unitIn(y);
			if (!v) { node = p[0]; offset = p[1] + 1; continue; }
			if (from && v.range.comparePoint(from[0], from[1]) === 0) {
				var a = p[1];
				while (a > 0 && OPEN.test(p[0].data.charAt(a - 1))) a--;
				v.range.setStart(p[0], a);
			}
			if (!inControl(v.range)) v.say = spoken(v.range);
			if (!v.say || !WORDCH.test(v.say)) { // a link, or only pronunciation
				node = v.range.endContainer; offset = v.range.endOffset;
				if (node.nodeType !== 3) return null;
				from = [node, offset];
				continue;
			}
			return v;
		}
		return null;
	}

	// ------------------------------------------------------------ the tap
	// Chrome selects inside a shadow root only through the root's own
	// (non-standard) selection; the standard one takes shadow nodes where the
	// browser follows the newer Selection spec.
	function sels(root) {
		var out = [];
		if (root && root.nodeType === 11 && root.getSelection) out.push(root.getSelection());
		out.push(document.getSelection());
		return out;
	}
	function selText(root) {
		var l = sels(root);
		for (var i = 0; i < l.length; i++) {
			var s = l[i] && norm(String(l[i]));
			if (s) return s;
		}
		return "";
	}
	// Selects the range; the selection's own text, or "" where the browser
	// would not select it - then nothing is marked.
	function select(r, root) {
		var l = sels(root);
		for (var i = 0; i < l.length; i++) {
			var s = l[i];
			if (!s) continue;
			try {
				s.setBaseAndExtent(r.startContainer, r.startOffset, r.endContainer, r.endOffset);
				var t = !s.isCollapsed && norm(String(s));
				if (t) return t;
			} catch (_) { /* this API would not take it; try the next */ }
		}
		return "";
	}

	// The unit marked last in this window: {range, root, sel, kind, lang, say,
	// next}, sel being the selection's text as the browser renders it,
	// normalised, say what it is read as, and next whether a next unit exists
	// (the ⏭ shows for it).
	var unit = null;

	function hasNext(u) {
		try { return !!nextOf(u); } catch (_) { return false; }
	}
	function make(v, root, sel) {
		var u = { range: v.range, root: root, sel: sel, kind: v.kind, lang: v.lang || "", next: false, say: v.say || "" };
		if (!u.say) try { u.say = spoken(v.range); } catch (_) { }
		u.next = hasNext(u);
		return u;
	}
	// The mark goes, if it is still the selection; the reader's own never does.
	function clearMark() {
		if (!unit) return;
		try {
			if (selText(unit.root) === unit.sel) sels(unit.root).forEach(function (s) { s.removeAllRanges(); });
		} catch (_) { }
		unit = null;
	}

	// opt: { root(e) -> the article root under the event, or null;
	//        enabled() -> read aloud is on;
	//        mark(unit, touch) -> hand the marked unit to read aloud;
	//        toggle() -> read it, or stop reading;
	//        stop() -> stop reading: Escape, or a tap on nothing }
	function tapUnits(opt) {
		var down = null, timer = 0, lastScroll = 0;
		var cancel = function () { clearTimeout(timer); timer = 0; };

		addEventListener("scroll", function () { lastScroll = Date.now(); }, { capture: true, passive: true });

		addEventListener("pointerdown", function (e) {
			cancel();
			down = null;
			if (!e.isPrimary || !opt.enabled()) return;
			var root = opt.root(e);
			if (!root) return;
			var d = { x: e.clientX, y: e.clientY, t: Date.now(), root: root,
				touch: e.pointerType !== "mouse", free: true, over: false, inUnit: false };
			var cur = "";
			try { cur = selText(root); } catch (_) { }
			var mine = !!unit && cur === unit.sel;
			// A tap that takes away a selection of the reader's own does only that.
			d.free = !cur || mine;
			// Decided now: pressing will collapse the selection the mark is.
			try {
				var t = target(e), c = caretAt(document, root, e.clientX, e.clientY);
				d.over = !!(t && t.closest && t.closest(HEAD)) || overText(c, e.clientX, e.clientY, d.touch ? 12 : 6);
				if (mine && d.over && unit.root === root) d.inUnit = !!c && unit.range.isPointInRange(c.node, c.offset);
			} catch (_) { /* another tree: not in it */ }
			down = d;
		}, true);

		// A press on the mark keeps it: the selection is what read aloud shows
		// and reads. Only a fresh press - the second of a double click is the
		// browser's, to select the word as it always has.
		addEventListener("mousedown", function (e) {
			if (down && down.inUnit && e.button === 0 && e.detail === 1) e.preventDefault();
		}, true);

		// the second tap of a double tap is a lookup, never a confirmation
		addEventListener("dblclick", cancel, true);

		addEventListener("keydown", function (e) {
			if (e.key !== "Escape") return;
			cancel(); // a mark still waiting out its window never appears
			if (!unit) return;
			clearMark();
			opt.stop();
		}, true);

		// Bubble phase, so the article's own handlers have had their say.
		addEventListener("click", function (e) {
			var d = down;
			down = null;
			if (!d || !opt.enabled()) return;
			if (e.defaultPrevented || e.button !== 0 || e.detail > 1 ||
				e.ctrlKey || e.metaKey || e.altKey || e.shiftKey) return;
			if (Math.abs(e.clientX - d.x) > 8 || Math.abs(e.clientY - d.y) > 8) return;
			if (d.t - lastScroll < 150) return; // the tap that stopped a fling
			if (!d.free) { unit = null; return; }
			var t = target(e);
			if (!t || t.closest(CONTROL)) return;
			// A tap on nothing - a margin, past a line's end, between paragraphs -
			// puts the mark away and ends its reading: the way out on a phone,
			// which has no Escape.
			if (!d.over) {
				if (unit) { clearMark(); opt.stop(); }
				return;
			}
			try {
				if (t.ownerDocument.defaultView.getComputedStyle(t).cursor === "pointer") return;
			} catch (_) { }
			// Both wait out the double-tap window, so that a second tap is a
			// lookup and nothing else: no reading starts, and no sentence is
			// marked for a gesture that is about a word.
			var wait = d.touch ? WAIT_TOUCH : WAIT_MOUSE;
			if (d.inUnit) {
				var u = unit;
				timer = setTimeout(function () {
					timer = 0;
					if (unit !== u) return;
					var now = "";
					try { now = selText(u.root); } catch (_) { }
					if (now !== u.sel) select(u.range, u.root); // a tap collapses it on Android
					opt.toggle();
				}, wait);
				return;
			}
			// The unit is found now, while the event is live; it is marked when
			// no second tap has come.
			var x = null;
			try {
				x = unitAt(e, d.root);
				// pronunciation alone, or a symbol: nothing a voice can say
				if (x && !WORDCH.test(x.say = spoken(x.range))) x = null;
			} catch (_) { x = null; }
			if (!x) { unit = null; return; }
			timer = setTimeout(function () {
				timer = 0;
				var sel = x.range.startContainer.isConnected ? select(x.range, d.root) : "";
				if (!sel) { unit = null; return; }
				unit = make(x, d.root, sel);
				opt.mark(unit, d.touch);
			}, wait);
		});
	}

	// Never throws: a failed guess is "no word", and the tap does nothing.
	window.wuWordAt = function (e, root) {
		try { return wordAt(e, root); } catch (_) { return ""; }
	};
	// The same word, also selected: what a desktop browser shows after a
	// double-click, given to the double tap on Android, which selects nothing.
	// The word is returned even where the browser would not select it.
	window.wuSelectWordAt = function (e, root) {
		try {
			var w = wordSeg(e, root);
			if (!w) return "";
			var a = point(w.x, w.x.base + w.s.index, false);
			var b = point(w.x, w.x.base + w.s.index + w.s.segment.length, true);
			if (a && b) {
				var r = w.x.doc.createRange();
				r.setStart(a[0], a[1]);
				r.setEnd(b[0], b[1]);
				select(r, root);
			}
			unit = null;
			return w.s.segment;
		} catch (_) { return ""; }
	};
	window.wuTapUnits = tapUnits;
	// the unit marked last, for the double tap: its lookup is the word, never the mark
	window.wuTapUnit = function () { return unit; };
	// ⏭ and Keep reading: mark the next unit in reading order and return it,
	// or null - the mark then stays where it is. A headword's next unit lies
	// in its article's root, not the page's.
	// A unit the browser will not select is looked past; when none follows,
	// the mark is put back where it was - a failed select collapses it.
	window.wuTapNext = function () {
		if (!unit) return null;
		var cur = unit, tried = false;
		for (var tries = 0; tries < 8; tries++) {
			var v = null;
			try { v = nextOf(cur); } catch (_) { }
			if (!v) break;
			tried = true;
			var root = v.root || cur.root, sel = select(v.range, root);
			if (sel) { unit = make(v, root, sel); return unit; }
			cur = { range: v.range, root: root, kind: v.kind };
		}
		if (tried) select(unit.range, unit.root);
		return null;
	};
	// ✕ on the speaker: the mark goes
	window.wuTapClear = clearMark;
	// what a selection is read as: abbreviations in full, no pronunciation
	window.wuSpoken = function (r) {
		try { return r ? spoken(r) : ""; } catch (_) { return ""; }
	};
})();
