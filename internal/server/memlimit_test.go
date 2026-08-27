// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/metrics"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	// The server package registers no formats - the CLI does. Without these a
	// sweep over a real corpus would silently discover only the handful of
	// formats some other test happens to have pulled in, and measure nothing.
	_ "github.com/wuweidict/wudict/internal/format/bgl"
	_ "github.com/wuweidict/wudict/internal/format/dsl"
	_ "github.com/wuweidict/wudict/internal/format/mdx"
	_ "github.com/wuweidict/wudict/internal/format/slob"
	_ "github.com/wuweidict/wudict/internal/format/stardict"
)

// A soft memory limit has exactly one lever: collect harder. Set below what
// the program genuinely keeps live, that means collecting continuously -
// re-tracing a live set that never shrinks, burning CPU (and on a phone,
// battery and heat) to free nothing. That is the failure mode D64's Android
// MEMORY_LIMIT default has to be proved clear of, and it cannot be proved by
// reading code: it depends on how large the live set actually is under a real
// corpus.
//
// So this measures it. Off by default and never part of `make check` - it
// wants a dictionary library on disk and minutes of wall time:
//
//	WUDICT_PERF_CORPUS=~/Downloads/Language \
//	  go test ./internal/server -run TestMemoryLimitSweep -v -timeout 60m
//
// The sweep runs each limit in a FRESH CHILD PROCESS. debug.SetMemoryLimit is
// process-global and the heap carries history, so measuring several limits in
// one process would measure the order they were tried in.
const (
	envCorpus  = "WUDICT_PERF_CORPUS"    // dictionary tree to open; unset → skipped
	envLimitMB = "WUDICT_PERF_LIMIT_MB"  // set by the parent: this child's limit, 0 = none
	envBudgetM = "WUDICT_PERF_BUDGET_MB" // preview budget, default = the Android one
	envRounds  = "WUDICT_PERF_ROUNDS"    // query passes over the word list
)

// The limits swept, in MB. 0 is the unlimited baseline every other row is
// judged against; 192 and 384 are the floor and cap of config.memoryLimitDefault.
var sweepMB = []int{0, 96, 128, 192, 256, 384, 512}

// Deliberately ordinary words in several scripts: the point is not recall, it
// is to make every dictionary in the corpus open and answer, since an open
// backend is where preview-mode memory goes.
var perfWords = []string{
	"a", "the", "water", "go", "hand", "light", "run", "time", "house", "make",
	"дом", "вода", "идти", "рука", "время",
	"水", "手", "行", "家", "时间",
	"casa", "agua", "mano", "hacer", "tiempo",
	"haus", "wasser", "hand", "machen", "zeit",
}

func TestMemoryLimitSweep(t *testing.T) {
	corpus := os.Getenv(envCorpus)
	if corpus == "" {
		t.Skipf("set %s to a dictionary directory to run this (see the file comment)", envCorpus)
	}
	if strings.HasPrefix(corpus, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			t.Fatalf("expanding %s: %v", corpus, err)
		}
		corpus = filepath.Join(home, corpus[2:])
	}
	if fi, err := os.Stat(corpus); err != nil || !fi.IsDir() {
		t.Fatalf("%s=%q is not a directory", envCorpus, corpus)
	}

	if os.Getenv(envLimitMB) != "" {
		runOneLimit(t, corpus)
		return
	}
	sweep(t, corpus)
}

// --- parent: run the children, tabulate, judge -------------------------

type result struct {
	LimitMB   int     `json:"limit_mb"`
	Dicts     int     `json:"dicts"`   // rows /api/dicts produced
	Entries   int     `json:"entries"` // dictionaries discovered
	OpenN     int     `json:"open_n"`  // reclaimable handles still open at the end
	PreviewMB float64 `json:"preview_mb"`
	Queries   int     `json:"queries"`
	WallSec   float64 `json:"wall_sec"`
	GCCycles  uint64  `json:"gc_cycles"`
	GCCPUSec  float64 `json:"gc_cpu_sec"`
	AllCPUSec float64 `json:"all_cpu_sec"`
	LiveMB    float64 `json:"live_mb"`  // heap live at the end
	InUseMB   float64 `json:"inuse_mb"` // what the limit is actually measured against
	PeakRSSMB float64 `json:"-"`        // filled in by the parent from rusage
}

func (r result) gcFraction() float64 {
	if r.AllCPUSec <= 0 {
		return 0
	}
	return r.GCCPUSec / r.AllCPUSec
}

func sweep(t *testing.T, corpus string) {
	var rows []result
	for _, mb := range sweepMB {
		r, err := runChild(t, corpus, mb)
		if err != nil {
			t.Fatalf("limit %d MB: %v", mb, err)
		}
		rows = append(rows, r)
		row := fmt.Sprintf("limit %4d MB → %3d dicts, %3d queries, wall %6.1fs  gc %5.1fs (%4.1f%% of cpu)  cycles %5d  live %6.1f MB  in-use %6.1f MB  preview %6.1f MB (%d open)  peak RSS %6.1f MB",
			mb, r.Entries, r.Queries, r.WallSec, r.GCCPUSec, 100*r.gcFraction(), r.GCCycles, r.LiveMB, r.InUseMB, r.PreviewMB, r.OpenN, r.PeakRSSMB)
		t.Log(row)
		// also unbuffered, because a sweep over a real corpus runs for tens of
		// minutes and testing's log is only flushed when the test ends: without
		// this, a run that is interrupted - or one later limit that hangs -
		// throws away every row already measured.
		fmt.Fprintln(os.Stderr, row)
		if r.Entries == 0 {
			t.Fatalf("no dictionaries discovered under %s - the measurement would be meaningless", corpus)
		}
	}

	base := rows[0] // the unlimited run
	t.Logf("baseline (no limit): wall %.1fs, gc %.1f%% of cpu, live %.1f MB, peak RSS %.1f MB",
		base.WallSec, 100*base.gcFraction(), base.LiveMB, base.PeakRSSMB)

	// The verdict, stated as thresholds rather than left to the reader:
	//   - more than a quarter of CPU spent collecting is thrash. (Go's own GC
	//     CPU limiter only steps in at 50%, which is far past useful.)
	//   - more than 1.5× the baseline wall time means the limit is paid for by
	//     the user waiting.
	// A row that fails these is a limit this program must not ship on Android.
	for _, r := range rows[1:] {
		if r.gcFraction() > 0.25 {
			t.Errorf("limit %d MB: GC took %.1f%% of CPU (baseline %.1f%%) - below the live set",
				r.LimitMB, 100*r.gcFraction(), 100*base.gcFraction())
		}
		if base.WallSec > 0 && r.WallSec > 1.5*base.WallSec {
			t.Errorf("limit %d MB: %.1fs vs %.1fs unlimited - the limit is being paid for in latency",
				r.LimitMB, r.WallSec, base.WallSec)
		}
	}
}

func runChild(t *testing.T, corpus string, mb int) (result, error) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run", "^TestMemoryLimitSweep$", "-test.v", "-test.timeout", "30m")
	cmd.Env = append(os.Environ(),
		envCorpus+"="+corpus,
		envLimitMB+"="+strconv.Itoa(mb),
	)
	out, err := cmd.CombinedOutput()
	var r result
	line := resultLine(string(out))
	if line == "" {
		return r, fmt.Errorf("child produced no RESULT (%v)\n%s", err, tail(string(out), 40))
	}
	if jsonErr := json.Unmarshal([]byte(line), &r); jsonErr != nil {
		return r, fmt.Errorf("unparsable RESULT: %v: %s", jsonErr, line)
	}
	if err != nil {
		return r, fmt.Errorf("child failed: %v\n%s", err, tail(string(out), 40))
	}
	if ru, ok := cmd.ProcessState.SysUsage().(*syscall.Rusage); ok {
		r.PeakRSSMB = maxRSSBytes(ru.Maxrss) / (1 << 20)
	}
	return r, nil
}

// maxRSSBytes normalises rusage.Maxrss, which is bytes on Darwin and
// kilobytes on Linux.
func maxRSSBytes(v int64) float64 {
	if runtime.GOOS == "darwin" {
		return float64(v)
	}
	return float64(v) * 1024
}

const resultPrefix = "RESULT "

func resultLine(out string) string {
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimSpace(l)
		if s, ok := strings.CutPrefix(l, resultPrefix); ok {
			return s
		}
	}
	return ""
}

func tail(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// --- child: one limit, one measurement ---------------------------------

func runOneLimit(t *testing.T, corpus string) {
	limit := int64(mustAtoi(t, os.Getenv(envLimitMB))) << 20
	budget := int64(64) << 20 // config.previewMemoryDefault() on Android
	if v := os.Getenv(envBudgetM); v != "" {
		budget = int64(mustAtoi(t, v)) << 20
	}
	rounds := 3
	if v := os.Getenv(envRounds); v != "" {
		rounds = mustAtoi(t, v)
	}

	// Never the user's real library: a prepared dictionary costs almost no
	// memory (docs.local/PERF.md §3), so borrowing an already-prepared library would
	// measure the easy case. An empty db dir keeps every dictionary in preview
	// mode, which is both the worst case and the state a fresh install is in.
	isolatedDBDir(t)

	if limit > 0 {
		SetMemoryLimit(limit)
	}
	reg, err := NewRegistry([]string{corpus}, false)
	if err != nil {
		t.Fatalf("scanning %s: %v", corpus, err)
	}
	reg.SetPreviewBudget(budget)
	s := New(reg) // AutoIndex stays false: no ingest, so this measures lookups only
	dicts := getDicts(t, s, "/api/dicts")

	m0 := readPerf()
	start := time.Now()
	queries := 0
	for i := 0; i < rounds; i++ {
		for _, w := range perfWords {
			searchStream(t, s, "/api/search?q="+w+"&mode=prefix&dict=all&n=20")
			queries++
		}
	}
	wall := time.Since(start).Seconds()
	m1 := readPerf()

	r := result{
		LimitMB:   int(limit >> 20),
		Dicts:     len(dicts),
		Entries:   reg.Count(),
		OpenN:     len(reg.reclaimables()),
		PreviewMB: float64(reg.previewBytes()) / (1 << 20),
		Queries:   queries,
		WallSec:   wall,
		GCCycles:  m1.cycles - m0.cycles,
		GCCPUSec:  m1.gcCPU - m0.gcCPU,
		AllCPUSec: m1.allCPU - m0.allCPU,
		LiveMB:    float64(m1.live) / (1 << 20),
		InUseMB:   float64(m1.inUse) / (1 << 20),
	}
	b, _ := json.Marshal(r)
	fmt.Println(resultPrefix + string(b))
}

func mustAtoi(t *testing.T, s string) int {
	t.Helper()
	n, err := strconv.Atoi(s)
	if err != nil {
		t.Fatalf("bad integer %q: %v", s, err)
	}
	return n
}

type perfSnap struct {
	cycles      uint64
	gcCPU       float64
	allCPU      float64
	live, inUse uint64
}

// readPerf samples runtime/metrics rather than ReadMemStats, which stops the
// world - measuring with an instrument that perturbs exactly the quantity
// being measured.
func readPerf() perfSnap {
	s := []metrics.Sample{
		{Name: "/gc/cycles/total:gc-cycles"},
		{Name: "/cpu/classes/gc/total:cpu-seconds"},
		{Name: "/cpu/classes/total:cpu-seconds"},
		{Name: "/gc/heap/live:bytes"},
		{Name: "/memory/classes/total:bytes"},
		{Name: "/memory/classes/heap/released:bytes"},
	}
	metrics.Read(s)
	var p perfSnap
	if s[0].Value.Kind() == metrics.KindUint64 {
		p.cycles = s[0].Value.Uint64()
	}
	if s[1].Value.Kind() == metrics.KindFloat64 {
		p.gcCPU = s[1].Value.Float64()
	}
	if s[2].Value.Kind() == metrics.KindFloat64 {
		p.allCPU = s[2].Value.Float64()
	}
	if s[3].Value.Kind() == metrics.KindUint64 { // Go 1.21+
		p.live = s[3].Value.Uint64()
	}
	if s[4].Value.Kind() == metrics.KindUint64 && s[5].Value.Kind() == metrics.KindUint64 {
		p.inUse = s[4].Value.Uint64() - s[5].Value.Uint64()
	}
	return p
}
