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

// oggReader is a minimal in-house Ogg (RFC 3533) demuxer: it yields the
// reassembled packets of the first logical bitstream (a .spx has one). Zero
// external dependencies. It reassembles packets across segments and pages via
// the lacing table; the page CRC is not verified (the codec rejects garbage).
type oggReader struct {
	br         *bufio.Reader
	serial     uint32
	haveSerial bool
	partial    []byte
	queue      [][]byte
	eos        bool
	granule    int64 // last valid granule position = total decoded sample count
}

func newOggReader(r io.Reader) *oggReader {
	return &oggReader{br: bufio.NewReaderSize(r, 1<<15)}
}

// nextPacket returns the next reassembled packet, or io.EOF at end of stream.
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
	return p, nil
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
	// The granule position of the last page is the total decoded sample count;
	// -1 marks a page on which no packet completes. Track the last valid value
	// so the caller can trim the decoder's trailing padding (speexdec parity).
	if g := int64(binary.LittleEndian.Uint64(head[6:14])); g != -1 {
		o.granule = g
	}
	if headerType&0x04 != 0 { // EOS
		o.eos = true
	}

	// Split the body by the lacing table: a segment < 255 ends a packet; a
	// run of 255s continues it (across pages, marked "continued").
	pos := 0
	for _, lace := range segTable {
		o.partial = append(o.partial, body[pos:pos+int(lace)]...)
		pos += int(lace)
		if lace < 255 {
			pkt := o.partial
			o.partial = nil
			o.queue = append(o.queue, pkt)
		}
	}
	return nil
}
