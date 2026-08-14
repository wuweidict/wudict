// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"net/http"
	"runtime"
	"runtime/debug"
	"runtime/metrics"
	"strings"
	"sync/atomic"

	"github.com/wuweidict/wudict/internal/logx"
)

// Power is how much this process may spend right now. It exists because a
// desktop server and a phone are the same program in two utterly different
// situations: on a desktop "idle" costs nothing anyone notices, while on a
// phone a background process that keeps a core awake is a battery complaint,
// a thermal event, and eventually a kill by the platform's low-memory daemon.
//
// The Go side does not detect any of this — it cannot: the signals are
// Android's (onStop, onTrimMemory, thermal status, power-save mode) and live
// in the JVM the server was exec'd away from (D52). The shell reports them
// through POST /api/power and the server obeys. Everything here is therefore
// platform-neutral: a desktop simply never leaves PowerActive.
type Power int32

const (
	// PowerActive: someone is looking at the screen. Normal behaviour.
	PowerActive Power = iota
	// PowerBackground: the app is not visible. Nothing new is indexed and
	// every reopenable handle is dropped — but prepared databases stay open,
	// because they cost ~nothing and returning must be instant.
	PowerBackground
	// PowerRestricted: the platform is unhappy — memory pressure, a thermal
	// warning, or battery saver. Everything closes, including prepared
	// databases; the process keeps only its listener.
	PowerRestricted
)

func (p Power) String() string {
	switch p {
	case PowerBackground:
		return "background"
	case PowerRestricted:
		return "restricted"
	default:
		return "active"
	}
}

// ParsePower reads a state name from the wire.
func ParsePower(s string) (Power, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "active", "foreground":
		return PowerActive, true
	case "background":
		return PowerBackground, true
	case "restricted", "critical":
		return PowerRestricted, true
	}
	return PowerActive, false
}

var powerState atomic.Int32

// warmEnabled decides whether a registry pre-opens prepared dictionaries at
// startup. False on Android for the reasons given on Warm; a var so a test can
// state which world it is testing.
var warmEnabled = runtime.GOOS != "android"

// CurrentPower reports the state the shell last set (PowerActive everywhere
// that never sets one).
func CurrentPower() Power { return Power(powerState.Load()) }

// activeProcs is the GOMAXPROCS to restore when active. Zero means "this
// platform's scheduler is not ours to manage" — the desktop case, where the
// runtime's own default (every core) is right and quietly halving it would be
// a performance regression nobody asked for.
var activeProcs atomic.Int32

// SetActiveProcs puts the process under power-managed parallelism: n cores
// while active, one while not. Called at startup on the platforms that want it.
func SetActiveProcs(n int) {
	if n < 1 {
		return
	}
	activeProcs.Store(int32(n))
	runtime.GOMAXPROCS(n)
}

func applyProcs(p Power) {
	n := int(activeProcs.Load())
	if n < 1 {
		return
	}
	if p != PowerActive {
		n = 1 // a background process must never look like a busy one
	}
	runtime.GOMAXPROCS(n)
}

// memLimit mirrors the soft heap ceiling passed to the runtime, because the
// janitor needs the number: a limit alone turns memory pressure into CONTINUOUS
// GC (the runtime's only lever is to collect harder), which on a phone is the
// worst possible answer — maximum CPU, maximum heat, for a live set that will
// not shrink no matter how often it is traced. Knowing the limit lets us shed
// caches as the limit is approached, so the collector is handed garbage to
// reclaim instead of being asked to trace a working set that is genuinely too
// big. The limit is then a floor under the shedding, never a substitute for it.
var memLimit atomic.Int64

// SetMemoryLimit installs the soft heap ceiling (config MEMORY_LIMIT) and arms
// the pressure-driven eviction that makes it safe.
func SetMemoryLimit(n int64) {
	if n <= 0 {
		return
	}
	memLimit.Store(n)
	debug.SetMemoryLimit(n)
}

// pressureRatio is how close to the ceiling counts as pressure. Below it the
// budget rules alone; above it, caches go. 0.85 leaves the collector room to
// work with what shedding just freed rather than starting from a full heap.
const pressureRatio = 0.85

// memoryPressure reports that the heap has grown close enough to the limit for
// the GC to be about to start working continuously.
//
// runtime/metrics rather than ReadMemStats: the latter stops the world, and a
// periodic pause on a battery-powered device to ask about memory would be its
// own defect. The quantity is what SetMemoryLimit itself governs — everything
// the runtime has mapped, less what it has already handed back to the OS.
func memoryPressure() bool {
	lim := memLimit.Load()
	if lim <= 0 {
		return false
	}
	s := []metrics.Sample{
		{Name: "/memory/classes/total:bytes"},
		{Name: "/memory/classes/heap/released:bytes"},
	}
	metrics.Read(s)
	if s[0].Value.Kind() != metrics.KindUint64 || s[1].Value.Kind() != metrics.KindUint64 {
		return false
	}
	inUse := int64(s[0].Value.Uint64()) - int64(s[1].Value.Uint64())
	return float64(inUse) > pressureRatio*float64(lim)
}

// SetPower moves the process between power states, applying the consequences
// immediately: the parallelism, the open handles, and (via CurrentPower) the
// background indexer's willingness to start anything.
func (r *Registry) SetPower(p Power) {
	prev := Power(powerState.Swap(int32(p)))
	if prev == p {
		return
	}
	logx.V("power: %s → %s", prev, p)
	applyProcs(p)
	if p == PowerActive {
		r.nudge() // the janitor may have work again once things reopen
		return
	}
	// Going quiet is the one moment when eviction needs no idle grace: the
	// user has just left, so "recently used" says nothing about what is about
	// to be used. Prepared databases survive PowerBackground because reopening
	// one costs milliseconds and holding it costs a page cache we bound
	// elsewhere; under PowerRestricted even that is too much.
	if freed := r.shed(p == PowerRestricted); freed > 0 {
		logx.V("power: released ~%d MB entering %s", freed>>20, p)
	}
}

// shed drops every handle that can be rebuilt on demand. everything also closes
// prepared databases, which hold no headword map — only file descriptors and a
// SQLite page cache — and so are worth closing solely under real pressure.
func (r *Registry) shed(everything bool) int64 {
	var freed int64
	for _, c := range r.reclaimables() {
		freed += c.free()
	}
	if everything {
		for _, e := range r.all() {
			freed += e.drop(true)
		}
	}
	// no FreeOSMemory here: every close above schedules its own, coalesced
	return freed
}

// handlePower is how the Android shell tells the server what the platform just
// told it (D54: the shell absorbs the platform, the page never learns of it).
//
// Loopback-only, like every other control that acts on the machine rather than
// on the library: on a desktop this server may be answering a browser on
// another host, and a remote page must not be able to put someone else's
// process to sleep. POST because it changes state — a GET here would be
// followed by every link prefetcher on the network.
func (s *Server) handlePower(w http.ResponseWriter, r *http.Request) {
	if !isLoopback(r) {
		httpErr(w, 403, "only available from the machine running wudict")
		return
	}
	p, ok := ParsePower(r.URL.Query().Get("state"))
	if !ok {
		httpErr(w, 400, "state must be active, background or restricted")
		return
	}
	s.reg.SetPower(p)
	writeJSON(w, map[string]any{"state": p.String()})
}
