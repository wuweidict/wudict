// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Package speex decodes Ogg-Speex (.spx) audio to 16-bit mono WAV entirely
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

// ErrNoBackend is returned by DecodeToWAV when the in-process decoder is not
// compiled in (CGO_ENABLED=0).
var ErrNoBackend = errors.New("speex: in-process decoder not built (CGO_ENABLED=0)")

// frameDecoder is the codec backend implemented by ./clib under cgo.
type frameDecoder interface {
	FrameSize() int
	DecodePacket(packet []byte, frames int) ([]int16, error)
	Close()
}

// newDecoder constructs a decoder for a Speex mode; set by the active backend.
var newDecoder func(mode int) (frameDecoder, error)

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

// DecodeToWAV reads an Ogg-Speex stream and returns a 16-bit mono WAV. The WAV
// sample rate is the stream's declared rate (matching the speexdec CLI).
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
	if h.channels != 1 {
		return nil, fmt.Errorf("speex: only mono is supported (channels=%d)", h.channels)
	}

	dec, err := newDecoder(h.mode)
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

	var pcm []int16
	for {
		pkt, err := og.nextPacket()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		samples, err := dec.DecodePacket(pkt, h.framesPerPacket)
		if err != nil {
			break // tolerate a corrupt trailing frame — keep what decoded
		}
		pcm = append(pcm, samples...)
	}
	if len(pcm) == 0 {
		return nil, fmt.Errorf("speex: no audio decoded")
	}
	// Trim the decoder's trailing padding to the exact sample count from the
	// last page's granule position (matches the speexdec CLI output length).
	if g := og.granule; g > 0 && int(g) < len(pcm) {
		pcm = pcm[:g]
	}
	return buildWAV(pcm, h.rate), nil
}

// buildWAV wraps 16-bit mono PCM samples in a canonical 44-byte WAV header.
func buildWAV(pcm []int16, rate int) []byte {
	dataLen := len(pcm) * 2
	buf := make([]byte, 44+dataLen)
	le := binary.LittleEndian
	copy(buf[0:4], "RIFF")
	le.PutUint32(buf[4:8], uint32(36+dataLen))
	copy(buf[8:12], "WAVE")
	copy(buf[12:16], "fmt ")
	le.PutUint32(buf[16:20], 16) // PCM fmt chunk size
	le.PutUint16(buf[20:22], 1)  // audio format: PCM
	le.PutUint16(buf[22:24], 1)  // channels: mono
	le.PutUint32(buf[24:28], uint32(rate))
	le.PutUint32(buf[28:32], uint32(rate*2)) // byte rate = rate * 1ch * 2 bytes
	le.PutUint16(buf[32:34], 2)              // block align
	le.PutUint16(buf[34:36], 16)             // bits per sample
	copy(buf[36:40], "data")
	le.PutUint32(buf[40:44], uint32(dataLen))
	for i, s := range pcm {
		le.PutUint16(buf[44+i*2:], uint16(s))
	}
	return buf
}
