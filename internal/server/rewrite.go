package server

import (
	"regexp"
	"strings"
)

// attrRef matches src/href/data/poster attributes with a quoted value.
// Go regexp has no backreferences, so double- and single-quoted values are
// separate alternatives (groups 2 and 3).
var attrRef = regexp.MustCompile(`(?i)(src|href|data|poster)=(?:"([^"]+)"|'([^']+)')`)

// schemeRef matches any real URI scheme (http:, data:, bword:, entry:, d:, x:…).
var schemeRef = regexp.MustCompile(`(?i)^[a-z][a-z0-9+.-]*:`)

// soundOrFile matches the two pseudo-schemes dictionaries use for their own
// bundled resources; these DO get rewritten.
var soundOrFile = regexp.MustCompile(`(?i)^(?:sound|file)://`)

// RewriteEntryHTML rewrites resource references inside one article's HTML so
// the browser fetches them from /res/{dictID}/…:
//
//   - sound:// and file:// pseudo-URLs        → /res/{dictID}/{path}
//   - server-absolute paths ("/img/x.png")    → /res/{dictID}/img/x.png
//   - bare relative paths ("spkr.png", "./a") → /res/{dictID}/…
//
// Left untouched: fragments (#), query-only (?), protocol-relative (//),
// real schemes (http:, https:, data:, bword:, entry:, d:, x:, …), and
// anything already under /res/{dictID}/ — the function is idempotent, so it
// can never produce the doubled /res/{d}/res/{d}/… URLs the old client-side
// chained-regex rewriter generated.
//
// This runs server-side (each attribute visited exactly once) so the logic
// is unit-tested here rather than living in embedded page JavaScript.
func RewriteEntryHTML(html, dictID string) string {
	if html == "" || dictID == "" {
		return html
	}
	resPrefix := "/res/" + dictID + "/"
	return attrRef.ReplaceAllStringFunc(html, func(m string) string {
		sub := attrRef.FindStringSubmatch(m)
		attr := sub[1]
		ref, quote := sub[2], `"`
		if ref == "" {
			ref, quote = sub[3], `'`
		}
		switch {
		case strings.HasPrefix(ref, "#"),
			strings.HasPrefix(ref, "?"),
			strings.HasPrefix(ref, "//"):
			return m
		case strings.HasPrefix(ref, resPrefix): // already rewritten: idempotent
			return m
		case schemeRef.MatchString(ref) && !soundOrFile.MatchString(ref):
			return m // real scheme: leave cross-references and externals alone
		}
		return attr + "=" + quote + resURL(dictID, ref) + quote
	})
}

// resURL maps one dictionary-internal reference to its /res/ URL: the
// sound://‌/file:// pseudo-scheme and a single leading "/" or "./" are
// stripped, mirroring the resource names the backends serve.
func resURL(dictID, ref string) string {
	ref = soundOrFile.ReplaceAllString(ref, "")
	if strings.HasPrefix(ref, "./") {
		ref = ref[2:]
	} else {
		ref = strings.TrimPrefix(ref, "/")
	}
	return "/res/" + dictID + "/" + ref
}
