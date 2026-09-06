// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Command wudict is WuWeiDict's entry point - the only one.
//
// Go names an installed binary after the last element of its package path, so
// the module path github.com/wuweidict/wudict makes
//
//	go install github.com/wuweidict/wudict@latest
//
// yield "wudict" directly, with no second shim package (D28). Everything the
// program actually does lives in internal/cli.
package main

import (
	_ "embed"

	"github.com/wuweidict/wudict/internal/cli"
)

// The notices travel INSIDE the binary (`wudict licenses`), because a release
// is a bare executable and several third-party licences require their text to
// accompany a binary distribution. Only this package can embed it: go:embed
// cannot reach out of its own directory, and the file belongs at the root
// where anyone browsing the repo will find it.
//
//go:embed THIRD-PARTY-NOTICES.md
var notices string

func main() {
	cli.Notices = notices
	cli.Main()
}
