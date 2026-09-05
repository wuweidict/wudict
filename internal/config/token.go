// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
)

// The capability token: the one secret wudict has.
//
// It exists because the server has no users and no login, and never will -
// there is nothing to log in AS. What it needs instead is proof that a request
// comes from the person who started it, and a random string held by the
// launcher is exactly that proof: whoever can read the token was able to read
// the user's home directory (desktop) or is the app itself (Android), and
// anyone else on the network cannot guess it.
//
// Not per launch, though the name suggests it. A token that changed on every
// start would invalidate the browser tab the user already has open, every
// bookmark, and the extension's stored copy - a logout on every restart, to
// defend against an attacker who by construction cannot read the file anyway.
// It is persisted 0600 and rotatable on demand instead (`wudict token
// --rotate`), which is the same property with the cost moved to the one moment
// the user actually wants it: after they suspect it leaked.

// TokenPath is where the secret lives: beside wudict.toml, in the directory
// that already holds the library. Empty when there is no home directory, in
// which case a token can still be supplied by AUTH_TOKEN.
func TokenPath() string {
	if h := homeDir(); h != "" {
		return filepath.Join(h, ".wudict", "token")
	}
	return ""
}

// NewToken returns 256 bits of crypto/rand, base64url, unpadded: 43 characters
// that survive a URL, a shell, a cookie and a copy-paste without escaping.
func NewToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generating token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

// LoadToken reads the persisted token, creating one on first use. With
// rotate, it always writes a new one.
//
// 0600 on the file and 0700 on the directory: the whole security property is
// "another account on this machine cannot read it", and a token file
// world-readable by default would be worse than no token at all - it would
// look like protection while granting it to every local process.
func LoadToken(rotate bool) (string, error) {
	p := TokenPath()
	if p == "" {
		return "", fmt.Errorf("no home directory: set AUTH_TOKEN instead")
	}
	if !rotate {
		if b, err := os.ReadFile(p); err == nil {
			if tok := strings.TrimSpace(string(b)); tok != "" {
				return tok, nil
			}
		} else if !os.IsNotExist(err) {
			return "", fmt.Errorf("reading %s: %w", p, err)
		}
	}
	tok, err := NewToken()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return "", fmt.Errorf("creating %s: %w", filepath.Dir(p), err)
	}
	// Written through a temp file with the final mode from the start: an
	// os.WriteFile that creates 0644 and chmods afterwards is readable for
	// the width of that window, and a crash in between leaves it readable
	// forever.
	tmp, err := os.CreateTemp(filepath.Dir(p), ".token-*")
	if err != nil {
		return "", fmt.Errorf("creating token file: %w", err)
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return "", fmt.Errorf("securing token file: %w", err)
	}
	if _, err := tmp.WriteString(tok + "\n"); err != nil {
		tmp.Close()
		return "", fmt.Errorf("writing token file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("writing token file: %w", err)
	}
	if err := os.Rename(tmp.Name(), p); err != nil {
		return "", fmt.Errorf("installing token file: %w", err)
	}
	return tok, nil
}

// AuthRequired resolves AUTH against the address actually being served.
//
// "auto" is the only value most installations will ever have, and it says the
// thing the user would say if asked: a server nobody else can reach needs no
// password, and a server on the network does. The explicit values exist
// because "reachable" is not always visible from the bind address - a port
// forwarded into a container binds loopback and is served to the world.
func AuthRequired(mode, ip string) bool {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "on", "1", "true", "yes", "always":
		return true
	case "off", "0", "false", "no", "never":
		return false
	}
	return !isLoopbackHost(ip)
}

// isLoopbackHost reports whether ip names an address only this machine can
// reach. An empty or unparseable value is treated as NOT loopback: the failure
// mode of guessing wrong in that direction is an unnecessary token, and in the
// other direction it is an open server.
func isLoopbackHost(ip string) bool {
	ip = strings.TrimSpace(ip)
	if ip == "" {
		return false
	}
	if strings.EqualFold(ip, "localhost") {
		return true
	}
	p := net.ParseIP(ip)
	return p != nil && p.IsLoopback()
}
