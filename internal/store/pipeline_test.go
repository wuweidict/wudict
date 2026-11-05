// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wuweidict/wudict/internal/dict"
	"github.com/wuweidict/wudict/internal/sysmem"
)

// laneEntries is a dictionary with everything IngestPlan treats apart:
// articles short and long, aliases, sub-entries, redirects that resolve (by
// case too) and one that does not, entries without a headword - enough of
// them for many batches and every bulk statement size.
func laneEntries(n int) []dict.Entry {
	var es []dict.Entry
	for i := range n {
		w := fmt.Sprintf("word%04d", i)
		body := fmt.Sprintf("<p>%s %s</p>", w, strings.Repeat("lorem ipsum ", i%40))
		e := dict.Entry{Headwords: []string{w}, Body: body, Kind: dict.BodyHTML}
		switch i % 7 {
		case 1:
			e.Headwords = append(e.Headwords, w+"-alias", " ", w)
		case 2:
			e.Headwords = []string{"@sub" + w}
		case 3:
			e = dict.Entry{Headwords: []string{"to" + w}, LinkTo: fmt.Sprintf("WORD%04d", i-3)}
		case 4:
			e = dict.Entry{Headwords: []string{"dangling" + w}, LinkTo: "nowhere" + w}
		case 5:
			e.Headwords = []string{"  "}
		case 6:
			e.Kind, e.Body = dict.BodyText, "plain\ntext "+w
		}
		es = append(es, e)
	}
	return es
}

func ingestPrint(t *testing.T, r dict.Reader, plan Plan) string {
	t.Helper()
	db := filepath.Join(t.TempDir(), TextDBName)
	if _, err := IngestPlan(r, db, plan, nil); err != nil {
		t.Fatal(err)
	}
	fp, err := Fingerprint(db)
	if err != nil {
		t.Fatal(err)
	}
	return fp
}

// TestIngestLanes: one core, the pipeline, switching between them at any
// rhythm, and the headword index built first all prepare the same content.
func TestIngestLanes(t *testing.T) {
	saved := urgentAt
	defer func() { urgentAt = saved }()
	entries := laneEntries(3000)
	meta := dict.Meta{Name: "lanes", Format: "test", Path: filepath.Join(t.TempDir(), "lanes.src")}
	read := func() dict.Reader { return &fakeReader{meta: meta, entries: entries} }
	for _, plan := range []Plan{{}, {FullText: true}} {
		urgentAt = func(string) bool { return false }
		want := ingestPrint(t, read(), plan)
		for name, lane := range map[string]func(string) bool{
			"urgent": func(string) bool { return true },
			"every boundary": func() func(string) bool {
				var n atomic.Int64
				return func(string) bool { return n.Add(1)%2 == 0 }
			}(),
			"every third": func() func(string) bool {
				var n atomic.Int64
				return func(string) bool { return n.Add(1)%3 != 0 }
			}(),
		} {
			urgentAt = lane
			if got := ingestPrint(t, read(), plan); got != want {
				t.Errorf("full text %v, %s: fingerprint %s, one core %s", plan.FullText, name, got, want)
			}
		}
		// Index first: a source that averages more than 2 KiB per entry (its
		// 3,000 entries need a 375 KiB cache, which any budget grants). The
		// fingerprint does not hash the source's path or size.
		big := meta
		big.Path, big.EntryCount = filepath.Join(t.TempDir(), "big.src"), len(entries)
		if err := os.WriteFile(big.Path, make([]byte, 2049*len(entries)), 0o644); err != nil {
			t.Fatal(err)
		}
		urgentAt = func(string) bool { return true }
		if got := ingestPrint(t, &fakeReader{meta: big, entries: entries}, plan); got != want {
			t.Errorf("full text %v, index first: fingerprint %s, index after %s", plan.FullText, got, want)
		}
	}
}

// failReader fails, or panics, at entry n.
type failReader struct {
	fakeReader
	at    int
	panic bool
}

var errBroken = errors.New("broken source")

func (f *failReader) Next() (dict.Entry, error) {
	if f.pos == f.at {
		if f.panic {
			panic("reader exploded")
		}
		return dict.Entry{}, errBroken
	}
	return f.fakeReader.Next()
}

// TestIngestFailures: an error or a panic in a reader, at any point of a
// batch, fails the ingest - never the process - on either lane, and leaves no
// goroutine behind.
func TestIngestFailures(t *testing.T) {
	saved := urgentAt
	defer func() { urgentAt = saved }()
	entries := laneEntries(1200)
	base := runtime.NumGoroutine()
	for _, urgent := range []bool{false, true} {
		urgentAt = func(string) bool { return urgent }
		for _, at := range []int{0, 1, 255, 256, 700} {
			for _, panics := range []bool{false, true} {
				if panics && !urgent {
					continue // on one core a panic is the caller's, which dict.Open recovers
				}
				r := &failReader{fakeReader: fakeReader{meta: dict.Meta{Path: "src.test"}, entries: entries}, at: at, panic: panics}
				_, err := IngestPlan(r, filepath.Join(t.TempDir(), TextDBName), Plan{}, nil)
				switch {
				case panics && (err == nil || !strings.Contains(err.Error(), "parser panic")):
					t.Errorf("urgent %v, panic at %d: err %v", urgent, at, err)
				case !panics && !errors.Is(err, errBroken):
					t.Errorf("urgent %v, error at %d: err %v", urgent, at, err)
				}
			}
		}
	}
	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > base && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if n := runtime.NumGoroutine(); n > base {
		t.Errorf("%d goroutines left behind", n-base)
	}
}

// TestIngestEOFOnBoundary: a reader that ends exactly at a batch boundary,
// or holds nothing at all.
func TestIngestEOFOnBoundary(t *testing.T) {
	saved := urgentAt
	defer func() { urgentAt = saved }()
	urgentAt = func(string) bool { return true }
	for _, n := range []int{0, 1, bulkRows, 2 * bulkRows} {
		var es []dict.Entry
		for i := range n {
			es = append(es, h(fmt.Sprintf("w%d", i), "<p>x</p>"))
		}
		db := filepath.Join(t.TempDir(), TextDBName)
		rep, err := IngestPlan(&fakeReader{meta: dict.Meta{Path: "eof.test"}, entries: es}, db, Plan{}, nil)
		if err != nil || rep.Entries != n {
			t.Errorf("%d entries: %+v, %v", n, rep, err)
		}
	}
}

// fillPool draws everything the preparation budget has left, as other
// preparations would, and returns what gives it back.
func fillPool() func() {
	var a sysmem.Account
	for n := sysmem.Budget(); n > 0; n /= 2 {
		for a.TryAcquire(n) {
		}
	}
	return a.Close
}

// TestIngestNoRoom: with no budget left, a preparation a person waits on runs
// at its floor - the writer encodes what the producer read and takes r.Next
// back - and writes the same content.
func TestIngestNoRoom(t *testing.T) {
	saved := urgentAt
	defer func() { urgentAt = saved }()
	entries := laneEntries(3000)
	meta := dict.Meta{Name: "lanes", Format: "test", Path: "noroom.src"}
	urgentAt = func(string) bool { return false }
	want := ingestPrint(t, &fakeReader{meta: meta, entries: entries}, Plan{FullText: true})
	release := fillPool()
	defer release()
	urgentAt = func(string) bool { return true }
	if got := ingestPrint(t, &fakeReader{meta: meta, entries: entries}, Plan{FullText: true}); got != want {
		t.Errorf("no room: fingerprint %s, want %s", got, want)
	}
}
