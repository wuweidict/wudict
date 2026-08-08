# wudict — client API

The contract for third-party clients of the wudict HTTP server: browser
extensions, scripts, other front-ends. Written for someone who will never read
this codebase's source, and **verified against a running server**, not read off
the code.

Everything here is stable. Everything not here is private to the desktop UI and
will move without notice.

- **Public:** `GET /api/dicts`, `GET /api/search`, `GET /res/{dict}/{name}`
- **Private — do not depend on:** `/api/prefs`, `/api/ingest`, `/api/setup`,
  `/api/library`, `/api/rescan`, `/api/reveal`, `/`, `/setup`, `/assets/…`

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

Errors mid-stream arrive as a frame with `"error"` set, not as an HTTP status:
the status is already 200 by the time anything can go wrong. A client must check
frames, not just `response.ok`.

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

---

## 3. `GET /api/search`

The only endpoint a lookup needs.

| param | values | notes |
|---|---|---|
| `q` | the term | required, URL-encoded |
| `mode` | `exact`, `prefix`, `contains`, `fts` | default `prefix` |
| `dict` | `all`, one id, or a **comma-separated ordered list** | default `all` |
| `n` | max results **per dictionary** | keep small |
| `format` | `raw`, `clean`, `text` | default `raw`; see §8 |

### Frames

```
{"t":"begin","slots":[{"dict":"<id>","name":"…"},…]}   ← the layout, in REQUESTED order
{"t":"hit","i":2,"dict":"<id>","name":"…","results":[{"Headword":"…","Body":"<html>"}]}
{"t":"hit","i":0,…}                                     ← note: arrives out of order
{"t":"end"}
```

`i` indexes into `begin.slots`. **Hits arrive in completion order, not request
order** — a small dictionary answers before a large one. Render into slots by
`i` and the display order is the order you asked for, with no sorting and no
waiting for the slowest dictionary.

`results[]` uses Go field names — `Headword` and `Body`, capitalised. `Body` is
article HTML (§5).

### One request is enough

A comma-separated `dict` list is answered in a **single** request, concurrently
server-side. Measured, 5 dictionaries, cold: **315 ms, 66 KB, one HTTP round
trip.** There is no reason for a client to fan out N requests, and good reason
not to.

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
