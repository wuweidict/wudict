// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package dict

import (
	"strings"
	"testing"
)

func TestAboutRegistry(t *testing.T) {
	RegisterAbout("testfmt", func(srcPath string) (About, bool) {
		// path-scoped, like every real provider: the registry is global, and a
		// provider that answers for any path at all would answer for another
		// test's (AboutForPath asks all of them).
		if !strings.HasSuffix(srcPath, ".tf") || srcPath == "/tmp/miss.tf" {
			return About{}, false
		}
		if srcPath == "/tmp/blank.tf" {
			// a provider that says yes with nothing in it is still a miss
			return About{Sections: []Section{{Text: "  \n "}}}, true
		}
		return About{Sections: []Section{{Lang: "English", Text: "hello"}}, Source: srcPath + ".ann"}, true
	})
	// A nil provider is ignored rather than stored, so AboutFor never has to
	// guard a call it made itself.
	RegisterAbout("nilfmt", nil)

	for _, c := range []struct {
		name, format, path string
		want               bool
	}{
		{"hit", "testfmt", "/tmp/x.tf", true},
		{"case-insensitive format", "TESTFMT", "/tmp/x.tf", true},
		{"provider says no", "testfmt", "/tmp/miss.tf", false},
		{"empty sections are a miss", "testfmt", "/tmp/blank.tf", false},
		{"unregistered format", "nosuch", "/tmp/x.tf", false},
		{"nil provider", "nilfmt", "/tmp/x.tf", false},
		{"no source path", "testfmt", "", false},
	} {
		a, ok := AboutFor(c.format, c.path)
		if ok != c.want {
			t.Errorf("%s: AboutFor(%q, %q) ok = %v, want %v", c.name, c.format, c.path, ok, c.want)
			continue
		}
		if ok && (len(a.Sections) != 1 || a.Sections[0].Text != "hello" || a.Source != c.path+".ann") {
			t.Errorf("%s: AboutFor = %+v", c.name, a)
		}
	}
}

func TestAboutEmpty(t *testing.T) {
	for _, c := range []struct {
		name string
		a    About
		want bool
	}{
		{"zero", About{}, true},
		{"whitespace only", About{Sections: []Section{{Text: "\n\t "}}}, true},
		{"heading with no body", About{Sections: []Section{{Lang: "Russian"}}}, true},
		{"one real section", About{Sections: []Section{{Text: "x"}}}, false},
		{"second section carries it", About{Sections: []Section{{}, {Text: "x"}}}, false},
	} {
		if got := c.a.Empty(); got != c.want {
			t.Errorf("%s: Empty = %v, want %v", c.name, got, c.want)
		}
	}
}

// AboutForPath is the caller that has a path and no trustworthy format name -
// the server's About endpoint, which must not open a dictionary to learn one.
func TestAboutForPath(t *testing.T) {
	// Two providers, deliberately registered out of alphabetical order: the
	// answer must not depend on map iteration.
	RegisterAbout("zzpath", func(srcPath string) (About, bool) {
		if strings.HasSuffix(srcPath, ".zz") {
			return About{Sections: []Section{{Text: "zz"}}}, true
		}
		return About{}, false
	})
	RegisterAbout("aapath", func(srcPath string) (About, bool) {
		if strings.HasSuffix(srcPath, ".aa") || strings.HasSuffix(srcPath, ".both") {
			return About{Sections: []Section{{Text: "aa"}}}, true
		}
		return About{}, false
	})
	RegisterAbout("mmpath", func(srcPath string) (About, bool) {
		if strings.HasSuffix(srcPath, ".both") {
			return About{Sections: []Section{{Text: "mm"}}}, true
		}
		return About{}, false
	})

	for _, c := range []struct {
		name, path, want string
	}{
		{"only one answers", "/tmp/x.zz", "zz"},
		{"the other one", "/tmp/x.aa", "aa"},
		{"two answer: lowest format name wins, twice running", "/tmp/x.both", "aa"},
		{"nobody answers", "/tmp/x.unknown", ""},
		{"no source path", "", ""},
	} {
		for i := 0; i < 8; i++ { // map order is randomised per range, so repeat
			a, ok := AboutForPath(c.path)
			if ok != (c.want != "") {
				t.Errorf("%s: ok = %v, want %v", c.name, ok, c.want != "")
				break
			}
			if ok && a.Sections[0].Text != c.want {
				t.Errorf("%s: text = %q, want %q", c.name, a.Sections[0].Text, c.want)
				break
			}
		}
	}
}
