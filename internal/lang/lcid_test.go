// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package lang

import "testing"

func TestFromLCID(t *testing.T) {
	tests := []struct {
		id   int
		want string
	}{
		// the sublanguage is a country, and a country is not a language
		{1033, "en"}, {2057, "en"}, {16393, "en"},
		{1049, "ru"}, {2073, "ru"},
		{1034, "es"}, {3082, "es"}, {20490, "es"},
		{1031, "de"}, {32775, "de"}, // GermanNewSpelling is German
		{1036, "fr"}, {14348, "fr"},

		// the sublanguage IS the language
		{1028, "zh-Hant"}, {2052, "zh-Hans"}, {5124, "zh-Hant"},
		{1044, "nb"}, {2068, "nn"},
		{1050, "hr"}, {5146, "bs"}, {2074, "sr-Latn"}, {3098, "sr-Cyrl"},
		{1068, "az-Latn"}, {2092, "az-Cyrl"},
		{1091, "uz-Latn"}, {2115, "uz-Cyrl"},
		{1084, "gd"}, {2108, "ga"},
		{32811, "hyw"}, {1067, "hy"},

		// reachable only through the primary table
		{1142, "la"}, {1125, "dv"}, {1112, "mni"}, {1153, "mi"},

		// the DSL reference's own typo for Kyrgyz, and the real one
		{1595, "ky"}, {1088, "ky"},

		{0, ""}, {-1, ""}, {1279, ""}, {999999, ""},
	}
	for _, tt := range tests {
		if got := FromLCID(tt.id); got != tt.want {
			t.Errorf("FromLCID(%d) = %q, want %q", tt.id, got, tt.want)
		}
	}
}

// The primary table is the fallback for every LCID that is not listed exactly,
// so a bare language subtag in it must be one Normalize can also read back -
// otherwise a caller that strips the script and looks up a lemmatizer finds
// nothing for a reason no reader could guess.
func TestLCIDPrimaryCodesAreWellFormed(t *testing.T) {
	for id, code := range lcidPrimary {
		if len(code) < 2 || len(code) > 3 {
			t.Errorf("primary 0x%x: %q is neither a 639-1 nor a 639-3 code", id, code)
		}
	}
}
