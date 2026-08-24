// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package speex

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
)

// pageInfo is the per-page bookkeeping the Speex decoder needs to chop the
// encoder's padding off the stream: a page's granule position, the previous
// page's, and how many packets complete on it. See trimming in speex.go.
type pageInfo struct {
	granule int64 // this page's granule position (samples per channel, or -1)
	prev    int64 // the previous page's, as speexdec's last_granule
	packets int   // packets that COMPLETE on this page (headers included)
	eos     bool
}

// queued is one reassembled packet together with where it sat in its page.
type queued struct {
	data []byte
	page *pageInfo
	idx  int // 1-based position among the packets completing on that page
}

// oggReader is a minimal in-house Ogg (RFC 3533) demuxer: it yields the
// reassembled packets of the first logical bitstream (a .spx has one). Zero
// external dependencies. It reassembles packets across segments and pages via
// the lacing table; the page CRC is not verified (the codec rejects garbage).
type oggReader struct {
	br          *bufio.Reader
	serial      uint32
	haveSerial  bool
	partial     []byte
	queue       []queued
	eos         bool
	lastGranule int64 // granule of the most recently read page

	// page and index describe the packet nextPacket returned last.
	page  *pageInfo
	index int
}

func newOggReader(r io.Reader) *oggReader {
	return &oggReader{br: bufio.NewReaderSize(r, 1<<15)}
}

// nextPacket returns the next reassembled packet, or io.EOF at end of stream.
// o.page and o.index then describe the page that packet completed on.
func (o *oggReader) nextPacket() ([]byte, error) {
	for len(o.queue) == 0 {
		if o.eos {
			return nil, io.EOF
		}
		if err := o.readPage(); err != nil {
			return nil, err
		}
	}
	p := o.queue[0]
	o.queue = o.queue[1:]
	o.page, o.index = p.page, p.idx
	return p.data, nil
}

// readPage reads one Ogg page and appends its completed packets to the queue.
func (o *oggReader) readPage() error {
	head := make([]byte, 27)
	if _, err := io.ReadFull(o.br, head); err != nil {
		if err == io.ErrUnexpectedEOF {
			return io.EOF
		}
		return err
	}
	if string(head[0:4]) != "OggS" || head[4] != 0 {
		return fmt.Errorf("ogg: bad page capture")
	}
	headerType := head[5]
	serial := binary.LittleEndian.Uint32(head[14:18])
	nSegs := int(head[26])

	segTable := make([]byte, nSegs)
	if _, err := io.ReadFull(o.br, segTable); err != nil {
		return err
	}
	bodyLen := 0
	for _, l := range segTable {
		bodyLen += int(l)
	}
	body := make([]byte, bodyLen)
	if _, err := io.ReadFull(o.br, body); err != nil {
		return err
	}

	if !o.haveSerial {
		o.serial = serial
		o.haveSerial = true
	}
	if serial != o.serial {
		return nil // a different logical stream (rare .spx multiplex): skip
	}
	// A page's granule position is the number of samples (per channel)
	// decodable through the end of that page; -1 marks a page on which no
	// packet completes. speexdec carries the previous value forward
	// unconditionally, including the -1, so this does too.
	pg := int64(binary.LittleEndian.Uint64(head[6:14]))
	pi := &pageInfo{granule: pg, prev: o.lastGranule, eos: headerType&0x04 != 0}
	o.lastGranule = pg
	if pi.eos {
		o.eos = true
	}
	// A lacing value below 255 terminates a packet, so counting them counts the
	// packets that complete here - libogg's ogg_page_packets, which speexdec's
	// padding arithmetic is written against.
	for _, lace := range segTable {
		if lace < 255 {
			pi.packets++
		}
	}

	// Split the body by the lacing table: a segment < 255 ends a packet; a
	// run of 255s continues it (across pages, marked "continued").
	pos, idx := 0, 0
	for _, lace := range segTable {
		o.partial = append(o.partial, body[pos:pos+int(lace)]...)
		pos += int(lace)
		if lace < 255 {
			pkt := o.partial
			o.partial = nil
			idx++
			o.queue = append(o.queue, queued{data: pkt, page: pi, idx: idx})
		}
	}
	return nil
}
