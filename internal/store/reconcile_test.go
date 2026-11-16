// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"database/sql"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/wuweidict/wudict/internal/dict"
	"github.com/wuweidict/wudict/internal/fsx"
)

// hasTable reports whether dbPath has a table named name.
func hasTable(t *testing.T, dbPath, name string) bool {
	t.Helper()
	db, err := sql.Open(driverName, dsnIngest(dbPath))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow("SELECT count(*) FROM sqlite_master WHERE name = ?", name).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n > 0
}

// listingSrc is a source with one packable resource.
type listingSrc struct{ mediaSrc }

func (listingSrc) Resources() []string { return []string{"a.png"} }

// reconcileHooks reads from a two-entry fake and packs from a source with one
// resource (or none), counting how often the source is read.
func reconcileHooks(src string, media dict.Dictionary, reads *int) Hooks {
	return Hooks{
		Reader: func() (dict.Reader, error) {
			*reads++
			return &fakeReader{
				meta:    dict.Meta{Name: "S", Format: "mdx", Path: src},
				entries: []dict.Entry{h("alpha", "<p>one two</p>"), h("beta", "<p>three</p>")},
			}, nil
		},
		Backend: func() (dict.Dictionary, func(), error) { return media, func() {}, nil },
	}
}

// The rules every preparation follows, stated once: a dictionary never
// prepared starts from headwords only; a flag given changes its index and one
// left out keeps what is built; current data is left alone, outdated data only
// when the caller asks; media follows its goal.
func TestReconcile(t *testing.T) {
	t.Setenv("WUDICT_DB_DIR", t.TempDir())
	src := writeSrc(t, filepath.Join(t.TempDir(), "s.mdx"), "source bytes")
	yes := true
	reads := 0
	hooks := reconcileHooks(src, listingSrc{}, &reads)
	run := func(tgt Target) Outcome {
		t.Helper()
		out, err := Reconcile(src, tgt, hooks)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}

	out := run(Target{})
	if !out.Rebuilt || out.Plan != (Plan{}) || out.Why != nil {
		t.Fatalf("never prepared: %+v, want a headwords-only first build", out)
	}
	if _, ok := PreparedFor(src); !ok {
		t.Fatal("the first build is not found as prepared")
	}
	if out = run(Target{}); out.Changed() {
		t.Errorf("current, nothing asked: %+v, want nothing done", out)
	}

	// A contains index from before D170 is no reason to rebuild; the next
	// rebuild for any other reason writes a file without it.
	addRetiredTrigram(t, out.TextDB)
	if out = run(Target{Rebuild: IfOutdated}); out.Changed() {
		t.Errorf("retired trigram, IfOutdated: %+v, want nothing done", out)
	}
	// Full text is an index beside the text, built from the text: nothing is
	// read from the source and the text is not written again (D195).
	reads = 0
	out = run(Target{FullText: &yes})
	if out.Rebuilt || !out.FullTextBuilt || reads != 0 || out.Plan != (Plan{FullText: true}) ||
		!fsx.FileExists(FullTextSibling(out.TextDB)) || !hasTable(t, out.TextDB, "entry_trigram") {
		t.Errorf("-fulltext: %+v, %d reads", out, reads)
	}
	if out = run(Target{}); out.Changed() || out.Plan != (Plan{FullText: true}) {
		t.Errorf("full text left out: %+v, want it kept", out)
	}
	no := false
	out = run(Target{FullText: &no})
	if out.Rebuilt || !out.FullTextRemoved || reads != 0 || out.Plan != (Plan{}) || fsx.FileExists(FullTextSibling(out.TextDB)) {
		t.Errorf("-fulltext=false: %+v, %d reads", out, reads)
	}
	if out = run(Target{FullText: &no}); out.Changed() {
		t.Errorf("-fulltext=false again: %+v, want nothing done", out)
	}
	if out = run(Target{FullText: &yes}); !out.FullTextBuilt {
		t.Errorf("-fulltext once more: %+v", out)
	}

	// Outdated data is rebuilt only for a caller that asks (D151).
	setMeta(t, out.TextDB, "ingest_version", "0")
	reads = 0
	if out = run(Target{}); out.Rebuilt || reads != 0 {
		t.Errorf("outdated, nobody asked: rebuilt=%v reads=%d, want left alone", out.Rebuilt, reads)
	}
	out = run(Target{Rebuild: IfOutdated})
	if !out.Rebuilt || !slices.Contains(out.Why, ReasonIngest) || out.Plan != (Plan{FullText: true}) {
		t.Errorf("outdated, asked: %+v, want rebuilt on the plan it had", out)
	}
	if hasTable(t, out.TextDB, "entry_trigram") || !Inspect(out.TextDB).Plan.FullText {
		t.Error("the rebuild kept the retired trigram table, or lost the full-text index")
	}
	if out = run(Target{Rebuild: Always}); !out.Rebuilt {
		t.Error("Always must rebuild current data")
	}

	// media
	if out = run(Target{Media: MediaKeep}); out.Packed != 0 {
		t.Errorf("MediaKeep with nothing packed packed %d", out.Packed)
	}
	if out = run(Target{Media: MediaOn}); out.Packed != 1 || !MediaPaired(out.TextDB) {
		t.Fatalf("MediaOn: %+v", out)
	}
	if out = run(Target{Media: MediaOn}); !out.MediaCurrent || out.Packed != 0 {
		t.Errorf("MediaOn over paired media: %+v, want it left", out)
	}
	if out = run(Target{Media: MediaRepack}); out.Packed != 1 {
		t.Errorf("MediaRepack: %+v, want repacked", out)
	}
	// a rebuild from an edited source unpairs it, and MediaKeep repacks it
	writeSrc(t, src, "different source bytes")
	out = run(Target{Media: MediaKeep})
	if !out.Rebuilt || out.Packed != 1 || !MediaPaired(out.TextDB) {
		t.Errorf("edited source, MediaKeep: %+v", out)
	}
	if out = run(Target{Media: MediaOff}); !out.MediaRemoved || fsx.FileExists(MediaSibling(out.TextDB)) {
		t.Errorf("MediaOff: %+v", out)
	}

	// nothing to pack leaves no media.db, and says so
	empty := reconcileHooks(src, mediaSrc{}, &reads)
	if out, err := Reconcile(src, Target{Media: MediaOn}, empty); err != nil || !out.MediaEmpty || out.Packed != 0 {
		t.Errorf("nothing to pack: %+v, %v", out, err)
	}

	// -o writes its own database from the flags alone, whatever the library has
	o := filepath.Join(t.TempDir(), "out.text.db")
	out, err := Reconcile(src, Target{Out: o}, hooks)
	if err != nil || !out.Rebuilt || out.TextDB != o || out.Plan != (Plan{}) {
		t.Errorf("Out: %+v, %v", out, err)
	}
}

// A caller holding the folder works in it, whatever its name says.
func TestReconcileInAGivenFolder(t *testing.T) {
	t.Setenv("WUDICT_DB_DIR", t.TempDir())
	src := writeSrc(t, filepath.Join(t.TempDir(), "s.mdx"), "source bytes")
	dir := filepath.Join(DefaultDBDir(), "renamed by hand")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	reads := 0
	out, err := Reconcile(src, Target{Dir: dir}, reconcileHooks(src, mediaSrc{}, &reads))
	if err != nil || out.TextDB != TextDBPath(dir) || !out.Rebuilt {
		t.Fatalf("%+v, %v", out, err)
	}
}

// Callers reaching one dictionary at once - a switch, a lane, a
// self-preparing open - are serialised: the first builds, the rest find the
// text current. Without the lock each would ingest, and the last rename wins.
func TestReconcileConcurrent(t *testing.T) {
	t.Setenv("WUDICT_DB_DIR", t.TempDir())
	src := writeSrc(t, filepath.Join(t.TempDir(), "s.mdx"), "source bytes")
	var reads int
	var mu sync.Mutex // reconcileHooks counts unguarded
	base := reconcileHooks(src, listingSrc{}, &reads)
	hooks := base
	hooks.Reader = func() (dict.Reader, error) {
		mu.Lock()
		defer mu.Unlock()
		return base.Reader()
	}
	const n = 8
	var rebuilt atomic.Int32
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, err := Reconcile(src, Target{}, hooks)
			if err != nil {
				errs <- err
				return
			}
			if out.Rebuilt {
				rebuilt.Add(1)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if got := rebuilt.Load(); got != 1 || reads != 1 {
		t.Errorf("%d concurrent first prepares: %d rebuilt, %d reads; want 1 and 1", n, got, reads)
	}
}

// TestReconcileActivity: an ingest says what it is doing while it runs, and
// nothing once it has ended - also when its reader failed after saying
// something itself.
func TestReconcileActivity(t *testing.T) {
	t.Setenv("WUDICT_DB_DIR", t.TempDir())
	src := writeSrc(t, filepath.Join(t.TempDir(), "s.mdx"), "source bytes")
	reads := 0
	hooks := reconcileHooks(src, mediaSrc{}, &reads)
	var seen []dict.Activity
	hooks.Progress = func(done, total int) {
		if a, ok := dict.ActivityOf(src); ok {
			seen = append(seen, a)
		}
	}
	if _, err := Reconcile(src, Target{}, hooks); err != nil {
		t.Fatal(err)
	}
	// the fake states no entry count: Total stays 0, "not known"
	if len(seen) == 0 || seen[len(seen)-1] != (dict.Activity{Phase: dict.PhaseIndexing, Done: 2}) {
		t.Errorf("during the ingest: %+v", seen)
	}
	if a, ok := dict.ActivityOf(src); ok {
		t.Errorf("after the ingest: %+v", a)
	}

	failing := writeSrc(t, filepath.Join(t.TempDir(), "f.mdx"), "source bytes")
	_, err := Reconcile(failing, Target{}, Hooks{Reader: func() (dict.Reader, error) {
		dict.SetActivity(failing, dict.Activity{Phase: dict.PhaseUnpacking, Done: 1})
		return nil, os.ErrInvalid
	}})
	if err == nil {
		t.Fatal("a failing reader prepared")
	}
	if a, ok := dict.ActivityOf(failing); ok {
		t.Errorf("after a failed ingest: %+v", a)
	}
}
