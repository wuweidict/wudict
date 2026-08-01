// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package go_mdict

import (
	"fmt"
	"os"
)

// pkgLogger is a minimal stand-in for the go-logging dependency of the
// original go-mdict code: errors go to stderr, info/debug are dropped
// unless WUDICT_DEBUG is set.
type pkgLogger struct{ name string }

var debugEnabled = os.Getenv("WUDICT_DEBUG") != ""

func (l pkgLogger) Infof(format string, args ...any) {
	if debugEnabled {
		fmt.Fprintf(os.Stderr, "[%s] "+format+"\n", append([]any{l.name}, args...)...)
	}
}

func (l pkgLogger) Debugf(format string, args ...any) { l.Infof(format, args...) }

func (l pkgLogger) Warnf(format string, args ...any) { l.Infof(format, args...) }

func (l pkgLogger) Errorf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "[%s] error: "+format+"\n", append([]any{l.name}, args...)...)
}

var (
	log   = pkgLogger{name: "gomdict"}
	v3log = pkgLogger{name: "gomdict-v3"}
)
