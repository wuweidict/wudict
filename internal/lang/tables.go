// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package lang

// languages is the recognition table: ISO 639-1 code, the ISO 639-2 B and T
// three-letter codes, and the English name(s).
//
// It is deliberately NOT the whole of ISO 639-3. A table that resolves every
// three-letter sequence somebody has ever registered turns "big-dictionary.mdx"
// into Biangai, and a dictionary attributed to a language wudict has no
// lemmatizer for is a dictionary that gets NO lemmatization - worse than one
// that resolves to nothing and falls back to English (see internal/server).
// Breadth here therefore has a cost, and the table covers the languages a
// dictionary is plausibly labelled with rather than every code that exists.
//
// Adding a language is one line and costs nothing else: nothing in the lookup
// path knows how long this list is.
var languages = []struct {
	code2 string
	code3 []string // 639-2/B first, then /T where they differ
	names []string
}{
	// English is compiled in; the rest of these have published lemma data
	// (`wudict lemmas list`). See internal/morph.
	{"en", []string{"eng"}, []string{"English"}},
	{"de", []string{"ger", "deu"}, []string{"German"}},
	{"fr", []string{"fre", "fra"}, []string{"French"}},
	{"es", []string{"spa"}, []string{"Spanish", "Castilian"}},
	{"it", []string{"ita"}, []string{"Italian"}},
	{"ru", []string{"rus"}, []string{"Russian"}},

	// Everything else: recognised so it is NOT lemmatized as English.
	{"nl", []string{"dut", "nld"}, []string{"Dutch", "Flemish"}},
	{"pt", []string{"por"}, []string{"Portuguese"}},
	{"pl", []string{"pol"}, []string{"Polish"}},
	{"uk", []string{"ukr"}, []string{"Ukrainian"}},
	{"cs", []string{"cze", "ces"}, []string{"Czech"}},
	{"sk", []string{"slo", "slk"}, []string{"Slovak"}},
	{"sl", []string{"slv"}, []string{"Slovenian", "Slovene"}},
	{"hr", []string{"hrv"}, []string{"Croatian"}},
	{"sr", []string{"srp"}, []string{"Serbian"}},
	{"bs", []string{"bos"}, []string{"Bosnian"}},
	{"bg", []string{"bul"}, []string{"Bulgarian"}},
	{"mk", []string{"mac", "mkd"}, []string{"Macedonian"}},
	{"ro", []string{"rum", "ron"}, []string{"Romanian", "Moldavian"}},
	{"hu", []string{"hun"}, []string{"Hungarian"}},
	{"el", []string{"gre", "ell"}, []string{"Greek"}},
	{"tr", []string{"tur"}, []string{"Turkish"}},
	{"fi", []string{"fin"}, []string{"Finnish"}},
	{"sv", []string{"swe"}, []string{"Swedish"}},
	{"no", []string{"nor"}, []string{"Norwegian"}},
	{"nb", []string{"nob"}, []string{"Norwegian Bokmal"}},
	{"nn", []string{"nno"}, []string{"Norwegian Nynorsk"}},
	{"da", []string{"dan"}, []string{"Danish"}},
	{"is", []string{"ice", "isl"}, []string{"Icelandic"}},
	{"fo", []string{"fao"}, []string{"Faroese"}},
	{"et", []string{"est"}, []string{"Estonian"}},
	{"lv", []string{"lav"}, []string{"Latvian"}},
	{"lt", []string{"lit"}, []string{"Lithuanian"}},
	{"be", []string{"bel"}, []string{"Belarusian"}},
	{"ga", []string{"gle"}, []string{"Irish"}},
	{"gd", []string{"gla"}, []string{"Scottish Gaelic"}},
	{"cy", []string{"wel", "cym"}, []string{"Welsh"}},
	{"gv", []string{"glv"}, []string{"Manx"}},
	{"br", []string{"bre"}, []string{"Breton"}},
	{"ca", []string{"cat"}, []string{"Catalan", "Valencian"}},
	{"eu", []string{"baq", "eus"}, []string{"Basque"}},
	{"gl", []string{"glg"}, []string{"Galician"}},
	{"ast", nil, []string{"Asturian"}}, // no 639-1; keyed by its 639-3 code
	{"sq", []string{"alb", "sqi"}, []string{"Albanian"}},
	{"mt", []string{"mlt"}, []string{"Maltese"}},
	{"lb", []string{"ltz"}, []string{"Luxembourgish"}},
	{"fy", []string{"fry"}, []string{"Western Frisian", "Frisian"}},
	{"af", []string{"afr"}, []string{"Afrikaans"}},
	{"la", []string{"lat"}, []string{"Latin"}},
	{"grc", nil, []string{"Ancient Greek"}}, // no 639-1; keyed by its 639-3 code
	{"eo", []string{"epo"}, []string{"Esperanto"}},
	{"yi", []string{"yid"}, []string{"Yiddish"}},
	{"he", []string{"heb"}, []string{"Hebrew"}},
	{"ar", []string{"ara"}, []string{"Arabic"}},
	{"fa", []string{"per", "fas"}, []string{"Persian", "Farsi"}},
	{"ur", []string{"urd"}, []string{"Urdu"}},
	{"hi", []string{"hin"}, []string{"Hindi"}},
	{"bn", []string{"ben"}, []string{"Bengali"}},
	{"pa", []string{"pan"}, []string{"Punjabi", "Panjabi"}},
	{"ta", []string{"tam"}, []string{"Tamil"}},
	{"te", []string{"tel"}, []string{"Telugu"}},
	{"ml", []string{"mal"}, []string{"Malayalam"}},
	{"kn", []string{"kan"}, []string{"Kannada"}},
	{"si", []string{"sin"}, []string{"Sinhala", "Sinhalese"}},
	{"ne", []string{"nep"}, []string{"Nepali"}},
	{"sa", []string{"san"}, []string{"Sanskrit"}},
	{"th", []string{"tha"}, []string{"Thai"}},
	{"lo", []string{"lao"}, []string{"Lao"}},
	{"km", []string{"khm"}, []string{"Khmer"}},
	{"my", []string{"bur", "mya"}, []string{"Burmese"}},
	{"vi", []string{"vie"}, []string{"Vietnamese"}},
	{"id", []string{"ind"}, []string{"Indonesian"}},
	{"ms", []string{"may", "msa"}, []string{"Malay"}},
	{"tl", []string{"tgl"}, []string{"Tagalog"}},
	{"ja", []string{"jpn"}, []string{"Japanese"}},
	{"ko", []string{"kor"}, []string{"Korean"}},
	{"zh", []string{"chi", "zho"}, []string{"Chinese"}},
	{"hy", []string{"arm", "hye"}, []string{"Armenian"}},
	{"ka", []string{"geo", "kat"}, []string{"Georgian"}},
	{"az", []string{"aze"}, []string{"Azerbaijani"}},
	{"kk", []string{"kaz"}, []string{"Kazakh"}},
	{"ky", []string{"kir"}, []string{"Kyrgyz"}},
	{"uz", []string{"uzb"}, []string{"Uzbek"}},
	{"tk", []string{"tuk"}, []string{"Turkmen"}},
	{"tg", []string{"tgk"}, []string{"Tajik"}},
	{"tt", []string{"tat"}, []string{"Tatar"}},
	{"ba", []string{"bak"}, []string{"Bashkir"}},
	{"cv", []string{"chv"}, []string{"Chuvash"}},
	{"mn", []string{"mon"}, []string{"Mongolian"}},
	{"sw", []string{"swa"}, []string{"Swahili"}},
	{"am", []string{"amh"}, []string{"Amharic"}},
	{"ha", []string{"hau"}, []string{"Hausa"}},
	{"yo", []string{"yor"}, []string{"Yoruba"}},
	{"zu", []string{"zul"}, []string{"Zulu"}},
}
