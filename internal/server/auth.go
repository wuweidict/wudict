// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

// The capability token, server side.
//
// wudict has no accounts, so there is nothing to authenticate; what there is
// to establish is that the request comes from the person who started the
// server. A random string, held by whoever launched it and by nobody else,
// establishes exactly that - and nothing more, which is why it is a
// capability rather than a login.
//
// Three ways in, because the requests that must carry it are not all the same
// kind of request:
//
//   - a cookie, which is the only one that works. Subresources (/res/ images
//     and audio inside an article), EventSource (/api/ingest) and ordinary
//     navigations cannot be given an Authorization header, so a header-only
//     scheme would authorise the page and then fail everything the page does.
//     SameSite=Strict is what keeps that cookie from being a CSRF liability:
//     a request originating anywhere else never carries it, so the token
//     boundary coincides with the same-origin boundary the CORS grant (D69)
//     already draws, by construction rather than by agreement.
//   - ?k= on the URL, which is how the cookie gets set in the first place -
//     the launcher and the Android shell open a tokenised link, and the
//     server turns it into a cookie and redirects to the clean URL so the
//     secret does not sit in history, in the address bar, or in a Referer.
//   - Authorization: Bearer, for everything that is not a browser: curl, the
//     shell's own /api/config probe, a script.
//
// HttpOnly on the cookie is deliberate even though it costs the page the
// ability to read its own token: an article renders untrusted HTML from a
// downloaded dictionary, and document.cookie is the shortest path from that
// to exfiltration.
const cookieName = "wudict_session"

// authFree is the set of routes reachable without the token, stated as an
// allowlist so that a route added tomorrow is protected by DEFAULT. The
// inverse - a flag on each route - protects nothing that nobody remembered to
// flag, which is precisely the route that will be forgotten.
//
// Three groups, and nothing else belongs here:
//
//   - the three read-only endpoints a browser extension is granted (D69),
//     plus their preflights. They are already a published contract with an
//     origin allowlist in front of them; requiring a token there would break
//     every installed extension while adding no boundary the CORS allowlist
//     does not already draw.
//   - the app's own shell: the pages and their static assets. They are the
//     program's source, embedded in a binary anyone can download, and they
//     disclose nothing about this machine. Serving them to an unauthorised
//     visitor is how that visitor gets an explanation instead of a blank
//     socket - every byte of DATA the page then asks for is gated.
//   - the API document, for the same reason.
var authFree = map[string]bool{
	"GET /api/dicts":          true,
	"GET /api/search":         true,
	"GET /res/":               true,
	"OPTIONS /api/dicts":      true,
	"OPTIONS /api/search":     true,
	"OPTIONS /res/":           true,
	"GET /api/openapi.yaml":   true,
	"GET /":                   true,
	"GET /setup":              true,
	"GET /lemmas":             true,
	"GET /assets/frame.js":    true,
	"GET /assets/setup.css":   true,
	"GET /assets/favicon.svg": true,
	"GET /favicon.ico":        true,
}

// tokenEqual compares in constant time. The length leak is inherent to
// ConstantTimeCompare and harmless here: the length is a build-time constant
// of NewToken, not a secret.
func tokenEqual(got, want string) bool {
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

// presented returns the token the request carries, by any of the three routes.
func presented(r *http.Request) string {
	if c, err := r.Cookie(cookieName); err == nil && c.Value != "" {
		return c.Value
	}
	if a := r.Header.Get("Authorization"); a != "" {
		if tok, ok := strings.CutPrefix(a, "Bearer "); ok {
			return strings.TrimSpace(tok)
		}
	}
	return r.URL.Query().Get("k")
}

func (s *Server) authorized(r *http.Request) bool {
	return s.AuthToken == "" || tokenEqual(presented(r), s.AuthToken)
}

// withAuth gates one route. The token is read at request time, not at wrap
// time: New builds the mux before the CLI has resolved the configuration.
func (s *Server) withAuth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.authorized(r) {
			h(w, r)
			return
		}
		unauthorized(w, r)
	}
}

// unauthorized answers in the language the caller is speaking. A fetch from
// the page gets JSON with a marker it can branch on; a person who typed the
// address gets a sentence telling them what to do about it, because "401" on
// a blank page is a bug report waiting to happen.
func unauthorized(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if strings.Contains(r.Header.Get("Accept"), "text/html") {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`<!doctype html><meta charset=utf-8>
<title>wudict: access key needed</title>
<style>body{font:16px/1.6 system-ui,sans-serif;margin:8vh auto;max-width:34em;padding:0 1.5em}code{background:#8881;padding:.1em .4em;border-radius:.3em}</style>
<h1>Access key needed</h1>
<p>This dictionary server is listening on the network, so it asks for the key
that was created when it started.</p>
<p>On the machine running it, type <code>wudict token</code>: it prints a link
that already carries the key. Open that link once and this browser will
remember it.</p>
`))
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`{"error":"access key required: open the link printed by 'wudict token'","auth":"required"}` + "\n"))
}

// grantCookie turns a valid ?k= into a cookie, and - for a navigation - a
// redirect to the same URL without it. Called before routing, so the token
// works on any route rather than on whichever one somebody remembered to
// handle it in.
//
// The redirect is not cosmetic: without it the secret stays in the address
// bar, in the browser's history, in any bookmark made from that tab, and in
// the Referer of every outbound request the page makes. Returning true means
// the response is finished.
func (s *Server) grantCookie(w http.ResponseWriter, r *http.Request) bool {
	if s.AuthToken == "" {
		return false
	}
	q := r.URL.Query()
	k := q.Get("k")
	if k == "" || !tokenEqual(k, s.AuthToken) {
		return false
	}
	http.SetCookie(w, &http.Cookie{
		Name:  cookieName,
		Value: s.AuthToken,
		Path:  "/",
		// No Expires: the cookie lives as long as the browser session. The
		// token itself persists, so a new session is one tokenised link away,
		// and a shared or public machine does not keep the capability after
		// the window closes.
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
	if r.Method != http.MethodGet || !strings.Contains(r.Header.Get("Accept"), "text/html") {
		return false // cookie set; carry on and serve the request itself
	}
	q.Del("k")
	u := *r.URL
	u.RawQuery = q.Encode()
	if u.Path == "" {
		u.Path = "/"
	}
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, u.RequestURI(), http.StatusFound)
	return true
}
