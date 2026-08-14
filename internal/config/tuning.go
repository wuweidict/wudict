// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"bufio"
	"os"
	"runtime"
	"strconv"
	"strings"
)

// The defaults in this file are the ones that differ on Android (D64). They are
// DEFAULTS, not policy: every one of them is an ordinary config key, so the
// layering still holds and anyone who wants desktop numbers on a phone sets
// them in wudict.toml or the environment and gets them.
//
// The reason they differ is not that a phone is slower. It is that a phone
// judges its apps. Sustained CPU is a battery complaint and a thermal event;
// resident memory above what the platform thinks reasonable is a kill by the
// low-memory daemon — and on many vendor builds, a lasting entry in some
// battery-abuse list that no amount of later good behaviour undoes. A desktop
// rewards a program for using the machine it is running on. Android punishes
// exactly the same behaviour.

// MaxProcs is the parallelism ceiling for this platform, or 0 for "leave the
// runtime's own default alone".
//
// Half the cores, at most four. Phone core counts describe a big.LITTLE
// arrangement, not four-to-eight equally useful CPUs: saturating all of them
// pulls in the big cores, which is where the heat and the battery go. Half
// keeps the work on what is typically the efficiency cluster while leaving the
// device responsive, and the app has almost no CPU-bound work outside indexing
// anyway.
func MaxProcs() int {
	if runtime.GOOS != "android" {
		return 0
	}
	n := runtime.NumCPU() / 2
	if n < 2 {
		n = 2
	}
	if n > 4 {
		n = 4
	}
	return n
}

// previewMemoryDefault caps what unprepared dictionaries may hold open.
//
// 1 GB is a reasonable desktop answer and an absurd one on a phone, where it
// exceeds what the whole app may resident before the platform intervenes. 64 MB
// holds roughly 180k headwords of direct backends (docs/PERF.md §3.1: ~350 B
// each) — several open dictionaries — and everything past that is evicted and
// reopened on demand, which costs a fraction of a second and no battery to
// speak of.
func previewMemoryDefault() int64 {
	if runtime.GOOS == "android" {
		return 64 << 20
	}
	return 1 << 30
}

// searchMemoryDefault caps what a SINGLE search may bring into memory.
//
// Unset on a desktop. The failure it prevents is real there too — a `dict=all`
// query over an unprepared library held 6.3 GB against a 64 MB preview budget
// (docs/PERF.md §8.2) — but the cost of preventing it is dictionaries reported
// as not searched, and on a machine with the RAM to spare that is a worse deal
// than the memory. The key exists; the default declines to take it.
//
// On Android it is the memory ceiling. Not a fraction of it and not a multiple:
// the ceiling is the number above which this process is in trouble, so letting
// one query materialise more than that guarantees the relax valve fires, which
// is a mechanism for surviving an emergency rather than a way to run. Sized in
// the weight model's own currency, which over-charges by ~1.7× (§8.3), so the
// real peak this admits is well under the ceiling — deliberately.
func searchMemoryDefault() int64 {
	if runtime.GOOS != "android" {
		return 0
	}
	return memoryLimitDefault()
}

// memoryLimitDefault is the soft heap ceiling.
//
// Unset on a desktop: the machine's own memory manager is better at this than
// a number we would guess, and a limit that is wrong makes the collector run
// continuously (see internal/server.memoryPressure for why that is the failure
// mode that matters).
//
// On Android it is set, because there the failure mode of NOT having one is
// worse: the app is killed. The value is a fraction of physical memory, floored
// so it cannot be smaller than a working set this program legitimately needs,
// and capped so it never approaches what the platform considers a hog. It is a
// ceiling on the GO heap only — the WebView's own memory, typically the larger
// half of the app, is not ours to bound.
func memoryLimitDefault() int64 {
	if runtime.GOOS != "android" {
		return 0
	}
	const (
		floor = 192 << 20
		cap_  = 384 << 20
	)
	n := memTotal() / 16
	if n < floor {
		return floor
	}
	if n > cap_ {
		return cap_
	}
	return n
}

// memTotal reads physical RAM from /proc/meminfo, 0 when it cannot be read.
// Linux-only by construction, which is exactly the platform that asks for it.
func memTotal() int64 {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "MemTotal:") {
			continue
		}
		// "MemTotal:       3908352 kB"
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return 0
		}
		kb, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			return 0
		}
		return kb << 10
	}
	return 0
}
