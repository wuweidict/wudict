---
title: The idea
description: Why WuWeiDict exists — wu wei, effortless action, and the belief that reading tools should never be slower than the thought that reaches for them.
---

# The idea

Your tools should be as fast as your thought. They should just work, not get in the way, and be almost unnoticeable.
And this is the challenge that WuWeiDict takes on. You want to look up a word, and the answer is right there.

Results appear as soon as any dictionary has a hit, and more results show up as more hits are collected.

## Your data is fully private

The server binds to the loopback address,
listens on your port, and does not know the internet exists. No
accounts, no telemetry, no analytics, no crash and log collection services, and no tracking.

**Override any file** If a dictionary
ships a bad script or a missing media file, wudict lets you override the original resource with files you place in the
`./res` subfolder under the internal DB dir, e.g. `~/.wudict/db/<dict_folder>/res`.

## WuWeiDict API

The `wudict` server uses a compact documented HTTP contract — a
handful of JSON endpoints. The same JSON API is used both by the WuWeiDict main page and the **[Chrome/Firefox extension](extension.md)**
which lets you hover or right-click any word on any page, and show its definition in a popup.
