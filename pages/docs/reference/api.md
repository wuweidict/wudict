---
title: HTTP API
description: The endpoints wuDict serves - the streaming search API, the dictionary list, resource files, and the OpenAPI document that defines them.
---

# HTTP API

[OpenAPI compatible API Reference](https://wudict.legbehindneck.com/api/){ .md-button }

The web page is one client of this API. The browser extension is another. Your
own script can be a third.

The server answers on `http://127.0.0.1:6888` by default. There is no
authentication, because there is no network exposure: the server binds to the
loopback address unless you change
[`SERVER_IP`](configuration.md#server_ip-and-server_port).

Three endpoints are read-only and open to other origins: `/api/dicts`,
`/api/search` and `/res/`. Everything else is same-origin, so neither an
extension nor a web page can reach it.

## Who can call it

| Client | Reaches |
| --- | --- |
| A program - `curl`, Node, Python, an Electron main process, a native app | everything, with no configuration |
| The WuWeiDict page itself | everything; it is same-origin |
| A browser extension | the three read-only endpoints, unless [`BROWSER_EXTENSIONS`](configuration.md#browser_extensions) narrows it |
| A web page in a browser | the three read-only endpoints, but **only** if [`WEB_ORIGINS`](configuration.md#web_origins) names its origin |

CORS is a rule a browser applies to pages. It is not a lock on the server:
anything that is not a browser page never sends an `Origin` header, and so is
neither checked nor limited. What keeps the server private is
[`SERVER_IP`](configuration.md#server_ip-and-server_port) binding it to
loopback.

### Calling it from your own page

Add the page's origin to the config file and restart:

``` toml title="~/.wudict/wudict.toml"
WEB_ORIGINS = ["http://localhost:3000"]
```

``` js title="then, from a page served at http://localhost:3000"
const url = 'http://127.0.0.1:6888/api/search?q=flight&mode=exact&format=clean&n=3';
const res = await fetch(url);            // no credentials, no custom headers
for (const line of (await res.text()).split('\n')) {
  if (!line) continue;
  const msg = JSON.parse(line);
  if (msg.t === 'hit') console.log(msg.name, msg.results);
}
```

Read the stream with a `ReadableStream` reader instead of `await res.text()` if
you want each dictionary to appear as it answers - that is the point of the
format, and it is described [below](#streaming).

Three things to know before you debug a failure:

-   **The origin must match exactly.** Scheme, host and port. A page on
    `https://localhost:3000` is not the page on `http://localhost:3000`.
-   **Do not send credentials or custom headers.** The server never answers
    `Access-Control-Allow-Credentials`, so `credentials: 'include'` fails the
    check. A plain `GET` needs no preflight.
-   **Chrome treats `127.0.0.1` as a private address** and preflights every
    request to it from a page that is not itself local. WuWeiDict answers that
    preflight for allowed origins, so this normally just works - but a Chrome
    policy or another extension can still block local network access.

A `null` origin is never allowed: that is what a `file://` page sends, and
every one of them sends the same value. Serve your page over `http://` while
developing, even locally.

## The contract

Every parameter, every field and every status code lives in one OpenAPI 3.1
document. This page explains the parts a schema cannot state.

| Where | What for                                                                                                                                  |
| --- |-------------------------------------------------------------------------------------------------------------------------------------------|
| `http://127.0.0.1:6888/api/openapi.yaml` | the running server describes itself; feed the downloaded .yml file to any OpenAPI compatible tool such as e.g. https://editor.swagger.io/ |
| [`internal/server/web/openapi.yaml`](https://github.com/wuweidict/wudict/blob/master/internal/server/web/openapi.yaml) | the same file, in the repository                                                                                                          |
| `make api-ui` | renders it into `dist/api-explorer.html`, one offline page                                                                                |
| [ API explorer](../api/index.html) | the same document, rendered and browsable, with a `curl` example on every endpoint                        |

A test walks that document against the server's route table in both
directions, so an endpoint cannot be added, renamed or dropped without the
document following it.

## Streaming

`/api/dicts`, `/api/search` and `/api/rescan` answer with **NDJSON**: one JSON
object per line, sent as soon as it is ready.

Read it line by line. Do not wait for the whole body. That is the entire point:
the first dictionary's results arrive while the slowest one is still reading.

Every stream starts with a `begin` line and ends with an `end` line.

`/api/ingest` streams Server-Sent Events instead, because it reports progress
rather than results.

## Reading a search stream

``` text title="request"
GET /api/search?q=flight&mode=prefix&format=clean&n=5
```

``` json title="one line per object, in arrival order"
{"t":"begin","i":0,"slots":[{"dict":"oxford","name":"oxford"},{"dict":"webster","name":"webster"}]}
{"t":"hit","i":1,"dict":"webster","name":"Webster's Revised Unabridged","results":[{"Headword":"flight","Body":"<div>…</div>"}]}
{"t":"hit","i":0,"dict":"oxford","name":"Oxford Advanced Learner's","results":[]}
{"t":"end","i":0}
```

The `begin` line lists every dictionary that will answer, in your preferred
order. Draw the layout from it at once.

Each `hit` line carries `i`, the position of that dictionary in the `slots`
array. Fill that slot. Lines arrive in completion order, not in slot order.

A `hit` can report `skipped`, `deferred` or `error` instead of results:

-   **`skipped`** - this dictionary does not support the requested mode. Ask
    `/api/dicts` for `caps` before offering a mode.
-   **`deferred`** - this dictionary was not opened, because the search reached
    its memory cap. Not an error. Asking for that dictionary alone answers it.
    See [`SEARCH_MEMORY`](configuration.md#search_memory).
-   **`error`** - this dictionary failed. The others still answered.

A search is cancelled after 30 seconds.

`fuzzy` is accepted as an old value for `mode`. It now means `prefix`.

### Article formats

`format` decides how much of the dictionary's own markup you get back.

| `format` | What you get | Relative size |
| --- | --- | --- |
| `raw` | the dictionary's HTML, untouched | 1.0 |
| `clean` | structure, emphasis and media; no scripts, styles or presentation | about 0.5 |
| `text` | no markup at all | about 0.4 |

Ask for `clean` unless you render the article with the dictionary's own
stylesheet. It also removes every stylesheet and script request the article
would otherwise cause - 82 of them for one entry in a large dictionary.

`clean` rewrites root-absolute links such as `/res/…` to full URLs, so the
payload works in a page served from somewhere else.

## Reading the dictionary list

``` json title="GET /api/dicts"
{"t":"begin","total":2}
{"t":"dict","dict":{"id":"oxford","name":"Oxford Advanced Learner's","format":"mdx","path":"/Users/me/Dicts/oald.mdx","entries":184000,"caps":{"Exact":true,"Prefix":true,"Contains":false,"FTS":false}}}
{"t":"end"}
```

> curl
```sh
curl -s 'http://127.0.0.1:6888/api/dicts' | jq 
```

`total` arrives first, from dictionary ids alone. Show a count before anything
is opened.

`id` is the value to pass as `dict` to `/api/search`. `caps` is the authority
on which modes to offer: `Contains` and `FTS` stay false until that dictionary
is prepared with those indexes.

Each row also carries where the dictionary's data lives - the source file, the
prepared databases, their sizes. The ☰ panel is built from those fields; the
[document](https://github.com/wuweidict/wudict/blob/master/internal/server/web/openapi.yaml)
lists them.

## Resource files

``` text title="resource request"
GET /res/oxford/audio/flight__gb.mp3
```

The path after the dictionary id is the path the article asks for, subfolders
included. A `404` means the dictionary does not hold that file.

Speex audio is converted to WAV and answered as `audio/wav`. A `.spx` file that
cannot be converted, or one you supplied in a
[`res/` folder](../dictionaries/override.md), is answered as it is, with the
type `audio/ogg`.

Files you supplied in a `res/` folder are served without caching, so an edit
takes effect on reload.

## Errors

Every failing request answers with `{"error":"…"}` and an HTTP status.

A failure inside one dictionary during a search is not an error status. It
arrives as an `error` field on that dictionary's `hit` line, and the other
dictionaries still answer.

## Same-origin endpoints

The rest of the surface serves the web page and the Android shell: rescan,
ingest, the library, the settings, the preferences, the power state. They are
not reachable from an extension or a web page - `WEB_ORIGINS` does not widen
that set, only who may call the three read-only endpoints - and they may change
between releases.

They are tagged `internal` in the document. Read them there rather than here,
so there is one description only.

## A working example

``` sh title="search every dictionary, print the headwords + definitions"
python3 -c '
import json, urllib.request

url = "http://127.0.0.1:6888/api/search?q=flight&mode=exact&format=text&n=3"

with urllib.request.urlopen(url) as f:
    for line in f:
        m = json.loads(line)
        if m.get("t") == "hit":
            for r in m.get("results") or []:
                print(m["name"], "|", r["Headword"])
                print("=" * 70)
                print(r.get("Body", ""))
'
```

`curl -sN` disables buffering, so lines print as they arrive.
