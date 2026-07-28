// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// hostOf strips the scheme from an httptest URL, leaving host:port.
func hostOf(t *testing.T, url string) string {
	t.Helper()
	h, err := net.ResolveTCPAddr("tcp", url[len("http://"):])
	if err != nil {
		t.Fatal(err)
	}
	return h.String()
}

// probeRunning decides whether a second launch hands the user's browser to
// whatever holds the port. It must be certain: pointing a browser at an
// unknown local service would be worse than the plain "port in use" error.
func TestProbeRunningIdentifiesOnlyUs(t *testing.T) {
	// a real gonow-dict answers with its Server header and /api/config
	ours := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Server", "gonow-dict/v1.2.3")
		fmt.Fprint(w, `{"roots":[{"path":"/a","count":3,"total":3,"exists":true}],
			"libDir":"/lib","prepared":7,"useCached":true,"total":3,"configPath":"/c.toml"}`)
	}))
	defer ours.Close()
	inst, ok := probeRunning(hostOf(t, ours.URL))
	if !ok {
		t.Fatal("a gonow-dict server must be recognised")
	}
	if inst.Version != "v1.2.3" || inst.Total != 3 || inst.Prepared != 7 || !inst.UseCached {
		t.Errorf("details not parsed: %+v", inst)
	}
	if len(inst.Roots) != 1 || inst.Roots[0].Path != "/a" {
		t.Errorf("roots not parsed: %+v", inst.Roots)
	}

	// something else on the port: never claimed, whatever it answers
	foreign := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"roots":[],"total":99}`) // even a convincing body
	}))
	defer foreign.Close()
	if _, ok := probeRunning(hostOf(t, foreign.URL)); ok {
		t.Error("a foreign server must NOT be mistaken for gonow-dict")
	}

	// a lookalike header is not enough of a prefix match to fool us
	impostor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Server", "nginx/1.25")
	}))
	defer impostor.Close()
	if _, ok := probeRunning(hostOf(t, impostor.URL)); ok {
		t.Error("nginx must not be mistaken for gonow-dict")
	}

	// nothing listening at all: fails fast, no claim
	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	addr := hostOf(t, dead.URL)
	dead.Close()
	if _, ok := probeRunning(addr); ok {
		t.Error("a closed port must not be claimed")
	}
}

// The point of comparing folders is to tell the user their new --dict-dir was
// ignored. Order must not matter; a different set must be reported.
func TestSameFolders(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	inst := func(paths ...string) *runningInstance {
		r := &runningInstance{}
		for _, p := range paths {
			r.Roots = append(r.Roots, struct {
				Path   string `json:"path"`
				Count  int    `json:"count"`
				Total  int    `json:"total"`
				Exists bool   `json:"exists"`
			}{Path: p})
		}
		return r
	}
	if !sameFolders([]string{a, b}, inst(b, a)) {
		t.Error("order must not matter")
	}
	if sameFolders([]string{a}, inst(a, b)) {
		t.Error("a superset is not the same set")
	}
	if sameFolders([]string{a, b}, inst(a)) {
		t.Error("a subset is not the same set")
	}
	// the same folder by another spelling IS the same folder
	link := filepath.Join(t.TempDir(), "shortcut")
	if err := os.Symlink(a, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if !sameFolders([]string{link}, inst(a)) {
		t.Error("a symlink to the same folder must compare equal")
	}
}
