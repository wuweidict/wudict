package dsl

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"mime"
	"os"
	"path"
	"path/filepath"
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

	dbPath := cacheDBPath(path, name)
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

// cacheDBPath builds `<cache>/<slug>-<hash8>.text.db`; the hash keys the
// cache to this exact source content (and disambiguates same-named
// dictionaries in other formats).
func cacheDBPath(srcPath, name string) string {
	h := sha256.New()
	if f, err := os.Open(srcPath); err == nil {
		io.CopyN(h, f, 1<<20)
		f.Close()
	}
	hash8 := hex.EncodeToString(h.Sum(nil))[:8]
	return filepath.Join(store.DefaultDBDir(), store.Slug(name)+"-"+hash8+".text.db")
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
