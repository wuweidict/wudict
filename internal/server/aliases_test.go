// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"reflect"
	"testing"
)

// The Android labels are derived from the paths actually in play: a device
// with no card is never told about one, and the app's own directory is shown
// under the volume that holds it — which is where the Files app shows it too.
func TestAndroidAliases(t *testing.T) {
	pkg := "/storage/emulated/0/Android/data/com.legbehindneck.wudict/files"
	for _, c := range []struct {
		name  string
		paths []string
		want  [][2]string
	}{
		{
			"the Play flavour: everything under the app's external files dir",
			[]string{pkg + "/db", pkg + "/.wudict/wudict.toml", pkg + "/Dictionaries"},
			[][2]string{{"/storage/emulated/0", "Internal storage"}},
		},
		{
			"the FOSS flavour: a shared folder plus internal flash",
			[]string{"/data/user/0/com.legbehindneck.wudict/files/db", "/storage/emulated/0/Dictionaries"},
			[][2]string{
				{"/data/user/0/com.legbehindneck.wudict", "App storage"},
				{"/storage/emulated/0", "Internal storage"},
			},
		},
		{
			"a removable card is named by its mount id, and beats nothing else",
			[]string{"/storage/1A2B-3C4D/Dictionaries", "/sdcard/db"},
			[][2]string{
				{"/storage/1A2B-3C4D", "SD card (1A2B-3C4D)"},
				{"/sdcard", "Internal storage"},
			},
		},
		{
			"non-volumes and repeats produce nothing extra",
			[]string{"", "/storage", "/storage/self/primary/x", "/storage/emulated/0", "/storage/emulated/0/a", "/tmp/x"},
			[][2]string{{"/storage/emulated/0", "Internal storage"}},
		},
	} {
		if got := androidAliases(c.paths); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s:\n androidAliases(%v) = %v\n want %v", c.name, c.paths, got, c.want)
		}
	}
}

// A path is shortened only at a segment boundary, so a sibling directory whose
// name merely starts with a volume's name keeps its full path.
func TestAndroidAliasesDoNotMatchPartialSegments(t *testing.T) {
	got := androidAliases([]string{"/storage/emulatedX/Dictionaries"})
	want := [][2]string{{"/storage/emulatedX", "SD card (emulatedX)"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("androidAliases = %v, want %v", got, want)
	}
}
