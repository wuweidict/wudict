package dsl

import (
	"archive/zip"
	"fmt"
	"io"
	"mime"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/glowinthedark/gonow-dict/internal/dict"
	"github.com/glowinthedark/gonow-dict/internal/store"
)

func init() {
	dict.RegisterFormat(".dsl", func(path string) (dict.Dictionary, error) { return Open(path) })
	dict.RegisterReader(".dsl", func(path string) (dict.Reader, error) { return NewReader(path) })
	// .dsl.dz surfaces as extension ".dz"
	dict.RegisterFormat(".dz", func(p string) (dict.Dictionary, error) {
		if !strings.HasSuffix(strings.ToLower(p), ".dsl.dz") {
			return nil, fmt.Errorf("unsupported .dz file (only .dsl.dz): %s", p)
		}
		return Open(p)
	})
	dict.RegisterReader(".dz", func(p string) (dict.Reader, error) {
		if !strings.HasSuffix(strings.ToLower(p), ".dsl.dz") {
			return nil, fmt.Errorf("unsupported .dz file (only .dsl.dz): %s", p)
		}
		return NewReader(p)
	})
}

// Dict is the DSL "direct" backend. DSL has no native index, so Open
// transparently ingests into a cached text.db on first use (SPEC §1);
// the cache file name embeds a source-content hash, so a changed source
// gets a fresh ingest automatically. Resources stay lazy in
// `<name>.files.zip` (or loose beside the .dsl).
type Dict struct {
	*store.Store
	srcPath string

	zipOnce  sync.Once
	zipFiles map[string]*zip.File
	zipMu    sync.Mutex
}

func Open(path string) (*Dict, error) {
	r, err := NewReader(path)
	if err != nil {
		return nil, err
	}
	name := r.Meta().Name

	dbPath := store.CacheBase(path, name) + ".text.db"
	if _, statErr := os.Stat(dbPath); statErr != nil {
		fmt.Fprintf(os.Stderr, "dsl: preparing search index for %q (first open)…\n", name)
		err = store.Ingest(r, dbPath, func(done, total int) {
			fmt.Fprintf(os.Stderr, "\r%d entries", done)
		})
		fmt.Fprintln(os.Stderr)
		r.Close()
		if err != nil {
			return nil, fmt.Errorf("dsl auto-ingest: %w", err)
		}
	} else {
		r.Close()
	}

	s, err := store.Open(dbPath)
	if err != nil {
		return nil, err
	}
	return &Dict{Store: s, srcPath: path}, nil
}

func (d *Dict) Meta() dict.Meta {
	m := d.Store.Meta()
	m.Format = "dsl"
	m.Path = d.srcPath
	return m
}

func (d *Dict) Close() error { return d.Store.Close() }

// Resource serves from `<dsl-path>.files.zip` (also `<base>.files.zip`
// for .dsl.dz), else a loose file beside the source.
func (d *Dict) Resource(name string) (io.ReadCloser, string, error) {
	norm := strings.TrimLeft(path.Clean(name), "/")
	if norm == "" || norm == "." || strings.HasPrefix(norm, "..") {
		return nil, "", dict.ErrNotFound
	}
	d.zipOnce.Do(d.loadZip)
	if d.zipFiles != nil {
		if zf, ok := d.zipFiles[strings.ToLower(norm)]; ok {
			d.zipMu.Lock()
			rc, err := zf.Open()
			d.zipMu.Unlock()
			if err != nil {
				return nil, "", err
			}
			return rc, mime.TypeByExtension(path.Ext(norm)), nil
		}
	}
	if f, err := os.Open(filepath.Join(filepath.Dir(d.srcPath), filepath.FromSlash(norm))); err == nil {
		return f, mime.TypeByExtension(path.Ext(norm)), nil
	}
	return nil, "", dict.ErrNotFound
}

// Resources lists the .files.zip entries.
func (d *Dict) Resources() []string {
	d.zipOnce.Do(d.loadZip)
	out := make([]string, 0, len(d.zipFiles))
	for name := range d.zipFiles {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func (d *Dict) loadZip() {
	candidates := []string{d.srcPath + ".files.zip"}
	if strings.HasSuffix(strings.ToLower(d.srcPath), ".dz") {
		candidates = append(candidates, strings.TrimSuffix(d.srcPath, filepath.Ext(d.srcPath))+".files.zip")
	}
	for _, p := range candidates {
		zr, err := zip.OpenReader(p)
		if err != nil {
			continue
		}
		d.zipFiles = make(map[string]*zip.File, len(zr.File))
		for _, f := range zr.File {
			d.zipFiles[strings.ToLower(f.Name)] = f
		}
		return
	}
}
