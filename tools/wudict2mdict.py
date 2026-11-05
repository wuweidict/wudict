#!/usr/bin/env python3
"""Export a wudict prepared dictionary to Octopus MDict .mdx/.mdd.

Input is a wudict library folder (<dir>/{text.db, media.db, res/}), a text.db,
or a loose <name>.text.db - the format documented in pages/docs/reference/text-db.md.

Output is an MDict 2.0 pair: <out>.mdx (articles) and, when the dictionary has
media, <out>.mdd (resources). Article HTML is copied verbatim: a wudict library
stores a dictionary's ORIGINAL references (sound://x.mp3, pictures/lion.jpg) and
those are what MDict resolves - the /res/<id>/ form only ever exists at
serve time, so there is nothing to undo here.

Requires mdict-utils (pip install mdict-utils); --mdict-utils PATH points at a
source checkout instead. A dictionary stored with zstd (the default since D191)
also needs Python 3.14 or later, or the zstandard package (pip install zstandard).

Examples:
  tools/wudict2mdict.py ~/.wudict/db/OALD10
  tools/wudict2mdict.py ~/.wudict/db/OALD10 -o ~/out/          # ~/out/OALD10.mdx
  tools/wudict2mdict.py ~/.wudict/db/OALD10 -o ~/out/oald.mdx --no-media
  tools/wudict2mdict.py OALD10 --title 'OALD 10' \\
      --append-description 'Exported from WuWeiDict on 2026-09-21.' \\
      --meta Left2Right=Yes --meta StyleSheet=''
"""

import argparse
import datetime
import html
import os
import re
import shutil
import sqlite3
import sys
import tempfile
import unicodedata
import zlib
from collections import OrderedDict

# Row markers and schemas, as internal/store/compress.go writes them: 0x00 is raw
# DEFLATE; in schema 2, 0x01 is a zstd frame without its 4-byte magic, read with
# the raw dictionary in body_dict (id 0), and 0x02-0x08 are reserved.
DEFLATE_MARK, ZSTD_MARK, LAST_RESERVED = 0x00, 0x01, 0x08
ZSTD_MAGIC = b"\x28\xb5\x2f\xfd"
SCHEMAS = (1, 2)
TEXT_DB, MEDIA_DB, RES_DIR = "text.db", "media.db", "res"

# mdict-utils normalises the header encoding to one of these and encodes records
# with the matching python codec; record sizes are declared up front, so the two
# must agree byte for byte (utf-16 means utf_16_le, BOM-less).
ENCODINGS = {"UTF-8": "utf_8", "UTF-16": "utf_16_le", "GBK": "gbk", "BIG5": "big5"}


def die(msg):
    print("wudict2mdict: %s" % msg, file=sys.stderr)
    raise SystemExit(2)


def log(args, msg):
    if not args.quiet:
        print(msg, file=sys.stderr)


# --------------------------------------------------------------------------- input

def resolve_input(path):
    """Return (text_db, media_db|None, res_dir|None, default_base)."""
    path = os.path.abspath(os.path.expanduser(path))
    if os.path.isdir(path):
        text_db = os.path.join(path, TEXT_DB)
        if not os.path.isfile(text_db):
            die("no %s in %s" % (TEXT_DB, path))
        media_db = os.path.join(path, MEDIA_DB)
        res = os.path.join(path, RES_DIR)
        return text_db, media_db if os.path.isfile(media_db) else None, \
            res if os.path.isdir(res) else None, os.path.basename(path.rstrip(os.sep))
    if not os.path.isfile(path):
        die("no such file or directory: %s" % path)
    base = os.path.basename(path)
    d = os.path.dirname(path)
    if base.lower() == TEXT_DB:                      # bundle form
        media_db = os.path.join(d, MEDIA_DB)
        res = os.path.join(d, RES_DIR)
        return path, media_db if os.path.isfile(media_db) else None, \
            res if os.path.isdir(res) else None, os.path.basename(d.rstrip(os.sep))
    if base.endswith(".text.db"):                    # loose form
        stem = path[: -len(".text.db")]
        media_db = stem + ".media.db"
        res = stem + ".res"
        return path, media_db if os.path.isfile(media_db) else None, \
            res if os.path.isdir(res) else None, os.path.basename(stem)
    die("not a wudict library: %s (expected a folder, text.db or <name>.text.db)" % path)


def resolve_output(spec, base, dry_run):
    """--output -> (mdx_path, mdd_path), resolved the way `go build -o` does:

      DIR/ (trailing separator)  -> DIR/<base>.mdx; DIR is created if missing
      existing DIR               -> DIR/<base>.mdx
      PATH.mdx / PATH.mdd        -> PATH.mdx + PATH.mdd
      anything else              -> treated as a base path: PATH.mdx + PATH.mdd

    To get abs.mdx beside an existing folder abs, pass -o abs.mdx.
    """
    if spec is None:
        out = os.path.join(os.getcwd(), base)
    else:
        if not spec:
            die("--output is empty")
        raw = os.path.expanduser(spec)
        as_dir = raw.endswith(tuple(s for s in (os.sep, os.altsep) if s))
        path = os.path.abspath(raw)                  # drops the trailing separator
        if as_dir or os.path.isdir(path):
            if os.path.exists(path) and not os.path.isdir(path):
                die("not a directory: %s" % path)
            if not os.path.isdir(path) and not dry_run:
                try:
                    os.makedirs(path, exist_ok=True)
                except OSError as e:
                    die("cannot create %s: %s" % (path, e))
            out = os.path.join(path, base)
            return out + ".mdx", out + ".mdd"
        out = path
        if out.lower().endswith((".mdx", ".mdd")):
            out = out[:-4]
    outdir = os.path.dirname(out)
    if outdir and not os.path.isdir(outdir):
        die("no such directory: %s" % outdir)
    return out + ".mdx", out + ".mdd"


def open_ro(path):
    uri = "file:%s?mode=ro" % path.replace("?", "%3f").replace("#", "%23")
    conn = sqlite3.connect(uri, uri=True)
    conn.text_factory = bytes          # bodies may be TEXT or BLOB; decode by hand
    return conn


def read_meta(conn):
    try:
        rows = conn.execute("SELECT key, value FROM meta").fetchall()
    except sqlite3.DatabaseError as e:
        die("not a wudict text.db (%s)" % e)
    out = {}
    for k, v in rows:
        out[as_text(k)] = as_text(v)
    return out


def as_text(v):
    if v is None:
        return ""
    if isinstance(v, bytes):
        return v.decode("utf-8", "replace")
    return str(v)


def zstd_reader(raw_dict):
    """A function zstd frame -> bytes, using raw_dict (b"" for none)."""
    try:
        from compression import zstd            # Python 3.14+
    except ImportError:
        zstd = None
    if zstd is not None:
        d = zstd.ZstdDict(raw_dict, is_raw=True) if raw_dict else None
        return lambda frame: zstd.decompress(frame, zstd_dict=d)
    try:
        import zstandard
    except ImportError:
        die("this dictionary is stored with zstd: use Python 3.14 or later, "
            "or install zstandard (pip install zstandard)")
    if raw_dict:
        d = zstandard.ZstdCompressionDict(raw_dict, dict_type=zstandard.DICT_TYPE_RAWCONTENT)
        z = zstandard.ZstdDecompressor(dict_data=d)
    else:
        z = zstandard.ZstdDecompressor()
    return z.decompress


def body_decoder(conn, schema):
    """A function text.db article -> str for a database of this schema."""
    unzstd = None

    def decode(v):
        nonlocal unzstd
        if v is None:
            return ""
        if not isinstance(v, bytes):
            return v
        if v[:1] == bytes([DEFLATE_MARK]):
            return zlib.decompress(v[1:], -15).decode("utf-8", "replace")
        if schema >= 2 and v and v[0] <= LAST_RESERVED:
            if v[0] != ZSTD_MARK:
                return ""                       # reserved: nothing this tool knows
            if unzstd is None:
                row = conn.execute("SELECT data FROM body_dict WHERE id = 0").fetchone()
                unzstd = zstd_reader(row[0] if row else b"")
            return unzstd(ZSTD_MAGIC + v[1:]).decode("utf-8", "replace")
        return v.decode("utf-8", "replace")

    return decode


# --------------------------------------------------------------------------- text

def looks_like_markup(s):
    """Mirror of internal/server/about.go:looksLikeMarkup."""
    i = s.find("<")
    while i >= 0:
        if i + 1 < len(s):
            c = s[i + 1]
            if c.isascii() and (c.isalpha() or c in "/!"):
                return True
        i = s.find("<", i + 1)
    return False


def as_html(chunk, mode):
    chunk = chunk.strip()
    if not chunk:
        return ""
    if mode == "html" or (mode == "auto" and looks_like_markup(chunk)):
        return chunk
    return "<p>" + "<br>".join(html.escape(line.rstrip()) for line in chunk.split("\n")) + "</p>"


def read_file_text(path):
    try:
        with open(os.path.expanduser(path), "r", encoding="utf-8") as f:
            return f.read()
    except OSError as e:
        die("cannot read %s: %s" % (path, e))


def build_description(args, meta):
    chunks = []
    if args.description is not None:
        chunks.append(args.description)
    elif args.description_file:
        chunks.append(read_file_text(args.description_file))
    elif not args.no_description:
        chunks.append(meta.get("description", ""))
    for t in args.prepend_description or []:
        chunks.insert(0, t)
    for p in args.prepend_description_file or []:
        chunks.insert(0, read_file_text(p))
    for t in args.append_description or []:
        chunks.append(t)
    for p in args.append_description_file or []:
        chunks.append(read_file_text(p))
    parts = [h for h in (as_html(c, args.description_format) for c in chunks) if h]
    return args.description_separator.join(parts)


# --------------------------------------------------------------------------- staging

def compile_filters(pats):
    try:
        return [re.compile(p) for p in (pats or [])]
    except re.error as e:
        die("bad regex: %s" % e)


def keep(name, include, exclude):
    if include and not any(r.search(name) for r in include):
        return False
    if exclude and any(r.search(name) for r in exclude):
        return False
    return True


def stage_articles(conn, stage, args, decode_body):
    """Fill mdx(entry, paraphrase, nbytes); return (records, aliases, skipped)."""
    inc, exc = compile_filters(args.include), compile_filters(args.exclude)
    enc = args.py_encoding
    stage.execute("CREATE TABLE mdx (entry TEXT NOT NULL, paraphrase TEXT NOT NULL, nbytes INTEGER NOT NULL)")
    ins = "INSERT INTO mdx (entry, paraphrase, nbytes) VALUES (?, ?, ?)"

    kept_ids, n_rec, n_alias, skipped = set(), 0, 0, 0
    batch = []
    cur = conn.execute("SELECT id, w, m FROM entry ORDER BY id")
    for eid, w, m in cur:
        word = as_text(w)
        sub = word.startswith("@") and len(word) > 1
        if sub:
            if args.subentries == "skip":
                skipped += 1
                continue
            if args.subentries == "strip":
                word = word[1:]
        if not word or not keep(word, inc, exc):
            skipped += 1
            continue
        if args.limit and n_rec >= args.limit:
            skipped += 1
            continue
        body = decode_body(m).replace("\0", "")
        if args.headwords_only:
            body = ""
        kept_ids.add(eid)
        batch.append((word, body, len((body + "\0").encode(enc))))
        n_rec += 1
        if len(batch) >= 2000:
            stage.executemany(ins, batch)
            batch.clear()
            if n_rec % 100000 == 0:
                log(args, "  %d articles staged" % n_rec)
    if batch:
        stage.executemany(ins, batch)
    cur.close()

    if args.aliases != "skip":
        batch = []
        head = {}
        for eid, w in conn.execute("SELECT id, w FROM entry"):
            if eid in kept_ids:
                head[eid] = as_text(w)
        cur = conn.execute("SELECT w, entry_id FROM alias")
        for w, eid in cur:
            target = head.get(eid)
            if target is None:
                continue
            word = as_text(w)
            if not word or word == target or not keep(word, inc, exc):
                continue
            if word.startswith("@") and args.subentries == "skip":
                continue
            if args.aliases == "link":
                body = "@@@LINK=%s\n" % target
            else:                                   # copy
                body = decode_body(
                    conn.execute("SELECT m FROM entry WHERE id=?", (eid,)).fetchone()[0]
                ).replace("\0", "")
            batch.append((word, body, len((body + "\0").encode(enc))))
            n_alias += 1
            if len(batch) >= 2000:
                stage.executemany(ins, batch)
                batch.clear()
        if batch:
            stage.executemany(ins, batch)
        cur.close()
    stage.commit()
    return n_rec, n_alias, skipped


def mdd_key(name):
    """wudict resource name -> MDD key: leading backslash, backslash separators."""
    name = name.replace("\\", "/").lstrip("/")
    name = re.sub(r"^[a-zA-Z][a-zA-Z0-9+.-]*://", "", name)   # sound://, file://
    name = unicodedata.normalize("NFC", name)
    return "\\" + name.replace("/", "\\")


def stage_media(media_db, dirs, stage, args):
    """Fill mdd(entry, file); later sources win. Return (count, bytes)."""
    inc, exc = compile_filters(args.include_media), compile_filters(args.exclude_media)
    stage.execute("CREATE TABLE mdd (entry TEXT NOT NULL UNIQUE, file BLOB NOT NULL)")
    ins = "INSERT INTO mdd (entry, file) VALUES (?, ?)"
    upd = "UPDATE mdd SET file = ? WHERE entry = ?"
    limit = args.max_media_size * 1024 * 1024 if args.max_media_size else 0
    # Collisions are resolved the way the app resolves them (resource.Key:
    # cleaned, NFC, lower case), so a res/ override named Lion.JPG replaces the
    # packed lion.jpg instead of becoming a second record. The packed spelling
    # is kept, because that is what the articles reference.
    seen, n, total = {}, 0, 0

    def add(name, blob):
        nonlocal n, total
        if not name or not keep(name, inc, exc):
            return
        if limit and len(blob) > limit:
            log(args, "  skip (too large): %s" % name)
            return
        key = mdd_key(name)
        fold = key.lower()
        if fold in seen:
            prev_key, prev_size = seen[fold]
            stage.execute(upd, (sqlite3.Binary(blob), prev_key))
            total += len(blob) - prev_size
            seen[fold] = (prev_key, len(blob))
            return
        stage.execute(ins, (key, sqlite3.Binary(blob)))
        seen[fold] = (key, len(blob))
        n += 1
        total += len(blob)

    if media_db and not args.no_packed_media:
        mc = open_ro(media_db)
        # Same pairing check the app makes: a media.db packed from a different
        # build of this dictionary resolves every name and serves wrong bytes.
        if args.media_uuid and read_meta(mc).get("dict_uuid", "") not in ("", args.media_uuid):
            die("%s was packed for a different dictionary (dict_uuid mismatch); "
                "repack it, or export with --no-packed-media" % media_db)
        try:
            cur = mc.execute("SELECT name, data FROM resource")
        except sqlite3.DatabaseError as e:
            die("cannot read %s: %s" % (media_db, e))
        for name, blob in cur:
            add(as_text(name), bytes(blob or b""))
            if n % 20000 == 0 and n:
                log(args, "  %d resources staged" % n)
        cur.close()
        mc.close()

    for root in dirs:                       # res/ overrides, then --media-dir
        root = os.path.abspath(os.path.expanduser(root))
        if not os.path.isdir(root):
            die("no such media directory: %s" % root)
        for dirpath, _dirs, files in os.walk(root):
            for f in sorted(files):
                if f in (".DS_Store", "Thumbs.db"):
                    continue
                full = os.path.join(dirpath, f)
                rel = os.path.relpath(full, root).replace(os.sep, "/")
                with open(full, "rb") as fh:
                    add(rel, fh.read())

    for spec in args.add_media or []:
        full = os.path.abspath(os.path.expanduser(spec))
        if not os.path.isfile(full):
            die("no such file: %s" % spec)
        with open(full, "rb") as fh:
            add(os.path.basename(full), fh.read())

    stage.commit()
    return n, total


# --------------------------------------------------------------------------- writing

def make_writer_class(mdict_writer, attrs_for):
    class Writer(mdict_writer):
        def _write_header(self, f):
            import struct
            root = "Library_Data" if self._is_mdd else "Dictionary"
            # an MDD header carries no encoding: writemdict leaves _encoding
            # unset for is_mdd and the attribute is written empty.
            attrs = attrs_for(self._is_mdd, self._title, self._description, self._version,
                              getattr(self, "_encoding", ""))
            body = "<%s %s/>\r\n\x00" % (
                root,
                " ".join('%s="%s"' % (k, html.escape(v, quote=True)) for k, v in attrs.items()),
            )
            header = body.encode("utf_16_le")
            f.write(struct.pack(b">L", len(header)))
            f.write(header)
            f.write(struct.pack(b"<L", zlib.adler32(header) & 0xFFFFFFFF))
    return Writer


def header_attrs(args, is_mdd, title, description, version, encoding):
    today = datetime.date.today()
    date = "%d-%d-%d" % (today.year, today.month, today.day)
    if is_mdd:
        a = OrderedDict([
            ("GeneratedByEngineVersion", version), ("RequiredEngineVersion", version),
            ("Encrypted", "No"), ("Encoding", ""), ("Format", ""),
            ("CreationDate", date), ("KeyCaseSensitive", "No"), ("Stripkey", "No"),
            ("Description", description), ("Title", title), ("RegisterBy", ""),
        ])
    else:
        a = OrderedDict([
            ("GeneratedByEngineVersion", version), ("RequiredEngineVersion", version),
            ("Encrypted", "No"), ("Encoding", encoding), ("Format", "Html"),
            ("Stripkey", "Yes"), ("CreationDate", date), ("Compact", "Yes"),
            ("Compat", "Yes"), ("KeyCaseSensitive", "No"),
            ("Description", description), ("Title", title),
            ("DataSourceFormat", "106"), ("StyleSheet", ""), ("Left2Right", "Yes"),
            ("RegisterBy", ""),
        ])
        for k, v in args.meta:
            a[k] = v
    return a


def write_pair(args, writer_cls, items, target, title, description, is_mdd):
    w = writer_cls(items, title=title, description=description,
                   key_size=args.key_size * 1024, record_size=args.record_size * 1024,
                   encoding=args.encoding, is_mdd=is_mdd)
    done = [0]
    step = max(1, len(items) // 20) if len(items) else 1

    def cb(value):
        done[0] += value
        if not args.quiet and done[0] % step < value:
            print("\r  %s: %d/%d records" % (os.path.basename(target), done[0], len(items)),
                  end="", file=sys.stderr)

    tmp = target + ".part"
    try:
        with open(tmp, "wb") as f:
            w.write(f, callback=cb)
        os.replace(tmp, target)
    finally:
        if os.path.exists(tmp):
            os.unlink(tmp)
    if not args.quiet:
        print("\r  %s: %d records, %.1f MiB          "
              % (os.path.basename(target), len(items), os.path.getsize(target) / 1048576.0),
              file=sys.stderr)


def load_mdict_utils(path):
    if path:
        sys.path.insert(0, os.path.abspath(os.path.expanduser(path)))
    try:
        from mdict_utils.writer import MDictWriter
    except ImportError as e:
        die("mdict-utils is required (pip install mdict-utils); "
            "use --mdict-utils PATH for a source checkout [%s]" % e)
    return MDictWriter


# --------------------------------------------------------------------------- cli

def parse_args(argv):
    p = argparse.ArgumentParser(
        prog="wudict2mdict.py",
        description="Export a wudict dictionary (library folder or text.db) to MDict .mdx/.mdd.",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="Article HTML is exported verbatim; resource references are left as the "
               "dictionary wrote them, which is what MDict resolves.")
    p.add_argument("input", help="library folder, text.db, or <name>.text.db")
    p.add_argument("-o", "--output", metavar="PATH",
                   help="an existing folder or DIR/ (writes DIR/<name>.mdx, creating DIR/ "
                        "if missing), a .mdx file path, or a base path "
                        "(default: ./<name>.mdx)")
    p.add_argument("-f", "--force", action="store_true", help="overwrite existing output")
    p.add_argument("-n", "--dry-run", action="store_true", help="report what would be written")
    p.add_argument("-q", "--quiet", action="store_true", help="no progress output")
    p.add_argument("--mdict-utils", metavar="PATH", help="path to an mdict-utils checkout")
    p.add_argument("--tmpdir", metavar="DIR", help="staging directory (default: system temp)")

    g = p.add_argument_group("metadata")
    g.add_argument("--title", metavar="TEXT", help="override Title (default: meta.name)")
    g.add_argument("--description", metavar="TEXT",
                   help="replace the description (default: meta.description)")
    g.add_argument("--description-file", metavar="FILE", help="read the description from FILE")
    g.add_argument("--no-description", action="store_true", help="drop meta.description")
    g.add_argument("--append-description", metavar="TEXT", action="append",
                   help="append TEXT after the description (repeatable)")
    g.add_argument("--append-description-file", metavar="FILE", action="append",
                   help="append FILE's text after the description (repeatable)")
    g.add_argument("--prepend-description", metavar="TEXT", action="append",
                   help="insert TEXT before the description (repeatable)")
    g.add_argument("--prepend-description-file", metavar="FILE", action="append",
                   help="insert FILE's text before the description (repeatable)")
    g.add_argument("--description-format", choices=("auto", "html", "text"), default="auto",
                   help="treat description chunks as HTML, as plain text, or decide per chunk "
                        "(default: auto)")
    g.add_argument("--description-separator", metavar="SEP", default="\n",
                   help="text placed between description chunks (default: newline)")
    g.add_argument("--meta", metavar="KEY=VALUE", action="append", default=[],
                   help="set or override an MDX header attribute, e.g. "
                        "--meta KeyCaseSensitive=Yes (repeatable)")

    g = p.add_argument_group("content")
    g.add_argument("--aliases", choices=("link", "copy", "skip"), default="link",
                   help="alternate headwords as @@@LINK= redirects, full copies, or omitted "
                        "(default: link)")
    g.add_argument("--subentries", choices=("include", "strip", "skip"), default="include",
                   help="'@'-prefixed sub-entries: keep as-is, drop the '@', or omit "
                        "(default: include)")
    g.add_argument("--include", metavar="REGEX", action="append",
                   help="only headwords matching REGEX (repeatable)")
    g.add_argument("--exclude", metavar="REGEX", action="append",
                   help="skip headwords matching REGEX (repeatable)")
    g.add_argument("--limit", metavar="N", type=int, default=0,
                   help="stop after N articles (aliases of kept articles still follow)")
    g.add_argument("--headwords-only", action="store_true",
                   help="write empty articles (index-only export)")

    g = p.add_argument_group("media")
    g.add_argument("--no-media", action="store_true", help="do not write an .mdd")
    g.add_argument("--no-packed-media", action="store_true",
                   help="ignore media.db; take media only from res/ and --media-dir")
    g.add_argument("--no-res", action="store_true",
                   help="ignore the library's res/ override folder")
    g.add_argument("--media-dir", metavar="DIR", action="append",
                   help="add a directory tree of resources; later sources win (repeatable)")
    g.add_argument("--add-media", metavar="FILE", action="append",
                   help="add one file at the MDD root, e.g. a stylesheet (repeatable)")
    g.add_argument("--include-media", metavar="REGEX", action="append",
                   help="only resources matching REGEX (repeatable)")
    g.add_argument("--exclude-media", metavar="REGEX", action="append",
                   help="skip resources matching REGEX (repeatable)")
    g.add_argument("--max-media-size", metavar="MB", type=int, default=0,
                   help="skip resources larger than MB megabytes")

    g = p.add_argument_group("container")
    g.add_argument("--encoding", default="UTF-8", choices=tuple(ENCODINGS),
                   help="article encoding written into the MDX (default: UTF-8)")
    g.add_argument("--key-size", metavar="KB", type=int, default=32,
                   help="key block size in KiB (default: 32)")
    g.add_argument("--record-size", metavar="KB", type=int, default=64,
                   help="record block size in KiB (default: 64)")

    args = p.parse_args(argv)
    meta = []
    for kv in args.meta:
        if "=" not in kv:
            die("--meta expects KEY=VALUE, got %r" % kv)
        k, v = kv.split("=", 1)
        k = k.strip()
        if not k:
            die("--meta expects a non-empty key")
        meta.append((k, v))
    args.meta = meta
    args.py_encoding = ENCODINGS[args.encoding]
    if args.limit < 0:
        die("--limit must be >= 0")
    return args


def main(argv=None):
    args = parse_args(argv if argv is not None else sys.argv[1:])
    text_db, media_db, res_dir, default_base = resolve_input(args.input)

    conn = open_ro(text_db)
    ver = conn.execute("PRAGMA user_version").fetchone()[0]
    if ver not in SCHEMAS:
        die("%s has user_version %d, expected %s" % (text_db, ver, " or ".join(map(str, SCHEMAS))))
    meta = read_meta(conn)

    mdx_path, mdd_path = resolve_output(args.output, default_base, args.dry_run)
    for path in (mdx_path, mdd_path):
        if os.path.exists(path) and not args.force and not args.dry_run:
            die("%s exists (use --force)" % path)

    title = args.title if args.title is not None else (meta.get("name") or default_base)
    description = build_description(args, meta)

    media_dirs = []
    if res_dir and not args.no_res:
        media_dirs.append(res_dir)
    media_dirs.extend(args.media_dir or [])
    want_media = not args.no_media and (
        (media_db and not args.no_packed_media) or media_dirs or args.add_media)

    log(args, "source: %s" % text_db)
    log(args, "title:  %s" % title)

    MDictWriter = None if args.dry_run else load_mdict_utils(args.mdict_utils)
    tmp = tempfile.mkdtemp(prefix="wudict2mdict-",
                           dir=os.path.expanduser(args.tmpdir) if args.tmpdir else None)
    try:
        stage_path = os.path.join(tmp, "stage.db")     # mdict-utils streams from a *.db
        stage = sqlite3.connect(stage_path)
        stage.execute("PRAGMA journal_mode=OFF")
        stage.execute("PRAGMA synchronous=OFF")

        n_rec, n_alias, skipped = stage_articles(conn, stage, args, body_decoder(conn, ver))
        log(args, "articles: %d (+%d alias records, %d skipped)" % (n_rec, n_alias, skipped))
        if n_rec + n_alias == 0:
            die("nothing to export")

        n_media, media_bytes = (0, 0)
        args.media_uuid = meta.get("dict_uuid", "")
        if want_media:
            n_media, media_bytes = stage_media(
                media_db if not args.no_packed_media else None, media_dirs, stage, args)
            log(args, "media: %d resources, %.1f MiB" % (n_media, media_bytes / 1048576.0))
        elif not args.no_media and not media_db:
            log(args, "media: none packed (prepare the dictionary with media, or use --media-dir)")

        if args.dry_run:
            log(args, "dry run: would write %s%s" % (mdx_path, " and " + mdd_path if n_media else ""))
            return 0

        attrs = lambda is_mdd, t, d, v, e: header_attrs(args, is_mdd, t, d, v, e)  # noqa: E731
        writer_cls = make_writer_class(MDictWriter, attrs)

        items = [{"key": k, "pos": rid, "path": stage_path, "size": nb}
                 for k, rid, nb in stage.execute("SELECT entry, rowid, nbytes FROM mdx")]
        write_pair(args, writer_cls, items, mdx_path, title, description, False)

        if n_media:
            items = [{"key": k, "pos": rid, "path": stage_path, "size": sz}
                     for k, rid, sz in stage.execute("SELECT entry, rowid, LENGTH(file) FROM mdd")]
            write_pair(args, writer_cls, items, mdd_path, title, description, True)
        stage.close()
    finally:
        conn.close()
        shutil.rmtree(tmp, ignore_errors=True)
    return 0


if __name__ == "__main__":
    sys.exit(main())
