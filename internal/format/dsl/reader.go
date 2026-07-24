package dsl

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/encoding/unicode/utf32"
	"golang.org/x/text/transform"

	"github.com/glowinthedark/gonow-dict/internal/dict"
)

// Reader is the sequential ingest scan over a .dsl / .dsl.dz file.
// Entry blocks: headword line(s) at column 0, indented body lines;
// "@ sub-headword" lines split off sub-entries (linked from the parent).
type Reader struct {
	f       *os.File
	scanner *bufio.Scanner
	meta    dict.Meta
	header  map[string]string

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
	r := &Reader{f: f, header: map[string]string{}}
	if err := r.init(path); err != nil {
		f.Close()
		return nil, err
	}
	return r, nil
}

func (r *Reader) init(path string) error {
	var src io.Reader = r.f
	if strings.HasSuffix(strings.ToLower(path), ".dz") {
		gz, err := gzip.NewReader(r.f)
		if err != nil {
			return fmt.Errorf("dsl %s: %w", path, err)
		}
		src = gz
	}
	br := bufio.NewReaderSize(src, 1<<20)
	enc, err := detectEncoding(br)
	if err != nil {
		return fmt.Errorf("dsl %s: %w", path, err)
	}
	r.scanner = bufio.NewScanner(transform.NewReader(br, enc.NewDecoder()))
	r.scanner.Buffer(make([]byte, 0, 1<<20), 1<<24)

	// header: leading #KEY "value" lines; first non-# line starts entries
	for r.scanner.Scan() {
		line := strings.TrimPrefix(strings.TrimRight(r.scanner.Text(), "\r"), "\uFEFF")
		if strings.TrimSpace(line) == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			if k, v, ok := strings.Cut(line[1:], " "); ok {
				r.header[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"'`)
			}
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
	r.meta = dict.Meta{
		Name:        name,
		Format:      "dsl",
		Path:        path,
		Description: r.header["INDEX_LANGUAGE"] + " → " + r.header["CONTENTS_LANGUAGE"],
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
	subKey, subText := "", strings.Builder{}
	flushSub := func() {
		if subKey == "" {
			return
		}
		body, _, err := transformBody(strings.TrimRight(subText.String(), "\n"), subKey)
		if err == nil {
			subs = append(subs, dict.Entry{Headwords: []string{subKey}, Body: body, Kind: dict.BodyHTML})
		}
		mainText.WriteString("\t[m2][ref]" + subKey + "[/ref][/m]\n")
		subKey = ""
		subText.Reset()
	}
	for _, line := range textLines {
		s := strings.TrimSpace(line)
		if s == "@" {
			flushSub()
			continue
		}
		if after, ok := strings.CutPrefix(s, "@ "); ok {
			flushSub()
			subKey = strings.TrimSpace(after)
			continue
		}
		if subKey != "" {
			subText.WriteString(line + "\n")
			continue
		}
		mainText.WriteString(line + "\n")
	}
	flushSub()

	body, _, err := transformBody(strings.TrimRight(mainText.String(), "\n"), terms[0])
	if err != nil {
		return dict.Entry{}, nil, fmt.Errorf("dsl: entry %q: %w", terms[0], err)
	}
	if len(displayTitles) > 0 {
		body = strings.Join(displayTitles, "<br/>") + "<br/>" + body
	}
	return dict.Entry{Headwords: terms, Body: body, Kind: dict.BodyHTML}, subs, nil
}
