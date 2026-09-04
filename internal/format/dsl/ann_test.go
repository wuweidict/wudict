// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package dsl

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wuweidict/wudict/internal/dict"
)

// writeAnn puts a .dsl and its annotation sidecar in one folder, which is what
// makes them a pair - writeDSL gives every file a temp dir of its own.
func writeAnn(t *testing.T, dslName, annName string, ann []byte) string {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, dslName)
	if err := os.WriteFile(src, []byte("#NAME \"X\"\n\ndog\n\tcanine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if annName != "" {
		if err := os.WriteFile(filepath.Join(dir, annName), ann, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return src
}

func utf16be(s string) []byte {
	var b bytes.Buffer
	b.Write([]byte{0xFE, 0xFF})
	for _, r := range s {
		binary.Write(&b, binary.BigEndian, uint16(r))
	}
	return b.Bytes()
}

func TestAnnPath(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"/d/Lingvo.dsl", "/d/Lingvo.ann"},
		{"/d/Lingvo.dsl.dz", "/d/Lingvo.ann"},
		{"/d/Lingvo.DSL", "/d/Lingvo.ann"},
		{"/d/Lingvo.DSL.DZ", "/d/Lingvo.ann"},
		// The companion is not a dictionary (D97), so nothing asks it for an
		// About - but the mapping must not invent one either.
		{"/d/Lingvo_abrv.dsl", "/d/Lingvo_abrv.ann"},
		{"/d/notadsl.mdx", ""},
	} {
		got, ok := annPath(c.in)
		if c.want == "" {
			if ok {
				t.Errorf("annPath(%q) = %q, want no path", c.in, got)
			}
			continue
		}
		if !ok || got != c.want {
			t.Errorf("annPath(%q) = %q,%v want %q", c.in, got, ok, c.want)
		}
	}
}

func TestLoadAnnEncodings(t *testing.T) {
	const text = "Англо-русский словарь\n© ABBYY, 2011"
	for _, c := range []struct {
		name string
		data []byte
	}{
		{"utf16le bom", utf16le(text)},
		{"utf16be bom", utf16be(text)},
		{"utf8", []byte(text)},
		{"utf8 bom", append([]byte{0xEF, 0xBB, 0xBF}, text...)},
		{"utf16le no bom", utf16le(text)[2:]},
		// A Windows-authored file: CRLF must not survive into the text.
		{"crlf", []byte(strings.ReplaceAll(text, "\n", "\r\n"))},
	} {
		t.Run(c.name, func(t *testing.T) {
			src := writeAnn(t, "X.dsl", "X.ann", c.data)
			a, ok := loadAnn(src)
			if !ok {
				t.Fatal("loadAnn reported no annotation")
			}
			if len(a.Sections) != 1 || a.Sections[0].Lang != "" {
				t.Fatalf("sections = %+v, want one unlabelled", a.Sections)
			}
			if a.Sections[0].Text != text {
				t.Errorf("text = %q, want %q", a.Sections[0].Text, text)
			}
			if a.HTML {
				t.Error("HTML must be false: the server does the escaping")
			}
			if filepath.Base(a.Source) != "X.ann" {
				t.Errorf("Source = %q", a.Source)
			}
		})
	}
}

func TestLoadAnnSections(t *testing.T) {
	multi := "#LANGUAGE \"English\"\nAn explanatory dictionary.\nSecond line.\n" +
		"#LANGUAGE \"Russian\"\nТолковый словарь.\n"
	a, ok := loadAnn(writeAnn(t, "B.dsl", "B.ann", utf16le(multi)))
	if !ok {
		t.Fatal("no annotation")
	}
	// EVERY section, in file order: goldendict picks one by system locale,
	// which hides the Russian blurb of a Ru-Ru dictionary from an English UI.
	if len(a.Sections) != 2 {
		t.Fatalf("sections = %+v, want 2", a.Sections)
	}
	if a.Sections[0].Lang != "English" || a.Sections[0].Text != "An explanatory dictionary.\nSecond line." {
		t.Errorf("section 0 = %+v", a.Sections[0])
	}
	if a.Sections[1].Lang != "Russian" || a.Sections[1].Text != "Толковый словарь." {
		t.Errorf("section 1 = %+v", a.Sections[1])
	}

	// A #LANGUAGE that is not the first line is prose, exactly as goldendict
	// reads it - the section split is a property of the file's first line.
	a2, ok := loadAnn(writeAnn(t, "C.dsl", "C.ann", []byte("Blurb.\n#LANGUAGE \"English\"\nmore\n")))
	if !ok {
		t.Fatal("no annotation")
	}
	if len(a2.Sections) != 1 || a2.Sections[0].Lang != "" ||
		!strings.Contains(a2.Sections[0].Text, "#LANGUAGE") {
		t.Errorf("sections = %+v, want one unlabelled block keeping the line", a2.Sections)
	}

	// The tag needs a separator after it. Without one, a file opening with
	// "#LANGUAGES SUPPORTED" was read as a section named "SUPPORTED".
	a3, ok := loadAnn(writeAnn(t, "D.dsl", "D.ann", []byte("#LANGUAGES SUPPORTED\nEnglish, Russian.\n")))
	if !ok {
		t.Fatal("no annotation")
	}
	if len(a3.Sections) != 1 || a3.Sections[0].Lang != "" {
		t.Errorf("sections = %+v, want one unlabelled block", a3.Sections)
	}
	// and a quoted value with no space still opens one, which is how the
	// directive is actually written in the wild.
	a4, ok := loadAnn(writeAnn(t, "Q.dsl", "Q.ann", []byte("#LANGUAGE\"English\"\nBlurb.\n")))
	if !ok {
		t.Fatal("no annotation")
	}
	if len(a4.Sections) != 1 || a4.Sections[0].Lang != "English" {
		t.Errorf("sections = %+v, want one English section", a4.Sections)
	}
}

func TestLoadAnnMisses(t *testing.T) {
	if _, ok := loadAnn(writeAnn(t, "N.dsl", "", nil)); ok {
		t.Error("a dictionary with no sidecar must report no annotation")
	}
	if _, ok := loadAnn(writeAnn(t, "E.dsl", "E.ann", nil)); ok {
		t.Error("an empty sidecar is not an annotation")
	}
	if _, ok := loadAnn(writeAnn(t, "W.dsl", "W.ann", []byte("  \n\n\t\n"))); ok {
		t.Error("a whitespace-only sidecar is not an annotation")
	}
	if _, ok := loadAnn(writeAnn(t, "H.dsl", "H.ann", []byte("#LANGUAGE \"English\"\n"))); ok {
		t.Error("a sidecar of headings alone is not an annotation")
	}
	if _, ok := loadAnn("/nonexistent/dir/Z.dsl"); ok {
		t.Error("a missing source must report no annotation")
	}
	if _, ok := loadAnn(writeAnn(t, "M.mdx", "M.ann", []byte("text"))); ok {
		t.Error("only a .dsl source has a .ann rule")
	}
}

// The _abrv companion is never asked for an About, and if something did ask,
// it must not be handed its PARENT's annotation.
func TestLoadAnnAbbrevCompanion(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"P.dsl", "P_abrv.dsl"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("#NAME \"X\"\n\na\n\tb\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "P.ann"), []byte("parent blurb"), 0o644); err != nil {
		t.Fatal(err)
	}
	if a, ok := loadAnn(filepath.Join(dir, "P_abrv.dsl")); ok {
		t.Errorf("the companion picked up %+v", a)
	}
}

func TestLoadAnnBounds(t *testing.T) {
	big := strings.Repeat("x", maxAnnBytes+4096)
	a, ok := loadAnn(writeAnn(t, "L.dsl", "L.ann", []byte(big)))
	if !ok {
		t.Fatal("an oversized sidecar is truncated, not dropped")
	}
	got := a.Sections[0].Text
	if len(got) > maxAnnBytes+8 {
		t.Errorf("text is %d bytes, want it cut at %d", len(got), maxAnnBytes)
	}
	if !strings.HasSuffix(got, "…") {
		t.Error("a truncated annotation must say so")
	}
}

// The provider is wired from init(), so the whole chain works through the
// registry and not only through the unexported function.
func TestAnnRegistered(t *testing.T) {
	src := writeAnn(t, "R.dsl", "R.ann", utf16le("Registered."))
	a, ok := dict.AboutFor("dsl", src)
	if !ok || len(a.Sections) != 1 || a.Sections[0].Text != "Registered." {
		t.Fatalf("dict.AboutFor = %+v, %v", a, ok)
	}
}
