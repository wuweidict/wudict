// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package slob

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// craft writes the smallest header a slob parser accepts, with storeOffset
// under the caller's control. file_size is set to the file's true length so
// the header's own size check - which covers size and nothing else - passes.
func craft(t *testing.T, storeOffset uint64) string {
	t.Helper()
	var b []byte
	b = append(b, magic...)
	b = append(b, make([]byte, 16)...) // uuid
	b = append(b, 5)                   // tinyText len
	b = append(b, "utf-8"...)
	b = append(b, 0)                        // compression: ""
	b = append(b, 0)                        // tag count
	b = append(b, 0)                        // content-type count
	b = binary.BigEndian.AppendUint32(b, 0) // blob count
	b = binary.BigEndian.AppendUint64(b, storeOffset)
	b = binary.BigEndian.AppendUint64(b, uint64(len(b)+8)) // file_size == truth
	path := filepath.Join(t.TempDir(), "crafted.slob")
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestCraftedStoreOffsetAllocatesNothing is the regression for the class of
// bug where a file-declared offset becomes an allocation size. 1<<31 asked for
// 2 GiB before the span checks; 1<<62 exceeded makeslice and panicked. Neither
// is a survivable failure - the first is a runtime OOM abort, which no recover
// converts.
func TestCraftedStoreOffsetAllocatesNothing(t *testing.T) {
	for _, off := range []uint64{1 << 31, 1 << 40, 1 << 62, ^uint64(0)} {
		path := craft(t, off)

		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		_, err := Open(path)
		runtime.ReadMemStats(&after)

		if err == nil {
			t.Fatalf("storeOffset %#x: opened a 61-byte file as valid", off)
		}
		const cap = 32 << 20
		if grew := after.TotalAlloc - before.TotalAlloc; grew > cap {
			t.Errorf("storeOffset %#x: allocated %d bytes rejecting a 61-byte file (cap %d)",
				off, grew, cap)
		}
	}
}

// TestCraftedStoreOffsetInsideFile covers the other direction: an offset that
// IS inside the file must still be rejected on its contents, not accepted.
func TestCraftedStoreOffsetInsideFile(t *testing.T) {
	if _, err := Open(craft(t, 61)); err == nil {
		t.Fatal("truncated store dir accepted")
	}
}
