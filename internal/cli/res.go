// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/wuweidict/wudict/internal/dict"
	"github.com/wuweidict/wudict/internal/fsx"
	"github.com/wuweidict/wudict/internal/logx"
	"github.com/wuweidict/wudict/internal/resource"
)

// `wudict res` extracts resources. The arguments decide what it does, never
// the number of resources they match:
//
//   - one name: that resource, to standard output when piped or redirected and
//     to a file named after it on a terminal (resDest);
//   - a pattern (resource.Glob) or several names: files under one folder, in
//     the folders the names carry, written only after every destination is
//     known (planRes) and none of them would replace a file without -f.
//
// An exact name is tried before an argument is read as a pattern, so a ZIM
// path with a literal "?" is still one resource.
//
// The dictionary is opened for its resources alone (openResources): a DSL, a
// BGL or a wudict markdown file is read from its containers and is not indexed
// to extract a file from it (D157).
func cmdRes(args []string) error {
	fs := flag.NewFlagSet("res", flag.ExitOnError)
	out := fs.String("o", "", `one name: the output file; "-" is stdout, a folder means "in there", empty writes a file when stdout is a terminal and stdout otherwise. A pattern or several names: the folder they are written under (default: the current folder)`)
	force := fs.Bool("f", false, "overwrite existing files whose names were derived from the resources")
	dryRun := fs.Bool("dry-run", false, "write nothing: print each resource name and the file it would be written to")
	fs.Parse(args)
	if fs.NArg() < 2 {
		return fmt.Errorf("usage: wudict res [-o out] [-f] [-dry-run] <dictfile> <name|pattern>…")
	}
	src := dict.MainFile(fs.Arg(0))
	if _, err := os.Stat(src); err != nil {
		return err
	}
	d, err := openResources(src, selfPreparedFormat(src))
	if err != nil {
		return err
	}
	defer d.Close()
	names := fs.Args()[1:]
	if len(names) == 1 && !resource.EndsInSeparator(names[0]) {
		rc, mimeType, err := d.Resource(names[0])
		switch {
		case err == nil:
			return resOne(rc, mimeType, names[0], *out, *force, *dryRun)
		case !errors.Is(err, dict.ErrNotFound):
			return err
		case !resource.IsPattern(names[0]):
			return resMissing(d.list(), names[0])
		}
	}
	return resMany(d, names, *out, *force, *dryRun)
}

// resOne writes one resource where resDest says.
func resOne(rc io.ReadCloser, mimeType, name, out string, force, dryRun bool) error {
	defer rc.Close()
	dest, derived, err := resDest(out, name, os.Stdout, os.Stat)
	if err != nil {
		return err
	}
	// A derived name is a guess, so it never destroys anything without -f; a
	// name the user typed is an instruction, and truncating it is what every
	// other tool does with an output path.
	if derived && !force {
		if _, err := os.Stat(dest); err == nil {
			return fmt.Errorf("%s already exists: pass -f to overwrite, or -o to name the output", dest)
		}
	}
	if dryRun {
		if dest == "" {
			dest = "-"
		}
		fmt.Printf("%s\t%s\n", printable(name), dest)
		return nil
	}
	if dest == "" { // stdout
		if _, err := io.Copy(os.Stdout, rc); err != nil {
			return err
		}
		if mimeType != "" {
			fmt.Fprintln(os.Stderr, "mime:", mimeType)
		}
		return nil
	}
	n, err := fsx.WriteAtomicFrom(dest, rc, 0o644)
	if err != nil {
		return err
	}
	// stdout stays free of anything that is not the resource itself, so this
	// remains usable in a pipeline that also asked for a file.
	if mimeType != "" {
		fmt.Fprintf(os.Stderr, "wrote %s (%d bytes, %s)\n", dest, n, mimeType)
	} else {
		fmt.Fprintf(os.Stderr, "wrote %s (%d bytes)\n", dest, n)
	}
	return nil
}

// resMany writes every resource args select under out.
//
// Everything that can stop the run is settled before the first file is
// written: an argument that selects nothing, and existing files without -f. A
// resource that cannot be written as asked (planRes, existingDests) or read is
// reported and skipped, as in a dump: the names come from a container that may
// be truncated or lying.
func resMany(d *resSource, args []string, out string, force, dryRun bool) error {
	if out == "-" {
		return errors.New("-o -: a pattern or several names are written as files; name a folder with -o")
	}
	if out == "" {
		out = "."
	}
	if fi, err := os.Stat(out); err == nil && !fi.IsDir() {
		return fmt.Errorf("-o %s: not a folder", out)
	}
	names, err := selectRes(d, args)
	if err != nil {
		return err
	}
	jobs, problems := planRes(names, out)
	jobs, existing, taken := existingDests(jobs)
	problems = append(problems, taken...)
	for _, p := range problems {
		fmt.Fprintln(os.Stderr, p)
	}
	if dryRun {
		w := bufio.NewWriter(os.Stdout)
		for _, j := range jobs {
			if _, err := fmt.Fprintf(w, "%s\t%s\n", printable(j.name), j.dest); err != nil {
				return err // a closed pipe (`| head`)
			}
		}
		if err := w.Flush(); err != nil {
			return err
		}
	}
	if len(existing) > 0 && !force {
		return overwriteError(existing)
	}
	if dryRun {
		if len(problems) > 0 {
			return fmt.Errorf("%d of %d resources cannot be written", len(problems), len(jobs)+len(problems))
		}
		return nil
	}

	files, written, failed := writeRes(d, jobs)
	failed += len(problems)
	fmt.Fprintf(os.Stderr, "wrote %s (%s) in %s\n", logx.Plural(files, "file", "files"), logx.Size(written), out)
	if failed > 0 {
		return fmt.Errorf("%d of %d resources not written", failed, files+failed)
	}
	return nil
}

// selectRes resolves args to the resources they name, each once, in argument
// order: an argument is an exact name when one resource has it, else a
// pattern when it holds a wildcard or ends in a separator. An argument that
// selects nothing is an error, so a mistyped pattern costs nothing.
//
// Patterns match the listed names minus the operating system's own files
// (resource.Filter): `'**'` is the dictionary's resources, not .DS_Store.
func selectRes(d *resSource, args []string) ([]string, error) {
	var listed []string
	loaded := false
	list := func() []string {
		if !loaded {
			listed, loaded = resource.Filter(d.list()), true
		}
		return listed
	}
	var out []string
	seen := map[string]bool{}
	add := func(name string) {
		if k := resource.Key(name); k != "" && !seen[k] {
			seen[k] = true
			out = append(out, name)
		}
	}
	for _, a := range args {
		if !resource.EndsInSeparator(a) {
			rc, _, err := d.Resource(a)
			if err == nil {
				rc.Close()
				add(a)
				continue
			}
			if !errors.Is(err, dict.ErrNotFound) {
				return nil, fmt.Errorf("%q: %w", a, err)
			}
			if !resource.IsPattern(a) {
				return nil, resMissing(list(), a)
			}
		}
		g, err := resource.NewGlob(a)
		if err != nil {
			return nil, fmt.Errorf("%q: %w", a, err)
		}
		n := 0
		for _, name := range list() {
			if g.Match(name) {
				add(name)
				n++
			}
		}
		if n == 0 {
			if len(list()) == 0 {
				return nil, fmt.Errorf("%q: this dictionary lists no resources", a)
			}
			return nil, fmt.Errorf("%q matches no resource (-dry-run '**' lists them all)", a)
		}
	}
	return out, nil
}

// resMissing is the error for a name no resource has. A name that is a
// folder gets the spelling that extracts it.
func resMissing(names []string, name string) error {
	if g, err := resource.NewGlob(name + "/"); err == nil {
		n := 0
		for _, r := range resource.Filter(names) {
			if g.Match(r) {
				n++
			}
		}
		if n > 0 {
			return fmt.Errorf("%q is a folder: %q extracts its %s", name, name+"/", logx.Plural(n, "resource", "resources"))
		}
	}
	// Flags end at the first argument that is not one, so a flag after the
	// dictionary file arrives here as a name.
	if strings.HasPrefix(name, "-") {
		return fmt.Errorf("no resource %q: flags go before the dictionary file", name)
	}
	return fmt.Errorf("no resource %q", name)
}

// existingDests finds the destinations already taken: by a file, which only
// -f overwrites, or by a folder, which a file cannot replace - that job is
// left out and returned as a problem.
func existingDests(jobs []resJob) (kept []resJob, files, problems []string) {
	kept = jobs[:0]
	for _, j := range jobs {
		// Stat, not Lstat: the write follows a symlink, so the check does.
		if fi, err := os.Stat(j.dest); err == nil {
			if fi.IsDir() {
				problems = append(problems, fmt.Sprintf("%q: %s is a folder", j.name, j.dest))
				continue
			}
			files = append(files, j.dest)
		}
		kept = append(kept, j)
	}
	return kept, files, problems
}

// overwriteError names the first files a run would overwrite, and how many.
func overwriteError(files []string) error {
	const shown = 10
	var b strings.Builder
	fmt.Fprintf(&b, "%s would be overwritten: pass -f to overwrite", logx.Plural(len(files), "file", "files"))
	for _, f := range files[:min(len(files), shown)] {
		fmt.Fprintf(&b, "\n  %s", f)
	}
	if len(files) > shown {
		fmt.Fprintf(&b, "\n  … and %d more", len(files)-shown)
	}
	return errors.New(b.String())
}

// resDest decides where `res` writes. It returns "" for stdout, and reports
// whether the path was DERIVED from the resource name (the caller then refuses
// to overwrite) rather than given by the user.
//
// The guard is "stdout is a terminal", NOT "stdout". Binary on a terminal is
// the accident; binary down a pipe or a redirect is the command working as
// intended, and `wudict res d.mdx img.png > img.png` and `… | file -` must keep
// meaning what they always meant. `-o -` is an explicit instruction and beats
// the terminal check - naming stdout IS the consent.
//
// stat is injected so the directory case is testable without touching disk.
func resDest(out, name string, stdout *os.File, stat func(string) (os.FileInfo, error)) (dest string, derived bool, err error) {
	if out == "-" {
		return "", false, nil
	}
	if out != "" {
		// An existing directory means "put it in there"; anything else is the
		// file name itself, parents included (mkdir -p is done at write time).
		if fi, err := stat(out); err == nil && fi.IsDir() {
			base := resBasename(name)
			if base == "" {
				return "", false, fmt.Errorf("resource %q has no usable file name: name the output file with -o", name)
			}
			return filepath.Join(out, base), false, nil
		}
		return out, false, nil
	}
	if !isTerminal(stdout) {
		return "", false, nil
	}
	base := resBasename(name)
	if base == "" {
		return "", false, fmt.Errorf("resource %q has no usable file name: pass -o to name the output, or -o - for stdout", name)
	}
	return base, true, nil
}

// resBasename reduces a resource name to a single, safe file name in the
// current directory. It returns "" when nothing usable is left.
//
// The name comes from the DICTIONARY, not from the user: MDX stores paths as
// `\audio\x.spx`, and a hostile or merely broken container can hold "..", an
// absolute path, a Windows drive letter, or a NUL. So the whole path is
// discarded and only the last element kept - the flat file the user asked for.
// Recreating the container's directory tree from those strings is what turns
// this into zip-slip, and is why -o exists for anyone who wants a specific
// place. The server solved the same problem the same way (D59).
func resBasename(name string) string {
	// Both separators, whatever the host: an .mdd written on Windows is read
	// on Linux, where a backslash is an ordinary character and would otherwise
	// survive into the file name.
	name = strings.ReplaceAll(name, "\\", "/")
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		name = name[i+1:]
	}
	// A drive-relative name ("c:x.png") keeps a colon that means something on
	// Windows and nothing here; take what follows it.
	if i := strings.LastIndexByte(name, ':'); i >= 0 {
		name = name[i+1:]
	}
	name = strings.TrimSpace(name)
	// "." and ".." are not file names, and a control byte in one is a
	// terminal-escape vector as much as a filesystem problem.
	if name == "." || name == ".." || strings.ContainsAny(name, "\x00") {
		return ""
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return ""
		}
	}
	return name
}

// isTerminal reports whether f is a character device - the same test logx uses
// for stderr, kept here rather than shared because logx is about progress
// output and this is about not vomiting a PNG into someone's shell.
func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}
