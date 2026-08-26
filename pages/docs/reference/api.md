---
title: HTTP API
description: The endpoints WuWeiDict serves - the streaming search API, the dictionary list, and resource files.
---

# HTTP API

The web page is one client of this API. The browser extension is another. Your
own script can be a third.

The server answers on `http://127.0.0.1:6888` by default. There is no
authentication, because there is no network exposure: the server binds to the
loopback address unless you change
[`SERVER_IP`](configuration.md#server_ip-and-server_port).

Three endpoints are public to browser extensions: `/api/dicts`, `/api/search`
and `/res/`. They are read-only. Everything else is same-origin, so a web page
or an extension cannot reach it.

## Streaming responses

`/api/dicts` and `/api/search` answer with **NDJSON**: one JSON object per
line, sent as soon as it is ready.

Read it line by line. Do not wait for the whole body. That is the entire point:
the first dictionary's results arrive while the slowest one is still reading.

Every stream starts with a `begin` line and ends with an `end` line.

## GET /api/search

``` text title="request"
GET /api/search?q=flight&mode=prefix&format=clean&n=5
```

| Parameter | Values | Default |
| --- | --- | --- |
| `q` | the search text; required | - |
| `mode` | `exact`, `prefix`, `contains`, `fts` | `prefix` |
| `format` | `raw`, `clean`, `text` | `raw` |
| `dict` | `all`, one id, or a comma-separated ordered list of ids | `all` |
| `n` | maximum results per dictionary | `20` |

`fuzzy` is accepted as an old alias for `mode`. It now means `prefix`.

### The response

``` json title="one line per object, in arrival order"
{"t":"begin","slots":[{"dict":"oxford","name":"oxford"},{"dict":"webster","name":"webster"}]}
{"t":"hit","i":1,"dict":"webster","name":"Webster's Revised Unabridged","results":[{"Headword":"flight","Body":"<div>…</div>"}]}
{"t":"hit","i":0,"dict":"oxford","name":"Oxford Advanced Learner's","results":[]}
{"t":"end"}
```

The `begin` line lists every dictionary that will answer, in your preferred
order. Draw the layout from it at once.

Each `hit` line carries `i`, the position of that dictionary in the `slots`
array. Fill that slot. Lines arrive in completion order, not in slot order.

| Field on a `hit` line | Meaning |
| --- | --- |
| `i` | slot index from the `begin` line |
| `dict`, `name` | the dictionary's id and its real display name |
| `results` | array of `{"Headword", "Body"}`; `Body` is article HTML |
| `skipped` | this dictionary does not support the requested mode |
| `deferred` | this dictionary was not opened, because the search reached its memory cap; not an error |
| `error` | this dictionary failed; the others still answered |

A `deferred` dictionary answers normally when you ask for it alone. See
[`SEARCH_MEMORY`](configuration.md#search_memory).

A search is cancelled after 30 seconds.

### Article formats

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

## GET /api/dicts

``` text title="request"
GET /api/dicts
```

``` json title="response"
{"t":"begin","total":2}
{"t":"dict","dict":{"id":"oxford","name":"Oxford Advanced Learner's","format":"mdx","path":"/Users/me/Dicts/oald.mdx","entries":184000,"caps":{"Exact":true,"Prefix":true,"Contains":false,"FTS":false}}}
{"t":"end"}
```

| Field | Meaning |
| --- | --- |
| `id` | the value to pass as `dict` to `/api/search` |
| `name` | display name |
| `format` | `mdx`, `stardict`, `slob`, `dsl`, `bgl` or `wudict` |
| `path` | the source file, or the `text.db` for a prepared dictionary |
| `entries` | headword count |
| `caps` | which modes this dictionary supports now |
| `error` | this dictionary could not be opened |

`caps` is the authority on which modes to offer. `Contains` and `FTS` are false
until that dictionary is prepared with those indexes.

??? info "Fields the ☰ panel uses"

    These are present when they apply, and describe where a dictionary's data
    lives on disk.

    | Field | Meaning |
    | --- | --- |
    | `source` | the original file, if still present |
    | `mediaSrc` | companion media sources, such as `.mdd` files |
    | `textDB`, `mediaDB` | the prepared databases |
    | `folder` | the library folder holding them |
    | `dbPath` | path of the prepared database |
    | `dbSize`, `mediaSize` | their sizes in bytes |
    | `hasMedia` | packable media exists, so "pack media" is offered |
    | `containsStale` | the substring index was built with older text folding, so it may miss words; the panel offers a rebuild |

## GET /res/{dict}/{name}

Returns one file from a dictionary: an image, a stylesheet, a script or audio.

``` text title="request"
GET /res/oxford/audio/flight__gb.mp3
```

`{name}` is the path the article asks for, including subfolders. A `404` means
the dictionary does not hold that file.

Speex audio is converted to WAV and answered as `audio/wav`. A `.spx` file that
cannot be converted, or one you supplied in a
[`res/` folder](../dictionaries/override.md), is answered as it is, with the
type `audio/ogg`.

Files you supplied in a `res/` folder are served without caching, so an edit
takes effect on reload.

## Errors

Every failing request answers with a JSON object and an HTTP status.

``` json title="400, 404 and 500 all look like this"
{"error":"missing q parameter"}
```

| Status | When |
| --- | --- |
| `400` | `q` is missing, or `mode` or `format` is unknown |
| `404` | no such dictionary id, or no such resource |
| `500` | the dictionary could not be opened |

A failure inside one dictionary during a search is not an error status. It
arrives as an `error` field on that dictionary's `hit` line, and the other
dictionaries still answer.

## Same-origin endpoints

These serve the web page itself. They are not reachable from an extension or a
web page, and they may change between releases.

| Endpoint | Purpose |
| --- | --- |
| `GET /api/rescan` | re-scan the dictionary folders |
| `GET /api/ingest` | prepare a dictionary, or add an index |
| `GET /api/library`, `DELETE /api/library` | list and remove library items |
| `GET /api/config` | the effective settings and where each came from |
| `GET /api/prefs`, `PUT /api/prefs` | dictionary order and on/off switches |
| `GET /api/setup`, `GET /setup` | the first-run setup page and its checks |
| `GET /api/reveal` | show a file in the system file manager |
| `POST /api/power` | the Android shell reports memory and battery state |

## A worked example

``` sh title="search every dictionary, print the headwords"
curl -sN 'http://127.0.0.1:6888/api/search?q=flight&mode=exact&format=text&n=3' \
  | while IFS= read -r line; do
      printf '%s\n' "$line" | python3 -c '
import json,sys
m = json.loads(sys.stdin.read())
if m.get("t") == "hit":
    for r in m.get("results") or []:
        print(m["name"], "|", r["Headword"])
'
    done
```

`curl -sN` disables buffering, so lines print as they arrive.
