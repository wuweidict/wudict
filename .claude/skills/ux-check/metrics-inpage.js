() => {
    const vis = el => { const r = el.getBoundingClientRect(); const s = getComputedStyle(el);
      return r.width > 0 && r.height > 0 && s.visibility !== 'hidden' && s.display !== 'none' && +s.opacity > 0.05 &&
        r.bottom > 0 && r.top < innerHeight && r.right > 0 && r.left < innerWidth; };
    const parse = c => { const m = c.match(/[\d.]+/g); return m ? m.map(Number) : [0,0,0,0]; };
    const lum = ([r,g,b]) => { const f = v => { v/=255; return v<=.03928? v/12.92 : Math.pow((v+.055)/1.055,2.4); }; return .2126*f(r)+.7152*f(g)+.0722*f(b); };
    const bgOf = el => { let e = el; while (e) { const c = parse(getComputedStyle(e).backgroundColor); if (c.length<4 || c[3] > .5) { if (c.length>=3 && !(c.length===4&&c[3]===0)) return c; } e = e.parentElement; } return [255,255,255]; };
    const blend = (fg, bg) => fg.length===4 ? [0,1,2].map(i=>fg[i]*fg[3]+bg[i]*(1-fg[3])) : fg;
    const cr = (a,b) => { const [x,y] = [lum(a),lum(b)].sort((p,q)=>q-p); return +((x+.05)/(y+.05)).toFixed(2); };
    const name = el => (el.id?'#'+el.id:el.tagName.toLowerCase()+(el.className&&typeof el.className==='string'?'.'+el.className.trim().split(/\s+/).join('.'):'')) + ' «' + ((el.getAttribute('aria-label')||el.textContent||el.value||'').trim().slice(0,28)) + '»';
    const inter = [...document.querySelectorAll('a[href],button,select,input:not([type=hidden]),summary,textarea,[role=button],[tabindex]:not([tabindex="-1"])')].filter(vis);
    const small = inter.map(el => { const r = el.getBoundingClientRect(); return {n:name(el), w:Math.round(r.width), h:Math.round(r.height)}; })
      .filter(o => o.w < 44 || o.h < 44);
    const under24 = small.filter(o => o.w < 24 || o.h < 24);
    const sizes = {}, lowC = [];
    const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
    const seen = new Set();
    while (walker.nextNode()) { const t = walker.currentNode; if (!t.textContent.trim()) continue; const el = t.parentElement; if (!el || seen.has(el) || !vis(el)) continue; seen.add(el);
      const s = getComputedStyle(el); const fs = parseFloat(s.fontSize); sizes[fs] = (sizes[fs]||0) + 1;
      const fg = blend(parse(s.color), bgOf(el)); const c = cr(fg, bgOf(el)); const big = fs >= 24 || (fs >= 18.66 && +s.fontWeight >= 700);
      if (c < (big ? 3 : 4.5)) lowC.push({n:name(el), fs, c}); }
    return { vw: innerWidth, vh: innerHeight, docH: document.documentElement.scrollHeight, hScroll: document.documentElement.scrollWidth > innerWidth,
      interactiveVisible: inter.length, targetsUnder44: small.length, targetsUnder24: under24, fontSizesInUse: sizes,
      lowContrast: lowC.slice(0, 40), lowContrastCount: lowC.length,
      bar: (() => { const b = document.getElementById('bar'); return b ? Math.round(b.getBoundingClientRect().height) : null; })() };
}
