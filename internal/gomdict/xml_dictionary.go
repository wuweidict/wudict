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

package go_mdict

import (
	"regexp"
	"strings"
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
	Encoding          string
	IsUTF16           string
	Format            string
	Stripkey          string
	CreationDate      string
	Compact           string
	Compat            string
	KeyCaseSensitive  string
	Description       string
	Title             string
	DataSourceFormat  string
	StyleSheet        string
	Left2Right        string
	RegisterBy        string
	UUID              string
	ContentType       string
	DefaultSortingLocale string
}

// headerAttrRe matches `name="value"` pairs inside the header tag.
var headerAttrRe = regexp.MustCompile(`(\w+)="(.*?)"`)

// unescapeEntities reverses the five standard XML predefined entities. The
// MDict header never uses numeric character references, so we don't bother
// with them either.
func unescapeEntities(s string) string {
	r := strings.NewReplacer(
		"&lt;", "<",
		"&gt;", ">",
		"&amp;", "&",
		"&apos;", "'",
		"&quot;", `"`,
	)
	return r.Replace(s)
}

func parseXMLHeader(xmldata string) (*Dictionary, error) {
	dic := &Dictionary{}
	for _, m := range headerAttrRe.FindAllStringSubmatch(xmldata, -1) {
		if len(m) != 3 {
			continue
		}
		key := m[1]
		val := unescapeEntities(m[2])
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
