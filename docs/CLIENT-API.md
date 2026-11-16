# wudict — client API

The contract for third-party clients of the wudict HTTP server: browser
extensions, scripts, other front-ends. Written for someone who will never read
the server's source. Checked against the server source and its OpenAPI document. The server publishes that document at `GET /api/openapi.yaml`
(same-origin only: open it in a browser tab, an extension cannot read it).

Everything here is stable. Everything not here is private to the desktop UI and
will move without notice.

- **Public:** `GET /api/dicts`, `GET /api/search`, `GET /res/{dict}/{name}`,
  and `/` **as a link target only** — its `?q=…&mode=…&dict=…` query string is
  a supported way to hand a lookup to a browser tab (§9). Its HTML is not.
- **Private — do not depend on:** every other route, for example `/api/prefs`,
  `/api/config`, `/api/ingest`, `/api/setup`, `/api/library`, `/api/rescan`,
  `/api/reveal`, `/api/about`, `/api/browse`, `/api/suggest`, `/api/groups`, `/api/lemmas`,
  `/api/intake`, `/api/reindex`, `/setup`, `/browse`, `/assets/…`

Default origin `http://127.0.0.1:6888`. Both parts are user-configurable
(`--ip` / `SERVER_IP`, `--port` / `SERVER_PORT`), so a client must treat the
base URL as configuration, never a constant.

The **host** in that base URL must be an IP literal or `localhost`. Any other
name (`http://mymac.local:6888`) is answered `421 Misdirected Request` unless
the user lists it in `TRUSTED_HOSTS` or it is the name the server listens on
(`SERVER_IP`). This is the server's DNS-rebinding guard.

**No access key is needed for the public routes.** When the server listens on a
non-loopback address, `AUTH` makes every other route require the access key
(`Authorization: Bearer <key>`, else `401` with `{"auth":"required"}`). The
three public routes and their preflights stay open, so an extension sends no
key and holds no secret.

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

| status | when |
|---|---|
| `400` | `q` blank; `mode` unknown (`contains` included); `format` unknown; a `wildcard` pattern over 64 characters |
| `404` | no id in `dict` resolves |
| `421` | the `Host` is not an IP literal, `localhost`, a `TRUSTED_HOSTS` name or the `SERVER_IP` name |

**After it starts** the status is already `200`, so a per-dictionary failure
rides on that dictionary's own frame as an `error` field (§3). There is **no
`{"t":"error"}` frame**; nothing else terminates the stream early.

So: check `response.ok` first and parse the body as plain JSON if it is false,
then check each frame's `error` while streaming. Neither check subsumes the
other.

**Switch on `t` and ignore a `t` you do not know.** Both streams have gained
frame types over time (`busy` on `/api/dicts`, `morph` on `/api/search`). A
client that throws on an unknown `t` breaks on the next server release.

---

## 2. `GET /api/dicts`

Lists what is available. Call it **once at startup**, not per lookup.

```
{"t":"begin","total":128}
{"t":"dict","dict":{"id":"6e0594a94deb","name":"Babylon English-Spanish",
                    "format":"bgl","entries":129224,
                    "caps":{"Exact":true,"Prefix":true,"FTS":false,"Wildcard":true}}}
…
{"t":"busy","busy":[{"id":"…","name":"OED2","phase":"indexing","done":120000,"total":576000}]}
…
{"t":"end","etag":"\"ac4fd9fecabd8ec4cf544212\""}
```

- `id` — opaque, stable while the dictionary stays in place. **Persist the id**,
  but re-resolve by `name` if it ever 404s: ids are derived from the path.
- `caps` — which modes this dictionary can answer **now**: `Exact`, `Prefix`,
  `FTS`, `Wildcard`. `FTS` stays false until the user builds the full-text
  index; `Wildcard` stays false until the dictionary is indexed (automatic, but
  not instant on first run). Asking for an unsupported mode is not an error;
  that dictionary is simply skipped (`"skipped":true`).
- `error` — this dictionary could not be opened (`tooLarge` beside it: too
  large to open on this system). The row is still sent, so `total` on `begin`
  is exactly the number of `dict` rows. Wait for `end` anyway: it is the only
  frame that says the list is complete.
- `articleLang` — ISO 639-1 code of the language the articles are written in,
  when known. The right default voice for reading a selection aloud.
- `groups` — the picker's groups this dictionary belongs to (`{f, fl, fo, v,
  vl}`: facet id and label, facet rank, value id and label). A group is selected
  by intersecting rows client-side; `/api/search` has no group parameter.
- Other fields exist (paths, sizes, `job`, `outdated`, `builtin`). They are for
  the desktop panel; ignore them.
- `busy` — a progress frame for rows pending 10 s or more, sent every second
  while there are any and once more, empty, when the last has arrived. A
  client may show it or ignore it; a row it names may already have arrived.
- `etag` on `end` — keep it with the rows. On the next start, use the rows you
  kept at once and send `If-None-Match: <etag>`: an unchanged list answers
  `304` with no body, and the server decides that without opening a single
  dictionary. A `200` is the new list, with a new `etag`. No `etag` means do
  not keep this answer (a row carried an error, an index was being built, or a
  dictionary was being prepared as it was listed). It is on `end`, not in an
  `ETag` header, because whether an answer may be kept is known only after its
  last row. `If-None-Match` is not a CORS-safelisted header, so from an
  extension origin this request is preflighted; the server's `OPTIONS` answer
  allows it (§4).
- `/api/dicts` lists the dictionaries the server has already found; it does not
  look in the folders again. Files added on disk appear after the user rescans
  in the app, or after a server restart.

---

## 3. `GET /api/search`

The only endpoint a lookup needs.

| param | values | notes |
|---|---|---|
| `q` | the term | required, URL-encoded (`encodeURIComponent` also encodes a wildcard `?` as `%3F`, which is required) |
| `mode` | `exact`, `prefix`, `fts`, `wildcard` | default `prefix`. `fuzzy` is a legacy alias of `prefix`. `contains` is retired and is a **400**: use `*word*` in `wildcard` |
| `dict` | `all`, one id, or a **comma-separated ordered list** | default `all`; no length limit |
| `n` | max results **per dictionary** | default **20**, capped at 500 — too many for hover; send it explicitly |
| `format` | `raw`, `clean`, `text` | default `raw`; see §8 |
| `hl` | `1` to mark the query's words in each article | read only with `mode=fts`; default off; see §8 |
| `mark` | words to mark in each article in place of the query's | every mode, no `hl` needed; one phrase, last word a prefix; at most 200 characters |

A search is cut off after **30 s**, or after 5 minutes when it covers exactly
one dictionary.

### Frames

Captured from a running server, verbatim:

```
{"t":"begin","slots":[{"dict":"6e0594a94deb","name":"6e0594a94deb"},{"dict":"bd07910f6552","name":"bd07910f6552"}],"i":0}
{"t":"hit","i":1,"dict":"bd07910f6552","name":"Cambridge English Dictionary Online.mdx","skipped":true}
{"t":"hit","i":0,"dict":"6e0594a94deb","name":"Babylon English-Spanish","skipped":true}
{"t":"end","i":0}
```

**`i` is present on every frame and meaningful only on `hit`.** The encoder
emits it unconditionally, so `begin`, `morph` and `end` carry a meaningless `"i":0`.
Do not use the presence of `i` to decide anything — switch on `t` first.

`t` is one of exactly four values:

| `t` | fields | meaning |
|---|---|---|
| `begin` | `slots[]` | the layout, in the order you asked for. Always first, exactly once. |
| `hit` | `i`, `dict`, `name`, and then `results[]` **or** `skipped` **or** `deferred` **or** `error`; `rung` in `fts` | one dictionary has finished. One per slot, plus at most one lemma hit (below). |
| `morph` | `from`, `to`, `lang` | every `hit` after it answers `to`, the lemma of `from`, not the typed word. Zero or more per stream — one per language whose lemma found something, after every first-wave `hit`. |
| `end` | — | the stream is complete. Always last, exactly once. |

`i` indexes into `begin.slots`. **Hits arrive in completion order, not request
order** — a small dictionary answers before a large one. Render into slots by
`i` and the display order is the order you asked for, with no sorting and no
waiting for the slowest dictionary.

### A `hit` is not always a result

Every slot produces one `hit` in the first wave. Four mutually exclusive
outcomes, plus a plain miss:

- **`results[]`** — found. Uses Go field names, capitalized: `Headword`, `Body`.
- **`"skipped":true`, no `results`** — this dictionary cannot answer this
  `mode` (see `caps` in §2). Not an error; expected whenever a mixed set of
  dictionaries is queried with `fts` or `wildcard`.
- **`"deferred":true`, no `results`** — not opened, because the search reached
  its memory cap (`SEARCH_MEMORY`; off on desktop by default, on by default on
  Android, and only dictionaries that are not yet indexed count against it).
  Not an error: a search of this dictionary alone always answers it, so offer
  that. `indexing:true` beside it means the dictionary is being indexed right
  now and will stop being deferred when that finishes. `bytes` is the estimated
  cost, for logs.
- **`"error":"…"`, no `results`** — this dictionary failed. The others are
  unaffected and the stream continues.
- **none of these** — the dictionary searched and found nothing.

Only the plain miss can change. After the first wave, a slot that missed may
get a second `hit` for the same `i`, preceded by a `morph` frame, carrying the
lemma's results; it replaces the miss. A lemma hit always has `results`: a
second miss is not sent. So a `results[]`, `skipped`, `deferred` or `error`
hit is final at once, and a miss is final only at `end`.

`rung` appears only in `fts`: `phrase` (the words adjacent and in order),
`near` (within ten tokens, any order), `words` (anywhere in the article) or
`query` (an operator expression, answered as written). It is per dictionary,
so two slots in one response can answer on different rungs. Show it when it is
not `phrase`: the phrase itself was not found there.

A dictionary searched through a full-text index its own file carries (a ZIM
archive; `ownText` in its `/api/dicts` row) can add two flags beside `rung`.
That index does not record where words stand, so a phrase or a proximity is
checked in the text of the best-ranked candidate articles only: `partial`
says a stricter reading than `rung` was not found *among those*, and `more`
says `rung` itself was answered from them, so there may be further results.

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

The server opens at most **8 dictionaries concurrently** on desktop (on Android,
one per CPU core). Listing more than that does not make the request slower than
the equivalent fan-out would be, but it does not make it faster either: past
the limit, latency grows with the queue. For a "more" action, eight ids is the
point of diminishing returns. A wide list can also come back partly `deferred`
(above); naming one dictionary per request never does.

This also gives the "look up more dictionaries" flow for free: the first request
names one id; "more" names the next N ids — **not** including the first, which
is already on screen.

### Matching semantics — read this before designing hover

- `exact` falls back to case- and accent-folded equality.
- **Lemmatization, every mode.** For a one-word query that finds nothing in a
  dictionary, the server searches that dictionary again for the word's lemma,
  with the lemma data for the dictionary's language (English built in, other
  languages installed by the user; `MORPH_CACHE = 0` turns it off):
  `q=running` also finds *run*. Not for a multi-word query, and not for a
  `wildcard` pattern that contains `*` or `?`. Only a dictionary that searched
  and missed is retried: one that hit, was skipped, deferred or failed is not.
  The answer arrives as another `hit` frame for the same slot `i`, after a
  `{"t":"morph","i":0,"from":…,"to":…,"lang":…}` frame (one per language that
  produced results, in language-code order). Every `hit` after a `morph` frame
  is for `to`, not for what was typed: say so.
- In practice large dictionaries do store inflected forms (verified on OED:
  `running`, `sped`, `better`, `mice` all hit directly), so hover works better
  than the previous paragraph suggests — but coverage depends on the
  dictionary and on the installed lemma data. A client wanting reliable lookup
  keeps its own fallback chain for what lemmatization does not cover:
  punctuation variants, and languages without lemma data (exact → naive suffix
  stripping → `prefix`).
- `prefix` returns everything starting with `q`, so for hover it is a poor
  default: hovering *run* yields *runny*, *runway*, *runt*. Use `exact` for
  hover and offer `prefix` as an explicit widening. It ignores case and
  accents.
- `wildcard` matches the **whole headword**: `?` is exactly one character, `*`
  any number including none (`colo*r`, `*ending`, `*word*` for a substring).
  There is no escape: `*` and `?` are always wildcards. A pattern with a
  capital letter is matched as typed, case and accents included; any other
  ignores them. A leading `*` is the slowest kind of query.
- `fts` searches the article text, not headwords. A multi-word query is read as
  a phrase first, then within ten words, then the words anywhere, stopping at
  the first reading that finds something (reported as `rung`, above). Quotes
  and `AND`, `OR`, `NOT`, `NEAR(a b, n)` are parsed when present and answered as
  written; an unparseable query is read as plain words. A bare word matches as
  a prefix (`run` finds *running*); a quoted word matches exactly.

---

## 4. CORS for extension origins only. This decides your architecture.

Websites get **no `Access-Control-Allow-Origin` header**, on any endpoint,
unless the user names the site's origin in `WEB_ORIGINS` (empty by default; it
grants the same three routes). A page cannot read wudict, which is the whole
protection an unauthenticated loopback server has.

**Browser extensions get one, on the public read API only.** From version 2.8,
wudict echoes the request's `Origin` back when — and only when — all of this
holds:

| condition | detail |
|---|---|
| scheme | `chrome-extension://` or `moz-extension://`, nothing else. `null`, `file://` and every http(s) origin are refused |
| shape | origin only: a host, no port, no path, no query, no userinfo |
| route | exactly `GET /api/dicts`, `GET /api/search`, `GET /res/` |
| pin (optional) | if `BROWSER_EXTENSIONS` is set, the origin must be on that list |

`Vary: Origin` is sent on those routes regardless, so a cache cannot serve one
client's answer to another. The header is **never** `*`, and
`Access-Control-Allow-Credentials` is **never** sent: the server has no cookies
and must not start honouring any. `OPTIONS` on the three routes answers 204
with `Allow-Methods: GET, OPTIONS`, `Allow-Headers: accept, content-type,
if-none-match` and `Max-Age: 600`, plus `Allow-Private-Network: true` when the
preflight asks for it. A plain lookup is CORS-simple and never preflights; a
conditional `/api/dicts` (§2) does, and is allowed.

Everything else stays same-origin. No extension can `DELETE /api/library`, drive
`/api/ingest` or `/api/rescan`, or read `/api/prefs`, `/api/config`,
`/api/reveal`. A browser request the browser marks `Sec-Fetch-Site: cross-site`
is answered `403` on every route except these three (and top-level page
navigations), so this is enforced even where CORS would only hide the answer.

The shape this buys you:

```
content script  ──message──▶  background service worker  ──fetch──▶  wudict
   (hover, DOM)                (extension origin, no host permission)
```

The worker is still where every fetch belongs — it is also the right place for
the response cache (§7), one shared by every tab rather than one per tab — but
it no longer needs `host_permissions` to get there. That matters for a reason
beyond the install prompt: **anything a content script puts in the page's DOM
carrying a `http://127.0.0.1:…` URL is the *page's* request**, and Chrome/Edge
142+ and Firefox 151+ gate those behind the Local Network Access prompt, which
names the site the user is reading. So fetch media in the worker and hand the
content script bytes (the wudict browser extension's `src/background/media.js`), and play
audio in an offscreen document rather than in the page. Then no loopback URL
ever reaches the page, and no prompt ever appears.

A host permission would also work and would bypass CORS, but the wudict browser
extension declares none — not even an optional one — and a client should not
need one. The server's CORS grant is the supported transport: a wudict too old
to answer extension origins, or one whose `BROWSER_EXTENSIONS` list omits the
extension, is fixed on the server side by updating it or widening that list. Holding a host permission invites putting
loopback URLs back in the page, which is exactly what raises the Local Network
Access prompt this design exists to avoid.

The remaining exposure: any extension the user has installed can read their
dictionaries. Any of them could have declared a host permission itself, so the
CORS grant adds no capability that was not already one prompt away, and
`BROWSER_EXTENSIONS` narrows it to a named list for anyone who wants that.

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
`{name}` may contain slashes (`js/entry.js`): send it unencoded, exactly as the
article references it. `.spx` audio is transcoded to WAV on the way out and
answered as `audio/wav`, so clients need no Speex decoder. A `.spx` that cannot
be transcoded, or one supplied by an override folder, is sent raw as
`audio/ogg`, which a browser cannot play; treat it as missing audio.

Cached `public, max-age=86400`; an untranscoded `.spx` is `no-store`. A
seekable file answers `Range` requests. A missing resource is 404 — common and
not an error worth surfacing: many dictionaries reference files they do not
contain.

Files placed in `<library folder>/res/` override or supply these and are served
uncached, so an edit shows on reload; see the README.

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

**Do not poll `/api/dicts`.** Once at startup, and again only when the user
asks — and at startup, conditionally, with the `etag` you kept (§2).

**One request in flight per lookup.** The server already parallelizes across
dictionaries (8 at a time on desktop), so client-side concurrency buys nothing and costs
the abort semantics above: with one request you can cancel cleanly, with five
you cannot.

---

## 8. `format=` — how much of the article you want

```
GET /api/search?…&format=raw|clean|text
```

**`raw`** (default) — the dictionary's own HTML. The server changes it in
these places only:

- a bundled file points at `/res/` (§5);
- a lookup link takes the `entry://` spelling (§11);
- `<base>` is dropped;
- an `<audio>` that a browser does not show becomes a `wu-audio` link (§10);
- with `hl=1`, full-text matches are marked (below).

This is what wudict's own page needs, since it renders articles with the
dictionary's own CSS and scripts.

**`clean`** — structural markup only. Dropped: `<script>`, `<style>`, `<link>`,
`<iframe>`, `<object>`, `<form>` and friends *with their content*; every
`on*` handler; `style` and all presentational attributes; every `class`
except wudict's own `wu-` classes (`wu-audio`, `wu-hl`, and the classes of the
articles wudict writes for DSL and XDXF); `javascript:`, `vbscript:`,
non-image `data:` and protocol-relative URLs.
Unknown and purely-cosmetic elements are **unwrapped** — their tags go, their
text stays — so nothing can silently swallow a definition. Kept: headings,
paragraphs, lists, tables, emphasis, links, images and audio.

Two things `clean` does that a naive strip would not:

- **Root-absolute `/res/…` becomes absolute**, against the origin you actually
  reached the server on. The payload is self-contained — the §5 trap is
  handled for you.
- **Audio in an `<object>` survives.** `<object type="audio/…" data="…">`
  becomes a plain `<audio src="…" controls>`; every other `<object>` is dropped
  as an embedding vector. This is the form DSL sound takes in a library folder
  indexed by an older version of wudict. Current DSL sound is a `wu-audio`
  link (§10).

**`text`** — no markup at all. Entities decoded, block boundaries as newlines,
whitespace collapsed.

### `hl=1` — the match marked in the article

With `mode=fts` and `hl=1`, every match of the query's words inside a `Body` is
wrapped in `<mark class="wu-hl">`. In every other mode `hl` is ignored: the
match is the headword itself. Off unless asked, so a client gets the
dictionary's own bytes by default. Marking is whole-token and prefix-based,
like the full-text match, and stops at 500 marks per article (the article is
still complete). `raw` and `clean` keep `<mark class="wu-hl">`; `text` drops
the marks with all other markup. A popup that shows a full-text result should
ask for it and scroll the first `<mark>` into view: in a long article the match
is otherwise below the fold.

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

An unknown `format` is a **400** (`unknown format "x" (raw, clean, text)`), not
a silent fallback: a client asking for something that does not exist should
hear about it.

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
| `mode` | `prefix`, `exact`, `wildcard`, `fts` | one of the page's four modes, spelled exactly so |
| `dict` | **one** dictionary id, `all`, or a group id `g:<f>:<v>` | *not* a comma list — see below |
| `from` | one dictionary id | a one-shot scope for this search only; leaves the dropdown alone (§11) |
| `theme` | `auto`, `light`, `dark` | **writes** the user's stored preference; don't set it |

### Always send `mode` and `dict` explicitly

`applyURL` only assigns a parameter that is **present**, and a `mode` only when
it is one of the four:

```js
if (!setMode(p.get("mode")) && p.get("q") && notForWords($("mode").value)) setMode("prefix");
if (p.get("dict")) selectDict(p.get("dict"));
```

A missing or unknown `mode` (`contains` included) keeps the mode the user last
picked, with one exception: a remembered `fts` or `wildcard` becomes `prefix`,
because a word handed over is a headword lookup. A missing `dict` keeps the
user's last selection. So a "search everywhere" link that omits `dict` will
quietly search only the dictionary they happen to have selected — the one case
where being explicit is not pedantry but correctness. Send both, every time.

### `dict` takes exactly one id

It is assigned to a `<select>`, not parsed as a list. The comma list of §3 is an
`/api/search` feature only; do not send one here.

An id the page does not list yet gets a temporary option, named when its first
result arrives. An id the server does not know (removed, moved, from another
library) falls back to `all`, so a stale link still searches, just more widely.
Build these links from the ids you got in the same session anyway.

A **group** id is `g:` + a row's `groups[].f` + `:` + its `groups[].v` (§2),
for example `g:pair:en-ru`. A group is known only from the whole list, so a
group link is answered after the page has loaded it.

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
what the popup leaves out. The global link answers a different
question — *"who else defines this?"* — so it belongs in the fixed header,
reachable no matter how far the user has scrolled into one long entry.

Timing is safe: a link that names one dictionary (`dict=<id>` or `from=<id>`)
is answered at once, without waiting for the dictionary list, and the dropdown
shows that dictionary even before the list has arrived. A link to `all` or a
group is answered once the page has a list: at once from the copy it kept from
the last visit, otherwise after loading it.

Open with `chrome.tabs.create({url})`, or reuse a remembered tab id with
`chrome.tabs.update` and fall back to `create` when it has been closed — one
wudict tab that keeps being reused is far less annoying than twenty.

---

## 10. Audio pronunciation

In the current scope, handle exactly one shape: **an anchor that plays an
audio file.** An anchor is one when either test passes:

- its `class` contains `wu-audio` (see below);
- its href ends in an audio extension:

```js
/\.(mp3|ogg|wav|spx|m4a)(\?|#|$)/i
```

Test the class first. A `wu-audio` link is audio whatever its extension
(`.opus`, `.flac`, `.aac`); the extension test covers the anchors a dictionary
writes itself. Count the class only for an href the server could have written
(next subsection): a dictionary can put the class on a `#fragment` link, and
that link must still scroll. The desktop UI applies both tests. Anything else — a speaker
rendered by the dictionary's own script, a `sound://` scheme, an `<embed>` — is
out of scope and should be ignored rather than half-supported.

### `wu-audio` links

The server writes `<a class="wu-audio" href="…">` in two cases:

- **A Lingvo DSL sound** (`[s]`): the link text is 🔊.
- **A dictionary's own `<audio>` element** with no `controls` attribute and an
  `<img>` inside it. LDOCE6 (StarDict) writes every pronunciation this way:

```html
<audio src="0hwd/bre/7/aeroplane0205.mp3"><img src="img/spkr_r.png"></audio>
```

A browser shows nothing for this element. It hides an `<audio>` that has no
`controls`, and the `<img>` is fallback content, which it draws only when it
cannot play audio. The server sends the element as the link its image shows:

```html
<a class="wu-audio" href="/res/ee263e878085/0hwd/bre/7/aeroplane0205.mp3"><img src="/res/ee263e878085/img/spkr_r.png"></a>
```

The href is the element's `src`, else the `src` of its first child
`<source>`. The conversion is part of the `/res/` rewrite (§5): a source the
rewrite maps into `/res/` (`sound://a.mp3`, `file://a.mp3`, `a.mp3`) is
converted with that `/res/` href. The href is always relative, root-absolute
(`/res/…`), protocol-relative, `http(s):` or `data:audio/…`.

`raw` and `clean` both carry the link, and `clean` keeps the `wu-audio` class.
In `clean`, a protocol-relative or `data:audio/…` href is dropped with every
other such URL (§8); the anchor keeps its class and image but plays nothing.

The server leaves an `<audio>` as the dictionary wrote it when any of these is
true:

- it has a `controls` attribute: the browser draws a player (see "`clean`
  also gives you native players");
- it has an `autoplay`, `muted`, `loop`, `id`, `on…` or `data-…` attribute:
  the browser or the dictionary's script acts on the element itself;
- it has no `<img>` inside: fallback text such as "Your browser does not
  support audio" is not a button;
- it has no `src` and no child `<source src>`, or the source is only a
  `#fragment` or a `?query`, or it has a scheme the rewrite does not map into
  `/res/` (`javascript:`, `entry://`, …);
- it is inside an `<a>`, or holds one: an anchor cannot contain an anchor;
- it holds anything other than text, comments and phrasing elements (`<img>`,
  `<span>`, `<b>`, …), each closed before `</audio>`. A block element, a
  script, a nested `<audio>` or a parent's end tag decides where the browser
  closes the element, and the server does not guess it. The end of the article
  closes it, as in the browser.

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
  const wuAudio = a.classList.contains("wu-audio") && href && !/^[#?]/.test(href);
  if (!wuAudio && !/\.(mp3|ogg|wav|spx|m4a)(\?|#|$)/i.test(href)) return;
  e.preventDefault();          // or the popup navigates away
  playAudio(href);
});
```

`closest("a[href]")` rather than checking the target: dictionaries wrap a
speaker **image** in the anchor, and so does a `wu-audio` link made from an
`<audio>`, so the click lands on the `<img>`.

`preventDefault` is not optional — without it the anchor navigates and the
popup is replaced by a bare media player.

The href must be **absolute**. With `format=clean` it already is (§8
absolutizes `/res/…` for you). With `format=raw` you must prefix the base
yourself, or the popup resolves it against the host page and 404s (§5).

### `clean` also gives you native players

`format=clean` rewrites `<object type="audio/…" data="…">` into
`<audio src="…" controls>`. This is the form DSL sound takes in a library
folder indexed by an older version of wudict. An `<audio controls>` that the
dictionary wrote itself is kept, with its `src` absolutized. Neither needs
JavaScript: the browser draws the control. So a popup rendering `clean` sees
two audio shapes: native `<audio controls>` elements that work by themselves,
and the anchors above that need the handler. Both, not either.

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

So `entry://`, `entry:`, `bword://`, `bword:`, `d:` and `x:` are all the same
thing, spelled six ways by six repackers. wudict itself only ever sends
`entry://` (and `entry:@…` for a sub-entry): a dictionary's `bword:` links are
respelled by the server before they reach a client. `bword:` is Babylon Ltd.'s
proprietary scheme, still accepted because a dictionary's own script may build
one. After the scheme, split at the **first literal `#`** — a `#` inside a
headword arrives percent-encoded, so decoding before splitting would promote it
to a delimiter — then percent-decode each half (all or nothing: a half that
does not decode is kept as written), and trim the headword of **spaces and tabs
only** (a headword may begin or end with U+3000 or NBSP, which
`String.prototype.trim` would eat).

wudict percent-encodes exactly four things in a headword it links to: `%`,
`#`, control characters, and a **leading `@`**. That last one is why the
**sub** test runs on the target **before decoding**: `entry:@Examples` is a
section of this entry, while `entry://%40home` is the headword `@home`.

| href | kind | what it means |
|---|---|---|
| `entry://run` | **lookup** | another headword |
| `entry:run` · `bword://run` · `d:run` · `x:run` | **lookup** | same thing, other spellings |
| `defendant` (bare, no scheme) | **lookup** | slob/OALD write cross-references bare |
| `entry:@Examples` | **sub** | a *section of this entry*, not a word |
| `entry://%40home` · `entry://C%23` | **lookup** | the headwords `@home` and `C#` |
| `entry://#sense2` · `#sense2` | **anchor** | a place in the article on screen |
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
