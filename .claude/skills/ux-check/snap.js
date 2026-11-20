// Playwright browser_run_code template: deterministic screenshots of the app's fixed UI states, for a
// before/after pixel diff (diff.sh). Fill the placeholders into a copy under .playwright-mcp/ (the MCP reads
// files only there or in the repo): ORIGIN = the server ("http://127.0.0.1:6953"), OUT = output folder
// relative to the repo root, ending in "/". Writes <scheme>-<width>-<state>.png; returns the list.
// Determinism: own context per shot, every non-GET request answered 204 unsent (nothing is written to the
// server, and no "could not save" banner appears at random); start the server from a fresh home each run;
// /api/random answered with a fixed word, animations, transitions and the caret switched off.
async (page) => {
  const origin = ORIGIN, out = OUT;
  const FREEZE = '*,*::before,*::after{animation:none!important;transition:none!important;caret-color:transparent!important}';
  const STATES = [
    {id: 'empty', path: '/'},
    {id: 'results', path: '/?q=serendipity&mode=prefix&dict=all', wait: '#out details[open] .article'},
    {id: 'panel', path: '/', act: async p => { await p.click('#panelBtn'); }},
    {id: 'settings', path: '/', act: async p => { await p.click('#panelBtn'); await p.click('#folders > summary'); }},
    {id: 'styler', path: '/', act: async p => { await p.click('#panelBtn'); await p.click('#folders > summary'); await p.click('#stylerLink'); }},
    {id: 'recall', path: '/', act: async p => { await p.click('#q'); await p.keyboard.press('ArrowDown'); }},
    {id: 'setup', path: '/setup'},
    {id: 'lemmas', path: '/lemmas'},
    {id: 'groups', path: '/groups'},
    {id: 'browse', path: '/browse'},
  ];
  const SIZES = [{w: 1440, h: 900, touch: false}, {w: 390, h: 844, touch: true}];
  const browser = page.context().browser();
  const done = [];
  for (const scheme of ['light', 'dark']) for (const sz of SIZES) for (const s of STATES) {
    const ctx = await browser.newContext({viewport: {width: sz.w, height: sz.h}, colorScheme: scheme,
      hasTouch: sz.touch, isMobile: sz.touch, reducedMotion: 'reduce'});
    await ctx.route('**/*', r => {
      const q = r.request();
      if (q.method() !== 'GET') return r.fulfill({status: 204, body: ''}); // a write that 'succeeds': no error banner, nothing stored
      if (q.url().includes('/api/random')) return r.fulfill({contentType: 'application/json', body: '{"word":"serendipity","dict":"fixed","name":"Sample dictionary"}'});
      return r.continue();
    });
    const p = await ctx.newPage();
    await p.goto(origin + s.path);
    await p.addStyleTag({content: FREEZE});
    if (s.wait) { try { await p.waitForSelector(s.wait, {timeout: 10000}); } catch (_) {} }
    await p.waitForTimeout(1200);
    if (s.act) { try { await s.act(p); } catch (e) { done.push(`${s.id}: ${e.message.split('\n')[0]}`); } await p.waitForTimeout(500); }
    const file = `${scheme}-${sz.w}-${s.id}.png`;
    await p.screenshot({path: out + file});
    done.push(file);
    await ctx.close();
  }
  return done.join('\n');
}
