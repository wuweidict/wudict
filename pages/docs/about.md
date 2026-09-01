---
title: About
description: What is WuWeiDict, the functions, design, and capabilities.
---

# About WuWeiDict

WuWeiDict is a modern dictionary frontend optimized for speed that runs in your browser. It lets you quickly find definitions 
in all your dictionaries at once.


## You can repair a dictionary

Some dictionaries contain broken files. A damaged script inside a dictionary can
break the interactive part or the styling of all articles, and you cannot edit the
dictionary file.

WuWeiDict reads a `res/` folder inside the dictionary's library folder first. Put
a file there and it replaces the dictionary's built-in file.

[How to override a file](dictionaries/override.md){ .md-button }

## Everything the app does, the API does

The web page is one client for a compact HTTP REST API. The browser extension
uses the same REST API. Your own code can also use the same REST API and 
there are working code sample in the link below.

[HTTP API reference](reference/api.md){ .md-button }

## The name

*Wú wéi* (無為) is the classical Chinese term for effortless action/no action. The project
is called WuWeiDict because a good tool should work effortlessly. The
program is called `wudict` because it's shorter, and easier to type and pronounce.

## License

WuWeiDict is free software under the GPL, version 3 or later.

[License and attribution](help/license.md){ .md-button }
