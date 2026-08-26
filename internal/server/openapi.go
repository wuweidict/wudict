// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	_ "embed"
	"net/http"
)

// openAPISpec is the API document, hand-written and embedded so a running
// server describes itself: point any OpenAPI tool at /api/openapi.yaml and it
// gets the contract of that exact build, with no file to fetch separately and
// no version to keep in step.
//
// It is hand-written rather than generated because the two streaming endpoints
// emit a union of line types discriminated by "t", which no Go-struct
// reflector can express, and because half the same-origin handlers answer with
// map literals that have no struct to reflect. openapi_test.go is what keeps
// it honest.
//
//go:embed web/openapi.yaml
var openAPISpec []byte

func (s *Server) handleOpenAPI(w http.ResponseWriter, r *http.Request) {
	// RFC 9512. Browsers will not render it, which is correct: it is a
	// document for tools, and the tools all accept this type.
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(openAPISpec)
}
