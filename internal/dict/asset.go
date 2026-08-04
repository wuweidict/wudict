// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package dict

import (
	"path"
	"strings"
)

// assetExt is what a dictionary may legitimately bundle and a browser may
// legitimately fetch from it. It is deliberately an allowlist, not a denylist:
// an article is third-party HTML, and this is what stops a stray
// `href="secrets.env"` from turning a resource route into a file browser.
var assetExt = map[string]bool{
	".css": true, ".js": true, ".html": true, ".htm": true,
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true,
	".svg": true, ".bmp": true, ".ico": true, ".avif": true,
	".mp3": true, ".ogg": true, ".oga": true, ".wav": true, ".spx": true,
	".m4a": true, ".opus": true, ".flac": true, ".aac": true,
	".mp4": true, ".webm": true, ".ogv": true,
	".woff": true, ".woff2": true, ".ttf": true, ".otf": true, ".eot": true,
	".json": true, ".xml": true, ".txt": true,
}

// IsAssetName reports whether a reference names a bundled file by extension.
//
// Two callers need the same answer and used to hold two lists. The MDX backend
// uses it to decide what may be served from beside the .mdx (a security
// allowlist), and the article rewriter uses it to tell a pronunciation link
// from a cross-reference — `<a href="defendant">` is a headword, while
// `<a href="defendant__gb_1.ogg">` is audio, and only the extension separates
// them. One list, so the two can never drift into disagreeing.
//
// Any query or fragment is discarded first: "spkr.png?v=2" is still a PNG.
func IsAssetName(ref string) bool {
	if i := strings.IndexAny(ref, "?#"); i >= 0 {
		ref = ref[:i]
	}
	if ref == "" {
		return false
	}
	return assetExt[strings.ToLower(path.Ext(ref))]
}
