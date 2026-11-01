// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package go_mdict

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"golang.org/x/text/encoding/simplifiedchinese"
)

var (
	fxKeys    = []string{"alpha", "beta", "gamma", "delta", "epsilon"}
	fxRecords = []string{"<b>A</b>", "<b>B</b>", "<b>Γ</b>", "<b>Δ</b>", "<b>E</b>"}
)

func wantAll(t *testing.T, got map[string]string, keys, records []string) {
	t.Helper()
	if len(got) != len(keys) {
		t.Fatalf("read %d entries, want %d: %v", len(got), len(keys), got)
	}
	for i, k := range keys {
		if got[k] != records[i] {
			t.Errorf("%q = %q, want %q", k, got[k], records[i])
		}
	}
}

func TestV2RoundTrip(t *testing.T) {
	path := writeV2(t, v2Spec{attrs: `Encrypted="0" Encoding="UTF-8" Format="Html"`, keys: fxKeys, records: fxRecords})
	md, got, _ := readAll(t, path)
	wantAll(t, got, fxKeys, fxRecords)
	if md.IsPlainText() || md.IsCompact() {
		t.Errorf("plain=%v compact=%v, want neither", md.IsPlainText(), md.IsCompact())
	}
}

// The record section's entry count is not the key count's keeper: MDict never
// compares the two, and cedict_2026-8-7.mdx (465 621 against 560 500 keys)
// reads perfectly well in it.
func TestV2RecordEntryCountIgnored(t *testing.T) {
	path := writeV2(t, v2Spec{attrs: `Encoding="UTF-8" Format="Html"`, keys: fxKeys, records: fxRecords, recordEntries: 3})
	_, got, _ := readAll(t, path)
	wantAll(t, got, fxKeys, fxRecords)
}

// A corrupt key block costs its own keys, not the dictionary, and the key
// before the gap is not handed the records of the keys that were lost.
func TestV2CorruptKeyBlockSkipped(t *testing.T) {
	path := writeV2(t, v2Spec{
		attrs: `Encoding="UTF-8" Format="Html"`, keys: fxKeys, records: fxRecords,
		keysPerBlock: 2, recsPerBlock: 1, corruptKeyBlock: 2,
	})
	_, got, order := readAll(t, path)
	if want := []string{"alpha", "beta", "epsilon"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("keys = %v, want %v", order, want)
	}
	for _, i := range []int{0, 1, 4} {
		if got[fxKeys[i]] != fxRecords[i] {
			t.Errorf("%q = %q, want %q", fxKeys[i], got[fxKeys[i]], fxRecords[i])
		}
	}

	// With every key block unreadable there is no dictionary left to open.
	path = writeV2(t, v2Spec{attrs: `Encoding="UTF-8"`, keys: fxKeys, records: fxRecords, keysPerBlock: 10, corruptKeyBlock: 1})
	md, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := md.BuildIndex(); err == nil {
		t.Fatal("a dictionary whose only key block is corrupt opened")
	}
}

func TestV2Encodings(t *testing.T) {
	gbk := func(s string) []byte {
		b, err := simplifiedchinese.GBK.NewEncoder().Bytes([]byte(s))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	latin1 := func(s string) []byte {
		var b []byte
		for _, r := range s {
			b = append(b, byte(r))
		}
		return b
	}
	cases := []struct {
		name   string
		attrs  string
		encode func(string) []byte
		utf16  bool
		keys   []string
	}{
		{"GBK named", `Encoding="GBK" Format="Html"`, gbk, false, []string{"中文", "汉字"}},
		// MDict's default for a v1/v2 .mdx that names no encoding is GBK.
		{"no Encoding is GBK", `Format="Html"`, gbk, false, []string{"中文", "汉字"}},
		{"empty Encoding is GBK", `Encoding="" Format="Html"`, gbk, false, []string{"中文", "汉字"}},
		{"ISO8859-1", `Encoding="ISO8859-1" Format="Html"`, latin1, false, []string{"café", "naïve", "Øre"}},
		{"UTF-16", `Encoding="UTF-16" Format="Html"`, utf16le, true, []string{"año", "日本"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			records := make([]string, len(tc.keys))
			for i, k := range tc.keys {
				records[i] = "<p>" + k + "</p>"
			}
			path := writeV2(t, v2Spec{attrs: tc.attrs, keys: tc.keys, records: records, encode: tc.encode, utf16: tc.utf16})
			md, got, _ := readAll(t, path)
			if len(got) != len(tc.keys) {
				t.Fatalf("read %d keys, want %d: %v", len(got), len(tc.keys), got)
			}
			for i, k := range tc.keys {
				raw, ok := got[k]
				if !ok {
					t.Fatalf("key %q missing (keys are decoded by gomdict): %v", k, got)
				}
				if text := DecodeText([]byte(raw), md.Encoding()); md.Encoding() != EncodingUtf16 && text != records[i] {
					t.Errorf("%q record = %q, want %q", k, text, records[i])
				} else if md.Encoding() == EncodingUtf16 && raw != records[i] {
					t.Errorf("%q record = %q, want %q", k, raw, records[i])
				}
			}
		})
	}
}

// Encrypted is a bit set read with atoi: bit 1 is the key-block info, whose
// key is in the file; bit 0 is a registered dictionary; "Yes" reads 0.
func TestV2Encrypted(t *testing.T) {
	t.Run("key info encrypted", func(t *testing.T) {
		path := writeV2(t, v2Spec{attrs: `Encrypted="2" Encoding="UTF-8"`, keys: fxKeys, records: fxRecords, encryptKeyInfo: true})
		_, got, _ := readAll(t, path)
		wantAll(t, got, fxKeys, fxRecords)
	})
	t.Run("Yes reads as 0", func(t *testing.T) {
		path := writeV2(t, v2Spec{attrs: `Encrypted="Yes" Encoding="UTF-8"`, keys: fxKeys, records: fxRecords})
		_, got, _ := readAll(t, path)
		wantAll(t, got, fxKeys, fxRecords)
	})
	for _, v := range []string{"1", "3"} {
		t.Run("registered "+v, func(t *testing.T) {
			path := writeV2(t, v2Spec{attrs: `Encrypted="` + v + `" Encoding="UTF-8"`, keys: fxKeys, records: fxRecords, encryptKeyInfo: v == "3"})
			if _, err := New(path); err == nil || !strings.Contains(err.Error(), "registration code") {
				t.Fatalf("err = %v, want the registration refusal", err)
			}
		})
	}
}

func TestV2HeaderFlags(t *testing.T) {
	for _, tc := range []struct {
		attrs          string
		plain, compact bool
	}{
		{`Format="Html" Compact="Yes"`, false, true},
		{`Format="Html" Compact="No" Compat="Yes"`, false, false},
		{`Format="Html" Compat="yes"`, false, true},
		{`Format="Text"`, true, false},
		{`Encoding="UTF-8"`, false, false}, // no Format: HTML, as every reader but MDict renders it
	} {
		path := writeV2(t, v2Spec{attrs: tc.attrs + ` Encoding="UTF-8"`, keys: fxKeys, records: fxRecords})
		md, err := New(path)
		if err != nil {
			t.Fatal(err)
		}
		if md.IsPlainText() != tc.plain || md.IsCompact() != tc.compact {
			t.Errorf("%s: plain=%v compact=%v, want %v %v", tc.attrs, md.IsPlainText(), md.IsCompact(), tc.plain, tc.compact)
		}
	}
}

// A wrong key-block header checksum is noted, not fatal: the counts it
// guards are checked by the reads they drive.
func TestV2MetaChecksum(t *testing.T) {
	path := writeV2(t, v2Spec{attrs: `Encoding="UTF-8" Format="Html"`, keys: fxKeys, records: fxRecords})
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	hl := int(raw[0])<<24 | int(raw[1])<<16 | int(raw[2])<<8 | int(raw[3])
	raw[4+hl+4+40] ^= 1 // the checksum itself
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	_, got, _ := readAll(t, path)
	wantAll(t, got, fxKeys, fxRecords)
}

func TestV3(t *testing.T) {
	cases := []struct {
		name string
		spec v3Spec
	}{
		{"encrypted, with the record index", v3Spec{uuid: "117363fc-ce66-40dd-bcc8-24f52554630f", withIndex: true}},
		{"encrypted, index disagrees", v3Spec{uuid: "117363fc-ce66-40dd-bcc8-24f52554630f", withIndex: true, badIndex: true}},
		{"encrypted, no index", v3Spec{uuid: "117363fc-ce66-40dd-bcc8-24f52554630f"}},
		// Unencrypted blocks carry the checksum of the decompressed data; a
		// reader that checked the compressed bytes refused them.
		{"unencrypted", v3Spec{withIndex: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.spec.keys, tc.spec.records = fxKeys, fxRecords
			path := writeV3(t, tc.spec)
			md, got, _ := readAll(t, path)
			wantAll(t, got, fxKeys, fxRecords)

			// The index and the walk must describe the same blocks.
			f, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			st, _ := f.Stat()
			walk, err := md.recordTableByWalk(f, st.Size())
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(md.v3RecBlocks, walk) {
				t.Errorf("table = %+v, walk = %+v", md.v3RecBlocks, walk)
			}
			idx, err := md.recordTableFromIndex(f)
			switch {
			case tc.spec.withIndex && !tc.spec.badIndex && (err != nil || !reflect.DeepEqual(idx, walk)):
				t.Errorf("index table = %+v (%v), want %+v", idx, err, walk)
			case tc.spec.badIndex && err == nil:
				t.Error("an index that disagrees with the data was accepted")
			}
			if s := md.meta.v3Offsets.sections[v3BlockTypeKeyData]; s == nil || s.attrs["keyCount"] != "5" {
				t.Errorf("KeyData trailer not read: %+v", s)
			}
		})
	}
}
