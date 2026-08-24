// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Package speex decodes Ogg-Speex (.spx) audio to 16-bit WAV (mono, or
// intensity stereo expanded to two channels) entirely
// in-process. The Ogg demuxing and Speex header parsing are pure Go (see
// ogg.go); the codec itself is a vendored decode-only subset of libspeex
// compiled via cgo (see ./clib). When built with CGO_ENABLED=0 the decoder is
// unavailable (Available == false) and callers fall back to the external
// `speexdec` binary.
package speex

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// Available reports whether the in-process (cgo) decoder is compiled in. It is
// set by the active backend (backend_cgo.go / backend_nocgo.go).
var Available bool

// maxSkip bounds the per-page padding correction. One page holds a handful of
// frames; anything past this is a broken or hostile granule pair, and the
// stream is better decoded whole than chopped on the strength of it.
const maxSkip = 1 << 20

// ErrNoBackend is returned by DecodeToWAV when the in-process decoder is not
// compiled in (CGO_ENABLED=0).
var ErrNoBackend = errors.New("speex: in-process decoder not built (CGO_ENABLED=0)")

// frameDecoder is the codec backend implemented by ./clib under cgo.
// DecodePacket returns interleaved samples when the decoder is stereo.
type frameDecoder interface {
	FrameSize() int
	Lookahead() int
	DecodePacket(packet []byte, frames int) ([]int16, error)
	Close()
}

// newDecoder constructs a decoder for a Speex mode and channel count (1 or 2);
// set by the active backend.
var newDecoder func(mode, channels int) (frameDecoder, error)

type header struct {
	rate            int
	mode            int
	channels        int
	frameSize       int
	framesPerPacket int
	extraHeaders    int
}

// parseHeader decodes the 80-byte Speex header packet (all int32 little-endian).
func parseHeader(p []byte) (*header, error) {
	if len(p) < 80 || string(p[0:8]) != "Speex   " {
		return nil, fmt.Errorf("speex: not a Speex header packet")
	}
	le := binary.LittleEndian
	h := &header{
		rate:            int(int32(le.Uint32(p[36:40]))),
		mode:            int(le.Uint32(p[40:44])),
		channels:        int(le.Uint32(p[48:52])),
		frameSize:       int(le.Uint32(p[56:60])),
		framesPerPacket: int(le.Uint32(p[64:68])),
		extraHeaders:    int(le.Uint32(p[68:72])),
	}
	if h.framesPerPacket <= 0 {
		h.framesPerPacket = 1
	}
	if h.mode < 0 || h.mode > 2 {
		return nil, fmt.Errorf("speex: unsupported mode %d", h.mode)
	}
	return h, nil
}

// outChannels is the output channel count: Speex encodes 1 or 2, and a header
// claiming anything else is not to be believed. speexdec maps everything that
// is not exactly 1 to stereo; we map only exactly 2, because a header reading
// 0 (seen in the wild) describes a stream with no in-band stereo packets at
// all, and decoding that as stereo would double its size to say nothing.
func (h *header) outChannels() int {
	if h.channels == 2 {
		return 2
	}
	return 1
}

// DecodeToWAV reads an Ogg-Speex stream and returns a 16-bit WAV, mono or
// stereo as the stream declares. The WAV sample rate is the stream's declared
// rate (matching the speexdec CLI).
func DecodeToWAV(r io.Reader) ([]byte, error) {
	if !Available {
		return nil, ErrNoBackend
	}
	og := newOggReader(r)

	hp, err := og.nextPacket()
	if err != nil {
		return nil, fmt.Errorf("speex: reading header: %w", err)
	}
	h, err := parseHeader(hp)
	if err != nil {
		return nil, err
	}
	ch := h.outChannels()

	dec, err := newDecoder(h.mode, ch)
	if err != nil {
		return nil, err
	}
	defer dec.Close()

	// Skip the mandatory Vorbis-comment packet plus any extra header packets.
	for i := 0; i < 1+h.extraHeaders; i++ {
		if _, err := og.nextPacket(); err != nil {
			return nil, fmt.Errorf("speex: reading extra headers: %w", err)
		}
	}

	// Padding. A Speex stream is a whole number of fixed-size frames, but the
	// audio inside it is not: the encoder pads both ends, and the granule
	// positions say by how much. Getting this wrong does not sound like an
	// error — it sounds like a word whose last consonant is clipped — so the
	// arithmetic below is speexdec's (speexdec.c:636 and :734-750), not an
	// approximation of it:
	//
	//   skip = packets_on_page*frames_per_packet*frame_size - granule_delta
	//
	// is what the page claims to hold minus what it says is really there.
	// Positive on the first page (leading padding, dropped together with the
	// codec's own lookahead delay), negated on the EOS page and then negative
	// (trailing padding, chopped off the last packet).
	frameSize := dec.FrameSize()
	lookahead := dec.Lookahead()
	nframes := h.framesPerPacket

	var pcm []int16
	var page *pageInfo
	skip := 0
	for {
		pkt, err := og.nextPacket()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if og.page != page {
			page = og.page
			skip = 0
			// int64 until the result is known small: the granule pair is
			// whatever the file says, and a hostile delta must not be able to
			// wrap this into a plausible-looking chop.
			if page.granule > 0 {
				claim := int64(page.packets) * int64(nframes) * int64(frameSize)
				if d := claim - (page.granule - page.prev); d > -maxSkip && d < maxSkip {
					skip = int(d)
					if page.eos {
						skip = -skip
					}
				}
			}
		}
		samples, err := dec.DecodePacket(pkt, nframes)
		if err != nil {
			break // tolerate a corrupt trailing frame — keep what decoded
		}
		switch {
		case og.index == 1 && skip > 0:
			// Leading padding, and only ever out of this packet's FIRST frame:
			// speexdec drops at most one frame here, however large the claim.
			drop := skip + lookahead
			if drop > frameSize {
				drop = frameSize
			}
			drop *= ch
			if drop >= len(samples) {
				samples = nil
			} else {
				samples = samples[drop:]
			}
		case og.index == page.packets && skip < 0:
			// Trailing padding: everything past the packet's real length goes,
			// and frames are kept from its start, so one cut does it.
			keep := (nframes*frameSize + skip + lookahead) * ch
			if keep < 0 {
				keep = 0
			}
			if keep < len(samples) {
				samples = samples[:keep]
			}
		}
		pcm = append(pcm, samples...)
	}
	if len(pcm) == 0 {
		return nil, fmt.Errorf("speex: no audio decoded")
	}
	return buildWAV(pcm, h.rate, ch), nil
}

// buildWAV wraps 16-bit PCM samples (interleaved, if ch == 2) in a canonical
// 44-byte WAV header.
func buildWAV(pcm []int16, rate, ch int) []byte {
	dataLen := len(pcm) * 2
	buf := make([]byte, 44+dataLen)
	le := binary.LittleEndian
	copy(buf[0:4], "RIFF")
	le.PutUint32(buf[4:8], uint32(36+dataLen))
	copy(buf[8:12], "WAVE")
	copy(buf[12:16], "fmt ")
	le.PutUint32(buf[16:20], 16)         // PCM fmt chunk size
	le.PutUint16(buf[20:22], 1)          // audio format: PCM
	le.PutUint16(buf[22:24], uint16(ch)) // channels
	le.PutUint32(buf[24:28], uint32(rate))
	le.PutUint32(buf[28:32], uint32(rate*ch*2)) // byte rate = rate * ch * 2 bytes
	le.PutUint16(buf[32:34], uint16(ch*2))      // block align
	le.PutUint16(buf[34:36], 16)                // bits per sample
	copy(buf[36:40], "data")
	le.PutUint32(buf[40:44], uint32(dataLen))
	for i, s := range pcm {
		le.PutUint16(buf[44+i*2:], uint16(s))
	}
	return buf
}
