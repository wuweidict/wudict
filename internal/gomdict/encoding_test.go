// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package go_mdict

import "testing"

func TestEncodingOf(t *testing.T) {
	for _, tc := range []struct {
		attr  string
		want  int
		named bool
	}{
		{"", EncodingUtf8, false},
		{"UTF-8", EncodingUtf8, true},
		{"utf8", EncodingUtf8, true},
		{"GBK", EncodingGb18030, true},
		{"gb2312", EncodingGb18030, true},
		{" GB18030 ", EncodingGb18030, true},
		{"Big5", EncodingBig5, true},
		{"UTF-16", EncodingUtf16, true},
		{"utf-16le", EncodingUtf16, true},
		{"ISO8859-1", EncodingLatin1, true},
		{"iso-8859-1", EncodingLatin1, true},
		{"x-unknown", EncodingUtf8, false},
	} {
		got, named := encodingOf(tc.attr)
		if got != tc.want || named != tc.named {
			t.Errorf("encodingOf(%q) = %d, %v; want %d, %v", tc.attr, got, named, tc.want, tc.named)
		}
	}
}

func TestContentTypeOf(t *testing.T) {
	for _, tc := range []struct {
		attr string
		mdd  bool
		want int
	}{
		{"Html", false, ContentHTML},
		{"text", false, ContentText},
		{"Xml", false, ContentXML},
		{"Binary", true, ContentBinary},
		{"", false, ContentHTML}, // MDict would say Text; see contentTypeOf
		{"", true, ContentBinary},
		{"bogus", false, ContentHTML},
	} {
		if got := contentTypeOf(tc.attr, tc.mdd); got != tc.want {
			t.Errorf("contentTypeOf(%q, mdd=%v) = %d, want %d", tc.attr, tc.mdd, got, tc.want)
		}
	}
}

// TestLeadingNumbers pins the atoi/atof readings MDict gives Encrypted and
// RequiredEngineVersion.
func TestLeadingNumbers(t *testing.T) {
	for in, want := range map[string]int{
		"": 0, "No": 0, "Yes": 0, "0": 0, "2": 2, "3": 3, " 1": 1, "2x": 2, "-1": -1, "+2": 2,
	} {
		if got := leadingInt(in); got != want {
			t.Errorf("leadingInt(%q) = %d, want %d", in, got, want)
		}
	}
	for _, tc := range []struct {
		in   string
		want float64
		ok   bool
	}{
		{"2.0", 2, true}, {"1.2", 1.2, true}, {"3.0", 3, true}, {" 3 ", 3, true},
		{"2.0 beta", 2, true}, {"2.", 2, true}, {"", 0, false}, {"v2", 0, false},
	} {
		got, ok := leadingFloat(tc.in)
		if ok != tc.ok || got != tc.want {
			t.Errorf("leadingFloat(%q) = %v, %v; want %v, %v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestDecodeText(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  []byte
		enc  int
		want string
	}{
		{"utf-8", []byte("año"), EncodingUtf8, "año"},
		{"utf-16", utf16le("año"), EncodingUtf16, "año"},
		{"gbk", []byte{0xd6, 0xd0, 0xce, 0xc4}, EncodingGb18030, "中文"},
		{"big5", []byte{0xa4, 0xa4, 0xa4, 0xe5}, EncodingBig5, "中文"},
		{"latin-1", []byte{'c', 'a', 'f', 0xe9}, EncodingLatin1, "café"},
	} {
		if got := DecodeText(tc.raw, tc.enc); got != tc.want {
			t.Errorf("%s: DecodeText = %q, want %q", tc.name, got, tc.want)
		}
	}
}
