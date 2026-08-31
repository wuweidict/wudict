---
title: About
description: What WuWeiDict is for, what it guarantees about your data, and the design rules behind it.
---

# About WuWeiDict

WuWeiDict answers one question fast: what does this word mean, according to the
dictionaries I already own.

Everything else follows from that.

## Speed comes first

A lookup should finish before you notice waiting. Three rules keep it that way.

- **Nothing blocks on the slowest dictionary.** Each dictionary streams its
  results as it finishes. The first answer reaches the page while other
  dictionaries are still reading.
- **Preparation happens in the background.** A dictionary is searchable the
  moment WuWeiDict finds it. Indexes are built later, one dictionary at a time, so
  the machine stays usable.
- **Articles are shown as the dictionary wrote them.** WuWeiDict does not reflow,
  paraphrase or restyle an entry. It loads the dictionary's own HTML, styles
  and scripts.

## Your data stays on your machine

The server binds to the loopback address `127.0.0.1` and listens on one port.
It has no account system, no telemetry, no analytics and no crash reporting. It
never uploads a dictionary, a query or a result.

One deliberate exception exists, and you control it: a browser extension may
read your dictionaries through three read-only endpoints.
[`BROWSER_EXTENSIONS`](reference/configuration.md#browser_extensions) narrows
that to a named list.

Web pages get nothing unless you say otherwise. Naming a page's origin in
[`WEB_ORIGINS`](reference/configuration.md#web_origins) lets it read those same
three endpoints; unset - the default - no page in a browser can reach WuWeiDict
at all. Neither key opens anything else: not your settings, not your
preferences, not your library.

## You can repair a dictionary

Dictionaries ship broken files. A damaged script inside a dictionary can
disable every interactive part of its articles, and you cannot edit the
dictionary file.

WuWeiDict reads a `res/` folder inside the dictionary's library folder first. Put
a file there and it replaces the dictionary's own. Delete it and the dictionary
is exactly as it shipped.

[How to override a file](dictionaries/override.md){ .md-button }

## Everything the app does, the API does

The web page is one client of a small HTTP API. The browser extension is
another. Your own script can be a third.

[HTTP API reference](reference/api.md){ .md-button }

## The name

*Wú wéi* (無為) is the classical Chinese term for effortless action. The product
is called WuWeiDict because a tool you notice is a tool that is in the way. The
program is called `wudict` because you type it more often than you read it.

## License

WuWeiDict is free software under the GPL, version 3 or later.

[License and attribution](help/license.md){ .md-button }
