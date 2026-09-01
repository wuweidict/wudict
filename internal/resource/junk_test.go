// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package resource

import (
	"reflect"
	"testing"
)

func TestIsJunk(t *testing.T) {
	for _, tc := range []struct {
		name string
		want bool
	}{
		{".DS_Store", true},
		{".ds_store", true},
		{".DS_STORE", true},
		{"._.DS_Store", true},      // the AppleDouble shadow of the junk itself
		{"._word.mp3", true},       // the shadow of a real file
		{"images/.DS_Store", true}, // any depth
		{`images\.DS_Store`, true}, // MDD-style separators
		{"__MACOSX/audio/x.mp3", true},
		{"__macosx/x", true},
		{".Spotlight-V100/store.db", true},
		{".Trashes/501/x.png", true},
		{".fseventsd/x", true},
		{".apdisk", true},
		{"Thumbs.db", true},
		{"thumbs.db", true},
		{"ehthumbs.db", true},
		{"desktop.ini", true},
		{"images/Thumbs.db", true},

		// Real resources, including ones that only look like junk.
		{"word.mp3", false},
		{"images/x.png", false},
		{".hidden.css", false},     // a dot file is not automatically junk
		{"_notes.txt", false},      // one underscore, not "._"
		{"my.ds_store.png", false}, // whole component only
		{"macosx/x.png", false},
		{"__MACOSX_theme.css", false},
		{"", false},
	} {
		if got := IsJunk(tc.name); got != tc.want {
			t.Errorf("IsJunk(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestFilter(t *testing.T) {
	in := []string{"a.mp3", ".DS_Store", "images/b.png", "images/.DS_Store", "._a.mp3", "c.css"}
	want := []string{"a.mp3", "images/b.png", "c.css"}
	if got := Filter(in); !reflect.DeepEqual(got, want) {
		t.Errorf("Filter = %q, want %q", got, want)
	}
	// Nothing to drop: the same slice comes back, not a copy.
	clean := []string{"a.mp3", "b.png"}
	if got := Filter(clean); &got[0] != &clean[0] {
		t.Error("Filter reallocated a list with no junk in it")
	}
	if got := Filter(nil); got != nil {
		t.Errorf("Filter(nil) = %q, want nil", got)
	}
}
