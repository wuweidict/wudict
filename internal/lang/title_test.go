// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package lang

import "testing"

func TestFromTitle(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		// Code pairs: the FIRST half is the index language.
		{"Hagen's Comprehensive paradigm (Ru-Ru)", "ru"},
		{"BSE (rus-rus)", "ru"},
		{"Apresyan (En-Ru)", "en"},
		{"ExplanatoryBTS (Ru-Ru)", "ru"},
		{"Universal (De-Ru)", "de"},
		{"Something [es-en]", "es"},
		{"Something (En - Ru)", "en"},
		{"Something (en–ru)", "en"}, // en dash
		{"Collins (ru)", "ru"},      // a parenthesis holding nothing else

		// English names, first one wins.
		{"Spanish-English Oxford dictionary", "es"},
		{"Larousse Compact English-Spanish", "en"},
		{"Dahl's Russian Dictionary", "ru"},
		{"JM Latin-English Dictionary", "la"},
		{"Cambridge English Pronouncing Dictionary - 18th Edition", "en"},
		{"Norwegian Bokmal-English", "nb"}, // longest name at the position

		// A name beats a code pair only by position, never by kind.
		{"Russian dictionary (en-en)", "ru"},
		{"(en-en) Russian dictionary", "en"},

		// Cyrillic majority, and only after everything else was silent.
		{"Брокгауз и Ефрон", "ru"},
		{"Толковый словарь", "ru"},
		{"Ожегов (Ru-Ru)", "ru"},
		{"Русско-английский словарь Apresyan", "ru"},

		// A bare code loose in a title is a word, not a language.
		{"A la carte", ""},
		{"What it is", ""},
		{"My Big Dictionary", ""},
		{"The Free On-line Dictionary of Computing", ""},
		{"Merriam-Webster", ""},
		{"Webster's Revised Unabridged Dictionary", ""},
		{"Oxford Dictionary of Idioms", ""},
		{"", ""},
		{"   ", ""},

		// Whole words only, on both sides.
		{"Englishman's Companion", ""}, // "English" inside a longer word
		{"NoRussianHere", ""},
		{"English2", "en"}, // a version digit is not a letter
	} {
		if got := FromTitle(tc.in); got != tc.want {
			t.Errorf("FromTitle(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
