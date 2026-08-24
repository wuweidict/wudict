// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package speex

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// fakeDecoder stands in for libspeex so the padding arithmetic and the WAV
// framing can be tested without the codec - and therefore under purego too.
// Every sample it returns is its own index in the stream, so a test can say
// exactly which samples survived the chop.
type fakeDecoder struct {
	frameSize int
	lookahead int
	channels  int
	n         int16
}

func (f *fakeDecoder) FrameSize() int { return f.frameSize }
func (f *fakeDecoder) Lookahead() int { return f.lookahead }
func (f *fakeDecoder) Close()         {}

func (f *fakeDecoder) DecodePacket(_ []byte, frames int) ([]int16, error) {
	out := make([]int16, frames*f.frameSize*f.channels)
	for i := range out {
		out[i] = f.n
		f.n++
	}
	return out, nil
}

// spxHeader builds the 80-byte Speex header packet.
func spxHeader(rate, mode, channels, frameSize, framesPerPacket int) []byte {
	p := make([]byte, 80)
	copy(p, "Speex   ")
	le := binary.LittleEndian
	le.PutUint32(p[36:40], uint32(rate))
	le.PutUint32(p[40:44], uint32(mode))
	le.PutUint32(p[48:52], uint32(channels))
	le.PutUint32(p[56:60], uint32(frameSize))
	le.PutUint32(p[64:68], uint32(framesPerPacket))
	return p
}

// spxStream lays the header, the comment packet and two audio pages out as a
// real encoder would: one packet per page for the headers, then a page of two
// audio packets and an EOS page of one.
//
// The granule positions are the point. With frameSize 320 and one frame per
// packet the three audio packets decode to 960 samples, but the pages claim
// only g3 of them: 200 (skip 100 + lookahead 100) are leading padding and 50
// are trailing. Output length is always the final granule - which is what the
// numbers below are chosen to demonstrate.
func spxStream(channels int) []byte {
	const serial = 0x5150
	var b []byte
	b = append(b, buildPage(0x02, serial, 0, 0, []byte{80}, spxHeader(16000, 1, channels, 320, 1))...)
	b = append(b, buildPage(0x00, serial, 1, 0, []byte{4}, []byte("cmnt"))...)
	b = append(b, buildPage(0x00, serial, 2, 540, []byte{10, 10}, make([]byte, 20))...)
	b = append(b, buildPage(0x04, serial, 3, 710, []byte{10}, make([]byte, 10))...)
	return b
}

func TestDecodeTrimsPadding(t *testing.T) {
	saveAvail, saveNew := Available, newDecoder
	defer func() { Available, newDecoder = saveAvail, saveNew }()
	Available = true

	for _, tc := range []struct {
		name     string
		channels int
		wantCh   int
	}{
		{"mono", 1, 1},
		{"stereo", 2, 2},
		{"bogus channel count decodes as mono", 0, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var gotMode, gotCh int
			newDecoder = func(mode, channels int) (frameDecoder, error) {
				gotMode, gotCh = mode, channels
				return &fakeDecoder{frameSize: 320, lookahead: 100, channels: channels}, nil
			}
			wav, err := DecodeToWAV(bytes.NewReader(spxStream(tc.channels)))
			if err != nil {
				t.Fatal(err)
			}
			if gotMode != 1 || gotCh != tc.wantCh {
				t.Fatalf("decoder built for mode %d, %d channels; want mode 1, %d channels", gotMode, gotCh, tc.wantCh)
			}

			le := binary.LittleEndian
			ch := int(le.Uint16(wav[22:24]))
			if ch != tc.wantCh {
				t.Errorf("WAV channels = %d, want %d", ch, tc.wantCh)
			}
			if got, want := le.Uint32(wav[24:28]), uint32(16000); got != want {
				t.Errorf("WAV rate = %d, want %d", got, want)
			}
			if got, want := le.Uint32(wav[28:32]), uint32(16000*ch*2); got != want {
				t.Errorf("WAV byte rate = %d, want %d", got, want)
			}
			if got, want := le.Uint16(wav[32:34]), uint16(ch*2); got != want {
				t.Errorf("WAV block align = %d, want %d", got, want)
			}

			// 710 frames: the final granule, per the stream above.
			pcm := make([]int16, le.Uint32(wav[40:44])/2)
			for i := range pcm {
				pcm[i] = int16(le.Uint16(wav[44+i*2:]))
			}
			if got, want := len(pcm), 710*ch; got != want {
				t.Fatalf("decoded %d samples, want %d", got, want)
			}
			// The fake numbers its samples, so the ends prove WHICH were kept:
			// 200 frames off the front, 50 off the back of 960.
			if got, want := pcm[0], int16(200*ch); got != want {
				t.Errorf("first sample = %d, want %d (leading padding not dropped)", got, want)
			}
			if got, want := pcm[len(pcm)-1], int16(910*ch-1); got != want {
				t.Errorf("last sample = %d, want %d (trailing padding not chopped)", got, want)
			}
		})
	}
}

// A stream whose granule positions are absurd must decode whole rather than be
// chopped on the strength of them.
func TestDecodeIgnoresImpossibleGranule(t *testing.T) {
	saveAvail, saveNew := Available, newDecoder
	defer func() { Available, newDecoder = saveAvail, saveNew }()
	Available = true
	newDecoder = func(mode, channels int) (frameDecoder, error) {
		return &fakeDecoder{frameSize: 320, lookahead: 100, channels: channels}, nil
	}

	const serial = 0x5150
	var b []byte
	b = append(b, buildPage(0x02, serial, 0, 0, []byte{80}, spxHeader(16000, 1, 1, 320, 1))...)
	b = append(b, buildPage(0x00, serial, 1, 0, []byte{4}, []byte("cmnt"))...)
	b = append(b, buildPage(0x04, serial, 2, 1<<62, []byte{10}, make([]byte, 10))...)

	wav, err := DecodeToWAV(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := binary.LittleEndian.Uint32(wav[40:44])/2, uint32(320); got != want {
		t.Fatalf("decoded %d samples, want the untrimmed %d", got, want)
	}
}
