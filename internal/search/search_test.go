// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package search

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wuweidict/wudict/internal/dict"
	"github.com/wuweidict/wudict/internal/wildcard"
)

// fake is a minimal prepared-style backend (a dict.Searcher); the wildcard
// capability is toggled per instance.
type fake struct {
	name        string
	words       []string
	hasWildcard bool
	err         error
}

func (f *fake) Meta() dict.Meta { return dict.Meta{Name: f.name, Format: "fake"} }
func (f *fake) Caps() dict.Caps {
	return dict.Caps{Exact: true, Prefix: true, Wildcard: f.hasWildcard}
}
func (f *fake) Close() error                    { return nil }
func (f *fake) Keywords(offset, n int) []string { return nil }
func (f *fake) Resource(string) (io.ReadCloser, string, error) {
	return nil, "", dict.ErrNotFound
}
func (f *fake) match(pred func(string) bool, limit int) ([]dict.Result, error) {
	if f.err != nil {
		return nil, f.err
	}
	var out []dict.Result
	for _, w := range f.words {
		if pred(w) {
			out = append(out, dict.Result{Headword: w, Body: "<p>" + w + "</p>"})
			if len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}
func (f *fake) Exact(w string, n int) ([]dict.Result, error) {
	return f.match(func(x string) bool { return x == w }, n)
}
func (f *fake) Prefix(w string, n int) ([]dict.Result, error) {
	return f.match(func(x string) bool { return strings.HasPrefix(x, w) }, n)
}
func (f *fake) ExactContext(_ context.Context, w string, n int) ([]dict.Result, error) {
	return f.Exact(w, n)
}
func (f *fake) PrefixContext(_ context.Context, w string, n int) ([]dict.Result, error) {
	return f.Prefix(w, n)
}
func (f *fake) FullTextContext(context.Context, string, int) ([]dict.Result, error) {
	return nil, dict.ErrUnsupported
}
func (f *fake) FullTextMatch(context.Context, string, int) ([]dict.Result, error) {
	return nil, dict.ErrUnsupported
}
func (f *fake) WildcardContext(_ context.Context, pattern string, n int) ([]dict.Result, error) {
	p, err := wildcard.Parse(pattern)
	if err != nil {
		return nil, err
	}
	m := p.Matcher()
	return f.match(func(x string) bool { return m.Match([]byte(x)) }, n)
}

// all runs the fan-out over dictionaries that are already open and returns
// the hits in input order.
func all(ctx context.Context, dicts []dict.Dictionary, mode Mode, term string, n int) []Hit {
	openers := make([]Opener, len(dicts))
	for i, d := range dicts {
		openers[i] = func() (dict.Dictionary, error) { return d, nil }
	}
	hits := make([]Hit, len(dicts))
	StreamOpen(ctx, openers, mode, term, n, func(i int, h Hit) { hits[i] = h })
	return hits
}

func TestStreamCoversAllSlots(t *testing.T) {
	dicts := []dict.Dictionary{
		&fake{name: "A", words: []string{"casa", "casona"}},
		&fake{name: "B", words: []string{"nada"}},
		&fake{name: "C", err: errors.New("boom")},
	}
	seen := make([]*Hit, len(dicts))
	openers := make([]Opener, len(dicts))
	for i, d := range dicts {
		openers[i] = func() (dict.Dictionary, error) { return d, nil }
	}
	StreamOpen(context.Background(), openers, Prefix, "cas", 10, func(i int, h Hit) {
		hc := h
		seen[i] = &hc
	})
	// every slot must be filled exactly once, indexed by input position
	if seen[0] == nil || len(seen[0].Results) != 2 {
		t.Fatalf("slot 0: %+v", seen[0])
	}
	if seen[1] == nil || len(seen[1].Results) != 0 {
		t.Fatalf("slot 1 (no match): %+v", seen[1])
	}
	if seen[2] == nil || seen[2].Err == nil {
		t.Fatalf("slot 2 (error) must propagate: %+v", seen[2])
	}
}

func TestStreamOpenSurfacesOpenError(t *testing.T) {
	openers := []Opener{
		func() (dict.Dictionary, error) { return &fake{name: "A", words: []string{"casa"}}, nil },
		func() (dict.Dictionary, error) { return nil, errors.New("open boom") },
	}
	seen := make([]*Hit, len(openers))
	StreamOpen(context.Background(), openers, Prefix, "cas", 10, func(i int, h Hit) {
		hc := h
		seen[i] = &hc
	})
	if seen[0] == nil || len(seen[0].Results) != 1 {
		t.Fatalf("slot 0 (opened): %+v", seen[0])
	}
	if seen[1] == nil || seen[1].Err == nil {
		t.Fatalf("slot 1 must carry the open error: %+v", seen[1])
	}
}

func TestFanOutOrderAndModes(t *testing.T) {
	dicts := []dict.Dictionary{
		&fake{name: "A", words: []string{"casa", "casona"}},
		&fake{name: "B", words: []string{"casa"}, hasWildcard: true},
		&fake{name: "C", err: errors.New("boom")},
	}
	hits := all(context.Background(), dicts, Prefix, "cas", 10)
	if len(hits) != 3 || hits[0].Meta.Name != "A" || hits[1].Meta.Name != "B" || hits[2].Meta.Name != "C" {
		t.Fatalf("order not preserved: %+v", hits)
	}
	if len(hits[0].Results) != 2 || len(hits[1].Results) != 1 {
		t.Errorf("wrong results: %+v", hits)
	}
	if hits[2].Err == nil {
		t.Error("error not propagated")
	}

	// wildcard: A lacks the capability -> skipped; B serves
	hits = all(context.Background(), dicts[:2], Wildcard, "*as*", 10)
	if !hits[0].Skipped {
		t.Error("A should be skipped for wildcard")
	}
	if hits[1].Skipped || len(hits[1].Results) != 1 {
		t.Errorf("B wildcard failed: %+v", hits[1])
	}
}

func TestFanOutPerDictLimit(t *testing.T) {
	d := &fake{name: "A", words: []string{"a1", "a2", "a3"}}
	hits := all(context.Background(), []dict.Dictionary{d}, Prefix, "a", 2)
	if len(hits[0].Results) != 2 {
		t.Errorf("perDict limit ignored: %d", len(hits[0].Results))
	}
}

func TestFanOutCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	dicts := make([]dict.Dictionary, 30)
	for i := range dicts {
		dicts[i] = &fake{name: "X", words: []string{"w"}}
	}
	hits := all(ctx, dicts, Exact, "w", 1)
	if len(hits) != 30 {
		t.Fatalf("want 30 hits, got %d", len(hits))
	}
	// with a pre-cancelled context most workers must bail with ctx.Err
	// (some may have grabbed a semaphore slot before observing cancel)
	cancelled := 0
	for _, h := range hits {
		if errors.Is(h.Err, context.Canceled) {
			cancelled++
		}
	}
	if cancelled == 0 {
		t.Error("no worker observed cancellation")
	}
}

// A cancelled fan-out must not open anything. The semaphore select alone does
// not guarantee this: when a slot is free, both of its cases are ready and the
// runtime picks one at random, so roughly half of an unbounded queue would sail
// past a context that was already dead and materialise a dictionary apiece. On
// a phone that is the difference between an abandoned keystroke costing nothing
// and costing a gigabyte (docs.local/PERF.md §8.7). With 200 openers the random path
// is indistinguishable from broken, which is the point of the count.
func TestStreamOpenCancelledBeforeStartOpensNothing(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var opened atomic.Int32
	openers := make([]Opener, 200)
	for i := range openers {
		openers[i] = func() (dict.Dictionary, error) {
			opened.Add(1)
			return &fake{name: "X", words: []string{"w"}}, nil
		}
	}
	seen := 0
	StreamOpen(ctx, openers, Exact, "w", 1, func(i int, h Hit) {
		if !errors.Is(h.Err, context.Canceled) {
			t.Errorf("slot %d: want context.Canceled, got %+v", i, h.Err)
		}
		seen++
	})
	if seen != len(openers) {
		t.Errorf("every slot must still be answered: got %d of %d", seen, len(openers))
	}
	if n := opened.Load(); n != 0 {
		t.Errorf("%d dictionaries opened for a request nobody is waiting for", n)
	}
}

// The fan-out width is one number shared by both places that touch every
// dictionary at once (search and the dictionary list), and on Android it is
// lowered to keep the work off the big cores (D64). Zero means unset, not
// "no workers": a fan-out of zero would deadlock, so the floor is one and
// nonsense is ignored rather than obeyed.
func TestWorkers(t *testing.T) {
	t.Cleanup(func() { workers.Store(0) })

	workers.Store(0)
	if got := Workers(); got != defaultWorkers {
		t.Errorf("unset: Workers() = %d, want %d", got, defaultWorkers)
	}
	SetWorkers(3)
	if got := Workers(); got != 3 {
		t.Errorf("after SetWorkers(3): %d", got)
	}
	for _, n := range []int{0, -1} {
		SetWorkers(n)
		if got := Workers(); got != 3 {
			t.Errorf("SetWorkers(%d) must be ignored, got %d", n, got)
		}
	}
}

// A prepared backend is handed the request's context, so cancelling a search
// stops its query inside SQLite instead of letting it run to completion for
// output nobody reads. ctxDict stands in for the store: its query ends only
// when its context does.
type ctxDict struct{ fake }

func (c *ctxDict) ExactContext(ctx context.Context, _ string, _ int) ([]dict.Result, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestQueryHandsTheContextToTheBackend(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	d := &ctxDict{fake{name: "S"}}
	done := make(chan Hit, 1)
	go func() { done <- Query(ctx, d, Exact, "w", 5) }()
	cancel()
	select {
	case h := <-done:
		if !errors.Is(h.Err, context.Canceled) {
			t.Fatalf("want the backend's cancellation, got %+v", h)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the backend never saw the cancellation")
	}
}

// A direct backend has no index for wildcard or full-text; Query reports it
// skipped rather than failing, exactly as the fan-out does.
func TestQuerySkipsWhatADirectBackendCannotDo(t *testing.T) {
	for _, m := range []Mode{Wildcard, FullText} {
		if h := Query(context.Background(), okDict{}, m, "w", 5); !h.Skipped {
			t.Errorf("%v on a direct backend: %+v, want skipped", m, h)
		}
	}
}

func TestParseMode(t *testing.T) {
	for in, want := range map[string]Mode{"": Prefix, "prefix": Prefix, "fuzzy": Prefix,
		"exact": Exact, "fts": FullText, "wildcard": Wildcard} {
		if got, err := ParseMode(in); err != nil || got != want {
			t.Errorf("ParseMode(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, retired := range []string{"regex", "contains"} { // contains: D170
		if _, err := ParseMode(retired); err == nil {
			t.Errorf("ParseMode(%q): an unknown mode must be refused", retired)
		}
	}
}

// keyed is a prepared backend that can list its keys (dict.Keyer). Its keys
// are marked, so a test can tell which of the two paths answered.
type keyed struct{ fake }

func (k *keyed) keys(rs []dict.Result, err error) ([]string, error) {
	var out []string
	for _, r := range rs {
		out = append(out, r.Headword+"!")
	}
	return out, err
}
func (k *keyed) PrefixKeys(ctx context.Context, w string, n int) ([]string, error) {
	return k.keys(k.PrefixContext(ctx, w, n))
}
func (k *keyed) WildcardKeys(ctx context.Context, p string, n int) ([]string, error) {
	return k.keys(k.WildcardContext(ctx, p, n))
}

// StreamKeys answers with headwords and no article: from the keys of a
// backend that lists them, and from the ordinary query of one that does not.
func TestStreamKeys(t *testing.T) {
	words := []string{"casa", "casar", "cosa"}
	openers := []Opener{
		func() (dict.Dictionary, error) { return &keyed{fake{name: "K", words: words, hasWildcard: true}}, nil },
		func() (dict.Dictionary, error) { return &fake{name: "F", words: words}, nil },
	}
	for _, tc := range []struct {
		mode Mode
		term string
		want [2][]string
		skip bool // the second backend has no wildcard search
	}{
		{Prefix, "cas", [2][]string{{"casa!", "casar!"}, {"casa", "casar"}}, false},
		{Wildcard, "c?sa", [2][]string{{"casa!", "cosa!"}, nil}, true},
		{Exact, "cosa", [2][]string{{"cosa"}, {"cosa"}}, false},
	} {
		hits := make([]Hit, 2)
		StreamKeys(context.Background(), openers, tc.mode, tc.term, 10, func(i int, h Hit) { hits[i] = h })
		for i, h := range hits {
			var got []string
			for _, r := range h.Results {
				if r.Body != "" {
					t.Errorf("%v %q: %q came with an article", tc.mode, tc.term, r.Headword)
				}
				got = append(got, r.Headword)
			}
			if h.Err != nil || strings.Join(got, ",") != strings.Join(tc.want[i], ",") {
				t.Errorf("%v %q, backend %d = %v, %v, want %v", tc.mode, tc.term, i, got, h.Err, tc.want[i])
			}
		}
		if hits[1].Skipped != tc.skip {
			t.Errorf("%v %q: skipped = %v, want %v", tc.mode, tc.term, hits[1].Skipped, tc.skip)
		}
	}
}
