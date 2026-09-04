// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package dsl

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/encoding/unicode/utf32"
	"golang.org/x/text/transform"

	"github.com/wuweidict/wudict/internal/dict"
	"github.com/wuweidict/wudict/internal/lang"
)

// Reader is the sequential ingest scan over a .dsl / .dsl.dz file.
// Entry blocks: headword line(s) at column 0, indented body lines;
// "@ sub-headword" lines split off sub-entries (linked from the parent).
type Reader struct {
	f       *os.File
	scanner *bufio.Scanner
	meta    dict.Meta
	header  map[string]string
	path    string

	// The abbreviation companion is parsed on the first entry, not in
	// NewReader: Open builds a Reader for its name alone on every open of an
	// already-prepared dictionary, and that path must not pay for a file it
	// will never transform with.
	abbrevOnce sync.Once
	abbrev     *abbrevMap
	// plainBody is set for the scan of a companion itself: its articles are
	// the tooltip text, so they must not be prefixed with their own headword,
	// and it has no companion of its own to absorb.
	plainBody bool

	buffered  []string     // lookahead lines
	pending   []dict.Entry // sub-entries queued behind the main entry
	scanCount int
	eof       bool
}

func NewReader(path string) (*Reader, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	r := &Reader{f: f, header: map[string]string{}, path: path}
	if err := r.init(path); err != nil {
		f.Close()
		return nil, err
	}
	return r, nil
}

// newAbbrevReader opens a DSL as an abbreviation glossary rather than as a
// dictionary. Same parser, two suppressions - see Reader.plainBody.
func newAbbrevReader(path string) (*Reader, error) {
	r, err := NewReader(path)
	if err != nil {
		return nil, err
	}
	r.plainBody = true
	return r, nil
}

// abbrevs resolves this dictionary's abbreviation companion, once.
func (r *Reader) abbrevs() *abbrevMap {
	if r.plainBody {
		return nil
	}
	r.abbrevOnce.Do(func() { r.abbrev = loadAbbrev(r.path) })
	return r.abbrev
}

// ExtraMeta records what was absorbed, so a later run can tell a text.db built
// with this companion from one built without it, or with an older copy of it.
// Written only when a companion was actually found: a dictionary that has none
// records nothing, and therefore never looks stale for the lack of it.
func (r *Reader) ExtraMeta() map[string]string {
	a := r.abbrevs()
	if a == nil {
		return nil
	}
	return map[string]string{
		"abbrev_path":  a.path,
		"abbrev_size":  strconv.FormatInt(a.size, 10),
		"abbrev_mtime": a.mtime.Format(time.RFC3339),
		"abbrev_count": strconv.Itoa(a.count),
	}
}

// decodedScanner turns an already-open Lingvo file into a line scanner: gunzip
// when gzipped, then whatever charset detectEncoding sniffs. Shared by the
// dictionary reader and by the .ann annotation loader (ann.go), so a Lingvo
// file - which is UTF-16LE far more often than not - is decoded in exactly one
// place. path is used for the error text only.
func decodedScanner(f *os.File, path string, gzipped bool) (*bufio.Scanner, error) {
	var src io.Reader = f
	if gzipped {
		gz, err := gzip.NewReader(f)
		if err != nil {
			return nil, fmt.Errorf("dsl %s: %w", path, err)
		}
		src = gz
	}
	br := bufio.NewReaderSize(src, 1<<20)
	enc, err := detectEncoding(br)
	if err != nil {
		return nil, fmt.Errorf("dsl %s: %w", path, err)
	}
	sc := bufio.NewScanner(transform.NewReader(br, enc.NewDecoder()))
	sc.Buffer(make([]byte, 0, 1<<20), 1<<24)
	return sc, nil
}

func (r *Reader) init(path string) error {
	sc, err := decodedScanner(r.f, path, strings.HasSuffix(strings.ToLower(path), ".dz"))
	if err != nil {
		return err
	}
	r.scanner = sc

	// header: leading #KEY "value" lines; first non-# line starts entries
	for r.scanner.Scan() {
		line := strings.TrimPrefix(strings.TrimRight(r.scanner.Text(), "\r"), "\uFEFF")
		if strings.TrimSpace(line) == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			// The key/value separator is "whitespace", not "a space":
			// Lingvo's own sample writes #INDEX_LANGUAGE with a tab, and
			// splitting on " " alone dropped the line entirely.
			rest, k, v := line[1:], line[1:], ""
			if i := strings.IndexAny(rest, " \t"); i >= 0 {
				k, v = rest[:i], rest[i+1:]
			}
			r.header[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"'`)
			continue
		}
		r.buffered = append(r.buffered, line)
		break
	}

	name := r.header["NAME"]
	if name == "" {
		base := filepath.Base(path)
		name = strings.TrimSuffix(strings.TrimSuffix(base, ".dz"), ".dsl")
	}
	from, to := r.header["INDEX_LANGUAGE"], r.header["CONTENTS_LANGUAGE"]
	desc := ""
	if from != "" || to != "" {
		desc = from + " → " + to
	}
	r.meta = dict.Meta{
		Name:        name,
		Format:      "dsl",
		Path:        path,
		Description: desc,
		// #INDEX_LANGUAGE names the language of the HEADWORDS, which is
		// exactly what a lemmatizer needs and is the one thing a path
		// convention can get backwards. Taken from the header field, never
		// re-parsed out of desc. Lingvo writes collation names here
		// ("SpanishModernSort"), which internal/lang absorbs.
		IndexLang: lang.FromDeclared(from),
		// #CONTENTS_LANGUAGE is the other end of the pair, and until the About
		// panel existed it survived only as the middle of the desc string
		// above. Recorded as a code so a consumer never has to parse " → ".
		ContentsLang: lang.FromDeclared(to),
	}
	return nil
}

// detectEncoding sniffs the BOM, falling back to UTF-8 validity checks.
func detectEncoding(br *bufio.Reader) (encoding.Encoding, error) {
	head, _ := br.Peek(4)
	switch {
	case len(head) >= 4 && bytes.Equal(head[:4], []byte{0xFF, 0xFE, 0x00, 0x00}):
		return utf32.UTF32(utf32.LittleEndian, utf32.UseBOM), nil
	case len(head) >= 4 && bytes.Equal(head[:4], []byte{0x00, 0x00, 0xFE, 0xFF}):
		return utf32.UTF32(utf32.BigEndian, utf32.UseBOM), nil
	case len(head) >= 2 && bytes.Equal(head[:2], []byte{0xFF, 0xFE}):
		return unicode.UTF16(unicode.LittleEndian, unicode.UseBOM), nil
	case len(head) >= 2 && bytes.Equal(head[:2], []byte{0xFE, 0xFF}):
		return unicode.UTF16(unicode.BigEndian, unicode.UseBOM), nil
	case len(head) >= 3 && bytes.Equal(head[:3], []byte{0xEF, 0xBB, 0xBF}):
		return unicode.UTF8BOM, nil
	}
	// no BOM: accept UTF-8 when a sample validates, else assume UTF-16LE
	// (the common BOM-less Lingvo export)
	sample, _ := br.Peek(1 << 12)
	if utf8.Valid(sample) {
		return unicode.UTF8, nil
	}
	return unicode.UTF16(unicode.LittleEndian, unicode.IgnoreBOM), nil
}

func (r *Reader) Meta() dict.Meta { return r.meta }

func (r *Reader) Close() error { return r.f.Close() }

// nextLine returns the next raw line (CR stripped) from lookahead or file.
func (r *Reader) nextLine() (string, bool) {
	if len(r.buffered) > 0 {
		l := r.buffered[0]
		r.buffered = r.buffered[1:]
		return l, true
	}
	if r.eof {
		return "", false
	}
	if !r.scanner.Scan() {
		r.eof = true
		return "", false
	}
	r.scanCount++
	return strings.TrimRight(r.scanner.Text(), "\r"), true
}

func (r *Reader) Next() (dict.Entry, error) {
	if len(r.pending) > 0 {
		e := r.pending[0]
		r.pending = r.pending[1:]
		return e, nil
	}

	var termLines, textLines []string
	for {
		line, ok := r.nextLine()
		if !ok {
			break
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			textLines = append(textLines, line)
			continue
		}
		// headword line: if a block is complete, push this line back
		if len(textLines) > 0 {
			r.buffered = append(r.buffered, line)
			break
		}
		termLines = append(termLines, line)
	}
	if len(textLines) == 0 {
		if err := r.scanner.Err(); err != nil {
			return dict.Entry{}, err
		}
		return dict.Entry{}, io.EOF
	}
	entry, subs, err := r.parseBlock(termLines, textLines)
	if err != nil {
		return dict.Entry{}, err
	}
	r.pending = subs
	return entry, nil
}

// parseBlock converts one entry block. Port of pyglossary parseEntryBlock:
// titles yield Full/Alt headword variants; "@" lines split sub-entries,
// referenced from the main article.
func (r *Reader) parseBlock(termLines, textLines []string) (dict.Entry, []dict.Entry, error) {
	var terms []string
	var displayTitles []string
	for _, line := range termLines {
		t := transformTitle(line)
		if t.Full == "" {
			continue
		}
		terms = append(terms, t.Full)
		if t.Alt != "" && t.Alt != t.Full {
			terms = append(terms, t.Alt)
		}
		if t.Display != escape(t.Full) && t.Display != "" {
			displayTitles = append(displayTitles, "<b>"+t.Display+"</b>")
		}
	}
	if len(terms) == 0 {
		return dict.Entry{}, nil, fmt.Errorf("dsl: entry block without headword near %q", textLines[0])
	}

	var mainText strings.Builder
	var subs []dict.Entry
	// One sub-card may carry several headings piled on consecutive "@" lines,
	// and each heading may expand into two lookup keys ("(...)" optional part),
	// so both are lists. linesInCard is what tells a pile from the start of the
	// next card: an "@" line with body lines behind it closes, one without
	// simply adds another heading (goldendict-ng src/dict/dsl.cc, the
	// insidedCards loop).
	var subHeads []string
	var subText strings.Builder
	subOpen, linesInCard := false, 0
	flushSub := func() {
		defer func() {
			subHeads = nil
			subText.Reset()
			subOpen, linesInCard = false, 0
		}()
		var heads, refs []string
		for _, h := range subHeads {
			t := transformTitle(h)
			if t.Full == "" {
				continue
			}
			heads = append(heads, t.Full)
			refs = append(refs, t.Full)
			if t.Alt != "" && t.Alt != t.Full {
				heads = append(heads, t.Alt)
				refs = append(refs, t.Alt)
			}
		}
		if len(heads) == 0 {
			return
		}
		body, _, err := transformBodyAbbrev(strings.TrimRight(subText.String(), "\n"), heads[0], r.abbrevs())
		if err == nil {
			subs = append(subs, dict.Entry{Headwords: heads, Body: body, Kind: dict.BodyHTML})
		}
		for _, k := range refs {
			// The back-reference is re-parsed as DSL, so the key has to survive
			// a second pass: an unescaped "[" or "~" in a sub-headword would be
			// read as markup and swallow the link. The leading "- " is what
			// Lingvo and GoldenDict both draw in front of a sub-card link.
			mainText.WriteString("\t[m2]- [ref]" + dslEscape(k) + "[/ref][/m]\n")
		}
	}
	for _, line := range textLines {
		if head, ok := atSignHeading(line); ok {
			if subOpen && linesInCard == 0 && head != "" {
				subHeads = append(subHeads, head) // another heading for the same card
				continue
			}
			flushSub()
			if head != "" {
				subHeads = append(subHeads, head)
				subOpen = true
			}
			continue
		}
		if subOpen {
			linesInCard++
			subText.WriteString(line + "\n")
			continue
		}
		mainText.WriteString(line + "\n")
	}
	flushSub()

	body, _, err := transformBodyAbbrev(strings.TrimRight(mainText.String(), "\n"), terms[0], r.abbrevs())
	if err != nil {
		return dict.Entry{}, nil, fmt.Errorf("dsl: entry %q: %w", terms[0], err)
	}
	if len(displayTitles) > 0 && !r.plainBody {
		body = strings.Join(displayTitles, "<br/>") + "<br/>" + body
	}
	return dict.Entry{Headwords: terms, Body: body, Kind: dict.BodyHTML}, subs, nil
}

// dslEscape backslash-escapes the characters the DSL lexer treats as markup,
// so a string can be embedded in generated DSL and come back out unchanged.
func dslEscape(s string) string { return dslEscaper.Replace(s) }

var dslEscaper = strings.NewReplacer(
	`\`, `\\`, "[", `\[`, "]", `\]`, "~", `\~`, "<", `\<`, ">", `\>`, "@", `\@`,
)

// atSignHeading recognises a sub-card line. Lingvo puts the "@" first on the
// line, but leading whitespace and leading DSL tags are allowed before it
// ("[m1]@ heading" is legal and common), and the space after it is optional:
// "@heading" and "@ heading" are the same thing. An empty heading closes the
// current sub-card. Mirrors isAtSignFirst() in goldendict-ng
// src/dict/dsl_details.cc.
var atSignLine = regexp.MustCompile(`^[ \t]*(?:\[[^\]]+\][ \t]*)*@`)

func atSignHeading(line string) (string, bool) {
	loc := atSignLine.FindStringIndex(line)
	if loc == nil {
		return "", false
	}
	return strings.TrimSpace(line[loc[1]:]), true
}
