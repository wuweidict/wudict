// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package dsl

import "testing"

func TestLangZone(t *testing.T) {
	tests := []struct{ in, want string }{
		// The author's token is what a Custom styles rule targets, so it is
		// always there, verbatim; lang= is the resolution on top of it.
		{`[lang name=Russian]рука[/lang]`,
			`<span class="wu-lang" data-lang="Russian" lang="ru">рука</span>`},
		{`[lang id=1049]рука[/lang]`,
			`<span class="wu-lang" data-lang="1049" lang="ru">рука</span>`},
		// Lingvo's collation names: the language is the part in front.
		{`[lang name=SpanishModernSort]mano[/lang]`,
			`<span class="wu-lang" data-lang="SpanishModernSort" lang="es">mano</span>`},
		// Two ids that mean two things to the author stay two selectors.
		{`[lang id=1034]a[/lang]`,
			`<span class="wu-lang" data-lang="1034" lang="es">a</span>`},
		{`[lang id=3082]a[/lang]`,
			`<span class="wu-lang" data-lang="3082" lang="es">a</span>`},
		// Old Lingvo numbering: below every LCID, so no dictionary has to say
		// which namespace it meant.
		{`[lang id=2]рука[/lang]`,
			`<span class="wu-lang" data-lang="2" lang="ru">рука</span>`},
		{`[lang id=23]a[/lang]`,
			`<span class="wu-lang" data-lang="23" lang="sr-Cyrl">a</span>`},
		// Names Lingvo spells its own way.
		{`[lang name=Kirgiz]a[/lang]`,
			`<span class="wu-lang" data-lang="Kirgiz" lang="ky">a</span>`},
		{`[lang name=AzeriLatin]a[/lang]`,
			`<span class="wu-lang" data-lang="AzeriLatin" lang="az-Latn">a</span>`},
		{`[lang name=NorwegianBokmal]a[/lang]`,
			`<span class="wu-lang" data-lang="NorwegianBokmal" lang="nb">a</span>`},
		// Unattributable: the role stays, the claim is not made up.
		{`[lang name=Klingon]a[/lang]`,
			`<span class="wu-lang" data-lang="Klingon">a</span>`},
		{`[lang id=0]a[/lang]`,
			`<span class="wu-lang" data-lang="0">a</span>`},
		{`[lang]a[/lang]`, `<span class="wu-lang">a</span>`},
		// A quote in an attribute closes it; quoteAttr is why it cannot.
		{`[lang name="a\"b"]x[/lang]`,
			`<span class="wu-lang" data-lang="a&quot;b">x</span>`},
	}
	for _, tt := range tests {
		got, _, err := transformBody(tt.in, "")
		if err != nil {
			t.Errorf("transformBody(%q): %v", tt.in, err)
			continue
		}
		if got != tt.want {
			t.Errorf("transformBody(%q)\n got %q\nwant %q", tt.in, got, tt.want)
		}
	}
}
