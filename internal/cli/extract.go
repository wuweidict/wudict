// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	"github.com/wuweidict/wudict/internal/dict"
	"github.com/wuweidict/wudict/internal/format/bgl"
	"github.com/wuweidict/wudict/internal/format/dsl"
	"github.com/wuweidict/wudict/internal/format/wmd"
	"github.com/wuweidict/wudict/internal/fsx"
	"github.com/wuweidict/wudict/internal/resource"
	"github.com/wuweidict/wudict/internal/store"
)

// Extracting resources to files, shared by `wudict res` and `wudict dump`:
// open a dictionary for its resources alone (openResources), map the chosen
// names to files below one folder (planRes), write them (writeRes). Which
// names are chosen, and what an existing file means, is each command's own.

// resSource is a dictionary opened for its resources alone.
type resSource struct {
	dict.ResourceOpener
	list  func() []string // every resource name
	close func() error
}

func (d *resSource) Close() error { return d.close() }

// selfPrepared holds the formats whose Dictionary prepares a library folder
// when opened (store.OpenSelfPrepared), each with the containers its resources
// are in. A dump reads these formats' resources from the containers and opens
// no Dictionary, so it never indexes what it dumps (D157).
var selfPrepared = map[string]func(path string) ([]resource.Source, error){
	"dsl":      func(p string) ([]resource.Source, error) { return dsl.MediaSources(p), nil },
	wmd.Format: func(p string) ([]resource.Source, error) { return wmd.MediaSources(p), nil },
	"bgl": func(p string) ([]resource.Source, error) {
		e, err := bgl.ReadEmbedded(p)
		if err != nil {
			return nil, err
		}
		return []resource.Source{e}, nil
	},
}

// selfPreparedFormat names the self-preparing format src is in, as
// openResources takes it, or "" for any other source - for a caller that has
// not read src's entries to learn it. By suffix, as dict.Open dispatches; a
// wudict markdown suffix must also pass the format's content gate, so a plain
// .md that is not one opens through dict.Open, which says why it is not.
func selfPreparedFormat(src string) string {
	if !dict.SelfPrepares(src) {
		return ""
	}
	lower := strings.ToLower(src)
	switch {
	case strings.HasSuffix(lower, ".bgl"):
		return "bgl"
	case strings.HasSuffix(lower, ".dsl"), strings.HasSuffix(lower, ".dsl.dz"):
		return "dsl"
	}
	if dict.Claims(src, func() (io.ReadCloser, error) { return os.Open(src) }) == nil {
		return wmd.Format
	}
	return ""
}

// openResources opens src for its resources, given the format its entries
// were read as. A self-preparing format is read from its containers; any
// other source - a format with an index of its own, a library folder's
// text.db - opens as the Dictionary it is, which prepares nothing.
func openResources(src, format string) (*resSource, error) {
	if read, ok := selfPrepared[format]; ok && !store.IsTextDB(src) {
		srcs, err := read(dict.MainFile(src))
		if err != nil {
			return nil, err
		}
		set := sourceSet(srcs)
		return &resSource{ResourceOpener: set, list: func() []string { return resource.ListAll(srcs) }, close: set.Close}, nil
	}
	d, err := dict.Open(src)
	if err != nil {
		return nil, err
	}
	list := func() []string {
		switch t := d.(type) {
		case *store.Store:
			return t.MediaNames() // packed media.db, if this library folder has one
		case dict.ResourceLister:
			return t.Resources()
		}
		return nil
	}
	return &resSource{ResourceOpener: d, list: list, close: d.Close}, nil
}

// sourceSet serves a name from the first container that holds it, in the
// order the format's Dictionary searches them.
type sourceSet []resource.Source

func (s sourceSet) Resource(name string) (io.ReadCloser, string, error) {
	for _, src := range s {
		if rc, err := src.Open(name); err == nil {
			return rc, resource.MIME(name), nil
		}
	}
	return nil, "", dict.ErrNotFound
}

func (s sourceSet) Close() error {
	var first error
	for _, src := range s {
		if err := src.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// resFilePath maps a stored resource name to a path inside resDir.
//
// The name comes from the DICTIONARY, and a container can hold "..", an
// absolute path, a drive letter or a NUL. resource.Clean removes the climbing
// and the leading slash; safeName then takes each remaining component, so
// nothing this writes can land outside resDir. The structure BELOW resDir is
// kept: a dump's HTML references these paths, and a `wudict res` pattern
// extracts a folder as the folder it is. Only `res` with one name flattens,
// to resBasename.
func resFilePath(resDir, name string) string {
	rel := resource.Clean(name)
	if rel == "" {
		return ""
	}
	parts := strings.Split(rel, "/")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if s := safeName(p); s != "" {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return ""
	}
	return filepath.Join(append([]string{resDir}, out...)...)
}

// safeName makes one path component legal on every host wudict runs on, the
// same rules store.FolderName applies to a library folder - minus its length
// cap, which would silently merge two resources into one file.
func safeName(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r < 0x20, r == 0x7f:
			// control characters (NUL included): drop
		case strings.ContainsRune(`/\:*?"<>|`, r):
			b.WriteByte('-')
		default:
			b.WriteRune(r)
		}
	}
	// trailing dots and spaces are illegal in Windows names; "." and ".." are
	// gone with them, so no component can mean "the parent".
	return strings.TrimRight(strings.TrimSpace(b.String()), " .")
}

// resJob is one resource and the file it is written to; key is the file's
// path below the output folder, folded as resource.Key folds a name.
type resJob struct {
	name, dest, key string
}

// planRes maps names to files under out, keeping the folders the names carry
// (resFilePath: nothing lands outside out). What cannot be written as asked is
// left out and returned as problems:
//
//   - a name with no usable file name;
//   - a name whose file another name already takes: names that differ only in
//     characters a file name cannot hold ("a:b", "a-b"), or only in case,
//     which a case-insensitive disk (macOS, Windows) stores as one file;
//   - a file named like a folder that other resources are written into.
func planRes(names []string, out string) (jobs []resJob, problems []string) {
	byKey := map[string]int{} // key → index in jobs
	for _, name := range names {
		rel := resFilePath("", name)
		if rel == "" {
			problems = append(problems, fmt.Sprintf("%q: no usable file name", name))
			continue
		}
		k := resource.Key(rel)
		if i, ok := byKey[k]; ok {
			problems = append(problems, fmt.Sprintf("%q: %s is the file of %q", name, jobs[i].dest, jobs[i].name))
			continue
		}
		byKey[k] = len(jobs)
		jobs = append(jobs, resJob{name: name, dest: filepath.Join(out, rel), key: k})
	}
	folders := map[string]bool{}
	for k := range byKey {
		for i := range len(k) {
			if k[i] == '/' {
				folders[k[:i]] = true
			}
		}
	}
	kept := jobs[:0]
	for _, j := range jobs {
		if folders[j.key] {
			problems = append(problems, fmt.Sprintf("%q: %s is the folder of other resources", j.name, j.dest))
			continue
		}
		kept = append(kept, j)
	}
	return kept, problems
}

// resWrite copies one resource to its file. Without a sync per file
// (fsx.WriteReplaceFrom): an interrupted run leaves whole files and temp
// files, never a truncated file that looks complete.
func resWrite(d dict.ResourceOpener, j resJob) (int64, error) {
	rc, _, err := d.Resource(j.name)
	if err != nil {
		return 0, err
	}
	defer rc.Close()
	return fsx.WriteReplaceFrom(j.dest, rc, 0o644)
}

// printable is name as one line of output: quoted when it holds a control
// character, which would end the line or reach the terminal as an escape
// sequence.
func printable(name string) string {
	if strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return strconv.Quote(name)
	}
	return name
}

// writeRes writes each job's resource to its file, with a progress meter on a
// terminal. A resource that cannot be read or written is reported and
// skipped: the names come from a container that may be truncated or lying,
// and 40,000 good files are not worth losing to one bad record.
func writeRes(d dict.ResourceOpener, jobs []resJob) (files int, written int64, failed int) {
	m := newMeter("resources", len(jobs), resourceEvery)
	if m != nil {
		m.bytes = true
	}
	for _, j := range jobs {
		n, err := resWrite(d, j)
		m.AddBytes(n)
		m.Add(1)
		if err != nil {
			m.Clear()
			fmt.Fprintf(os.Stderr, "  %s: %v\n", printable(j.name), err)
			failed++
			continue
		}
		files++
		written += n
	}
	m.Clear()
	return files, written, failed
}
