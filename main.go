// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Command wuweidict is WuWeiDict's module-root entry point.
//
// Go names an installed binary after the last element of its package path, so
// this package exists purely so that
//
//	go install github.com/legbehindneck/wuweidict@latest
//
// yields a binary called "wuweidict". The short, canonical name is built from
// cmd/wudict; both are the same program (see internal/cli).
package main

import "github.com/legbehindneck/wuweidict/internal/cli"

func main() { cli.Main() }
