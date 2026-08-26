---
title: Repair a dictionary
description: Replace a dictionary's broken script, style or media file by putting your own file in its res/ folder.
---

# Patch a dictionary

**Goal:** replace a file a dictionary ships broken, or supply missing files.

wuDict looks in the `~/.wudict/db/<dict_dir>/res/` folder
inside that dictionary's library folder **first**, and uses your file if one is
there.

## When a resource patch might be needed 

A dictionary carries its own styles, scripts, images and audio. Some files can be missing of incorrect content. 

Because a dictionary's own scripts usually load first,
one bad .js or .css file can distort all articles in that dictionary.

wuDict warns when it serves a `.js`, `.css`, `.html`, `.json`, `.xml`, `.svg`
or `.txt` file containing a NUL bytes. That byte cannot occur in those formats,
so it proves the stored copy is broken. The warning names the `res/` path that
would replace it.

## How to patch dictionary files

1. Find the dictionary's library folder. The ☰ panel shows the path.
2. Create a `res/` folder inside it.
3. Put your file there, under **the same path the article asks for**.

``` text title="two replacements and one addition"
~/.wudict/db/Cambridge English Dictionary Online/
  text.db
  res/
    jquery.js      replaces the dictionary's own copy
    js/entry.js    supplies a file the dictionary never had
    css/style.css  replaces the dictionary's stylesheet
```

Subfolders matter. Articles routinely ask for `js/…` and `css/…`, so mirror
that path exactly.

## Find the resource path

Open your browser's network panel and look for a request to
`/res/<dictionary_id>/<name>`. The `<name>` part is the path you must mirror. A
404 there is the missing file.

## Verify

Reload the article. Override files are served without caching, so an edit takes
effect on the next reload.

To go back to the original, delete your file. Nothing inside the dictionary was
ever changed.

## The one exception

A `.spx` file placed in `res/` is served as it is, not converted to WAV.
Browsers cannot play Speex, so supply `.mp3` or `.wav` instead.
