---
name: oracle
description: Differential-test a wudict format reader against a reference implementation — libzim (ZIM container), Xapian glass (ZIM full-text index), or Ghidra on a native library. Use when validating the ZIM/Xapian readers or checking how another reader parses a format.
argument-hint: "zim|xapian|ghidra"
---

Method: a small C++ dumper over the reference library and a throwaway `_test.go` in the package (deleted afterwards) printing the **same line format**; `diff` per file. On a mismatch, check the data itself before blaming either side. Clone reference sources into the session scratchpad, never into the repo.

## zim — libzim (Homebrew `libzim`)
`clang++ -std=c++17 -DZIM_PRIVATE -I/opt/homebrew/include -L/opt/homebrew/lib -lzim dump.cc` (`ZIM_PRIVATE` exposes `Item::getClusterIndex/getBlobIndex`). Per entry index: kind, redirect target, MIME, size, FNV-64 of content, path, title; then `iterByTitle` order and metadata. openZIM's suite: `make test-zim-suite`.

## xapian — `internal/format/zim/glass` (D194)
Homebrew `xapian` 2.0 reads Kiwix's single-file glass databases; ICU from `icu4c`. Extract `X/fulltext/xapian` from each ZIM with a throwaway test (`c.find('X', "fulltext/xapian")` + `contentReader`; stored uncompressed).
- Oracle 1 (reader): dump every term, posting (docid, wdf), doclength, document data; byte diff against a Go dump.
- Oracle 2 (query): mirror libzim `search.cpp` — ICU `Lower; NFD; [:M:] remove; NFC`, `QueryParser` + `Xapian::Stem(lang)`, `STEM_ALL`, stopper, `OP_AND`, `FLAG_CJK_NGRAM`, BM25. Skip queries whose description contains `PHRASE` (positionless index).
- **Trap:** Homebrew's Snowball is newer than the Xapian 1.4 that built Kiwix archives (`emergence`, `intervals`, `organization` stem differently; Khmer/Myanmar n-grams). On disagreement test the index itself: every title word's term must be in that article's postings.
- The fixture `glass/testdata/small.glass.gz` regenerates with `testdata/mkfix.cc` (needs libxapian).

## ghidra — parsers inside a native `.so`
Ghidra headless: `analyzeHeadless <proj> p -import lib.so -postScript DecompAll.java` (minutes for a few thousand functions). Ghidra rebases by 0x100000 (rodata 0x149d12 → 0x249d12). Find code by its string literals (format tag names, error messages).

Maintainer overlay: if `.claude/local/oracle.md` exists, take corpus paths and prior results from it.
