// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package mdx

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/wuweidict/wudict/internal/dict"
	gomdict "github.com/wuweidict/wudict/internal/gomdict"
	"github.com/wuweidict/wudict/internal/resource"
)

// Where an .mdx's media is, instead of what it is (O8).
//
// MDict is the format the locator exists for. StarDict and DSL keep resources
// in a folder or a zip that a path alone finds; an .mdd keeps them inside a
// container whose only map from name to bytes is the key index - the expensive
// half of opening the dictionary. Recording (part, offset, size) once turns
// every later fetch into a record-table read plus one block decompression.

func init() {
	resource.Register("mdx", resource.Provider{Sources: MediaSources, Open: OpenFetcher})
}

// MediaSources is the loose-file half: MDict serves files sitting NEXT TO the
// .mdx (LDOCE6 ships LDOCE6.css and entry.js that way), and those need no
// dictionary and no locator. The folder is shared with every other dictionary
// in it, so it is exact-path only and gated by the same allowlist looseFile
// uses - dict.IsAssetName, one copy, because it is a security boundary.
func MediaSources(srcPath string) []resource.Source {
	return []resource.Source{&assetDir{Dir: resource.NewDirExact(filepath.Dir(srcPath))}}
}

// assetDir is a folder that only answers for names that are assets.
type assetDir struct{ *resource.Dir }

func (a *assetDir) Open(name string) (io.ReadCloser, error) {
	if !dict.IsAssetName(resource.Clean(name)) {
		return nil, dict.ErrNotFound
	}
	return a.Dir.Open(name)
}

// MediaLinks enumerates every resource of every companion .mdd as a location.
// It walks exactly what resourceIndex walks and keeps the same winner on a
// duplicate (first container, first entry), so a linked fetch and a direct one
// can never disagree about which file "audio/x.mp3" is.
func (d *Dict) MediaLinks() ([]resource.Part, []resource.Link, error) {
	parts := make([]resource.Part, 0, len(d.mdds))
	var links []resource.Link
	seen := map[string]bool{}
	for i := range d.mdds {
		m := &d.mdds[i]
		st, err := os.Stat(m.path)
		if err != nil {
			return nil, nil, err
		}
		part := len(parts)
		parts = append(parts, resource.Part{
			Path: m.path, Size: st.Size(), MTime: st.ModTime().Unix(),
		})
		for _, e := range m.entries {
			key := resource.Key(e.KeyWord)
			if key == "" || seen[key] {
				continue
			}
			seen[key] = true
			links = append(links, resource.Link{
				Name: key,
				MIME: resource.MIME(key),
				Part: part,
				Off:  e.RecordStartOffset,
				Size: linkSize(e),
			})
		}
	}
	return parts, links, nil
}

// linkSize converts an entry's end offset to a length, collapsing the two
// spellings of "to the end of the block" - readKeyEntries fills in an entry's
// end from its SUCCESSOR's start, so the last entry of a container has none,
// and the placeholder left behind is 0 in v1/v2 and negative in v3. Both mean
// the same thing and are stored as -1, which gomdict.LocateAt translates back
// into whichever sentinel the container's layout expects.
func linkSize(e *gomdict.MDictKeywordEntry) int64 {
	if e.RecordEndOffset <= 0 {
		return -1
	}
	return e.RecordEndOffset - e.RecordStartOffset
}

// OpenFetcher reads recorded locations back out of the .mdd files. Containers
// are opened lazily and one at a time: an .mdx with eight companions that only
// ever serves audio should pay for the one holding the audio.
func OpenFetcher(parts []resource.Part) (resource.Fetcher, error) {
	if len(parts) == 0 {
		return nil, errors.New("mdx: no media containers recorded")
	}
	return &mddFetcher{parts: parts, mds: make([]*gomdict.Mdict, len(parts))}, nil
}

type mddFetcher struct {
	mu    sync.Mutex
	parts []resource.Part
	mds   []*gomdict.Mdict
}

func (f *mddFetcher) Fetch(part int, off, size int64) ([]byte, error) {
	if part < 0 || part >= len(f.parts) {
		return nil, fmt.Errorf("mdx: media container %d out of range", part)
	}
	if size == 0 {
		return nil, nil
	}
	md, err := f.container(part)
	if err != nil {
		return nil, err
	}
	return md.LocateAt(off, size)
}

func (f *mddFetcher) container(part int) (*gomdict.Mdict, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if md := f.mds[part]; md != nil {
		return md, nil
	}
	md, err := gomdict.New(f.parts[part].Path)
	if err != nil {
		return nil, err
	}
	// The cheap half only: record blocks, no key blocks, no headwords. That is
	// the whole saving - see gomdict.BuildRecordIndex.
	if err := md.BuildRecordIndex(); err != nil {
		return nil, err
	}
	f.mds[part] = md
	return md, nil
}

func (f *mddFetcher) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.mds {
		f.mds[i] = nil // gomdict opens files per read; nothing holds a descriptor
	}
	return nil
}
