// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Command wudict is WuWeiDict's entry point — the only one.
//
// Go names an installed binary after the last element of its package path, so
// the module path github.com/wuweidict/wudict makes
//
//	go install github.com/wuweidict/wudict@latest
//
// yield "wudict" directly, with no second shim package (D28). Everything the
// program actually does lives in internal/cli.
package main

import "github.com/wuweidict/wudict/internal/cli"

func main() { cli.Main() }
