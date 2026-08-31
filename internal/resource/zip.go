// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package resource

import (
	"archive/zip"
	"io"
	"strings"
	"sync"

	"github.com/wuweidict/wudict/internal/dict"
)

// Zip is a resource archive beside a dictionary: a DSL ".files.zip", a
// StarDict "res.zip". Entries are indexed under every reading of their stored
// bytes, so a name written in a code page nobody recorded still resolves.
type Zip struct {
	rc    *zip.ReadCloser
	files []*zip.File // slot -> entry
	ix    Index
	mu    sync.Mutex // the archive's file handle is shared by every reader
}

// OpenZip indexes an archive. The handle stays open until Close.
func OpenZip(path string) (*Zip, error) {
	rc, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	z := &Zip{rc: rc, files: make([]*zip.File, 0, len(rc.File))}
	for _, f := range rc.File {
		if strings.HasSuffix(f.Name, "/") || f.FileInfo().IsDir() {
			continue
		}
		// NonUTF8 is the general-purpose flag, and it is a claim, not proof:
		// readings() trusts it only where the bytes bear it out.
		z.ix.Add(f.Name, !f.NonUTF8)
		z.files = append(z.files, f)
	}
	return z, nil
}

func (z *Zip) Open(name string) (io.ReadCloser, error) {
	slot, ok := z.ix.Lookup(name)
	if !ok {
		return nil, dict.ErrNotFound
	}
	z.mu.Lock()
	defer z.mu.Unlock()
	return z.files[slot].Open()
}

func (z *Zip) List() []string { return z.ix.Names() }

func (z *Zip) Close() error { return z.rc.Close() }
