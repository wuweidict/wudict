// Playwright browser_run_code template: renders one article per dictionary in the running app, dark then
// light, and measures article text contrast BEFORE the app's dark-mode invert filter (the filter keeps a
// low ratio low). In a copy under .playwright-mcp/ (the MCP reads files only there or in the repo), replace
// the two code lines `const rows = ROWS;` (rows: [[dictId, headword, name], …], ids from GET /api/dicts,
// NDJSON) and `const origin = ORIGIN;` (the server) - replace those lines, not the words, which this
// comment also contains. Writes are answered 204 unsent, so nothing reaches the server's history.
// Output per dictionary: kind (shadow|iframe), share of text chars under 3:1, median ratio, the three
// worst strings.
async (page) => {
  const rows = ROWS;
  const origin = ORIGIN;
  // Read-only: a lookup would otherwise land in the server's history.
  const ro = r => r.request().method() === 'GET' ? r.continue() : r.fulfill({status: 204, body: ''});
  await page.route('**/*', ro);
  const measure = () => {
    const det = document.querySelector('#out details[open]'); if (!det) return {err:'no open section'};
    const lum = c => { const m = c.match(/[\d.]+/g).map(Number); const f = v => { v/=255; return v<=.03928? v/12.92 : Math.pow((v+.055)/1.055,2.4); }; return [.2126*f(m[0])+.7152*f(m[1])+.0722*f(m[2]), m.length>3?m[3]:1]; };
    const roots = [];
    for (const h of det.querySelectorAll('.article')) { if (h.shadowRoot) roots.push([h.shadowRoot, window, h]); else if (h.tagName==='IFRAME' && h.contentDocument) roots.push([h.contentDocument, h.contentWindow, h]); }
    if (!roots.length) return {err:'no article root'};
    let chars=0, bad=0; const ratios=[]; const worst=[];
    for (const [root, win, host] of roots) {
      const bgOf = e => { while (e && e.nodeType===1) { const [l,a] = lum(win.getComputedStyle(e).backgroundColor); if (a>0.5) return l; e = e.parentElement || (e.getRootNode && e.getRootNode().host) || null; if (e===host) { const [hl,ha]=lum(getComputedStyle(host).backgroundColor); return ha>0.5?hl:1; } } return 1; };
      const doc = root.nodeType===9 ? root : root.ownerDocument; const w = doc.createTreeWalker(root.nodeType===9 ? (root.body||root) : root, NodeFilter.SHOW_TEXT);
      while (w.nextNode()) { const t=w.currentNode; const s=t.textContent.trim(); if (!s) continue; const el=t.parentElement; if (!el) continue; const cs=win.getComputedStyle(el); if (cs.display==='none'||cs.visibility==='hidden') continue; const r=el.getBoundingClientRect(); if (!r.width) continue;
        const [fl]=lum(cs.color), bl=bgOf(el); const cr=(Math.max(fl,bl)+.05)/(Math.min(fl,bl)+.05);
        chars+=s.length; ratios.push(cr); if (cr<3) { bad+=s.length; if (worst.length<3) worst.push(s.slice(0,24)+' '+cr.toFixed(2)); } }
    }
    ratios.sort((a,b)=>a-b);
    return {kind: roots[0][0]===roots[0][2].shadowRoot?'shadow':'iframe', chars, badPct: chars? Math.round(100*bad/chars):0, median: ratios.length? +ratios[ratios.length>>1].toFixed(2):null, worst};
  };
  const out = [];
  for (const [id, k, name] of rows) {
    const rec = {name: name.slice(0,34), k};
    for (const scheme of ['dark','light']) {
      await page.emulateMedia({colorScheme: scheme});
      await page.goto(`${origin}/?q=${encodeURIComponent(k)}&mode=exact&dict=${id}`);
      try { await page.waitForSelector('#out details[open] .article', {timeout: 8000}); } catch (_) {}
      await page.waitForTimeout(700);
      rec[scheme] = await page.evaluate(measure);
    }
    out.push(rec);
  }
  await page.emulateMedia({colorScheme: 'light'});
  await page.unroute('**/*', ro);
  return out.map(r => [r.name, r.k, r.dark.err||`${r.dark.kind} dark bad=${r.dark.badPct}% med=${r.dark.median}`, r.light.err||`light bad=${r.light.badPct}% med=${r.light.median}`, (r.dark.worst||[]).join(' | ')].join(' ¦ '));
}
