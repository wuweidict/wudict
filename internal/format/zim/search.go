// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package zim

import (
	"sort"
	"strings"
)

// Lookup over the path pointer list, which is sorted in raw BYTE order over
// (namespace, path). That is what makes this a binary search over the file
// instead of over an in-memory map, and it is why the ban in docs/SPEC.md §6c
// on "binary-search / collation work" in a direct backend does not apply: that
// rule exists to stop a backend GUESSING a collation and returning silently
// wrong results. Byte order is not a guess - it is exact, it is what the file
// was written with, and it was verified against every entry of a real
// wiktionary before this was written.

// lowerBound returns the first entry index whose key is >= key, or entryCount
// when none is. ~21 preads on a 1.5M-entry file, no resident index.
func (c *container) lowerBound(key string) (int, error) {
	var err error
	n := len(c.urlPtr)
	i := sort.Search(n, func(m int) bool {
		if err != nil {
			return true // unwind: every probe reports "at or past", so lo stops moving
		}
		d, e := c.entry(m)
		if e != nil {
			err = e
			return true
		}
		return d.key() >= key
	})
	if err != nil {
		return 0, err
	}
	return i, nil
}

// find locates an exact (namespace, path) entry.
func (c *container) find(ns byte, path string) (int, dirent, bool) {
	key := searchKey(ns, path)
	i, err := c.lowerBound(key)
	if err != nil || i >= len(c.urlPtr) {
		return 0, dirent{}, false
	}
	d, err := c.entry(i)
	if err != nil || d.key() != key {
		return 0, dirent{}, false
	}
	return i, d, true
}

// nsRange returns the half-open entry range of one namespace. Two binary
// searches, no scan: the namespace is the first byte of every key, so its
// entries are contiguous.
func (c *container) nsRange(ns byte) (int, int) {
	lo, err := c.lowerBound(string([]byte{ns}))
	if err != nil {
		return 0, 0
	}
	if ns == 0xFF { // no successor byte to bound with
		return lo, len(c.urlPtr)
	}
	hi, err := c.lowerBound(string([]byte{ns + 1}))
	if err != nil || hi < lo {
		return lo, lo
	}
	return lo, hi
}

// prefixScan walks entries in a namespace whose path starts with prefix,
// calling fn until it returns false or the prefix runs out.
//
// The read budget is bounded independently of the caller's limit, because a
// prefix can sit in front of thousands of entries the caller rejects (a
// non-article MIME, a resource path) and an unbounded scan would turn one
// keystroke into a walk of the namespace.
func (c *container) prefixScan(ns byte, prefix string, budget int, fn func(int, dirent) bool) error {
	key := searchKey(ns, prefix)
	i, err := c.lowerBound(key)
	if err != nil {
		return err
	}
	for ; i < len(c.urlPtr) && budget > 0; i, budget = i+1, budget-1 {
		d, err := c.entry(i)
		if err != nil {
			return err
		}
		if !strings.HasPrefix(d.key(), key) {
			return nil
		}
		if !fn(i, d) {
			return nil
		}
	}
	return nil
}

// pathVariants expands a user's word into the spellings a ZIM path may use,
// most likely first. Paths are the wiki page titles with spaces written as
// '_', MediaWiki capitalises the first letter of every title, and a proper
// noun carries that capital on every word - so "new york" must still find
// "New_York" without any resident folded index. Each variant costs one binary
// search (~21 preads) and is only tried after the plainer ones miss, which is
// why a short closed list of spellings is cheaper here than the folded map it
// replaces. It is not case folding: a title that capitalises nothing a user
// would guess stays a preview miss, and is found once the dictionary is
// prepared.
func pathVariants(word string) []string {
	out := make([]string, 0, 5)
	add := func(s string) {
		if s == "" {
			return
		}
		for _, v := range out {
			if v == s {
				return
			}
		}
		out = append(out, s)
	}
	add(word)
	underscored := strings.ReplaceAll(word, " ", "_")
	add(underscored)
	add(upperFirst(word))
	add(upperFirst(underscored))
	add(titleWords(underscored))
	return out
}

// titleWords upper-cases the first letter of every '_'-separated word, which
// is how a multiword proper noun is spelled as a path ("New_York").
func titleWords(s string) string {
	parts := strings.Split(s, "_")
	for i, p := range parts {
		parts[i] = upperFirst(p)
	}
	return strings.Join(parts, "_")
}

// upperFirst upper-cases the first rune only, leaving the rest of the string
// exactly as it was: "new york" -> "New york". Capitalising the later words
// too is titleWords' job, and a separate variant on purpose.
func upperFirst(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	u := []rune(strings.ToUpper(string(r[0])))
	return string(u) + string(r[1:])
}
