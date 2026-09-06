// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// The notices are an obligation, not documentation: MIT, BSD and Apache all
// require their text to accompany a BINARY distribution, and a wudict release
// is a bare executable. So the file must be embedded, and it must still name
// every module we actually link - the failure mode being a dependency added
// months after anyone last thought about licences.
func TestNoticesCoverEveryDependency(t *testing.T) {
	if len(notices) < 1000 {
		t.Fatalf("THIRD-PARTY-NOTICES.md embedded as %d bytes - it is not there", len(notices))
	}
	gomod, err := os.ReadFile("go.mod")
	if err != nil {
		t.Fatal(err)
	}
	// Every module path go.mod requires, indirect ones included: they are
	// linked in too, and their licences say nothing about being indirect.
	re := regexp.MustCompile(`(?m)^\s+([a-z0-9][^\s]*\.[^\s]*/[^\s]+) v`)
	var missing []string
	for _, m := range re.FindAllStringSubmatch(string(gomod), -1) {
		if !strings.Contains(notices, "### "+m[1]+" ") {
			missing = append(missing, m[1])
		}
	}
	if len(missing) > 0 {
		t.Errorf("not in THIRD-PARTY-NOTICES.md: %s\nrun `make notices`", strings.Join(missing, ", "))
	}
}
