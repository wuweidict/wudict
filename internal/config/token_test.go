// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"os"
	"runtime"
	"testing"
)

func TestAuthRequired(t *testing.T) {
	cases := []struct {
		mode, ip string
		want     bool
	}{
		{"", "127.0.0.1", false},
		{"auto", "127.0.0.1", false},
		{"auto", "::1", false},
		{"auto", "localhost", false},
		{"auto", "0.0.0.0", true}, // the wildcard IS the network
		{"auto", "192.168.1.5", true},
		{"auto", "", true}, // unknown reads as exposed, never as safe
		{"AUTO", "10.0.0.2", true},
		{"on", "127.0.0.1", true},
		{"On", "127.0.0.1", true},
		{"1", "127.0.0.1", true},
		{"off", "0.0.0.0", false}, // the user's call, and it is allowed to be
		{" off ", "192.168.1.5", false},
		{"0", "192.168.1.5", false},
		{"nonsense", "127.0.0.1", false}, // falls through to auto
	}
	for _, c := range cases {
		if got := AuthRequired(c.mode, c.ip); got != c.want {
			t.Errorf("AuthRequired(%q, %q) = %v, want %v", c.mode, c.ip, got, c.want)
		}
	}
}

// TestLoadToken covers the three properties the file has to have: it is
// created once, it is stable across reads, and it is readable only by its
// owner - the last being the entire point of keeping a secret in a file.
func TestLoadToken(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", os.Getenv("HOME"))
	}
	first, err := LoadToken(false)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) < 40 {
		t.Fatalf("token is only %d characters: %q", len(first), first)
	}
	again, err := LoadToken(false)
	if err != nil {
		t.Fatal(err)
	}
	if again != first {
		t.Errorf("token changed on a plain read: %q then %q", first, again)
	}
	fi, err := os.Stat(TokenPath())
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
		t.Errorf("token file is %v, want 0600", fi.Mode().Perm())
	}
	rotated, err := LoadToken(true)
	if err != nil {
		t.Fatal(err)
	}
	if rotated == first {
		t.Error("--rotate returned the same token")
	}
	back, err := LoadToken(false)
	if err != nil {
		t.Fatal(err)
	}
	if back != rotated {
		t.Error("the rotated token was not the one persisted")
	}
}

func TestNewTokenIsUnique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 64; i++ {
		tok, err := NewToken()
		if err != nil {
			t.Fatal(err)
		}
		if seen[tok] {
			t.Fatalf("NewToken repeated %q", tok)
		}
		seen[tok] = true
	}
}

// AUTH and AUTH_TOKEN travel the same layering as everything else, and neither
// is a Tunable: a tunable is published by /api/config, and publishing the key
// would hand it to exactly the caller who does not have it.
func TestAuthKeysLayerAndAreNotTunable(t *testing.T) {
	t.Setenv("AUTH", "on")
	cfg, err := Load("", map[string]string{"AUTH_TOKEN": "from-flag"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Auth != "on" {
		t.Errorf("AUTH = %q, want on", cfg.Auth)
	}
	if cfg.AuthToken != "from-flag" {
		t.Errorf("AUTH_TOKEN = %q, want from-flag", cfg.AuthToken)
	}
	for _, k := range Tunables {
		if k == "AUTH" || k == "AUTH_TOKEN" {
			t.Errorf("%s is a Tunable: /api/config would publish it", k)
		}
	}
}
