# MDict container format

Byte layout of `.mdx` (articles) and `.mdd` (resources). The two are one container; they
differ only in what the records hold and in a few defaults. Sources: see `README.md`.

## 1. Conventions

- Integers are **big-endian** unless marked LE.
- **Version** is `int(atof(RequiredEngineVersion) × 100)` (`ZDB_T::parseSpec`).
  `RequiredEngineVersion` is mandatory: MDict refuses a header without it (error 4) and any
  version above 300 (error 12). `GeneratedByEngineVersion` is never read by the engine.
  *readmdict differs*: it reads `GeneratedByEngineVersion`. Corpus: the two are equal in
  all 273 files. Versions in the wild: 1.2, 2.0, 3.0.
- **Number**: 4 bytes when the version is ≤ 120, 8 bytes above
  (`ZDB_KeyBlockIndex::loadKeyBlockIndexV2`, `ZDB_KeyBlock::setData`). The record index
  switches at 200 instead (`ZDB_RecordBlock::loadRecordBlockIndexV2`). The two thresholds
  disagree only for versions 1.21–1.99, which no known file uses.
- **Unit**: one byte, or one 2-byte UTF-16LE code unit when the key encoding is UTF-16
  (code page 1200). A NUL unit terminates text.

## 2. Header (every version)

| Offset | Size | Field |
|---|---|---|
| 0 | 4 | header length `L`. MDict accepts 6 ≤ L ≤ 102 400. |
| 4 | L | header text |
| 4+L | 4 **LE** | adler32 of the `L` header bytes |

The adler32 is **little-endian** (corpus: 273/273) and **mandatory** in MDict: a mismatch
refuses the file (error 8, `ZDB_T::openDB`).

The text is UTF-16LE when its first two bytes are `<` `00` (v1/v2, which end with a
two-byte NUL), otherwise UTF-8 (v3). It is one XML element. Its tag decides the kind:
`Dictionary` or `ZDB` → MDX, `Library_Data` → MDD. Attribute values are XML-unescaped,
named and numeric entities alike (CMarkup).

### 2.1 Header attributes

Read by `ZDB_T::parseSpec` unless noted. "bool" means: empty → the default; otherwise true
unless the value is `No` (case-insensitive) (`GetXmlBoolAttr`). Attribute names are
case-sensitive.

| Attribute | MDict's reading |
|---|---|
| `RequiredEngineVersion` | The format version (§1). Mandatory. |
| `GeneratedByEngineVersion` | Not read. |
| `Encrypted` | `atoi(value)` as a bit set. Bit 0 (1): registered dictionary: the v2 key-block meta and the record blocks are encrypted with the registration key (§6). Bit 1 (2): the key-block info is encrypted (§3.2). `No`, `Yes` and an empty value all give 0. *readmdict differs*: `Yes` → 1. Corpus: `No`, `0`, `2`, absent. |
| `Encoding` | v1/v2, matched case-insensitively against exact names: `GBK` → 936, `BIG5` → 950, `UTF-16` → 1200, `UTF-8` → 65001, `ISO8859-1` → 28591. Any other value, empty or absent (`GB2312`, `GB18030`, `UTF16` included) keeps the default: **936 (GBK) for an MDX, 1200 (UTF-16LE) for an MDD**. v3: default 65001; the real key and record encodings come from the section trailers (§4.2). Corpus: MDX `UTF-8` ×167, `GBK` ×1; MDD empty ×102. |
| `Format` (v1/v2), `ContentType` (v3) | `Text` → 0, `Html` → 1, `Xml` → 2, `Binary` → 3. Absent → **Text for an MDX**, Binary for an MDD. An MDX must not be Binary; an MDD must be Binary (error 4 otherwise). Text records are HTML-escaped when shown (`SEMANTICS.md` §3). |
| `KeyCaseSensitive` | bool, default No. MDX and every v3 file: keys compare case-insensitively unless it is true. v1/v2 MDD: always case-insensitive. |
| `StripKey` | bool. Default **Yes for an MDX, No for an MDD**. Only this spelling is read; `Stripkey` (111 corpus files) leaves the default in force. Meaning: `SEMANTICS.md` §2. |
| `Left2Right` | bool, default Yes. |
| `Compact` (or `Compat` when `Compact` is empty) | `Yes` → 1, `No` → 0, otherwise 0. Turns StyleSheet substitution on (`SEMANTICS.md` §3). |
| `StyleSheet` | Style-token table (`SEMANTICS.md` §3). Corpus: none. |
| `Title`, `Description` | Display text. |
| `RegCode` | Hex registration code embedded in the header (§6). Corpus: 8 files. |
| `RegisterBy` | `DeviceID` (case-insensitive) or anything else (EMail): which user identity decrypts `RegCode`. |
| `LibSN` | Stored; serial of the registered library. |
| `UUID` | v3 key source (§6). When absent the engine invents an internal id `hex(adler32 ^ file size)`. |
| `DefaultSortingLocale` | v3: ICU collation locale of the key order (`SEMANTICS.md` §2). |
| `CreationDate`, `DataSourceFormat` | Informational (MdxBuilder writes `DataSourceFormat="107"` for its HTML source type). |

MdxBuilder 3 (the v2 writer) writes, in this order: `GeneratedByEngineVersion="2.0"
RequiredEngineVersion="2.0" Format KeyCaseSensitive StripKey Encrypted RegisterBy
Description Title Encoding CreationDate`, then, for an MDX only, `Compact` and `Compat`
(same value), `Left2Right`, `Filters` (only with an encryption key), `DataSourceFormat`,
`StyleSheet`. The values it uses:
- `Encrypted`: a decimal bit set, 1 when an encryption key was given and 2 when "allow
  export" is off.
- `Format`: `Html` for the MDict-HTML, compact-HTML and StarDict sources, `Text` for
  SugarDict and KDict sources.
- `Encoding`: one of `GBK`, `BIG5`, `ISO8859-1`, `UTF-8`, `UTF-16`.
- booleans: `Yes`/`No`.

`No`/`Yes` for `Encrypted`, the `Stripkey` spelling and an empty `Encoding` on an MDX come
from older or third-party writers.

MdxBuilder 4 writes, for v3: `<ZDB GeneratedByEngineVersion="3.0" RequiredEngineVersion="3.0"
ContentType=… RegisterBy=… Description=… Title=… DefaultSortingLocale=… UUID=… CreationDate=…
Compact=… DataSourceFormat=… StyleSheet=…/>`. There is no `Encoding` in a v3 header.

## 3. v1 / v2 layout

The header is followed by four regions, back to back.

### 3.1 Key-block meta

| Version | Layout |
|---|---|
| v2 | 5 × u64: key blocks `nKB`, entries, key-info decompressed size, key-info size `KI`, key-blocks size `KS`; then a u32 adler32 of those 40 bytes. |
| v1 | 4 × u32: key blocks, entries, key-info size `KI`, key-blocks size `KS`. No checksum. |

With `Encrypted` bit 0 the 40 bytes are Salsa20/8-encrypted with the registration key; no
key, or the wrong one, shows up as an adler mismatch, which MDict reports as "registration
required" (error 11) rather than as corruption. MDict requires every field non-zero. Of the
counts, only the number of key blocks and the key-info sizes are used; MDict keeps only the
low 32 bits of the block and entry counts.

### 3.2 Key-block info (`KI` bytes)

v2: one block (§5) whose decompressed size is the meta's key-info decompressed size. MDict
decodes it with the generic block codec, so any compression method is accepted (corpus:
always zlib, info word `02 00 00 00`). v1: the bytes are the info, uncompressed.

`Encrypted` bit 1: before decoding, bytes `[8:]` are fast-decrypted (§5.2) with key
`ripemd128(bytes[4:8] ‖ 95 36 00 00)`. MDict applies this whatever the version.

Then `nKB` records:

| Field | v1 | v2 |
|---|---|---|
| entries in this key block | Number | Number |
| first-key length (units) | u8 | u16 |
| first key | length units | length units + NUL unit |
| last-key length | u8 | u16 |
| last key | length units | length units + NUL unit |
| compressed size | Number | Number |
| decompressed size | Number | Number |

The units are UTF-16 code units when the key encoding is UTF-16 (an MDD by default),
bytes otherwise. MDict folds the first and last keys (`SEMANTICS.md` §2) and binary-searches
them to find the block that holds a key.

### 3.3 Key blocks (`KS` bytes)

Consecutive blocks (§5), one per info record. Decompressed, each holds exactly its declared
number of entries:

    [record offset: Number][key: units][NUL unit]   × entries

The record offset is a byte offset into the concatenation of all decompressed record
blocks. MDict verifies, per block, that the declared number of keys consumes the block
exactly (error 5). It does **not** check that the per-block counts add up to the meta's
entry count, nor that the compressed sizes add up to `KS`; blocks are decoded lazily, and a
corrupt one costs only its own keys.

### 3.4 Record section

    [record blocks: Number][entries: Number][record-info size: Number][record-blocks size: Number]
    [compressed size: Number][decompressed size: Number]   × record blocks
    blocks (§5)

MDict requires all four header fields to be non-zero and checks one thing: the compressed
sizes add up to the record-blocks size (error 5). The entry count is read and ignored. It
is **not** compared with the number of keys. *readmdict differs* (it asserts equality).
Corpus: `cc-cedict/cedict_2026-8-7.mdx` declares 465 621 entries in the record section
against 560 500 valid keys, and is a working dictionary.

### 3.5 Record extent

Records are addressed in key order: record *i* spans `[offset(i), offset(i+1))` across the
whole key list (block boundaries are irrelevant), and the last record runs to the total
decompressed size (`ZDB_T::getRecordPtr`). MDict returns a pointer into a single
decompressed block, so a record that crossed a block boundary would be read past the
block. Writers never produce one.

## 4. v3 layout

### 4.1 Sections

After the header comes a sequence of sections:

    [type: u32][section size: u64]
      [block count: u32][data size: u64]
      data: block count × [decompressed size: u32][compressed size: u32][block (§5)]
      trailer: [decompressed size: u32][compressed size: u32][block (§5) holding XML]

| Type | Section | Blocks |
|---|---|---|
| `0x01000000` | RecordData | the record blocks |
| `0x02000000` | RecordIndex | 1 |
| `0x03000000` | KeyData | the key blocks |
| `0x04000000` | KeyBlockIndex | 1 |

`section size = 12 + data size + 8 + trailer compressed size`. **`data size` is the byte
length of the framed data blocks** (each 8 + compressed size), not a decompressed total. The
corpus files store the sections in the order 1, 2, 3, 4. MDict reads a section header as
24 bytes (`ParseSection`, `SectionHeader`). *readmdict differs*: it sees 12-byte section
headers, walks the data blocks, and ignores the trailers and both index sections.

### 4.2 Trailers

Every section ends with an XML element (UTF-8, CRLF-terminated):

    <RecordData encoding="UTF-8" recordCount="81554"/>
    <RecordIndex encoding="UTF-8" recordCount="81554"/>
    <KeyData encoding="UTF-8" locale="es-u-ks-level1-ka-shifted" keyCount="81554"/>
    <KeyBlockIndex encoding="UTF-8" locale="es-u-ks-level1-ka-shifted" blockCount="45"/>

`RecordData@encoding` is the record text encoding (`ZDB_RecordBlock::contentEncoding`).
`KeyData@encoding` is the key encoding (UTF-16 is possible: the key import has a UTF-16
branch). `KeyData@locale` builds the collator. `KeyData@keyCount` is the entry count.

### 4.3 Index sections

RecordIndex holds one entry per RecordData block:

    [framed size: u64 = 8 + compressed size][decompressed size: u64]

KeyBlockIndex holds one entry per KeyData block. Numbers are u32, unlike v2:

    [entries: u32][first length: u16][first key + NUL][last length: u16][last key + NUL]
    [framed size: u32][decompressed size: u32]

MDict opens a v3 file through these two indexes (`loadRecordBlockIndexV3`,
`loadKeyBlockIndexV3`): one read each and no walk along the block chain. Block *k*'s file
offset is the start of the section's data plus the sum of the earlier framed sizes.

### 4.4 Key and record blocks

KeyData blocks decompress to `[record offset: u64][key][NUL unit]` × entries. Record
offsets and extents follow §3.5.

## 5. Block codec

Every compressed unit (v1/v2 key-block info, key and record blocks, every v3 block)
has this shape (`UncompressBlock`):

    [info: u32 LE][adler32: u32 BE][payload]

| info bits | Meaning |
|---|---|
| 0–3 | compression: **0** none (payload length must equal the decompressed size), **1** LZO1X (raw stream, no header), **2** zlib (RFC 1950 stream), **5** LZ4 block (`LZ4_decompress_safe`, output exactly the decompressed size). 3, 4 and 6–15 are rejected. |
| 4–7 | encryption: **0** none, **1** fast (§5.2), **2** Salsa20/8 (§5.2). |
| 8–15 | number of leading payload bytes encrypted (0–255). Encryption is applied after compression, so only the head of the compressed stream is scrambled. |
| 16–31 | 0 |

The v1/v2 path (`UncompBlockV2`) and the v3 path (`UncompBlockV3`) share this one codec.
Encryption bits are honoured in v1/v2 blocks too.

Corpus info words, as stored bytes: v1/v2 `00000000`, `01000000`, `02000000` (none, LZO,
zlib; LZO only in v1.2). v3 `12100000` for key, index and trailer blocks (zlib + fast, 16 bytes) and
`22100000` for record blocks (zlib + Salsa20/8, 16 bytes); trailers `10100000`/`20100000`.
MdxBuilder 4 contains no LZO or LZ4 strings. LZ4 is a reader capability with no known writer.

### 5.1 Block key

The key is the dictionary key when there is one (§6). Otherwise it is
`ripemd128(the 4 adler32 bytes as stored)`; MdxBuilder 4 before RC4 wrote such blocks.

### 5.2 Ciphers

- **fast**: for each byte *i*: `t = rotate-nibbles(b[i]) ^ prev ^ (i & 0xFF) ^ key[i mod
  len(key)]`, where `prev` starts at `0x36` and is the previous *ciphertext* byte
  (`MDXDecrypt`, method 1).
- **Salsa20/8**: the ECRYPT reference Salsa20 with 8 rounds, 128-bit key, all-zero 64-bit
  nonce, counter from 0 (`MDXDecrypt`, method 2; `ECRYPT_keysetup(…, 128, 64)`).

### 5.3 Checksum

The adler32 covers **the decrypted, still-compressed payload when the encryption bits are
non-zero, and the decompressed output when they are zero**. MDict checks it only when its
global `g_CRC_Check` is set, and it is zero-initialised (BSS), so by default the engine
trusts the block. *readmdict differs*: it decides by version (v3 → pre-decompression,
v1/v2 → post-decompression), which gives the wrong answer for an unencrypted v3 block and
for an encrypted v1/v2 block. The corpus v3 blocks are all encrypted, and both rules agree
on them.

## 6. Keys and encryption

| Purpose | Key |
|---|---|
| Key-block info (`Encrypted` bit 1) | fast cipher, `ripemd128(info[4:8] ‖ 95 36 00 00)` |
| Any block without a dictionary key | `ripemd128(adler32 bytes)` (§5.1) |
| v3 dictionary key (version exactly 300, no registration code) | `xxh64(uuid[:m]) ‖ xxh64(uuid[m:])`, each digest big-endian, `m = (len(uuid)+1)/2` (`FastHashDigest`) |
| Registered dictionary (`Encrypted` bit 0) | `K = Salsa20/8(RegCode, key = ripemd128(user id))`, where the user id is the e-mail address or the device id according to `RegisterBy`. `RegCode` comes from the header attribute, or from `NAME.key` beside the library (MdxBuilder `LibKeyGen.txt`). `K` decrypts the v2 key-block meta (§3.1) directly. For v1/v2 blocks the engine derives `ripemd128(ripemd128(K))` as the block key (`UncompBlockV2`). *readmdict differs*: it uses `K` itself. Untested against a real registered file. |

A reader without the user's registration code cannot read a bit-0 dictionary. Bit 1 and
v3 UUID encryption need no secret: their keys are in the file.

## 7. Limits and defaults

- Header ≤ 100 KiB (§2).
- MdxBuilder 4 block sizes (decompressed): key blocks 32 768 bytes, record blocks 65 536
  (`MdxBuilder.cfg`).
- MdxBuilder source limits (manual): keyword < 255 characters; one SugarDict-format line
  < 32 KiB.
- MdxBuilder 4 default sorting locale:
  `zh-u-co-pinyin-ks-level1-ka-shifted-kr-space-punct-symbol-digit-en-others-hani`.
