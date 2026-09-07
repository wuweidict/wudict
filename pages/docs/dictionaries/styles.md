---
title: Custom styles
description: Two CSS files of your own — one for wuDict itself, one for what dictionaries render — with examples for sepia, dark mode and a compact mobile layout.
---

# Custom styles

**Goal:** make wuDict look the way you want it, and make every dictionary
respect that — a sepia page, a wider column, or a phone layout that is not
half empty margin.

Two files, both optional, both plain CSS:

| File | Styles |
|---|---|
| `app.css` | wuDict itself — its colours, its own layout |
| `article.css` | what dictionaries render, in every article |

They live in a `style/` folder beside the `wudict.toml` in effect, usually
`~/.wudict/style/`. Nothing is created until you save something.

## The editor

Open the <kbd>☰</kbd> panel, then <kbd>**Custom styles…</kbd>** under the folder summary. It is a
sheet docked at the bottom rather than a window over the page, so the article
you are adjusting stays visible and reflows as you type.

- Two tabs over one box: **App** and **Article**.
- **Examples…** drops a working snippet at the cursor. It is text, not
  a switch — edit it, keep half of it, delete it. One menu serves both tabs:
  each example knows which box it belongs in, and a few need both (a colour a
  dictionary paints over, a width a dictionary caps). Those insert two halves
  and the view follows the second one, so nothing arrives unseen. Picking the
  same example twice does not duplicate it.
- Typing previews. **Save** writes the files.
- A dot on a tab means that box holds something.

## Undo, remove, disable

| You want | Do this |
|---|---|
| Take back what you just typed or inserted | <kbd>⌘Z</kbd> / <kbd>Ctrl</kbd>+<kbd>Z</kbd> in the box, as in any text field |
| Discard everything since the last Save | Close the sheet — the page goes back to what the files say, and your text is still in the box when you reopen |
| Delete a customization for good | **Clear**, then **Save**. The file is removed, not left empty |
| Turn everything off for one page load | Open `/?style=off` |

**Clear** appears only when the box has something in it, and it does not touch
the disk on its own — **Save** is still what commits, and <kbd>⌘Z</kbd> brings
the text back until then.

You can edit the same two files in any text editor instead. They are served
uncached, so an external edit takes effect on the next reload.

## Which box

> **App** is the page. **Article** is what dictionaries render.

The split is not bookkeeping. `body`, `p`, `a` and `table` are exactly the
selectors you reach for when styling a definition — and every one of them would
also hit wuDict's own interface. Two boxes make that impossible.

The rule that decides every example we ship, and every rule you write:

| What you are writing | Box |
|---|---|
| A `--token` | **App** — custom properties are the one thing that crosses into an article, so one line recolours the page and the definitions together |
| A selector naming wuDict's own furniture (`.col`, `details.dict`) | **App** |
| A selector naming dictionary markup (`body`, `p`, `table`) | **Article** — where it cannot reach the interface |
| A look that needs both | one example, **two halves** |

Colours are the common case, and they need no duplication: custom properties
set on `:root` in **App** are inherited by articles as well.

``` css title="App — one block, both layers"
html:not([data-dark]){
  --bg:#f4ecd8; --bg-card:#faf3e3; --bg-bar:rgba(244,236,216,.92);
  --fg:#3b3229; --fg-soft:#6b5f4e; --line:#e0d5bd; --line-soft:#ebe2cf;
  --wd-article-bg:#faf3e3; --wd-article-fg:#3b3229;
}
```

`--wd-article-bg` and `--wd-article-fg` are the article surface; the rest are
wuDict's own tokens.

Some dictionaries paint their own white background over that, and no token can
reach a colour a dictionary set on its own markup. That is the second half the
colour examples insert:

``` css title="Article — let the app's colours through"
:host, :root > body,
:host > *, :root > body > * { background:none !important }
```

Two levels deep only, so a coloured box inside an entry keeps its colour.

## What the examples do

| Example | Box | For |
|---|---|---|
| Sepia | App + Article | warm paper in light mode, page and definitions together |
| High contrast | App + Article | near-black on a warm off-white, not a blinding `#fff` |
| True black | App | black chrome for OLED, white text in articles |
| Warm dark | App | the default dark, warmed |
| Wider column | App + Article | a wider reading column, and dictionaries that cap their own width |
| Compact | App + Article | reclaim desktop side padding on a phone, and make what is left fit |
| Quieter results | App | fewer counts, badges and borders around the definitions |
| Roomier lines | Article | prose leading and air between paragraphs |
| Serif font | App + Article | one family everywhere, over the dictionary's own |
| Bolder text | App + Article | synthetic weight where the type looks washed out |
| Justified text | Article | justification with hyphens, so it opens no rivers |
| Wide tables | Article | conjugation tables scroll inside themselves |

## Dark mode

wuDict is dark when you choose dark, and when you leave it on *auto* and your
system is dark. You do not have to write that twice:

``` css
html[data-dark]      { /* dark, however it got there */ }
html:not([data-dark]){ /* light */ }
```

`[data-dark]` is set on the page, on an article and inside a script-bearing
article's frame, so it means the same thing in both boxes. In **Article**,
match both flavours at once:

``` css
:host([data-dark]), html[data-dark] { … }
```

!!! note "Article colours are written for light"

    In dark mode wuDict inverts the whole article rather than recolouring it,
    because a dictionary's own stylesheet has no dark variant to use. So write
    article colours for light and let dark be derived. `[data-dark]` in the
    Article box is for exempting something from that inversion, not for
    picking dark colours.

    The same goes for the two article tokens under `html[data-dark]` in the
    App box: they are the value *before* the flip. `--wd-article-bg:#fff`
    there is what gives you a near-black article, and `--wd-article-fg:#000`
    is what gives you white text. Hue survives, so a warm off-white lands as
    a warm near-black.

## Reclaim the margins on a phone

The common complaint: a dictionary designed for a desktop window reserves side
padding that leaves a phone with a narrow column of text between two empty
strips. There is no way to *clamp* an unknown padding, so remove it below a
width you choose.

``` css title="Article — compact view"
@media (max-width: 700px){
  :host, :root > body { margin-inline:0 !important; padding-inline:0 !important }
  div, section, article, main, header, footer, aside, nav,
  p, h1, h2, h3, h4, h5, h6, figure, blockquote, pre, dl, dt, table
    { margin-inline:0 !important; padding-inline:0 !important }
  ul, ol, dd, menu
    { margin-inline:0 !important; padding-inline-start:1.1em !important }
  td, th { padding-inline:.25em !important }

  :host, :root > body { width:auto !important; max-width:100% !important;
    overflow-x:hidden }
  :host *, :root > body *
    { max-width:100% !important; min-width:0 !important;
      box-sizing:border-box !important; overflow-wrap:break-word }
  div, section, article, main, header, footer, aside, nav,
  p, h1, h2, h3, h4, h5, h6, figure, blockquote, dl, dt, dd, ul, ol
    { width:auto !important; float:none !important }
  table { display:block !important; overflow-x:auto !important;
    border-collapse:collapse }
  table * { max-width:none !important }
  pre { overflow-x:auto !important; white-space:pre-wrap }
  img, video, svg { height:auto !important }
}
```

`:host` is the root of a plain article; `:root > body` is the root of one that
came with scripts and therefore renders in its own frame. Writing both covers
every dictionary.

Only the *root* needs that prefix. Everything below it can be named plainly —
this box only ever reaches article markup, so a bare `div` here cannot touch
wuDict's interface.

Lists are the side space people notice last: a browser indents `ul`, `ol` and
`dd` by 40px of its own, on top of anything the dictionary adds. `1.1em` is
what keeps the bullet inside the article instead of clipped off its edge; for
flush text use `0` together with `list-style-position:inside`. Table cells hide
width the same way.

The second half is what makes the result actually *fit*. Removing padding is
not enough on its own: a stylesheet written for a desktop window also states
widths the phone does not have, and it states them in `em` as often as in `px`
— which is why the sideways drag gets **worse** as you raise the font size, a
40em column being 40em wide whatever the screen is. So nothing is allowed to
declare its own width, nothing may exceed the box it is in, and long words
break instead of pushing. `box-sizing:border-box` is what makes those two
agree, since a padded 100% box is wider than 100% under the `content-box`
default a dictionary may have set.

Two things genuinely need their own width and scroll inside themselves
instead: a table of forms and a block of code. The table's own box stays at
100% — it is the window — while its cells are let back out of the clamp, or a
ten-column table becomes ten slivers rather than a table you can push
sideways.

The example's other half zeroes wuDict's own gutter around the article — the
column padding, the definition list and the card — because on a phone that is
the same complaint.

## How it relates to `res/`

[Patching a dictionary](override.md) *replaces* one file a single dictionary
ships. Custom styles are *added* after whatever the dictionary brought, for
every dictionary at once. Both can be in effect; yours is applied last, and
`!important` settles anything it still loses.

## If a rule hides the app

`* { display:none }` in the App box hides the editor that would undo it. Open

``` text
http://127.0.0.1:6888/?style=off
```

and wuDict serves the page with neither file applied. The editor still works
there — it saves, but does not preview — so you can fix the rule and reload
normally.
