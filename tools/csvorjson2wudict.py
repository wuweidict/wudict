#!/usr/bin/env python3
"""Build a wudict library folder (<dir>/{text.db, media.db, info.txt}) from a
pyglossary CSV, a pyglossary JSON, a `wudict dump` CSV, or a native JSON spec.

The schema written here is the one internal/store/ingest.go builds and
internal/store/store.go validates: user_version=1, meta/entry/alias +
entry_fts. Bodies are DEFLATE-compressed per row
behind a 0x00 sentinel exactly as store.encodeBody does (internal/store/
compress.go); --no-compress stores them as plain TEXT instead. Both forms are
legal in the same table, because the read path discriminates per row.

INPUT DIALECTS (auto-detected; force with --dialect)

  csv        pyglossary's CSV writer and `wudict dump`, which writes the same
             dialect on purpose (internal/cli/dump.go). Columns, per
             pyglossary/plugins/csv_plugin/writer.py:

                 word, definition [, defiFormat] [, "alt1,alt2"]

             defiFormat is present only when the file was written with
             add_defi_format=true; it is "h" (html), "m" (plain text) or "x"
             (xdxf). pyglossary's OWN reader ignores that possibility and reads
             alternates from column 2 regardless, so a 4-column file
             round-trips through pyglossary with the format marker as a fake
             alias. Here column 2 is read as defiFormat only when a 4th column
             exists, or when --defi-format=yes says so.

             Leading rows whose first cell starts with "#" are info
             ("#name", "#description", "#sourceLang", …) and are consumed until
             the first entry row, which is precisely the reader's rule.
             enable_info=false simply means there are none.

  pyglossary-json
             pyglossary's JSON writer: one flat object, key = entry term,
             value = definition, info keys prefixed "##". Two encodings ride in
             that key and are decoded here:

               * ALTERNATES. The key is entry.s_term - the headword and its
                 alternates joined by "|", with "\\" and "|" backslash-escaped
                 (pyglossary/text_utils.py joinByBar). --no-bar-alts keeps the
                 key whole for a dictionary whose headwords contain literal
                 bars and no alternates.
               * DUPLICATE DISAMBIGUATION. The writer calls
                 preventDuplicateWords(), so the second entry with the same
                 term is stored as "term (2)", the third as "term (3)"
                 (entry_filters.PreventDuplicateTerms). A text.db has no such
                 restriction, so the suffix is stripped; --keep-dup-suffix
                 keeps it for a glossary whose headwords really end that way.

             The final '"": ""' the writer emits as its tail is an empty
             headword and is dropped like any other.

  native-json
             this tool's own spec, unchanged:

               {"name": …, "description": …, "format": …,
                "index_lang": …, "contents_lang": …,
                "entries": [{"w": …, "body": …, "aliases": [...], "text": bool}]}

RESOURCES

pyglossary writes data entries (stylesheets, images, audio) to "<file>_res"
beside the CSV/JSON, and `wudict dump` writes "<file>.csv_res" for the same
reason. That folder is packed into media.db and stamped with the same
dict_uuid as text.db, which is what pairs the two (store.mediaDB). Article
references like <link rel="stylesheet" href="style.css"> are then resolved at
serve time through /res/{id}/… (internal/server/rewrite.go), so a converted
AppleDict keeps its stylesheet.

FULL HTML DOCUMENTS

Several pyglossary writers emit one complete <!DOCTYPE html>…</html> document
per entry. --unwrap (default) reduces each to the inner HTML of <body>, with
the head's stylesheet <link>s kept in front of it - the same transform the ZIM
backend applies (internal/format/zim/article.go articleBody), and for the same
reason: the page shell is per-entry dead weight and the stylesheet link is not.

BROKEN HEADWORD COLUMNS

Some conversion chains (AppleDict binaries in particular) lose the headword and
leave an ordinal in its place while the real word survives as <h1> in the
article. --headword-from h1 takes the headword from the first such element
instead of from the column. The run warns by itself when the headwords it wrote
are degenerate, because a dictionary with eleven distinct keys is not a
dictionary.

Usage:
  python3 csvorjson2wudict.py INPUT [-o OUTPUT] [--fulltext] [--force]
  python3 csvorjson2wudict.py --demo [-o OUTPUT]

OUTPUT (resolved like `go build -o`; <name> is the input's file stem):
  omitted                     ./<name>/
  an existing library folder  that folder, rebuilt in place (needs --force)
  DIR/ or an existing DIR     DIR/<name>/ (DIR/ is created if missing)
  anything else               that path is the library folder; its parent must exist
"""

import argparse
import csv
import html
import json
import mimetypes
import os
import re
import secrets
import sqlite3
import sys
import tempfile
import zlib
from datetime import datetime, timezone

SCHEMA_VERSION = 1   # a schema-1 text.db: DEFLATE and literal rows, read by every wudict (internal/store/compress.go)
MARKUP_VERSION = 2   # artmark/artmark.go:83
COMPRESS_MARK = 0x00  # store/compress.go:31
COMPRESS_MIN = 120    # store/compress.go:73 - shorter than deflate's overhead
COMPRESS_LEVEL = 6    # store/compress.go: level 6 off-device, 1 on android
PROGRESS_EVERY = 50_000

# pyglossary raises this in its own plugin; a single article can exceed the
# 128 KiB default by an order of magnitude.
csv.field_size_limit(0x7FFFFFFF)

DEMO = {
    "name": "Minimal Example",
    "description": "<p>A three-word dictionary written by hand.</p>"
                   "<p>Licence: <a href=\"https://creativecommons.org/licenses/by-sa/4.0/\">CC BY-SA 4.0</a>.</p>",
    "format": "native",
    "index_lang": "en",
    "entries": [
        {"w": "alpha", "body": "<p><b>alpha</b> — the first letter of the Greek alphabet.</p>",
         "aliases": ["α", "Alpha"]},
        {"w": "beta", "body": "<p><b>beta</b> — the second letter; also a pre-release.</p>"},
        {"w": "gamma", "body": "gamma — the third letter.\nAlso a function.", "text": True},
    ],
}

TAG = re.compile(r"<[^>]*>")
SKIP = re.compile(r"(?is)<(script|style)\b.*?</\1\s*>")
LINK_TAG = re.compile(r"(?is)<link\b[^>]*>")
DUP_SUFFIX = re.compile(r" \(\d+\)$")
# pyglossary/text_utils.py:82-83, copied verbatim so the split agrees with the
# writer that produced the key.
BAR_SPLIT = re.compile(r"(?:(?<!\\)(?:\\\\)*)\|")
BAR_UNESCAPE = re.compile(r"((?<!\\)(?:\\\\)*)\\\|")

# Types pyglossary and MDict repacks ship that the stdlib map misses or gets
# wrong. Everything else falls through to mimetypes.
EXTRA_MIME = {
    ".spx": "audio/ogg", ".opus": "audio/ogg", ".oga": "audio/ogg",
    ".webp": "image/webp", ".avif": "image/avif", ".woff": "font/woff",
    ".woff2": "font/woff2", ".ttf": "font/ttf", ".otf": "font/otf",
    ".m4a": "audio/mp4", ".webm": "video/webm",
}


# ---------------------------------------------------------------- primitives

def strip_html(s):
    """store.StripHTML (store/strip.go): tags out, script/style contents out,
    entities decoded, whitespace collapsed."""
    return " ".join(html.unescape(TAG.sub(" ", SKIP.sub(" ", s))).split())


def encode_body(s, compress):
    """store.encodeBody (store/compress.go:72). Raw DEFLATE behind a NUL byte,
    and only when it actually wins; anything else is stored literally."""
    if not compress or len(s) < COMPRESS_MIN:
        return s
    raw = s.encode("utf-8")
    co = zlib.compressobj(COMPRESS_LEVEL, zlib.DEFLATED, -zlib.MAX_WBITS)
    blob = bytes([COMPRESS_MARK]) + co.compress(raw) + co.flush()
    if len(blob) >= len(raw):
        return s
    return blob


def article_body(s):
    """zim.articleBody (format/zim/article.go:48): inner HTML of <body>, with
    the head's stylesheet links kept in front. A document with no <body> is
    returned whole rather than emptied."""
    lower = s.lower()
    i = lower.find("<body")
    if i < 0:
        return s
    j = s.find(">", i)
    if j < 0:
        return s
    start = j + 1
    end = lower.rfind("</body>")
    if end < start:
        end = len(s)
    styles = "".join(t for t in LINK_TAG.findall(s[:i])
                     if "stylesheet" in t.lower())
    return styles + s[start:end]


def normalize_body(body, as_text):
    """store.NormalizeBody: HTML verbatim; plain text escaped and wrapped."""
    if not as_text:
        return body
    esc = body.replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;")
    return "<p>" + esc.replace("\n", "<br/>") + "</p>"


def split_by_bar(s):
    """pyglossary/text_utils.py splitByBar: split on unescaped '|', then
    unescape '\\|' and '\\\\' in each part."""
    return [BAR_UNESCAPE.sub(r"\1|", p).replace("\\\\", "\\")
            for p in BAR_SPLIT.split(s)]


def require_sqlite():
    db = sqlite3.connect(":memory:")
    try:
        db.execute("CREATE VIRTUAL TABLE p USING fts5(a)")
    except sqlite3.OperationalError:
        raise SystemExit("this Python's SQLite has no FTS5; FTS5 is mandatory (D29)")
    db.close()


# ------------------------------------------------------------------- readers
#
# Every reader returns (meta, entries) where meta is a dict of info keys the
# source declared and entries yields (words, body, defi_format). words[0] is
# the headword, words[1:] are alternates, defi_format is "h"/"m"/"x" or None.

def sniff_dialect(path, forced):
    if forced != "auto":
        return forced
    ext = os.path.splitext(path)[1].lower()
    with open(path, "rb") as f:
        head = f.read(4096).lstrip()
    if head[:1] in (b"{", b"["):
        # A native spec is an object carrying "entries"; pyglossary's is a flat
        # term->definition map. The probe is bounded to the first 4 KiB, which
        # is where the key would be in a file this tool wrote.
        return "native-json" if b'"entries"' in head[:4096] else "pyglossary-json"
    if ext == ".json":
        raise SystemExit(f"{path}: .json that does not start with '{{' or '['")
    return "csv"


def sniff_delimiter(path, encoding, forced):
    """pyglossary's delimiter= is free-form; ',' ';' '@' are its presets. The
    file itself answers the question: the right delimiter is the one that makes
    the first row more than one field."""
    if forced:
        return forced
    with open(path, encoding=encoding, errors="replace", newline="") as f:
        sample = f.read(1 << 20)
    for d in (",", ";", "\t", "|", "@"):
        try:
            row = next(csv.reader(sample.splitlines(True), delimiter=d), None)
        except csv.Error:
            continue
        if row and len(row) >= 2:
            return d
    return ","


def read_csv(path, a):
    delim = sniff_delimiter(path, a.encoding, a.delimiter)
    f = open(path, encoding=a.encoding, errors="replace" if a.lenient else "strict",
             newline="")
    reader = csv.reader(f, dialect="excel", delimiter=delim)
    meta = {}
    first = None
    # Info block: leading rows only, exactly as csv_plugin/reader.py open()
    # consumes them. A later "#…" row is a headword that starts with a hash.
    for row in reader:
        if not row:
            continue
        if not a.no_info_rows and row[0].startswith("#"):
            if len(row) >= 2:
                meta[row[0].lstrip("#")] = row[1]
            continue
        first = row
        break

    def rows():
        try:
            if first:
                yield first
            for row in reader:
                if row:
                    yield row
        finally:
            f.close()

    def decoded():
        for row in rows():
            if len(row) < 2:
                continue                      # reader.py logs and drops these
            words = [row[0]]
            fmt = None
            rest = row[2:]
            if len(rest) >= 2:                # word, defi, defiFormat, alts
                fmt, alts = rest[0], rest[1]
            elif len(rest) == 1:
                if a.defi_format == "yes":
                    fmt, alts = rest[0], ""
                else:
                    alts = rest[0]
            else:
                alts = ""
            if alts:
                words += alts.split(",")
            yield words, row[1], fmt

    return meta, decoded()


_DEC = json.JSONDecoder()


def _json_lines(f):
    """pyglossary's JSON writer emits exactly one entry per line, both halves
    json.dumps'd, so the file streams at constant memory however large it is.
    Anything that does not match that shape aborts to the whole-file parser."""
    for line in f:
        s = line.strip()
        if not s or s in ("{", "}", "},"):
            continue
        if not s.startswith('"'):
            raise ValueError(f"not a pyglossary JSON line: {s[:60]!r}")
        key, i = _DEC.raw_decode(s)
        rest = s[i:].lstrip()
        if not rest.startswith(":"):
            raise ValueError(f"missing ':' after key {key[:40]!r}")
        rest = rest[1:].lstrip()
        val, j = _DEC.raw_decode(rest)
        if rest[j:].strip() not in ("", ","):
            raise ValueError(f"trailing junk after value of {key[:40]!r}")
        yield key, val


def read_pyglossary_json(path, a):
    meta = {}
    if a.json_load:
        with open(path, encoding=a.encoding) as f:
            doc = json.load(f)
        if not isinstance(doc, dict):
            raise SystemExit("pyglossary JSON must be an object")
        pairs = list(doc.items())
        head = pairs
    else:
        f = open(path, encoding=a.encoding, errors="replace" if a.lenient else "strict")
        stream = _json_lines(f)
        head = []
        try:
            for _ in range(8):                # validate the shape before committing
                head.append(next(stream))
        except StopIteration:
            pass
        except ValueError as e:
            f.close()
            raise SystemExit(f"{path}: {e}\nre-run with --json-load to parse the "
                             "whole file at once (needs RAM for the file)")

        def pairs_iter():
            try:
                yield from head
                yield from stream
            finally:
                f.close()
        pairs = pairs_iter()

    for k, v in head:
        if k.startswith("#") and isinstance(v, str):
            meta.setdefault(k.lstrip("#"), v)

    def decoded():
        for k, v in pairs:
            if k.startswith("#"):
                continue                       # info, already taken
            if not isinstance(v, str):
                v = "" if v is None else str(v)
            if not a.keep_dup_suffix:
                k = DUP_SUFFIX.sub("", k)
            words = split_by_bar(k) if not a.no_bar_alts else [k]
            yield words, v, None

    return meta, decoded()


def read_native_json(path, a):
    with open(path, encoding=a.encoding) as f:
        spec = json.load(f)
    if not isinstance(spec, dict):
        raise SystemExit("spec must be a JSON object")
    return native_spec(spec)


def native_spec(spec):
    meta = {k: str(spec.get(k) or "") for k in
            ("name", "description", "format", "index_lang", "contents_lang")
            if spec.get(k)}
    rows = spec.get("entries") or []
    if not isinstance(rows, list) or not rows:
        raise SystemExit("no entries")

    def decoded():
        for e in rows:
            if not isinstance(e, dict):
                raise SystemExit("each entry must be an object")
            body = e.get("body", "")
            if not isinstance(body, str):
                raise SystemExit(f"entry {e.get('w')!r}: body must be a string")
            words = [str(e.get("w", ""))]
            words += [str(x) for x in (e.get("aliases") or [])]
            yield words, body, ("m" if e.get("text") else "h")

    return meta, decoded()


# -------------------------------------------------------------------- build

def build(db, meta, entries, a, source_name):
    db.executescript(f"""
        PRAGMA user_version = {SCHEMA_VERSION};
        PRAGMA journal_mode = OFF;
        PRAGMA synchronous = OFF;
        PRAGMA cache_size = -32000;
        CREATE TABLE meta(key TEXT PRIMARY KEY, value TEXT);
        CREATE TABLE entry(id INTEGER PRIMARY KEY, w TEXT NOT NULL, m TEXT NOT NULL);
        CREATE TABLE alias(w TEXT NOT NULL, entry_id INTEGER NOT NULL REFERENCES entry(id));
        CREATE VIRTUAL TABLE entry_fts USING fts5(
            w, txt, content='', columnsize=0,
            tokenize='unicode61 remove_diacritics 2');
    """)

    hw_re = None
    if a.headword_from:
        t = re.escape(a.headword_from)
        hw_re = re.compile(rf"<{t}\b[^>]*>(.*?)</{t}\s*>", re.I | re.S)
    title_re = re.compile(r"(?is)^\s*<h1\b[^>]*>(.*?)</h1\s*>")

    eid = sub = rescued = xdxf = 0
    for words, raw, fmt in entries:
        if a.limit and eid >= a.limit:
            break
        body = article_body(raw) if a.unwrap else raw

        w = words[0].strip() if words else ""
        if hw_re:
            m = hw_re.search(body)
            if m:
                cand = " ".join(html.unescape(TAG.sub(" ", m.group(1))).split())
                if cand:
                    w, rescued = cand, rescued + 1
        if not w:
            continue                          # ingest.go skips empty headwords

        if a.strip_word_title:
            m = title_re.match(body)
            if m and " ".join(html.unescape(TAG.sub(" ", m.group(1))).split()) == w:
                body = body[m.end():]

        if a.body == "text":
            as_text = True
        elif a.body == "html":
            as_text = False
        else:
            as_text = fmt == "m"
        if fmt == "x":
            xdxf += 1                          # XDXF is not HTML; passed through
        body = normalize_body(body, as_text)

        eid += 1
        db.execute("INSERT INTO entry(id, w, m) VALUES(?,?,?)",
                   (eid, w, encode_body(body, a.compress)))
        db.execute("INSERT INTO entry_fts(rowid, w, txt) VALUES(?,?,?)",
                   (eid, w, strip_html(body) if a.fulltext else ""))
        if len(w) > 1 and w[0] == "@":        # MDict sub-entry: hidden from browsing
            sub += 1
        seen = {w}
        for alt in words[1:]:
            alt = alt.strip()
            if not alt or alt in seen:
                continue
            seen.add(alt)
            db.execute("INSERT INTO alias(w, entry_id) VALUES(?,?)", (alt, eid))
        if eid % PROGRESS_EVERY == 0:
            print(f"  {eid:,} entries…", file=sys.stderr, flush=True)

    if eid == 0:
        raise SystemExit("no entry had a headword")

    # The collation the query planner needs: Exact compares COLLATE NOCASE.
    db.execute("CREATE INDEX idx_entry_w ON entry(w COLLATE NOCASE)")
    db.execute("CREATE INDEX idx_alias_w ON alias(w COLLATE NOCASE)")

    out = {
        "dict_uuid": secrets.token_hex(16),     # 16 bytes; pairs text.db with media.db
        "name": str(meta.get("name") or source_name),
        "description": str(meta.get("description") or ""),
        "format": str(meta.get("format") or a.format),
        "entry_count": str(eid - sub),
        "sub_entries": str(sub),
        "ingest_level": "text" if a.fulltext else "headwords",
        "markup_version": str(MARKUP_VERSION),
        "body_encoding": "deflate" if a.compress else "plain",
        "created": datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
        # source_path deliberately absent: an existing path would let that
        # format's About provider shadow `description` (server/about.go).
    }
    for key, src in (("index_lang", "sourceLang"), ("contents_lang", "targetLang")):
        v = str(meta.get(key) or meta.get(src) or "").strip()
        if v:
            out[key] = v
    db.executemany("INSERT INTO meta(key, value) VALUES(?,?)", sorted(out.items()))
    return out, {"rescued": rescued, "xdxf": xdxf, "rows": eid}


def finish_indexes(db):
    db.execute("INSERT INTO entry_fts(entry_fts) VALUES('optimize')")
    db.execute("ANALYZE")
    db.execute("PRAGMA optimize")


def distinct_headwords(db):
    return db.execute("SELECT count(*) FROM (SELECT 1 FROM entry GROUP BY w)").fetchone()[0]


# -------------------------------------------------------------------- media

def build_media(res_dir, path, uuid, name, fmt):
    """Pack <input>_res into a media.db paired to text.db by dict_uuid
    (store.IngestMedia, store/media.go:184)."""
    files = []
    for root, _, names in os.walk(res_dir):
        for n in names:
            full = os.path.join(root, n)
            rel = os.path.relpath(full, res_dir).replace(os.sep, "/")
            files.append((rel, full))
    if not files:
        return 0, 0
    tmp = path + f".{os.getpid()}.tmp"
    if os.path.exists(tmp):
        os.unlink(tmp)
    total = 0
    try:
        db = sqlite3.connect(tmp)
        try:
            with db:
                db.executescript(f"""
                    PRAGMA user_version = {SCHEMA_VERSION};
                    PRAGMA journal_mode = OFF;
                    PRAGMA synchronous = OFF;
                    CREATE TABLE meta(key TEXT PRIMARY KEY, value TEXT);
                    CREATE TABLE resource(name TEXT PRIMARY KEY, mime TEXT, data BLOB);
                """)
                db.executemany("INSERT INTO meta(key, value) VALUES(?,?)",
                               [("dict_uuid", uuid), ("name", name), ("format", fmt)])
                for rel, full in sorted(files):
                    ext = os.path.splitext(rel)[1].lower()
                    mime = EXTRA_MIME.get(ext) or mimetypes.guess_type(rel)[0] \
                        or "application/octet-stream"
                    with open(full, "rb") as fh:
                        data = fh.read()
                    total += len(data)
                    db.execute("INSERT OR IGNORE INTO resource(name, mime, data) "
                               "VALUES(?,?,?)", (rel, mime, data))
        finally:
            db.close()
        os.replace(tmp, path)
    except BaseException:
        if os.path.exists(tmp):
            os.unlink(tmp)
        raise
    return len(files), total


# --------------------------------------------------------------------- info

def human_size(n):
    for unit in ("B", "KB", "MB", "GB"):
        if n < 1024 or unit == "GB":
            return f"{n:.0f} {unit}" if unit == "B" else f"{n:.1f} {unit}"
        n /= 1024.0


def write_info(outdir, meta, source, media_bytes):
    """Same shape store.WriteInfo regenerates (store/library.go:466), so the
    receipt does not change the first time wudict rewrites it."""
    level = ("headwords only (exact · prefix · wildcard)"
             if meta["ingest_level"] == "headwords"
             else "full text (exact · prefix · wildcard · full-text)")
    media = ("not packed - resources come from the original files"
             if media_bytes is None else f"media.db ({human_size(media_bytes)})")
    lines = [
        "# wudict - prepared dictionary",
        "# This folder is one dictionary. Copy, move or zip it as a unit;",
        "# drop it into your dictionary folder to use it on another machine.",
        "# Regenerated automatically - edits are overwritten.",
        "",
        f"name = {meta['name']}",
        f"format = {meta['format']}",
        f"entries = {meta['entry_count']}",
        f"index = {level}",
        f"media = {media}",
    ]
    if meta.get("index_lang"):
        lines.append(f"language = {meta['index_lang']}")
    lines += [
        f"source = {source}",
        "source_size = ",
        "source_mtime = ",
        f"imported = {meta['created']}",
        f"uuid = {meta['dict_uuid']}",
        "",
        f"# origin: {source or '(unknown)'}",
        "# files: text.db (articles + search index)"
        + (", media.db (images/audio)" if media_bytes is not None else "")
        + ", info.txt (this receipt)",
        "",
    ]
    with open(os.path.join(outdir, "info.txt"), "w", encoding="utf-8") as f:
        f.write("\n".join(lines))


# --------------------------------------------------------------------- main

def parse_args(argv):
    ap = argparse.ArgumentParser(
        description="build a wudict library folder from pyglossary CSV/JSON, "
                    "a `wudict dump` CSV, or a native JSON spec",
        formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("input", nargs="?", help="the .csv or .json to convert")
    ap.add_argument("-o", "--output", metavar="PATH",
                    help="library folder to write, or an existing folder / DIR/ to "
                         "write <name>/ into (default: ./<name>/)")
    ap.add_argument("--demo", action="store_true", help="ignore INPUT, write the 3-entry example")

    g = ap.add_argument_group("input dialect")
    g.add_argument("--dialect", choices=("auto", "csv", "pyglossary-json", "native-json"),
                   default="auto", help="default: auto-detect from the first bytes")
    g.add_argument("--encoding", default="utf-8-sig",
                   help="pyglossary's encoding= option (default utf-8-sig: eats a BOM)")
    g.add_argument("--lenient", action="store_true",
                   help="replace undecodable bytes instead of failing")
    g.add_argument("--delimiter", default=None,
                   help="CSV delimiter= (default: detected from the first row)")
    g.add_argument("--defi-format", choices=("auto", "yes", "no"), default="auto",
                   help="was add_defi_format=true? auto: yes when a 4th column exists")
    g.add_argument("--no-info-rows", action="store_true",
                   help="do not read leading '#key' rows as metadata")
    g.add_argument("--no-bar-alts", action="store_true",
                   help="pyglossary JSON: keep '|' in keys instead of splitting alternates")
    g.add_argument("--keep-dup-suffix", action="store_true",
                   help="pyglossary JSON: keep the ' (2)' duplicate disambiguator")
    g.add_argument("--json-load", action="store_true",
                   help="parse the whole JSON at once (needed for reformatted files)")

    g = ap.add_argument_group("article handling")
    g.add_argument("--no-unwrap", dest="unwrap", action="store_false",
                   help="keep whole <html> documents instead of their <body>")
    g.add_argument("--headword-from", metavar="TAG",
                   help="take the headword from the first <TAG> of the article "
                        "(e.g. h1) when the column holds an id")
    g.add_argument("--strip-word-title", action="store_true",
                   help="drop a leading <h1>headword</h1> (pyglossary word_title=true)")
    g.add_argument("--body", choices=("auto", "html", "text"), default="auto",
                   help="auto: HTML unless defiFormat says 'm' (plain text)")
    g.add_argument("--no-media", dest="media", action="store_false",
                   help="do not pack the <input>_res folder into media.db")
    g.add_argument("--res", metavar="DIR", help="resource folder (default: <input>_res)")

    g = ap.add_argument_group("output")
    g.add_argument("--fulltext", action="store_true", help="index article text (full-text mode)")
    g.add_argument("--no-compress", dest="compress", action="store_false",
                   help="store bodies as plain text (bigger, marginally faster to read)")
    g.add_argument("--name", help="override the dictionary title")
    g.add_argument("--description", help="override the About text")
    g.add_argument("--index-lang", help="language of the headwords, e.g. ro")
    g.add_argument("--contents-lang", help="language of the articles")
    g.add_argument("--format", default="native", help="free label recorded in meta.format")
    g.add_argument("--limit", type=int, default=0, help="stop after N entries (trial runs)")
    g.add_argument("--force", action="store_true", help="overwrite an existing text.db")

    a = ap.parse_args(argv)
    if not a.demo and not a.input:
        ap.error("INPUT is required (or pass --demo)")
    return a


def resolve_output(spec, base):
    """--output -> the library folder to write (see OUTPUT in the module doc).

    An existing folder that already holds a text.db IS a library, so it is the
    target whatever the spelling: a trailing separator from shell completion must
    not nest a new library inside the one being rebuilt.
    """
    if base in ("", ".", ".."):
        raise SystemExit(f"cannot derive a folder name from {base!r}; pass -o PATH")
    if spec is None:
        return os.path.join(os.getcwd(), base)
    if not spec:
        raise SystemExit("--output is empty")
    raw = os.path.expanduser(spec)
    as_dir = raw.endswith(tuple(s for s in (os.sep, os.altsep) if s))
    path = os.path.abspath(raw)                      # drops the trailing separator
    if os.path.exists(path) and not os.path.isdir(path):
        raise SystemExit(f"{path}: not a directory")
    if os.path.isfile(os.path.join(path, "text.db")):
        return path
    if as_dir or os.path.isdir(path):
        try:
            os.makedirs(path, exist_ok=True)
        except OSError as e:
            raise SystemExit(f"cannot create {path}: {e}")
        return os.path.join(path, base)
    parent = os.path.dirname(path)
    if parent and not os.path.isdir(parent):
        raise SystemExit(f"{parent}: no such directory")
    return path


def main():
    a = parse_args(sys.argv[1:])
    require_sqlite()

    source = ""
    if a.demo:
        meta, entries = native_spec(DEMO)
        base = "Minimal Example"
        res_dir = None
    else:
        if not os.path.isfile(a.input):
            raise SystemExit(f"{a.input}: not a file")
        source = os.path.abspath(a.input)
        base = os.path.splitext(os.path.basename(a.input))[0]
        dialect = sniff_dialect(a.input, a.dialect)
        reader = {"csv": read_csv,
                  "pyglossary-json": read_pyglossary_json,
                  "native-json": read_native_json}[dialect]
        meta, entries = reader(a.input, a)
        res_dir = a.res or (a.input + "_res")
        if not a.media or not os.path.isdir(res_dir):
            res_dir = None
        print(f"{a.input}: {dialect}", file=sys.stderr)

    for key, val in (("name", a.name), ("description", a.description),
                     ("index_lang", a.index_lang), ("contents_lang", a.contents_lang)):
        if val:
            meta[key] = val

    a.outdir = resolve_output(a.output, base)
    if os.path.exists(a.outdir) and not os.path.isdir(a.outdir):
        raise SystemExit(f"{a.outdir}: not a directory")
    os.makedirs(a.outdir, exist_ok=True)
    final = os.path.join(a.outdir, "text.db")
    if os.path.exists(final) and not a.force:
        raise SystemExit(f"{final} exists (use --force)")

    fd, tmp = tempfile.mkstemp(prefix=".text.db.", dir=a.outdir)
    os.close(fd)
    os.unlink(tmp)
    try:
        db = sqlite3.connect(tmp)
        try:
            with db:
                out, stats = build(db, meta, entries, a, base)
            finish_indexes(db)
            distinct = distinct_headwords(db)
        finally:
            db.close()
        os.replace(tmp, final)                # atomic: no half-written text.db is seen
    except BaseException:
        if os.path.exists(tmp):
            os.unlink(tmp)
        raise

    media_bytes = None
    if res_dir:
        n, media_bytes = build_media(res_dir, os.path.join(a.outdir, "media.db"),
                                     out["dict_uuid"], out["name"], out["format"])
        if n == 0:
            media_bytes = None
        else:
            print(f"media.db: {n} resources, {human_size(media_bytes)}", file=sys.stderr)

    write_info(a.outdir, out, source, media_bytes)

    size = os.path.getsize(final)
    print(f"{final}: {out['entry_count']} entries, "
          f"{'full-text' if a.fulltext else 'headwords'}, {human_size(size)} — {out['name']}",
          file=sys.stderr)
    if stats["rescued"]:
        print(f"headwords taken from <{a.headword_from}>: {stats['rescued']:,}",
              file=sys.stderr)
    if stats["xdxf"]:
        print(f"warning: {stats['xdxf']:,} entries were marked defiFormat=x (XDXF); "
              "they are stored as-is and will render as plain text", file=sys.stderr)
    # A dictionary whose key column was lost in conversion is not searchable,
    # and nothing downstream can tell: it is a valid database of useless keys.
    if stats["rows"] >= 1000 and distinct * 20 < stats["rows"]:
        print(f"WARNING: {stats['rows']:,} entries share only {distinct:,} distinct "
              "headwords — the source's headword column is probably an id, not a "
              "word. Re-run with --headword-from h1 (or the tag that holds the "
              "real headword).", file=sys.stderr)


if __name__ == "__main__":
    main()
