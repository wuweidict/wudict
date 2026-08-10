# wudict — client API

The contract for third-party clients of the wudict HTTP server: browser
extensions, scripts, other front-ends. Written for someone who will never read
this codebase's source, and **verified against a running server**, not read off
the code.

Everything here is stable. Everything not here is private to the desktop UI and
will move without notice.

- **Public:** `GET /api/dicts`, `GET /api/search`, `GET /res/{dict}/{name}`,
  and `/` **as a link target only** — its `?q=…&mode=…&dict=…` query string is
  a supported way to hand a lookup to a browser tab (§9). Its HTML is not.
- **Private — do not depend on:** `/api/prefs`, `/api/ingest`, `/api/setup`,
  `/api/library`, `/api/rescan`, `/api/reveal`, `/setup`, `/assets/…`

Default origin `http://127.0.0.1:6888`. Both parts are user-configurable
(`--ip` / `SERVER_IP`, `--port` / `SERVER_PORT`), so a client must treat the
base URL as configuration, never a constant.

---

## 1. Transport: NDJSON, not JSON

`/api/dicts` and `/api/search` stream **newline-delimited JSON**: one complete
JSON object per line, flushed as it is produced. There is no enclosing array.

```
{"t":"begin","total":128}
{"t":"dict","dict":{…}}
{"t":"end"}
```

`await response.json()` **fails** on these endpoints. Either read the whole body
and split on `\n`, or — preferred for search — consume the stream and handle
each line as it lands, so the first dictionary can render before the slowest one
has opened.

`Content-Type: application/x-ndjson; charset=utf-8`.

### Two different failure shapes, and you need both checks

**Before the stream starts** — bad parameters, unknown dictionary — the reply is
an ordinary HTTP error with a plain JSON body, *not* NDJSON:

```
HTTP/1.1 400 Bad Request
Content-Type: application/json; charset=utf-8

{"error":"missing q parameter"}
```

**After it starts** the status is already `200`, so a per-dictionary failure
rides on that dictionary's own frame as an `error` field (§3). There is **no
`{"t":"error"}` frame**; nothing else terminates the stream early.

So: check `response.ok` first and parse the body as plain JSON if it is false,
then check each frame's `error` while streaming. Neither check subsumes the
other.

---

## 2. `GET /api/dicts`

Lists what is available. Call it **once at startup**, not per lookup.

```
{"t":"begin","total":128}
{"t":"dict","dict":{"id":"6e0594a94deb","name":"Babylon English-Spanish",
                    "format":"bgl","entries":129224,
                    "caps":{"Exact":true,"Prefix":true,"Contains":false,"FTS":false}}}
…
{"t":"end"}
```

- `id` — opaque, stable while the dictionary stays in place. **Persist the id**,
  but re-resolve by `name` if it ever 404s: ids are derived from the path.
- `caps` — which modes this dictionary can answer. Asking for an unsupported
  mode is not an error; that dictionary is simply skipped (`"skipped":true`).
- Other fields exist (paths, sizes). They are for the desktop panel; ignore them.
- `total` on `begin` is the number of `dict` frames to expect.
- This stream has **no error frame and no per-row error field**: a dictionary
  that cannot be described is omitted, so `total` is an upper bound on the rows
  you will actually receive. Do not wait for a count that may never arrive —
  wait for `end`.

---

## 3. `GET /api/search`

The only endpoint a lookup needs.

| param | values | notes |
|---|---|---|
| `q` | the term | required, URL-encoded |
| `mode` | `exact`, `prefix`, `contains`, `fts` | default `prefix` |
| `dict` | `all`, one id, or a **comma-separated ordered list** | default `all`; no length limit |
| `n` | max results **per dictionary** | default **20** — too many for hover; send it explicitly |
| `format` | `raw`, `clean`, `text` | default `raw`; see §8 |

### Frames

Captured from a running server, verbatim:

```
{"t":"begin","slots":[{"dict":"6e0594a94deb","name":"6e0594a94deb"},{"dict":"bd07910f6552","name":"bd07910f6552"}],"i":0}
{"t":"hit","i":1,"dict":"bd07910f6552","name":"Cambridge English Dictionary Online.mdx","skipped":true}
{"t":"hit","i":0,"dict":"6e0594a94deb","name":"Babylon English-Spanish","skipped":true}
{"t":"end","i":0}
```

**`i` is present on every frame and meaningful only on `hit`.** The encoder
emits it unconditionally, so `begin` and `end` both carry a meaningless `"i":0`.
Do not use the presence of `i` to decide anything — switch on `t` first.

`t` is one of exactly three values:

| `t` | fields | meaning |
|---|---|---|
| `begin` | `slots[]` | the layout, in the order you asked for. Always first, exactly once. |
| `hit` | `i`, `dict`, `name`, and then `results[]` **or** `skipped` **or** `error` | one dictionary has finished. Exactly one per slot. |
| `end` | — | the stream is complete. Always last, exactly once. |

`i` indexes into `begin.slots`. **Hits arrive in completion order, not request
order** — a small dictionary answers before a large one. Render into slots by
`i` and the display order is the order you asked for, with no sorting and no
waiting for the slowest dictionary.

### A `hit` is not always a result

Every slot produces exactly one `hit`, which is the slot's terminator whatever
it carries. Three mutually exclusive outcomes:

- **`results[]`** — found. Uses Go field names, capitalised: `Headword`, `Body`.
- **`"skipped":true`, no `results`** — this dictionary cannot answer this
  `mode` (see `caps` in §2). Not an error; expected whenever a mixed set of
  dictionaries is queried with `contains` or `fts`.
- **`"error":"…"`, no `results`** — this dictionary failed. The others are
  unaffected and the stream continues.

A `hit` with none of the three is a dictionary that answered with no matches.
Treat all four as "slot `i` is done".

`slots[].name` is **the id again, not the dictionary's name.** The real name
arrives on the `hit`, because naming a dictionary means opening it and `begin`
is emitted before any open. If you paint placeholder rows from `begin`, label
them from your own `/api/dicts` cache, not from `slots[].name`.

An empty or whitespace-only `q` is a **400**, not an empty result set. Guard it
client-side; a hover handler will otherwise fire one on every gap between words.

Unknown ids in a `dict` list are **silently dropped**, and the search proceeds
with whatever resolved. Only if *none* resolve do you get a `404`. So a stale
saved id degrades quietly — compare `begin.slots` against what you asked for if
you want to notice.

### One request is enough

A comma-separated `dict` list is answered in a **single** request, concurrently
server-side. Measured, 5 dictionaries, cold: **315 ms, 66 KB, one HTTP round
trip.** There is no reason for a client to fan out N requests, and good reason
not to.

The server opens at most **8 dictionaries concurrently**. Listing more than
eight does not make the request slower than the equivalent fan-out would be, but
it does not make it faster either: past eight, latency grows with the queue. For
a "more" action, eight ids is the point of diminishing returns.

This also gives the "look up more dictionaries" flow for free: the first request
names one id; "more" names the next N ids — **not** including the first, which
is already on screen.

### Matching semantics — read this before designing hover

- `exact` falls back to case- and accent-folded equality. It does **not**
  lemmatise. wudict has no morphology: `q=running` finds an entry only if some
  dictionary stores *running* as a headword.
- In practice large dictionaries do store inflected forms (verified on OED:
  `running`, `sped`, `better`, `mice` all hit directly), so hover works better
  than the previous paragraph suggests — but coverage is the **dictionary's**,
  not wudict's, and it varies. A client wanting reliable inflected lookup needs
  its own fallback chain (exact → naive suffix stripping → `prefix`).
- `prefix` returns everything starting with `q`, so for hover it is a poor
  default: hovering *run* yields *runny*, *runway*, *runt*. Use `exact` for
  hover and offer `prefix` as a deliberate widening.

---

## 4. No CORS. This decides your architecture.

The server sends **no `Access-Control-Allow-Origin` header**, on any endpoint.

For an MV3 extension this means a content script cannot fetch the server
directly — its requests carry the host page's origin and are subject to that
page's CORS. The working shape is:

```
content script  ──message──▶  background service worker  ──fetch──▶  wudict
   (hover, DOM)                (host_permissions: http://127.0.0.1:6888/*)
```

The service worker holds the host permission, so its fetches are not
cross-origin-restricted. It is also the right place for the response cache
(§7) — one cache shared by every tab, rather than one per tab.

Do not "fix" this by adding CORS headers to wudict without deciding who may
call it: the server binds loopback by default precisely because it is
unauthenticated.

---

## 5. What `Body` actually is — and what it is not

**`Body` is the dictionary's own HTML, essentially verbatim.** There is no
semantic layer. wudict does not parse senses, parts of speech or examples, and
cannot: every dictionary's markup is its own, which is why the desktop UI
renders articles unmodified rather than reformatting them.

A client wanting "just the definition" must reduce the HTML itself. Measured on
real articles (`q=speed`, `mode=exact`, `n=1`):

| dictionary | payload | text only | markup share | `/res/` refs |
|---|---|---|---|---|
| LDOCE (contemporary English) | 187,100 B | 65,062 B | 65% | **82** |
| Oxford English Dictionary 2nd | 123,937 B | 69,024 B | 44% | 2 |
| Merriam-Webster Online | 48,561 B | 9,411 B | **81%** | 9 |
| Merriam-Webster Collegiate | 27,132 B | 6,405 B | 76% | 10 |
| Babylon English-Spanish | 135 B | 98 B | 27% | 0 |

Two conclusions a hover client should treat as design constraints:

1. **Markup is 44–81% of the payload** in the dictionaries people actually
   hover over. Reducing it is not cosmetic: `format=clean` (§8) is measured at
   1.9x smaller and `format=text` at 2.6x, with the DOM-node and parse-time
   savings following the same curve.
2. **`/res/` refs are extra HTTP requests.** Rendering LDOCE with full fidelity
   costs **83 requests** for one hover. `format=clean` (§8) removes every
   stylesheet and script request — the mandatory ones — and leaves media as
   plain `<img>`/`<audio>` with absolute URLs, which the client can load lazily.
   A hover that is glanced at and dismissed then costs exactly **one request**.

### The `/res/` trap

Article HTML references bundled files as **root-absolute** paths:

```html
<link rel="stylesheet" href="/res/bd07910f6552/styles_cb.css">
<img src="/res/bd07910f6552/spkr.png">
```

Root-absolute is correct inside the wudict page. **Injected into a third-party
page it resolves against that page's origin and 404s.** Any client embedding
`Body` anywhere other than a wudict-origin document must rewrite
`/res/` → `<base>/res/`, or strip the references.

### Bodies that carry `<script>`

Some articles ship their own JavaScript and jQuery. The desktop UI routes those
to a sandboxed `srcdoc` iframe and script-free ones to a shadow root. A client
choosing to run dictionary scripts inherits both lessons that cost this project
real time:

- **`@font-face` does not participate in shadow-tree scoping.** A face declared
  inside a shadow root is parsed and then ignored, so a dictionary's embedded
  IPA font silently falls back to a serif with missing glyphs. Faces must be
  hoisted to the document.
- **In an `about:srcdoc` document the document URL and the base URL differ.**
  The browser therefore treats `href="#sense2"` as a *cross-document*
  navigation and loads the base URL into the frame. Every fragment link must be
  intercepted.

A client that strips scripts avoids both entirely. That is the recommended
default; fidelity is the exception, not the baseline.

---

## 6. `GET /res/{dictID}/{name}`

Serves a file the dictionary bundles — stylesheet, script, image, audio.
`{name}` may contain slashes (`js/entry.js`). `.spx` audio is transcoded to WAV
on the way out, so clients need no Speex decoder.

Cached `public, max-age=86400`. A missing resource is 404 — common and not an
error worth surfacing: many dictionaries reference files they do not contain.

Files placed in `<library folder>/res/` override or supply these; see the README.

---

## 7. Client patterns that matter

**Debounce and abort.** Hover fires continuously. Debounce ~150–250 ms, and
`AbortController.abort()` the in-flight request when a new one starts — the
server stops streaming for an aborted request, so this saves work on both
sides. (The desktop UI uses 300 ms and the same abort.)

**Cache in the service worker.** An LRU keyed by `dict|mode|q` removes repeat
lookups entirely — the largest single win for hover, where the same word is
crossed many times. Cache the *reduced* form, not the raw HTML, and the memory
cost drops with it.

**Budget one request per lookup.** Achievable: comma-separated `dict`, `n=1`,
and no `/res/` fetches once the dictionary's own CSS/JS are stripped.

**Keep `n` at 1–3 for hover.** `n` is per dictionary; `n=25` across ten
dictionaries is megabytes.

**Do not poll `/api/dicts`.** Once at startup, and again only when the user asks.

**One request in flight per lookup.** The server already parallelises across
dictionaries (8 at a time), so client-side concurrency buys nothing and costs
the abort semantics above: with one request you can cancel cleanly, with five
you cannot.

---

## 8. `format=` — how much of the article you want

```
GET /api/search?…&format=raw|clean|text
```

**`raw`** (default) — the dictionary's HTML verbatim. What wudict's own page
needs, since it renders articles with the dictionary's own CSS and scripts.

**`clean`** — structural markup only. Dropped: `<script>`, `<style>`, `<link>`,
`<iframe>`, `<object>`, `<form>` and friends *with their content*; every
`on*` handler; `class`, `style` and all presentational attributes;
`javascript:`, `vbscript:`, non-image `data:` and protocol-relative URLs.
Unknown and purely-cosmetic elements are **unwrapped** — their tags go, their
text stays — so nothing can silently swallow a definition. Kept: headings,
paragraphs, lists, tables, emphasis, links, images and audio.

Two things `clean` does that a naive strip would not:

- **Root-absolute `/res/…` becomes absolute**, against the origin you actually
  reached the server on. The payload is self-contained — the §5 trap is
  handled for you.
- **A DSL dictionary's pronunciation survives.** DSL spells audio as
  `<object type="audio/x-wav" data="…">`; dropping `<object>` as an embedding
  vector would silently remove audio from every DSL dictionary, so it is
  rewritten to a plain `<audio src="…" controls>`.

**`text`** — no markup at all. Entities decoded, block boundaries as newlines,
whitespace collapsed.

### Measured, on real articles

`q=speed`, nine heavy dictionaries, 748 KB of raw article:

| | bytes | of raw |
|---|---|---|
| `raw` | 748,013 | 100% |
| `clean` | 396,264 | **53%** |
| `text` | 289,969 | **39%** |

So `clean` is ~1.9x smaller and `text` ~2.6x. Per dictionary the spread is
wide — LDOCE 187 KB → 77 KB (41%), Cambridge Pronouncing 3.6 KB → 0.5 KB (15%),
OED 125 KB → 91 KB (73%), because OED's markup is genuinely structural while
LDOCE's is mostly `<span class="…">`.

An unknown `format` is a **400**, not a silent fallback: a client asking for
something that does not exist should hear about it.

---

## 9. Opening the full entry in the wudict page

The app reads its own state from the query string, so a client can hand a
lookup over to a real browser tab. Verified against `applyURL()`:

```
<base>/?q=<word>&mode=<mode>&dict=<id|all>
```

| param | values | notes |
|---|---|---|
| `q` | the term | **required** — nothing searches without it |
| `mode` | `prefix`, `exact`, `contains`, `fts` | must match the mode dropdown exactly |
| `dict` | **one** dictionary id, or `all` | *not* a comma list — see below |
| `theme` | `auto`, `light`, `dark` | **writes** the user's stored preference; don't set it |

### Always send `mode` and `dict` explicitly

`applyURL` only assigns a parameter that is **present**:

```js
if (p.get("mode")) $("mode").value = p.get("mode");
if (p.get("dict")) $("dict").value = p.get("dict");
```

Anything you omit keeps whatever the user last used, restored from their saved
preferences. So a "search everywhere" link that omits `dict` will quietly
search only the dictionary they happen to have selected — the one case where
being explicit is not pedantry but correctness. Send both, every time.

### `dict` takes exactly one id

It is assigned to a `<select>`, not parsed as a list. A comma-separated value
matches no option and silently selects nothing, which then searches nothing.
The comma list of §3 is an `/api/search` feature only.

An id that no longer exists fails the same silent way, so build these links
from the ids you got in the same session, not from stale storage.

Scoping to a **disabled** dictionary works: an explicit `dict=<id>` bypasses
the user's enabled set. A link to a dictionary they have switched off still
opens that dictionary's entry.

### The two links a hover popup wants

Build both from one word, and put them in different places for a reason:

```js
const enc = encodeURIComponent;
// per dictionary — next to that dictionary's block, inside the scroll area
const one = `${base}/?q=${enc(word)}&mode=exact&dict=${enc(dictId)}`;
// everywhere — in the popup header, which must not scroll away
const all = `${base}/?q=${enc(word)}&mode=exact&dict=all`;
```

The per-dictionary link answers *"show me all of this entry"* — the popup is
showing a truncated `n=1` result, and the tab shows the whole thing in the
dictionary's own styling, with its scripts and CSS working, which is exactly
what the popup deliberately threw away. The global link answers a different
question — *"who else defines this?"* — so it belongs in the fixed header,
reachable no matter how far the user has scrolled into one long entry.

Timing is safe: the page runs `applyURL` only after `/api/dicts` has finished,
so a valid `dict` id is always in the dropdown by the time it is assigned.

Open with `chrome.tabs.create({url})`, or reuse a remembered tab id with
`chrome.tabs.update` and fall back to `create` when it has been closed — one
wudict tab that keeps being reused is far less annoying than twenty.

---

## 10. Audio pronunciation

In the current scope, handle exactly one shape: **an anchor whose href is an
audio file.**

```js
/\.(mp3|ogg|wav|spx|m4a)(\?|#|$)/i
```

That is the same test the desktop UI uses. Anything else — a speaker rendered
by the dictionary's own script, a `sound://` scheme, an `<embed>` — is out of
scope and should be ignored rather than half-supported.

### Use one reused `Audio`, not a new one per click

```js
let audioEl = null;                       // module scope, NOT per click
function playAudio(url) {
  if (!url) return;
  if (!audioEl) audioEl = new Audio();
  audioEl.src = url;
  const p = audioEl.play();
  if (p && p.catch) p.catch(err => console.warn("audio play failed:", url, err));
}
```

This is `playAudio` from the wudict page, and the single reused element is the
part that matters. `.spx` is transcoded to WAV **server-side**, so the first
response can lag long enough that an unreferenced `new Audio(url)` is garbage
collected before it ever plays — silently, and only sometimes, which is the
worst way for a bug to behave.

Three more things that follow from how the server behaves:

- **Never sniff the extension to pick a decoder.** Playback decodes by
  `Content-Type`, and `/res/` serves `.spx` as `audio/wav`. The URL lies; the
  header does not.
- **`play()` returns a promise that can reject** — autoplay policy, a missing
  file, an unsupported codec. Always `.catch`. A click is a user gesture so it
  will resolve; anything you trigger on *hover* will not.
- **This must live where the DOM is.** An MV3 service worker has no `Audio`
  constructor. Play in the content script or the popup, and let the worker do
  only the fetching.

### Wiring it to a click

```js
root.addEventListener("click", e => {
  const a = e.target.closest("a[href]");
  if (!a) return;
  const href = a.getAttribute("href");
  if (!/\.(mp3|ogg|wav|spx|m4a)(\?|#|$)/i.test(href)) return;
  e.preventDefault();          // or the popup navigates away
  playAudio(href);
});
```

`closest("a[href]")` rather than checking the target: dictionaries wrap a
speaker **image** in the anchor, so the click lands on the `<img>`.

`preventDefault` is not optional — without it the anchor navigates and the
popup is replaced by a bare media player.

The href must be **absolute**. With `format=clean` it already is (§8
absolutises `/res/…` for you). With `format=raw` you must prefix the base
yourself, or the popup resolves it against the host page and 404s (§5).

### `clean` also gives you native players

`format=clean` rewrites a DSL dictionary's `<object type="audio/x-wav">` into
`<audio src="…" controls>`. That needs no JavaScript at all — the browser
draws the control. So a popup rendering `clean` sees two audio shapes: native
`<audio>` elements that work by themselves, and the anchors above that need
the handler. Both, not either.

---

## 11. Cross-reference links inside an article

Articles are full of links the dictionary's author wrote — to other headwords,
to sections of the same entry, to the web. **None of them may navigate the
popup.** The rule, without exceptions worth arguing about:

> A click that changes *which word* or *which dictionary* you are looking at
> leaves the popup and opens the full wudict in a tab (§9). The popup itself
> never becomes a browser.

A popup is a tooltip, not a viewport. It has no address bar, no back button and
no room; letting it navigate strands the user in a box they cannot get out of.
The full app has all three affordances, and it is one tab away.

### The grammar, exactly

Verified against `parseRef` and the click handlers. Scheme test:

```js
/^(?:(?:bword|entry):(?:\/\/)?|[dx]:)/i
```

So `bword:`, `bword://`, `entry:`, `entry://`, `d:` and `x:` are all the same
thing, spelled six ways by six repackers. After the scheme, split at the
**first literal `#`** — a `#` inside a headword arrives percent-encoded, so
decoding before splitting would promote it to a delimiter — then
percent-decode each half.

| href | kind | what it means |
|---|---|---|
| `bword://run` | **lookup** | another headword |
| `entry:run` · `d:run` · `x:run` | **lookup** | same thing, other spellings |
| `defendant` (bare, no scheme) | **lookup** | slob/OALD write cross-references bare |
| `bword://@Examples` | **sub** | a *section of this entry*, not a word |
| `bword://#sense2` · `#sense2` | **anchor** | a place in the article on screen |
| `https://…` | external | the open web |
| `…/x.mp3` | audio | §10 |

A bare href is a headword only when it looks like nothing else:

```js
href && !/^([a-z][\w+.-]*:|\/|#|res\/|assets\/)/i.test(href)
```

Each exclusion has an owner elsewhere — a real scheme, a rooted path, a
fragment, and wudict's own `res/`/`assets/` prefixes. Drop one and you will
search for `#sense2` as if it were a word.

### What to do with each

**lookup — the common case.** Open the full app, scoped to the dictionary the
link was written in. An author's reference means *this dictionary's* entry, so
searching all 128 buries it among namesakes:

```js
const url = `${base}/?q=${enc(word)}&mode=exact&dict=${enc(sourceDictId)}`;
```

`sourceDictId` is the `dict` field of the `hit` frame the article came from —
not the user's currently selected dictionary. Keep it alongside the rendered
HTML; you will need it for every link in that block.

(The app itself expresses the same scoping as `&from=<id>`, a one-shot scope
that leaves the visible dropdown alone. Either works; `dict=` is preferred
here because it is truthful about what is being searched, and because §9 asks
you to send `dict` explicitly regardless.)

**sub — `@`-prefixed.** MDict repacks store an entry's collapsible sections
(*Examples*, *Collocations*, *Word Origin*) as headwords beginning with `@`.
The desktop app expands them inline; a popup should not try. Treat it as a
lookup and pass the `@` through unchanged — it is a real headword and resolves:

```js
`${base}/?q=${enc("@Examples")}&mode=exact&dict=${enc(sourceDictId)}`
```

Label it as a section rather than a word if you can — `@Examples` shown raw in
a link looks like a bug.

**anchor — the one shape where a tab is the wrong answer.** It points *inside
the content already on screen*. `format=clean` keeps `id` attributes, so scroll
to it within the popup instead. That is not navigation and does not violate the
rule above — nothing changes about which word or dictionary is displayed. If
the target id is missing, do nothing; do not fall back to searching, or you
will look up `sense2`.

Opening a tab for an anchor is a poor second best: `applyURL` does not read
`location.hash`, so the tab cannot restore the position and simply reopens the
entry at the top.

**external `http(s)://`** — a normal new tab to the site itself, never wudict,
and never the popup. One Cambridge entry links out three times.

### Every one of them needs `preventDefault`

Whatever you do with a link, cancel the default first. In a popup injected into
someone else's page, a bare `<a href="defendant">` resolves against **that
page's** URL, so the host site navigates and the user loses both your popup and
their place. This is the same base-URL trap as §5, arriving through a different
door.

Delegate once on the popup root rather than binding per link — articles have
hundreds:

```js
root.addEventListener("click", e => {
  const a = e.target.closest("a[href]");
  if (!a) return;
  e.preventDefault();                    // first, always
  route(a.getAttribute("href"), sourceDictId);
});
```
