// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"net"
	"net/http"
	"strings"
)

// The Host guard closes DNS rebinding, which is the one way a page the user
// merely VISITS can reach a server bound to loopback.
//
// The attack does not need the server to be on the network. A page served from
// evil.example resolves that name to the attacker's address, then re-resolves
// it to 127.0.0.1 after a short TTL; the browser still considers the origin to
// be evil.example, so its own same-origin policy stops nothing, and the script
// issues same-origin requests that arrive here from 127.0.0.1. isLoopback(r)
// says yes - correctly, the socket really is loopback - and the whole internal
// surface, up to and including DELETE /api/library, is open. CORS (D69) is not
// a defence here either: rebinding produces same-origin requests, which are
// never subject to it.
//
// The one thing the attacker cannot forge is the Host header: it is whatever
// name the victim's browser was pointed at, and a name is exactly what a
// rebinder must use - an IP literal cannot be re-resolved, because there is
// nothing to resolve. So the rule is not an address allowlist (which would
// have to be recomputed on every DHCP lease and every roam, and would break
// the wildcard bind outright) but a SHAPE rule:
//
//	an IP literal is fine, whatever the address;
//	"localhost" is fine, because no public resolver may answer for it;
//	any other name must be named in TRUSTED_HOSTS.
//
// That is uniform across 127.0.0.1, a LAN bind and 0.0.0.0, needs no knowledge
// of the interfaces, and costs a legitimate user nothing: browsers, the
// Android WebView and every extension address this server by IP and port.
//
// hostName reduces a Host header to the bare host: no port, no brackets, no
// trailing root dot. It tolerates the malformed forms too ("::1" unbracketed),
// because the answer for those must be decided by the rule below, not by a
// parse failure that would silently take some other branch.
func hostName(h string) string {
	h = strings.TrimSpace(h)
	if h == "" {
		return ""
	}
	if host, _, err := net.SplitHostPort(h); err == nil {
		h = host
	} else if strings.HasPrefix(h, "[") && strings.HasSuffix(h, "]") {
		h = h[1 : len(h)-1] // "[::1]" with no port
	}
	return strings.TrimSuffix(h, ".")
}

// hostAllowed answers the rule above for one request.
func (s *Server) hostAllowed(r *http.Request) bool {
	h := hostName(r.Host)
	if h == "" {
		// HTTP/1.1 requires a Host and Go rejects a request that lacks one
		// before it reaches here; an empty one at this point is either
		// HTTP/1.0 or something hand-rolled. Neither has a claim on the
		// internal API, and neither is how any client of ours speaks.
		return false
	}
	if net.ParseIP(h) != nil {
		return true
	}
	if strings.EqualFold(h, "localhost") {
		return true
	}
	for _, t := range s.TrustedHosts {
		// A bare "*" is the deliberate opt-out, for a reverse proxy that
		// passes through a name we cannot know. It reinstates the rebinding
		// exposure, which is why it has to be typed.
		if t == "*" || strings.EqualFold(hostName(t), h) {
			return true
		}
	}
	return false
}

// requireHost is the gate ServeHTTP applies before routing. 421 is the exact
// code for it: the request reached a server that is not the one its authority
// names.
func (s *Server) requireHost(w http.ResponseWriter, r *http.Request) bool {
	if s.hostAllowed(r) {
		return true
	}
	httpErr(w, http.StatusMisdirectedRequest,
		"request Host %q is not a name this server answers to; reach it by address, or list the name in TRUSTED_HOSTS", r.Host)
	return false
}

// requireSameSite rejects the one request shape a token cannot police: a
// cross-site GET.
//
// CSRF-by-token is already decided (auth.go) and this does not re-open it. The
// gap it closes is narrower: two of this server's mutations - GET /api/rescan
// and GET /api/setup?save=1 - change state on a SAFE method, so a page on any
// origin can fire them with an <img> tag, and neither the token (carried in a
// cookie the browser attaches for us) nor CORS (which never blocks the request,
// only the reading of the response) stands in the way. A write the attacker
// cannot read is still a write.
//
// Fetch metadata is the cheap answer: the browser states the initiator's
// relationship to this origin, and a script cannot forge or suppress the
// header. Absent metadata means a client that is not a browser - curl, the
// Android shell's HttpURLConnection, an old WebView - and those are not
// reachable by a hostile page in the first place, so the check is fail-open by
// design rather than by oversight.
//
// Two exemptions keep existing contracts intact:
//
//   - the three routes of the CORS grant (D69), which exist precisely to be
//     called cross-origin by an extension, plus their preflights;
//   - a top-level navigation to a PAGE, so that a link to this server handed
//     out in a chat or a QR code still opens. /api/ is excluded from that:
//     nobody legitimately navigates to the internal API from another site,
//     and allowing it would hand the popup back to the same attack.
func (s *Server) requireSameSite(w http.ResponseWriter, r *http.Request) bool {
	if !strings.EqualFold(r.Header.Get("Sec-Fetch-Site"), "cross-site") {
		return true
	}
	if corsRoute(r) {
		return true
	}
	if !strings.HasPrefix(r.URL.Path, "/api/") &&
		strings.EqualFold(r.Header.Get("Sec-Fetch-Mode"), "navigate") &&
		strings.EqualFold(r.Header.Get("Sec-Fetch-Dest"), "document") {
		return true
	}
	httpErr(w, http.StatusForbidden,
		"cross-site request refused; this server answers its own pages and the read-only extension API, nothing else")
	return false
}

// corsRoute reports whether r addresses a route inside the D69 grant. It reads
// the single allowlist rather than restating it: the keys are mux patterns, so
// one ending in "/" is a prefix match exactly as ServeMux treats it, and a
// preflight is part of the grant for the method it precedes.
func corsRoute(r *http.Request) bool {
	m := r.Method
	if m == http.MethodOptions {
		m = http.MethodGet
	}
	for k := range corsAllowed {
		km, p, ok := strings.Cut(k, " ")
		if !ok || km != m {
			continue
		}
		if p == r.URL.Path || (strings.HasSuffix(p, "/") && strings.HasPrefix(r.URL.Path, p)) {
			return true
		}
	}
	return false
}
