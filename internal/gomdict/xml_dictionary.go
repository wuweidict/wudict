//
// Copyright (C) 2023 Quan Chen <chenquan_act@163.com>
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with this program.  If not, see <http://www.gnu.org/licenses/>.
//
// Derived from medict (https://github.com/terasum/medict),
// internal/libs/go-mdict.
//
// Modified in 2026 by glowinthedark for wudict: largely rewritten; only
// the v3 XML header shape survives.
//
// SPDX-License-Identifier: GPL-3.0-or-later

package go_mdict

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// Dictionary holds the attributes parsed from the MDict header.
//
// v1/v2 headers use the <Dictionary ...> root tag; v3 headers use <ZDB ...>.
// Rather than fighting encoding/xml's strict root-tag matching, we parse the
// attributes with a regex (mirroring the reference Python reader's
// _parse_header) so both root tags work uniformly.
type Dictionary struct {
	GeneratedByEngineVersion string
	RequiredEngineVersion    string
	Encrypted                string
	// NOTE: the real text encoding lives in the "Encoding" attribute; the
	// upstream code mapped this field to "IsUTF16" by mistake, which silently
	// misdetected every non-UTF16 dictionary (e.g. GBK) as UTF-8.
	Encoding             string
	IsUTF16              string
	Format               string
	Stripkey             string
	CreationDate         string
	Compact              string
	Compat               string
	KeyCaseSensitive     string
	Description          string
	Title                string
	DataSourceFormat     string
	StyleSheet           string
	Left2Right           string
	RegisterBy           string
	UUID                 string
	ContentType          string
	DefaultSortingLocale string

	// Attrs is every attribute of the header tag, in file order, entities
	// undone - the typed fields above are the ones the parser acts on, this is
	// what the dictionary declared, including keys nobody here knows.
	Attrs [][2]string
}

// headerAttrRe matches `name="value"` (or `name='value'`) pairs inside the
// header tag. The value is "anything but the closing quote", NOT `.*?`: a
// Description is routinely a small HTML document with literal line breaks
// inside the attribute, and `.` stops at a newline, which silently dropped
// the whole attribute. An XML attribute value cannot contain its own raw
// quote, so this is exact rather than lenient.
var headerAttrRe = regexp.MustCompile(`(\w+)\s*=\s*(?:"([^"]*)"|'([^']*)')`)

// unescapeEntities reverses the five standard XML predefined entities. The
// MDict header never uses numeric character references, so we don't bother
// with them either.
// unescapeEntities undoes XML escaping in an attribute value: the five named
// entities and numeric character references, decimal and hexadecimal, which
// is what MDict's XML reader (CMarkup) undoes. Anything else - an HTML-only
// entity such as &nbsp;, or a malformed reference - is left as written.
func unescapeEntities(s string) string {
	if !strings.Contains(s, "&") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		if s[i] != '&' {
			b.WriteByte(s[i])
			i++
			continue
		}
		semi := strings.IndexByte(s[i:], ';')
		if semi < 0 || semi > 12 {
			b.WriteByte('&')
			i++
			continue
		}
		name := s[i+1 : i+semi]
		if r, ok := xmlEntity(name); ok {
			b.WriteRune(r)
			i += semi + 1
			continue
		}
		b.WriteByte('&')
		i++
	}
	return b.String()
}

// xmlEntity resolves one entity name (the text between & and ;).
func xmlEntity(name string) (rune, bool) {
	switch name {
	case "lt":
		return '<', true
	case "gt":
		return '>', true
	case "amp":
		return '&', true
	case "apos":
		return '\'', true
	case "quot":
		return '"', true
	}
	if len(name) < 2 || name[0] != '#' {
		return 0, false
	}
	base, digits := 10, name[1:]
	if digits[0] == 'x' || digits[0] == 'X' {
		base, digits = 16, digits[1:]
	}
	n, err := strconv.ParseUint(digits, base, 32)
	if err != nil || digits == "" || n == 0 || n > unicode.MaxRune || (n >= 0xD800 && n <= 0xDFFF) {
		return 0, false
	}
	return rune(n), true
}

func parseXMLHeader(xmldata string) (*Dictionary, error) {
	dic := &Dictionary{}
	for _, m := range headerAttrRe.FindAllStringSubmatch(xmldata, -1) {
		key := m[1]
		val := unescapeEntities(m[2] + m[3]) // at most one alternative is non-empty
		dic.Attrs = append(dic.Attrs, [2]string{key, val})
		switch key {
		case "GeneratedByEngineVersion":
			dic.GeneratedByEngineVersion = val
		case "RequiredEngineVersion":
			dic.RequiredEngineVersion = val
		case "Encrypted":
			dic.Encrypted = val
		case "Encoding":
			dic.Encoding = val
		case "IsUTF16":
			dic.IsUTF16 = val
		case "Format":
			dic.Format = val
		case "Stripkey", "StripKey":
			dic.Stripkey = val
		case "creationDate", "CreationDate":
			dic.CreationDate = val
		case "Compact":
			dic.Compact = val
		case "Compat":
			dic.Compat = val
		case "KeyCaseSensitive":
			dic.KeyCaseSensitive = val
		case "Description":
			dic.Description = val
		case "Title":
			dic.Title = val
		case "DataSourceFormat":
			dic.DataSourceFormat = val
		case "StyleSheet":
			dic.StyleSheet = val
		case "Left2Right":
			dic.Left2Right = val
		case "RegisterBy":
			dic.RegisterBy = val
		case "UUID":
			dic.UUID = val
		case "ContentType":
			dic.ContentType = val
		case "DefaultSortingLocale":
			dic.DefaultSortingLocale = val
		}
	}
	return dic, nil
}
