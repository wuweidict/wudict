// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package dict

import (
	"os"
	"path/filepath"
	"testing"
)

// registerRealFormats gives the test binary the vocabulary a real build has.
// ClassifyName asks the REGISTRY what a main file is, and the registry is
// filled by the format packages' init() - which this package cannot import
// without a cycle. Registration is additive and idempotent, so doing it here
// costs the other tests in the package nothing.
func registerRealFormats(t *testing.T) {
	t.Helper()
	open := func(path string) (Dictionary, error) { return fakeDict{}, nil }
	for _, ext := range []string{".mdx", ".ifo", ".dsl", ".dsl.dz", ".slob", ".bgl", ".zim", ".zimaa"} {
		RegisterFormat(ext, open)
	}
	RegisterInspectable(".mdd", open)
}

func TestClassifyName(t *testing.T) {
	registerRealFormats(t)

	cases := []struct {
		name string
		want Kind
	}{
		// main files, however they are spelled and wherever they sit
		{"Oxford.mdx", KindMain},
		{"Oxford.MDX", KindMain},
		{"Dicts/English/Oxford.mdx", KindMain},
		{`Dicts\English\Oxford.mdx`, KindMain}, // zip written on Windows
		{"star.ifo", KindMain},
		{"x.slob", KindMain},
		{"x.bgl", KindMain},
		{"wiki.zim", KindMain},
		{"ru-en.dsl", KindMain},
		{"ru-en.dsl.dz", KindMain},

		// MDX resources: openable by name, never a dictionary
		{"Oxford.mdd", KindCompanion},
		{"Oxford.1.mdd", KindCompanion},
		{"Oxford.12.mdd", KindCompanion},

		// StarDict's index, articles and synonyms, every spelling
		{"star.idx", KindCompanion},
		{"star.idx.gz", KindCompanion},
		{"star.idx.oft", KindCompanion},
		{"star.dict", KindCompanion},
		{"star.dict.dz", KindCompanion},
		{"star.syn.dz", KindCompanion},
		{"star.ann", KindCompanion},
		// the shared resource archive is a fixed name, not a stem suffix
		{"res.zip", KindCompanion},
		{"RES.ZIP", KindCompanion},
		{"sd/res.zip", KindCompanion},
		// fonts: a dictionary's own, and the folder's
		{"Oxford.ttf", KindCompanion},
		{"Oxford.woff2", KindCompanion},
		{"font.otf", KindCompanion},
		{"Oxford.ttc", KindOther},

		// DSL: the abbreviation glossary is part of its parent, and the media
		// zip must not read as the ordinary archive it technically is
		{"ru-en_abrv.dsl", KindCompanion},
		{"ru-en_abrv.dsl.dz", KindCompanion},
		{"ru-en.dsl.files.zip", KindCompanion},
		{"ru-en.files.zip", KindCompanion},
		{"ru-en.dsl.ann", KindCompanion},
		// ...but one with nothing in front of it names no parent, so it is
		// somebody's own abbreviation dictionary (cf. IsAbbrevCompanion)
		{"_abrv.dsl", KindMain},
		{"_abrv.dsl.dz", KindMain},
		{"d/_abrv.dsl", KindMain},

		// everything an archive carries that is none of our business
		{"readme.txt", KindOther},
		{"Oxford.zip", KindOther},
		{"Oxford.7z", KindOther},
		{"cover.jpg", KindOther},
		{"Oxford", KindOther},
		{"", KindOther},
		{"dir/", KindOther},
		// a prepared library folder is a shape intake does not group yet, and
		// half of one is worse than none
		{"MyDict/text.db", KindOther},
		{"MyDict/media.db", KindOther},
		// macOS packs a resource fork beside every entry; it would otherwise
		// classify as a perfectly good MDX
		{"__MACOSX/._Oxford.mdx", KindOther},
		{".DS_Store", KindOther},
		{".hidden.mdx", KindOther},
	}
	for _, c := range cases {
		if got := ClassifyName(c.name); got != c.want {
			t.Errorf("ClassifyName(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}

// An extension this build did not link must not be offered: intake would
// install a dictionary that nothing can then open.
func TestClassifyNameUnregisteredFormatIsOther(t *testing.T) {
	if got := ClassifyName("x.nosuchformat"); got != KindOther {
		t.Errorf("unregistered extension = %v, want KindOther", got)
	}
}

// The name-only tables and the stat-based ones are the same tables; this is
// the assertion that keeps them from drifting apart if one is edited alone.
func TestCompanionVocabularyCoversStatBasedTables(t *testing.T) {
	registerRealFormats(t)
	for _, ext := range mainExts {
		for _, suf := range append(CompanionSuffixes(ext), MediaSuffixes(ext)...) {
			if got := ClassifyName("dict" + suf); got != KindCompanion {
				t.Errorf("%q companion %q classifies as %v, want KindCompanion", ext, suf, got)
			}
		}
		for _, group := range RequiredCompanions(ext) {
			for _, suf := range group {
				if got := ClassifyName("dict" + suf); got != KindCompanion {
					t.Errorf("%q required companion %q classifies as %v", ext, suf, got)
				}
			}
		}
	}
}

// Hidden directories are not part of anyone's library, and intake's staging
// directory is one: a scan racing an extraction must not offer a dictionary
// whose files are still arriving.
func TestDiscoverSkipsHiddenDirectories(t *testing.T) {
	RegisterFormat(".faketest", func(path string) (Dictionary, error) { return fakeDict{}, nil })
	dir := t.TempDir()
	mk(t, dir, "real.faketest")
	mk(t, dir, ".wudict-intake/job1/half.faketest")
	mk(t, dir, ".git/objects/x.faketest")

	got, err := Discover(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("Discover = %v, want only the visible dictionary", got)
	}
}

// mk creates dir/rel and every parent it needs.
func mk(t *testing.T, dir, rel string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCompanionStem(t *testing.T) {
	registerRealFormats(t)
	cases := []struct{ name, want string }{
		{"Oxford.mdd", "Oxford"},
		{"Oxford.1.mdd", "Oxford"},
		{"Oxford.12.mdd", "Oxford"},
		{"d/Oxford.mdd", "Oxford"},
		// a dot in the dictionary's own name is not a part number
		{"Collins.v2.mdd", "Collins.v2"},
		{"star.idx", "star"},
		{"star.idx.gz", "star"},
		{"star.dict.dz", "star"},
		{"star.syn", "star"},
		{"ru-en_abrv.dsl", "ru-en"},
		{"ru-en_abrv.dsl.dz", "ru-en"},
		// the media zip is written against the stem, the main file, or the
		// main file including its compression suffix
		{"ru-en.files.zip", "ru-en"},
		{"ru-en.dsl.files.zip", "ru-en"},
		{"ru-en.dsl.dz.files.zip", "ru-en"},
		{"ru-en.dsl.ann", "ru-en"},
		// a dictionary's own font, under every format's stem
		{"Oxford.ttf", "Oxford"},
		{"Oxford.WOFF2", "Oxford"},
		{"notes.wudict.otf", "notes.wudict"},
		// belongs to the folder, not to a stem
		{"res.zip", ""},
		{"font.ttf", ""},
		{"FONT.woff", ""},
		// not companions at all
		{"Oxford.mdx", ""},
		{"readme.txt", ""},
		{"_abrv.dsl", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := CompanionStem(c.name); got != c.want {
			t.Errorf("CompanionStem(%q) = %q, want %q", c.name, got, c.want)
		}
	}
}

// A repacked MDX ships its stylesheet and scripts loose beside the .mdx, so
// they are companions by name - carried with the dictionary, never offered as
// one. The stem rule is what keeps a neighbouring site's "style.css" out: it
// names a dictionary that is not there.
func TestClassifyNameMDXAssets(t *testing.T) {
	registerRealFormats(t)
	for _, name := range []string{"LDOCE6.css", "LDOCE6.js", "d/OED.CSS"} {
		if got := ClassifyName(name); got != KindCompanion {
			t.Errorf("ClassifyName(%q) = %v, want KindCompanion", name, got)
		}
	}
	for _, c := range []struct{ name, want string }{
		{"LDOCE6.css", "LDOCE6"},
		{"LDOCE6.js", "LDOCE6"},
		{"d/OED.css", "OED"},
		// a dot in the dictionary's own name is not an extension to trim
		{"Collins.v2.css", "Collins.v2"},
	} {
		if got := CompanionStem(c.name); got != c.want {
			t.Errorf("CompanionStem(%q) = %q, want %q", c.name, got, c.want)
		}
	}
}

// The download shelf is a VISIBLE folder on purpose - somebody who fetched a
// 2 GB bundle has to be able to find it - and is still not part of the
// library: the copy installed under its own folder is. Walking it is what made
// one import list the same dictionary twice.
func TestDiscoverSkipsTheDownloadShelf(t *testing.T) {
	RegisterFormat(".faketest", func(path string) (Dictionary, error) { return fakeDict{}, nil })
	dir := t.TempDir()
	mk(t, dir, "OED/OED.faketest")
	mk(t, dir, DownloadDirName+"/OED.faketest")
	// A "Downloads" folder somebody made further down is theirs, and skipping
	// it would lose real dictionaries. Only the direct child is the shelf.
	mk(t, dir, "shelf/"+DownloadDirName+"/Other.faketest")

	got, err := Discover(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("Discover = %v, want the installed copy and the nested one", got)
	}
	for _, p := range got {
		if filepath.Dir(p) == filepath.Join(dir, DownloadDirName) {
			t.Fatalf("Discover returned the download shelf entry %q", p)
		}
	}
}

// A split ZIM's first part is the dictionary; the parts after it are its
// companions, named after the same stem, and never offered on their own.
func TestClassifySplitZIM(t *testing.T) {
	registerRealFormats(t)
	for name, want := range map[string]Kind{
		"Wiki.zimaa": KindMain, "Wiki.zimab": KindCompanion, "Wiki.zimzz": KindCompanion,
		"Wiki.ZIMAC": KindCompanion, "Wiki.zim": KindMain, "Wiki.zimx": KindOther,
	} {
		if got := ClassifyName(name); got != want {
			t.Errorf("ClassifyName(%q) = %v, want %v", name, got, want)
		}
	}
	for _, name := range []string{"Wiki.zimab", "Wiki.zimbe"} {
		if got := CompanionStem(name); got != "Wiki" {
			t.Errorf("CompanionStem(%q) = %q, want Wiki", name, got)
		}
	}
	if Name("dir/Wiki_en_all.zimaa") != "Wiki_en_all" || Stem("dir/Wiki.zimaa") != "dir/Wiki" {
		t.Errorf("Name/Stem of a first part = %q, %q", Name("dir/Wiki_en_all.zimaa"), Stem("dir/Wiki.zimaa"))
	}
}
