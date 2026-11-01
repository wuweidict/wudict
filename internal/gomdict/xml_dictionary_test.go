package go_mdict

import "testing"

func TestParseXMLHeader(t *testing.T) {
	tests := []struct {
		name, xml, title, desc string
	}{
		{
			name:  "single line",
			xml:   `<Dictionary Title="T" Description="&lt;p&gt;one&lt;/p&gt;"/>`,
			title: "T",
			desc:  "<p>one</p>",
		},
		{
			// the shape mdict-mdx-maker writes: a literal newline inside the value
			name:  "multi-line description",
			xml:   "<Dictionary Encoding=\"UTF-8\" Description=\"&lt;p&gt;a&lt;/p&gt;\n&lt;p&gt;b&lt;/p&gt;\" Title=\"Elhuyar en-eu\" StyleSheet=\"\"/>",
			title: "Elhuyar en-eu",
			desc:  "<p>a</p>\n<p>b</p>",
		},
		{
			name:  "CRLF and quoted text inside value",
			xml:   "<Dictionary Description=\"x &quot;y&quot;\r\nz='w'\" Title=\"T\"/>",
			title: "T",
			desc:  "x \"y\"\r\nz='w'",
		},
		{
			name:  "single-quoted attributes",
			xml:   `<Dictionary Title='T' Description='say "hi"'/>`,
			title: "T",
			desc:  `say "hi"`,
		},
		{
			name:  "numeric character references",
			xml:   `<Dictionary Title="Learner&#x27;s &#169;" Description="a&#10;b &#xD; &nbsp; &#0; &bogus"/>`,
			title: "Learner's ©",
			desc:  "a\nb \r &nbsp; &#0; &bogus",
		},
		{
			name:  "empty description",
			xml:   `<Dictionary Description="" Title="T"/>`,
			title: "T",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, err := parseXMLHeader(tt.xml)
			if err != nil {
				t.Fatal(err)
			}
			if d.Title != tt.title {
				t.Errorf("Title = %q, want %q", d.Title, tt.title)
			}
			if d.Description != tt.desc {
				t.Errorf("Description = %q, want %q", d.Description, tt.desc)
			}
		})
	}
}
