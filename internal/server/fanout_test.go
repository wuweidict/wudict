// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The budget is spent in two steps — a reservation from what is already known,
// then a correction once the open has happened — and the interesting property
// is what a refusal does NOT do: a dictionary too big for the remainder is
// declined without consuming the remainder, so one oversized dictionary early
// in the user's preference order cannot starve the cheap ones behind it.
func TestFanoutBudgetPacksRatherThanStops(t *testing.T) {
	f := &fanout{}
	f.left.Store(100)

	if !f.admit(0) {
		t.Fatal("an unknown cost must be admitted while there is budget left")
	}
	f.settle(0, 60) // it turned out to cost 60
	if got := f.left.Load(); got != 40 {
		t.Fatalf("after a 60-byte open: left = %d, want 40", got)
	}
	if f.admit(50) {
		t.Error("50 must not be admitted against 40 left")
	}
	if got := f.left.Load(); got != 40 {
		t.Errorf("a refusal spent the budget: left = %d, want 40", got)
	}
	if !f.admit(30) {
		t.Error("30 fits in 40 and must be admitted")
	}
	if got := f.left.Load(); got != 10 {
		t.Fatalf("after reserving 30: left = %d, want 10", got)
	}
	// An open that fails returns its reservation: nothing was materialised.
	f.settle(30, 0)
	if got := f.left.Load(); got != 40 {
		t.Errorf("a failed open kept its reservation: left = %d, want 40", got)
	}
	// Overshoot is allowed to go negative — that is what closes the fan-out to
	// everything after the dictionary that blew the budget.
	if !f.admit(0) {
		t.Fatal("still budget left")
	}
	f.settle(0, 100)
	if got := f.left.Load(); got >= 0 {
		t.Errorf("left = %d, want negative after an overshooting open", got)
	}
	if f.admit(0) {
		t.Error("nothing may be admitted once the budget is spent")
	}
}

// A nil fanout is "no cap", and must stay free of both allocation and special
// cases at the call sites.
func TestNilFanoutAdmitsEverything(t *testing.T) {
	var f *fanout
	if !f.admit(1 << 40) {
		t.Error("an uncapped search must admit anything")
	}
	f.settle(0, 1<<40) // must not panic
}

// End to end on a real registry: the first search of a never-opened dictionary
// cannot be priced (nothing is known about it yet), so it is admitted and then
// charged — and that charge is what closes the fan-out. The second search knows
// the price and refuses before paying it, which is the whole point.
func TestSearchBudgetRefusesUnpreparedDictionariesOnceSpent(t *testing.T) {
	isolatedDBDir(t)
	dir := t.TempDir()
	for i := 0; i < 4; i++ {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("p%d.fake", i)), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	reg, err := NewRegistry([]string{dir}, false)
	if err != nil {
		t.Fatal(err)
	}
	if reg.fanout() != nil {
		t.Fatal("setup: no budget is configured, so a search must be uncapped")
	}
	reg.SetSearchBudget(1) // one byte: enough to admit one unknown, never two

	f := reg.fanout()
	if f == nil {
		t.Fatal("a configured budget must produce a fan-out")
	}
	opened, refused := 0, 0
	var last error
	for _, e := range reg.all() {
		if _, err := e.openWithin(f); err != nil {
			var th tooHeavy
			if !errors.As(err, &th) {
				t.Fatalf("unexpected open error: %v", err)
			}
			last = err
			refused++
			continue
		}
		opened++
	}
	if opened != 1 || refused != 3 {
		t.Fatalf("opened %d and refused %d, want 1 and 3", opened, refused)
	}
	if last == nil || last.Error() == "" {
		t.Error("a refusal must say something the client can show")
	}

	// Second fan-out, same registry: the one that is still open is free and
	// must never be refused — the cap exists to stop memory being created, and
	// declining what is already resident costs results for no saving.
	f2 := reg.fanout()
	first := reg.all()[0]
	if _, err := first.openWithin(f2); err != nil {
		t.Errorf("an already-open backend must not be capped: %v", err)
	}

	// Now the priced path, which is what the cap is actually for: evict the one
	// dictionary whose cost is known, and it must be refused BEFORE it is
	// opened rather than after — no bytes materialised, and the estimate
	// reported so the client can say what it declined and how much it would
	// have cost.
	price := first.lastWeight.Load()
	if price <= 0 {
		t.Fatalf("an opened preview backend must remember its cost, got %d", price)
	}
	first.evict()
	f3 := &fanout{}
	f3.left.Store(price - 1)
	before := reg.previewBytes()
	_, err = first.openWithin(f3)
	var th tooHeavy
	if !errors.As(err, &th) {
		t.Fatalf("a dictionary priced above the remaining budget must be refused, got %v", err)
	}
	if th.bytes != price {
		t.Errorf("refusal reported %d bytes, want the remembered %d", th.bytes, price)
	}
	if after := reg.previewBytes(); after != before {
		t.Errorf("a refused open materialised %d bytes", after-before)
	}
	if got := f3.left.Load(); got != price-1 {
		t.Errorf("the refusal spent budget: left = %d, want %d", got, price-1)
	}
}

// Over HTTP, a capped search must still answer for every dictionary it was
// asked about. A silently missing slot is the failure mode that would make this
// cap dishonest — the user would read "no results" where the truth is "not
// looked", so the refusal travels to the client as the slot's error, with the
// dictionary named.
func TestCappedSearchReportsWhatItDeclined(t *testing.T) {
	isolatedDBDir(t)
	dir := t.TempDir()
	for i := 0; i < 4; i++ {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("p%d.fake", i)), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	reg, err := NewRegistry([]string{dir}, false)
	if err != nil {
		t.Fatal(err)
	}
	s := New(reg)

	// Warm the prices first, then shed the backends. A fan-out is parallel, so
	// the FIRST search of a cold library cannot be capped by cost — every opener
	// reads "unknown" before any of them has settled a charge, and unknown is
	// admitted. That is by design (§8.2: a price has to be paid once to be
	// learned); the cap bites from the second search on, which is the state this
	// test needs.
	if hits := searchStream(t, s, "/api/search?q=beta&mode=prefix&dict=all"); len(hits) != 4 {
		t.Fatalf("setup: uncapped search answered %d of 4", len(hits))
	}
	price := reg.all()[0].lastWeight.Load()
	if price <= 0 {
		t.Fatalf("setup: no price was learned, got %d", price)
	}
	for _, e := range reg.all() {
		e.evict()
	}
	// Room for exactly one: admit() is a CAS loop, so of four equally priced
	// dictionaries precisely one wins the whole budget and the rest are refused.
	reg.SetSearchBudget(price)

	hits := searchStream(t, s, "/api/search?q=beta&mode=prefix&dict=all")
	if len(hits) != 4 {
		t.Fatalf("every dictionary must be accounted for: got %d hits of 4", len(hits))
	}
	declined, answered := 0, 0
	for _, h := range hits {
		switch {
		case h.Error != "":
			declined++
			if !strings.Contains(h.Error, "not searched") {
				t.Errorf("unhelpful refusal: %q", h.Error)
			}
			if h.Name == "" {
				t.Error("a declined slot must still name its dictionary")
			}
		case len(h.Results) > 0:
			answered++
		}
	}
	if answered == 0 || declined == 0 {
		t.Fatalf("answered %d, declined %d — want some of each under a one-dictionary budget", answered, declined)
	}

	// Uncapped, the same query answers everything: the cap is the only reason
	// anything was declined.
	reg.SetSearchBudget(0)
	hits = searchStream(t, s, "/api/search?q=beta&mode=prefix&dict=all")
	for _, h := range hits {
		if h.Error != "" {
			t.Errorf("uncapped search still declined %s: %s", h.Name, h.Error)
		}
	}
}

// The budget must never touch a prepared dictionary. It holds no headword
// index, so refusing it would drop results and save nothing — and on Android,
// where the cap is on by default, prepared is the steady state the whole
// library is heading for.
func TestSearchBudgetNeverCapsPreparedDictionaries(t *testing.T) {
	isolatedDBDir(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "x.dsl"), []byte(sampleDSL), 0o644); err != nil {
		t.Fatal(err)
	}
	reg, err := NewRegistry([]string{dir}, false)
	if err != nil {
		t.Fatal(err)
	}
	e := reg.all()[0]
	if _, err := e.open(); err != nil { // DSL prepares itself on open
		t.Fatal(err)
	}
	if w := e.weight.Load(); w != 0 {
		t.Fatalf("setup: a prepared dictionary must weigh nothing, got %d", w)
	}
	e.drop(true) // close it, so the next open is a real one

	f := &fanout{}
	f.left.Store(-1 << 20) // a budget already blown by something else
	if _, err := e.openWithin(f); err != nil {
		t.Errorf("prepared dictionary refused by the search budget: %v", err)
	}
}
