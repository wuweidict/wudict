// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package intake

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/wuweidict/wudict/internal/dict"
)

// The classifier asks the format registry what a main file is, and the
// registry is filled by the format packages' init(). Registering the same
// extensions here keeps the test honest about what a real build recognises
// without linking five format packages into it.
func init() {
	open := func(path string) (dict.Dictionary, error) { return nil, errors.New("not opened in tests") }
	for _, ext := range []string{".mdx", ".ifo", ".dsl", ".dsl.dz", ".slob", ".bgl", ".zim", ".zimaa"} {
		dict.RegisterFormat(ext, open)
	}
	dict.RegisterInspectable(".mdd", open)
}

type member struct {
	name string
	body string
}

// buildZip writes an archive of small files and returns its path.
func buildZip(t *testing.T, members ...member) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "bundle.zip")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for _, m := range members {
		w, err := zw.Create(m.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(w, m.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return p
}

func sniffZip(t *testing.T, members ...member) ([]Candidate, Archive) {
	t.Helper()
	a, err := OpenZip(buildZip(t, members...))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	got, err := Sniff(a)
	if err != nil {
		t.Fatal(err)
	}
	return got, a
}

func names(c Candidate) []string {
	out := append([]string(nil), c.Files...)
	sort.Strings(out)
	return out
}

func TestSniffMDXWithResources(t *testing.T) {
	got, _ := sniffZip(t,
		member{"Oxford/Oxford.mdx", "x"},
		member{"Oxford/Oxford.mdd", "x"},
		member{"Oxford/Oxford.1.mdd", "x"},
		member{"Oxford/readme.txt", "x"},
	)
	if len(got) != 1 {
		t.Fatalf("want one dictionary, got %d: %+v", len(got), got)
	}
	c := got[0]
	if c.Name != "Oxford" || c.Format != ".mdx" || c.Main != "Oxford/Oxford.mdx" {
		t.Errorf("unexpected candidate: %+v", c)
	}
	if !c.Complete() {
		t.Errorf("MDX with its resources must be complete, missing %v", c.Missing)
	}
	if c.Files[0] != c.Main {
		t.Errorf("the main file must be extracted first: %v", c.Files)
	}
	want := []string{"Oxford/Oxford.1.mdd", "Oxford/Oxford.mdd", "Oxford/Oxford.mdx"}
	if got := names(c); !equal(got, want) {
		t.Errorf("files = %v, want %v (the readme is not ours)", got, want)
	}
}

func TestSniffStarDictCompleteAndShared(t *testing.T) {
	got, _ := sniffZip(t,
		member{"sd/star.ifo", "x"},
		member{"sd/star.idx", "x"},
		member{"sd/star.dict.dz", "x"},
		member{"sd/star.syn", "x"},
		member{"sd/res/a.png", "x"},
		member{"sd/res.zip", "x"},
	)
	if len(got) != 1 {
		t.Fatalf("want one dictionary, got %+v", got)
	}
	c := got[0]
	if !c.Complete() {
		t.Fatalf("a full StarDict set must be complete, missing %v", c.Missing)
	}
	want := []string{"sd/res.zip", "sd/res/a.png", "sd/star.dict.dz", "sd/star.idx", "sd/star.ifo", "sd/star.syn"}
	if got := names(c); !equal(got, want) {
		t.Errorf("files = %v, want %v", got, want)
	}
}

// Resources that belong to two dictionaries belong to neither: handing a
// shared res/ to a guess costs the other dictionary its media on removal.
func TestSniffSharedResourcesWithTwoDictionaries(t *testing.T) {
	got, _ := sniffZip(t,
		member{"sd/a.ifo", "x"}, member{"sd/a.idx", "x"}, member{"sd/a.dict", "x"},
		member{"sd/b.ifo", "x"}, member{"sd/b.idx", "x"}, member{"sd/b.dict", "x"},
		member{"sd/res/pic.png", "x"},
	)
	if len(got) != 2 {
		t.Fatalf("want two dictionaries, got %+v", got)
	}
	for _, c := range got {
		for _, f := range c.Files {
			if strings.Contains(f, "/res/") {
				t.Errorf("%s claimed the shared res/ folder: %v", c.Name, c.Files)
			}
		}
	}
}

// The folder's custom.css and font.ttf are every dictionary's there (D205):
// each dictionary installs into a folder of its own, so each one carries a
// copy - unlike res/, which stays with no one when two could claim it.
func TestSniffFolderFilesGoWithEveryDictionary(t *testing.T) {
	got, a := sniffZip(t,
		member{"lib/oxford.mdx", "x"}, member{"lib/cambridge.bgl", "x"}, member{"lib/dahl.dsl", "x"},
		member{"lib/wiktionary.ifo", "x"}, member{"lib/wiktionary.idx", "x"}, member{"lib/wiktionary.dict", "x"},
		member{"lib/custom.css", ".a{}"}, member{"lib/font.ttf", "OTTO"}, member{"lib/res.zip", "x"},
		member{"other/custom.css", ".b{}"},
	)
	if len(got) != 4 {
		t.Fatalf("want four dictionaries, got %+v", got)
	}
	for _, c := range got {
		fs := names(c)
		if !slices.Contains(fs, "lib/custom.css") || !slices.Contains(fs, "lib/font.ttf") {
			t.Errorf("%s lacks the folder's files: %v", c.Name, fs)
		}
		if slices.Contains(fs, "lib/res.zip") || slices.Contains(fs, "other/custom.css") {
			t.Errorf("%s took a file that is not its folder's to give: %v", c.Name, fs)
		}
	}
	// and each install folder really receives its own copy
	for _, c := range got[:2] {
		dest := t.TempDir()
		if err := Extract(context.Background(), a, c, dest, nil); err != nil {
			t.Fatalf("%s: %v", c.Name, err)
		}
		if b, err := os.ReadFile(filepath.Join(dest, "custom.css")); err != nil || string(b) != ".a{}" {
			t.Errorf("%s: custom.css = %q, %v", c.Name, b, err)
		}
	}
}

// A StarDict .ifo is a header naming an index and an article blob it does not
// contain. Alone, it is not a small dictionary - it is a broken one.
func TestSniffLoneIfoIsIncomplete(t *testing.T) {
	got, _ := sniffZip(t, member{"star.ifo", "x"})
	if len(got) != 1 {
		t.Fatalf("want the candidate reported, got %+v", got)
	}
	if got[0].Complete() {
		t.Fatal("a lone .ifo must not be installable")
	}
	if !equal(got[0].Missing, []string{".idx", ".dict"}) {
		t.Errorf("missing = %v, want .idx and .dict", got[0].Missing)
	}
}

func TestSniffDSLWithAbbreviationsAndMedia(t *testing.T) {
	got, _ := sniffZip(t,
		member{"ru-en.dsl.dz", "x"},
		member{"ru-en_abrv.dsl", "x"},
		member{"ru-en.dsl.files.zip", "x"},
		member{"ru-en.ann", "x"},
	)
	if len(got) != 1 {
		t.Fatalf("the abbreviation glossary must not be offered as a dictionary: %+v", got)
	}
	c := got[0]
	if c.Name != "ru-en" || c.Format != ".dsl.dz" {
		t.Errorf("unexpected candidate: %+v", c)
	}
	if len(c.Files) != 4 {
		t.Errorf("files = %v, want all four", c.Files)
	}
}

// Two dictionaries sharing a stem in DIFFERENT folders are two dictionaries,
// and neither may collect the other's companions.
func TestSniffGroupsPerDirectory(t *testing.T) {
	got, _ := sniffZip(t,
		member{"en/Oxford.mdx", "x"}, member{"en/Oxford.mdd", "x"},
		member{"fr/Oxford.mdx", "x"}, member{"fr/Oxford.mdd", "x"},
	)
	if len(got) != 2 {
		t.Fatalf("want two dictionaries, got %+v", got)
	}
	for _, c := range got {
		dir := strings.SplitN(c.Main, "/", 2)[0]
		for _, f := range c.Files {
			if !strings.HasPrefix(f, dir+"/") {
				t.Errorf("%s took a file from the other folder: %v", c.Main, c.Files)
			}
		}
	}
}

// Case is not a distinction on the filesystems these land on: a bundle packed
// on Windows spells the resources differently from the dictionary and means
// the same dictionary.
func TestSniffCaseInsensitiveGrouping(t *testing.T) {
	got, _ := sniffZip(t, member{"Oxford.MDX", "x"}, member{"oxford.mdd", "x"})
	if len(got) != 1 || len(got[0].Files) != 2 {
		t.Fatalf("want one dictionary of two files, got %+v", got)
	}
}

// Every shape of a name that points somewhere it was not invited, refused
// before it is so much as listed to the user.
func TestSniffRejectsUnsafeNames(t *testing.T) {
	got, _ := sniffZip(t,
		member{"../escape.mdx", "x"},
		member{"a/../../escape.mdx", "x"},
		member{"/absolute/escape.mdx", "x"},
		member{`..\windows.mdx`, "x"},
		member{"./ok.mdx", "x"},
	)
	if len(got) != 1 {
		t.Fatalf("only the harmless entry may survive: %+v", got)
	}
	if got[0].Main != "ok.mdx" {
		t.Errorf("main = %q, want the normalised ok.mdx", got[0].Main)
	}
}

func TestSniffRejectsDeepNesting(t *testing.T) {
	deep := strings.Repeat("a/", maxDepth+1) + "x.mdx"
	got, _ := sniffZip(t, member{deep, "x"})
	if len(got) != 0 {
		t.Fatalf("a path past the depth cap must not be offered: %+v", got)
	}
}

// Companions with no main file beside them are somebody's loose resources.
func TestSniffCompanionsWithoutMain(t *testing.T) {
	got, _ := sniffZip(t, member{"Oxford.mdd", "x"}, member{"star.idx", "x"})
	if len(got) != 0 {
		t.Fatalf("nothing installable here: %+v", got)
	}
}

func TestExtractLayout(t *testing.T) {
	got, a := sniffZip(t,
		member{"Bundle/sd/star.ifo", "ifo"},
		member{"Bundle/sd/star.idx", "idx"},
		member{"Bundle/sd/star.dict.dz", "dict"},
		member{"Bundle/sd/res/audio/a.spx", "spx"},
	)
	if len(got) != 1 || !got[0].Complete() {
		t.Fatalf("unexpected sniff: %+v", got)
	}
	dest := t.TempDir()
	if err := Extract(context.Background(), a, got[0], dest, nil); err != nil {
		t.Fatal(err)
	}
	// The archive's folders are dropped; the format's own are kept.
	want := map[string]string{
		"star.ifo":        "ifo",
		"star.idx":        "idx",
		"star.dict.dz":    "dict",
		"res/audio/a.spx": "spx",
	}
	for rel, body := range want {
		b, err := os.ReadFile(filepath.Join(dest, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
		if string(b) != body {
			t.Errorf("%s = %q, want %q", rel, b, body)
		}
	}
}

func TestExtractRefusesIncomplete(t *testing.T) {
	got, a := sniffZip(t, member{"star.ifo", "x"})
	if err := Extract(context.Background(), a, got[0], t.TempDir(), nil); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("err = %v, want ErrIncomplete", err)
	}
}

// A fake archive is the only way to make a directory LIE: a real zip writer
// records what it actually wrote, and the whole point of the checks in
// writeEntry is that the directory is an attacker-chosen claim.
type fakeArchive struct {
	entries []Entry
	bodies  map[string]string
}

func (f *fakeArchive) Entries() []Entry { return f.entries }
func (f *fakeArchive) Open(name string) (io.ReadCloser, error) {
	b, ok := f.bodies[name]
	if !ok {
		return nil, errors.New("no such entry")
	}
	return io.NopCloser(strings.NewReader(b)), nil
}
func (f *fakeArchive) Close() error { return nil }

func TestExtractRefusesEntryLargerThanDeclared(t *testing.T) {
	a := &fakeArchive{
		entries: []Entry{{Name: "x.mdx", Base: "x.mdx", Size: 4, Compressed: 4}},
		bodies:  map[string]string{"x.mdx": strings.Repeat("A", 4096)},
	}
	c := Candidate{Name: "x", Format: ".mdx", Main: "x.mdx", Files: []string{"x.mdx"}, Size: 4}
	dest := t.TempDir()
	if err := Extract(context.Background(), a, c, dest, nil); !errors.Is(err, ErrSizeLied) {
		t.Fatalf("err = %v, want ErrSizeLied", err)
	}
	// and the write stopped at the declared size plus the byte that caught it
	fi, err := os.Stat(filepath.Join(dest, "x.mdx"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Size() > 64 {
		t.Errorf("wrote %d bytes past a 4-byte declaration", fi.Size())
	}
}

func TestExtractRefusesBomb(t *testing.T) {
	a := &fakeArchive{
		entries: []Entry{{Name: "x.mdx", Base: "x.mdx", Size: 8 << 30, Compressed: 1024}},
		bodies:  map[string]string{"x.mdx": "irrelevant"},
	}
	c := Candidate{Name: "x", Format: ".mdx", Main: "x.mdx", Files: []string{"x.mdx"}, Size: 8 << 30}
	if err := Extract(context.Background(), a, c, t.TempDir(), nil); !errors.Is(err, ErrBomb) {
		t.Fatalf("err = %v, want ErrBomb", err)
	}
}

// Even handed a candidate assembled outside Sniff, extraction must not write
// through a climbing name.
func TestExtractRefusesUnsafeCandidate(t *testing.T) {
	a := &fakeArchive{
		entries: []Entry{{Name: "d/x.mdx", Base: "x.mdx", Size: 1, Compressed: 1}, {Name: "d/../../evil", Base: "evil", Size: 1, Compressed: 1}},
		bodies:  map[string]string{"d/x.mdx": "x", "d/../../evil": "x"},
	}
	c := Candidate{Name: "x", Format: ".mdx", Main: "d/x.mdx", Files: []string{"d/x.mdx", "d/../../evil"}, Size: 2}
	dest := t.TempDir()
	if err := Extract(context.Background(), a, c, dest, nil); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("err = %v, want ErrUnsafePath", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dest), "evil")); err == nil {
		t.Fatal("wrote outside the destination")
	}
}

func TestOpenZipRejectsNonArchive(t *testing.T) {
	p := filepath.Join(t.TempDir(), "not.zip")
	if err := os.WriteFile(p, []byte("this is not a zip"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenZip(p); err == nil {
		t.Fatal("a file that is not an archive must not open as one")
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// What the user is shown about a candidate is its files, and the names have to
// be the ones they would recognise: an archive that packs its dictionary in a
// folder must not put that folder in the row, and a StarDict resource subtree
// must not put a thousand images in it (D137).
func TestCandidateNames(t *testing.T) {
	for _, tc := range []struct {
		name    string
		members []member
		want    []string
	}{
		{
			name:    "loose at the archive root",
			members: []member{{"OED.mdx", "main"}, {"OED.mdd", "media"}},
			want:    []string{"OED.mdx", "OED.mdd"},
		},
		{
			name: "packed in a folder: the folder is not part of the answer",
			members: []member{
				{"Dicts/English/Oxford.mdx", "main"},
				{"Dicts/English/Oxford.mdd", "media"},
			},
			want: []string{"Oxford.mdx", "Oxford.mdd"},
		},
		{
			name: "a resource subtree is named once",
			members: []member{
				{"sd/x.ifo", "header"},
				{"sd/x.idx", "index"},
				{"sd/x.dict", "bodies"},
				{"sd/res/a.png", "image"},
				{"sd/res/b.png", "image"},
			},
			want: []string{"x.ifo", "x.dict", "x.idx", "res/"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, err := OpenZip(buildZip(t, tc.members...))
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			cands, err := Sniff(a)
			if err != nil {
				t.Fatal(err)
			}
			if len(cands) != 1 {
				t.Fatalf("candidates = %d, want 1", len(cands))
			}
			got := cands[0].Names()
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("Names() = %v, want %v", got, tc.want)
			}
			// And they reach the caller: the row cannot say which files this
			// is if the JSON does not carry them.
			b, err := json.Marshal(cands[0])
			if err != nil {
				t.Fatal(err)
			}
			var back struct {
				Name  string   `json:"name"`
				Files []string `json:"files"`
			}
			if err := json.Unmarshal(b, &back); err != nil {
				t.Fatal(err)
			}
			if back.Name != cands[0].Name || !reflect.DeepEqual(back.Files, tc.want) {
				t.Fatalf("marshalled %s, want name %q and files %v", b, cands[0].Name, tc.want)
			}
		})
	}
}
