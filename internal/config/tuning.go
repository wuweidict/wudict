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
	"sync"
)

// The defaults in this file are the ones that differ on Android (D64). They are
// DEFAULTS, not policy: every one of them is an ordinary config key, so the
// layering still holds and anyone who wants desktop numbers on a phone sets
// them in wudict.toml or the environment and gets them.
//
// The reason they differ is not that a phone is slower. It is that a phone
// judges its apps. Sustained CPU is a battery complaint and a thermal event;
// resident memory above what the platform thinks reasonable is a kill by the
// low-memory daemon - and on many vendor builds, a lasting entry in some
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
// 1 GB is a reasonable desktop answer and an absurd one on a phone. On Android
// it is a third of the process ceiling (memoryLimitDefault) - 64 MB on a device
// at the floor, 128 MB on one at the cap - so a bigger phone gets a bigger
// budget without a second constant to keep in step with the first.
//
// A third, because this is the number the app carries BETWEEN searches, and
// that residue is what lmkd scores a backgrounded process on. The search itself
// is bounded by SEARCH_MEMORY (= the ceiling), not by this: during a fan-out
// the janitor protects recently-used backends, so the preview budget is what
// memory settles back to, not what the peak is held to. Leaving two thirds of
// the ceiling free for one query to work in is the trade; spending it all on
// residue would buy a faster second keystroke and a kill while backgrounded.
//
// 64 MB holds roughly 180k headwords of direct backends (docs.local/PERF.md
// §3.1: ~350 B each) - several open dictionaries - and everything past that is
// evicted and reopened on demand, which costs a fraction of a second and no
// battery to speak of.
func previewMemoryDefault() int64 {
	if runtime.GOOS == "android" {
		return memoryLimitDefault() / 3
	}
	return 1 << 30
}

// morphCacheDefault is how many lemmatizer packs may stay resident (O3).
//
// The packs are not small and they are not uniform: measured after load, en is
// 7.0 MB, fr 27.8, de 35.3, it 34.7, es 60.0, ru 65.0 - 230 MB for all six.
// They are also only ever consulted on a search that found NOTHING, so a cache
// miss costs a reload (25-157 ms) on a query that was already going to be slow
// and empty, while a resident pack costs its megabytes on every query that was
// not.
//
// 2 on a desktop: a bilingual user's two languages stay hot, and the worst case
// (es + ru) is 125 MB against a 1 GB preview budget. 1 on Android, where the
// whole preview budget is 64-128 MB and a single Russian pack is 65 MB of it -
// the pack is loaded, used and dropped as soon as another language needs the
// slot. Raising it there would spend the ceiling on data consulted only by
// searches that FAILED, at the expense of the dictionaries that answer.
// MORPH_CACHE=0 turns lemmatization off entirely and loads nothing, ever.
func morphCacheDefault() int {
	if runtime.GOOS == "android" {
		return 1
	}
	return 2
}

// searchMemoryDefault caps what a SINGLE search may bring into memory.
//
// Unset on a desktop. The failure it prevents is real there too - a `dict=all`
// query over an unprepared library held 6.3 GB against a 64 MB preview budget
// (docs.local/PERF.md §8.2) - but the cost of preventing it is dictionaries reported
// as not searched, and on a machine with the RAM to spare that is a worse deal
// than the memory. The key exists; the default declines to take it.
//
// On Android it is the memory ceiling. Not a fraction of it and not a multiple:
// the ceiling is the number above which this process is in trouble, so letting
// one query materialise more than that guarantees the relax valve fires, which
// is a mechanism for surviving an emergency rather than a way to run. Sized in
// the weight model's own currency, which over-charges by ~1.7× (§8.3), so the
// real peak this admits is well under the ceiling - deliberately.
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
// ceiling on the GO heap only - the WebView's own memory, typically the larger
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
// Read once: three defaults derive from it and the answer cannot change while
// the process runs.
var memTotal = sync.OnceValue(readMemTotal)

func readMemTotal() int64 {
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
