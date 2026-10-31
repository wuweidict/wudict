// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package resource

import "testing"

func TestGlob(t *testing.T) {
	tests := []struct {
		pattern, name string
		want          bool
	}{
		// no slash: the file name, at any depth
		{"*.ogg", "1.ogg", true},
		{"*.ogg", "audio/US/ab/1.ogg", true},
		{"*.ogg", "audio/US/ab/1.mp3", false},
		{"1.ogg", "audio/1.ogg", true},
		{"?.ogg", "audio/12.ogg", false},
		{"??.ogg", "audio/12.ogg", true},
		// a slash anchors at the top
		{"audio/*.ogg", "audio/1.ogg", true},
		{"audio/*.ogg", "audio/US/1.ogg", false},
		{"audio/*.ogg", "x/audio/1.ogg", false},
		{"audio/*/*.ogg", "audio/US/1.ogg", true},
		// * stays inside one name
		{"audio*", "audio/1.ogg", false},
		{"a*/1.ogg", "audio/1.ogg", true},
		// ** spans folders, none included
		{"audio/**/1.ogg", "audio/1.ogg", true},
		{"audio/**/1.ogg", "audio/US/ab/1.ogg", true},
		{"**/ab/*", "audio/US/ab/1.ogg", true},
		{"**/ab/*", "ab/1.ogg", true},
		{"audio/**/**/1.ogg", "audio/1.ogg", true},
		{"**", "x.png", true},
		{"**", "a/b/c.png", true},
		// a trailing ** or slash: what is in the folder, not a file of its name
		{"audio/**", "audio/US/ab/1.ogg", true},
		{"audio/**", "audio", false},
		{"audio/", "audio/1.ogg", true},
		{"audio/", "audio/US/ab/1.ogg", true},
		{"audio/", "audio", false},
		{"audio/", "audiobook/1.ogg", false},
		{"audio/US/", "audio/US/ab/1.ogg", true},
		{"audio/US/", "audio/UK/ab/1.ogg", false},
		{`audio\US\`, "audio/US/ab/1.ogg", true},
		{"/", "a/b.png", true},
		// folded as a lookup folds: case, NFC, separators, a leading slash
		{"AUDIO/us/*.OGG", "audio/US/1.ogg", true},
		{"*.ogg", `\audio\US\1.ogg`, true},
		{"/audio/*.ogg", "audio/1.ogg", true},
		{"café.mp3", "café.mp3", true},
		// accents count
		{"cafe.mp3", "café.mp3", false},
		// no classes: brackets are characters
		{"word [1].*", "word [1].mp3", true},
		{"word [1].*", "word 1.mp3", false},
		// a * in the name is still matched by a * in the pattern
		{"*a", "*ba", true},
		{"a*b*c", "aXbYbZc", true},
		{"a*b*c", "aXbYbZ", false},
		// one rune, not one byte
		{"?.png", "ж.png", true},
		{"*", "", false},
	}
	for _, tt := range tests {
		g, err := NewGlob(tt.pattern)
		if err != nil {
			t.Fatalf("NewGlob(%q): %v", tt.pattern, err)
		}
		if got := g.Match(tt.name); got != tt.want {
			t.Errorf("NewGlob(%q).Match(%q) = %v, want %v", tt.pattern, tt.name, got, tt.want)
		}
	}
}

func TestGlobEmpty(t *testing.T) {
	for _, p := range []string{"", ".", "..", "/.."} {
		if _, err := NewGlob(p); err == nil {
			t.Errorf("NewGlob(%q): want an error", p)
		}
	}
}

func TestIsPattern(t *testing.T) {
	for s, want := range map[string]bool{
		"audio/word.mp3": false,
		"word [1].mp3":   false,
		"*.ogg":          true,
		"what?.html":     true,
		"audio/":         true,
		`audio\`:         true,
		"":               false,
	} {
		if got := IsPattern(s); got != want {
			t.Errorf("IsPattern(%q) = %v, want %v", s, got, want)
		}
	}
}
