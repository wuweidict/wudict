---
title: The idea
description: Why WuWeiDict exists — wu wei, effortless action, and the belief that reading tools should never be slower than the thought that reaches for them.
---

# The idea

Your tools should as fast as your thought. They should just work, should not get in the way, and be almost invisible.
And this is the challenge that WuWeiDict take on. You reach for a word, and the answer is
already there.

## The problem worth solving

WuWeiDict reads all your dictionaries in any of the six formats and presents all
of them behind one search box.

## Two engines, one interface

Under the hood lives a deliberately simple idea. Every dictionary gets two
ways to be read:

- **Immediately**, through its own native index — the _preview_ mode. Drop
  the file in, search at once. No import step exists, because nothing needs
  importing.
- **Prepared**, quietly, into a small library folder that gives the
  dictionary powers its original format never had — searching _inside_ the
  definitions, look up partial words, packing audio. Preparation runs by
  itself on first use, costs a couple of megabytes, and is always reversible.

Results appear as soon as any dictionary has an answer, and more results appear
as more answers are available.

## Designed for total privacy

**Your privacy is the default.** The server binds to your loopback address,
listens on your port, and does not know the internet exists. There is no
account, no telemetry, and not tracking.

**Override any file** Even when a dictionary
ships a damaged script or a missing image, wudict serves the bytes exactly
as stored and let's you override the original resource with files you place in the
`./res` subfolder under the internal DB dir, e.g. `~/.wudict/db/<dict_folder>/res`.

## WuWeiDict API

The `wudict` server uses a compact documented HTTP contract — a
handful of endpoints, NDJSON. The JSON API is used by the **[Chrome/Firefox extension](extension.md)**
to enable hovering any word on any page,
and show the defintion in a popup.
