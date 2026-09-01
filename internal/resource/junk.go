// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package resource

import "strings"

// IsJunk reports whether a stored resource name is filesystem bookkeeping
// rather than a file the dictionary means to hold: a `.DS_Store`, the `._name`
// AppleDouble shadow every macOS copy to a non-Apple filesystem leaves behind,
// the `__MACOSX/` folder a macOS zip carries, a `Thumbs.db`.
//
// These arrive by the thousand in repacked dictionaries - a collection zipped
// on a Mac has one `.DS_Store` per folder and one `._x` per file - and no
// article ever references them. Packing them copies the noise into media.db
// forever; unpacking them writes it back out into an export. Neither is a
// judgement call, so it is made once, here, and every path that enumerates a
// container's files uses it.
//
// Matching is case-insensitive because the container's spelling is not ours to
// predict (`.ds_store` comes back from case-folding filesystems and from
// archivers that lower-case), and applies to EVERY path component, because a
// `.DS_Store` two folders down is the same file as one at the root.
//
// Names are matched, never contents. A dictionary that genuinely stores a file
// called `desktop.ini` and references it from an article loses it - the trade
// is deliberate: that has never been observed, and the noise has, at a scale
// (110,639 resources in one dictionary here) where it is the difference between
// an export a human can read and one they cannot.
func IsJunk(name string) bool {
	if name == "" {
		return false
	}
	for _, c := range strings.FieldsFunc(name, func(r rune) bool { return r == '/' || r == '\\' }) {
		if junkComponent(c) {
			return true
		}
	}
	return false
}

func junkComponent(c string) bool {
	low := strings.ToLower(strings.TrimSpace(c))
	if low == "" {
		return false
	}
	// AppleDouble: "._" plus the name of the real file it shadows. The real
	// file is stored beside it and is NOT junk, so the prefix - not the
	// suffix - is what decides.
	if strings.HasPrefix(low, "._") {
		return true
	}
	return junkNames[low]
}

// junkNames is the closed list: macOS first, because that is what the report
// was about, then the two Windows equivalents that arrive in the same repacks.
// Every entry is a name an OS writes on its own, never one a dictionary author
// types.
var junkNames = map[string]bool{
	".ds_store":               true,
	"__macosx":                true, // the folder a macOS zip wraps its metadata in
	".spotlight-v100":         true,
	".documentrevisions-v100": true,
	".temporaryitems":         true,
	".trashes":                true,
	".fseventsd":              true,
	".apdisk":                 true,
	".volumeicon.icns":        true,
	"thumbs.db":               true,
	"ehthumbs.db":             true,
	"desktop.ini":             true,
}

// Filter drops the junk from a list of stored names, preserving order. The
// list is rebuilt only when something is actually dropped, so the common case
// - a container with no junk in it - costs one pass and no allocation.
func Filter(names []string) []string {
	for i, n := range names {
		if !IsJunk(n) {
			continue
		}
		out := make([]string, i, len(names))
		copy(out, names[:i])
		for _, n := range names[i+1:] {
			if !IsJunk(n) {
				out = append(out, n)
			}
		}
		return out
	}
	return names
}
