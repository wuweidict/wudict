// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package speex

import (
	"bytes"
	"encoding/binary"
	"io"
	"testing"
)

// buildPage assembles one raw Ogg page (CRC left zero - the reader doesn't
// verify it).
func buildPage(htype byte, serial, seq uint32, granule int64, laces, body []byte) []byte {
	p := []byte("OggS")
	p = append(p, 0, htype)
	var b8 [8]byte
	binary.LittleEndian.PutUint64(b8[:], uint64(granule))
	p = append(p, b8[:]...)
	var b4 [4]byte
	binary.LittleEndian.PutUint32(b4[:], serial)
	p = append(p, b4[:]...)
	binary.LittleEndian.PutUint32(b4[:], seq)
	p = append(p, b4[:]...)
	p = append(p, 0, 0, 0, 0) // CRC
	p = append(p, byte(len(laces)))
	p = append(p, laces...)
	p = append(p, body...)
	return p
}

func readAllPackets(t *testing.T, data []byte) [][]byte {
	t.Helper()
	og := newOggReader(bytes.NewReader(data))
	var out [][]byte
	for {
		pkt, err := og.nextPacket()
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, pkt)
	}
}

// TestOggMultiPacketPage: a single page carrying two packets (split by the
// lacing table) must yield both.
func TestOggMultiPacketPage(t *testing.T) {
	page := buildPage(0x02|0x04, 7, 0, 0, []byte{2, 3}, []byte("ABCDE"))
	pkts := readAllPackets(t, page)
	if len(pkts) != 2 || string(pkts[0]) != "AB" || string(pkts[1]) != "CDE" {
		t.Fatalf("multi-packet page: %q", pkts)
	}
}

// TestOggPacketSpanningPages: a 300-byte packet whose lacing ends on 255 must
// be reassembled from the continuation page.
func TestOggPacketSpanningPages(t *testing.T) {
	big := bytes.Repeat([]byte{0xAB}, 300)
	pageA := buildPage(0x02, 9, 0, 0, []byte{255}, big[:255]) // BOS, continues
	pageB := buildPage(0x01|0x04, 9, 1, 300, []byte{45}, big[255:])
	pkts := readAllPackets(t, append(pageA, pageB...))
	if len(pkts) != 1 || !bytes.Equal(pkts[0], big) {
		t.Fatalf("cross-page packet: got %d packets, len %d", len(pkts), len(pkts[0]))
	}
}

// TestParseHeader decodes a synthetic 80-byte Speex header.
func TestParseHeader(t *testing.T) {
	p := make([]byte, 80)
	copy(p, "Speex   ")
	le := binary.LittleEndian
	le.PutUint32(p[36:], 11025) // rate
	le.PutUint32(p[40:], 0)     // mode (narrowband)
	le.PutUint32(p[48:], 1)     // channels
	le.PutUint32(p[56:], 160)   // frame_size
	le.PutUint32(p[64:], 1)     // frames_per_packet
	le.PutUint32(p[68:], 0)     // extra_headers

	h, err := parseHeader(p)
	if err != nil {
		t.Fatal(err)
	}
	if h.rate != 11025 || h.mode != 0 || h.channels != 1 || h.frameSize != 160 || h.framesPerPacket != 1 {
		t.Fatalf("header: %+v", h)
	}
	// bad magic rejected
	bad := make([]byte, 80)
	if _, err := parseHeader(bad); err == nil {
		t.Error("expected error on non-Speex header")
	}
}
