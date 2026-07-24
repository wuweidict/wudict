package search

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/glowinthedark/gonow-dict/internal/dict"
)

// fake is a minimal Dictionary; fuzzy support is toggled per instance.
type fake struct {
	name     string
	words    []string
	hasFuzzy bool
	err      error
}

func (f *fake) Meta() dict.Meta { return dict.Meta{Name: f.name, Format: "fake"} }
func (f *fake) Caps() dict.Caps {
	return dict.Caps{Exact: true, Prefix: true, Fuzzy: f.hasFuzzy}
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
func (f *fake) Fuzzy(w string, n int) ([]dict.Result, error) {
	return f.match(func(x string) bool { return strings.Contains(x, w) }, n)
}

func TestAllOrderAndModes(t *testing.T) {
	dicts := []dict.Dictionary{
		&fake{name: "A", words: []string{"casa", "casona"}},
		&fake{name: "B", words: []string{"casa"}, hasFuzzy: true},
		&fake{name: "C", err: errors.New("boom")},
	}
	hits := All(context.Background(), dicts, Prefix, "cas", 10)
	if len(hits) != 3 || hits[0].Meta.Name != "A" || hits[1].Meta.Name != "B" || hits[2].Meta.Name != "C" {
		t.Fatalf("order not preserved: %+v", hits)
	}
	if len(hits[0].Results) != 2 || len(hits[1].Results) != 1 {
		t.Errorf("wrong results: %+v", hits)
	}
	if hits[2].Err == nil {
		t.Error("error not propagated")
	}

	// fuzzy: A lacks the capability -> skipped; B serves
	hits = All(context.Background(), dicts[:2], Fuzzy, "as", 10)
	if !hits[0].Skipped {
		t.Error("A should be skipped for fuzzy")
	}
	if hits[1].Skipped || len(hits[1].Results) != 1 {
		t.Errorf("B fuzzy failed: %+v", hits[1])
	}
}

func TestAllPerDictLimit(t *testing.T) {
	d := &fake{name: "A", words: []string{"a1", "a2", "a3"}}
	hits := All(context.Background(), []dict.Dictionary{d}, Prefix, "a", 2)
	if len(hits[0].Results) != 2 {
		t.Errorf("perDict limit ignored: %d", len(hits[0].Results))
	}
}

func TestAllCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	dicts := make([]dict.Dictionary, 30)
	for i := range dicts {
		dicts[i] = &fake{name: "X", words: []string{"w"}}
	}
	hits := All(ctx, dicts, Exact, "w", 1)
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
