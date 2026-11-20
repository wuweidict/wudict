# Writing for wudict

These rules apply to the manual (`pages/docs`), `README.md`, the web UI, `wudict --help`
and the wudict howto. The app is intended for general public as well as lexicographers,
translators, linguists, developers. They want the exact term, the exact condition and
the exact default, and nothing that stands between them and the fact.

## Base: ASD-STE100

The base style is [ASD-STE100 Simplified Technical English](https://www.asd-ste100.org).
This guide extends it. Where the two differ, this guide takes precedence.

The STE rules applied most often:

- Procedural sentences: 20 words or fewer. Descriptive sentences: 25 words or fewer.
- One instruction per sentence. Instructions are imperative.
- Active voice. Simple tenses: present, past, future.
- Keep the articles (*a*, *an*, *the*). Noun clusters of 3 words or fewer.
- A vertical list in place of a long sentence with many items.
- Each word has one meaning. Use the approved meaning, not a synonym.

The [Terms](#terms) table is this project's set of STE technical names and technical
verbs: they are permitted, and only with the meaning given there. [Spelling](#form)
(rule 15) is Oxford English, also where an STE word list uses a different spelling.

## Terms

One name per concept, in every text the user reads. A different word tells the reader
it is a different thing.

| Use | Meaning | Do not use |
|---|---|---|
| **wudict** | the program, in prose (D27) | wuDict (except the Android app and the browser extension), WuWeiDict (except where the official name is required: title, licence, About) |
| **dictionary** | one dictionary, as wudict lists it | entry (for a dictionary), book |
| **dictionary files** | the files a dictionary consists of on disk: `.mdx` + `.mdd`, `.ifo` + `.idx` + `.dict.dz`, … | original(s), source, source files |
| **index** (verb), **indexed**, **indexing** | building the dictionary's copy in the library | prepare, ingest (except the `ingest` command), cache, import, build |
| **not indexed** | searched through the dictionary's own format | preview, cached, raw |
| **library** | the folder of all indexed dictionaries (`DB_DIR`, `~/.wudict/db`) | DB folder, DB dir, database, cache folder, internal databases |
| **library folder** | one dictionary's folder in the library (`text.db`, `media.db`, `info.txt`, `res/`) | index folder, prepared folder, cache folder |
| **headword index** | the index every indexed dictionary has: exact and prefix search | regular index, base index |
| **full-text index** | the index behind full-text search (FTS5) | FTS index (except in API and CLI reference), text index |
| **media pack** | `media.db`: the dictionary's images and audio, copied into the library | packed media, media index |
| **exact**, **prefix**, **full-text**, **wildcard** | the four search modes, as the API, the URL and the CLI spell them | starts with, contains (retired, D170), substring search, FTS mode, fuzzy, pattern search, glob |
| **wildcard** (character) | `?` (one character) or `*` (any number of characters) in a wildcard query | joker, placeholder, glob character |
| **phrase**, **proximity**, **anywhere** | the three stages of an unquoted full-text query | ladder, rung, widening, step |
| **headword** | the word an entry is filed under | keyword, key (except `wudict keys`), lemma (for a headword) |
| **article** | the text of one entry, as the dictionary formats it | definition (for the whole article), body, card |
| **lemmatization** | finding the lemma of an inflected form and searching for the lemma | morphology, stemming, word forms |
| **lemma**, **inflected form** | *know*, *knew* | dictionary form, base form, word form |
| **lemma data** | the per-language files lemmatization reads | lemma packs, word-form data, lemmatizers, morphology data |
| **resource** | a file a dictionary ships: stylesheet, script, image, audio, font | asset, media file (for all of them) |
| **resource override** | a file in a library folder's `res/` that wudict serves instead of the dictionary's own | patch, repair, fix, replacement |
| **dictionary panel** | the panel the <kbd>☰</kbd> button opens | ☰ panel, settings, the panel (on first mention) |
| **folder summary** | the row at the top of the dictionary panel (<kbd>⚙ 2 folders · 105 dictionaries</kbd>) and the actions it expands to | ⚙ box, cog menu, settings |
| **setup page** | `/setup` | settings page, configuration page |
| **dictionary picker** | the dictionary drop-down next to the search box | dropdown, selector |
| **group** | a set of dictionaries the picker offers, defined in `groups.ini` | facet, category, tag |
| **enabled** | included in *All dictionaries* searches | active, on, selected |
| **pinned** | placed at the top of the dictionary list, in the user's order; the others are listed A–Z | favourite, starred, my order |
| **favourite** | a word the user starred with ☆ next to the search box; never a dictionary | bookmark, saved word, starred |
| **history** | the words the user looked up, latest first | recent, recents, log |
| **random word** | a headword wudict draws at random from the indexed dictionaries of the search, shown under the empty search box (D202) | word of the day, surprise, lucky, discover, explore, shuffle, serendipity |
| **import** | installing dictionary files from a file or a link | download (for the whole act), add |
| **outdated** | indexed by an older version of wudict, or from dictionary files that have changed since | stale, old |
| **reindex** | rebuild an outdated index | refresh, rebuild (as the name of the act) |
| **access key** | the token a request from the network must carry | password, token (except `wudict token`), secret |
| **orphan** | a library folder whose dictionary files are gone | leftover, deleted externally |

## Register

1. **One page, one purpose.** A how-to page gives steps. A reference page states facts:
   synopsis, values, default, notes, example. Explanation of how wudict works lives on
   one concepts page.
2. **How-to pages are imperative.** "Open the dictionary panel." Not "You can open…",
   not "To do this, simply…".
3. **Reference pages are declarative, present tense.** The subject is the program, the
   setting or the file: "`AUTH` sets whether a request must carry the access key."
4. **State the rule, not its defence.** Give a reason only when the reader must decide
   something with it (D102). "Deleted on purpose", "by design", "deliberately" and "the
   safe reading" do not appear.
5. **Use the term, not a metaphor.** If the mechanism has a name (proximity search,
   operator precedence, quarantine attribute), use the name. Define a term once, in
   bold, where it is introduced.
6. **No figures of speech in place of facts.** No antithesis as a closing line ("a
   setting, not a fault"), no personification (wudict does not refuse, know, answer
   honestly or argue), no drama ("is not over", "Gone."), nothing addressed to the
   reader's identity ("the way a lexicographer writes them").
7. **Exact conditions.** "Within 10 words", "at least 3 characters", "1 MB to 90 MB".
   Every default is stated. Every number has a unit.
8. **Absolutes only when true and tested.** "Never", "always", "every" claim a guarantee;
   use them only for one the code makes.
9. **One fact per sentence.** Prefer a colon or a full stop to an em dash; use an em dash
   only around a parenthesis.
10. **Scope is explicit.** Name the platform or build a statement is limited to: "FOSS
    build", "Google Play build", "desktop", "Windows".

## Form

11. **UI text is quoted verbatim** in `<kbd>`, ellipsis included: <kbd>Edit folders…</kbd>.
    A path through the UI, wudict's or the operating system's, uses →:
    <kbd>☰</kbd> → folder summary → <kbd>Lemmatization…</kbd>;
    *Settings → Apps → wuDict → Storage*.
12. **Headings are names, not sentences or slogans:** "Unquoted queries", "Remove a
    dictionary", "AUTH".
13. **Admonitions have fixed meanings.** *warning*: data loss or exposure. *note*: a
    limit of scope or platform. *tip*: an optional shortcut. A collapsed box (`???`)
    may hold secondary detail most readers skip: internals, measurements, rationale.
    Values, defaults and the facts a reader needs to act are never inside a box.
14. **Examples are real and runnable.** Paths: `~/Dictionaries`, `~/.wudict/db/Oxford`.
    Dictionary: `Oxford.mdx`. Commands are copy-pasteable as shown.
15. **Spelling: Oxford** (en-GB-oxendict): colour, behaviour, catalogue, flavour, centre,
    licence (noun) and license (verb); organize, recognize, normalize, customize;
    analyse. Computing terms: program, disk, dialog.
16. **Sizes are decimal SI:** kB, MB, GB (1 MB = 1,000,000 bytes), as wudict prints them.

## UI strings

17. Sentence case for labels and messages. A label that opens a page or a dialog ends
    with "…".
18. A message starts with a capital letter. A single-sentence status line has no full
    stop. Errors read "Could not *verb*: *reason*".
19. A destructive confirmation names what is deleted, its size, and that the deletion is
    permanent. No emoji conveys emotion.
20. A tooltip states the effect of the control in one sentence. It explains a
    mechanism only when the user decides with it (D102).
