// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"regexp"
	"strings"
	"testing"
)

// This file is the reason the API document may be hand-written: it makes the
// document and the route table prove each other, in both directions, on every
// `make check`. Adding an endpoint without documenting it fails here, and so
// does documenting one that no longer exists.
//
// The parser below reads the `paths:` block by indentation rather than through
// a YAML library, because a YAML library would be a dependency added for a
// test, to read a file this repository writes and formats itself. It is
// deliberately strict: if the file is ever reformatted, the test says so
// instead of quietly matching nothing.

var (
	specPathRe   = regexp.MustCompile(`^ {2}(/\S*):\s*$`)
	specMethodRe = regexp.MustCompile(`^ {4}(get|put|post|delete|options|patch|head):\s*$`)
)

// specOperations returns the "METHOD /path" set the document declares.
func specOperations(t *testing.T) map[string]bool {
	t.Helper()
	ops := map[string]bool{}
	inPaths := false
	path := ""
	for _, line := range strings.Split(string(openAPISpec), "\n") {
		if strings.HasPrefix(line, "paths:") {
			inPaths = true
			continue
		}
		if !inPaths {
			continue
		}
		// any other top-level key ends the block
		if line != "" && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "#") {
			break
		}
		if m := specPathRe.FindStringSubmatch(line); m != nil {
			path = m[1]
			continue
		}
		if m := specMethodRe.FindStringSubmatch(line); m != nil {
			if path == "" {
				t.Fatalf("openapi.yaml: operation %q before any path", m[1])
			}
			ops[strings.ToUpper(m[1])+" "+path] = true
		}
	}
	if len(ops) == 0 {
		t.Fatal("openapi.yaml: no operations parsed - did the indentation change?")
	}
	return ops
}

// TestOpenAPICoversEveryRoute is the drift gate. An endpoint that the server
// answers but the document does not mention is an undocumented API; an
// operation the document promises but the server does not serve is a lie.
func TestOpenAPICoversEveryRoute(t *testing.T) {
	spec := specOperations(t)
	routed := map[string]bool{}

	for _, rt := range New(nil).routes() {
		if rt.Spec == "" { // pages and static assets: the app, not its contract
			continue
		}
		op := rt.Method + " " + rt.Spec
		routed[op] = true
		if !spec[op] {
			t.Errorf("route %s %s is not in web/openapi.yaml (documented as %q)",
				rt.Method, rt.Pattern, rt.Spec)
		}
	}
	for op := range spec {
		if !routed[op] {
			t.Errorf("web/openapi.yaml documents %s, which no route serves", op)
		}
	}
}

// TestCORSBoundary pins the D69 grant. Three read-only routes answer a browser
// extension; widening that set now takes an edit in two places, and this test
// is the second one.
func TestCORSBoundary(t *testing.T) {
	got := map[string]bool{}
	for _, rt := range New(nil).routes() {
		if rt.CORS {
			got[rt.Method+" "+rt.Pattern] = true
		}
	}
	for op := range got {
		if !corsAllowed[op] {
			t.Errorf("%s answers cross-origin but is not in corsAllowed (D69)", op)
		}
	}
	for op := range corsAllowed {
		if !got[op] {
			t.Errorf("corsAllowed lists %s, which no route grants", op)
		}
	}
}

// TestRoutesAreUnique catches a duplicated pattern, which net/http would
// otherwise report as a panic at startup - after the binary shipped.
func TestRoutesAreUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, rt := range New(nil).routes() {
		key := rt.Method + " " + rt.Pattern
		if seen[key] {
			t.Errorf("duplicate route %s", key)
		}
		seen[key] = true
	}
}

// TestOpenAPIServed checks the embed is present and reaches the wire. An empty
// embed compiles.
func TestOpenAPIServed(t *testing.T) {
	if !strings.HasPrefix(strings.TrimLeft(string(openAPISpec), "#\n "), "wudict") &&
		!strings.Contains(string(openAPISpec), "openapi: 3.1") {
		t.Fatal("web/openapi.yaml is not embedded, or is not an OpenAPI document")
	}
}
