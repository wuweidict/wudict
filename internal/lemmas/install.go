// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package lemmas

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/wuweidict/wudict/internal/morph"
)

// Local is one lemma file already in LEMMA_DIR.
type Local struct {
	Code string
	Path string
	Size int64
}

// Installed indexes LEMMA_DIR the way the lemmatizer does - through
// morph.LemmaFile, so the two cannot disagree about what a name means - and
// additionally reports the files the lemmatizer will IGNORE.
//
// That second return is the point. morph resolves a collision by taking the
// first name in sorted order and silently dropping the rest, which is the
// right thing for a lookup path and a mystery for a user: "ru.tsv" sorts
// before "ru.tsv.gz", so a stale uncompressed file quietly wins over the one
// just downloaded. It is reported, never deleted - the shadowed file may well
// be the one the user wrote by hand.
func Installed(dir string) (map[string]Local, []string) {
	if dir == "" {
		return nil, nil
	}
	ents, err := os.ReadDir(dir) // sorted, same order morph resolves in
	if err != nil {
		return nil, nil
	}
	out := map[string]Local{}
	var shadowed []string
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		code, ok := morph.LemmaFile(e.Name())
		if !ok {
			continue
		}
		p := filepath.Join(dir, e.Name())
		if won, dup := out[code]; dup {
			shadowed = append(shadowed, fmt.Sprintf("%s is ignored: %s already supplies %s",
				p, filepath.Base(won.Path), code))
			continue
		}
		var size int64
		if fi, err := e.Info(); err == nil {
			size = fi.Size()
		}
		out[code] = Local{Code: code, Path: p, Size: size}
	}
	return out, shadowed
}

// Hash is the sha256 of a file on disk, hex, matching the manifest's field. It
// is what distinguishes "installed" from "installed, but not the published
// version" - a size comparison would call a truncated download up to date.
func Hash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Install downloads one language into dir and returns the path it wrote.
//
// The file is streamed to a temporary name in the SAME directory, hashed and
// length-checked as it arrives, and only then renamed into place. Nothing
// partially downloaded is ever visible to the lemmatizer: the temporary name
// is dot-prefixed and does not end in an extension morph recognises, so a
// killed download leaves a file that is skipped rather than loaded, and a
// failed one leaves nothing at all.
//
// The name it lands under is built here from the validated language code -
// never from the manifest - so a hostile or broken catalogue cannot choose
// where wudict writes.
//
// progress, when non-nil, is called with the running byte count.
func (c *Catalog) Install(ctx context.Context, dir string, e Entry, progress func(int64)) (string, error) {
	if dir == "" {
		return "", fmt.Errorf("no lemma folder configured (LEMMA_DIR)")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	rc, err := c.src.open(ctx, e.File)
	if err != nil {
		return "", fmt.Errorf("%s: %w", e.Code, err)
	}
	defer rc.Close()

	f, err := os.CreateTemp(dir, ".wudict-lemma-*.part")
	if err != nil {
		return "", err
	}
	tmp := f.Name()
	defer os.Remove(tmp) // no-op once the rename has succeeded

	h := sha256.New()
	var w io.Writer = io.MultiWriter(f, h)
	if progress != nil {
		w = io.MultiWriter(f, h, &counter{f: progress})
	}
	// LimitReader at Size+1 so "longer than declared" is caught as a mismatch
	// rather than by filling the disk with whatever the server felt like sending.
	n, err := io.Copy(w, io.LimitReader(rc, e.Size+1))
	if err != nil {
		f.Close()
		return "", fmt.Errorf("%s: %w", e.Code, err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	if n != e.Size {
		return "", fmt.Errorf("%s: expected %d bytes, got %d", e.Code, e.Size, n)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != e.SHA256 {
		return "", fmt.Errorf("%s: checksum mismatch (expected %s, got %s)", e.Code, e.SHA256, got)
	}
	if err := os.Chmod(tmp, 0o644); err != nil {
		return "", err
	}
	dst := filepath.Join(dir, e.LocalName())
	if err := os.Rename(tmp, dst); err != nil {
		return "", err
	}
	return dst, nil
}

// Remove deletes every lemma file in dir that supplies code, not just the one
// morph happens to be using: after `wudict lemmas remove ru`, ru must actually
// be gone, and leaving a shadowed second file behind would mean it was not.
// Returns the paths removed, sorted; none is not an error.
func Remove(dir, code string) ([]string, error) {
	if dir == "" {
		return nil, nil
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var gone []string
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		if c, ok := morph.LemmaFile(e.Name()); !ok || c != code {
			continue
		}
		p := filepath.Join(dir, e.Name())
		if err := os.Remove(p); err != nil {
			return gone, err
		}
		gone = append(gone, p)
	}
	sort.Strings(gone)
	return gone, nil
}

// counter reports download progress without buffering anything.
type counter struct {
	n int64
	f func(int64)
}

func (c *counter) Write(p []byte) (int, error) {
	c.n += int64(len(p))
	c.f(c.n)
	return len(p), nil
}
