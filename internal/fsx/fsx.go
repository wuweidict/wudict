// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Package fsx is the file handling every package shares: reading a file a
// user may have edited by hand without trusting its size or its kind, and
// replacing a file so that a crash leaves the old one or the new one, never a
// truncated one. Standard library only, so any package can import it.
package fsx

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// ErrNotRegular: the path is a directory, a pipe, a device or a socket. A pipe
// or a device reports size 0 and may never end, so it is refused, not read.
var ErrNotRegular = errors.New("not a regular file")

// ErrTooLarge: the file is larger than the caller's limit.
var ErrTooLarge = errors.New("too large")

// ReadBounded reads a regular file of at most max bytes. A missing file is
// fs.ErrNotExist (test with errors.Is); a directory, pipe or device is
// ErrNotRegular; a larger file is ErrTooLarge - also when it grew between the
// size check and the read.
func ReadBounded(path string, max int64) ([]byte, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%s: %w", filepath.Base(path), ErrNotRegular)
	}
	if fi.Size() > max {
		return nil, fmt.Errorf("%s is larger than %d bytes: %w", filepath.Base(path), max, ErrTooLarge)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, fmt.Errorf("%s is larger than %d bytes: %w", filepath.Base(path), max, ErrTooLarge)
	}
	return b, nil
}

// WriteAtomic replaces the file at path with data:
//
//   - through a temp file in the same folder, synced, then renamed over path,
//     so a crash or a power loss leaves the old file or the new one, never a
//     truncated one. The folder is not synced after the rename: a power loss
//     in that window can bring the previous file back, whole;
//   - keeping the mode of the file it replaces - a hand-set 0644 stays 0644 -
//     and using perm for a new file; either way the owner can read and write
//     it, or replacing a mode-000 file would leave one this process cannot
//     read back;
//   - writing where a symlink points, not over the link: a dotfile manager's
//     link keeps working.
func WriteAtomic(path string, data []byte, perm fs.FileMode) error {
	_, err := write(path, bytes.NewReader(data), perm, true, true)
	return err
}

// WriteAtomicExact is WriteAtomic with the mode always perm, whatever the
// replaced file had: for a secret, where an inherited 0644 would leak it.
func WriteAtomicExact(path string, data []byte, perm fs.FileMode) error {
	_, err := write(path, bytes.NewReader(data), perm, false, true)
	return err
}

// WriteAtomicFrom is WriteAtomic for a stream, returning the bytes written.
// On an error the previous file is untouched.
func WriteAtomicFrom(path string, r io.Reader, perm fs.FileMode) (int64, error) {
	return write(path, r, perm, true, true)
}

// WriteReplaceFrom is WriteAtomicFrom without the sync: an interrupted process
// leaves the old file or the new one, never a truncated one, but a power loss
// can lose the file. For writing many files that can be written again from
// where they came from: on darwin a sync is a full device flush (F_FULLFSYNC),
// paid once per file.
func WriteReplaceFrom(path string, r io.Reader, perm fs.FileMode) (int64, error) {
	return write(path, r, perm, true, false)
}

func write(path string, r io.Reader, perm fs.FileMode, keepMode, sync bool) (int64, error) {
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real
	} else if !errors.Is(err, fs.ErrNotExist) {
		return 0, err
	}
	if keepMode {
		if fi, err := os.Stat(path); err == nil {
			perm = fi.Mode().Perm()
		}
		perm |= 0o600
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, err
	}
	// CreateTemp makes the file 0600, so a secret is never readable by others,
	// not even before the Chmod below.
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+"-*")
	if err != nil {
		return 0, err
	}
	defer os.Remove(tmp.Name()) // a no-op once renamed
	n, err := io.Copy(tmp, r)
	if err == nil {
		err = tmp.Chmod(perm)
	}
	if err == nil && sync {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return n, err
	}
	return n, os.Rename(tmp.Name(), path)
}

// RemoveIfExists removes path; one that is already gone is not an error.
func RemoveIfExists(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// FileExists reports that path names something that is not a directory.
func FileExists(path string) bool {
	if path == "" {
		return false
	}
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir()
}

// DirExists reports that path names a directory.
func DirExists(path string) bool {
	if path == "" {
		return false
	}
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

// SamePath reports that a and b name the same file: the same absolute, clean
// path, or - through a symlink, a hard link, or a case-insensitive filesystem
// - the same file on disk.
func SamePath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if a == b {
		return true
	}
	ca, err1 := filepath.Abs(a)
	cb, err2 := filepath.Abs(b)
	if err1 == nil && err2 == nil && filepath.Clean(ca) == filepath.Clean(cb) {
		return true
	}
	sa, err1 := os.Stat(a)
	sb, err2 := os.Stat(b)
	return err1 == nil && err2 == nil && os.SameFile(sa, sb)
}
