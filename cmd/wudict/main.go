// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Command wudict is WuWeiDict's canonical binary — the short name meant for
// typing. Identical to the module-root "wuweidict" command; both delegate to
// internal/cli.
package main

import "github.com/legbehindneck/wuweidict/internal/cli"

func main() { cli.Main() }
